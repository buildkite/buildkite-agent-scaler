package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/buildkite/buildkite-ecs-agent-scaler/scaler"
	"github.com/eawsy/aws-lambda-go/service/lambda/runtime"
)

func handle(evt json.RawMessage, ctx *runtime.Context) (interface{}, error) {
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

func init() {
	runtime.HandleFunc(handle)
}
