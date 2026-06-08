package scaler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// ErrWindowsGracefulScaleInNotSupported is returned when attempting graceful scale-in on Windows instances.
// Windows instances don't support SIGTERM-based graceful shutdown; they rely on lifecycle hooks instead.
var ErrWindowsGracefulScaleInNotSupported = errors.New("graceful scale-in not supported on Windows")

const (
	activitySucessfulStatusCode           = "Successful"
	userRequestForChangingDesiredCapacity = "a user request explicitly set group desired capacity changing the desired capacity"
	scalingOutKey                         = "increasing the capacity"
	shrinkingKey                          = "shrinking the capacity"
)

type AutoscaleGroupDetails struct {
	Pending      int64
	DesiredCount int64
	MinSize      int64
	MaxSize      int64
	InstanceIDs  []string // Instance IDs in the ASG
	ActualCount  int64    // Actual number of running instances
}

type ASGDriver struct {
	Name                              string
	Cfg                               aws.Config
	MaxDescribeScalingActivitiesPages int
	ElasticCIMode                     bool
	MinimumInstanceUptime             time.Duration
	MaxDanglingInstancesToCheck       int           // Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)
	DanglingInstancesCheckInterval    time.Duration // Interval between dangling-instance checks; used to rotate the check window. Defaults to 60s when 0.

	// SSM Run Command timings for checkAndMarkUnhealthy. Zero values fall back
	// to the defaults below; set only in tests to avoid real sleeps.
	ssmRegistrationDelay time.Duration
	ssmPollInterval      time.Duration
	ssmPollDeadline      time.Duration
}

// waitForSSMReady blocks until the SSM agent on instanceID reports PingStatus="Online",
// or until timeout elapses.
func (a *ASGDriver) waitForSSMReady(ctx context.Context, instanceID string, timeout time.Duration) error {
	ssmSvc := ssm.NewFromConfig(a.Cfg)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := ssmSvc.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			Filters: []ssmTypes.InstanceInformationStringFilter{
				{
					Key:    aws.String("InstanceIds"),
					Values: []string{instanceID},
				},
			},
		})
		if err != nil {
			log.Printf("[SSM] DescribeInstanceInformation failed for %s: %v", instanceID, err)
		} else if len(resp.InstanceInformationList) > 0 &&
			resp.InstanceInformationList[0].PingStatus == ssmTypes.PingStatusOnline {
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timed out waiting for SSM agent to become ready on %s", instanceID)
}

// getASGPlatform detects whether the ASG contains Linux or Windows instances.
// Since each ASG is single-platform, we only need to check one instance.
func (a *ASGDriver) getASGPlatform(ctx context.Context, instances []ec2Types.Instance) string {
	for _, instance := range instances {
		// The Platform field is only set for Windows instances.
		// Use case-insensitive comparison because the EC2 API returns "windows" (lowercase)
		// but ec2Types.PlatformValuesWindows is "Windows" (capitalized).
		if strings.EqualFold(string(instance.Platform), "windows") {
			return "windows"
		}
	}
	return "linux"
}

// getCheckCommand returns the appropriate check command for the platform
func (a *ASGDriver) getCheckCommand(platform string) string {
	if platform == "windows" {
		return `
$AgentStatus = nssm status buildkite-agent 2>&1
if ($AgentStatus -match "SERVICE_RUNNING") {
    Write-Output "RUNNING"
} else {
    Write-Output "NOT_RUNNING"
}
`
	}

	// Default to Linux
	return `#!/bin/bash
# Linux check command
if [ -f /tmp/buildkite-agent-termination-marker ]; then
  echo "MARKER_EXISTS: Instance is already marked for termination"
  exit 0
fi

ACTIVE_STATE=$(systemctl show buildkite-agent -p ActiveState | cut -d= -f2)
case "$ACTIVE_STATE" in
  "active"|"activating") echo "RUNNING" ;;
  *) echo "NOT_RUNNING: $ACTIVE_STATE" ;;
esac
`
}

