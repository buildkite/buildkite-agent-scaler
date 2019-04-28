package scaler

import (
	"log"
	"math"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScaleInParams struct {
	Disable        bool
	CooldownPeriod time.Duration
	MinAdjustment  int64
	MaxAdjustment  int64
	LastScaleIn    time.Time
}

type ScaleOutParams struct {
	Disable        bool
	CooldownPeriod time.Duration
	MinAdjustment  int64
	MaxAdjustment  int64
	LastScaleOut   time.Time
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
	ScaleOutParams           ScaleOutParams
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
	scaleOutParams    ScaleOutParams
}

func NewScaler(client *buildkite.Client, params Params) (*Scaler, error) {
	scaler := &Scaler{
		bk: &buildkiteDriver{
			client: client,
			queue:  params.BuildkiteQueue,
		},
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

func (s *Scaler) Run() error {
	metrics, err := s.bk.GetAgentMetrics()
	if err != nil {
		return err
	}

	if s.metrics != nil {
		err = s.metrics.Publish(metrics.OrgSlug, metrics.Queue, map[string]int64{
			`ScheduledJobsCount`: metrics.ScheduledJobs,
			`RunningJobsCount`:   metrics.RunningJobs,
		})
		if err != nil {
			return err
		}
	}

	count := metrics.ScheduledJobs + metrics.RunningJobs

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

	if desired > current.DesiredCount {
		return s.scaleOut(desired, current)
	}

	if current.DesiredCount > desired {
		return s.scaleIn(desired, current)
	}

	log.Printf("No scaling required, currently %d", current.DesiredCount)
	return nil
}

func (s *Scaler) scaleIn(desired int64, current AutoscaleGroupDetails) error {
	if s.scaleInParams.Disable {
		return nil
	}

	// If we've scaled down before, check if a cooldown should be enforced
	if !s.scaleInParams.LastScaleIn.IsZero() {
		cooldownRemaining := s.scaleInParams.CooldownPeriod - time.Since(s.scaleInParams.LastScaleIn)

		if cooldownRemaining > 0 {
			log.Printf("⏲ Want to scale IN but in cooldown for %d seconds", cooldownRemaining/time.Second)
			return nil
		}
	}

	// Calculate the change in the desired count, will be negative
	change := desired - current.DesiredCount

	// Remember these are negative numbers, which can be confusing
	if s.scaleInParams.MinAdjustment < 0 && s.scaleInParams.MinAdjustment < change {
		log.Printf("👮‍️ Enforcing min adjustment of %d to scale-in (was %d)",
			s.scaleInParams.MinAdjustment, change)
		desired = current.DesiredCount + s.scaleInParams.MinAdjustment
	} else if s.scaleInParams.MaxAdjustment < 0 && s.scaleInParams.MaxAdjustment > change {
		log.Printf("👮‍️ Enforcing max adjustment of %d to scale-in (was %d)",
			s.scaleInParams.MaxAdjustment, change)
		desired = current.DesiredCount + s.scaleInParams.MaxAdjustment
	}

	// Correct negative values if we get them
	if desired < 0 {
		desired = 0
	}

	log.Printf("Scaling IN 📉 to %d instances (currently %d)", desired, current.DesiredCount)

	if err := s.setDesiredCapacity(desired); err != nil {
		return err
	}

	s.scaleInParams.LastScaleIn = time.Now()
	return nil
}

func (s *Scaler) scaleOut(desired int64, current AutoscaleGroupDetails) error {
	if s.scaleOutParams.Disable {
		return nil
	}

	// If we've scaled out before, check if a cooldown should be enforced
	if !s.scaleOutParams.LastScaleOut.IsZero() {
		cooldownRemaining := s.scaleOutParams.CooldownPeriod - time.Since(s.scaleOutParams.LastScaleOut)

		if cooldownRemaining > 0 {
			log.Printf("⏲ Want to scale OUT but in cooldown for %d seconds", cooldownRemaining/time.Second)
			return nil
		}
	}

	// Calculate the change in the desired count, will be positive
	change := current.DesiredCount - desired

	if s.scaleOutParams.MinAdjustment > 0 && s.scaleOutParams.MinAdjustment > change {
		log.Printf("👮‍️ Enforcing min adjustment of %d to scale-out (was %d)",
			s.scaleOutParams.MinAdjustment, change)
		desired = current.DesiredCount + s.scaleOutParams.MinAdjustment
	} else if s.scaleOutParams.MaxAdjustment > 0 && s.scaleOutParams.MaxAdjustment > change {
		log.Printf("👮‍️ Enforcing max adjustment of %d to scale-out (was %d)",
			s.scaleOutParams.MaxAdjustment, change)
		desired = current.DesiredCount + s.scaleOutParams.MaxAdjustment
	}

	log.Printf("Scaling OUT 📈 to %d instances (currently %d)", desired, current.DesiredCount)

	if err := s.setDesiredCapacity(desired); err != nil {
		return err
	}

	s.scaleOutParams.LastScaleOut = time.Now()
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
	return s.scaleOutParams.LastScaleOut
}

func (s *Scaler) LastScaleIn() time.Time {
	return s.scaleOutParams.LastScaleOut
}

type buildkiteDriver struct {
	client *buildkite.Client
	queue  string
}

func (a *buildkiteDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return a.client.GetAgentMetrics(a.queue)
}
