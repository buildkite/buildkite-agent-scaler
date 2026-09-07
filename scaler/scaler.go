package scaler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScaleParams struct {
	Disable        bool
	CooldownPeriod time.Duration
	Factor         float64
	LastEvent      time.Time
}

type Params struct {
	AutoScalingGroupName           string
	AgentsPerInstance              int
	BuildkiteAgentToken            string
	BuildkiteQueue                 string
	UserAgent                      string
	PublishCloudWatchMetrics       bool
	DryRun                         bool
	IncludeWaiting                 bool
	ScaleInParams                  ScaleParams
	ScaleOutParams                 ScaleParams
	InstanceBuffer                 int
	ScaleOnlyAfterAllEvent         bool
	AvailabilityThreshold          float64       // Threshold for agent availability (default 50%, all modes)
	ASGActivityCooldown            time.Duration // How long to wait after an ASG activity before scaling again
	ElasticCIMode                  bool          // Special mode for Elastic CI Stack with additional safety checks
	MinimumInstanceUptime          time.Duration // How long instance should be online before being eligible for dangling instance check
	MaxDanglingInstancesToCheck    int           // Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)
	MaxInstanceCap                 int           // Maximum instance count cap (0 means no cap)
	DanglingInstancesCheckInterval time.Duration // Interval between dangling-instance checks; used to rotate the check window. Defaults to 60s when 0.
	LastScaleInSSMParameter        string        // SSM parameter that shares the last scale-in time across Lambda containers; empty keeps it in memory only. Ignored in DryRun.
}

type Scaler struct {
	autoscaling interface {
		Describe(ctx context.Context) (AutoscaleGroupDetails, error)
		SetDesiredCapacity(ctx context.Context, count int64) error
		GracefulStopTargets(ctx context.Context, instanceIDs []string) ([]string, error)
		SendSIGTERMToAgentsBatch(ctx context.Context, instanceIDs []string) (int, error)
		CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error
	}
	bk interface {
		GetAgentMetrics(ctx context.Context) (buildkite.AgentMetrics, error)
	}
	metrics interface {
		Publish(ctx context.Context, orgSlug, queue string, metrics map[string]int64) error
	}
	scaling                     ScalingCalculator
	scaleInParams               ScaleParams
	scaleOutParams              ScaleParams
	instanceBuffer              int
	scaleOnlyAfterAllEvent      bool
	asgActivityCooldown         time.Duration
	elasticCIMode               bool // Special mode for Elastic CI Stack
	minimumInstanceUptime       time.Duration
	maxDanglingInstancesToCheck int
	// ec2 and ssm back the graceful scale-in path in Elastic CI Mode. asgName
	// only labels log lines from that path.
	ec2     scaleInEC2API
	ssm     ssmCheckAPI
	asgName string
	// lastScaleInStore, when set, shares scaleInParams.LastEvent with other
	// Lambda containers.
	lastScaleInStore lastScaleInStore
}

func NewScaler(client *buildkite.Client, cfg aws.Config, params Params) (*Scaler, error) {
	ssmClient := ssm.NewFromConfig(cfg)
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
		ec2:                    ec2.NewFromConfig(cfg),
		ssm:                    ssmClient,
		asgName:                params.AutoScalingGroupName,
	}

	scaler.minimumInstanceUptime = params.MinimumInstanceUptime
	scaler.maxDanglingInstancesToCheck = params.MaxDanglingInstancesToCheck

	scaler.scaling = ScalingCalculator{
		includeWaiting:        params.IncludeWaiting,
		agentsPerInstance:     params.AgentsPerInstance,
		availabilityThreshold: params.AvailabilityThreshold,
		elasticCIMode:         params.ElasticCIMode,
		maxInstanceCap:        params.MaxInstanceCap,
	}

	if params.DryRun {
		scaler.autoscaling = &dryRunASG{}
		if params.PublishCloudWatchMetrics {
			scaler.metrics = &dryRunMetricsPublisher{}
		}
		return scaler, nil
	}

	if params.LastScaleInSSMParameter != "" {
		scaler.lastScaleInStore = &ssmLastScaleInStore{client: ssmClient, name: params.LastScaleInSSMParameter}
	}

	danglingInstancesCheckInterval := params.DanglingInstancesCheckInterval
	if danglingInstancesCheckInterval <= 0 {
		danglingInstancesCheckInterval = time.Minute
	}

	scaler.autoscaling = &ASGDriver{
		Name:                           params.AutoScalingGroupName,
		Cfg:                            cfg,
		ElasticCIMode:                  params.ElasticCIMode,
		MinimumInstanceUptime:          params.MinimumInstanceUptime,
		MaxDanglingInstancesToCheck:    params.MaxDanglingInstancesToCheck,
		DanglingInstancesCheckInterval: danglingInstancesCheckInterval,
	}

	if params.PublishCloudWatchMetrics {
		scaler.metrics = &cloudWatchMetricsPublisher{
			cfg: cfg,
		}
	}

	if params.ElasticCIMode {
		log.Printf("🛡️ [Elastic CI Mode] Running with enhanced safety features (stale metrics detection, dangling instance protection)")
		if params.ScaleInParams.Disable {
			log.Printf("ℹ️ [Elastic CI Mode] DISABLE_SCALE_IN=true is set but will be ignored to allow proper bidirectional scaling")
		}
	}

	if params.IncludeWaiting {
		log.Printf("ℹ️ ScaleOutForWaitingJobs is enabled. Agents will be created for jobs behind a wait step which can cause Agent bloat if the jobs being waited on are long running.")
	}

	return scaler, nil
}

