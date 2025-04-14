package scaler

import (
	"log"
	"math"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
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
	BuildkiteQueue           string
	UserAgent                string
	PublishCloudWatchMetrics bool
	DryRun                   bool
	IncludeWaiting           bool
	ScaleInParams            ScaleParams
	ScaleOutParams           ScaleParams
	InstanceBuffer           int
	ScaleOnlyAfterAllEvent   bool

	GracefulTermination bool
}

type Scaler struct {
	autoscaling interface {
		Describe() (AutoscaleGroupDetails, error)
		SetDesiredCapacity(count int64) error
		SendSIGTERMToAgents(instanceID string) error
	}
	bk interface {
		GetAgentMetrics() (buildkite.AgentMetrics, error)
	}
	metrics interface {
		Publish(orgSlug, queue string, metrics map[string]int64) error
	}
	scaling                ScalingCalculator
	scaleInParams          ScaleParams
	scaleOutParams         ScaleParams
	instanceBuffer         int
	scaleOnlyAfterAllEvent bool
}

func NewScaler(client *buildkite.Client, sess *session.Session, params Params) (*Scaler, error) {
	scaler := &Scaler{
		bk: &buildkiteDriver{
			client: client,
			queue:  params.BuildkiteQueue,
		},
		scaleInParams:          params.ScaleInParams,
		scaleOutParams:         params.ScaleOutParams,
		instanceBuffer:         params.InstanceBuffer,
		scaleOnlyAfterAllEvent: params.ScaleOnlyAfterAllEvent,
	}

	scaler.scaling = ScalingCalculator{
		includeWaiting:    params.IncludeWaiting,
		agentsPerInstance: params.AgentsPerInstance,
	}

	if params.DryRun {
		scaler.autoscaling = &dryRunASG{}
		if params.PublishCloudWatchMetrics {
			scaler.metrics = &dryRunMetricsPublisher{}
		}
		return scaler, nil
	}

	scaler.autoscaling = &ASGDriver{
		Name:                params.AutoScalingGroupName,
		Sess:                sess,
		GracefulTermination: params.GracefulTermination,
	}

	if params.PublishCloudWatchMetrics {
		scaler.metrics = &cloudWatchMetricsPublisher{
			sess: sess,
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
			"ScheduledJobsCount": metrics.ScheduledJobs,
			"RunningJobsCount":   metrics.RunningJobs,
			"WaitingJobsCount":   metrics.WaitingJobs,
		})
		if err != nil {
			return metrics.PollDuration, err
		}
	}

	asg, err := s.autoscaling.Describe()
	if err != nil {
		return metrics.PollDuration, err
	}

	desired := s.scaling.DesiredCount(&metrics, &asg) + int64(s.instanceBuffer)

	if desired > asg.MaxSize {
		log.Printf("⚠️  Desired count exceed MaxSize, capping at %d", asg.MaxSize)
		desired = asg.MaxSize
	}
	if desired < asg.MinSize {
		log.Printf("⚠️  Desired count is less than MinSize, capping at %d", asg.MinSize)
		desired = asg.MinSize
	}

	if desired > asg.DesiredCount {
		return metrics.PollDuration, s.scaleOut(desired, asg)
	}

	if asg.DesiredCount > desired {
		return metrics.PollDuration, s.scaleIn(desired, asg)
	}

	log.Printf("No scaling required, currently %d", asg.DesiredCount)
	return metrics.PollDuration, nil
}

