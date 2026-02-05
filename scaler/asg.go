package scaler

import (
	"context"
	"fmt"
	"log"
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
)

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
	MaxDanglingInstancesToCheck       int // Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)
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
  *) echo "NOT_RUNNING" ;;
esac
`
}

// checkAndMarkUnhealthy uses SSM to check if buildkite-agent is running on each instance.
// Instances where the agent service is not running are marked unhealthy via the ASG API,
// which will cause the ASG to terminate and replace them.
//
// This is safe to use without graceful shutdown because:
// - We only mark unhealthy if the agent service is NOT running
// - If the agent service is not running, there cannot be any jobs in progress
// - This is different from normal scale-in which uses SIGTERM for graceful shutdown
//
// This function is specifically for "zombie" instances where the EC2 instance is running
// but the buildkite-agent process has died (e.g., due to crashes, OOM, or other failures).
func (a *ASGDriver) checkAndMarkUnhealthy(
	ctx context.Context,
	instances []string,
	ssmSvc *ssm.Client,
	asgSvc *autoscaling.Client,
	platform string,
) (markedUnhealthyCount int, firstError error) {
	checkCommand := a.getCheckCommand(platform)

	// Use the appropriate SSM document based on platform
	documentName := "AWS-RunShellScript"
	if platform == "windows" {
		documentName = "AWS-RunPowerShellScript"
	}

	for _, instanceID := range instances {
		checkInput := &ssm.SendCommandInput{
			InstanceIds:  []string{instanceID},
			DocumentName: aws.String(documentName),
			Parameters: map[string][]string{
				"commands": {checkCommand},
			},
			Comment: aws.String("Check if buildkite-agent service is running"),
		}

		if err := a.waitForSSMReady(ctx, instanceID, 2*time.Minute); err != nil {
			log.Printf("[Elastic CI Mode] SSM agent never came online for %s: %v; skipping check for this instance", instanceID, err)
			if firstError == nil {
				firstError = fmt.Errorf("SSM agent not ready for %s: %w", instanceID, err)
			}
			continue // to the next instance in the 'instances' slice
		}

		checkOutput, err := ssmSvc.SendCommand(ctx, checkInput)
		if err != nil {
			log.Printf("[Elastic CI Mode] Warning: Could not send check command to instance %s: %v", instanceID, err)
			if firstError == nil {
				firstError = fmt.Errorf("SendCommand failed for %s: %w", instanceID, err)
			}
			continue // to the next instance
		}

		time.Sleep(3 * time.Second) // Give SSM time to run and report

		checkResultInput := &ssm.GetCommandInvocationInput{
			CommandId:  checkOutput.Command.CommandId,
			InstanceId: aws.String(instanceID),
		}

		var checkCmdResult *ssm.GetCommandInvocationOutput
		// Retry GetCommandInvocation a few times as it might not be immediately available
		for i := 0; i < 3; i++ {
			checkCmdResult, err = ssmSvc.GetCommandInvocation(ctx, checkResultInput)
			if err == nil && checkCmdResult.Status != ssmTypes.CommandInvocationStatusPending && checkCmdResult.Status != ssmTypes.CommandInvocationStatusInProgress {
				break
			}
			if err != nil {
				log.Printf("[Elastic CI Mode] Retrying GetCommandInvocation for %s (attempt %d): %v", instanceID, i+1, err)
			}
			time.Sleep(2 * time.Second)
		}

		if err != nil {
			log.Printf("[Elastic CI Mode] Warning: Could not get check result for instance %s after retries: %v", instanceID, err)
			if firstError == nil {
				firstError = fmt.Errorf("GetCommandInvocation failed for %s: %w", instanceID, err)
			}
			continue // to the next instance
		}

		// If command failed or agent service isn't running (based on script's exit code or output)
		if checkCmdResult.Status == ssmTypes.CommandInvocationStatusFailed ||
			(checkCmdResult.Status == ssmTypes.CommandInvocationStatusSuccess && checkCmdResult.StandardOutputContent != nil && strings.Contains(*checkCmdResult.StandardOutputContent, "NOT_RUNNING")) {

			// Skip if it's already been marked for termination or is activating (based on script's output)
			if checkCmdResult.StandardOutputContent != nil &&
				(strings.Contains(*checkCmdResult.StandardOutputContent, "MARKER_EXISTS") || strings.Contains(*checkCmdResult.StandardOutputContent, "ACTIVATING")) {
				log.Printf("[Elastic CI Mode] ℹ️ Instance %s has buildkite-agent in transition state (marker exists or activating), not a dangling instance", instanceID)
				if checkCmdResult.StandardOutputContent != nil {
					log.Printf("[Elastic CI Mode] Service status details for %s: %s", instanceID, *checkCmdResult.StandardOutputContent)
				}
				continue // to the next instance
			}

			log.Printf("[Elastic CI Mode] 🧟 Found dangling instance %s - buildkite-agent service is not running or check command failed", instanceID)
			if checkCmdResult.StandardOutputContent != nil {
				log.Printf("[Elastic CI Mode] Service status for %s: %s", instanceID, *checkCmdResult.StandardOutputContent)
			}

			// Mark instance as unhealthy so ASG will terminate it
			_, err := asgSvc.SetInstanceHealth(ctx, &autoscaling.SetInstanceHealthInput{
				InstanceId:   aws.String(instanceID),
				HealthStatus: aws.String("Unhealthy"),
			})

			if err != nil {
				log.Printf("[Elastic CI Mode] Error: Failed to mark instance %s as unhealthy: %v", instanceID, err)
				if firstError == nil {
					firstError = fmt.Errorf("SetInstanceHealth failed for %s: %w", instanceID, err)
				}
			} else {
				log.Printf("[Elastic CI Mode] Successfully marked instance %s as unhealthy", instanceID)
				markedUnhealthyCount++
			}
		} else if checkCmdResult.Status != ssmTypes.CommandInvocationStatusSuccess {
			log.Printf("[Elastic CI Mode] Agent status check command for %s did not succeed (status: %s). Output: %s", instanceID, checkCmdResult.Status, aws.ToString(checkCmdResult.StandardOutputContent))
		}
	}

	return markedUnhealthyCount, firstError
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
	ssmClient := ssm.NewFromConfig(a.Cfg)

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

	_, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
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

	descInstancesOutput, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: asgDetails.InstanceIDs,
	})
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
		return nil
	}

	// Detect platform from instances (each ASG is single-platform)
	platform := a.getASGPlatform(ctx, instancesToConsiderChecking)

	// Sort instances by launch time (oldest first) to prioritize checking older ones
	sort.SliceStable(instancesToConsiderChecking, func(i, j int) bool {
		return instancesToConsiderChecking[i].LaunchTime.Before(*instancesToConsiderChecking[j].LaunchTime)
	})

	checkedCount := 0
	totalMarkedUnhealthy := 0
	var firstErrorEncountered error

	instancesForSSMCheck := make([]string, 0)
	for i := 0; i < len(instancesToConsiderChecking) && (maxDanglingInstancesToCheck <= 0 || checkedCount < maxDanglingInstancesToCheck); i++ {
		instance := instancesToConsiderChecking[i]
		instanceID := *instance.InstanceId
		instancesForSSMCheck = append(instancesForSSMCheck, instanceID)
		checkedCount++
	}

	if len(instancesForSSMCheck) > 0 {
		log.Printf("[Elastic CI Mode] Checking %d %s instance(s) for dangling agents: %v", len(instancesForSSMCheck), platform, instancesForSSMCheck)
		markedInCall, errInCall := a.checkAndMarkUnhealthy(ctx, instancesForSSMCheck, ssmClient, asgClient, platform)
		totalMarkedUnhealthy += markedInCall
		if errInCall != nil {
			firstErrorEncountered = errInCall
		}
	}

	// Only log summary when we actually marked instances unhealthy
	if totalMarkedUnhealthy > 0 {
		log.Printf("[Elastic CI Mode] Dangling instance check: marked %d instance(s) as unhealthy", totalMarkedUnhealthy)
	}

	return firstErrorEncountered
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
