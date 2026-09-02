package scaler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

func TestNewScalerConfiguresScalingActivityLookup(t *testing.T) {
	s, err := NewScaler(nil, aws.Config{}, Params{
		ASGActivityTimeout:                3 * time.Second,
		MaxDescribeScalingActivitiesPages: 7,
	})
	if err != nil {
		t.Fatalf("NewScaler() error = %v", err)
	}
	driver, ok := s.autoscaling.(*ASGDriver)
	if !ok {
		t.Fatalf("autoscaling driver type = %T, want *ASGDriver", s.autoscaling)
	}
	if got := driver.scalingActivitiesTimeout; got != 3*time.Second {
		t.Errorf("scaling activities timeout = %s, want %s", got, 3*time.Second)
	}
	if got := driver.MaxDescribeScalingActivitiesPages; got != 7 {
		t.Errorf("maximum scaling activity pages = %d, want 7", got)
	}
}

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
	err                     error
	desiredCapacity         int64
	actualCapacity          int64 // If 0, will default to desiredCapacity
	pendingCapacity         int64
	maxSize                 int64 // If 0, defaults to 100
	sigTermsSent            []string
	sigTermsAccepted        int
	sigTermErr              error
	setDesiredCapacityCalls int
	elasticCIMode           bool
	danglingInstancesFound  int
	events                  []string
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

	maxSize := d.maxSize
	if maxSize == 0 {
		maxSize = 100
	}

	return AutoscaleGroupDetails{
		Pending:      d.pendingCapacity,
		DesiredCount: d.desiredCapacity,
		ActualCount:  actualCount,
		TotalCount:   d.desiredCapacity,
		MinSize:      0,
		MaxSize:      maxSize,
		InstanceIDs:  instanceIDs,
	}, d.err
}

func (d *asgTestDriver) SetDesiredCapacity(ctx context.Context, count int64) error {
	d.setDesiredCapacityCalls++
	d.desiredCapacity = count
	d.events = append(d.events, "set desired capacity")
	return d.err
}

func (d *asgTestDriver) SendSIGTERMToAgentsBatch(ctx context.Context, instanceIDs []string) (int, error) {
	d.events = append(d.events, "send graceful stop")
	d.sigTermsSent = append(d.sigTermsSent, instanceIDs...)
	return d.sigTermsAccepted, d.sigTermErr
}

func (d *asgTestDriver) CleanupDanglingInstances(ctx context.Context, minimumInstanceUptime time.Duration, maxDanglingInstancesToCheck int) error {
	d.danglingInstancesFound++
	return d.err
}

type terminateInstancesTestClient struct {
	events *[]string
}

func (c *terminateInstancesTestClient) TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	*c.events = append(*c.events, "terminate instance")
	return &ec2.TerminateInstancesOutput{}, nil
}

// dangleProbeTestClient combines the describe and terminate stubs so one value
// satisfies dangleProbeEC2API.
type dangleProbeTestClient struct {
	*stubDescribeInstancesClient
	*terminateInstancesTestClient
}

func describeOutput(instances ...ec2Types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{Reservations: []ec2Types.Reservation{{Instances: instances}}}
}

