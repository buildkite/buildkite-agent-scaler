package scaler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

func TestGetASGPlatform(t *testing.T) {
	driver := &ASGDriver{}
	ctx := context.Background()

	testCases := []struct {
		name             string
		instances        []ec2Types.Instance
		expectedPlatform string
	}{
		{
			name:             "empty instances defaults to linux",
			instances:        []ec2Types.Instance{},
			expectedPlatform: "linux",
		},
		{
			name: "linux instance (no platform set)",
			instances: []ec2Types.Instance{
				{
					InstanceId: aws.String("i-linux123"),
					// Platform is not set for Linux instances
				},
			},
			expectedPlatform: "linux",
		},
		{
			name: "windows instance",
			instances: []ec2Types.Instance{
				{
					InstanceId: aws.String("i-windows123"),
					Platform:   ec2Types.PlatformValuesWindows,
				},
			},
			expectedPlatform: "windows",
		},
		{
			name: "multiple linux instances",
			instances: []ec2Types.Instance{
				{InstanceId: aws.String("i-linux1")},
				{InstanceId: aws.String("i-linux2")},
				{InstanceId: aws.String("i-linux3")},
			},
			expectedPlatform: "linux",
		},
		{
			name: "multiple windows instances",
			instances: []ec2Types.Instance{
				{InstanceId: aws.String("i-win1"), Platform: ec2Types.PlatformValuesWindows},
				{InstanceId: aws.String("i-win2"), Platform: ec2Types.PlatformValuesWindows},
			},
			expectedPlatform: "windows",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := driver.getASGPlatform(ctx, tc.instances)
			if platform != tc.expectedPlatform {
				t.Errorf("expected platform %q, got %q", tc.expectedPlatform, platform)
			}
		})
	}
}

func TestParseStaleInstanceIDs(t *testing.T) {
	testCases := []struct {
		name     string
		msg      string
		expected []string
	}{
		{
			name:     "single stale ID",
			msg:      "The instance ID 'i-0027f3b5de8a270d2' does not exist",
			expected: []string{"i-0027f3b5de8a270d2"},
		},
		{
			name:     "multiple stale IDs",
			msg:      "The instance IDs 'i-0abc1234, i-0def5678, i-0099aabbccdd' do not exist",
			expected: []string{"i-0abc1234", "i-0def5678", "i-0099aabbccdd"},
		},
		{
			name:     "message with no instance IDs",
			msg:      "You are not authorized to perform this operation",
			expected: nil,
		},
		{
			name:     "empty message",
			msg:      "",
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStaleInstanceIDs(tc.msg)
			if !slices.Equal(got, tc.expected) {
				t.Errorf("parseStaleInstanceIDs(%q) = %v, want %v", tc.msg, got, tc.expected)
			}
		})
	}
}

type stubDescribeInstancesClient struct {
	calls []ec2.DescribeInstancesInput
	// responses[i] is returned on the i-th call; defaults to (empty output, nil) if exhausted.
	responses []stubDescribeResponse
}

type stubDescribeResponse struct {
	out *ec2.DescribeInstancesOutput
	err error
}

func (s *stubDescribeInstancesClient) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	s.calls = append(s.calls, *params)
	idx := len(s.calls) - 1
	if idx >= len(s.responses) {
		return &ec2.DescribeInstancesOutput{}, nil
	}
	r := s.responses[idx]
	return r.out, r.err
}

