package main

import (
	"context"
	"hash/fnv"
	"log"
	"time"
)

func deterministicJitter(key string, max time.Duration) time.Duration {
	if key == "" || max <= 0 {
		return 0
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(key))

	return time.Duration(h.Sum64() % uint64(max))
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func applyStartupJitter(ctx context.Context, key string, max time.Duration) error {
	jitter := deterministicJitter(key, max)
	if jitter <= 0 {
		return nil
	}

	log.Printf("Waiting %v before polling to stagger scheduled scaler invocations", jitter)
	return sleepWithContext(ctx, jitter)
}
