package main

import (
	"context"
	"encoding/json"
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

		scaleInCooldownPeriod  time.Duration
		scaleInMax, scaleInMin int64

		scaleOutCooldownPeriod   time.Duration
		scaleOutMax, scaleOutMin int64

		err error
	)

	if v := os.Getenv(`LAMBDA_INTERVAL`); v != "" {
		if interval, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv(`LAMBDA_INTERVAL`); v != "" {
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

	if v := os.Getenv(`SCALE_IN_MAX`); v != "" {
		if scaleInMax, err = strconv.ParseInt(v, 10, 64); err != nil {
			return "", err
		}
		if scaleInMax >= 0 {
			panic(fmt.Sprintf("SCALE_IN_MAX (%d) must be negative", scaleInMax))
		}
	}

	if v := os.Getenv(`SCALE_IN_MIN`); v != "" {
		if scaleInMin, err = strconv.ParseInt(v, 10, 64); err != nil {
			return "", err
		}
		if scaleInMax >= 0 {
			panic(fmt.Sprintf("SCALE_IN_MIN (%d) must be negative", scaleInMax))
		}
	}

	if v := os.Getenv(`SCALE_OUT_COOLDOWN_PERIOD`); v != "" {
		if scaleOutCooldownPeriod, err = time.ParseDuration(v); err != nil {
			return "", err
		}
	}

	if v := os.Getenv(`SCALE_OUT_MAX`); v != "" {
		if scaleOutMax, err = strconv.ParseInt(v, 10, 64); err != nil {
			return "", err
		}
		if scaleOutMax < 0 {
			panic(fmt.Sprintf("SCALE_OUT_MAX (%d) must be positive", scaleOutMax))
		}
	}

	if v := os.Getenv(`SCALE_OUT_MIN`); v != "" {
		if scaleOutMin, err = strconv.ParseInt(v, 10, 64); err != nil {
			return "", err
		}
		if scaleOutMin < 0 {
			panic(fmt.Sprintf("SCALE_OUT_MIN (%d) must be positive", scaleOutMin))
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
			client := buildkite.NewClient(mustGetEnv(`BUILDKITE_AGENT_TOKEN`))

			params := scaler.Params{
				BuildkiteQueue:       mustGetEnv(`BUILDKITE_QUEUE`),
				AutoScalingGroupName: mustGetEnv(`ASG_NAME`),
				AgentsPerInstance:    mustGetEnvInt(`AGENTS_PER_INSTANCE`),
				ScaleInParams: scaler.ScaleInParams{
					CooldownPeriod: scaleInCooldownPeriod,
					MaxAdjustment:  scaleInMax,
					MinAdjustment:  scaleInMin,
					LastScaleIn:    lastScaleIn,
				},
				ScaleOutParams: scaler.ScaleOutParams{
					CooldownPeriod: scaleOutCooldownPeriod,
					MaxAdjustment:  scaleOutMax,
					MinAdjustment:  scaleOutMin,
					LastScaleOut:   lastScaleOut,
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

			// Persist the times back into the global state
			lastScaleIn = scaler.LastScaleIn()
			lastScaleOut = scaler.LastScaleOut()

			log.Printf("Waiting for %v", interval)
			time.Sleep(interval)
		}
	}
}
