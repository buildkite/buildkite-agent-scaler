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
		ssmTokenKey       = flag.String("agent-token-ssm-key", "", "The AWS SSM Parameter Store key for the agent token")

		// buildkite params
		buildkiteQueue      = flag.String("queue", "default", "The queue to watch in the metrics")
		buildkiteAgentToken = flag.String("agent-token", "", "A buildkite agent registration token")

		// scale in params
		scaleInAdjustment = flag.Int64("scale-in-adjustment", -1, "Maximum adjustment to the desired capacity on scale in")

		// general params
		dryRun = flag.Bool("dry-run", false, "Whether to just show what would be done")
	)
	flag.Parse()

	if *ssmTokenKey != "" {
		token, err := scaler.RetrieveFromParameterStore(*ssmTokenKey)
		if err != nil {
			log.Fatal(err)
		}
		buildkiteAgentToken = &token
	}

	client := buildkite.NewClient(*buildkiteAgentToken)

	scaler, err := scaler.NewScaler(client, scaler.Params{
		BuildkiteQueue:           *buildkiteQueue,
		AutoScalingGroupName:     *asgName,
		AgentsPerInstance:        *agentsPerInstance,
		PublishCloudWatchMetrics: *cwMetrics,
		DryRun:                   *dryRun,
		ScaleInParams: scaler.ScaleInParams{
			// We run in one-shot so cooldown isn't implemented
			CooldownPeriod:  time.Duration(0),
			Adjustment:      *scaleInAdjustment,
			LastScaleInTime: &time.Time{},
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