func (s *Scaler) scaleIn(desired int64, current AutoscaleGroupDetails) error {
	if s.scaleInParams.Disable {
		return nil
	}

	// If we've scaled down before, check if a cooldown should be enforced
	if !s.scaleInParams.LastEvent.IsZero() {
		lastScaleInEvent := s.scaleInParams.LastEvent
		lastScaleOutEvent := s.scaleOutParams.LastEvent
		lastEvent := lastScaleInEvent
		if s.scaleOnlyAfterAllEvent && lastScaleInEvent.Before(lastScaleOutEvent) {
			lastEvent = lastScaleOutEvent
		}
		cooldownRemaining := s.scaleInParams.CooldownPeriod - time.Since(lastEvent)

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

		switch {
		case factoredChange < change:
			log.Printf("👮‍️ Increasing scale-in of %d by factor of %0.2f",
				change, factor)

		case factoredChange > change:
			log.Printf("👮‍️ Decreasing scale-in of %d by factor of %0.2f",
				change, factor)

		default:
			log.Printf("👮‍️ Scale-in factor of %0.2f was ignored",
				factor)
		}

		desired = current.DesiredCount + factoredChange

		if desired < current.MinSize {
			log.Printf("⚠️  Post scalein-factor desired count lower than MinSize, capping at %d", current.MinSize)
			desired = current.MinSize
		}
	}

	// Correct negative values if we get them
	if desired < 0 {
		desired = 0
	}

	log.Printf("Scaling IN 📉 to %d instances (currently %d)", desired, current.DesiredCount)

	instancesToTerminate := current.DesiredCount - desired

	// If we're using graceful termination and have instance IDs
	if driver, ok := s.autoscaling.(*ASGDriver); ok && driver.GracefulTermination && len(current.InstanceIDs) > 0 && instancesToTerminate > 0 {
		log.Printf("Using graceful termination for %d instances", instancesToTerminate)

		// Check if lifecycle hooks are configured for this ASG
		lifeCycleHookCount, err := driver.CountTerminationLifecycleHooks()
		if err != nil {
			log.Printf("Warning: Could not check for lifecycle hooks: %v. Falling back to standard termination.", err)
			lifeCycleHookCount = 0
		}

		if lifeCycleHookCount > 0 {
			// With lifecycle hooks, ASG will handle termination with our hooks
			log.Printf("Using ASG lifecycle hooks for graceful termination (found %d hooks)", lifeCycleHookCount)

			// Decrease desired capacity and let lifecycle hooks handle the rest
			if err := s.setDesiredCapacity(desired); err != nil {
				return err
			}

			log.Printf("Decreased desired capacity to %d. ASG lifecycle hooks will ensure graceful termination.", desired)
		} else {
			// Fallback: use standard scaling with no special handling
			// This relies on the agent's own graceful shutdown mechanism
			log.Printf("No ASG lifecycle hooks found. For improved reliability, consider adding lifecycle hooks to your ASG.")

			// Set desired capacity directly and let ASG handle termination
			if err := s.setDesiredCapacity(desired); err != nil {
				return err
			}

			log.Printf("Decreased desired capacity to %d. Standard termination will be used.", desired)
		}
	} else {
		// Regular scale-in by setting desired capacity
		if err := s.setDesiredCapacity(desired); err != nil {
			return err
		}
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
		lastScaleInEvent := s.scaleInParams.LastEvent
		lastScaleOutEvent := s.scaleOutParams.LastEvent
		lastEvent := lastScaleOutEvent
		if s.scaleOnlyAfterAllEvent && lastScaleOutEvent.Before(lastScaleInEvent) {
			lastEvent = lastScaleInEvent
		}
		cooldownRemaining := s.scaleOutParams.CooldownPeriod - time.Since(lastEvent)

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

		switch {
		case factoredChange > change:
			log.Printf("👮‍️ Increasing scale-out of %d by factor of %0.2f",
				change, s.scaleOutParams.Factor)

		case factoredChange < change:
			log.Printf("👮‍️ Decreasing scale-out of %d by factor of %0.2f",
				change, s.scaleOutParams.Factor)

		default:
			log.Printf("👮‍️ Scale-out factor of %0.2f was ignored",
				s.scaleOutParams.Factor)
		}

		desired = current.DesiredCount + factoredChange

		if desired > current.MaxSize {
			log.Printf("⚠️  Post scaleout-factor desired count exceed MaxSize, capping at %d", current.MaxSize)
			desired = current.MaxSize
		}
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
	queue  string
}

func (a *buildkiteDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return a.client.GetAgentMetrics(a.queue)
}
