package scaler

import (
	"log"
	"math"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScaleParams struct {
	Disable        bool
	CooldownPeriod time.Duration
	Factor         float64
	LastEvent      time.Time
}

type Params struct {
	AutoScalingGroupName     string
	AgentsPerInstance        int
	BuildkiteAgentToken      string
	BuildkiteQueues          []string
	UserAgent                string
	PublishCloudWatchMetrics bool
	DryRun                   bool
	IncludeWaiting           bool
	ScaleInParams            ScaleParams
	ScaleOutParams           ScaleParams
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
	includeWaiting    bool
	agentsPerInstance int
	scaleInParams     ScaleParams
	scaleOutParams    ScaleParams
}

func NewScaler(client *buildkite.Client, params Params) (*Scaler, error) {
	scaler := &Scaler{
		bk: &buildkiteDriver{
			client: client,
			queues: params.BuildkiteQueues,
		},
		includeWaiting:    params.IncludeWaiting,
		agentsPerInstance: params.AgentsPerInstance,
		scaleInParams:     params.ScaleInParams,
		scaleOutParams:    params.ScaleOutParams,
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
		for queue, queueMetrics := range metrics.Queues {
			err = s.metrics.Publish(metrics.OrgSlug, queue, map[string]int64{
				`ScheduledJobsCount`: queueMetrics.ScheduledJobs,
				`RunningJobsCount`:   queueMetrics.RunningJobs,
				`WaitingJobsCount`:   queueMetrics.WaitingJobs,
			})
			if err != nil {
				return metrics.PollDuration, err
			}
		}
	}

	var count int64
	for _, metrics := range metrics.Queues {
		count += metrics.ScheduledJobs

		// If waiting jobs are greater than running jobs then optionally
		// use waiting jobs for scaling so that we have instances booted
		// by the time we get to them. This is a gamble, as if the instances
		// scale down before the jobs get scheduled, it's a huge waste.
		if s.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
			count += metrics.WaitingJobs
		} else {
			count += metrics.RunningJobs
		}
	}

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

	if desired > current.DesiredCount {
		return metrics.PollDuration, s.scaleOut(desired, current)
	}

	if current.DesiredCount > desired {
		return metrics.PollDuration, s.scaleIn(desired, current)
	}

	log.Printf("No scaling required, currently %d", current.DesiredCount)
	return metrics.PollDuration, nil
}

func (s *Scaler) scaleIn(desired int64, current AutoscaleGroupDetails) error {
	if s.scaleInParams.Disable {
		return nil
	}

	// If we've scaled down before, check if a cooldown should be enforced
	if !s.scaleInParams.LastEvent.IsZero() {
		cooldownRemaining := s.scaleInParams.CooldownPeriod - time.Since(s.scaleInParams.LastEvent)

		if cooldownRemaining > 0 {
			log.Printf("⏲ Want to scale IN but in cooldown for %d seconds", cooldownRemaining/time.Second)
			return nil
		}
	}

	// Calculate the change in the desired count, will be negative
	change := desired - current.DesiredCount

	// Apply scaling factor if one is given
	if factor := s.scaleInParams.Factor; factor != 0 {
		// Use Floor to avoid never reaching upper bound
		factoredChange := int64(math.Floor(float64(change) * factor))

		if factoredChange < change {
			log.Printf("👮‍️ Increasing scale-in of %d by factor of %0.2f",
				change, factor)
		} else if factoredChange > change {
			log.Printf("👮‍️ Decreasing scale-in of %d by factor of %0.2f",
				change, factor)
		} else {
			log.Printf("👮‍️ Scale-in factor of %0.2f was ignored",
				factor)
		}

		desired = current.DesiredCount + factoredChange
	}

	// Correct negative values if we get them
	if desired < 0 {
		desired = 0
	}

	if desired < current.MinSize {
		log.Printf("⚠️  Factored desired count is less than MinSize, capping at %d", current.MinSize)
		desired = current.MinSize
	}

	log.Printf("Scaling IN 📉 to %d instances (currently %d)", desired, current.DesiredCount)

	if err := s.setDesiredCapacity(desired); err != nil {
		return err
	}

	s.scaleInParams.LastEvent = time.Now()
	return nil
}

func (s *Scaler) scaleOut(desired int64, current AutoscaleGroupDetails) error {
	if s.scaleOutParams.Disable {
		return nil
	}

	// If we've scaled out before, check if a cooldown should be enforced
	if !s.scaleOutParams.LastEvent.IsZero() {
		cooldownRemaining := s.scaleOutParams.CooldownPeriod - time.Since(s.scaleOutParams.LastEvent)

		if cooldownRemaining > 0 {
			log.Printf("⏲ Want to scale OUT but in cooldown for %d seconds", cooldownRemaining/time.Second)
			return nil
		}
	}

	// Calculate the change in the desired count, will be positive
	change := desired - current.DesiredCount

	// Apply scaling factor if one is given
	if s.scaleOutParams.Factor != 0 {
		// Use Ceil to avoid never reaching upper bound
		factoredChange := int64(math.Ceil(float64(change) * s.scaleOutParams.Factor))

		if factoredChange > change {
			log.Printf("👮‍️ Increasing scale-out of %d by factor of %0.2f",
				change, s.scaleOutParams.Factor)
		} else if factoredChange < change {
			log.Printf("👮‍️ Decreasing scale-out of %d by factor of %0.2f",
				change, s.scaleOutParams.Factor)
		} else {
			log.Printf("👮‍️ Scale-out factor of %0.2f was ignored",
				s.scaleOutParams.Factor)
		}

		desired = current.DesiredCount + factoredChange
	}

	if desired > current.MaxSize {
		log.Printf("⚠️  Factored desired count exceed MaxSize, capping at %d", current.MaxSize)
		desired = current.MaxSize
	}

	log.Printf("Scaling OUT 📈 to %d instances (currently %d)", desired, current.DesiredCount)

	if err := s.setDesiredCapacity(desired); err != nil {
		return err
	}

	s.scaleOutParams.LastEvent = time.Now()
	return nil
}

func (s *Scaler) setDesiredCapacity(desired int64) error {
	t := time.Now()

	if err := s.autoscaling.SetDesiredCapacity(desired); err != nil {
		return err
	}

	log.Printf("↳ Set desired to %d (took %v)", desired, time.Now().Sub(t))
	return nil
}

func (s *Scaler) LastScaleOut() time.Time {
	return s.scaleOutParams.LastEvent
}

func (s *Scaler) LastScaleIn() time.Time {
	return s.scaleInParams.LastEvent
}

type buildkiteDriver struct {
	client *buildkite.Client
	queues []string
}

func (a *buildkiteDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return a.client.GetAgentMetrics(a.queues)
}
