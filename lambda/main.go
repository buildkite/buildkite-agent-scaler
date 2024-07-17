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

func mustGetEnv(env string) string {
	val := os.Getenv(env)
	if val == "" {
		log.Fatalf("Env %q not set", env)
	}
	return val
}

func mustGetEnvInt(env string) int {
	v := mustGetEnv(env)
	vi, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("Env %q is not an int: %v", env, v)
	}
	return vi
}

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

	if v := os.Getenv("LAMBDA_INTERVAL"); v != "" {
		if interval, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv("LAMBDA_TIMEOUT"); v != "" {
		if timeoutDuration, err := time.ParseDuration(v); err != nil {
			return "", err
		} else {
			timeout = time.After(timeoutDuration)
		}
	}

	if v := os.Getenv("ASG_ACTIVITY_TIMEOUT"); v != "" {
		if timeoutDuration, err := time.ParseDuration(v); err != nil {
			return "", err
		} else {
			asgActivityTimeoutDuration = timeoutDuration
		}
	}

	if v := os.Getenv("SCALE_IN_COOLDOWN_PERIOD"); v != "" {
		if scaleInCooldownPeriod, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv("SCALE_IN_FACTOR"); v != "" {
		if scaleInFactor, err = strconv.ParseFloat(v, 64); err != nil {
			return "", err
		}
		scaleInFactor = math.Abs(scaleInFactor)
	}

	if v := os.Getenv("SCALE_ONLY_AFTER_ALL_EVENT"); v != "" {
		if v == "true" || v == "1" {
			scaleOnlyAfterAllEvent = true
		}
	}

	if v := os.Getenv("SCALE_OUT_COOLDOWN_PERIOD"); v != "" {
		if scaleOutCooldownPeriod, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv("SCALE_OUT_FACTOR"); v != "" {
		if scaleOutFactor, err = strconv.ParseFloat(v, 64); err != nil {
			return "", err
		}
		scaleOutFactor = math.Abs(scaleOutFactor)
	}

	if v := os.Getenv("INCLUDE_WAITING"); v != "" {
		if v == "true" || v == "1" {
			includeWaiting = true
		}
	}

	if v := os.Getenv("INSTANCE_BUFFER"); v != "" {
		if instanceBuffer, err = strconv.Atoi(v); err != nil {
			return "", err
		}
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
			Name:                              mustGetEnv("ASG_NAME"),
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

		if scaleInOutput != nil {
			lastScaleIn = *scaleInOutput.StartTime
		}
		if scaleOutOutput != nil {
			lastScaleOut = *scaleOutOutput.StartTime
		}

		scalingTimeDiff := time.Since(scalingLastActivityStartTime)
		log.Printf("Succesfully retrieved last scaling activity events. Last scale out %v, last scale in %v. Discovery took %s.", lastScaleOut, lastScaleIn, scalingTimeDiff)
	}()

	token := os.Getenv("BUILDKITE_AGENT_TOKEN")
	ssmTokenKey := os.Getenv("BUILDKITE_AGENT_TOKEN_SSM_KEY")

	if ssmTokenKey != "" {
		var err error
		token, err = scaler.RetrieveFromParameterStore(sess, ssmTokenKey)
		if err != nil {
			return "", err
		}
	}

	if token == "" {
		return "", errors.New("Must provide either BUILDKITE_AGENT_TOKEN or BUILDKITE_AGENT_TOKEN_SSM_KEY")
	}

	client := buildkite.NewClient(token)
	params := scaler.Params{
		BuildkiteQueue:       mustGetEnv("BUILDKITE_QUEUE"),
		AutoScalingGroupName: mustGetEnv("ASG_NAME"),
		AgentsPerInstance:    mustGetEnvInt("AGENTS_PER_INSTANCE"),
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
		select {
		case <-timeout:
			return "", nil

		default:
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
			time.Sleep(interval)
		}
	}
}
