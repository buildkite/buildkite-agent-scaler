package main

import (
	"flag"
	"log"

	"github.com/buildkite/buildkite-ecs-agent-scaler/scaler"
)

func main() {
	// aws params
	var agentCluster = flag.String("cluster", "", "The ECS cluster to scale")
	var agentService = flag.String("service", "", "The ECS service to scale")
	var queue = flag.String("queue", "default", "The queue to watch in the metrics")
	var spotFleet = flag.String("spot-fleet", "", "The spot fleet to adjust")

	// buildkite params
	var buildkiteApiToken = flag.String("api-token", "", "A buildkite api token for metrics")
	var buildkiteOrg = flag.String("org", "", "The buildkite organization slug")
	flag.Parse()

	err := scaler.Run(scaler.Params{
		ECSCluster:        *agentCluster,
		ECSService:        *agentService,
		BuildkiteQueue:    *queue,
		BuildkiteOrg:      *buildkiteOrg,
		BuildkiteApiToken: *buildkiteApiToken,
		SpotFleetID:       *spotFleet,
	})
	if err != nil {
		log.Fatal(err)
	}
}
