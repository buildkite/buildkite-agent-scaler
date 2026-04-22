package scaler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
