package scaler

import (
	"context"
	"fmt"
	"log"
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
}

type ASGDriver struct {
	Name                              string
	Sess                              *session.Session
	MaxDescribeScalingActivitiesPages int
	GracefulTermination               bool
}

// CountTerminationLifecycleHooks counts how many termination lifecycle hooks
// are configured for the ASG. Returns 0 if none are found.
func (a *ASGDriver) CountTerminationLifecycleHooks() (int, error) {
	svc := autoscaling.New(a.Sess)

	input := &autoscaling.DescribeLifecycleHooksInput{
		AutoScalingGroupName: aws.String(a.Name),
	}

	result, err := svc.DescribeLifecycleHooks(input)
	if err != nil {
		return 0, fmt.Errorf("failed to describe lifecycle hooks: %v", err)
	}

	var count int
	for _, hook := range result.LifecycleHooks {
		if hook.LifecycleTransition != nil &&
			*hook.LifecycleTransition == "autoscaling:EC2_INSTANCE_TERMINATING" {
			count++
		}
	}

	return count, nil
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

	queryDuration := time.Now().Sub(t)

	asg := result.AutoScalingGroups[0]

	var pending int64
	for _, instance := range asg.Instances {
		lifecycleState := aws.StringValue(instance.LifecycleState)
		if strings.HasPrefix(lifecycleState, "Pending") {
			pending += 1
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
	}

	log.Printf("↳ Got pending=%d, desired=%d, min=%d, max=%d (took %v)",
		details.Pending, details.DesiredCount, details.MinSize, details.MaxSize, queryDuration)

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
	return AutoscaleGroupDetails{}, nil
}

func (a *dryRunASG) SetDesiredCapacity(count int64) error {
	return nil
}

func (a *dryRunASG) SendSIGTERMToAgents(instanceID string) error {
	log.Printf("DRY RUN: Would send SIGTERM to buildkite agents on instance %s", instanceID)
	return nil
}

func (a *ASGDriver) SendSIGTERMToAgents(instanceID string) error {
	log.Printf("Sending SIGTERM to buildkite agents on instance %s", instanceID)

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
			log.Printf("Instance %s is in %s state, not sending SIGTERM", instanceID, status)
			return fmt.Errorf("instance %s is in %s state, not ready for SIGTERM", instanceID, status)
		}
	}

	ssmSvc := ssm.New(a.Sess)

	// First check if buildkite-agent service is running
	checkServiceCmd := `
#!/bin/bash
# Check if buildkite-agent.service is running
if systemctl is-active buildkite-agent.service >/dev/null; then
  echo "buildkite-agent service is running"
  exit 0
else
  echo "buildkite-agent service is not running"
  exit 1
fi
`

	checkInput := &ssm.SendCommandInput{
		InstanceIds:  []*string{aws.String(instanceID)},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]*string{
			"commands": {aws.String(checkServiceCmd)},
		},
		Comment: aws.String("Check if buildkite-agent service is running"),
	}

	checkOutput, err := ssmSvc.SendCommand(checkInput)
	if err != nil {
		return fmt.Errorf("failed to check service status via SSM: %v", err)
	}

	checkCommandID := *checkOutput.Command.CommandId
	log.Printf("Started service check for instance %s with CommandID: %s", instanceID, checkCommandID)

	// Wait and check with retries
	maxRetries := 3
	retryWaitTime := 5 * time.Second

	var checkResult *ssm.GetCommandInvocationOutput
	var serviceRunning bool

	for i := 0; i < maxRetries; i++ {
		// Wait between retries
		time.Sleep(retryWaitTime)

		checkResult, err = ssmSvc.GetCommandInvocation(&ssm.GetCommandInvocationInput{
			CommandId:  aws.String(checkCommandID),
			InstanceId: aws.String(instanceID),
		})

		if err != nil {
			log.Printf("Retry %d/%d: Could not check service status: %v", i+1, maxRetries, err)
			continue
		}

		if checkResult.Status != nil {
			status := *checkResult.Status
			log.Printf("Retry %d/%d: Service check status: %s", i+1, maxRetries, status)

			if status == "Success" {
				// Exit code 0 means service is running
				serviceRunning = true
				log.Printf("Confirmed buildkite-agent service is running on %s", instanceID)
				break
			} else if status == "Failed" || status == "Cancelled" || status == "TimedOut" {
				// Command definitively failed
				if checkResult.StandardOutputContent != nil {
					log.Printf("Service check output: %s", *checkResult.StandardOutputContent)
				}
				if checkResult.StandardErrorContent != nil {
					log.Printf("Service check error: %s", *checkResult.StandardErrorContent)
				}
				break
			}
		}
	}

	if !serviceRunning {
		return fmt.Errorf("buildkite-agent service is not running or could not be verified on instance %s", instanceID)
	}

	// Use the existing stop-agent-gracefully script
	command := `
#!/bin/bash
set -euo pipefail

echo "Starting graceful termination using stop-agent-gracefully at $(date)"

# Call the existing stop-agent-gracefully script with proper lifecycle transition
# This script handles everything: SIGTERM, waiting for jobs, and termination
/usr/local/bin/stop-agent-gracefully "autoscaling:EC2_INSTANCE_TERMINATING"
`

	input := &ssm.SendCommandInput{
		InstanceIds:  []*string{aws.String(instanceID)},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]*string{
			"commands": {aws.String(command)},
		},
		Comment: aws.String("Graceful termination: Send SIGTERM to buildkite-agent processes"),
	}

	output, err := ssmSvc.SendCommand(input)
	if err != nil {
		return fmt.Errorf("failed to send SIGTERM command via SSM: %v", err)
	}

	commandID := *output.Command.CommandId
	log.Printf("Started SIGTERM command for instance %s with CommandID: %s", instanceID, commandID)

	// Wait and check with retries to get proper status - commands might take time to execute
	maxRetries = 5
	retryWaitTime = 5 * time.Second

	var cmdOutput *ssm.GetCommandInvocationOutput
	var commandSucceeded bool

	for i := 0; i < maxRetries; i++ {
		// Wait between retries
		time.Sleep(retryWaitTime)

		cmdOutput, err = ssmSvc.GetCommandInvocation(&ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		})

		if err != nil {
			log.Printf("Retry %d/%d: Could not check command status: %v", i+1, maxRetries, err)
			continue
		}

		// If command is still in progress, wait and try again
		if cmdOutput.Status != nil {
			status := *cmdOutput.Status
			log.Printf("Retry %d/%d: Command status: %s", i+1, maxRetries, status)

			if status == "Success" {
				commandSucceeded = true
				break
			} else if status == "Failed" || status == "Cancelled" || status == "TimedOut" {
				break
			}
			// If status is "InProgress" or "Pending", continue retrying
		}
	}

	// Final status check
	if !commandSucceeded {
		status := "unknown"
		if cmdOutput != nil && cmdOutput.Status != nil {
			status = *cmdOutput.Status
		}

		errorMsg := fmt.Sprintf("SIGTERM command to %s failed with status: %s", instanceID, status)
		log.Printf("%s", errorMsg)

		if cmdOutput != nil {
			if cmdOutput.StandardOutputContent != nil && *cmdOutput.StandardOutputContent != "" {
				log.Printf("Command output: %s", *cmdOutput.StandardOutputContent)
			}
			if cmdOutput.StandardErrorContent != nil && *cmdOutput.StandardErrorContent != "" {
				log.Printf("Command error: %s", *cmdOutput.StandardErrorContent)
			}
		}

		if err != nil {
			return fmt.Errorf("%s: %v", errorMsg, err)
		}
		return fmt.Errorf("%s", errorMsg)
	}

	log.Printf("Successfully sent SIGTERM to buildkite-agent on instance %s", instanceID)
	log.Printf("Instance %s will self-terminate after current jobs complete", instanceID)
	if cmdOutput != nil && cmdOutput.StandardOutputContent != nil && *cmdOutput.StandardOutputContent != "" {
		log.Printf("Command output: %s", *cmdOutput.StandardOutputContent)
	}

	return nil
}
