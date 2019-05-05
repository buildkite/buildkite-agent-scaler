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
		params                  ScaleParams
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
		// Scale-out with a factor of 50%
		{
			agentsPerInstance: 1,
			params: ScaleParams{
				Factor: 0.5,
			},
			expectedDesiredCapacity: 6,
		},
		// Scale-out with a factor of 10%
		{
			agentsPerInstance: 1,
			params: ScaleParams{
				Factor: 0.10,
			},
			currentDesiredCapacity: 11,
			expectedDesiredCapacity: 12,
		},
		// Cool-down period is enforced
		{
			agentsPerInstance: 1,
			params: ScaleParams{
				LastEvent:   time.Now(),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 4,
		},
		// Cool-down period is passed
		{
			agentsPerInstance: 1,
			params: ScaleParams{
				LastEvent:   time.Now().Add(-10 * time.Minute),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 12,
		},
		// Cool-down period is passed, factor is applied
		{
			agentsPerInstance: 1,
			params: ScaleParams{
				Factor:  2.0,
				LastEvent:   time.Now().Add(-10 * time.Minute),
				CooldownPeriod: 5 * time.Minute,
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 20,
		},
		// Scale out disabled
		{
			agentsPerInstance: 1,
			params: ScaleParams{
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
		params                  ScaleParams
		expectedDesiredCapacity int64
	}{
		// We're inside cooldown
		{
			currentDesiredCapacity: 10,
			params: ScaleParams{
				CooldownPeriod: 5 * time.Minute,
				LastEvent:    time.Now(),
			},
			expectedDesiredCapacity: 10,
		},
		// We're out of cooldown, apply factor
		{
			currentDesiredCapacity: 10,
			params: ScaleParams{
				CooldownPeriod: 5 * time.Minute,
				LastEvent:    time.Now().Add(-10 * time.Minute),
				Factor:  0.10,
			},
			expectedDesiredCapacity: 9,
		},
		// With 500% factor, we scale all the way down despite scheduled jobs
		{
			currentDesiredCapacity: 20,
			scheduledJobs:          10,
			params: ScaleParams{
				Factor: 5.0,
			},
			expectedDesiredCapacity: 0,
		},
		// Make sure we round down so we eventually reach zero
		{
			currentDesiredCapacity: 1,
			params: ScaleParams{
				Factor: 0.10,
			},
			expectedDesiredCapacity: 0,
		},
		// Scale in disabled
		{
			params: ScaleParams{
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
