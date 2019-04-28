package main

import (
	"flag"
	"log"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
)

func main() {
	var (
		// aws params
		asgName           = flag.String("asg-name", "", "The name of the autoscaling group")
		agentsPerInstance = flag.Int("agents-per-instance", 1, "The number of agents per instance")
		cwMetrics         = flag.Bool("cloudwatch-metrics", false, "Whether to publish cloudwatch metrics")

		// buildkite params
		buildkiteQueue      = flag.String("queue", "default", "The queue to watch in the metrics")
		buildkiteAgentToken = flag.String("agent-token", "", "A buildkite agent registration token")

		// scale in/out params
		scaleInMinAdjustment  = flag.Int64("scale-in-min", 0, "A lower bound for negative desired count changes")
		scaleInMaxAdjustment  = flag.Int64("scale-in-max", 0, "An upper bound for negative desired count changes")
		scaleOutMinAdjustment = flag.Int64("scale-out-min", 0, "A lower bound for positive desired count changes")
		scaleOutMaxAdjustment = flag.Int64("scale-out-max", 0, "An upper bound for positive desired count changes")

		// general params
		dryRun = flag.Bool("dry-run", false, "Whether to just show what would be done")
	)
	flag.Parse()

	client := buildkite.NewClient(*buildkiteAgentToken)

	scaler, err := scaler.NewScaler(client, scaler.Params{
		BuildkiteQueue:           *buildkiteQueue,
		AutoScalingGroupName:     *asgName,
		AgentsPerInstance:        *agentsPerInstance,
		PublishCloudWatchMetrics: *cwMetrics,
		DryRun:                   *dryRun,
		ScaleInParams: scaler.ScaleInParams{
			MinAdjustment: *scaleInMinAdjustment,
			MaxAdjustment: *scaleInMaxAdjustment,
		},
		ScaleOutParams: scaler.ScaleOutParams{
			MinAdjustment: *scaleOutMinAdjustment,
			MaxAdjustment: *scaleOutMaxAdjustment,
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if *dryRun {
		log.Printf("Running as a dry-run, no changes will be made")
	}

	if err := scaler.Run(); err != nil {
		log.Fatal(err)
	}
}