func TestOldestInstances(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	instance := func(id string, launched time.Time) ec2Types.Instance {
		return ec2Types.Instance{InstanceId: aws.String(id), LaunchTime: aws.Time(launched)}
	}

	for _, tc := range []struct {
		name string
		out  *ec2.DescribeInstancesOutput
		n    int64
		want []string
	}{
		{
			name: "oldest first regardless of describe order",
			out:  describeOutput(instance("i-c", t0.Add(2*time.Minute)), instance("i-a", t0), instance("i-b", t0.Add(time.Minute))),
			n:    2,
			want: []string{"i-a", "i-b"},
		},
		{
			name: "launch time ties break on instance ID",
			out:  describeOutput(instance("i-b", t0), instance("i-c", t0), instance("i-a", t0)),
			n:    2,
			want: []string{"i-a", "i-b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubDescribeInstancesClient{responses: []stubDescribeResponse{{out: tc.out}}}
			got, err := oldestInstances(t.Context(), client, []string{"i-a", "i-b", "i-c"}, tc.n, "asg-1")
			if err != nil {
				t.Fatalf("oldestInstances() error = %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("oldestInstances() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOldestInstancesFailsClosedOnDescribeError(t *testing.T) {
	describeErr := errors.New("throttled")
	client := &stubDescribeInstancesClient{responses: []stubDescribeResponse{{err: describeErr}}}

	got, err := oldestInstances(t.Context(), client, []string{"i-a", "i-b"}, 1, "asg-1")
	if !errors.Is(err, describeErr) {
		t.Fatalf("oldestInstances() error = %v, want %v", err, describeErr)
	}
	if len(got) != 0 {
		t.Errorf("oldestInstances() = %v, want no candidates", got)
	}
}

func TestTerminateIfDangling(t *testing.T) {
	probeResult := func(status ssmTypes.CommandInvocationStatus) []stubListResponse {
		return []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{
			CommandInvocations: []ssmTypes.CommandInvocation{{InstanceId: aws.String("i-a"), Status: status}},
		}}}
	}
	listErr := errors.New("ListCommandInvocations throttled")

	for _, tc := range []struct {
		name           string
		sendErr        error
		listResponses  []stubListResponse
		wantErr        error
		wantTerminated bool
		wantEvents     string
	}{
		{
			name:           "unreachable instance is terminated after lowering capacity",
			sendErr:        errors.New("InvalidInstanceId"),
			wantTerminated: true,
			wantEvents:     "[set desired capacity terminate instance]",
		},
		{
			name:          "instance with an active agent service is left alone",
			listResponses: probeResult(ssmTypes.CommandInvocationStatusSuccess),
			wantEvents:    "[]",
		},
		{
			// SendCommand accepts commands for ConnectionLost instances and
			// leaves them Pending; accepted is not the same as executed.
			name:           "probe that never executes is terminated",
			listResponses:  probeResult(ssmTypes.CommandInvocationStatusPending),
			wantTerminated: true,
			wantEvents:     "[set desired capacity terminate instance]",
		},
		{
			name:           "probe reporting a stopped agent service is terminated",
			listResponses:  probeResult(ssmTypes.CommandInvocationStatusFailed),
			wantTerminated: true,
			wantEvents:     "[set desired capacity terminate instance]",
		},
		{
			name:          "polling failure leaves the instance alone and reports the error",
			listResponses: []stubListResponse{{err: listErr}},
			wantErr:       listErr,
			wantEvents:    "[]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asg := &asgTestDriver{desiredCapacity: 1}
			ec2Client := dangleProbeTestClient{
				stubDescribeInstancesClient:  &stubDescribeInstancesClient{},
				terminateInstancesTestClient: &terminateInstancesTestClient{events: &asg.events},
			}
			ssmClient := &stubSSMClient{sendErr: tc.sendErr, listResponses: tc.listResponses}
			s := Scaler{autoscaling: asg}

			terminated, err := s.terminateIfDangling(t.Context(), ec2Client, ssmClient, "i-a", 0, time.Millisecond, time.Millisecond)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("terminateIfDangling() error = %v, want %v", err, tc.wantErr)
			}
			if terminated != tc.wantTerminated {
				t.Errorf("terminated = %v, want %v", terminated, tc.wantTerminated)
			}
			if got := fmt.Sprint(asg.events); got != tc.wantEvents {
				t.Errorf("calls = %s, want %s", got, tc.wantEvents)
			}
		})
	}
}

func TestDirectlyTerminateInstanceLowersDesiredCapacityFirst(t *testing.T) {
	asg := &asgTestDriver{desiredCapacity: 1}
	ec2Client := &terminateInstancesTestClient{events: &asg.events}
	s := Scaler{autoscaling: asg}

	if err := s.directlyTerminateInstance(t.Context(), ec2Client, "i-a", 0); err != nil {
		t.Fatalf("directlyTerminateInstance() error = %v", err)
	}

	if got, want := fmt.Sprint(asg.events), "[set desired capacity terminate instance]"; got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
	if got, want := asg.desiredCapacity, int64(0); got != want {
		t.Errorf("desired capacity = %d, want %d", got, want)
	}
}

func TestGracefullyScaleInLetsLinuxAgentsDecrementCapacity(t *testing.T) {
	testCases := []struct {
		name                string
		accepted            int
		dispatchErr         error
		setDesiredErr       error
		wantEvents          string
		wantDesiredCapacity int64
		wantLastEvent       bool
		wantErr             error
		wantErrText         string
	}{
		{
			name:                "successful Linux dispatch",
			accepted:            2,
			wantEvents:          "[send graceful stop]",
			wantDesiredCapacity: 3,
			wantLastEvent:       true,
		},
		{
			name:                "no online Linux instances",
			wantEvents:          "[send graceful stop]",
			wantDesiredCapacity: 3,
			wantLastEvent:       true,
			wantErr:             errNoReachableScaleInCandidates,
		},
		{
			name:                "SSM API failure",
			dispatchErr:         errors.New("describe SSM instance information: throttled"),
			wantEvents:          "[send graceful stop]",
			wantDesiredCapacity: 3,
			wantLastEvent:       true,
			wantErrText:         "describe SSM instance information: throttled",
		},
		{
			name:                "partially successful Linux dispatch",
			accepted:            1,
			dispatchErr:         errors.New("send command to later batch"),
			wantEvents:          "[send graceful stop]",
			wantDesiredCapacity: 3,
			wantLastEvent:       true,
		},
		{
			name:                "Windows dispatch skipped",
			dispatchErr:         ErrWindowsGracefulScaleInNotSupported,
			wantEvents:          "[send graceful stop set desired capacity]",
			wantDesiredCapacity: 1,
			wantLastEvent:       true,
		},
		{
			name:                "Windows desired capacity update fails",
			dispatchErr:         ErrWindowsGracefulScaleInNotSupported,
			setDesiredErr:       errors.New("throttled"),
			wantEvents:          "[send graceful stop set desired capacity]",
			wantDesiredCapacity: 1,
			wantErrText:         "set desired capacity to 1 for windows instances: throttled",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity:  3,
				sigTermsAccepted: tc.accepted,
				sigTermErr:       tc.dispatchErr,
				err:              tc.setDesiredErr,
			}
			s := Scaler{autoscaling: asg}

			err := s.gracefullyScaleIn(t.Context(), []string{"i-a", "i-b"}, 1)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("gracefullyScaleIn() error = %v, want %v", err, tc.wantErr)
				}
			case tc.wantErrText != "":
				if err == nil || err.Error() != tc.wantErrText {
					t.Fatalf("gracefullyScaleIn() error = %v, want %q", err, tc.wantErrText)
				}
			case err != nil:
				t.Fatalf("gracefullyScaleIn() error = %v, want nil", err)
			}

			if got := fmt.Sprint(asg.events); got != tc.wantEvents {
				t.Errorf("calls = %s, want %s", got, tc.wantEvents)
			}
			if got := asg.desiredCapacity; got != tc.wantDesiredCapacity {
				t.Errorf("desired capacity = %d, want %d", got, tc.wantDesiredCapacity)
			}
			if got := !s.scaleInParams.LastEvent.IsZero(); got != tc.wantLastEvent {
				t.Errorf("LastEvent recorded = %t, want %t", got, tc.wantLastEvent)
			}
		})
	}
}

