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
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
	"github.com/buildkite/buildkite-agent-scaler/version"
)

// Stores the last time we scaled in/out in global lambda state
// On a cold start this will be reset to a zero value
var (
	lastScaleIn, lastScaleOut time.Time
)

func main() {
	if os.Getenv(`DEBUG`) != "" {
		_, err := Handler(context.Background(), json.RawMessage([]byte{}))
		if err != nil {
			log.Fatal(err)
		}
	} else {
		lambda.Start(Handler)
	}
}

func Handler(ctx context.Context, evt json.RawMessage) (string, error) {
	log.Printf("buildkite-agent-scaler version %s", version.VersionString())

	var (
		timeout  <-chan time.Time = make(chan time.Time)
		interval time.Duration    = 10 * time.Second

		scaleInCooldownPeriod time.Duration
		scaleInFactor         float64

		scaleOutCooldownPeriod time.Duration
		scaleOutFactor         float64

		includeWaiting bool
		err            error
	)

	if v := os.Getenv(`LAMBDA_INTERVAL`); v != "" {
		if interval, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv(`LAMBDA_TIMEOUT`); v != "" {
		if timeoutDuration, err := time.ParseDuration(v); err != nil {
			return "", err
		} else {
			timeout = time.After(timeoutDuration)
		}
	}

	if v := os.Getenv(`SCALE_IN_COOLDOWN_PERIOD`); v != "" {
		if scaleInCooldownPeriod, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv(`SCALE_IN_FACTOR`); v != "" {
		if scaleInFactor, err = strconv.ParseFloat(v, 64); err != nil {
			return "", err
		}
		scaleInFactor = math.Abs(scaleInFactor)
	}

	if v := os.Getenv(`SCALE_OUT_COOLDOWN_PERIOD`); v != "" {
		if scaleOutCooldownPeriod, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv(`SCALE_OUT_FACTOR`); v != "" {
		if scaleOutFactor, err = strconv.ParseFloat(v, 64); err != nil {
			return "", err
		}
		scaleOutFactor = math.Abs(scaleOutFactor)
	}

	if v := os.Getenv(`INCLUDE_WAITING`); v != "" {
		if v == "true" || v == "1" {
			includeWaiting = true
		}
	}

	var mustGetEnv = func(env string) string {
		val := os.Getenv(env)
		if val == "" {
			panic(fmt.Sprintf("Env %q not set", env))
		}
		return val
	}

	var mustGetEnvInt = func(env string) int {
		v := mustGetEnv(env)
		vi, err := strconv.Atoi(v)
		if err != nil {
			panic(fmt.Sprintf("Env %q is not an int: %v", env, v))
		}
		return vi
	}

	for {
		select {
		case <-timeout:
			return "", nil
		default:
			token := os.Getenv(`BUILDKITE_AGENT_TOKEN`)
			ssmTokenKey := os.Getenv("BUILDKITE_AGENT_TOKEN_SSM_KEY")
			queue := os.Getenv("BUILDKITE_QUEUE")
			queues := os.Getenv("BUILDKITE_QUEUES")

			if ssmTokenKey != "" {
				var err error
				token, err = scaler.RetrieveFromParameterStore(ssmTokenKey)
				if err != nil {
					return "", err
				}
			}

			if token == "" {
				return "", errors.New(
					"Must provide either BUILDKITE_AGENT_TOKEN or BUILDKITE_AGENT_TOKEN_SSM_KEY",
				)
			}

			// merge values for BUILDKITE_QUEUE and BUILDKITE_QUEUES into a single list of unique values
			queuesSet := map[string]bool{}
			if queue != "" {
				queuesSet[queue] = true
			}
			if queues != "" {
				queuesPcs := strings.Split(queues, ",")
				for _, queue := range queuesPcs {
					queuesSet[queue] = true
				}
			}
			buildkiteQueues := make([]string, len(queuesSet))
			idx := 0
			for q := range queuesSet {
				buildkiteQueues[idx] = q
				idx++
			}

			client := buildkite.NewClient(token)

			params := scaler.Params{
				BuildkiteQueues:      buildkiteQueues,
				AutoScalingGroupName: mustGetEnv(`ASG_NAME`),
				AgentsPerInstance:    mustGetEnvInt(`AGENTS_PER_INSTANCE`),
				IncludeWaiting:       includeWaiting,
				ScaleInParams: scaler.ScaleParams{
					CooldownPeriod: scaleInCooldownPeriod,
					Factor:         scaleInFactor,
					LastEvent:      lastScaleIn,
				},
				ScaleOutParams: scaler.ScaleParams{
					CooldownPeriod: scaleOutCooldownPeriod,
					Factor:         scaleOutFactor,
					LastEvent:      lastScaleOut,
				},
			}

			if m := os.Getenv(`CLOUDWATCH_METRICS`); m == `true` || m == `1` {
				log.Printf("Publishing cloudwatch metrics")
				params.PublishCloudWatchMetrics = true
			}

			if m := os.Getenv(`DISABLE_SCALE_IN`); m == `true` || m == `1` {
				log.Printf("Disabling scale-in 🙅🏼‍")
				params.ScaleInParams.Disable = true
			}

			if m := os.Getenv(`DISABLE_SCALE_OUT`); m == `true` || m == `1` {
				log.Printf("Disabling scale-out 🙅🏼‍♂️")
				params.ScaleOutParams.Disable = true
			}

			scaler, err := scaler.NewScaler(client, params)
			if err != nil {
				log.Fatal(err)
			}

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

			log.Printf("Waiting for %v", interval)
			time.Sleep(interval)
		}
	}
}
