package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
	"github.com/buildkite/buildkite-agent-scaler/version"
)

var invokeCount = 0

// lastScaleDownTime stores the last time we scaled down the ASG
// On a cold start this will be reset to Jan 1st, 1970
var lastScaleInTime time.Time

func main() {
	if os.Getenv(`DEBUG`) != "" {
		_, err := Handler(context.Background(), json.RawMessage([]byte{}))
		if err != nil {
			log.Fatal(err)
		}
	} else {
		invokeCount = invokeCount + 1
		log.Printf("Invocation count %d", invokeCount)
		lambda.Start(Handler)
	}
}

func Handler(ctx context.Context, evt json.RawMessage) (string, error) {
	log.Printf("buildkite-agent-scaler version %s", version.VersionString())

	var timeout <-chan time.Time = make(chan time.Time)
	var interval time.Duration = 10 * time.Second
	var scaleInCooldownPeriod time.Duration = 5 * time.Minute
	var scaleInAdjustment int64 = -1

	if intervalStr := os.Getenv(`LAMBDA_INTERVAL`); intervalStr != "" {
		var err error
		interval, err = time.ParseDuration(intervalStr)
		if err != nil {
			return "", err
		}
	}

	if timeoutStr := os.Getenv(`LAMBDA_TIMEOUT`); timeoutStr != "" {
		timeoutDuration, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return "", err
		}
		timeout = time.After(timeoutDuration)
	}

	if scaleInCooldownPeriodStr := os.Getenv(`SCALE_IN_COOLDOWN_PERIOD`); scaleInCooldownPeriodStr != "" {
		var err error
		scaleInCooldownPeriod, err = time.ParseDuration(scaleInCooldownPeriodStr)
		if err != nil {
			return "", err
		}
	}

	if scaleInAdjustmentStr := os.Getenv(`SCALE_IN_ADJUSTMENT`); scaleInAdjustmentStr != "" {
		var err error
		scaleInAdjustment, err = strconv.ParseInt(scaleInAdjustmentStr, 10, 64)
		if err != nil {
			return "", err
		}

		if scaleInAdjustment >= 0 {
			panic(fmt.Sprintf("Scale in adjustment (%d) must be negative", scaleInAdjustment))
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

			client := buildkite.NewClient(token)

			params := scaler.Params{
				BuildkiteQueue:       mustGetEnv(`BUILDKITE_QUEUE`),
				AutoScalingGroupName: mustGetEnv(`ASG_NAME`),
				AgentsPerInstance:    mustGetEnvInt(`AGENTS_PER_INSTANCE`),
				ScaleInParams: scaler.ScaleInParams{
					CooldownPeriod:  scaleInCooldownPeriod,
					Adjustment:      scaleInAdjustment,
					LastScaleInTime: &lastScaleInTime,
				},
			}

			if m := os.Getenv(`CLOUDWATCH_METRICS`); m == `true` || m == `1` {
				log.Printf("Publishing cloudwatch metrics")
				params.PublishCloudWatchMetrics = true
			}

			if m := os.Getenv(`DISABLE_SCALE_IN`); m == `true` || m == `1` {
				log.Printf("Disabling scale-in")
				params.ScaleInParams.Disable = true
			}

			scaler, err := scaler.NewScaler(client, params)
			if err != nil {
				log.Fatal(err)
			}

			if err := scaler.Run(); err != nil {
				log.Printf("Scaling error: %v", err)
			}

			log.Printf("Waiting for %v", interval)
			time.Sleep(interval)
		}
	}
}