// TestScaleInWhileASGConverging pins the early return in Run that suppresses
// repeated scaling while the ASG is still converging on a previously set
// desired capacity. It must fire only when the computed desired count equals
// the ASG's desired count, and must never swallow a real scale-in.
func TestScaleInWhileASGConverging(t *testing.T) {
	testCases := []struct {
		name                     string
		asgDesired               int64
		asgActual                int64
		expectedDesiredCapacity  int64
		expectedSetCapacityCalls int
	}{
		// The ASG was already told to shrink to 3 and 5 instances are still
		// running: the scaler must wait, not issue another scale-in.
		{
			name:                     "no scale-in while converging down",
			asgDesired:               3,
			asgActual:                5,
			expectedDesiredCapacity:  3,
			expectedSetCapacityCalls: 0,
		},
		// Converging up: desired already 3, only 2 running yet. If the
		// converging guard ever moves below the desired > instanceCount
		// branch, this case would fall through to scaleIn.
		{
			name:                     "no scale-in while converging up",
			asgDesired:               3,
			asgActual:                2,
			expectedDesiredCapacity:  3,
			expectedSetCapacityCalls: 0,
		},
		// The ASG desired count is above the computed desired count, so a
		// real scale-in must still fire despite actual == computed desired.
		{
			name:                     "scale-in fires when ASG desired exceeds computed desired",
			asgDesired:               5,
			asgActual:                3,
			expectedDesiredCapacity:  3,
			expectedSetCapacityCalls: 1,
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
				// ScheduledJobs 3 with one agent per instance computes a
				// desired count of 3 in every case.
				bk: &buildkiteTestDriver{metrics: buildkite.AgentMetrics{
					ScheduledJobs: 3,
				}},
				scaling: ScalingCalculator{agentsPerInstance: 1},
			}

			if _, err := s.Run(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if asg.setDesiredCapacityCalls != tc.expectedSetCapacityCalls {
				t.Errorf("SetDesiredCapacity calls = %d, want %d",
					asg.setDesiredCapacityCalls, tc.expectedSetCapacityCalls)
			}
			if asg.desiredCapacity != tc.expectedDesiredCapacity {
				t.Errorf("desired capacity = %d, want %d",
					asg.desiredCapacity, tc.expectedDesiredCapacity)
			}
			if len(asg.sigTermsSent) != 0 {
				t.Errorf("SIGTERMs sent to %v, want none", asg.sigTermsSent)
			}
		})
	}
}