// ssmCheckAPI is the subset of ssm.Client used by checkAndMarkUnhealthy,
// extracted so tests can stub it.
type ssmCheckAPI interface {
	DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
	SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	ListCommandInvocations(ctx context.Context, params *ssm.ListCommandInvocationsInput, optFns ...func(*ssm.Options)) (*ssm.ListCommandInvocationsOutput, error)
}

// asgHealthAPI is the subset of autoscaling.Client used by checkAndMarkUnhealthy,
// extracted so tests can stub it.
type asgHealthAPI interface {
	SetInstanceHealth(ctx context.Context, params *autoscaling.SetInstanceHealthInput, optFns ...func(*autoscaling.Options)) (*autoscaling.SetInstanceHealthOutput, error)
}

// filterOnlineSSMInstances returns the subset of instanceIDs whose SSM agent
// last pinged as Online. Anything else (ConnectionLost, Inactive) means
// SendCommand would either fail or sit Pending until it times out.
// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_InstanceInformation.html
func filterOnlineSSMInstances(ctx context.Context, ssmSvc ssmCheckAPI, instanceIDs []string) ([]string, error) {
	resp, err := ssmSvc.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []ssmTypes.InstanceInformationStringFilter{
			{Key: aws.String("InstanceIds"), Values: instanceIDs},
		},
	})
	if err != nil {
		return nil, err
	}
	online := make([]string, 0, len(resp.InstanceInformationList))
	for _, info := range resp.InstanceInformationList {
		if info.PingStatus == ssmTypes.PingStatusOnline && info.InstanceId != nil {
			online = append(online, *info.InstanceId)
		}
	}
	return online, nil
}