func TestDescribeInstancesTolerant(t *testing.T) {
	ctx := context.Background()
	staleErr := &smithy.GenericAPIError{
		Code:    "InvalidInstanceID.NotFound",
		Message: "The instance IDs 'i-0abc1234, i-0def5678' do not exist",
	}

	t.Run("success on first call returns output unchanged", func(t *testing.T) {
		expected := &ec2.DescribeInstancesOutput{Reservations: []ec2Types.Reservation{{}}}
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{{out: expected, err: nil}},
		}
		out, err := describeInstancesTolerant(ctx, stub, []string{"i-a", "i-b"}, "asg-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != expected {
			t.Errorf("expected output to be passed through unchanged")
		}
		if len(stub.calls) != 1 {
			t.Errorf("expected 1 call, got %d", len(stub.calls))
		}
	})

	t.Run("stale IDs removed and retry succeeds", func(t *testing.T) {
		retryOut := &ec2.DescribeInstancesOutput{Reservations: []ec2Types.Reservation{{}}}
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{
				{out: nil, err: staleErr},
				{out: retryOut, err: nil},
			},
		}
		out, err := describeInstancesTolerant(ctx, stub, []string{"i-0abc1234", "i-0def5678", "i-0999aabb"}, "asg-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != retryOut {
			t.Errorf("expected retry output to be returned")
		}
		if len(stub.calls) != 2 {
			t.Fatalf("expected 2 calls (original + retry), got %d", len(stub.calls))
		}
		if !slices.Equal(stub.calls[1].InstanceIds, []string{"i-0999aabb"}) {
			t.Errorf("retry InstanceIds = %v, want [i-0999aabb]", stub.calls[1].InstanceIds)
		}
	})

	t.Run("all IDs stale returns empty output without retry", func(t *testing.T) {
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{{out: nil, err: staleErr}},
		}
		out, err := describeInstancesTolerant(ctx, stub, []string{"i-0abc1234", "i-0def5678"}, "asg-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out == nil || len(out.Reservations) != 0 {
			t.Errorf("expected empty output, got %+v", out)
		}
		if len(stub.calls) != 1 {
			t.Errorf("expected 1 call (no retry when all stale), got %d", len(stub.calls))
		}
	})

	t.Run("non-stale error returned as-is", func(t *testing.T) {
		otherErr := &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "denied"}
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{{out: nil, err: otherErr}},
		}
		_, err := describeInstancesTolerant(ctx, stub, []string{"i-a"}, "asg-1")
		if !errors.Is(err, otherErr) {
			t.Errorf("expected pass-through of UnauthorizedOperation, got %v", err)
		}
		if len(stub.calls) != 1 {
			t.Errorf("expected no retry for non-stale error, got %d calls", len(stub.calls))
		}
	})

	t.Run("second NotFound on retry is not retried again", func(t *testing.T) {
		secondStaleErr := &smithy.GenericAPIError{
			Code:    "InvalidInstanceID.NotFound",
			Message: "The instance ID 'i-0999aabb00' does not exist",
		}
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{
				{out: nil, err: staleErr},
				{out: nil, err: secondStaleErr},
			},
		}
		_, err := describeInstancesTolerant(ctx, stub, []string{"i-0abc1234", "i-0def5678", "i-0999aabb00"}, "asg-1")
		if !errors.Is(err, secondStaleErr) {
			t.Errorf("expected second NotFound to propagate, got %v", err)
		}
		if len(stub.calls) != 2 {
			t.Errorf("expected exactly 2 calls (no second retry), got %d", len(stub.calls))
		}
	})

	t.Run("stale error with no parseable IDs returned as-is", func(t *testing.T) {
		malformed := &smithy.GenericAPIError{
			Code:    "InvalidInstanceID.NotFound",
			Message: "something went wrong but no IDs are in this message",
		}
		stub := &stubDescribeInstancesClient{
			responses: []stubDescribeResponse{{out: nil, err: malformed}},
		}
		_, err := describeInstancesTolerant(ctx, stub, []string{"i-a"}, "asg-1")
		if !errors.Is(err, malformed) {
			t.Errorf("expected pass-through when no IDs parseable, got %v", err)
		}
	})
}

func TestGetCheckCommand(t *testing.T) {
	driver := &ASGDriver{}

	testCases := []struct {
		name                string
		platform            string
		expectedContains    []string
		expectedNotContains []string
	}{
		{
			name:     "linux command uses systemctl",
			platform: "linux",
			expectedContains: []string{
				"#!/bin/bash",
				"systemctl show buildkite-agent",
				"ActiveState",
				"RUNNING",
				"NOT_RUNNING",
				"MARKER_EXISTS",
			},
			expectedNotContains: []string{
				"PowerShell",
				"Get-Service",
			},
		},
		{
			name:     "windows command uses nssm",
			platform: "windows",
			expectedContains: []string{
				"nssm status buildkite-agent",
				"SERVICE_RUNNING",
				"RUNNING",
				"NOT_RUNNING",
			},
			expectedNotContains: []string{
				"#!/bin/bash",
				"systemctl",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := driver.getCheckCommand(tc.platform)

			for _, expected := range tc.expectedContains {
				if !strings.Contains(cmd, expected) {
					t.Errorf("expected command to contain %q, but it didn't.\nCommand: %s", expected, cmd)
				}
			}

			for _, notExpected := range tc.expectedNotContains {
				if strings.Contains(cmd, notExpected) {
					t.Errorf("expected command NOT to contain %q, but it did.\nCommand: %s", notExpected, cmd)
				}
			}
		})
	}
}

