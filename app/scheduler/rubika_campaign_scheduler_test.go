package scheduler

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
)

func TestDispatchPendingRubikaCampaignsSerializesSameBundle(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	bundleID := uint(42)
	otherBundleID := uint(77)
	started := make(chan uint, 3)
	releaseFirst := make(chan struct{})

	s := &RubikaCampaignScheduler{
		logger: log.New(io.Discard, "", 0),
	}

	pending := []dto.BotGetCampaignResponse{
		{ID: 1, CustomerID: 10, BundleID: &bundleID},
		{ID: 2, CustomerID: 10, BundleID: &bundleID},
		{ID: 3, CustomerID: 10, BundleID: &otherBundleID},
	}

	s.dispatchPendingRubikaCampaigns(
		parent,
		"token-1",
		pending,
		func(ctx context.Context, token string, c dto.BotGetCampaignResponse) error {
			if token != "token-1" {
				t.Errorf("unexpected token: %q", token)
			}
			started <- c.ID
			if c.ID == 1 {
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	)

	seen := map[uint]bool{}
	deadline := time.After(time.Second)
	for !(seen[1] && seen[3]) {
		select {
		case id := <-started:
			if id == 2 {
				t.Fatalf("campaign 2 started before prior campaign in the same bundle completed")
			}
			seen[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for first campaign in each bundle to start; seen=%v", seen)
		}
	}

	close(releaseFirst)

	select {
	case id := <-started:
		if id != 2 {
			t.Fatalf("expected campaign 2 to start after releasing campaign 1, got campaign %d", id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for second campaign in same bundle to start")
	}
}
