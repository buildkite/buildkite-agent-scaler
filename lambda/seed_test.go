package main

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

func TestSeedLastScaleTimes(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	scaleOutAt := now.Add(-30 * time.Minute)
	scaleInAt := now.Add(-10 * time.Minute)

	testCases := []struct {
		name           string
		scaleOut       *types.Activity
		scaleIn        *types.Activity
		err            error
		disableScaleIn bool
		wantScaleOut   time.Time
		wantScaleIn    time.Time
	}{
		{
			name:         "uses activity start times",
			scaleOut:     &types.Activity{StartTime: aws.Time(scaleOutAt)},
			scaleIn:      &types.Activity{StartTime: aws.Time(scaleInAt)},
			wantScaleOut: scaleOutAt,
			wantScaleIn:  scaleInAt,
		},
		{
			name: "no activities means never",
		},
		{
			name:        "lookup failure fails closed for scale-in only",
			err:         errors.New("throttled"),
			wantScaleIn: now,
		},
		{
			name:           "lookup failure with scale-in disabled seeds nothing",
			err:            errors.New("throttled"),
			disableScaleIn: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotScaleOut, gotScaleIn := seedLastScaleTimes(tc.scaleOut, tc.scaleIn, tc.err, tc.disableScaleIn, now)
			if !gotScaleOut.Equal(tc.wantScaleOut) {
				t.Errorf("lastScaleOut = %v, want %v", gotScaleOut, tc.wantScaleOut)
			}
			if !gotScaleIn.Equal(tc.wantScaleIn) {
				t.Errorf("lastScaleIn = %v, want %v", gotScaleIn, tc.wantScaleIn)
			}
		})
	}
}
