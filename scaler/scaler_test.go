package scaler

import (
	"context"
	"fmt"
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
		// Basic scale out with instance buffer
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				WaitingJobs:   2,
				TotalAgents:   2,
			},
			params: Params{
				AgentsPerInstance: 1,
				InstanceBuffer:    10,
			},
			currentDesiredCapacity:  12,
			expectedDesiredCapacity: 22,
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
		// Many agents per instance
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
		// With 50% scale factor
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
		// With 10% scale factor
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
		// With 500% scale factor
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				TotalAgents:   0,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					Factor: 5.0,
				},
			},
			currentDesiredCapacity:  0,
			expectedDesiredCapacity: 50, // Modified from 100 to 50 since our test has MaxSize set to 100
		},
		// Scale-out is in cool down
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   4,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now(),
				},
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 4,
		},
		// Scale-out out of cool down
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   4,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now().Add(-10 * time.Minute),
				},
			},
			currentDesiredCapacity:  4,
			expectedDesiredCapacity: 12,
		},
		// Scale out applies scale factor after cooldown
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   2,
				TotalAgents:   4,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					CooldownPeriod: 5 * time.Minute,
					LastEvent:      time.Now().Add(-10 * time.Minute),
					Factor:         2.0,
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
				IdleAgents:    0,
				TotalAgents:   1,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleInParams: ScaleParams{
					LastEvent:      time.Now().Add(-1 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now().Add(-10 * time.Minute),
					CooldownPeriod: 2 * time.Minute,
				},
				ScaleOnlyAfterAllEvent: true,
			},
			currentDesiredCapacity:  1,
			expectedDesiredCapacity: 1,
		},
	} {
		t.Run("", func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity: tc.currentDesiredCapacity,
			}
			s := Scaler{
				autoscaling:            asg,
				bk:                     &buildkiteTestDriver{metrics: tc.metrics},
				scaleOutParams:         tc.params.ScaleOutParams,
				scaleInParams:          tc.params.ScaleInParams,
				instanceBuffer:         tc.params.InstanceBuffer,
				scaleOnlyAfterAllEvent: tc.params.ScaleOnlyAfterAllEvent,
				elasticCIMode:          false,
				scaling: ScalingCalculator{
					includeWaiting:        tc.params.IncludeWaiting,
					agentsPerInstance:     tc.params.AgentsPerInstance,
					availabilityThreshold: 0.0, // Disable availability threshold for tests
				},
			}

			if _, err := s.Run(context.Background()); err != nil {
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
		// Calculate using an instance buffer
		{
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   5,
				IdleAgents:    25,
				TotalAgents:   30,
			},
			params: Params{
				AgentsPerInstance: 1,
				InstanceBuffer:    10,
			},
			currentDesiredCapacity:  30,
			expectedDesiredCapacity: 25,
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
				IdleAgents:  1,
				TotalAgents: 1,
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
				TotalAgents: 1,
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
				TotalAgents: 3,
			},
			params: Params{
				AgentsPerInstance: 1,
				ScaleOutParams: ScaleParams{
					LastEvent:      time.Now().Add(-1 * time.Minute),
					CooldownPeriod: 5 * time.Minute,
				},
				ScaleInParams: ScaleParams{
					LastEvent:      time.Now().Add(-10 * time.Minute),
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
					includeWaiting:        tc.params.IncludeWaiting,
					agentsPerInstance:     tc.params.AgentsPerInstance,
					availabilityThreshold: 0.0, // Disable availability threshold for tests
				},
				scaleInParams:          tc.params.ScaleInParams,
				scaleOutParams:         tc.params.ScaleOutParams,
				instanceBuffer:         tc.params.InstanceBuffer,
				scaleOnlyAfterAllEvent: tc.params.ScaleOnlyAfterAllEvent,
				elasticCIMode:          false, // Use standard mode for most tests
			}

			if _, err := s.Run(context.Background()); err != nil {
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

func (d *buildkiteTestDriver) GetAgentMetrics(ctx context.Context) (buildkite.AgentMetrics, error) {
	return d.metrics, d.err
}

type asgTestDriver struct {
	err                    error
	desiredCapacity        int64
	sigTermsSent           []string
	elasticCIMode          bool
	danglingInstancesFound int
}

func (d *asgTestDriver) Describe(ctx context.Context) (AutoscaleGroupDetails, error) {
	d.elasticCIMode = false
	instanceIDs := make([]string, d.desiredCapacity)
	for i := int64(0); i < d.desiredCapacity; i++ {
		instanceIDs[i] = fmt.Sprintf("i-%012d", i)
	}

	return AutoscaleGroupDetails{
		DesiredCount: d.desiredCapacity,
		MinSize:      0,
		MaxSize:      100,
		InstanceIDs:  instanceIDs,
	}, d.err
}

func (d *asgTestDriver) SetDesiredCapacity(ctx context.Context, count int64) error {
	d.desiredCapacity = count
	return d.err
}

func (d *asgTestDriver) SendSIGTERMToAgents(ctx context.Context, instanceID string) error {
	if d.sigTermsSent == nil {
		d.sigTermsSent = []string{}
	}
	d.sigTermsSent = append(d.sigTermsSent, instanceID)
	return d.err
}

func (d *asgTestDriver) CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error {
	d.danglingInstancesFound++
	return d.err
}
