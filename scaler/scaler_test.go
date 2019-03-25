package scaler

import (
	"testing"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

func TestScalingOutWithoutError(t *testing.T) {
	for _, tc := range []struct {
		Metrics           buildkite.AgentMetrics
		AgentsPerInstance int
		DesiredCount      int64
	}{
		{buildkite.AgentMetrics{ScheduledJobs: 0}, 1, 0},
		{buildkite.AgentMetrics{ScheduledJobs: 10}, 1, 10},
		{buildkite.AgentMetrics{ScheduledJobs: 10}, 4, 3},
		{buildkite.AgentMetrics{ScheduledJobs: 12}, 4, 3},
		{buildkite.AgentMetrics{ScheduledJobs: 13}, 4, 4},
		{buildkite.AgentMetrics{ScheduledJobs: 10, RunningJobs: 2}, 4, 3},
		{buildkite.AgentMetrics{ScheduledJobs: 2, RunningJobs: 8}, 4, 3},
	} {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{}
			s := Scaler{
				autoscaling:       asg,
				bk:                &buildkiteTestDriver{metrics: tc.Metrics},
				agentsPerInstance: tc.AgentsPerInstance,
			}

			if err := s.Run(); err != nil {
				t.Fatal(err)
			}

			if asg.desiredCapacity != tc.DesiredCount {
				t.Fatalf("Expected desired capacity of %d, got %d",
					tc.DesiredCount, asg.desiredCapacity,
				)
			}
		})
	}
}

func TestScalingInWithoutError(t *testing.T) {
	testCases := []struct {
		currentDesiredCapacity int64
		coolDownPeriod         time.Duration
		lastScaleInTime        time.Time
		adjustment             int64

		expectedDesiredCapacity int64
	}{
		{
			currentDesiredCapacity: 10,
			coolDownPeriod:         5 * time.Minute,
			lastScaleInTime:        time.Now(),
			adjustment:             -1,

			// We're inside cooldown
			expectedDesiredCapacity: 10,
		},
		{
			currentDesiredCapacity: 10,
			coolDownPeriod:         5 * time.Minute,
			lastScaleInTime:        time.Now().Add(-10 * time.Minute),
			adjustment:             -2,

			// We're out of cooldown but we can only adjust by -2
			expectedDesiredCapacity: 8,
		},
		{
			currentDesiredCapacity: 10,
			coolDownPeriod:         5 * time.Minute,
			lastScaleInTime:        time.Now().Add(-10 * time.Minute),
			adjustment:             -100,

			// We're allowed to adjust the whole amount
			expectedDesiredCapacity: 0,
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{desiredCapacity: tc.currentDesiredCapacity}
			s := Scaler{
				autoscaling:       asg,
				bk:                &buildkiteTestDriver{metrics: buildkite.AgentMetrics{}},
				agentsPerInstance: 1,
				scaleInParams: ScaleInParams{
					CooldownPeriod:  tc.coolDownPeriod,
					Adjustment:      tc.adjustment,
					LastScaleInTime: &tc.lastScaleInTime,
				},
			}

			if err := s.Run(); err != nil {
				t.Fatal(err)
			}

			if asg.desiredCapacity != tc.expectedDesiredCapacity {
				t.Fatalf("Expected desired capacity of %d, got %d",
					tc.expectedDesiredCapacity, asg.desiredCapacity,
				)
			}
		})
	}
}

type buildkiteTestDriver struct {
	metrics buildkite.AgentMetrics
	err     error
}

func (d *buildkiteTestDriver) GetAgentMetrics() (buildkite.AgentMetrics, error) {
	return d.metrics, d.err
}

type asgTestDriver struct {
	err             error
	desiredCapacity int64
}

func (d *asgTestDriver) Describe() (AutoscaleGroupDetails, error) {
	return AutoscaleGroupDetails{
		DesiredCount: d.desiredCapacity,
		MinSize:      0,
		MaxSize:      100,
	}, nil
}

func (d *asgTestDriver) SetDesiredCapacity(count int64) error {
	d.desiredCapacity = count
	return d.err
}