func TestRotateInstanceWindowSmallerThanWindow(t *testing.T) {
	// When len(sorted) <= windowSize, all instances are returned without rotation.
	instances := makeInstancesForRotation(3)
	got := rotateInstanceWindow(instances, 5, time.Minute, time.Unix(0, 0))
	if len(got) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(got))
	}
	for i := range instances {
		if *got[i].InstanceId != *instances[i].InstanceId {
			t.Errorf("at index %d: got %q, want %q", i, *got[i].InstanceId, *instances[i].InstanceId)
		}
	}
}

func TestRotateInstanceWindowCoversFullList(t *testing.T) {
	// Over enough invocations, every instance in a fleet larger than windowSize
	// should be examined at least once.
	const (
		total         = 100
		windowSize    = 5
		checkInterval = time.Minute
	)
	instances := makeInstancesForRotation(total)

	seen := make(map[string]bool, total)
	for i := 0; i < (total/windowSize)+2; i++ {
		at := time.Unix(int64(i)*int64(checkInterval.Seconds()), 0)
		window := rotateInstanceWindow(instances, windowSize, checkInterval, at)
		if len(window) != windowSize {
			t.Fatalf("iteration %d: expected window of %d, got %d", i, windowSize, len(window))
		}
		for _, inst := range window {
			seen[*inst.InstanceId] = true
		}
	}

	if len(seen) != total {
		var missing []string
		for _, inst := range instances {
			if !seen[*inst.InstanceId] {
				missing = append(missing, *inst.InstanceId)
			}
		}
		t.Errorf("expected all %d instances to be checked, missing: %v", total, missing)
	}
}

func TestRotateInstanceWindowZeroPeriodDefaultsToSixty(t *testing.T) {
	instances := makeInstancesForRotation(20)
	got := rotateInstanceWindow(instances, 5, 0, time.Unix(60, 0))
	if len(got) != 5 {
		t.Fatalf("expected window of 5, got %d", len(got))
	}
	// tick = 60/60 = 1; offset = (1 * 5) % 20 = 5.
	if *got[0].InstanceId != "i-00000005" {
		t.Errorf("expected first instance i-00000005 at offset 5, got %s", *got[0].InstanceId)
	}
}

func TestRotateInstanceWindowAdvancesEveryPeriod(t *testing.T) {
	instances := makeInstancesForRotation(20)
	prev := rotateInstanceWindow(instances, 5, time.Minute, time.Unix(0, 0))
	next := rotateInstanceWindow(instances, 5, time.Minute, time.Unix(60, 0))

	if *prev[0].InstanceId == *next[0].InstanceId {
		t.Errorf("window did not advance across a period boundary: both started at %s", *prev[0].InstanceId)
	}
	if *next[0].InstanceId != "i-00000005" {
		t.Errorf("expected next window to start at i-00000005 (offset 5), got %s", *next[0].InstanceId)
	}
}

func makeInstancesForRotation(n int) []ec2Types.Instance {
	out := make([]ec2Types.Instance, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("i-%08x", i)
		out = append(out, ec2Types.Instance{InstanceId: aws.String(id)})
	}
	return out
}

// stubSSMClient implements ssmCheckAPI for unit tests. Each
// ListCommandInvocations call returns the next listResponses entry, or repeats
// the last one if exhausted (so deadline-driven tests don't have to enumerate
// every poll).
type stubSSMClient struct {
	describeOut   *ssm.DescribeInstanceInformationOutput
	describeErr   error
	listResponses []stubListResponse
	listCalls     int

	sendErr     error
	sendBatches [][]string // InstanceIds passed to each SendCommand call
}

type stubListResponse struct {
	out *ssm.ListCommandInvocationsOutput
	err error
}

func (s *stubSSMClient) DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return s.describeOut, s.describeErr
}

func (s *stubSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	s.sendBatches = append(s.sendBatches, params.InstanceIds)
	if s.sendErr != nil {
		return nil, s.sendErr
	}
	id := fmt.Sprintf("cmd-%d", len(s.sendBatches))
	return &ssm.SendCommandOutput{Command: &ssmTypes.Command{CommandId: aws.String(id)}}, nil
}

// stubASGClient implements asgHealthAPI, recording SetInstanceHealth calls.
type stubASGClient struct {
	setHealthErr    error
	markedUnhealthy []string
}

func (s *stubASGClient) SetInstanceHealth(ctx context.Context, params *autoscaling.SetInstanceHealthInput, _ ...func(*autoscaling.Options)) (*autoscaling.SetInstanceHealthOutput, error) {
	if s.setHealthErr != nil {
		return nil, s.setHealthErr
	}
	s.markedUnhealthy = append(s.markedUnhealthy, aws.ToString(params.InstanceId))
	return &autoscaling.SetInstanceHealthOutput{}, nil
}