// pollCommandInvocations polls ListCommandInvocations every interval until
// every expected invocation reaches a terminal status or the deadline elapses.
// On timeout it returns whatever invocations exist so far. Each poll lists every
// commandID, following NextToken so fleets larger than one response page (50)
// are fully collected.
// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_ListCommandInvocations.html
func pollCommandInvocations(ctx context.Context, ssmSvc ssmCheckAPI, commandIDs []string, expected int, interval, deadline time.Duration) (map[string]ssmTypes.CommandInvocation, error) {
	end := time.Now().Add(deadline)
	results := make(map[string]ssmTypes.CommandInvocation, expected)
	for {
		allTerminal := true
		for _, commandID := range commandIDs {
			var nextToken *string
			for {
				out, err := ssmSvc.ListCommandInvocations(ctx, &ssm.ListCommandInvocationsInput{
					CommandId: aws.String(commandID),
					Details:   true,
					NextToken: nextToken,
				})
				if err != nil {
					return results, err
				}
				// Non-terminal statuses per CommandInvocation docs:
				// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_CommandInvocation.html
				for _, inv := range out.CommandInvocations {
					if inv.InstanceId == nil {
						continue
					}
					results[*inv.InstanceId] = inv
					switch inv.Status {
					case ssmTypes.CommandInvocationStatusPending,
						ssmTypes.CommandInvocationStatusInProgress,
						ssmTypes.CommandInvocationStatusDelayed,
						ssmTypes.CommandInvocationStatusCancelling:
						allTerminal = false
					}
				}
				if out.NextToken == nil || *out.NextToken == "" {
					break
				}
				nextToken = out.NextToken
			}
		}
		if len(results) >= expected && allTerminal {
			return results, nil
		}
		if !time.Now().Before(end) {
			return results, nil
		}
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// pluginOutput concatenates stdout across an invocation's plugins. SSM returns
// one CommandPlugin per document step; our check documents (AWS-RunShellScript
// and AWS-RunPowerShellScript) have a single step, but we concatenate to stay
// correct if that ever changes.
// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_CommandPlugin.html
func pluginOutput(inv ssmTypes.CommandInvocation) string {
	var b strings.Builder
	for _, p := range inv.CommandPlugins {
		if p.Output != nil {
			b.WriteString(*p.Output)
		}
	}
	return b.String()
}

// checkAndMarkUnhealthy probes buildkite-agent on each instance via SSM and
// marks unhealthy any whose agent service is not running, so the ASG
// terminates and replaces them. Skipping graceful shutdown is safe here: a
// stopped agent has no jobs to drain.
//
// The probe is batched in three AWS calls plus polling:
//  1. DescribeInstanceInformation filters down to instances whose SSM agent
//     is reachable.
//  2. One SendCommand fans the check script out to all targets in parallel
//     (https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html).
//  3. ListCommandInvocations is polled until each invocation reaches a
//     terminal status or the deadline elapses.
func (a *ASGDriver) checkAndMarkUnhealthy(
	ctx context.Context,
	instances []string,
	ssmSvc ssmCheckAPI,
	asgSvc asgHealthAPI,
	platform string,
) (markedUnhealthyCount int, checkedCount int, firstError error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}

	onlineIDs, err := filterOnlineSSMInstances(ctx, ssmSvc, instances)
	if err != nil {
		return 0, 0, fmt.Errorf("DescribeInstanceInformation failed: %w", err)
	}
	if offline := len(instances) - len(onlineIDs); offline > 0 {
		log.Printf("[Elastic CI Mode] SSM agent not online for %d of %d instance(s); skipping those", offline, len(instances))
	}
	if len(onlineIDs) == 0 {
		return 0, 0, nil
	}

	documentName := "AWS-RunShellScript"
	if platform == "windows" {
		documentName = "AWS-RunPowerShellScript"
	}
	// SendCommand accepts at most 50 instance IDs per call, so fan out one
	// command per batch and poll them together.
	// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html
	const sendCommandMaxTargets = 50
	var commandIDs []string
	for batch := range slices.Chunk(onlineIDs, sendCommandMaxTargets) {
		sendOut, err := ssmSvc.SendCommand(ctx, &ssm.SendCommandInput{
			InstanceIds:  batch,
			DocumentName: aws.String(documentName),
			Parameters:   map[string][]string{"commands": {a.getCheckCommand(platform)}},
			Comment:      aws.String("Check if buildkite-agent service is running"),
		})
		if err != nil {
			return 0, 0, fmt.Errorf("SendCommand failed: %w", err)
		}
		commandIDs = append(commandIDs, *sendOut.Command.CommandId)
	}

	// Run Command follows an eventual consistency model, so invocations may
	// not appear immediately after SendCommand returns. Wait once before the
	// first poll, then poll on a fixed interval up to a bounded deadline.
	// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetCommandInvocation.html
	registrationDelay := cmp.Or(a.ssmRegistrationDelay, 3*time.Second)
	pollInterval := cmp.Or(a.ssmPollInterval, 3*time.Second)
	pollDeadline := cmp.Or(a.ssmPollDeadline, 60*time.Second)
	time.Sleep(registrationDelay)
	results, pollErr := pollCommandInvocations(ctx, ssmSvc, commandIDs, len(onlineIDs), pollInterval, pollDeadline)
	if pollErr != nil {
		log.Printf("[Elastic CI Mode] ListCommandInvocations failed for commands %v: %v", commandIDs, pollErr)
		firstError = fmt.Errorf("ListCommandInvocations failed: %w", pollErr)
	}

	// Healthy and already-marked instances are the common, non-actionable
	// cases; collect them and log one summary line each instead of one line
	// per instance.
	var healthy, alreadyMarked []string

	for _, instanceID := range onlineIDs {
		inv, ok := results[instanceID]
		if !ok {
			log.Printf("[Elastic CI Mode] No invocation result for %s within deadline; skipping", instanceID)
			if firstError == nil {
				firstError = fmt.Errorf("no invocation result for %s", instanceID)
			}
			continue
		}
		switch inv.Status {
		case ssmTypes.CommandInvocationStatusPending,
			ssmTypes.CommandInvocationStatusInProgress,
			ssmTypes.CommandInvocationStatusDelayed,
			ssmTypes.CommandInvocationStatusCancelling:
			log.Printf("[Elastic CI Mode] Invocation for %s did not terminate (status: %s); skipping", instanceID, inv.Status)
			if firstError == nil {
				firstError = fmt.Errorf("invocation for %s did not terminate (status: %s)", instanceID, inv.Status)
			}
			continue
		}

		checkedCount++
		output := pluginOutput(inv)

		// Agent service isn't running (script printed NOT_RUNNING) or the
		// command itself failed (e.g. script error / unsupported platform).
		isDangling := inv.Status == ssmTypes.CommandInvocationStatusFailed ||
			(inv.Status == ssmTypes.CommandInvocationStatusSuccess && strings.Contains(output, "NOT_RUNNING"))

		if !isDangling {
			// A marker means a previous run already flagged this instance for
			// termination; the script exits before checking the agent, so we
			// can't claim it's running.
			if strings.Contains(output, "MARKER_EXISTS") {
				alreadyMarked = append(alreadyMarked, instanceID)
			} else {
				healthy = append(healthy, instanceID)
			}
			continue
		}

		log.Printf("[Elastic CI Mode] 🧟 Found dangling instance %s; output: %s", instanceID, output)
		if _, err := asgSvc.SetInstanceHealth(ctx, &autoscaling.SetInstanceHealthInput{
			InstanceId:   aws.String(instanceID),
			HealthStatus: aws.String("Unhealthy"),
		}); err != nil {
			log.Printf("[Elastic CI Mode] Failed to mark instance %s as unhealthy: %v", instanceID, err)
			if firstError == nil {
				firstError = fmt.Errorf("SetInstanceHealth failed for %s: %w", instanceID, err)
			}
		} else {
			log.Printf("[Elastic CI Mode] Marked instance %s as unhealthy", instanceID)
			markedUnhealthyCount++
		}
	}

	if len(healthy) > 0 {
		log.Printf("[Elastic CI Mode] %d instance(s) healthy: %v", len(healthy), healthy)
	}
	if len(alreadyMarked) > 0 {
		log.Printf("[Elastic CI Mode] ℹ️ %d instance(s) already marked for termination, skipping: %v", len(alreadyMarked), alreadyMarked)
	}

	return markedUnhealthyCount, checkedCount, firstError
}

func (a *ASGDriver) Describe(ctx context.Context) (AutoscaleGroupDetails, error) {
	log.Printf("Collecting AutoScaling details for ASG %q", a.Name)

	svc := autoscaling.NewFromConfig(a.Cfg)
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{
			a.Name,
		},
	}

	t := time.Now()

	result, err := svc.DescribeAutoScalingGroups(ctx, input)
	if err != nil {
		return AutoscaleGroupDetails{}, err
	}

	queryDuration := time.Since(t)

	asg := result.AutoScalingGroups[0]

	var pending int64
	var running int64
	for _, instance := range asg.Instances {
		lifecycleState := string(instance.LifecycleState)
		if strings.HasPrefix(lifecycleState, "Pending") {
			pending += 1
		}
		// Count instances in InService state
		if lifecycleState == "InService" {
			running += 1
		}
	}

	instanceIDs := make([]string, 0, len(asg.Instances))
	for _, instance := range asg.Instances {
		if instance.InstanceId != nil {
			instanceIDs = append(instanceIDs, *instance.InstanceId)
		}
	}

	details := AutoscaleGroupDetails{
		Pending:      pending,
		DesiredCount: int64(*result.AutoScalingGroups[0].DesiredCapacity),
		MinSize:      int64(*result.AutoScalingGroups[0].MinSize),
		MaxSize:      int64(*result.AutoScalingGroups[0].MaxSize),
		InstanceIDs:  instanceIDs,
		ActualCount:  running,
	}

	log.Printf("↳ Got pending=%d, desired=%d, actual=%d, min=%d, max=%d (took %v)",
		details.Pending, details.DesiredCount, details.ActualCount, details.MinSize, details.MaxSize, queryDuration)

	return details, nil
}

