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
		asg                     AutoscaleGroupDetails
		currentDesiredCapacity  int64
		expectedDesiredCapacity int64
	}{
		// Basic scale out without waiting jobs
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				WaitingJobs:   2,
				TotalAgents:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
			},
			currentDesiredCapacity:  2,
			expectedDesiredCapacity: 12,
		},
		// Basic scale out with waiting jobs
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 8,
				RunningJobs:   2,
				WaitingJobs:   20,
				TotalAgents:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				IncludeWaiting:    true,
			},
			currentDesiredCapacity:  2,
			expectedDesiredCapacity: 28,
		},
		// Scale-out with multiple agents per instance
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				IdleAgents:    2,
				TotalAgents:   4,
			},
			params: Params{
				AgentsPerInstance: 4,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 3,
		},
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   2,
			},
			params: Params{
				AgentsPerInstance: 2,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 6,
		},
		// Scale-out with multiple agents per instance
		// where it doesn't divide evenly
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				IdleAgents:    3,
				TotalAgents:   5,
			},
			params: Params{
				AgentsPerInstance: 5,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 3,
		},
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				IdleAgents:    18,
				TotalAgents:   20,
			},
			params: Params{
				AgentsPerInstance: 20,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
		// Scale-out with a factor of 50%
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 0.5,
				},
			},
			currentDesiredCapacity:  2,
			expectedDesiredCapacity: 7,
		},
		// Scale-out with a factor of 10%
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   11,
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
		// Scale-out with a factor too large
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 500.0,
				},
			},
			expectedDesiredCapacity: 100.0,
		},
		// Cool-down period is enforced
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   4,
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
				IdleAgents:    2,
				TotalAgents:   4,
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
				IdleAgents:    2,
				TotalAgents:   4,
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
				TotalAgents:   1,
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
		// Do not scale out after scale in
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 1,
				IdleAgents:  0,
				TotalAgents:   0,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now().Add(-10 * time.Minute),
					CooldownPeriod: 2 * time.Minute,
				},
				ScaleInParams: ScaleParams{
					LastEvent:time.Now().Add(-1 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
				ScaleOnlyAfterAllEvent: true,
			},
			currentDesiredCapacity:  0,
			expectedDesiredCapacity: 0,
		},
	} {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity: tc.currentDesiredCapacity,
			}
			s := Scaler{
				autoscaling:    asg,
				bk:             &buildkiteTestDriver{metrics: tc.metrics},
				scaleOutParams: tc.params.ScaleOutParams,
				scaleInParams: tc.params.ScaleInParams,
				scaleOnlyAfterAllEvent: tc.params.ScaleOnlyAfterAllEvent,
				scaling: ScalingCalculator{
					includeWaiting:    tc.params.IncludeWaiting,
					agentsPerInstance: tc.params.AgentsPerInstance,
				},
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
		currentDesiredCapacity  int64
		expectedDesiredCapacity int64
	}{
		// We're inside cooldown
		{
			metrics: buildkite.AgentMetrics{
				TotalAgents: 10,
			},
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
			metrics: buildkite.AgentMetrics{
				IdleAgents:  10,
				TotalAgents: 10,
			},
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
				IdleAgents:    20,
				TotalAgents:   20,
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
			metrics: buildkite.AgentMetrics{
				IdleAgents: 1,
				TotalAgents:   1,
			},
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
			metrics: buildkite.AgentMetrics{
				TotalAgents:   1,
			},
			params: Params{
				ScaleInParams: ScaleParams{
					Disable: true,
				},
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
		// Do not scale in after scale out
		{
			metrics: buildkite.AgentMetrics{
				IdleAgents:  3,
				TotalAgents:   3,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now().Add(-1 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
				ScaleInParams: ScaleParams{
					LastEvent:time.Now().Add(-10 * time.Minute),
					CooldownPeriod: 2 * time.Minute,
				},
				ScaleOnlyAfterAllEvent: true,
			},
			currentDesiredCapacity:  3,
			expectedDesiredCapacity: 3,
		},
	}

	for _, tc := range testCases {
		t.Run("absolute", func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity: tc.currentDesiredCapacity,
			}
			s := Scaler{
				autoscaling: asg,
				bk:          &buildkiteTestDriver{metrics: tc.metrics},
				scaling: ScalingCalculator{
					includeWaiting:    tc.params.IncludeWaiting,
					agentsPerInstance: tc.params.AgentsPerInstance,
				},
				scaleInParams: tc.params.ScaleInParams,
				scaleOutParams: tc.params.ScaleOutParams,
				scaleOnlyAfterAllEvent: tc.params.ScaleOnlyAfterAllEvent,
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