func (s *stubSSMClient) ListCommandInvocations(ctx context.Context, params *ssm.ListCommandInvocationsInput, _ ...func(*ssm.Options)) (*ssm.ListCommandInvocationsOutput, error) {
	idx := s.listCalls
	s.listCalls++
	if idx >= len(s.listResponses) {
		if n := len(s.listResponses); n > 0 {
			r := s.listResponses[n-1]
			return r.out, r.err
		}
		return &ssm.ListCommandInvocationsOutput{}, nil
	}
	r := s.listResponses[idx]
	return r.out, r.err
}

func TestFilterOnlineSSMInstances(t *testing.T) {
	stub := &stubSSMClient{
		describeOut: &ssm.DescribeInstanceInformationOutput{
			InstanceInformationList: []ssmTypes.InstanceInformation{
				{InstanceId: aws.String("i-a"), PingStatus: ssmTypes.PingStatusOnline},
				{InstanceId: aws.String("i-b"), PingStatus: ssmTypes.PingStatusConnectionLost},
				{InstanceId: aws.String("i-c"), PingStatus: ssmTypes.PingStatusOnline},
			},
		},
	}
	got, err := filterOnlineSSMInstances(context.Background(), stub, []string{"i-a", "i-b", "i-c", "i-d"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(got)
	if !slices.Equal(got, []string{"i-a", "i-c"}) {
		t.Errorf("got %v, want [i-a i-c]", got)
	}
}

func TestPollCommandInvocations(t *testing.T) {
	ctx := context.Background()
	inv := func(id string, status ssmTypes.CommandInvocationStatus) ssmTypes.CommandInvocation {
		return ssmTypes.CommandInvocation{InstanceId: aws.String(id), Status: status}
	}

	t.Run("returns when all expected reach terminal status", func(t *testing.T) {
		stub := &stubSSMClient{listResponses: []stubListResponse{
			{out: &ssm.ListCommandInvocationsOutput{CommandInvocations: []ssmTypes.CommandInvocation{
				inv("i-a", ssmTypes.CommandInvocationStatusInProgress),
			}}},
			{out: &ssm.ListCommandInvocationsOutput{CommandInvocations: []ssmTypes.CommandInvocation{
				inv("i-a", ssmTypes.CommandInvocationStatusSuccess),
				inv("i-b", ssmTypes.CommandInvocationStatusSuccess),
			}}},
		}}
		results, err := pollCommandInvocations(ctx, stub, []string{"cmd-1"}, 2, time.Millisecond, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results["i-a"].Status != ssmTypes.CommandInvocationStatusSuccess ||
			results["i-b"].Status != ssmTypes.CommandInvocationStatusSuccess {
			t.Errorf("expected both Success, got %+v", results)
		}
		if stub.listCalls != 2 {
			t.Errorf("expected 2 list calls, got %d", stub.listCalls)
		}
	})

	t.Run("follows NextToken within a single poll", func(t *testing.T) {
		stub := &stubSSMClient{listResponses: []stubListResponse{
			{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{inv("i-a", ssmTypes.CommandInvocationStatusSuccess)},
				NextToken:          aws.String("page-2"),
			}},
			{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{inv("i-b", ssmTypes.CommandInvocationStatusSuccess)},
			}},
		}}
		results, err := pollCommandInvocations(ctx, stub, []string{"cmd-1"}, 2, time.Millisecond, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results["i-a"].Status != ssmTypes.CommandInvocationStatusSuccess ||
			results["i-b"].Status != ssmTypes.CommandInvocationStatusSuccess {
			t.Errorf("expected both Success across pages, got %+v", results)
		}
		if stub.listCalls != 2 {
			t.Errorf("expected 2 paginated list calls in one poll, got %d", stub.listCalls)
		}
	})

	t.Run("returns partial result on deadline", func(t *testing.T) {
		stub := &stubSSMClient{listResponses: []stubListResponse{
			{out: &ssm.ListCommandInvocationsOutput{CommandInvocations: []ssmTypes.CommandInvocation{
				inv("i-a", ssmTypes.CommandInvocationStatusInProgress),
			}}},
		}}
		// expected=2 with only InProgress for i-a; never terminal, deadline elapses.
		results, err := pollCommandInvocations(ctx, stub, []string{"cmd-1"}, 2, time.Millisecond, 5*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results["i-a"].Status != ssmTypes.CommandInvocationStatusInProgress {
			t.Errorf("expected InProgress partial result, got %v", results["i-a"].Status)
		}
	})

	t.Run("API error propagates", func(t *testing.T) {
		stub := &stubSSMClient{listResponses: []stubListResponse{{err: errors.New("throttled")}}}
		_, err := pollCommandInvocations(ctx, stub, []string{"cmd-1"}, 1, time.Millisecond, time.Second)
		if err == nil || !strings.Contains(err.Error(), "throttled") {
			t.Errorf("expected throttled error, got %v", err)
		}
	})
}

func TestCheckAndMarkUnhealthy(t *testing.T) {
	ctx := context.Background()
	// Fast timings so the test doesn't sleep on real SSM defaults.
	driver := &ASGDriver{
		ssmRegistrationDelay: time.Millisecond,
		ssmPollInterval:      time.Millisecond,
		ssmPollDeadline:      time.Second,
	}
	online := func(ids ...string) *ssm.DescribeInstanceInformationOutput {
		out := &ssm.DescribeInstanceInformationOutput{}
		for _, id := range ids {
			out.InstanceInformationList = append(out.InstanceInformationList,
				ssmTypes.InstanceInformation{InstanceId: aws.String(id), PingStatus: ssmTypes.PingStatusOnline})
		}
		return out
	}
	invOut := func(id string, status ssmTypes.CommandInvocationStatus, output string) ssmTypes.CommandInvocation {
		return ssmTypes.CommandInvocation{
			InstanceId:     aws.String(id),
			Status:         status,
			CommandPlugins: []ssmTypes.CommandPlugin{{Output: aws.String(output)}},
		}
	}

	t.Run("marks dangling instances and skips healthy or already-marked", func(t *testing.T) {
		ssmStub := &stubSSMClient{
			describeOut: online("i-dangling", "i-healthy", "i-failed", "i-marked"),
			listResponses: []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{
					invOut("i-dangling", ssmTypes.CommandInvocationStatusSuccess, "NOT_RUNNING: dead"),
					invOut("i-healthy", ssmTypes.CommandInvocationStatusSuccess, "RUNNING"),
					invOut("i-failed", ssmTypes.CommandInvocationStatusFailed, ""),
					invOut("i-marked", ssmTypes.CommandInvocationStatusSuccess, "MARKER_EXISTS: already marked"),
				},
			}}},
		}
		asgStub := &stubASGClient{}

		// i-offline is not in the describe output, so it should be filtered out.
		marked, checked, err := driver.checkAndMarkUnhealthy(ctx,
			[]string{"i-dangling", "i-healthy", "i-failed", "i-marked", "i-offline"},
			ssmStub, asgStub, "linux")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if checked != 4 {
			t.Errorf("checkedCount = %d, want 4", checked)
		}
		if marked != 2 {
			t.Errorf("markedUnhealthyCount = %d, want 2", marked)
		}
		got := slices.Clone(asgStub.markedUnhealthy)
		sort.Strings(got)
		if !slices.Equal(got, []string{"i-dangling", "i-failed"}) {
			t.Errorf("marked unhealthy = %v, want [i-dangling i-failed]", got)
		}
		if len(ssmStub.sendBatches) != 1 || len(ssmStub.sendBatches[0]) != 4 {
			t.Errorf("expected one SendCommand of 4 instances, got %v", ssmStub.sendBatches)
		}
	})

	t.Run("chunks SendCommand over the 50-instance limit and aggregates results", func(t *testing.T) {
		ids := make([]string, 51)
		invs := make([]ssmTypes.CommandInvocation, 51)
		for i := range ids {
			ids[i] = fmt.Sprintf("i-%03d", i)
			invs[i] = invOut(ids[i], ssmTypes.CommandInvocationStatusSuccess, "RUNNING")
		}
		ssmStub := &stubSSMClient{
			describeOut:   online(ids...),
			listResponses: []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{CommandInvocations: invs}}},
		}

		_, checked, err := driver.checkAndMarkUnhealthy(ctx, ids, ssmStub, &stubASGClient{}, "linux")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 51 instances split into 50 + 1, and results from both batches aggregate.
		gotSizes := make([]int, len(ssmStub.sendBatches))
		for i, b := range ssmStub.sendBatches {
			gotSizes[i] = len(b)
		}
		if !slices.Equal(gotSizes, []int{50, 1}) {
			t.Errorf("SendCommand batch sizes = %v, want [50 1]", gotSizes)
		}
		if checked != 51 {
			t.Errorf("checkedCount = %d, want 51", checked)
		}
	})
}