func (a *ASGDriver) SetDesiredCapacity(ctx context.Context, count int64) error {
	svc := autoscaling.NewFromConfig(a.Cfg)
	input := &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(a.Name),
		DesiredCapacity:      aws.Int32(int32(count)),
		HonorCooldown:        aws.Bool(false),
	}

	_, err := svc.SetDesiredCapacity(ctx, input)
	if err != nil {
		return err
	}

	return nil
}

func (a *ASGDriver) GetAutoscalingActivities(ctx context.Context, nextToken *string) (*autoscaling.DescribeScalingActivitiesOutput, error) {
	svc := autoscaling.NewFromConfig(a.Cfg)
	input := &autoscaling.DescribeScalingActivitiesInput{
		AutoScalingGroupName: aws.String(a.Name),
		NextToken:            nextToken,
	}
	return svc.DescribeScalingActivities(ctx, input)
}

func (a *ASGDriver) GetLastScalingInAndOutActivity(ctx context.Context, findScaleOut, findScaleIn bool) (*types.Activity, *types.Activity, error) {
	const scalingOutKey = "increasing the capacity"
	const shrinkingKey = "shrinking the capacity"
	var nextToken *string
	var lastScalingOutActivity *types.Activity
	var lastScalingInActivity *types.Activity
	hasFoundScalingActivities := false

	for i := 0; !hasFoundScalingActivities; {
		i++
		if a.MaxDescribeScalingActivitiesPages >= 0 && i >= a.MaxDescribeScalingActivitiesPages {
			return lastScalingOutActivity, lastScalingInActivity, fmt.Errorf("%d exceeds allowed pages for autoscaling:DescribeScalingActivities, %d", i, a.MaxDescribeScalingActivitiesPages)
		}

		output, err := a.GetAutoscalingActivities(ctx, nextToken)
		if err != nil {
			return lastScalingOutActivity, lastScalingInActivity, err
		}

		for _, activity := range output.Activities {
			// Convert StatusCode to string and check if it matches the successful status
			if string(activity.StatusCode) == activitySucessfulStatusCode &&
				strings.Contains(*activity.Cause, userRequestForChangingDesiredCapacity) {
				if lastScalingOutActivity == nil && strings.Contains(*activity.Cause, scalingOutKey) {
					lastScalingOutActivity = &activity
				} else if lastScalingInActivity == nil && strings.Contains(*activity.Cause, shrinkingKey) {
					lastScalingInActivity = &activity
				}
			}

			if findScaleOut && findScaleIn {
				hasFoundScalingActivities = lastScalingOutActivity != nil && lastScalingInActivity != nil
			} else if findScaleOut {
				hasFoundScalingActivities = lastScalingOutActivity != nil
			} else if findScaleIn {
				hasFoundScalingActivities = lastScalingInActivity != nil
			}
		}

		nextToken = output.NextToken
		if nextToken == nil {
			break
		}
	}

	return lastScalingOutActivity, lastScalingInActivity, nil
}

