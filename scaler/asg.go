package scaler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ssm"
)

const (
	activitySucessfulStatusCode           = "Successful"
	userRequestForChangingDesiredCapacity = "a user request explicitly set group desired capacity changing the desired capacity"
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
	Sess                              *session.Session
	MaxDescribeScalingActivitiesPages int
	ElasticCIMode                     bool
	MinimumInstanceUptime             time.Duration
	MaxDanglingInstancesToCheck       int // Maximum number of instances to check for dangling instances (only used for dangling instance scanning, not for normal scale-in)
}

// waitForSSMReady blocks until the SSM agent on instanceID reports PingStatus="Online",
// or until timeout elapses.
func (a *ASGDriver) waitForSSMReady(instanceID string, timeout time.Duration) error {
	ssmSvc := ssm.New(a.Sess)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := ssmSvc.DescribeInstanceInformation(&ssm.DescribeInstanceInformationInput{
			Filters: []*ssm.InstanceInformationStringFilter{
				{
					Key:    aws.String("InstanceIds"),
					Values: []*string{aws.String(instanceID)},
				},
			},
		})
		if err != nil {
			log.Printf("[SSM] DescribeInstanceInformation failed for %s: %v", instanceID, err)
		} else if len(resp.InstanceInformationList) > 0 &&
			aws.StringValue(resp.InstanceInformationList[0].PingStatus) == "Online" {
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timed out waiting for SSM agent to become ready on %s", instanceID)
}

// CleanupDanglingInstances checks for instances where buildkite-agent service is not running
// and terminates them. This is only enabled in ElasticCIMode.
// It will check up to MaxDanglingInstancesToCheck oldest instances for dangling instances (to limit API calls)
// and only considers instances with uptime >= MinimumInstanceUptime.
// Note: MaxDanglingInstancesToCheck is only used for dangling instance scanning, not for normal scale-in operations.
func (a *ASGDriver) CleanupDanglingInstances() error {
	if !a.ElasticCIMode {
		return nil // Only perform dangling instance cleanup in Elastic CI Mode
	}

	// Use default value if not set
	maxToCheck := a.MaxDanglingInstancesToCheck
	if maxToCheck <= 0 {
		maxToCheck = 5
	}

	log.Printf("[Elastic CI Mode] Checking for dangling instances in ASG %s", a.Name)

	asgDetails, err := a.Describe()
	if err != nil {
		return fmt.Errorf("failed to describe ASG for dangling instance cleanup: %v", err)
	}

	if len(asgDetails.InstanceIDs) == 0 {
		return nil
	}

	ssmSvc := ssm.New(a.Sess)
	ec2Svc := ec2.New(a.Sess)

	type instanceInfo struct {
		ID         string
		LaunchTime time.Time
	}
	instances := make([]instanceInfo, 0, len(asgDetails.InstanceIDs))
	describeResult, err := ec2Svc.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice(asgDetails.InstanceIDs),
	})

	if err != nil {
		log.Printf("[Elastic CI Mode] Warning: Could not get instance launch times: %v", err)
		// Fallback to raw IDs if needed
		instancesToCheck := asgDetails.InstanceIDs
		if len(instancesToCheck) > maxToCheck {
			instancesToCheck = instancesToCheck[:maxToCheck]
		}
		return a.checkAndTerminate(instancesToCheck, ssmSvc, ec2Svc, asgDetails)
	}

	for _, reservation := range describeResult.Reservations {
		for _, inst := range reservation.Instances {
			if inst.InstanceId != nil && inst.LaunchTime != nil {
				instances = append(instances, instanceInfo{ID: *inst.InstanceId, LaunchTime: *inst.LaunchTime})
			}
		}
	}

	cutoff := time.Now().Add(-a.MinimumInstanceUptime)
	eligible := make([]instanceInfo, 0, len(instances))
	skipped := make([]string, 0)
	for _, inst := range instances {
		if inst.LaunchTime.Before(cutoff) {
			eligible = append(eligible, inst)
		} else {
			skipped = append(skipped, inst.ID)
		}
	}

	if len(skipped) > 0 {
		log.Printf("[Elastic CI Mode] Skipping %d instances with uptime < %s: %v", len(skipped), a.MinimumInstanceUptime, skipped)
	}

	if len(eligible) == 0 {
		return nil
	}

	// Sort by launch time (oldest first)
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].LaunchTime.Before(eligible[j].LaunchTime)
	})

	limit := maxToCheck
	if len(eligible) < limit {
		limit = len(eligible)
	}
	instancesToCheck := make([]string, limit)
	for i := 0; i < limit; i++ {
		instancesToCheck[i] = eligible[i].ID
	}

	log.Printf("[Elastic CI Mode] Checking up to %d oldest eligible instances: %v", limit, instancesToCheck)

	return a.checkAndTerminate(instancesToCheck, ssmSvc, ec2Svc, asgDetails)
}