func (s *Scaler) LastScaleIn() time.Time {
	return s.scaleInParams.LastEvent
}

func (s *Scaler) LastScaleOut() time.Time {
	return s.scaleOutParams.LastEvent
}

func (s *Scaler) Run(ctx context.Context) (time.Duration, error) {

	// In Elastic CI mode, check for any dangling instances (where buildkite-agent is not running)
	// This runs first, before getting metrics or scaling
	if driver, ok := s.autoscaling.(*ASGDriver); ok && s.elasticCIMode {
		if err := driver.CleanupDanglingInstances(ctx, s.minimumInstanceUptime, s.maxDanglingInstancesToCheck); err != nil {
			log.Printf("[Elastic CI Mode] Warning: Failed to cleanup dangling instances: %v", err)
			// Continue with normal scaling operations even if dangling instance cleanup fails
		}
	}

	metrics, err := s.bk.GetAgentMetrics(ctx)
	if err != nil {
		return metrics.PollDuration, err
	}

	// Check if metrics are stale (older than 60 seconds)
	metricAge := time.Since(metrics.Timestamp)
	if !metrics.Timestamp.IsZero() && metricAge > 60*time.Second {
		log.Printf("⚠️ [Elastic CI Mode] Warning: Using metrics that are %.1f seconds old", metricAge.Seconds())
	}

	if s.metrics != nil {
		err = s.metrics.Publish(ctx, metrics.OrgSlug, metrics.Queue, map[string]int64{
			"ScheduledJobsCount": metrics.ScheduledJobs,
			"RunningJobsCount":   metrics.RunningJobs,
			"WaitingJobsCount":   metrics.WaitingJobs,
		})
		if err != nil {
			return metrics.PollDuration, err
		}
	}

	asg, err := s.autoscaling.Describe(ctx)
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

		if proportionalBuffer < 0 {
			log.Printf("⚠️  Calculated negative proportional buffer %d, capping at 0", proportionalBuffer)
			proportionalBuffer = 0
		}

		if s.scaling.maxInstanceCap > 0 && proportionalBuffer > int64(s.scaling.maxInstanceCap) {
			log.Printf("⚠️  Calculated proportional buffer %d exceeds max cap, capping at %d", proportionalBuffer, s.scaling.maxInstanceCap)
			proportionalBuffer = int64(s.scaling.maxInstanceCap)
		}

		if proportionalBuffer > int64(s.instanceBuffer) {
			proportionalBuffer = int64(s.instanceBuffer)
		}

		if proportionalBuffer > 0 {
			log.Printf("↳ 🧮 Adding proportional instance buffer: %d (based on %d total jobs)", proportionalBuffer, totalJobs)
		}
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
	if asg.DesiredCount == desired && instanceCount != desired {
		log.Printf("ASG is converging to desired capacity %d (currently %d running instances)", desired, instanceCount)
		return metrics.PollDuration, nil
	}

	// Another container may have scaled in since we last looked. Read before
	// choosing a direction, as scale-out checks the scale-in cooldown too when
	// SCALE_ONLY_AFTER_ALL_EVENT is set. Only read when the cooldown matters,
	// so a broken parameter can't hold up a plain scale-out. A failed read
	// scales nothing this poll; the next one retries.
	cooldownMatters := desired < asg.DesiredCount || (desired > asg.DesiredCount && s.scaleOnlyAfterAllEvent)
	if s.lastScaleInStore != nil && cooldownMatters {
		stored, err := s.lastScaleInStore.Load(ctx)
		if err != nil {
			return metrics.PollDuration, fmt.Errorf("read last scale-in time: %w", err)
		}
		if stored.After(s.scaleInParams.LastEvent) {
			s.scaleInParams.LastEvent = stored
		}
	}

	if desired > asg.DesiredCount {
		log.Printf("Scaling decision: calculated desired %d instances. ASG current desired: %d, ASG actual running: %d (approx %d agents), Buildkite scheduled: %d, running: %d, waiting: %d",
			desired, asg.DesiredCount, instanceCount, instanceCount*int64(s.scaling.agentsPerInstance), metrics.ScheduledJobs, metrics.RunningJobs, metrics.WaitingJobs)
		log.Printf(" Action: Scale out from %d to %d", asg.DesiredCount, desired)
		return metrics.PollDuration, s.scaleOut(ctx, desired, asg)
	}

	if asg.DesiredCount > desired {
		// In Elastic CI mode, check for pending instances before scaling down
		// If there are pending instances, it means ASG is already scaling, so we should wait
		if s.elasticCIMode && asg.Pending > 0 {
			log.Printf("⏳ [Elastic CI Mode] ASG has %d pending instances, waiting before scaling in", asg.Pending)
			return metrics.PollDuration, nil
		}

		log.Printf(" Action: Scale in from %d to %d", asg.DesiredCount, desired)
		return metrics.PollDuration, s.scaleIn(ctx, desired, asg)
	}

	// In Elastic CI mode, detect dangling instances: we have running instances but zero agents reporting.
	// This can happen when the buildkite-agent process dies on the instance (e.g. OOM killed) while
	// the EC2 instance itself remains healthy. In this case, the normal CleanupDanglingInstances check
	// at the top of Run() may not fire due to the minimumInstanceUptime filter, and neither scaleIn()
	// nor scaleOut() is called because desired == instanceCount after MaxSize capping.
	// We use a 10-minute uptime threshold here to ensure we don't check instances that are still
	// booting (typical boot-to-agent-registration takes 3-5 minutes for Elastic CI Stack instances).
	// This is shorter than the default 1-hour minimumInstanceUptime but long enough to avoid
	// prematurely killing instances that haven't finished starting up.
	if s.elasticCIMode && instanceCount > 0 && metrics.TotalAgents == 0 {
		log.Printf("🔍 [Elastic CI Mode] Detected %d running instance(s) but 0 agents reporting — checking for dangling instances", instanceCount)
		danglingCheckUptime := 10 * time.Minute
		if err := s.autoscaling.CleanupDanglingInstances(ctx, danglingCheckUptime, s.maxDanglingInstancesToCheck); err != nil {
			log.Printf("⚠️  [Elastic CI Mode] Failed to cleanup dangling instances: %v", err)
		}
		return metrics.PollDuration, nil
	}

	log.Printf("No scaling required, currently %d actual instances (desired set to %d)",
		instanceCount, asg.DesiredCount)
	return metrics.PollDuration, nil
}

