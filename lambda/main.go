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
	"github.com/buildkite/buildkite-agent-scaler/metrics"
	"github.com/buildkite/buildkite-agent-scaler/scaler/asg"
)

var (
	Version string = "dev"
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
	log.Printf("buildkite-agent-scaler version %s", Version)

	var timeout <-chan time.Time = make(chan time.Time)
	var interval time.Duration = 10 * time.Second

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
			var p *metrics.CloudWatchPublisher

			if m := os.Getenv(`CLOUDWATCH_METRICS`); m == `true` || m == `1` {
				log.Printf("Publishing cloudwatch metrics")
				p = metrics.NewCloudWatchPublisher()
			}

			scaler := asg.NewScaler(asg.Params{
				BuildkiteQueue:       mustGetEnv(`BUILDKITE_QUEUE`),
				BuildkiteAgentToken:  mustGetEnv(`BUILDKITE_AGENT_TOKEN`),
				AutoScalingGroupName: mustGetEnv(`ASG_NAME`),
				AgentsPerInstance:    mustGetEnvInt(`AGENTS_PER_INSTANCE`),
				CloudWatchPublisher:  p,
			})

			if err := scaler.Run(); err != nil {
				log.Printf("Scaling error: %v", err)
			}

			log.Printf("Waiting for %v", interval)
			time.Sleep(interval)
		}
	}
}
