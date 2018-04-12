package main

import (
	"flag"
	"log"

	"github.com/buildkite/buildkite-agent-scaler/scaler/asg"
)

func main() {
	var (
		// aws params
		asgName           = flag.String("asg-name", "", "The name of the autoscaling group")
		agentsPerInstance = flag.Int("agents-per-instance", 1, "The number of agents per instance")

		// buildkite params
		buildkiteQueue    = flag.String("queue", "default", "The queue to watch in the metrics")
		buildkiteApiToken = flag.String("api-token", "", "A buildkite api token for metrics")
		buildkiteOrgSlug  = flag.String("org", "", "The buildkite organization slug")
	)
	flag.Parse()

	scaler := asg.NewScaler(asg.Params{
		BuildkiteQueue:       *buildkiteQueue,
		BuildkiteOrgSlug:     *buildkiteOrgSlug,
		BuildkiteApiToken:    *buildkiteApiToken,
		AutoScalingGroupName: *asgName,
		AgentsPerInstance:    *agentsPerInstance,
	})

	if err := scaler.Run(); err != nil {
		log.Fatal(err)
	}
}
