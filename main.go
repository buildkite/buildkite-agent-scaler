package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
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
		buildkiteAgentEndpoint = flag.String("agent-endpoint", "https://agent.buildkite.com/v3", "The buildkite agent API endpoint")
		buildkiteQueue         = flag.String("queue", "default", "The queue to watch in the metrics")
		buildkiteAgentToken    = flag.String("agent-token", "", "A buildkite agent registration token")
		includeWaiting         = flag.Bool("include-waiting", false, "Whether to include jobs behind a wait step for scaling")

		// scale in/out params
		scaleInFactor    = flag.Float64("scale-in-factor", 1.0, "A factor to apply to scale ins")
		scaleOutFactor   = flag.Float64("scale-out-factor", 1.0, "A factor to apply to scale outs")
		scaleInCooldown  = flag.Duration("scale-in-cooldown", 1*time.Hour, "How long to wait between scale in events")
		scaleOutCooldown = flag.Duration("scale-out-cooldown", 0, "How long to wait between scale out events")
		instanceBuffer   = flag.Int("instance-buffer", 0, "Keep this many instances as extra capacity")

		// general params
		dryRun                      = flag.Bool("dry-run", false, "Whether to just show what would be done")
		elasticCIMode               = flag.Bool("elastic-ci-mode", false, "Whether to enable Elastic CI mode with additional safety checks")
		minimumInstanceUptime       = flag.Duration("minimum-instance-uptime", 1*time.Hour, "Minimum instance uptime before being eligible for dangling instance check")
		maxDanglingInstancesToCheck = flag.Int("max-dangling-instances-to-check", 5, "Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)")
	)
	flag.Parse()

	// establish an AWS session to be re-used
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal("unable to load SDK config, ", err)
	}

	if *ssmTokenKey != "" {
		token, err := scaler.RetrieveFromParameterStore(cfg, *ssmTokenKey)
		if err != nil {
			log.Fatal(err)
		}
		buildkiteAgentToken = &token
	}

	client := buildkite.NewClient(*buildkiteAgentToken, *buildkiteAgentEndpoint)

	scaler, err := scaler.NewScaler(client, cfg, scaler.Params{
		BuildkiteQueue:           *buildkiteQueue,
		AutoScalingGroupName:     *asgName,
		AgentsPerInstance:        *agentsPerInstance,
		PublishCloudWatchMetrics: *cwMetrics,
		DryRun:                   *dryRun,
		IncludeWaiting:           *includeWaiting,
		ScaleInParams: scaler.ScaleParams{
			Factor:         *scaleInFactor,
			CooldownPeriod: *scaleInCooldown,
		},
		ScaleOutParams: scaler.ScaleParams{
			Factor:         *scaleOutFactor,
			CooldownPeriod: *scaleOutCooldown,
		},
		InstanceBuffer:              *instanceBuffer,
		ElasticCIMode:               *elasticCIMode,
		MinimumInstanceUptime:       *minimumInstanceUptime,
		MaxDanglingInstancesToCheck: *maxDanglingInstancesToCheck,
	})
	if err != nil {
		log.Fatal(err)
	}

	if *dryRun {
		log.Printf("Running as a dry-run, no changes will be made")
	}

	var interval = 10 * time.Second

	for {
		minPollDuration, err := scaler.Run()
		if err != nil {
			log.Fatal(err)
		}

		if interval < minPollDuration {
			interval = minPollDuration
			log.Printf("Increasing poll interval to %v based on rate limit", interval)
		}

		log.Printf("Waiting for %v", interval)
		log.Println("")
		time.Sleep(interval)
	}
}
