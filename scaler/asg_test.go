package scaler

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