func (s *Scaler) scaleIn(ctx context.Context, desired int64, current AutoscaleGroupDetails) error {
	// In ElasticCIMode, DISABLE_SCALE_IN is ignored (handled by s.elasticCIMode check below)
	if s.scaleInParams.Disable && !s.elasticCIMode {
		return nil
	}

	// If we're in ElasticCIMode and DISABLE_SCALE_IN is true, log that we're ignoring it
	if s.scaleInParams.Disable && s.elasticCIMode {
		log.Printf("ℹ️ [Elastic CI Mode] Ignoring DISABLE_SCALE_IN=true since ElasticCIMode has safer scaling mechanisms")
	}

	// If we've scaled down before, check if a cooldown should be enforced.
	// Run has already merged in the stored time from other containers.
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

	if !s.elasticCIMode || len(current.InstanceIDs) == 0 || instancesToTerminate <= 0 {
		log.Printf("Using standard scale-in (Elastic CI Mode disabled or no instances to terminate)")
		if err := s.setDesiredCapacity(ctx, desired); err != nil {
			return err
		}
		s.recordScaleIn(ctx)
		return nil
	}

	// In Elastic CI Mode, use graceful termination. Pick the candidates and
	// work out which ones a graceful stop can reach first: both steps only
	// read EC2 and SSM, so a throttled lookup should retry next poll rather
	// than start the cooldown.
	log.Printf("[Elastic CI Mode] Using graceful termination for %d instances", instancesToTerminate)
	instancesForTermination, err := oldestInstances(ctx, s.ec2, current.InstanceIDs, instancesToTerminate, s.asgName)
	if err != nil {
		return fmt.Errorf("select scale-in candidates: %w", err)
	}
	targets, targetsErr := s.autoscaling.GracefulStopTargets(ctx, instancesForTermination)
	if targetsErr == nil && len(targets) > 0 {
		return s.dispatchGracefulStops(ctx, targets)
	}

	// Nothing is draining in any of these cases, so there's no agent-side
	// decrement to collide with. Change desired capacity the way the
	// non-graceful path does, otherwise the scaler keeps picking the same
	// unreachable instances every poll and never converges.
	switch {
	case errors.Is(targetsErr, ErrWindowsGracefulScaleInNotSupported):
		log.Printf("ℹ️  Skipped %d Windows instance(s) - graceful scale-in not supported, will be terminated directly by ASG", len(instancesForTermination))
	case targetsErr != nil:
		return fmt.Errorf("resolve graceful-stop targets: %w", targetsErr)
	default:
		log.Printf("[Elastic CI Mode] no scale-in candidates reachable via SSM; falling back to a desired-capacity change")
	}

	if err := s.setDesiredCapacity(ctx, desired); err != nil {
		return errors.Join(targetsErr, err)
	}
	s.recordScaleIn(ctx)

	// The Elastic CI Stack protects instances from scale-in by default, so
	// the ASG won't act on the capacity change and a lone instance whose
	// agent is gone would sit there forever. Probe it and terminate
	// directly when SSM can't even reach it. Windows instances are never
	// checked against SSM before this point, so they always get the probe.
	singleInstanceASG := current.DesiredCount <= 1 && current.TotalCount == 1 && len(current.InstanceIDs) == 1
	if len(instancesForTermination) > 0 && singleInstanceASG {
		if err := terminateIfAgentUnreachable(ctx, s.ec2, s.ssm, current.InstanceIDs[0]); err != nil {
			return fmt.Errorf("directly terminate probable dangling instance: %w", err)
		}
	}
	return nil
}

