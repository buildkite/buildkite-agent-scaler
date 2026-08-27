package scaler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

func TestGetASGPlatform(t *testing.T) {
	driver := &ASGDriver{}

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
			platform := driver.getASGPlatform(tc.instances)
			if platform != tc.expectedPlatform {
				t.Errorf("expected platform %q, got %q", tc.expectedPlatform, platform)
			}
		})
	}
}

func TestAutoscaleGroupDetailsUsesOnlyInServiceInstancesAsCandidates(t *testing.T) {
	details := autoscaleGroupDetails(types.AutoScalingGroup{
		DesiredCapacity: aws.Int32(4),
		MinSize:         aws.Int32(1),
		MaxSize:         aws.Int32(10),
		Instances: []types.Instance{
			{InstanceId: aws.String("i-pending"), LifecycleState: types.LifecycleStatePending},
			{InstanceId: aws.String("i-pending-wait"), LifecycleState: types.LifecycleStatePendingWait},
			{InstanceId: aws.String("i-in-service"), LifecycleState: types.LifecycleStateInService},
			{InstanceId: aws.String("i-terminating"), LifecycleState: types.LifecycleStateTerminating},
			{InstanceId: aws.String("i-standby"), LifecycleState: types.LifecycleStateStandby},
		},
	})

	if got, want := details.Pending, int64(2); got != want {
		t.Errorf("pending count = %d, want %d", got, want)
	}
	if got, want := details.ActualCount, int64(1); got != want {
		t.Errorf("actual count = %d, want %d", got, want)
	}
	if got, want := details.InstanceIDs, []string{"i-in-service"}; !slices.Equal(got, want) {
		t.Errorf("scale-in candidates = %v, want %v", got, want)
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

func TestDetectPlatform(t *testing.T) {
	ctx := context.Background()
	driver := &ASGDriver{Name: "asg-1"}
	describeErr := errors.New("describe failed")

	reservations := func(platforms ...ec2Types.PlatformValues) []ec2Types.Reservation {
		var instances []ec2Types.Instance
		for _, p := range platforms {
			instances = append(instances, ec2Types.Instance{Platform: p})
		}
		return []ec2Types.Reservation{{Instances: instances}}
	}

	tests := []struct {
		name         string
		response     stubDescribeResponse
		wantPlatform string
		wantErr      bool
	}{
		{
			name:         "linux instance",
			response:     stubDescribeResponse{out: &ec2.DescribeInstancesOutput{Reservations: reservations("")}},
			wantPlatform: "linux",
		},
		{
			name:         "windows instance",
			response:     stubDescribeResponse{out: &ec2.DescribeInstancesOutput{Reservations: reservations("windows")}},
			wantPlatform: "windows",
		},
		{
			name:     "describe error fails closed",
			response: stubDescribeResponse{err: describeErr},
			wantErr:  true,
		},
		{
			// The sampled candidate can disappear between candidate selection
			// and the describe call. Guessing Linux for a Windows ASG here
			// would skip the SetDesiredCapacity fallback, so fail closed.
			name:     "missing instance fails closed",
			response: stubDescribeResponse{out: &ec2.DescribeInstancesOutput{}},
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubDescribeInstancesClient{responses: []stubDescribeResponse{tc.response}}
			platform, err := driver.detectPlatform(ctx, stub, []string{"i-a", "i-b"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("detectPlatform() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("detectPlatform() error = %v", err)
			}
			if platform != tc.wantPlatform {
				t.Errorf("detectPlatform() = %q, want %q", platform, tc.wantPlatform)
			}
			if got := stub.calls[0].InstanceIds; !slices.Equal(got, []string{"i-a"}) {
				t.Errorf("described %v, want only the first candidate", got)
			}
		})
	}
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
			},
			expectedNotContains: []string{
				"PowerShell",
				"Get-Service",
				"MARKER_EXISTS",
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

func TestLinuxCheckCommand(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(`#!/bin/sh
if [ -n "$SYSTEMCTL_ERROR" ]; then
  echo "$SYSTEMCTL_ERROR"
  exit 1
fi
printf 'ActiveState=%s\nMainPID=%s\n' "$ACTIVE_STATE" "$MAIN_PID"
`), 0o755); err != nil {
		t.Fatalf("write systemctl stub: %v", err)
	}

	tests := []struct {
		name         string
		activeState  string
		mainPID      string
		marker       bool
		systemctlErr string
		want         string
	}{
		{name: "running", activeState: "active", mainPID: "123", want: "RUNNING"},
		{name: "draining", activeState: "active", mainPID: "123", marker: true, want: "DRAINING: ActiveState=active MainPID=123"},
		{name: "marked but stopped", activeState: "inactive", mainPID: "0", marker: true, want: "NOT_RUNNING: ActiveState=inactive"},
		{name: "activating", activeState: "activating", mainPID: "0", marker: true, want: "DRAINING: ActiveState=activating MainPID=0"},
		// A live process in deactivating may still be draining a job, so it
		// must not be classified NOT_RUNNING while the PID exists.
		{name: "deactivating without marker", activeState: "deactivating", mainPID: "123", want: "RUNNING"},
		// The normal graceful scale-in window: marker written, systemd
		// stopping the unit, agent finishing its last job.
		{name: "deactivating with marker", activeState: "deactivating", mainPID: "123", marker: true, want: "DRAINING: ActiveState=deactivating MainPID=123"},
		// After the agent exits, ExecStopPost (the stack's terminate-instance
		// script) runs with MainPID=0 while the unit stays deactivating. The
		// instance is about to decrement desired capacity and terminate
		// itself, so marking it unhealthy here would race that handoff.
		{name: "deactivating in ExecStopPost with marker", activeState: "deactivating", mainPID: "0", marker: true, want: "DRAINING: ActiveState=deactivating MainPID=0"},
		{name: "deactivating in ExecStopPost without marker", activeState: "deactivating", mainPID: "0", want: "RUNNING"},
		{name: "failed unit", activeState: "failed", mainPID: "0", want: "NOT_RUNNING: ActiveState=failed"},
		// Type=simple assumption: active with MainPID=0 means the process is
		// gone even though systemd still reports the unit active.
		{name: "active without main PID", activeState: "active", mainPID: "0", want: "NOT_RUNNING: ActiveState=active"},
		{name: "systemctl failure", systemctlErr: "systemctl unavailable", want: "UNKNOWN: systemctl unavailable"},
		{name: "malformed PID", activeState: "active", mainPID: "invalid", want: "UNKNOWN: ActiveState=active MainPID=invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "termination-marker")
			if tc.marker {
				if err := os.WriteFile(marker, nil, 0o600); err != nil {
					t.Fatalf("write termination marker: %v", err)
				}
			}

			script := strings.ReplaceAll((&ASGDriver{}).getCheckCommand("linux"), terminationMarkerPath, marker)
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+":"+os.Getenv("PATH"),
				"ACTIVE_STATE="+tc.activeState,
				"MAIN_PID="+tc.mainPID,
				"SYSTEMCTL_ERROR="+tc.systemctlErr,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run check command: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
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
// every poll). DescribeInstanceInformation resolves in priority order:
// describeResponses (one entry per call), then describeErr, then describeOut
// filtered down to the instance IDs requested in that call.
type stubSSMClient struct {
	describeOut       *ssm.DescribeInstanceInformationOutput
	describeErr       error
	describeResponses []stubSSMDescribeResponse
	describeCalls     []ssm.DescribeInstanceInformationInput
	listResponses     []stubListResponse
	listCalls         int

	sendErr     error
	sendErrors  []error
	sendBatches [][]string // InstanceIds passed to each SendCommand call
}

type stubSSMDescribeResponse struct {
	out *ssm.DescribeInstanceInformationOutput
	err error
}

type stubListResponse struct {
	out *ssm.ListCommandInvocationsOutput
	err error
}

func (s *stubSSMClient) DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	s.describeCalls = append(s.describeCalls, *params)
	if len(s.describeResponses) > 0 {
		response := s.describeResponses[len(s.describeCalls)-1]
		return response.out, response.err
	}
	if s.describeErr != nil {
		return nil, s.describeErr
	}
	if s.describeOut == nil {
		return &ssm.DescribeInstanceInformationOutput{}, nil
	}

	requested := make(map[string]bool)
	for _, filter := range params.Filters {
		if aws.ToString(filter.Key) == "InstanceIds" {
			for _, instanceID := range filter.Values {
				requested[instanceID] = true
			}
		}
	}
	output := &ssm.DescribeInstanceInformationOutput{}
	for _, info := range s.describeOut.InstanceInformationList {
		if requested[aws.ToString(info.InstanceId)] {
			output.InstanceInformationList = append(output.InstanceInformationList, info)
		}
	}
	return output, nil
}

func (s *stubSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	s.sendBatches = append(s.sendBatches, slices.Clone(params.InstanceIds))
	if index := len(s.sendBatches) - 1; index < len(s.sendErrors) && s.sendErrors[index] != nil {
		return nil, s.sendErrors[index]
	}
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
	if got := aws.ToInt32(stub.describeCalls[0].MaxResults); got != ssmMaxInstanceIDs {
		t.Errorf("MaxResults = %d, want %d", got, ssmMaxInstanceIDs)
	}
}

func TestFilterOnlineSSMInstancesChunksAndPaginates(t *testing.T) {
	instanceIDs := make([]string, 51)
	for i := range instanceIDs {
		instanceIDs[i] = fmt.Sprintf("i-%03d", i)
	}
	stub := &stubSSMClient{
		describeResponses: []stubSSMDescribeResponse{
			{out: &ssm.DescribeInstanceInformationOutput{
				InstanceInformationList: []ssmTypes.InstanceInformation{{InstanceId: aws.String("i-000"), PingStatus: ssmTypes.PingStatusOnline}},
				NextToken:               aws.String("page-2"),
			}},
			{out: &ssm.DescribeInstanceInformationOutput{
				InstanceInformationList: []ssmTypes.InstanceInformation{{InstanceId: aws.String("i-001"), PingStatus: ssmTypes.PingStatusOnline}},
			}},
			{out: &ssm.DescribeInstanceInformationOutput{
				InstanceInformationList: []ssmTypes.InstanceInformation{{InstanceId: aws.String("i-050"), PingStatus: ssmTypes.PingStatusOnline}},
			}},
		},
	}

	got, err := filterOnlineSSMInstances(t.Context(), stub, instanceIDs)
	if err != nil {
		t.Fatalf("filterOnlineSSMInstances() error = %v", err)
	}
	if want := []string{"i-000", "i-001", "i-050"}; !slices.Equal(got, want) {
		t.Errorf("online instances = %v, want %v", got, want)
	}
	if got, want := len(stub.describeCalls), 3; got != want {
		t.Fatalf("DescribeInstanceInformation calls = %d, want %d", got, want)
	}
	if got, want := len(stub.describeCalls[0].Filters[0].Values), ssmMaxInstanceIDs; got != want {
		t.Errorf("first instance filter size = %d, want %d", got, want)
	}
	if got, want := aws.ToString(stub.describeCalls[1].NextToken), "page-2"; got != want {
		t.Errorf("second NextToken = %q, want %q", got, want)
	}
	if got, want := len(stub.describeCalls[2].Filters[0].Values), 1; got != want {
		t.Errorf("last instance filter size = %d, want %d", got, want)
	}
}

func TestSendSIGTERMToAgentsBatch(t *testing.T) {
	onlineOutput := func(instanceIDs ...string) *ssm.DescribeInstanceInformationOutput {
		output := &ssm.DescribeInstanceInformationOutput{}
		for _, instanceID := range instanceIDs {
			output.InstanceInformationList = append(output.InstanceInformationList, ssmTypes.InstanceInformation{
				InstanceId: aws.String(instanceID),
				PingStatus: ssmTypes.PingStatusOnline,
			})
		}
		return output
	}

	t.Run("chunks more than 50 targets without polling completion", func(t *testing.T) {
		instanceIDs := make([]string, 120)
		for i := range instanceIDs {
			instanceIDs[i] = fmt.Sprintf("i-%03d", i)
		}
		stub := &stubSSMClient{describeOut: onlineOutput(instanceIDs...)}

		dispatched, err := (&ASGDriver{}).sendSIGTERMToAgentsBatch(t.Context(), stub, instanceIDs, "linux")
		if err != nil {
			t.Fatalf("sendSIGTERMToAgentsBatch() error = %v", err)
		}
		if dispatched != 120 {
			t.Errorf("dispatched instances = %d, want 120", dispatched)
		}
		gotBatchSizes := make([]int, len(stub.sendBatches))
		for i, batch := range stub.sendBatches {
			gotBatchSizes[i] = len(batch)
		}
		if want := []int{50, 50, 20}; !slices.Equal(gotBatchSizes, want) {
			t.Errorf("SendCommand batch sizes = %v, want %v", gotBatchSizes, want)
		}
		if stub.listCalls != 0 {
			t.Errorf("ListCommandInvocations calls = %d, want 0", stub.listCalls)
		}
	})

	t.Run("skips targets whose SSM agent is offline", func(t *testing.T) {
		stub := &stubSSMClient{describeOut: onlineOutput("i-a", "i-c")}

		dispatched, err := (&ASGDriver{}).sendSIGTERMToAgentsBatch(t.Context(), stub, []string{"i-a", "i-b", "i-c"}, "linux")
		if err != nil {
			t.Fatalf("sendSIGTERMToAgentsBatch() error = %v", err)
		}
		if dispatched != 2 {
			t.Errorf("dispatched instances = %d, want 2", dispatched)
		}
		if len(stub.sendBatches) != 1 || !slices.Equal(stub.sendBatches[0], []string{"i-a", "i-c"}) {
			t.Errorf("SendCommand targets = %v, want [[i-a i-c]]", stub.sendBatches)
		}
	})

	t.Run("reports no dispatch when every SSM agent is offline", func(t *testing.T) {
		stub := &stubSSMClient{}

		dispatched, err := (&ASGDriver{}).sendSIGTERMToAgentsBatch(t.Context(), stub, []string{"i-a", "i-b"}, "linux")
		if err != nil {
			t.Fatalf("sendSIGTERMToAgentsBatch() error = %v", err)
		}
		if dispatched != 0 {
			t.Errorf("dispatched instances = %d, want 0", dispatched)
		}
		if len(stub.sendBatches) != 0 {
			t.Errorf("SendCommand calls = %d, want 0", len(stub.sendBatches))
		}
	})

	t.Run("attempts later batches after partial failures", func(t *testing.T) {
		instanceIDs := make([]string, 120)
		for i := range instanceIDs {
			instanceIDs[i] = fmt.Sprintf("i-%03d", i)
		}
		firstErr := errors.New("first batch failed")
		lastErr := errors.New("last batch failed")
		stub := &stubSSMClient{
			describeOut: onlineOutput(instanceIDs...),
			sendErrors:  []error{firstErr, nil, lastErr},
		}

		dispatched, err := (&ASGDriver{}).sendSIGTERMToAgentsBatch(t.Context(), stub, instanceIDs, "linux")
		if !errors.Is(err, firstErr) || !errors.Is(err, lastErr) {
			t.Errorf("sendSIGTERMToAgentsBatch() error = %v, want both batch errors", err)
		}
		if dispatched != 50 {
			t.Errorf("dispatched instances = %d, want 50", dispatched)
		}
		if got, want := len(stub.sendBatches), 3; got != want {
			t.Errorf("SendCommand calls = %d, want %d", got, want)
		}
	})

	t.Run("rejects Windows before contacting SSM", func(t *testing.T) {
		stub := &stubSSMClient{}

		dispatched, err := (&ASGDriver{}).sendSIGTERMToAgentsBatch(t.Context(), stub, []string{"i-windows"}, "windows")
		if !errors.Is(err, ErrWindowsGracefulScaleInNotSupported) {
			t.Errorf("sendSIGTERMToAgentsBatch() error = %v, want %v", err, ErrWindowsGracefulScaleInNotSupported)
		}
		if dispatched != 0 {
			t.Errorf("dispatched instances = %d, want 0", dispatched)
		}
		if len(stub.describeCalls) != 0 || len(stub.sendBatches) != 0 {
			t.Errorf("SSM calls made for Windows: describe=%d send=%d, want none", len(stub.describeCalls), len(stub.sendBatches))
		}
	})
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

	t.Run("classifies conclusive results", func(t *testing.T) {
		ssmStub := &stubSSMClient{
			describeOut: online("i-dangling", "i-healthy", "i-draining"),
			listResponses: []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{
					invOut("i-dangling", ssmTypes.CommandInvocationStatusSuccess, "NOT_RUNNING: dead"),
					invOut("i-healthy", ssmTypes.CommandInvocationStatusSuccess, "RUNNING"),
					invOut("i-draining", ssmTypes.CommandInvocationStatusSuccess, "DRAINING: ActiveState=active MainPID=123"),
				},
			}}},
		}
		asgStub := &stubASGClient{}

		result, err := driver.checkAndMarkUnhealthy(ctx,
			[]string{"i-dangling", "i-healthy", "i-draining"},
			ssmStub, asgStub, "linux")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := (danglingCheckResult{healthy: 1, draining: 1, markedUnhealthy: 1}); result != want {
			t.Errorf("result = %+v, want %+v", result, want)
		}
		if !slices.Equal(asgStub.markedUnhealthy, []string{"i-dangling"}) {
			t.Errorf("marked unhealthy = %v, want [i-dangling]", asgStub.markedUnhealthy)
		}
	})

	t.Run("treats failed or ambiguous results as inconclusive", func(t *testing.T) {
		ids := []string{"i-failed", "i-timed-out", "i-future-status", "i-empty", "i-ambiguous", "i-unknown"}
		ssmStub := &stubSSMClient{
			describeOut: online(ids...),
			listResponses: []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{
					invOut("i-failed", ssmTypes.CommandInvocationStatusFailed, "NOT_RUNNING: dead"),
					invOut("i-timed-out", ssmTypes.CommandInvocationStatusTimedOut, ""),
					invOut("i-future-status", ssmTypes.CommandInvocationStatus("FutureStatus"), ""),
					invOut("i-empty", ssmTypes.CommandInvocationStatusSuccess, ""),
					invOut("i-ambiguous", ssmTypes.CommandInvocationStatusSuccess, "RUNNING: maybe"),
					invOut("i-unknown", ssmTypes.CommandInvocationStatusSuccess, "UNKNOWN: systemctl failed"),
				},
			}}},
		}
		asgStub := &stubASGClient{}

		result, err := driver.checkAndMarkUnhealthy(ctx, ids, ssmStub, asgStub, "linux")
		if err == nil {
			t.Fatal("expected an inconclusive-result error")
		}
		if result != (danglingCheckResult{}) {
			t.Errorf("result = %+v, want all zero", result)
		}
		if len(asgStub.markedUnhealthy) != 0 {
			t.Errorf("marked unhealthy = %v, want none", asgStub.markedUnhealthy)
		}
	})

	t.Run("excludes a failed unhealthy mark from every bucket", func(t *testing.T) {
		ssmStub := &stubSSMClient{
			describeOut: online("i-dangling"),
			listResponses: []stubListResponse{{out: &ssm.ListCommandInvocationsOutput{
				CommandInvocations: []ssmTypes.CommandInvocation{
					invOut("i-dangling", ssmTypes.CommandInvocationStatusSuccess, "NOT_RUNNING: dead"),
				},
			}}},
		}
		asgStub := &stubASGClient{setHealthErr: errors.New("unavailable")}

		result, err := driver.checkAndMarkUnhealthy(ctx, []string{"i-dangling"}, ssmStub, asgStub, "linux")
		if err == nil {
			t.Fatal("expected SetInstanceHealth error")
		}
		if result != (danglingCheckResult{}) {
			t.Errorf("result = %+v, want all zero", result)
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

		result, err := driver.checkAndMarkUnhealthy(ctx, ids, ssmStub, &stubASGClient{}, "linux")
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
		if result.healthy != 51 {
			t.Errorf("healthy = %d, want 51", result.healthy)
		}
	})
}
