package scaler

import (
	"context"
	"log"
	"math"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScaleParams struct {
	Disable        bool
	CooldownPeriod time.Duration
	Factor         float64
	LastEvent      time.Time
}

type Params struct {
	AutoScalingGroupName        string
	AgentsPerInstance           int
	BuildkiteAgentToken         string
	BuildkiteQueue              string
	UserAgent                   string
	PublishCloudWatchMetrics    bool
	DryRun                      bool
	IncludeWaiting              bool
	ScaleInParams               ScaleParams
	ScaleOutParams              ScaleParams
	InstanceBuffer              int
	ScaleOnlyAfterAllEvent      bool
	AvailabilityThreshold       float64       // Threshold for agent availability
	MinAgentsPercentage         float64       // Minimum acceptable percentage of expected agents
	ASGActivityCooldown         time.Duration // How long to wait after an ASG activity before scaling again
	ElasticCIMode               bool          // Special mode for Elastic CI Stack with additional safety checks
	MinimumInstanceUptime       time.Duration // How long instance should be online before being eligible for dangling instance check
	MaxDanglingInstancesToCheck int           // Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)
}

type Scaler struct {
	autoscaling interface {
		Describe() (AutoscaleGroupDetails, error)
		SetDesiredCapacity(count int64) error
		SendSIGTERMToAgents(instanceID string) error
		CleanupDanglingInstances() error
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
	asgActivityCooldown    time.Duration
	elasticCIMode          bool // Special mode for Elastic CI Stack
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
		asgActivityCooldown:    params.ASGActivityCooldown,
		elasticCIMode:          params.ElasticCIMode,
	}

	scaler.scaling = ScalingCalculator{
		includeWaiting:        params.IncludeWaiting,
		agentsPerInstance:     params.AgentsPerInstance,
		availabilityThreshold: params.AvailabilityThreshold,
		minAgentsPercentage:   params.MinAgentsPercentage,
		elasticCIMode:         params.ElasticCIMode,
	}

	if params.DryRun {
		scaler.autoscaling = &dryRunASG{}
		if params.PublishCloudWatchMetrics {
			scaler.metrics = &dryRunMetricsPublisher{}
		}
		return scaler, nil
	}

	scaler.autoscaling = &ASGDriver{
		Name:                        params.AutoScalingGroupName,
		Sess:                        sess,
		ElasticCIMode:               params.ElasticCIMode,
		MinimumInstanceUptime:       params.MinimumInstanceUptime,
		MaxDanglingInstancesToCheck: params.MaxDanglingInstancesToCheck,
	}

	if params.PublishCloudWatchMetrics {
		scaler.metrics = &cloudWatchMetricsPublisher{
			sess: sess,
		}
	}

	return scaler, nil
}

func (s *Scaler) Run() (time.Duration, error) {
	if s.elasticCIMode {
		log.Printf("🛡️ [Elastic CI Mode] Running scaler with enhanced safety features (stale metrics detection, dangling instance protection)")
		if s.scaleInParams.Disable {
			log.Printf("ℹ️ [Elastic CI Mode] DISABLE_SCALE_IN=true is set but will be ignored in ElasticCIMode to allow proper bidirectional scaling")
		}
	}

	// In Elastic CI mode, check for any dangling instances (where buildkite-agent is not running)
	// This runs first, before getting metrics or scaling
	if driver, ok := s.autoscaling.(*ASGDriver); ok && s.elasticCIMode {
		if err := driver.CleanupDanglingInstances(); err != nil {
			log.Printf("[Elastic CI Mode] Warning: Failed to cleanup dangling instances: %v", err)
			// Continue with normal scaling operations even if dangling instance cleanup fails
		}
	}

	metrics, err := s.bk.GetAgentMetrics()
	if err != nil {
		return metrics.PollDuration, err
	}

	// Check if metrics are stale (older than 60 seconds)
	metricAge := time.Since(metrics.Timestamp)
	if !metrics.Timestamp.IsZero() && metricAge > 60*time.Second {
		log.Printf("⚠️ [Elastic CI Mode] Warning: Using metrics that are %.1f seconds old", metricAge.Seconds())
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

	log.Printf("Scaling calculation based on metrics collected at %s", metrics.Timestamp.Format(time.RFC3339))

	desired := s.scaling.DesiredCount(&metrics, &asg)

	// Only add instance buffer if there are agents required (any jobs that need processing)
	if metrics.ScheduledJobs > 0 || metrics.RunningJobs > 0 || metrics.WaitingJobs > 0 {
		// Calculate a proportional buffer based on the number of jobs
		totalJobs := metrics.ScheduledJobs + metrics.RunningJobs
		if s.scaling.includeWaiting {
			totalJobs += metrics.WaitingJobs
		}

		// Apply a proportional buffer, but ensure we don't add more than the configured buffer
		// For a single job add just 1 instance buffer, scaling up to the full buffer for larger workloads
		var proportionalBuffer int64

		if s.scaling.agentsPerInstance <= 0 {
			log.Printf("⚠️  Invalid agentsPerInstance value %d, defaulting to 1", s.scaling.agentsPerInstance)
			proportionalBuffer = totalJobs // Default to 1:1 mapping
		} else {
			proportionalBuffer = int64(math.Ceil(float64(totalJobs) / float64(s.scaling.agentsPerInstance)))
		}

		if proportionalBuffer < 0 || proportionalBuffer > 1000 {
			log.Printf("⚠️  Calculated unreasonable proportional buffer %d, capping at 1000", proportionalBuffer)
			proportionalBuffer = 1000
		}

		if proportionalBuffer > int64(s.instanceBuffer) {
			proportionalBuffer = int64(s.instanceBuffer)
		}

		log.Printf("↳ 🧮 Adding proportional instance buffer: %d (based on %d total jobs)", proportionalBuffer, totalJobs)
		desired += proportionalBuffer
	}

	if desired > asg.MaxSize {
		log.Printf("⚠️  Desired count exceed MaxSize, capping at %d", asg.MaxSize)
		desired = asg.MaxSize
	}
	if desired < asg.MinSize {
		log.Printf("⚠️  Desired count is less than MinSize, capping at %d", asg.MinSize)
		desired = asg.MinSize
	}

	// Use actual count for comparison if available, otherwise fall back to desired count
	instanceCount := asg.ActualCount
	if instanceCount == 0 {
		instanceCount = asg.DesiredCount
	}

	if desired > instanceCount {
		log.Printf("Scaling decision: need %d instances, have %d actual running instances (desired set to %d)",
			desired, instanceCount, asg.DesiredCount)
		return metrics.PollDuration, s.scaleOut(desired, asg)
	}

	if instanceCount > desired {
		// In Elastic CI mode, check for pending instances before scaling down
		// If there are pending instances, it means ASG is already scaling, so we should wait
		if s.elasticCIMode && asg.Pending > 0 {
			log.Printf("⏳ [Elastic CI Mode] ASG has %d pending instances, waiting before scaling in", asg.Pending)
			return metrics.PollDuration, nil
		}

		log.Printf("Scaling decision: need %d instances, have %d actual running instances (desired set to %d)",
			desired, instanceCount, asg.DesiredCount)
		return metrics.PollDuration, s.scaleIn(desired, asg)
	}

	log.Printf("No scaling required, currently %d actual instances (desired set to %d)",
		instanceCount, asg.DesiredCount)
	return metrics.PollDuration, nil
}

func (s *Scaler) scaleIn(desired int64, current AutoscaleGroupDetails) error {
	// In ElasticCIMode, we ignore DISABLE_SCALE_IN since we have safer scaling mechanisms
	if s.scaleInParams.Disable && !s.elasticCIMode {
		return nil
	}

	// If we're in ElasticCIMode and DISABLE_SCALE_IN is true, log that we're ignoring it
	if s.scaleInParams.Disable && s.elasticCIMode {
		log.Printf("ℹ️ [Elastic CI Mode] Ignoring DISABLE_SCALE_IN=true since ElasticCIMode has safer scaling mechanisms")
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

	// Special Elastic CI Stack mode with additional safety checks
	if s.elasticCIMode {
		// Check for recent ASG scale-down activity to avoid scaling down too quickly
		// Only do this check if we have access to the ASG activities
		if driver, ok := s.autoscaling.(*ASGDriver); ok {
			// In ElasticCIMode, override the page limit to allow unlimited pages
			if driver.MaxDescribeScalingActivitiesPages >= 0 {
				// Override to allow unlimited pages (-1) for full activity history in ElasticCIMode
				log.Printf("ℹ️ [Elastic CI Mode] Setting MAX_DESCRIBE_SCALING_ACTIVITIES_PAGES from %d to -1 (unlimited) for better safety checks",
					driver.MaxDescribeScalingActivitiesPages)
				driver.MaxDescribeScalingActivitiesPages = -1
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Get the last scale-in activity from ASG history
			_, lastScaleInActivity, err := driver.GetLastScalingInAndOutActivity(ctx, false, true)
			if err != nil {
				log.Printf("⚠️ [Elastic CI Mode] Could not check last ASG scale-in activity: %v", err)
			} else if lastScaleInActivity != nil && lastScaleInActivity.StartTime != nil {
				// Check how recently the ASG scaled down
				lastScaleInTime := *lastScaleInActivity.StartTime
				timeSinceLastScaleIn := time.Since(lastScaleInTime)

				// Check if we're in cooldown period based on the last ASG scale-in activity
				if s.scaleInParams.CooldownPeriod > 0 && timeSinceLastScaleIn < s.scaleInParams.CooldownPeriod {
					log.Printf("⏲ [Elastic CI Mode] Last ASG scale-in was %s ago, in cooldown period for %s more (cooldown: %s)",
						timeSinceLastScaleIn.Round(time.Second),
						(s.scaleInParams.CooldownPeriod - timeSinceLastScaleIn).Round(time.Second),
						s.scaleInParams.CooldownPeriod)
					return nil
				}

				log.Printf("[Elastic CI Mode] Last ASG scale-in was %s ago", timeSinceLastScaleIn.Round(time.Second))
			}
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

	// In Elastic CI Mode, use graceful termination if we have instance IDs
	if driver, ok := s.autoscaling.(*ASGDriver); ok && driver.ElasticCIMode && len(current.InstanceIDs) > 0 && instancesToTerminate > 0 {
		log.Printf("[Elastic CI Mode] Using graceful termination for %d instances", instancesToTerminate)

		// Determine instances to terminate by sorting by launch time (oldest first)
		maxToTerminate := instancesToTerminate

		instancesForTermination := make([]string, 0, maxToTerminate)

		if len(current.InstanceIDs) > 0 {
			// Define a struct to hold instance info for sorting
			type instanceInfo struct {
				ID         string
				LaunchTime time.Time
			}

			ec2Svc := ec2.New(driver.Sess)

			// Convert to AWS string pointers for API call - using aws.StringSlice helper
			instanceIDPtrs := aws.StringSlice(current.InstanceIDs)

			instances := make([]instanceInfo, 0, len(current.InstanceIDs))
			describeResult, err := ec2Svc.DescribeInstances(&ec2.DescribeInstancesInput{
				InstanceIds: instanceIDPtrs,
			})

			if err != nil {
				log.Printf("[Elastic CI Mode] Warning: Could not get instance launch times: %v", err)
				// Fall back to unsorted if we can't get launch times
				instancesForTermination = current.InstanceIDs
				if int64(len(instancesForTermination)) > maxToTerminate {
					instancesForTermination = instancesForTermination[:maxToTerminate]
				}
			} else {
				// Process results and build list of instances with launch times
				// We need to iterate through reservations as that's how AWS groups the instances
				for _, reservation := range describeResult.Reservations {
					for _, instance := range reservation.Instances {
						if instance.InstanceId != nil && instance.LaunchTime != nil {
							instances = append(instances, instanceInfo{
								ID:         *instance.InstanceId,
								LaunchTime: *instance.LaunchTime,
							})
						}
					}
				}

				// Sort instances by launch time (oldest first)
				sort.Slice(instances, func(i, j int) bool {
					return instances[i].LaunchTime.Before(instances[j].LaunchTime)
				})

				limit := int(maxToTerminate)
				if len(instances) < limit {
					limit = len(instances)
				}

				instancesForTermination = make([]string, limit)
				for i := 0; i < limit; i++ {
					instancesForTermination[i] = instances[i].ID
				}

				if len(instances) > 0 {
					oldestTime := instances[0].LaunchTime.Format(time.RFC3339)
					log.Printf("[Elastic CI Mode] Selecting %d oldest instances by launch time for termination (oldest from %s)",
						len(instancesForTermination), oldestTime)
				}
			}
		}

		log.Printf("Sending SIGTERM to %d instances: %v", len(instancesForTermination), instancesForTermination)

		sigTermErrors := 0
		for _, instanceID := range instancesForTermination {
			if err := driver.SendSIGTERMToAgents(instanceID); err != nil {
				log.Printf("⚠️  Failed to send SIGTERM to instance %s: %v", instanceID, err)
				sigTermErrors++
			} else {
				log.Printf("✅ Successfully sent SIGTERM to instance %s", instanceID)
			}
		}

		if sigTermErrors > 0 {
			log.Printf("⚠️  Failed to send SIGTERM to %d/%d instances",
				sigTermErrors, len(instancesForTermination))
		} else {
			log.Printf("✅ Successfully sent SIGTERM to all %d instances",
				len(instancesForTermination))
		}

		if current.DesiredCount <= 1 && len(current.InstanceIDs) == 1 {
			instanceID := current.InstanceIDs[0]
			log.Printf("[Elastic CI Mode] Single-instance ASG detected - checking if instance %s is a dangling instance", instanceID)

			// Only consider direct termination for dangling instances
			ssmSvc := ssm.New(driver.Sess)
			ec2Svc := ec2.New(driver.Sess)

			// Try to check if buildkite-agent is running via SSM
			_, err := ssmSvc.SendCommand(&ssm.SendCommandInput{
				InstanceIds:  []*string{aws.String(instanceID)},
				DocumentName: aws.String("AWS-RunShellScript"),
				Parameters: map[string][]*string{
					"commands": {aws.String("systemctl is-active buildkite-agent")},
				},
				Comment: aws.String("Check if buildkite-agent service is running"),
			})

			// Only terminate if we can't check agent status, suggesting it's likely a dangling instance
			if err != nil {
				log.Printf("[Elastic CI Mode] Warning: Cannot check agent status, assuming dangling instance: %v", err)
				log.Printf("[Elastic CI Mode] Directly terminating probable dangling instance")

				if termErr := directlyTerminateInstance(ec2Svc, instanceID); termErr != nil {
					log.Printf("[Elastic CI Mode] Error: Failed to terminate: %v", termErr)
				}
			} else {
				log.Printf("[Elastic CI Mode] Instance appears responsive, not terminating directly")
			}
		}
	} else {
		log.Printf("Using standard scale-in (Elastic CI Mode disabled or no instances to terminate)")
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

	log.Printf("↳ Set desired to %d (took %v)", desired, time.Since(t))
	return nil
}

func (s *Scaler) LastScaleOut() time.Time {
	return s.scaleOutParams.LastEvent
}

func (s *Scaler) LastScaleIn() time.Time {
	return s.scaleInParams.LastEvent
}

// directlyTerminateInstance terminates an EC2 instance directly via EC2 API
// This is a helper function for dangling instance termination
func directlyTerminateInstance(ec2Svc *ec2.EC2, instanceID string) error {
	_, err := ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})

	if err != nil {
		return err
	}

	log.Printf("[Elastic CI Mode] Successfully terminated instance %s via EC2 API", instanceID)
	return nil
}

type buildkiteDriver struct {
	client *buildkite.Client
	queue  string
}

func (a *buildkiteDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return a.client.GetAgentMetrics(a.queue)
}