// dispatchGracefulStops asks targets to drain and stop. Elastic CI Stack
// agents decrement desired capacity themselves once drained, so the caller
// must leave it alone.
func (s *Scaler) dispatchGracefulStops(ctx context.Context, targets []string) error {
	// Start the cooldown before dispatching, so a recycled or overlapping
	// container sees it before it can scale in on top of us. Every attempt
	// counts: draining instances stay in the ASG, so an early retry would
	// pick the same ones again. A failed write dispatches nothing; the next
	// poll retries.
	now := time.Now()
	if s.lastScaleInStore != nil {
		err := s.lastScaleInStore.Save(ctx, now)
		if errors.Is(err, errAnotherScaleIn) {
			log.Printf("⏲ Want to scale IN but another container got there first")
			return nil
		}
		if err != nil {
			return fmt.Errorf("persist last scale-in time: %w", err)
		}
	}
	s.scaleInParams.LastEvent = now

	log.Printf("[Elastic CI Mode] Attempting graceful termination for %d instance(s): %v", len(targets), targets)
	accepted, err := s.autoscaling.SendSIGTERMToAgentsBatch(ctx, targets)
	if err != nil {
		log.Printf("⚠️  Failed to send graceful-stop command to one or more instances: %v", err)
	}
	if accepted > 0 {
		return nil
	}
	// A failed SendCommand tells us nothing about the instances. Some of
	// them may have taken the command, so changing desired capacity now
	// could decrement twice. Back off and retry after the cooldown.
	return err
}

// recordScaleIn starts the cooldown after a desired-capacity change. Capacity
// is absolute, so overlapping containers can't over-terminate here: losing the
// race just means another container set the same capacity, and a store that
// can't be written isn't worth failing the poll over.
func (s *Scaler) recordScaleIn(ctx context.Context) {
	s.scaleInParams.LastEvent = time.Now()
	if s.lastScaleInStore == nil {
		return
	}
	if err := s.lastScaleInStore.Save(ctx, s.scaleInParams.LastEvent); err != nil && !errors.Is(err, errAnotherScaleIn) {
		log.Printf("⚠️  Failed to persist last scale-in time: %v", err)
	}
}

func (s *Scaler) scaleOut(ctx context.Context, desired int64, current AutoscaleGroupDetails) error {
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

	if err := s.setDesiredCapacity(ctx, desired); err != nil {
		return err
	}

	s.scaleOutParams.LastEvent = time.Now()
	return nil
}

func (s *Scaler) setDesiredCapacity(ctx context.Context, desired int64) error {
	t := time.Now()

	if err := s.autoscaling.SetDesiredCapacity(ctx, desired); err != nil {
		return err
	}

	log.Printf("↳ Set desired to %d (took %v)", desired, time.Since(t))
	return nil
}

