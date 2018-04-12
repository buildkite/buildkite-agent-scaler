package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/buildkite/buildkite-agent-scaler/scaler/asg"
)

func handler(ctx context.Context, snsEvent events.SNSEvent) (string, error) {
	buildkiteOrgSlug := os.Getenv("BUILDKITE_ORG")
	buildkiteApiToken := os.Getenv("BUILDKITE_TOKEN")
	buildkiteQueue := os.Getenv("BUILDKITE_QUEUE")
	quiet := os.Getenv("BUILDKITE_QUIET")
	asgName := os.Getenv("BUILDKITE_ASG_NAME")

	if quiet == "1" || quiet == "false" {
		log.SetOutput(ioutil.Discard)
	}

	t := time.Now()

	scaler := asg.NewScaler(asg.Params{
		BuildkiteQueue:       buildkiteQueue,
		BuildkiteOrgSlug:     buildkiteOrgSlug,
		BuildkiteApiToken:    buildkiteApiToken,
		AutoScalingGroupName: asgName,
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