func TestScalerRunScalesOutWhenTargetExceedsASGDesired(t *testing.T) {
	lastScaleIn := time.Now()
	asg := &asgTestDriver{
		desiredCapacity: 2,
		actualCapacity:  5,
	}
	s := Scaler{
		autoscaling: asg,
		// Three scheduled jobs with one agent per instance require three
		// instances: above ASG desired capacity, but below actual capacity.
		bk: &buildkiteTestDriver{metrics: buildkite.AgentMetrics{
			ScheduledJobs: 3,
		}},
		scaling: ScalingCalculator{agentsPerInstance: 1},
		scaleInParams: ScaleParams{
			CooldownPeriod: time.Hour,
			LastEvent:      lastScaleIn,
		},
	}

	if _, err := s.Run(t.Context()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if asg.setDesiredCapacityCalls != 1 {
		t.Errorf("SetDesiredCapacity calls = %d, want 1", asg.setDesiredCapacityCalls)
	}
	if asg.desiredCapacity != 3 {
		t.Errorf("desired capacity = %d, want 3", asg.desiredCapacity)
	}
	if !s.LastScaleIn().Equal(lastScaleIn) {
		t.Errorf("last scale-in = %s, want unchanged at %s", s.LastScaleIn(), lastScaleIn)
	}
	if s.LastScaleOut().IsZero() {
		t.Error("last scale-out is zero, want scale-out recorded")
	}
}

func TestScalerRunWaitsToScaleInWhileInstancesArePending(t *testing.T) {
	asg := &asgTestDriver{
		desiredCapacity: 5,
		actualCapacity:  5,
		pendingCapacity: 1,
	}
	s := Scaler{
		autoscaling: asg,
		bk: &buildkiteTestDriver{metrics: buildkite.AgentMetrics{
			ScheduledJobs: 3,
		}},
		scaling:       ScalingCalculator{agentsPerInstance: 1},
		elasticCIMode: true,
	}

	if _, err := s.Run(t.Context()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if asg.setDesiredCapacityCalls != 0 {
		t.Errorf("SetDesiredCapacity calls = %d, want 0", asg.setDesiredCapacityCalls)
	}
	if asg.desiredCapacity != 5 {
		t.Errorf("desired capacity = %d, want 5", asg.desiredCapacity)
	}
}

func TestScaleInFailsClosedWhenActivityLookupFails(t *testing.T) {
	wantErr := errors.New("describe activities")
	client := &stubScalingActivitiesClient{
		responses: []stubScalingActivitiesResponse{{err: wantErr}},
	}
	driver := &ASGDriver{
		Name:                              "agents",
		MaxDescribeScalingActivitiesPages: -1,
		scalingActivitiesSvc:              client,
		scalingActivitiesTimeout:          time.Second,
	}
	s := Scaler{
		autoscaling:   driver,
		elasticCIMode: true,
		scaleInParams: ScaleParams{
			CooldownPeriod: time.Hour,
		},
	}

	err := s.scaleIn(t.Context(), 1, AutoscaleGroupDetails{DesiredCount: 2})
	if !errors.Is(err, wantErr) {
		t.Errorf("scaleIn() error = %v, want %v", err, wantErr)
	}
}

func TestScaleInActivityLookupUsesCallerContext(t *testing.T) {
	client := &stubScalingActivitiesClient{
		describe: func(ctx context.Context, _ *autoscaling.DescribeScalingActivitiesInput) (*autoscaling.DescribeScalingActivitiesOutput, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	driver := &ASGDriver{
		MaxDescribeScalingActivitiesPages: -1,
		scalingActivitiesSvc:              client,
		scalingActivitiesTimeout:          time.Hour,
	}
	s := Scaler{
		autoscaling:   driver,
		elasticCIMode: true,
		scaleInParams: ScaleParams{
			CooldownPeriod: time.Hour,
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.scaleIn(ctx, 1, AutoscaleGroupDetails{DesiredCount: 2})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("scaleIn() error = %v, want %v", err, context.Canceled)
	}
}

func TestScaleInHonorsRecoveredActivity(t *testing.T) {
	client := &stubScalingActivitiesClient{
		responses: []stubScalingActivitiesResponse{{
			output: &autoscaling.DescribeScalingActivitiesOutput{
				Activities: []types.Activity{{
					Cause:      aws.String("At 2026-08-26T16:14:36Z instance i-017bacd33066f0408 was taken out of service in response to a user request, shrinking the capacity from 2 to 1."),
					StartTime:  aws.Time(time.Now().Add(-time.Minute)),
					StatusCode: types.ScalingActivityStatusCodeSuccessful,
				}},
			},
		}},
	}
	driver := &ASGDriver{
		MaxDescribeScalingActivitiesPages: -1,
		scalingActivitiesSvc:              client,
		scalingActivitiesTimeout:          time.Second,
	}
	s := Scaler{
		autoscaling:   driver,
		elasticCIMode: true,
		scaleInParams: ScaleParams{
			CooldownPeriod: time.Hour,
		},
	}

	if err := s.scaleIn(t.Context(), 1, AutoscaleGroupDetails{DesiredCount: 2}); err != nil {
		t.Fatalf("scaleIn() error = %v", err)
	}
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

func TestDanglingInstanceDetection(t *testing.T) {
	testCases := []struct {
		name                    string
		metrics                 buildkite.AgentMetrics
		asgDesired              int64
		asgActual               int64
		maxSize                 int64
		agentsPerInstance       int
		elasticCIMode           bool
		expectedDesiredCapacity int64
		expectedDanglingChecks  int
	}{
		// Core scenario from SUP-5153: agents die on running instance, MaxSize=1 caps
		// desired to 1, scaler sees desired==actual and says "No scaling required".
		// With the fix, it should detect total=0 with instances running and trigger cleanup.
		{
			name: "Elastic CI detects dangling instance when agents die and MaxSize caps desired",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 4,
				RunningJobs:   0,
				WaitingJobs:   7,
				TotalAgents:   0,
			},
			asgDesired:              1,
			asgActual:               1,
			maxSize:                 1,
			agentsPerInstance:       6,
			elasticCIMode:           true,
			expectedDesiredCapacity: 1, // capacity unchanged — cleanup handles recovery
			expectedDanglingChecks:  1,
		},
		// Same scenario but without Elastic CI Mode — should NOT trigger dangling check.
		{
			name: "Non-Elastic CI mode does not trigger dangling check",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 4,
				RunningJobs:   0,
				WaitingJobs:   7,
				TotalAgents:   0,
			},
			asgDesired:              1,
			asgActual:               1,
			maxSize:                 1,
			agentsPerInstance:       6,
			elasticCIMode:           false,
			expectedDesiredCapacity: 1,
			expectedDanglingChecks:  0,
		},
		// When there are zero instances AND zero agents (normal idle state),
		// should NOT trigger dangling check — there's nothing dangling.
		{
			name: "No dangling check when zero instances and zero agents",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 0,
				RunningJobs:   0,
				TotalAgents:   0,
			},
			asgDesired:              0,
			asgActual:               0,
			maxSize:                 1,
			agentsPerInstance:       6,
			elasticCIMode:           true,
			expectedDesiredCapacity: 0,
			expectedDanglingChecks:  0,
		},
		// When agents are healthy (total > 0), no dangling check needed.
		{
			name: "No dangling check when agents are healthy",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 0,
				RunningJobs:   0,
				TotalAgents:   6,
			},
			asgDesired:              1,
			asgActual:               1,
			maxSize:                 1,
			agentsPerInstance:       6,
			elasticCIMode:           true,
			expectedDesiredCapacity: 0, // scales in to 0 (idle agents, no jobs)
			expectedDanglingChecks:  0,
		},
		// Multiple instances with zero agents — should still trigger dangling check.
		{
			name: "Elastic CI detects dangling with multiple instances and zero agents",
			metrics: buildkite.AgentMetrics{
				ScheduledJobs: 10,
				RunningJobs:   0,
				TotalAgents:   0,
			},
			asgDesired:              3,
			asgActual:               3,
			maxSize:                 3,
			agentsPerInstance:       4,
			elasticCIMode:           true,
			expectedDesiredCapacity: 3, // capacity unchanged — cleanup handles recovery
			expectedDanglingChecks:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			asg := &asgTestDriver{
				desiredCapacity: tc.asgDesired,
				actualCapacity:  tc.asgActual,
				maxSize:         tc.maxSize,
			}

			s := Scaler{
				autoscaling:   asg,
				bk:            &buildkiteTestDriver{metrics: tc.metrics},
				elasticCIMode: tc.elasticCIMode,
				scaling: ScalingCalculator{
					includeWaiting:    true,
					agentsPerInstance: tc.agentsPerInstance,
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

			if asg.danglingInstancesFound != tc.expectedDanglingChecks {
				t.Errorf("Expected %d dangling instance check(s), got: %d",
					tc.expectedDanglingChecks, asg.danglingInstancesFound)
			}
		})
	}
}