// scaleInEC2API is the subset of ec2.Client the graceful scale-in path uses,
// extracted so tests can stub it.
type scaleInEC2API interface {
	describeInstancesAPI
	TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}

// terminateIfAgentUnreachable asks SSM to run a service status check on the
// instance and terminates it only when SSM refuses the command outright.
// That's what an instance whose agent and SSM agent are both gone looks like.
// A command that SSM accepts means the box is alive, so we leave it alone
// even if the agent turns out to be stopped.
//
// The caller must have lowered desired capacity already so the ASG does not
// launch a replacement.
func terminateIfAgentUnreachable(ctx context.Context, ec2Client scaleInEC2API, ssmClient ssmCheckAPI, instanceID string) error {
	log.Printf("[Elastic CI Mode] Single-instance ASG detected - checking if instance %s is a dangling instance", instanceID)

	descResp, descErr := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})

	// Default to Linux when detection fails. We only get here after SSM
	// reported the instance offline, so the probe is expected to fail with
	// InvalidInstanceId whatever document we pick.
	documentName := "AWS-RunShellScript"
	checkCommand := "systemctl is-active buildkite-agent"
	if descErr != nil {
		log.Printf("[Elastic CI Mode] Warning: Failed to detect platform for %s, defaulting to Linux: %v", instanceID, descErr)
	} else if len(descResp.Reservations) > 0 && len(descResp.Reservations[0].Instances) > 0 {
		instance := descResp.Reservations[0].Instances[0]
		if strings.EqualFold(string(instance.Platform), "windows") {
			documentName = "AWS-RunPowerShellScript"
			checkCommand = "nssm status buildkite-agent"
			log.Printf("[Elastic CI Mode] Detected Windows platform for instance %s", instanceID)
		}
	}

	_, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String(documentName),
		Parameters: map[string][]string{
			"commands": {checkCommand},
		},
		Comment: aws.String("Check if buildkite-agent service is running"),
	})
	if err == nil {
		log.Printf("[Elastic CI Mode] Instance appears responsive, not terminating directly")
		return nil
	}

	log.Printf("[Elastic CI Mode] Warning: Cannot check agent status, assuming dangling instance: %v", err)
	log.Printf("[Elastic CI Mode] Directly terminating probable dangling instance")
	if _, err := ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}); err != nil {
		return err
	}

	log.Printf("[Elastic CI Mode] Successfully terminated instance %s via EC2 API", instanceID)
	return nil
}

// oldestInstances picks up to n instances to stop, oldest launch first.
//
// Draining instances stay in the ASG until their job finishes, so a later poll
// sees the same set and must pick the same instances. Anything else stops more
// agents than the scaling target asked for. Instance ID breaks launch-time ties
// because instances launched in one batch often share a timestamp. When the
// describe call fails we return the error instead of guessing from ASG order,
// which AWS does not keep stable.
func oldestInstances(ctx context.Context, client describeInstancesAPI, instanceIDs []string, n int64, asgName string) ([]string, error) {
	describeResult, err := describeInstancesTolerant(ctx, client, instanceIDs, asgName)
	if err != nil {
		return nil, fmt.Errorf("describe instance launch times: %w", err)
	}

	type instanceInfo struct {
		ID         string
		LaunchTime time.Time
	}
	instances := make([]instanceInfo, 0, len(instanceIDs))
	for _, reservation := range describeResult.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId == nil || instance.LaunchTime == nil {
				continue
			}
			instances = append(instances, instanceInfo{ID: *instance.InstanceId, LaunchTime: *instance.LaunchTime})
		}
	}

	slices.SortFunc(instances, func(a, b instanceInfo) int {
		return cmp.Or(a.LaunchTime.Compare(b.LaunchTime), cmp.Compare(a.ID, b.ID))
	})

	limit := min(int(n), len(instances))
	selected := make([]string, 0, limit)
	for _, instance := range instances[:limit] {
		selected = append(selected, instance.ID)
	}

	if len(selected) > 0 {
		log.Printf("[Elastic CI Mode] Selecting %d oldest instances by launch time for termination (oldest from %s)",
			len(selected), instances[0].LaunchTime.Format(time.RFC3339))
	}
	return selected, nil
}

type buildkiteDriver struct {
	client *buildkite.Client
	queue  string
}

func (b *buildkiteDriver) GetAgentMetrics(ctx context.Context) (buildkite.AgentMetrics, error) {
	return b.client.GetAgentMetrics(ctx, b.queue)
}
