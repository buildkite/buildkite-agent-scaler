package main

import (
	"flag"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
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
		includeWaiting      = flag.Bool("include-waiting", false, "Whether to include jobs behind a wait step for scaling")

		// scale in/out params
		scaleInFactor  = flag.Float64("scale-in-factor", 1.0, "A factor to apply to scale ins")
		scaleOutFactor = flag.Float64("scale-out-factor", 1.0, "A factor to apply to scale outs")

		// general params
		dryRun = flag.Bool("dry-run", false, "Whether to just show what would be done")
	)
	flag.Parse()

	// establish an AWS session to be re-used
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	if *ssmTokenKey != "" {
		token, err := scaler.RetrieveFromParameterStore(sess, *ssmTokenKey)
		if err != nil {
			log.Fatal(err)
		}
		buildkiteAgentToken = &token
	}

	client := buildkite.NewClient(*buildkiteAgentToken)

	scaler, err := scaler.NewScaler(client, sess, scaler.Params{
		BuildkiteQueue:           *buildkiteQueue,
		AutoScalingGroupName:     *asgName,
		AgentsPerInstance:        *agentsPerInstance,
		PublishCloudWatchMetrics: *cwMetrics,
		DryRun:                   *dryRun,
		IncludeWaiting:           *includeWaiting,
		ScaleInParams:            scaler.ScaleParams{Factor: *scaleInFactor},
		ScaleOutParams:           scaler.ScaleParams{Factor: *scaleOutFactor},
	})
	if err != nil {
		log.Fatal(err)
	}

	if *dryRun {
		log.Printf("Running as a dry-run, no changes will be made")
	}

	var interval time.Duration = 10 * time.Second

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