type dryRunASG struct {
}

func (a *ASGDriver) SendSIGTERMToAgents(ctx context.Context, instanceID string) error {
	ec2Client := ec2.NewFromConfig(a.Cfg)
	ssmClient := ssm.NewFromConfig(a.Cfg)

	// Detect platform - graceful SIGTERM is only supported on Linux
	descResp, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err == nil && len(descResp.Reservations) > 0 && len(descResp.Reservations[0].Instances) > 0 {
		instance := descResp.Reservations[0].Instances[0]
		if strings.EqualFold(string(instance.Platform), "windows") {
			return ErrWindowsGracefulScaleInNotSupported
		}
	}

	// Wait for SSM agent to be ready before sending command
	if err := a.waitForSSMReady(ctx, instanceID, 30*time.Second); err != nil {
		log.Printf("SSM agent not ready on instance %s, cannot send SIGTERM: %v", instanceID, err)
		return err
	}

	// With consecutive Lambda invocations the same instance selected for scale-in,
	// only during the first invocation will actually signal the agent to finish current jobs and stop.
	command := `#!/bin/bash
if [ -f /tmp/buildkite-agent-termination-marker ]; then
  echo "Already marked for termination, skipping"
  exit 0
fi
echo "Termination requested at $(date)" > /tmp/buildkite-agent-termination-marker
sudo systemctl stop buildkite-agent.service || sudo /opt/buildkite-agent/bin/buildkite-agent stop --signal SIGTERM
`
	log.Printf("[Elastic CI Mode] Sending SIGTERM to instance %s", instanceID)

	_, err = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters:   map[string][]string{"commands": {command}},
		Comment:      aws.String("Gracefully stop Buildkite agent"),
	})

	if err != nil {
		log.Printf("[Elastic CI Mode] Error sending SIGTERM to instance %s: %v", instanceID, err)
		return err
	}
	log.Printf("[Elastic CI Mode] Successfully sent SIGTERM command to instance %s", instanceID)
	return nil
}

