package main

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
)

func handler(ctx context.Context, snsEvent events.SNSEvent) (string, error) {
	org := os.Getenv("BUILDKITE_ORG")
	token := os.Getenv("BUILDKITE_TOKEN")
	queue := os.Getenv("BUILDKITE_QUEUE")
	quiet := os.Getenv("BUILDKITE_QUIET")
	cluster := os.Getenv("BUILDKITE_ECS_CLUSTER")
	service := os.Getenv("BUILDKITE_ECS_SERVICE")
	spotFleet := os.Getenv("BUILDKITE_SPOT_FLEET")

	if quiet == "1" || quiet == "false" {
		log.SetOutput(ioutil.Discard)
	}

	t := time.Now()

	err := scaler.Run(scaler.Params{
		ECSCluster:        cluster,
		ECSService:        service,
		BuildkiteQueue:    queue,
		BuildkiteOrg:      org,
		BuildkiteApiToken: token,
		SpotFleetID:       spotFleet,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Finished in %s", time.Now().Sub(t))
	return "", nil
}

func main() {
	// Make the handler available for Remote Procedure Call by AWS Lambda
	lambda.Start(handler)
}
