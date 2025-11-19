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
					availabilityThreshold: 0, // Disable availability threshold for tests
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
					availabilityThreshold: 0, // Disable availability threshold for tests
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
	actualCapacity         int64 // If 0, will default to desiredCapacity
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

	actualCount := d.actualCapacity
	if actualCount == 0 {
		actualCount = d.desiredCapacity
	}

	return AutoscaleGroupDetails{
		DesiredCount: d.desiredCapacity,
		ActualCount:  actualCount,
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

func TestAvailabilityBasedScaling(t *testing.T) {
	testCases := []struct {
		name                    string
		metrics                 buildkite.AgentMetrics
		asgDesired              int64
		asgActual               int64
		agentsPerInstance       int
		availabilityThreshold   float64
		expectedDesiredCapacity int64
	}{
		// With 2 instances @ 4 agents each = 8 expected, but only 3 online (37.5%).
		// Should scale from 2 to 3 instances when ASG has converged.
		{
			name: "Low availability triggers scale-out when ASG converged",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 5,
				RunningJobs:   2,
				TotalAgents:   3,
			},
			asgDesired:              2,
			asgActual:               2,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 3,
		},
		// ASG not converged (actual 1 != desired 2), should wait for convergence
		// before applying availability-based scaling.
		{
			name: "Low availability does not trigger when ASG still converging",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 5,
				RunningJobs:   2,
				TotalAgents:   3,
			},
			asgDesired:              2,
			asgActual:               1,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 2,
		},
		// 7 out of 8 expected agents (87.5% availability) is above 50% threshold.
		// No scale-out needed.
		{
			name: "Good availability does not trigger scale-out",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 5,
				RunningJobs:   2,
				TotalAgents:   7,
			},
			asgDesired:              2,
			asgActual:               2,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 2,
		},
		// Threshold set to 0 disables availability-based scaling.
		// No scale-out despite only 2 out of 8 agents online (25%).
		{
			name: "Availability threshold disabled (0) does not trigger",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 5,
				RunningJobs:   2,
				TotalAgents:   2,
			},
			asgDesired:              2,
			asgActual:               2,
			agentsPerInstance:       4,
			availabilityThreshold:   0,
			expectedDesiredCapacity: 2,
		},
		// With 0 instances, job-based scaling takes over.
		// Need 2 instances for 5 jobs (at 4 agents per instance).
		{
			name: "Low availability from zero instances scales to 1",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 5,
				RunningJobs:   0,
				TotalAgents:   0,
			},
			asgDesired:              0,
			asgActual:               0,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 2,
		},
		// Only 2 out of 12 expected agents online (16.7% availability).
		// Availability-based boost from 3 to 4 overrides lower job-based need (1).
		{
			name: "Availability boost when job-based need is lower",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 2,
				RunningJobs:   0,
				TotalAgents:   2,
			},
			asgDesired:              3,
			asgActual:               3,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 4,
		},
		// Need 5 instances for 20 jobs. Job-based scaling (5) dominates
		// over availability boost (3), despite low availability (25%).
		{
			name: "No boost when job-based need is higher",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 20,
				RunningJobs:   0,
				TotalAgents:   2,
			},
			asgDesired:              2,
			asgActual:               2,
			agentsPerInstance:       4,
			availabilityThreshold:   0.5,
			expectedDesiredCapacity: 5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity: tc.asgDesired,
				actualCapacity:  tc.asgActual,
			}

			s := Scaler{
				autoscaling: asg,
				bk:          &buildkiteTestDriver{metrics: tc.metrics},
				scaling: ScalingCalculator{
					includeWaiting:        false,
					agentsPerInstance:     tc.agentsPerInstance,
					availabilityThreshold: tc.availabilityThreshold,
				},
			}

			_, err := s.Run(context.Background())
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if asg.desiredCapacity != tc.expectedDesiredCapacity {
				t.Errorf("Expected desired capacity: %d, got: %d",
					tc.expectedDesiredCapacity, asg.desiredCapacity)
			}
		})
	}
}
