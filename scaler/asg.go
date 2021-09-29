package scaler

import (
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

const (
	activitySucessfulStatusCode = "Successful"
)

type AutoscaleGroupDetails struct {
	Pending      int64
	DesiredCount int64
	MinSize      int64
	MaxSize      int64
}

type ASGDriver struct {
	Name string
	Sess *session.Session
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

	details := AutoscaleGroupDetails{
		Pending:      pending,
		DesiredCount: int64(*result.AutoScalingGroups[0].DesiredCapacity),
		MinSize:      int64(*result.AutoScalingGroups[0].MinSize),
		MaxSize:      int64(*result.AutoScalingGroups[0].MaxSize),
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

func (a *ASGDriver) GetAutoscalingActivities(nextToken *string) (*autoscaling.DescribeScalingActivitiesOutput, error) {
	svc := autoscaling.New(a.Sess)
	input := &autoscaling.DescribeScalingActivitiesInput{
		AutoScalingGroupName: aws.String(a.Name),
		NextToken: nextToken,
	}
	return svc.DescribeScalingActivities(input)
}

func (a *ASGDriver) GetLastTerminatingActivity() (*autoscaling.Activity, error) {
	const terminatingKey = "Terminating"
	var nextToken *string
	for {
		output, err := a.GetAutoscalingActivities(nextToken)
		if err != nil {
			return nil, err
		}
		for _, activity := range output.Activities {
			if *activity.StatusCode == activitySucessfulStatusCode && strings.Contains(*activity.Description, terminatingKey) {
				return activity, nil
			}
		}
		nextToken = output.NextToken
		if nextToken == nil {
			break
		}
	}
	return nil, nil
}

func (a *ASGDriver) GetLastLaunchingActivity() (*autoscaling.Activity, error) {
	const launchingKey = "Launching"
	var nextToken *string
	for {
		output, err := a.GetAutoscalingActivities(nextToken)
		if err != nil {
			return nil, err
		}
		for _, activity := range output.Activities {
			if *activity.StatusCode == activitySucessfulStatusCode && strings.Contains(*activity.Description, launchingKey) {
				return activity, nil
			}
		}
		nextToken = output.NextToken
		if nextToken == nil {
			break
		}
	}
	return nil, nil
}

type dryRunASG struct {
}

func (a *dryRunASG) Describe() (AutoscaleGroupDetails, error) {
	return AutoscaleGroupDetails{}, nil
}

func (a *dryRunASG) SetDesiredCapacity(count int64) error {
	return nil
}
