package scheduler

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type bundleActionSchedulerRepoStub struct {
	repository.BundleActionRepository
	rows  []*models.BundleActionFile
	stale time.Time
}

func (r *bundleActionSchedulerRepoStub) ClaimPending(_ context.Context, _ int, stale, _ time.Time) ([]*models.BundleActionFile, error) {
	r.stale = stale
	return r.rows, nil
}

type bundleActionSchedulerFlowStub struct{ deadline time.Time }

func (f *bundleActionSchedulerFlowStub) ExecuteBundleActionFile(ctx context.Context, _ int64, _ time.Time) error {
	f.deadline, _ = ctx.Deadline()
	return nil
}

func TestBundleActionFileSchedulerUsesDedicatedTimeoutAndLongerLease(t *testing.T) {
	started := time.Now().UTC()
	repo := &bundleActionSchedulerRepoStub{rows: []*models.BundleActionFile{{ID: 9, StartedAt: &started}}}
	flow := &bundleActionSchedulerFlowStub{}
	s := NewBundleActionFileScheduler(flow, repo, log.New(io.Discard, "", 0), time.Hour, 1, 30*time.Minute, 35*time.Minute)

	before := time.Now().UTC()
	s.runOnce(t.Context())
	after := time.Now().UTC()

	if flow.deadline.Before(before.Add(29*time.Minute+59*time.Second)) || flow.deadline.After(after.Add(30*time.Minute+time.Second)) {
		t.Fatalf("job deadline = %s; want approximately 30 minutes from now", flow.deadline)
	}
	lease := before.Sub(repo.stale)
	if lease < 34*time.Minute+59*time.Second || lease > 35*time.Minute+time.Second {
		t.Fatalf("claim lease = %s; want 35 minutes", lease)
	}
}

func TestBundleActionFileSchedulerExtendsInvalidLeaseBeyondDeadline(t *testing.T) {
	s := NewBundleActionFileScheduler(nil, nil, nil, time.Hour, 1, 30*time.Minute, 30*time.Minute)
	if s.leaseDuration <= s.jobTimeout {
		t.Fatalf("lease duration %s must exceed job timeout %s", s.leaseDuration, s.jobTimeout)
	}
}