// CleanupDanglingInstances finds and marks unhealthy any "zombie" instances where the
// buildkite-agent service has stopped running but the EC2 instance is still alive.
//
// This is different from normal scale-in:
// - Normal scale-in: instances are healthy, jobs may be running -> uses SIGTERM for graceful shutdown
// - This function: agent service is stopped, no jobs can be running -> safe to mark as unhealthy
//
// Marking instances unhealthy (via autoscaling:SetInstanceHealth) causes the ASG to terminate
// and replace them according to its configured policies.
func (a *ASGDriver) CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error {
	ec2Client := ec2.NewFromConfig(a.Cfg)
	ssmClient := ssm.NewFromConfig(a.Cfg)
	asgClient := autoscaling.NewFromConfig(a.Cfg)

	asgDetails, err := a.Describe(ctx)
	if err != nil {
		return fmt.Errorf("failed to describe ASG %s: %w", a.Name, err)
	}

	if len(asgDetails.InstanceIDs) == 0 {
		return nil
	}

	descInstancesOutput, err := describeInstancesTolerant(ctx, ec2Client, asgDetails.InstanceIDs, a.Name)
	if err != nil {
		return fmt.Errorf("failed to describe instances in ASG %s: %w", a.Name, err)
	}

	var instancesToConsiderChecking []ec2Types.Instance
	now := time.Now()
	for _, reservation := range descInstancesOutput.Reservations {
		for _, instance := range reservation.Instances {
			if instance.LaunchTime != nil && now.Sub(*instance.LaunchTime) >= minimumInstanceUptime {
				if instance.State != nil && instance.State.Name == ec2Types.InstanceStateNameRunning {
					instancesToConsiderChecking = append(instancesToConsiderChecking, instance)
				}
			}
		}
	}

	if len(instancesToConsiderChecking) == 0 {
		log.Printf("[Elastic CI Mode] None of the %d instance(s) met the dangling check criteria (uptime >= %v and state = running) — skipping", len(asgDetails.InstanceIDs), minimumInstanceUptime)
		return nil
	}

	// Detect platform from instances (each ASG is single-platform)
	platform := a.getASGPlatform(ctx, instancesToConsiderChecking)

	// Sort instances by launch time (oldest first) to prioritize checking older ones
	sort.SliceStable(instancesToConsiderChecking, func(i, j int) bool {
		return instancesToConsiderChecking[i].LaunchTime.Before(*instancesToConsiderChecking[j].LaunchTime)
	})

	totalMarkedUnhealthy := 0
	var firstErrorEncountered error

	// Pick a sliding slice so oldest-N instances stuck failing SSM checks
	// don't block the rest of the fleet from ever being examined.
	instancesToCheck := rotateInstanceWindow(instancesToConsiderChecking, maxDanglingInstancesToCheck, a.DanglingInstancesCheckInterval, time.Now())

	instancesForSSMCheck := make([]string, 0, len(instancesToCheck))
	for _, instance := range instancesToCheck {
		instancesForSSMCheck = append(instancesForSSMCheck, *instance.InstanceId)
	}

	totalChecked := 0

	if len(instancesForSSMCheck) > 0 {
		log.Printf("[Elastic CI Mode] Checking %d %s instance(s) for dangling agents: %v", len(instancesForSSMCheck), platform, instancesForSSMCheck)
		markedInCall, checkedInCall, errInCall := a.checkAndMarkUnhealthy(ctx, instancesForSSMCheck, ssmClient, asgClient, platform)
		totalMarkedUnhealthy += markedInCall
		totalChecked += checkedInCall
		if errInCall != nil {
			firstErrorEncountered = errInCall
		}
	}

	skipped := len(instancesForSSMCheck) - totalChecked
	if totalMarkedUnhealthy > 0 {
		log.Printf("[Elastic CI Mode] Dangling instance check: marked %d of %d checked instance(s) as unhealthy (%d skipped due to errors)", totalMarkedUnhealthy, totalChecked, skipped)
	} else if skipped > 0 {
		log.Printf("[Elastic CI Mode] Dangling instance check complete: %d of %d instance(s) checked and healthy, %d skipped due to errors", totalChecked, len(instancesForSSMCheck), skipped)
	} else {
		log.Printf("[Elastic CI Mode] Dangling instance check complete: all %d instance(s) healthy", totalChecked)
	}

	return firstErrorEncountered
}

