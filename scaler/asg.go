package scaler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
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
}

type ASGDriver struct {
	Name                              string
	Cfg                               aws.Config
	MaxDescribeScalingActivitiesPages int
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

	queryDuration := time.Now().Sub(t)

	asg := result.AutoScalingGroups[0]

	var pending int64
	for _, instance := range asg.Instances {
		lifecycleState := string(instance.LifecycleState)
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
	return svc.DescribeScalingActivities(context.TODO(), input)
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

func (a *dryRunASG) Describe(ctx context.Context) (AutoscaleGroupDetails, error) {
	return AutoscaleGroupDetails{}, nil
}

func (a *dryRunASG) SetDesiredCapacity(ctx context.Context, count int64) error {
	return nil
}
