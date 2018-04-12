package asg

import (
	"log"
	"math"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/buildkite/buildkite-agent-scaler/scaler"
)

type Params struct {
	AutoScalingGroupName string
	AgentsPerInstance    int
	BuildkiteApiToken    string
	BuildkiteOrgSlug     string
	BuildkiteQueue       string
}

type Scaler struct {
	ASG interface {
		SetDesiredCapacity(count int64) error
	}
	Buildkite interface {
		GetScheduledJobCount() (int64, error)
	}
	AgentsPerInstance int
}

func NewScaler(params Params) *Scaler {
	return &Scaler{
		ASG: &asgDriver{
			name: params.AutoScalingGroupName,
		},
		Buildkite: &buildkiteDriver{
			apiToken: params.BuildkiteApiToken,
			orgSlug:  params.BuildkiteOrgSlug,
			queue:    params.BuildkiteQueue,
		},
		AgentsPerInstance: params.AgentsPerInstance,
	}
}

func (s *Scaler) Run() error {
	count, err := s.Buildkite.GetScheduledJobCount()
	if err != nil {
		return err
	}

	var desired int64
	if count > 0 {
		desired = int64(math.Ceil(float64(count) / float64(s.AgentsPerInstance)))
	}

	log.Printf("Setting a desired capacity of %d", desired)
	err = s.ASG.SetDesiredCapacity(desired)
	if err != nil {
		return err
	}

	return nil
}

type asgDriver struct {
	name string
}

func (a *asgDriver) SetDesiredCapacity(count int64) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(a.name),
		DesiredCapacity:      aws.Int64(count),
		HonorCooldown:        aws.Bool(true),
	}

	_, err := svc.SetDesiredCapacity(input)
	if err != nil {
		return err
	}

	return nil
}

type buildkiteDriver struct {
	apiToken string
	orgSlug  string
	queue    string
}

func (a *buildkiteDriver) GetScheduledJobCount() (int64, error) {
	client, err := scaler.NewBuildkiteClient(a.apiToken)
	if err != nil {
		return 0, err
	}

	return client.GetScheduledJobCount(a.orgSlug, a.queue)
}
