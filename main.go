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
		buildkiteQueue      = flag.String("queue", "default", "The queue to watch in the metrics")
		buildkiteAgentToken = flag.String("agent-token", "", "A buildkite agent registration token")

		// general params
		dryRun = flag.Bool("dry-run", false, "Whether to just show what would be done")
	)
	flag.Parse()

	scaler := asg.NewScaler(asg.Params{
		BuildkiteQueue:       *buildkiteQueue,
		BuildkiteAgentToken:  *buildkiteAgentToken,
		AutoScalingGroupName: *asgName,
		AgentsPerInstance:    *agentsPerInstance,
	})

	if *dryRun {
		log.Printf("Running as a dry-run, no changes will be made")
		scaler.ASG = &asg.DryRunASG{}
	}

	if err := scaler.Run(); err != nil {
		log.Fatal(err)
	}
}
