package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
	"github.com/buildkite/buildkite-agent-scaler/version"
)

// Stores the last time we scaled in/out in global lambda state
// On a cold start this will be reset to a zero value
var (
	lastScaleMu               sync.Mutex
	lastScaleIn, lastScaleOut time.Time
)

func main() {
	if os.Getenv("DEBUG") != "" {
		_, err := Handler(context.Background(), json.RawMessage([]byte{}))
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	lambda.Start(Handler)
}

func Handler(ctx context.Context, evt json.RawMessage) (string, error) {
	log.Printf("buildkite-agent-scaler version %s", version.VersionString())

	var (
		timeout  <-chan time.Time = make(chan time.Time)
		interval time.Duration    = 10 * time.Second

		asgActivityTimeoutDuration = 10 * time.Second

		scaleInCooldownPeriod time.Duration
		scaleInFactor         float64

		scaleOutCooldownPeriod time.Duration
		scaleOutFactor         float64

		instanceBuffer = 0

		scaleOnlyAfterAllEvent bool

		includeWaiting bool
		err            error

		publishCloudWatchMetrics        bool
		disableScaleOut, disableScaleIn bool
	)

	// Required environment variables
	buildkiteQueue := os.Getenv("BUILDKITE_QUEUE")
	if buildkiteQueue == "" {
		return "", errors.New("BUILDKITE_QUEUE is required")
	}

	asgName := os.Getenv("ASG_NAME")
	if asgName == "" {
		return "", errors.New("ASG_NAME is required")
	}

	agentsPerInstanceStr := os.Getenv("AGENTS_PER_INSTANCE")
	if agentsPerInstanceStr == "" {
		return "", errors.New("AGENTS_PER_INSTANCE is required")
	}
	agentsPerInstance, err := strconv.Atoi(agentsPerInstanceStr)
	if err != nil {
		return "", fmt.Errorf("AGENTS_PER_INSTANCE must be an integer: %w", err)
	}

	if v := os.Getenv("LAMBDA_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", err
		}
		interval = d
	}

	if v := os.Getenv("LAMBDA_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", err
		}
		timeout = time.After(d)
	}

	if v := os.Getenv("ASG_ACTIVITY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", err
		}
		asgActivityTimeoutDuration = d
	}

	if v := os.Getenv("SCALE_IN_COOLDOWN_PERIOD"); v != "" {
		p, err := time.ParseDuration(v)
		if err != nil {
			return "", err
		}
		scaleInCooldownPeriod = p
	}

	if v := os.Getenv("SCALE_IN_FACTOR"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", err
		}
		scaleInFactor = math.Abs(f)
	}

	if v := os.Getenv("SCALE_ONLY_AFTER_ALL_EVENT"); v != "" {
		scaleOnlyAfterAllEvent = v == "true" || v == "1"
	}

	if v := os.Getenv("SCALE_OUT_COOLDOWN_PERIOD"); v != "" {
		p, err := time.ParseDuration(v)
		if err != nil {
			return "", err
		}
		scaleOutCooldownPeriod = p
	}

	if v := os.Getenv("SCALE_OUT_FACTOR"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", err
		}
		scaleOutFactor = math.Abs(f)
	}

	if v := os.Getenv("INCLUDE_WAITING"); v != "" {
		includeWaiting = v == "true" || v == "1"
	}

	if v := os.Getenv("INSTANCE_BUFFER"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return "", err
		}
		instanceBuffer = i
	}

	maxDescribeScalingActivitiesPages := -1
	if v := os.Getenv("MAX_DESCRIBE_SCALING_ACTIVITIES_PAGES"); v != "" {
		maxDescribeScalingActivitiesPages, err = strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse MAX_DESCRIBE_SCALING_ACTIVITIES_PAGES: %v", err)
		}
	}

	if m := os.Getenv("CLOUDWATCH_METRICS"); m == "true" || m == "1" {
		log.Printf("Publishing cloudwatch metrics")
		publishCloudWatchMetrics = true
	}

	if m := os.Getenv("DISABLE_SCALE_IN"); m == "true" || m == "1" {
		log.Printf("Disabling scale-in 🙅🏼‍")
		disableScaleIn = true
	}

	if m := os.Getenv("DISABLE_SCALE_OUT"); m == "true" || m == "1" {
		log.Printf("Disabling scale-out 🙅🏼‍♂️")
		disableScaleOut = true
	}

	// establish an AWS session to be re-used
	sess, err := session.NewSession()
	if err != nil {
		return "", err
	}

	// get last scale in and out from asg's activities
	// This is wrapped in a mutex to avoid multiple outbound requests if the
	// lambda ever runs multiple times in parallel.
	func() {
		lastScaleMu.Lock()
		defer lastScaleMu.Unlock()

		if (disableScaleIn || !lastScaleIn.IsZero()) && (disableScaleOut || !lastScaleOut.IsZero()) {
			// We've already fetched the last scaling times that we need.
			return
		}

		asg := &scaler.ASGDriver{
			Name:                              asgName,
			Sess:                              sess,
			MaxDescribeScalingActivitiesPages: maxDescribeScalingActivitiesPages,
		}

		cctx, cancel := context.WithTimeout(ctx, asgActivityTimeoutDuration)
		defer cancel()

		scalingLastActivityStartTime := time.Now()
		scaleOutOutput, scaleInOutput, err := asg.GetLastScalingInAndOutActivity(cctx, !disableScaleOut, !disableScaleIn)
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Failed to retrieve last scaling activity events due to %v timeout", asgActivityTimeoutDuration)
			return
		}
		if err != nil { // Some other error.
			log.Printf("Encountered error when retrieving last scaling activities: %s", err)
			return
		}

		lastScaleInStr := "never"
		if scaleInOutput != nil && scaleInOutput.StartTime != nil {
			lastScaleIn = *scaleInOutput.StartTime
			lastScaleInStr = lastScaleIn.Format(time.RFC3339Nano)
		}
		lastScaleOutStr := "never"
		if scaleOutOutput != nil && scaleOutOutput.StartTime != nil {
			lastScaleOut = *scaleOutOutput.StartTime
			lastScaleOutStr = lastScaleOut.Format(time.RFC3339Nano)
		}

		scalingTimeDiff := time.Since(scalingLastActivityStartTime)
		log.Printf("Succesfully retrieved last scaling activity events. Last scale out %s, last scale in %s. Discovery took %s.", lastScaleOutStr, lastScaleInStr, scalingTimeDiff)
	}()

	token := os.Getenv("BUILDKITE_AGENT_TOKEN")
	ssmTokenKey := os.Getenv("BUILDKITE_AGENT_TOKEN_SSM_KEY")

	if ssmTokenKey != "" {
		tk, err := scaler.RetrieveFromParameterStore(sess, ssmTokenKey)
		if err != nil {
			return "", err
		}
		token = tk
	}

	if token == "" {
		return "", errors.New("Must provide either BUILDKITE_AGENT_TOKEN or BUILDKITE_AGENT_TOKEN_SSM_KEY")
	}

	client := buildkite.NewClient(token)
	params := scaler.Params{
		BuildkiteQueue:       buildkiteQueue,
		AutoScalingGroupName: asgName,
		AgentsPerInstance:    agentsPerInstance,
		IncludeWaiting:       includeWaiting,
		ScaleInParams: scaler.ScaleParams{
			CooldownPeriod: scaleInCooldownPeriod,
			Factor:         scaleInFactor,
			LastEvent:      lastScaleIn,
			Disable:        disableScaleIn,
		},
		ScaleOutParams: scaler.ScaleParams{
			CooldownPeriod: scaleOutCooldownPeriod,
			Factor:         scaleOutFactor,
			LastEvent:      lastScaleOut,
			Disable:        disableScaleOut,
		},
		InstanceBuffer:           instanceBuffer,
		ScaleOnlyAfterAllEvent:   scaleOnlyAfterAllEvent,
		PublishCloudWatchMetrics: publishCloudWatchMetrics,
	}

	scaler, err := scaler.NewScaler(client, sess, params)
	if err != nil {
		log.Fatalf("Couldn't create new scaler: %v", err)
	}

	for {
		minPollDuration, err := scaler.Run()
		if err != nil {
			log.Printf("Scaling error: %v", err)
		}

		if interval < minPollDuration {
			interval = minPollDuration
			log.Printf("Increasing poll interval to %v based on rate limit",
				interval)
		}

		// Persist the times back into the global state
		lastScaleIn = scaler.LastScaleIn()
		lastScaleOut = scaler.LastScaleOut()

		log.Printf("Waiting for %v\n", interval)
		select {
		case <-timeout:
			return "", nil
		case <-time.After(interval):
			// Continue
		}
	}
}