// checkAndTerminate runs the existing SSM check + terminate logic on a slice of instance IDs.
// The number of instances to check is already limited by the caller.
func (a *ASGDriver) checkAndTerminate(
	instances []string,
	ssmSvc *ssm.SSM,
	ec2Svc *ec2.EC2,
	asgDetails AutoscaleGroupDetails,
) error {
	terminated := 0

	for _, instanceID := range instances {

		checkCommand := `
#!/bin/bash
# Check if buildkite-agent service is running or has been marked for termination
# Note: Even after SIGTERM, the service status may still show as "active" and "running"
# until jobs complete, so we primarily rely on the termination marker file.

if [ -f /tmp/buildkite-agent-termination-marker ]; then
  echo "MARKER_EXISTS: Instance is already marked for termination"
  cat /tmp/buildkite-agent-termination-marker
  exit 0
fi

ACTIVE_STATE=$(systemctl show buildkite-agent -p ActiveState | cut -d= -f2)
SUB_STATE=$(systemctl show buildkite-agent -p SubState | cut -d= -f2)

echo "Service status: ActiveState=$ACTIVE_STATE SubState=$SUB_STATE"

case "$ACTIVE_STATE" in
  "active")
    echo "RUNNING: Service is active ($SUB_STATE)"
    exit 0
    ;;
  "activating")
    echo "ACTIVATING: Service is starting"
    exit 0
    ;;
  *)
    # Service is not running
    echo "Detailed service information:"
    systemctl status buildkite-agent --no-pager || true
    echo "NOT_RUNNING: Service is $ACTIVE_STATE/$SUB_STATE"
    exit 1
    ;;
esac
`

		checkInput := &ssm.SendCommandInput{
			InstanceIds:  []*string{aws.String(instanceID)},
			DocumentName: aws.String("AWS-RunShellScript"),
			Parameters: map[string][]*string{
				"commands": {aws.String(checkCommand)},
			},
			Comment: aws.String("Check if buildkite-agent service is running"),
		}

		if err := a.waitForSSMReady(instanceID, 2*time.Minute); err != nil {
			log.Printf("[Elastic CI Mode] SSM agent never came online for %s: %v; skipping", instanceID, err)
			continue
		}

		checkOutput, err := ssmSvc.SendCommand(checkInput)
		if err != nil {
			log.Printf("[Elastic CI Mode] Warning: Could not send check command to instance %s: %v", instanceID, err)
			continue
		}

		time.Sleep(3 * time.Second)

		checkResult, err := ssmSvc.GetCommandInvocation(&ssm.GetCommandInvocationInput{
			CommandId:  checkOutput.Command.CommandId,
			InstanceId: aws.String(instanceID),
		})

		if err != nil {
			log.Printf("[Elastic CI Mode] Warning: Could not get check result for instance %s: %v", instanceID, err)
			continue
		}

		// If command failed or agent service isn't running
		if checkResult.Status != nil && (*checkResult.Status == "Failed" ||
			(*checkResult.Status == "Success" && strings.Contains(*checkResult.StandardOutputContent, "NOT_RUNNING"))) {

			// Skip if it's already been marked for termination or is activating
			if strings.Contains(*checkResult.StandardOutputContent, "MARKER_EXISTS") ||
				strings.Contains(*checkResult.StandardOutputContent, "ACTIVATING") {
				log.Printf("[Elastic CI Mode] ℹ️ Instance %s has buildkite-agent in transition state, not a dangling instance", instanceID)
				if checkResult.StandardOutputContent != nil {
					log.Printf("[Elastic CI Mode] Service status details: %s", *checkResult.StandardOutputContent)
				}
				continue
			}

			log.Printf("[Elastic CI Mode] 🧟 Found dangling instance %s - buildkite-agent service is not running", instanceID)
			if checkResult.StandardOutputContent != nil {
				log.Printf("[Elastic CI Mode] Service status: %s", *checkResult.StandardOutputContent)
			}

			_, err = ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
				InstanceIds: []*string{aws.String(instanceID)},
			})

			if err != nil {
				log.Printf("[Elastic CI Mode] Error: Failed to terminate dangling instance %s: %v", instanceID, err)
			} else {
				log.Printf("[Elastic CI Mode] Successfully terminated dangling instance %s via EC2 API", instanceID)
				terminated++
			}
		}
	}

	return nil
}

