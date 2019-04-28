package scaler

import (
	"testing"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

func TestScalingOutWithoutError(t *testing.T) {
	metrics := buildkite.AgentMetrics{
		ScheduledJobs: 10,
		RunningJobs:   2,
	}

	for _, tc := range []struct {
		agentsPerInstance       int
		currentDesiredCapacity  int64
		params                  ScaleOutParams
		expectedDesiredCapacity int64
	}{
		// Basic scale out
		{
			agentsPerInstance:       1,
			expectedDesiredCapacity: 12,
		},
		// Scale-out with multiple agents per instance
		{
			agentsPerInstance:       4,
			expectedDesiredCapacity: 3,
		},
		{
			agentsPerInstance:       2,
			expectedDesiredCapacity: 6,
		},
		// Scale-out with multiple agents per instance
		// where it doesn't divide evenly
		{
			agentsPerInstance:       5,
			expectedDesiredCapacity: 3,
		},
		{
			agentsPerInstance:       20,
			expectedDesiredCapacity: 1,
		},
		// Scale-out with a minimum bound
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				MinAdjustment: 4,
			},
			expectedDesiredCapacity: 4,
		},
		// Cool-down period is enforced
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				LastScaleOut:   time.Now(),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 4,
		},
		// Cool-down period is enforced, regardless of min scale out
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				MinAdjustment:  10,
				MaxAdjustment:  20,
				LastScaleOut:   time.Now(),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 4,
		},
		// Cool-down period is passed
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				LastScaleOut:   time.Now().Add(-10 * time.Minute),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 12,
		},
		// Cool-down period is passed, max scale out is applied
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				MaxAdjustment:  2,
				LastScaleOut:   time.Now().Add(-10 * time.Minute),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 6,
		},
		// Scale out disabled
		{
			agentsPerInstance: 1,
			params: ScaleOutParams{
				Disable: true,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
	} {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{desiredCapacity: tc.currentDesiredCapacity}
			s := Scaler{
				autoscaling:       asg,
				bk:                &buildkiteTestDriver{metrics: metrics},
				agentsPerInstance: tc.agentsPerInstance,
				scaleOutParams:    tc.params,
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

func TestScalingInWithoutError(t *testing.T) {
	testCases := []struct {
		currentDesiredCapacity  int64
		scheduledJobs           int64
		params                  ScaleInParams
		expectedDesiredCapacity int64
	}{
		// We're inside cooldown
		{
			currentDesiredCapacity: 10,
			params: ScaleInParams{
				CooldownPeriod: 5 * time.Minute,
				LastScaleIn:    time.Now(),
			},
			expectedDesiredCapacity: 10,
		},
		// We're out of cooldown but we can only adjust by -2
		{
			currentDesiredCapacity: 10,
			params: ScaleInParams{
				CooldownPeriod: 5 * time.Minute,
				LastScaleIn:    time.Now().Add(-10 * time.Minute),
				MaxAdjustment:  -2,
			},
			expectedDesiredCapacity: 8,
		},
		// We're allowed to adjust the whole amount
		{
			currentDesiredCapacity: 20,
			scheduledJobs:          10,
			params: ScaleInParams{
				MinAdjustment: -100,
			},
			expectedDesiredCapacity: 0,
		},
		// Scale in disabled
		{
			params: ScaleInParams{
				Disable: true,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{desiredCapacity: tc.currentDesiredCapacity}
			s := Scaler{
				autoscaling: asg,
				bk: &buildkiteTestDriver{metrics: buildkite.AgentMetrics{
					ScheduledJobs: tc.scheduledJobs,
				}},
				agentsPerInstance: 1,
				scaleInParams:     tc.params,
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
