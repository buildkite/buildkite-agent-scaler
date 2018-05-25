package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/buildkite/buildkite-agent-scaler/scaler/asg"
)

func handler(ctx context.Context, snsEvent events.SNSEvent) (string, error) {
	buildkiteAgentToken := os.Getenv("BUILDKITE_TOKEN")
	buildkiteQueue := os.Getenv("BUILDKITE_QUEUE")
	quiet := os.Getenv("BUILDKITE_QUIET")
	asgName := os.Getenv("BUILDKITE_ASG_NAME")

	agentsPerInstance, err := strconv.Atoi(os.Getenv("BUILDKITE_AGENTS_PER_INSTANCE"))
	if err != nil {
		return "", err
	}

	if quiet == "1" || quiet == "false" {
		log.SetOutput(ioutil.Discard)
	}

	t := time.Now()

	scaler := asg.NewScaler(asg.Params{
		BuildkiteQueue:       buildkiteQueue,
		BuildkiteAgentToken:  buildkiteAgentToken,
		AutoScalingGroupName: asgName,
		AgentsPerInstance:    agentsPerInstance,
	})

	if err := scaler.Run(); err != nil {
		log.Fatal(err)
	}

	log.Printf("Finished in %s", time.Now().Sub(t))
	return "", nil
}

func main() {
	// Make the handler available for Remote Procedure Call by AWS Lambda
	lambda.Start(handler)
}
