package scheduler

import (
	"context"
	"testing"
)

func TestCampaignExecutionLimiterBoundsAndReleasesSlots(t *testing.T) {
	limiter := NewCampaignExecutionLimiter(1)
	if !limiter.Acquire(context.Background()) {
		t.Fatal("first slot was not acquired")
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if limiter.Acquire(waitCtx) {
		t.Fatal("acquired a slot despite the configured limit")
	}

	limiter.Release()
	if !limiter.Acquire(context.Background()) {
		t.Fatal("released slot was not available")
	}
	limiter.Release()
}

func TestNilCampaignExecutionLimiterDoesNotBlock(t *testing.T) {
	var limiter *CampaignExecutionLimiter
	if !limiter.Acquire(context.Background()) {
		t.Fatal("nil limiter should preserve direct scheduler construction")
	}
	limiter.Release()
}
