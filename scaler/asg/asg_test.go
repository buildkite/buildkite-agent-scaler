package asg_test

import (
	"testing"

	"github.com/buildkite/buildkite-agent-scaler/scaler/asg"
)

func TestScalingWithoutError(t *testing.T) {
	for _, tc := range []struct {
		ScheduledJobs     int64
		AgentsPerInstance int
		DesiredCount      int64
	}{
		{0, 1, 0},
		{10, 1, 10},
		{10, 4, 3},
		{12, 4, 3},
		{13, 4, 4},
	} {
		t.Run("", func(t *testing.T) {
			bd := &asgTestDriver{}
			s := asg.Scaler{
				ASG:               bd,
				Buildkite:         &buildkiteTestDriver{count: tc.ScheduledJobs},
				AgentsPerInstance: tc.AgentsPerInstance,
			}

			if err := s.Run(); err != nil {
				t.Fatal(err)
			}

			if bd.desiredCapacity != tc.DesiredCount {
				t.Fatalf("Expected desired capacity of %d, got %d",
					tc.DesiredCount, bd.desiredCapacity,
				)
			}
		})
	}
}

type buildkiteTestDriver struct {
	count int64
	err   error
}

func (d *buildkiteTestDriver) GetScheduledJobCount() (int64, error) {
	return d.count, d.err
}

type asgTestDriver struct {
	err             error
	desiredCapacity int64
}

func (d *asgTestDriver) Describe() (asg.Details, error) {
	return asg.Details{MinSize: 0, MaxSize: 100}, nil
}

func (d *asgTestDriver) SetDesiredCapacity(count int64) error {
	d.desiredCapacity = count
	return d.err
}
