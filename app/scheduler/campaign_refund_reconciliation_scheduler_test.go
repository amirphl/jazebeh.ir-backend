package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type refundExecutorStub struct {
	err   error
	delay time.Duration
}

func (s *refundExecutorStub) ReconcileUndeliveredCampaignRefund(_ context.Context, _ uint, delay time.Duration) error {
	s.delay = delay
	return s.err
}

type refundRepoStub struct {
	mu         sync.Mutex
	discovered bool
	jobs       []*models.CampaignRefundReconciliationJob
	completed  bool
	retried    bool
	manual     bool
}

func (r *refundRepoStub) DiscoverPending(context.Context, time.Time, int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discovered = true
	return nil
}
func (r *refundRepoStub) ClaimPending(_ context.Context, _ int, _, _, at, _ time.Time) ([]*models.CampaignRefundReconciliationJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		job.StartedAt = &at
	}
	return r.jobs, nil
}
func (r *refundRepoStub) Complete(context.Context, uint, time.Time, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = true
	return nil
}
func (r *refundRepoStub) Retry(context.Context, uint, time.Time, string, string, time.Time, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = true
	return nil
}
func (r *refundRepoStub) ManualReview(context.Context, uint, time.Time, string, string, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manual = true
	return nil
}

func TestCampaignRefundReconciliationSchedulerCompletesClaimedJob(t *testing.T) {
	repo := &refundRepoStub{jobs: []*models.CampaignRefundReconciliationJob{{CampaignID: 7}}}
	executor := &refundExecutorStub{}
	s := NewCampaignRefundReconciliationScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, time.Hour, time.Second, time.Minute, 10, 1, 3, time.Second, time.Minute)
	var workers sync.WaitGroup
	s.runOnce(context.Background(), &workers)
	workers.Wait()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.discovered || !repo.completed || repo.retried || repo.manual {
		t.Fatalf("unexpected queue transitions: %+v", repo)
	}
	if executor.delay != time.Hour {
		t.Fatalf("executor eligibility delay = %s, want %s", executor.delay, time.Hour)
	}
}

func TestCampaignRefundReconciliationSchedulerMovesExhaustedJobToManualReview(t *testing.T) {
	repo := &refundRepoStub{jobs: []*models.CampaignRefundReconciliationJob{{CampaignID: 9, AttemptCount: 3}}}
	s := NewCampaignRefundReconciliationScheduler(&refundExecutorStub{err: errors.New("database unavailable")}, repo, log.New(io.Discard, "", 0), time.Hour, time.Hour, time.Second, time.Minute, 10, 1, 3, time.Second, time.Minute)
	var workers sync.WaitGroup
	s.runOnce(context.Background(), &workers)
	workers.Wait()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.manual || repo.completed || repo.retried {
		t.Fatalf("expected manual review, got %+v", repo)
	}
}

func TestCampaignRefundReconciliationSchedulerCompletesNoopWithoutRetry(t *testing.T) {
	repo := &refundRepoStub{jobs: []*models.CampaignRefundReconciliationJob{{CampaignID: 11}}}
	s := NewCampaignRefundReconciliationScheduler(&refundExecutorStub{err: repository.ErrCampaignRefundReconciliationNoop}, repo, log.New(io.Discard, "", 0), time.Hour, time.Hour, time.Second, time.Minute, 10, 1, 3, time.Second, time.Minute)
	var workers sync.WaitGroup
	s.runOnce(context.Background(), &workers)
	workers.Wait()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.completed || repo.retried || repo.manual {
		t.Fatalf("noop queue transitions: %+v", repo)
	}
}

func TestCampaignRefundReconciliationSchedulerImmediatelyEscalatesManualReview(t *testing.T) {
	repo := &refundRepoStub{jobs: []*models.CampaignRefundReconciliationJob{{CampaignID: 12, AttemptCount: 1}}}
	err := errors.Join(repository.ErrCampaignRefundReconciliationManualReview, errors.New("missing debit"))
	s := NewCampaignRefundReconciliationScheduler(&refundExecutorStub{err: err}, repo, log.New(io.Discard, "", 0), time.Hour, time.Hour, time.Second, time.Minute, 10, 1, 8, time.Second, time.Minute)
	var workers sync.WaitGroup
	s.runOnce(context.Background(), &workers)
	workers.Wait()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.manual || repo.completed || repo.retried {
		t.Fatalf("manual-review queue transitions: %+v", repo)
	}
}
