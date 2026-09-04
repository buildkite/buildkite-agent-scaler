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
	InstanceIDs  []string // InService instance IDs eligible for scale-in
	ActualCount  int64    // Actual number of running instances
	TotalCount   int64    // Total number of instances across all lifecycle states
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

// getASGPlatform detects whether the ASG contains Linux or Windows instances.
// Since each ASG is single-platform, we only need to check one instance.
func (a *ASGDriver) getASGPlatform(instances []ec2Types.Instance) string {
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

// terminationMarkerPath is created by the graceful-stop script sent from
// sendSIGTERMToAgentsBatch and probed by the dangling-instance check script,
// so both sides of the drain handshake must agree on it.
const terminationMarkerPath = "/tmp/buildkite-agent-termination-marker"

// SendCommand and DescribeInstanceInformation each accept at most 50 explicit
// instance IDs. Keep both operations on the same batch boundary.
const ssmMaxInstanceIDs = 50

// buildkiteAgentBinary is where the Elastic CI Stack installs the agent. The
// stop script falls back to it when systemctl can't stop the unit.
const buildkiteAgentBinary = "/opt/buildkite-agent/bin/buildkite-agent"

// getStopCommand returns the Linux script that asks the agent to drain and
// exit. Consecutive Lambda invocations can pick the same instance. Only the
// first command stops the agent; later ones see the marker and exit without
// disturbing the drain. The marker is written before the stop so the drain
// can see it, and removed if both stop commands fail, since a stale marker
// would block every retry.
func (a *ASGDriver) getStopCommand() string {
	return `#!/bin/bash
if [ -f ` + terminationMarkerPath + ` ]; then
  echo "Already marked for termination, skipping"
  exit 0
fi
echo "Termination requested at $(date)" > ` + terminationMarkerPath + `
if ! sudo systemctl stop buildkite-agent.service && ! sudo ` + buildkiteAgentBinary + ` stop --signal SIGTERM; then
  echo "Both stop commands failed, clearing termination marker so a later attempt can retry"
  rm -f ` + terminationMarkerPath + `
  exit 1
fi
`
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
if ! SERVICE_STATE=$(systemctl show buildkite-agent --property=ActiveState --property=MainPID 2>&1); then
  echo "UNKNOWN: $SERVICE_STATE"
  exit 0
fi

ACTIVE_STATE=$(printf '%s\n' "$SERVICE_STATE" | sed -n 's/^ActiveState=//p')
MAIN_PID=$(printf '%s\n' "$SERVICE_STATE" | sed -n 's/^MainPID=//p')
case "$ACTIVE_STATE:$MAIN_PID" in
  :*|*:|*:*[!0-9]*)
    echo "UNKNOWN: ActiveState=$ACTIVE_STATE MainPID=$MAIN_PID"
    exit 0
    ;;
esac

if [ "$MAIN_PID" != "0" ] || [ "$ACTIVE_STATE" = "activating" ] || [ "$ACTIVE_STATE" = "deactivating" ]; then
  if [ -f ` + terminationMarkerPath + ` ]; then
    echo "DRAINING: ActiveState=$ACTIVE_STATE MainPID=$MAIN_PID"
  else
    echo "RUNNING"
  fi
else
  echo "NOT_RUNNING: ActiveState=$ACTIVE_STATE"
fi
`
}

// ssmCheckAPI is the subset of ssm.Client used by the dangling-instance check,
// the graceful-stop dispatch and the single-instance probe, extracted so tests
// can stub it.
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
	var online []string
	for instanceBatch := range slices.Chunk(instanceIDs, ssmMaxInstanceIDs) {
		var nextToken *string
		for {
			resp, err := ssmSvc.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
				Filters: []ssmTypes.InstanceInformationStringFilter{
					{Key: aws.String("InstanceIds"), Values: instanceBatch},
				},
				MaxResults: aws.Int32(ssmMaxInstanceIDs),
				NextToken:  nextToken,
			})
			if err != nil {
				return nil, err
			}
			for _, info := range resp.InstanceInformationList {
				if info.PingStatus == ssmTypes.PingStatusOnline && info.InstanceId != nil {
					online = append(online, *info.InstanceId)
				}
			}
			if resp.NextToken == nil || *resp.NextToken == "" {
				break
			}
			nextToken = resp.NextToken
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

// danglingCheckResult counts the conclusive outcomes of one dangling-instance
// sweep. Instances whose probe failed, was ambiguous, or whose unhealthy mark
// could not be applied appear in no bucket; the caller derives the skipped
// count from the total it submitted.
type danglingCheckResult struct {
	healthy         int
	draining        int
	markedUnhealthy int
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
) (result danglingCheckResult, firstError error) {
	if len(instances) == 0 {
		return danglingCheckResult{}, nil
	}

	onlineIDs, err := filterOnlineSSMInstances(ctx, ssmSvc, instances)
	if err != nil {
		return danglingCheckResult{}, fmt.Errorf("DescribeInstanceInformation failed: %w", err)
	}
	if offline := len(instances) - len(onlineIDs); offline > 0 {
		log.Printf("[Elastic CI Mode] SSM agent not online for %d of %d instance(s); skipping those", offline, len(instances))
	}
	if len(onlineIDs) == 0 {
		return danglingCheckResult{}, nil
	}

	documentName := "AWS-RunShellScript"
	if platform == "windows" {
		documentName = "AWS-RunPowerShellScript"
	}
	// SendCommand accepts at most 50 instance IDs per call, so fan out one
	// command per batch and poll them together.
	// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html
	var commandIDs []string
	for batch := range slices.Chunk(onlineIDs, ssmMaxInstanceIDs) {
		sendOut, err := ssmSvc.SendCommand(ctx, &ssm.SendCommandInput{
			InstanceIds:  batch,
			DocumentName: aws.String(documentName),
			Parameters:   map[string][]string{"commands": {a.getCheckCommand(platform)}},
			Comment:      aws.String("Check if buildkite-agent service is running"),
		})
		if err != nil {
			return danglingCheckResult{}, fmt.Errorf("SendCommand failed: %w", err)
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

	// Healthy and draining instances are the common, non-actionable
	// cases; collect them and log one summary line each instead of one line
	// per instance.
	var healthy, draining []string

	for _, instanceID := range onlineIDs {
		inv, ok := results[instanceID]
		if !ok {
			log.Printf("[Elastic CI Mode] No invocation result for %s within deadline; skipping", instanceID)
			if firstError == nil {
				firstError = fmt.Errorf("no invocation result for %s", instanceID)
			}
			continue
		}

		output := strings.TrimSpace(pluginOutput(inv))
		if inv.Status != ssmTypes.CommandInvocationStatusSuccess {
			log.Printf("[Elastic CI Mode] Invocation for %s was inconclusive (status: %s, details: %s, output: %q); skipping", instanceID, inv.Status, aws.ToString(inv.StatusDetails), output)
			if firstError == nil {
				firstError = fmt.Errorf("invocation for %s was inconclusive (status: %s, details: %s)", instanceID, inv.Status, aws.ToString(inv.StatusDetails))
			}
			continue
		}

		switch {
		case output == "RUNNING":
			healthy = append(healthy, instanceID)
			continue
		case output == "DRAINING" || strings.HasPrefix(output, "DRAINING:"):
			draining = append(draining, instanceID)
			continue
		case output == "NOT_RUNNING" || strings.HasPrefix(output, "NOT_RUNNING:"):
			// A successful probe found no agent process, so there are no jobs
			// to drain before the ASG replaces this instance.
		default:
			log.Printf("[Elastic CI Mode] Invocation for %s returned inconclusive output %q; skipping", instanceID, output)
			if firstError == nil {
				firstError = fmt.Errorf("invocation for %s returned inconclusive output %q", instanceID, output)
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
			result.markedUnhealthy++
		}
	}

	if len(healthy) > 0 {
		log.Printf("[Elastic CI Mode] %d instance(s) healthy: %v", len(healthy), healthy)
	}
	if len(draining) > 0 {
		log.Printf("[Elastic CI Mode] ℹ️ %d instance(s) draining: %v", len(draining), draining)
	}

	result.healthy = len(healthy)
	result.draining = len(draining)
	return result, firstError
}

func autoscaleGroupDetails(asg types.AutoScalingGroup) AutoscaleGroupDetails {
	details := AutoscaleGroupDetails{
		DesiredCount: int64(*asg.DesiredCapacity),
		MinSize:      int64(*asg.MinSize),
		MaxSize:      int64(*asg.MaxSize),
		InstanceIDs:  make([]string, 0, len(asg.Instances)),
		TotalCount:   int64(len(asg.Instances)),
	}

	for _, instance := range asg.Instances {
		lifecycleState := string(instance.LifecycleState)
		if strings.HasPrefix(lifecycleState, "Pending") {
			details.Pending++
		}
		if lifecycleState != "InService" {
			continue
		}

		details.ActualCount++
		if instance.InstanceId != nil {
			details.InstanceIDs = append(details.InstanceIDs, *instance.InstanceId)
		}
	}

	return details
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
	details := autoscaleGroupDetails(asg)

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

// scalingActivitiesAPI is the subset of autoscaling.Client used to read the
// group's activity history.
type scalingActivitiesAPI interface {
	DescribeScalingActivities(ctx context.Context, params *autoscaling.DescribeScalingActivitiesInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeScalingActivitiesOutput, error)
}

// isUserRequestedScaleIn reports whether an activity shrank the group through
// a desired-capacity change, which is how the scaler scales in outside
// Elastic CI Mode. Instances that terminate with
// --should-decrement-desired-capacity are deliberately not matched: an agent
// idling out writes the same cause as one we asked to stop, so treating them
// as scale-ins would make an idle-out restart the cooldown.
func isUserRequestedScaleIn(cause string) bool {
	return strings.Contains(cause, shrinkingKey) &&
		strings.Contains(cause, userRequestForChangingDesiredCapacity)
}

// GetLastScalingInAndOutActivity finds the most recent successful scale-out
// and user-requested scale-in that started at or after since, walking at most
// MaxDescribeScalingActivitiesPages pages. A zero since means no lower bound.
// This seeds the cooldowns across Lambda cold starts.
func (a *ASGDriver) GetLastScalingInAndOutActivity(ctx context.Context, findScaleOut, findScaleIn bool, since time.Time) (*types.Activity, *types.Activity, error) {
	return a.lastScalingActivities(ctx, autoscaling.NewFromConfig(a.Cfg), findScaleOut, findScaleIn, since, a.MaxDescribeScalingActivitiesPages)
}

// lastScalingActivities walks the group's successful activities, newest
// first, until it has found what was asked for or runs out of pages. A
// non-positive maxPages means unlimited. Running out of pages before finding
// a match isn't an error; hitting the cap is, because the answer is unknown.
func (a *ASGDriver) lastScalingActivities(ctx context.Context, svc scalingActivitiesAPI, findScaleOut, findScaleIn bool, since time.Time, maxPages int) (*types.Activity, *types.Activity, error) {
	if !findScaleOut && !findScaleIn {
		return nil, nil, nil
	}

	filters := []types.Filter{
		{Name: aws.String("Status"), Values: []string{activitySucessfulStatusCode}},
	}
	if !since.IsZero() {
		filters = append(filters, types.Filter{
			Name:   aws.String("StartTimeLowerBound"),
			Values: []string{since.UTC().Format(time.RFC3339)},
		})
	}

	var nextToken *string
	var lastScalingOutActivity *types.Activity
	var lastScalingInActivity *types.Activity

	for page := 0; ; page++ {
		if maxPages > 0 && page >= maxPages {
			return lastScalingOutActivity, lastScalingInActivity, fmt.Errorf("autoscaling:DescribeScalingActivities exceeded maximum of %d pages", maxPages)
		}

		output, err := svc.DescribeScalingActivities(ctx, &autoscaling.DescribeScalingActivitiesInput{
			AutoScalingGroupName: aws.String(a.Name),
			Filters:              filters,
			MaxRecords:           aws.Int32(100),
			NextToken:            nextToken,
		})
		if err != nil {
			return lastScalingOutActivity, lastScalingInActivity, err
		}

		for i := range output.Activities {
			activity := &output.Activities[i]
			cause := aws.ToString(activity.Cause)
			if findScaleOut && lastScalingOutActivity == nil &&
				strings.Contains(cause, userRequestForChangingDesiredCapacity) &&
				strings.Contains(cause, scalingOutKey) {
				lastScalingOutActivity = activity
			}
			if findScaleIn && lastScalingInActivity == nil && isUserRequestedScaleIn(cause) {
				lastScalingInActivity = activity
			}
		}

		if (!findScaleOut || lastScalingOutActivity != nil) &&
			(!findScaleIn || lastScalingInActivity != nil) {
			return lastScalingOutActivity, lastScalingInActivity, nil
		}

		nextToken = output.NextToken
		if nextToken == nil {
			return lastScalingOutActivity, lastScalingInActivity, nil
		}
	}
}

type dryRunASG struct {
}

// SendSIGTERMToAgentsBatch submits the graceful-stop command for all reachable
// Linux instances without waiting for command completion. Elastic CI Stack
// agents self-terminate and decrement desired capacity after draining. The
// returned count is the number of instances included in accepted commands.
func (a *ASGDriver) SendSIGTERMToAgentsBatch(ctx context.Context, instanceIDs []string) (int, error) {
	if len(instanceIDs) == 0 {
		return 0, nil
	}

	platform, err := a.detectPlatform(ctx, ec2.NewFromConfig(a.Cfg), instanceIDs)
	if err != nil {
		// The instance owns the capacity decrement on Linux while Windows
		// needs the SetDesiredCapacity fallback, so guessing the platform
		// wrong loses the decrement. Fail closed and retry next run.
		return 0, fmt.Errorf("detect platform for graceful scale-in: %w", err)
	}

	return a.sendSIGTERMToAgentsBatch(ctx, ssm.NewFromConfig(a.Cfg), instanceIDs, platform)
}

// detectPlatform inspects one scale-in candidate to choose the SSM document
// platform. ASGs are single-platform, so one instance is enough.
func (a *ASGDriver) detectPlatform(ctx context.Context, client describeInstancesAPI, instanceIDs []string) (string, error) {
	describeResult, err := describeInstancesTolerant(ctx, client, instanceIDs[:1], a.Name)
	if err != nil {
		return "", err
	}
	var instances []ec2Types.Instance
	for _, reservation := range describeResult.Reservations {
		instances = append(instances, reservation.Instances...)
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceIDs[0])
	}
	return a.getASGPlatform(instances), nil
}

func (a *ASGDriver) sendSIGTERMToAgentsBatch(ctx context.Context, ssmSvc ssmCheckAPI, instanceIDs []string, platform string) (int, error) {
	if strings.EqualFold(platform, "windows") {
		return 0, ErrWindowsGracefulScaleInNotSupported
	}

	onlineIDs, err := filterOnlineSSMInstances(ctx, ssmSvc, instanceIDs)
	if err != nil {
		return 0, fmt.Errorf("describe SSM instance information: %w", err)
	}
	if offline := len(instanceIDs) - len(onlineIDs); offline > 0 {
		log.Printf("[Elastic CI Mode] SSM agent not online for %d of %d graceful scale-in candidate(s); skipping those", offline, len(instanceIDs))
	}
	if len(onlineIDs) == 0 {
		return 0, nil
	}

	command := a.getStopCommand()

	var sendErrors []error
	accepted := 0
	for instanceBatch := range slices.Chunk(onlineIDs, ssmMaxInstanceIDs) {
		_, err := ssmSvc.SendCommand(ctx, &ssm.SendCommandInput{
			InstanceIds:  instanceBatch,
			DocumentName: aws.String("AWS-RunShellScript"),
			Parameters:   map[string][]string{"commands": {command}},
			Comment:      aws.String("Gracefully stop Buildkite agent"),
			MaxErrors:    aws.String("100%"),
		})
		if err != nil {
			sendErrors = append(sendErrors, err)
			continue
		}
		accepted += len(instanceBatch)
		log.Printf("[Elastic CI Mode] Submitted graceful-stop command to %d instance(s)", len(instanceBatch))
	}

	return accepted, errors.Join(sendErrors...)
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
	platform := a.getASGPlatform(instancesToConsiderChecking)

	// Sort instances by launch time (oldest first) to prioritize checking older ones
	sort.SliceStable(instancesToConsiderChecking, func(i, j int) bool {
		return instancesToConsiderChecking[i].LaunchTime.Before(*instancesToConsiderChecking[j].LaunchTime)
	})

	// Pick a sliding slice so oldest-N instances stuck failing SSM checks
	// don't block the rest of the fleet from ever being examined.
	instancesToCheck := rotateInstanceWindow(instancesToConsiderChecking, maxDanglingInstancesToCheck, a.DanglingInstancesCheckInterval, time.Now())

	instancesForSSMCheck := make([]string, 0, len(instancesToCheck))
	for _, instance := range instancesToCheck {
		instancesForSSMCheck = append(instancesForSSMCheck, *instance.InstanceId)
	}

	log.Printf("[Elastic CI Mode] Checking %d %s instance(s) for dangling agents: %v", len(instancesForSSMCheck), platform, instancesForSSMCheck)
	result, checkErr := a.checkAndMarkUnhealthy(ctx, instancesForSSMCheck, ssmClient, asgClient, platform)
	skipped := len(instancesForSSMCheck) - result.healthy - result.draining - result.markedUnhealthy
	log.Printf("[Elastic CI Mode] Dangling instance check complete: %d healthy, %d draining, %d marked unhealthy, %d skipped (of %d instance(s))", result.healthy, result.draining, result.markedUnhealthy, skipped, len(instancesForSSMCheck))

	return checkErr
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

func (a *dryRunASG) SendSIGTERMToAgentsBatch(ctx context.Context, instanceIDs []string) (int, error) {
	log.Printf("[DryRun] Would send SIGTERM to instances %v", instanceIDs)
	return len(instanceIDs), nil
}

func (a *dryRunASG) CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error {
	log.Printf("[DryRun] Would cleanup dangling instances (min uptime: %s, max check: %d)", minimumInstanceUptime, maxDanglingInstancesToCheck)
	return nil
}
