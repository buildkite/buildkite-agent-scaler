package scaler

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

type AutoscaleGroupDetails struct {
	Pending      int64
	DesiredCount int64
	MinSize      int64
	MaxSize      int64
}

type asgDriver struct {
	name string
}

func (a *asgDriver) Describe() (AutoscaleGroupDetails, error) {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{
			aws.String(a.name),
		},
	}

	result, err := svc.DescribeAutoScalingGroups(input)
	if err != nil {
		return AutoscaleGroupDetails{}, err
	}

	asg := result.AutoScalingGroups[0]

	var pending int64
	for _, instance := range asg.Instances {
		lifecycleState := aws.StringValue(instance.LifecycleState)
		if strings.HasPrefix(lifecycleState, "Pending") {
			pending += 1
		}
	}

	// There may be a race condition between increasing DesiredCapacity and
	// actually seeing a pending instance in the list. In this case, treat the
	// difference as pending instances that are yet to be created.
	numInstances := int64(len(asg.Instances))
	desired := int64(*result.AutoScalingGroups[0].DesiredCapacity)
	if numInstances < desired {
		pending += desired - numInstances
	}

	return AutoscaleGroupDetails{
		Pending:      pending,
		DesiredCount: desired,
		MinSize:      int64(*result.AutoScalingGroups[0].MinSize),
		MaxSize:      int64(*result.AutoScalingGroups[0].MaxSize),
	}, nil

}

func (a *asgDriver) SetDesiredCapacity(count int64) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(a.name),
		DesiredCapacity:      aws.Int64(count),
		HonorCooldown:        aws.Bool(false),
	}

	_, err := svc.SetDesiredCapacity(input)
	if err != nil {
		return err
	}

	return nil
}

type dryRunASG struct {
}

func (a *dryRunASG) Describe() (AutoscaleGroupDetails, error) {
	return AutoscaleGroupDetails{}, nil
}

func (a *dryRunASG) SetDesiredCapacity(count int64) error {
	return nil
}