// describeInstancesAPI is the subset of ec2.Client used by describeInstancesTolerant.
// Defined as an interface so tests can substitute a stub without wiring a full EC2 mock.
type describeInstancesAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// describeInstancesTolerant calls EC2 DescribeInstances and retries once with stale IDs
// removed if the API returns InvalidInstanceID.NotFound. This handles the race where an
// instance is terminated between the ASG Describe and the EC2 Describe. If all IDs are
// stale, an empty output is returned.
func describeInstancesTolerant(ctx context.Context, client describeInstancesAPI, instanceIDs []string, asgName string) (*ec2.DescribeInstancesOutput, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: instanceIDs})
	if err == nil {
		return out, nil
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
		return nil, err
	}

	stale := parseStaleInstanceIDs(apiErr.ErrorMessage())
	if len(stale) == 0 {
		return nil, err
	}

	staleSet := make(map[string]struct{}, len(stale))
	for _, id := range stale {
		staleSet[id] = struct{}{}
	}
	remaining := slices.DeleteFunc(slices.Clone(instanceIDs), func(id string) bool {
		_, ok := staleSet[id]
		return ok
	})
	log.Printf("[Elastic CI Mode] Skipping %d stale instance ID(s) in ASG %s (no longer present in EC2): %v", len(stale), asgName, stale)

	if len(remaining) == 0 {
		log.Printf("[Elastic CI Mode] All %d instance ID(s) in ASG %s are stale; nothing to check", len(instanceIDs), asgName)
		return &ec2.DescribeInstancesOutput{}, nil
	}

	return client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: remaining})
}

// rotateInstanceWindow returns windowSize instances from sorted, picked at a
// time-seeded offset that advances by windowSize every checkInterval.
// Returns sorted unchanged when it's already <= windowSize. checkInterval
// defaults to 60s when <= 0.
func rotateInstanceWindow(sorted []ec2Types.Instance, windowSize int, checkInterval time.Duration, now time.Time) []ec2Types.Instance {
	total := len(sorted)
	if windowSize <= 0 || total <= windowSize {
		return sorted
	}
	if checkInterval <= 0 {
		checkInterval = time.Minute
	}

	offset := int(now.UnixNano() / int64(checkInterval) * int64(windowSize) % int64(total))

	window := make([]ec2Types.Instance, windowSize)
	for i := range window {
		window[i] = sorted[(offset+i)%total]
	}
	return window
}

var staleInstanceIDRegex = regexp.MustCompile(`i-[0-9a-f]{8,17}`)

// parseStaleInstanceIDs extracts instance IDs from an InvalidInstanceID.NotFound message.
// Example: "The instance IDs 'i-0abc1234def5, i-0def5678abcd' do not exist"
func parseStaleInstanceIDs(msg string) []string {
	return staleInstanceIDRegex.FindAllString(msg, -1)
}

func (a *dryRunASG) Describe(ctx context.Context) (AutoscaleGroupDetails, error) {
	// In dry run mode, return empty details but ensure ActualCount is also set to 0
	return AutoscaleGroupDetails{
		ActualCount: 0,
	}, nil
}

func (a *dryRunASG) SetDesiredCapacity(ctx context.Context, count int64) error {
	return nil
}

func (a *dryRunASG) SendSIGTERMToAgents(ctx context.Context, instanceID string) error {
	log.Printf("[DryRun] Would send SIGTERM to instance %s", instanceID)
	return nil
}

func (a *dryRunASG) CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error {
	log.Printf("[DryRun] Would cleanup dangling instances (min uptime: %s, max check: %d)", minimumInstanceUptime, maxDanglingInstancesToCheck)
	return nil
}
