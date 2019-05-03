package scaler

import (
	"log"
	"math"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScaleInParams struct {
	Disable         bool
	CooldownPeriod  time.Duration
	Adjustment      int64
	LastScaleInTime *time.Time
}

type Params struct {
	AutoScalingGroupName     string
	AgentsPerInstance        int
	BuildkiteAgentToken      string
	BuildkiteQueue           string
	UserAgent                string
	PublishCloudWatchMetrics bool
	DryRun                   bool
	ScaleInParams            ScaleInParams
}

type Scaler struct {
	autoscaling interface {
		Describe() (AutoscaleGroupDetails, error)
		SetDesiredCapacity(count int64) error
	}
	bk interface {
		GetAgentMetrics() (buildkite.AgentMetrics, error)
	}
	metrics interface {
		Publish(orgSlug, queue string, metrics map[string]int64) error
	}
	agentsPerInstance int
	scaleInParams     ScaleInParams
}

func NewScaler(client *buildkite.Client, params Params) (*Scaler, error) {
	scaler := &Scaler{
		bk: &buildkiteDriver{
			client: client,
			queue:  params.BuildkiteQueue,
		},
		agentsPerInstance: params.AgentsPerInstance,
		scaleInParams:     params.ScaleInParams,
	}

	if params.DryRun {
		scaler.autoscaling = &dryRunASG{}

		if params.PublishCloudWatchMetrics {
			scaler.metrics = &dryRunMetricsPublisher{}
		}
	} else {
		scaler.autoscaling = &asgDriver{
			name: params.AutoScalingGroupName,
		}

		if params.PublishCloudWatchMetrics {
			scaler.metrics = &cloudWatchMetricsPublisher{}
		}
	}

	return scaler, nil
}

func (s *Scaler) Run() (time.Duration, error) {
	metrics, err := s.bk.GetAgentMetrics()
	if err != nil {
		return metrics.PollDuration, err
	}

	if s.metrics != nil {
		err = s.metrics.Publish(metrics.OrgSlug, metrics.Queue, map[string]int64{
			`ScheduledJobsCount`: metrics.ScheduledJobs,
			`RunningJobsCount`:   metrics.RunningJobs,
		})
		if err != nil {
			return metrics.PollDuration, err
		}
	}

	count := metrics.ScheduledJobs + metrics.RunningJobs

	var desired int64
	if count > 0 {
		desired = int64(math.Ceil(float64(count) / float64(s.agentsPerInstance)))
	}

	current, err := s.autoscaling.Describe()
	if err != nil {
		return metrics.PollDuration, err
	}

	if desired > current.MaxSize {
		log.Printf("⚠️  Desired count exceed MaxSize, capping at %d", current.MaxSize)
		desired = current.MaxSize
	} else if desired < current.MinSize {
		log.Printf("⚠️  Desired count is less than MinSize, capping at %d", current.MinSize)
		desired = current.MinSize
	}

	t := time.Now()

	if desired > current.DesiredCount {
		log.Printf("Scaling OUT 📈 to %d instances (currently %d)", desired, current.DesiredCount)

		err = s.autoscaling.SetDesiredCapacity(desired)
		if err != nil {
			return metrics.PollDuration, err
		}

		log.Printf("↳ Set desired to %d (took %v)", desired, time.Now().Sub(t))
	} else if current.DesiredCount > desired {
		if s.scaleInParams.Disable {
			log.Printf("Skipping scale IN, disabled")
			return metrics.PollDuration, nil
		}

		cooldownRemaining := s.scaleInParams.CooldownPeriod - time.Since(*s.scaleInParams.LastScaleInTime)

		if cooldownRemaining > 0 {
			log.Printf("⏲ Want to scale IN but in cooldown for %d seconds", cooldownRemaining/time.Second)
			return metrics.PollDuration, nil
		}

		minimumDesired := current.DesiredCount + s.scaleInParams.Adjustment
		if desired < minimumDesired {
			desired = minimumDesired
		}

		log.Printf("Scaling IN 📉 to %d instances (currently %d)", desired, current.DesiredCount)

		err = s.autoscaling.SetDesiredCapacity(desired)
		if err != nil {
			return metrics.PollDuration, err
		}

		*s.scaleInParams.LastScaleInTime = time.Now()
		log.Printf("↳ Set desired to %d (took %v)", desired, time.Now().Sub(t))
	} else {
		log.Printf("No scaling required, currently %d", current.DesiredCount)
	}

	return metrics.PollDuration, nil
}

type buildkiteDriver struct {
	client *buildkite.Client
	queue  string
}

func (a *buildkiteDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return a.client.GetAgentMetrics(a.queue)
}