func (a *ASGDriver) Describe() (AutoscaleGroupDetails, error) {
	log.Printf("Collecting AutoScaling details for ASG %q", a.Name)

	svc := autoscaling.New(a.Sess)
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{
			aws.String(a.Name),
		},
	}

	t := time.Now()

	result, err := svc.DescribeAutoScalingGroups(input)
	if err != nil {
		return AutoscaleGroupDetails{}, err
	}

	queryDuration := time.Since(t)

	asg := result.AutoScalingGroups[0]

	var pending int64
	var running int64
	for _, instance := range asg.Instances {
		lifecycleState := aws.StringValue(instance.LifecycleState)
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

func (a *ASGDriver) SetDesiredCapacity(count int64) error {
	svc := autoscaling.New(a.Sess)
	input := &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(a.Name),
		DesiredCapacity:      aws.Int64(count),
		HonorCooldown:        aws.Bool(false),
	}

	_, err := svc.SetDesiredCapacity(input)
	if err != nil {
		return err
	}

	return nil
}

func (a *ASGDriver) GetAutoscalingActivities(ctx context.Context, nextToken *string) (*autoscaling.DescribeScalingActivitiesOutput, error) {
	svc := autoscaling.New(a.Sess)
	input := &autoscaling.DescribeScalingActivitiesInput{
		AutoScalingGroupName: aws.String(a.Name),
		NextToken:            nextToken,
	}
	return svc.DescribeScalingActivitiesWithContext(ctx, input)
}

