package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeterministicJitter(t *testing.T) {
	max := 30 * time.Second

	got := deterministicJitter("stack-a-AgentAutoScaleGroup", max)
	if got <= 0 {
		t.Fatalf("deterministicJitter() = %v, want positive duration", got)
	}
	if got >= max {
		t.Fatalf("deterministicJitter() = %v, want less than %v", got, max)
	}

	again := deterministicJitter("stack-a-AgentAutoScaleGroup", max)
	if got != again {
		t.Fatalf("deterministicJitter() = %v then %v, want stable result", got, again)
	}
}

func TestDeterministicJitterDisabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		max  time.Duration
	}{
		{name: "empty key", key: "", max: 30 * time.Second},
		{name: "zero max", key: "stack-a-AgentAutoScaleGroup", max: 0},
		{name: "negative max", key: "stack-a-AgentAutoScaleGroup", max: -1 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deterministicJitter(tc.key, tc.max); got != 0 {
				t.Fatalf("deterministicJitter() = %v, want 0", got)
			}
		})
	}
}

func TestSleepWithContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sleepWithContext(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepWithContext() error = %v, want context.Canceled", err)
	}
}
