package scaler

import (
	"testing"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

func TestScalingOutWithoutError(t *testing.T) {
	for _, tc := range []struct {
		params                  Params
		metrics                 buildkite.AgentMetrics
		currentMinSize          int64
		currentMaxSize          int64
		currentDesiredCapacity  int64
		expectedDesiredCapacity int64
	}{
		// Basic scale out without waiting jobs
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				WaitingJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
			},
			expectedDesiredCapacity: 12,
		},
		// Basic scale out with waiting jobs
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 8,
				RunningJobs:   2,
				WaitingJobs:   20,
			},
			params: Params{
				AgentsPerInstance: 1,
				IncludeWaiting:    true,
			},
			expectedDesiredCapacity: 28,
		},
		// Scale-out with multiple agents per instance
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 4,
			},
			expectedDesiredCapacity: 3,
		},
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 2,
			},
			expectedDesiredCapacity: 6,
		},
		// Scale-out with multiple agents per instance
		// where it doesn't divide evenly
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 5,
			},
			expectedDesiredCapacity: 3,
		},
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 20,
			},
			expectedDesiredCapacity: 1,
		},
		// Scale-out with a factor of 50%
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 0.5,
				},
			},
			expectedDesiredCapacity: 6,
		},
		// Scale-out with a factor of 10%
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 0.10,
				},
			},
			currentDesiredCapacity:  11,
			expectedDesiredCapacity: 12,
		},
		// Cool-down period is enforced
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now(),
					CooldownPeriod: 5 * time.Minute,
				},
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 4,
		},
		// Cool-down period is passed
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now().Add(-10 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 12,
		},
		// Cool-down period is passed, factor is applied
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor:         2.0,
					LastEvent:      time.Now().Add(-10 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 20,
		},
		// Scale out disabled
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Disable: true,
				},
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
		// Scale out capped by max size *after* factor applied
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 2.0,
				},
			},
			currentMaxSize:          2,
			expectedDesiredCapacity: 2,
		},
	} {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{
				minSize:         tc.currentMinSize,
				maxSize:         tc.currentMaxSize,
				desiredCapacity: tc.currentDesiredCapacity,
			}
			s := Scaler{
				autoscaling:       asg,
				bk:                &buildkiteTestDriver{metrics: tc.metrics},
				agentsPerInstance: tc.params.AgentsPerInstance,
				scaleOutParams:    tc.params.ScaleOutParams,
				includeWaiting:    tc.params.IncludeWaiting,
			}

			if _, err := s.Run(); err != nil {
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
		params                  Params
		metrics                 buildkite.AgentMetrics
		currentMinSize          int64
		currentMaxSize          int64
		currentDesiredCapacity  int64
		expectedDesiredCapacity int64
	}{
		// We're inside cooldown
		{
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now(),
				},
			},
			currentDesiredCapacity:  10,
			expectedDesiredCapacity: 10,
		},
		// We're out of cooldown, apply factor
		{
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now().Add(-10 * time.Minute),
					Factor:         0.10,
				},
			},
			currentDesiredCapacity:  10,
			expectedDesiredCapacity: 9,
		},
		// With 500% factor, we scale all the way down despite scheduled jobs
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					Factor: 5.0,
				},
			},
			currentDesiredCapacity:  20,
			expectedDesiredCapacity: 0,
		},
		// Make sure we round down so we eventually reach zero
		{
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					Factor: 0.10,
				},
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 0,
		},
		// Scale in disabled
		{
			params: Params{
				ScaleInParams: ScaleParams{
					Disable: true,
				},
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
		// Scale in capped by min size *after* factor applied
		{
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now().Add(-10 * time.Minute),
					Factor:         10.00,
				},
			},
			currentMinSize:          1,
			currentDesiredCapacity:  2,
			expectedDesiredCapacity: 1,
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{
				minSize:         tc.currentMinSize,
				maxSize:         tc.currentMaxSize,
				desiredCapacity: tc.currentDesiredCapacity,
			}
			s := Scaler{
				autoscaling:       asg,
				bk:                &buildkiteTestDriver{metrics: tc.metrics},
				agentsPerInstance: tc.params.AgentsPerInstance,
				scaleInParams:     tc.params.ScaleInParams,
			}

			if _, err := s.Run(); err != nil {
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
	minSize         int64
	maxSize         int64
	desiredCapacity int64
}

func (d *asgTestDriver) Describe() (AutoscaleGroupDetails, error) {
	// provide a default so we don't have to set it in every test case
	if d.maxSize == 0 {
		d.maxSize = 100
	}

	return AutoscaleGroupDetails{
		DesiredCount: d.desiredCapacity,
		MinSize:      d.minSize,
		MaxSize:      d.maxSize,
	}, nil
}

func (d *asgTestDriver) SetDesiredCapacity(count int64) error {
	d.desiredCapacity = count
	return d.err
}