func (a *ASGDriver) GetLastScalingInAndOutActivity(ctx context.Context, findScaleOut, findScaleIn bool) (*autoscaling.Activity, *autoscaling.Activity, error) {
	const scalingOutKey = "increasing the capacity"
	const shrinkingKey = "shrinking the capacity"
	var nextToken *string
	var lastScalingOutActivity *autoscaling.Activity
	var lastScalingInActivity *autoscaling.Activity
	hasFoundScalingActivities := false
	for i := 0; !hasFoundScalingActivities; {
		i++
		if a.MaxDescribeScalingActivitiesPages >= 0 && i >= a.MaxDescribeScalingActivitiesPages {
			return lastScalingOutActivity, lastScalingInActivity, fmt.Errorf("%d exceedes allowed pages for autoscaling:DescribeScalingActivities, %d", i, a.MaxDescribeScalingActivitiesPages)
		}

		output, err := a.GetAutoscalingActivities(ctx, nextToken)
		if err != nil {
			return lastScalingOutActivity, lastScalingInActivity, err
		}

		for _, activity := range output.Activities {
			// Filter for successful activity and explicit desired count changes
			if *activity.StatusCode == activitySucessfulStatusCode &&
				strings.Contains(*activity.Cause, userRequestForChangingDesiredCapacity) {
				if lastScalingOutActivity == nil && strings.Contains(*activity.Cause, scalingOutKey) {
					lastScalingOutActivity = activity
				} else if lastScalingInActivity == nil && strings.Contains(*activity.Cause, shrinkingKey) {
					lastScalingInActivity = activity
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

func (a *dryRunASG) Describe() (AutoscaleGroupDetails, error) {
	// In dry run mode, return empty details but ensure ActualCount is also set to 0
	return AutoscaleGroupDetails{
		ActualCount: 0,
	}, nil
}

func (a *dryRunASG) SetDesiredCapacity(count int64) error {
	return nil
}

func (a *dryRunASG) SendSIGTERMToAgents(instanceID string) error {
	log.Printf("DRY RUN: Would send SIGTERM to buildkite agents on instance %s", instanceID)
	return nil
}

func (a *dryRunASG) CleanupDanglingInstances() error {
	log.Printf("DRY RUN: Would check for and cleanup dangling instances")
	return nil
}

func (a *ASGDriver) SendSIGTERMToAgents(instanceID string) error {
	log.Printf("[Elastic CI Mode] Sending graceful termination signal to instance %s", instanceID)

	// Check if the instance is in a proper state for receiving commands
	ec2Svc := ec2.New(a.Sess)
	instanceStatus, err := ec2Svc.DescribeInstanceStatus(&ec2.DescribeInstanceStatusInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	if err != nil {
		log.Printf("Warning: Could not check instance status: %v", err)
	} else if len(instanceStatus.InstanceStatuses) > 0 {
		status := *instanceStatus.InstanceStatuses[0].InstanceState.Name
		if status != "running" {
			log.Printf("Instance %s is in %s state, skipping graceful termination", instanceID, status)
			return nil // Just skip graceful termination for non-running instances
		}
	}

	ssmSvc := ssm.New(a.Sess)

	// First check if this instance already has a termination marker
	// This makes SIGTERM idempotent, so even if we're called multiple times
	// across different Lambda invocations, we only send one SIGTERM
	checkMarkerCommand := `
#!/bin/bash
if [ -f /tmp/buildkite-agent-termination-marker ]; then
  echo "MARKER_EXISTS"
  # Echo the contents/timestamp for debugging
  cat /tmp/buildkite-agent-termination-marker
  exit 0
else
  echo "NO_MARKER"
  exit 1
fi
`

	checkMarkerInput := &ssm.SendCommandInput{
		InstanceIds:  []*string{aws.String(instanceID)},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]*string{
			"commands": {aws.String(checkMarkerCommand)},
		},
		Comment: aws.String("Check if instance already has termination marker"),
	}

	checkOutput, err := ssmSvc.SendCommand(checkMarkerInput)
	if err != nil {
		log.Printf("Warning: Could not check termination marker: %v", err)
	} else {
		time.Sleep(2 * time.Second)

		checkResult, err := ssmSvc.GetCommandInvocation(&ssm.GetCommandInvocationInput{
			CommandId:  checkOutput.Command.CommandId,
			InstanceId: aws.String(instanceID),
		})

		if err == nil && checkResult.Status != nil && *checkResult.Status == "Success" &&
			checkResult.StandardOutputContent != nil && strings.Contains(*checkResult.StandardOutputContent, "MARKER_EXISTS") {
			log.Printf("[Elastic CI Mode] ⚠️ Instance %s already received SIGTERM (marker exists), not sending again", instanceID)
			log.Printf("[Elastic CI Mode] Marker details: %s", *checkResult.StandardOutputContent)
			return nil
		}
	}

	command := `
#!/bin/bash
set -euo pipefail

echo "Starting graceful termination for Elastic CI Stack at $(date)"

# Create a marker file to prevent multiple SIGTERMs
echo "Termination requested at $(date)" > /tmp/buildkite-agent-termination-marker

# Use the stop-agent-gracefully script that comes with Elastic CI Stack
if [ -f /usr/local/bin/stop-agent-gracefully ]; then
  echo "Using stop-agent-gracefully script"
  /usr/local/bin/stop-agent-gracefully "autoscaling:EC2_INSTANCE_TERMINATING"
  exit $?
else
  echo "WARNING: stop-agent-gracefully script not found - this doesn't appear to be an Elastic CI Stack instance"
  exit 1
fi
`

	input := &ssm.SendCommandInput{
		InstanceIds:  []*string{aws.String(instanceID)},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]*string{
			"commands": {aws.String(command)},
		},
		Comment: aws.String("Elastic CI Stack: Graceful termination of buildkite-agent"),
	}

	output, err := ssmSvc.SendCommand(input)
	if err != nil {
		log.Printf("Warning: Failed to send graceful termination command: %v", err)
		return nil
	}

	commandID := *output.Command.CommandId
	log.Printf("Started graceful termination for instance %s (CommandID: %s)", instanceID, commandID)

	// Don't wait for completion - the script will handle graceful shutdown
	// and we'll continue with scaling regardless of the result
	log.Printf("Instance %s will finish current jobs before terminating", instanceID)

	return nil
}
