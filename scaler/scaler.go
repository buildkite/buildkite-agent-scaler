package scaler

import (
	"log"
	"math"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type Params struct {
	AutoScalingGroupName     string
	AgentsPerInstance        int
	BuildkiteAgentToken      string
	BuildkiteQueue           string
	UserAgent                string
	PublishCloudWatchMetrics bool
	DryRun                   bool
}

type Scaler struct {
	autoscaling interface {
		Describe() (AutoscaleGroupDetails, error)
		SetDesiredCapacity(count int64) error
	}
	bk interface {
		GetScheduledJobCount() (int64, error)
	}
	metrics interface {
		Publish(metrics map[string]int64) error
	}
	agentsPerInstance int
}

func NewScaler(bk *buildkite.Client, params Params) (*Scaler, error) {
	scaler := &Scaler{
		bk: &buildkiteDriver{
			agentToken: params.BuildkiteAgentToken,
			queue:      params.BuildkiteQueue,
		},
		agentsPerInstance: params.AgentsPerInstance,
	}

	orgSlug, err := bk.GetOrgSlug()
	if err != nil {
		return nil, err
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
			scaler.metrics = &cloudWatchMetricsPublisher{
				OrgSlug: orgSlug,
				Queue:   params.BuildkiteQueue,
			}
		}

	}

	return scaler, nil
}

func (s *Scaler) Run() error {
	count, err := s.bk.GetScheduledJobCount()
	if err != nil {
		return err
	}

	if s.metrics != nil {
		err = s.metrics.Publish(map[string]int64{
			`ScheduledJobsCount`: count,
		})
		if err != nil {
			return err
		}
	}

	var desired int64
	if count > 0 {
		desired = int64(math.Ceil(float64(count) / float64(s.agentsPerInstance)))
	}

	current, err := s.autoscaling.Describe()
	if err != nil {
		return err
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
			return err
		}

		log.Printf("↳ Set desired to %d (took %v)", desired, time.Now().Sub(t))
	} else if current.DesiredCount > desired {
		log.Printf("Scaling IN 📉 to %d instances (currently %d)", desired, current.DesiredCount)

		err = s.autoscaling.SetDesiredCapacity(desired)
		if err != nil {
			return err
		}

		log.Printf("↳ Set desired to %d (took %v)", desired, time.Now().Sub(t))
	} else {
		log.Printf("No scaling required, currently %d", current.DesiredCount)
	}

	return nil
}

type buildkiteDriver struct {
	agentToken string
	queue      string
}

func (a *buildkiteDriver) GetScheduledJobCount() (int64, error) {
	return buildkite.NewClient(a.agentToken).GetScheduledJobCount(a.queue)
}
