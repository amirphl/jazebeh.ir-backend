package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

type testSamplingSchedulerRepo struct {
	mu              sync.Mutex
	claimCalls      int
	claimed         []*models.CampaignTargetingTestSamplingCalculation
	lastLimit       int
	lastStaleBefore time.Time
}

func (r *testSamplingSchedulerRepo) Save(context.Context, *models.CampaignTargetingTestSamplingCalculation) error {
	return nil
}
func (r *testSamplingSchedulerRepo) ByID(context.Context, int64) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return nil, nil
}
func (r *testSamplingSchedulerRepo) LatestByCampaignID(context.Context, uint) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return nil, nil
}
func (r *testSamplingSchedulerRepo) ActiveByCampaignID(context.Context, uint) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return nil, nil
}
func (r *testSamplingSchedulerRepo) LatestByInput(context.Context, uint, string) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return nil, nil
}

func (r *testSamplingSchedulerRepo) LatestCalculatedByInput(
	context.Context,
	uint,
	string,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return nil, nil
}
func (r *testSamplingSchedulerRepo) Supersede(context.Context, int64, string, string, time.Time) error {
	return nil
}

func (r *testSamplingSchedulerRepo) ClaimPending(
	_ context.Context,
	limit int,
	staleBefore, _ time.Time,
) ([]*models.CampaignTargetingTestSamplingCalculation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCalls++
	r.lastLimit = limit
	r.lastStaleBefore = staleBefore
	rows := r.claimed
	r.claimed = nil
	return rows, nil
}

func (r *testSamplingSchedulerRepo) Complete(
	context.Context,
	int64,
	time.Time,
	json.RawMessage,
	int,
	int64,
	uint64,
	string,
	time.Time,
) error {
	return nil
}
func (r *testSamplingSchedulerRepo) Fail(context.Context, int64, time.Time, string, string, time.Time) error {
	return nil
}

type testSamplingSchedulerExecutor struct {
	mu    sync.Mutex
	ids   []int64
	block <-chan struct{}
}

func (e *testSamplingSchedulerExecutor) ExecuteSmartTargetingTestSamplingCalculation(_ context.Context, id int64, _ time.Time) error {
	e.mu.Lock()
	e.ids = append(e.ids, id)
	block := e.block
	e.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}

func TestSmartTargetingTestSamplingSchedulerUsesDurableClaimsAndCleanup(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &testSamplingSchedulerRepo{
		claimed: []*models.CampaignTargetingTestSamplingCalculation{{ID: 41, StartedAt: &startedAt}},
	}
	executor := &testSamplingSchedulerExecutor{}
	scheduler := NewSmartTargetingTestSamplingScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 1)

	var workers sync.WaitGroup
	scheduler.runOnce(t.Context(), &workers)
	workers.Wait()

	repo.mu.Lock()
	claimCalls, limit := repo.claimCalls, repo.lastLimit
	leaseAge := time.Since(repo.lastStaleBefore)
	repo.mu.Unlock()
	if claimCalls != 1 || limit != 1 {
		t.Fatalf("claim calls = %d with limit %d, want 1 with limit 1", claimCalls, limit)
	}
	if leaseAge < smartTargetingCalculationLeaseDuration || leaseAge > smartTargetingCalculationLeaseDuration+time.Minute {
		t.Fatalf("stale lease age = %s, want approximately %s", leaseAge, smartTargetingCalculationLeaseDuration)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.ids) != 1 || executor.ids[0] != 41 {
		t.Fatalf("executed IDs = %v, want [41]", executor.ids)
	}
}

func TestSmartTargetingTestSamplingSchedulerExecutesDuplicateClaimOnce(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &testSamplingSchedulerRepo{claimed: []*models.CampaignTargetingTestSamplingCalculation{
		{ID: 73, StartedAt: &startedAt}, {ID: 73, StartedAt: &startedAt},
	}}
	release := make(chan struct{})
	executor := &testSamplingSchedulerExecutor{block: release}
	scheduler := NewSmartTargetingTestSamplingScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 2)

	var workers sync.WaitGroup
	scheduler.runOnce(t.Context(), &workers)
	close(release)
	workers.Wait()

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.ids) != 1 || executor.ids[0] != 73 {
		t.Fatalf("executed IDs = %v, want one execution of 73", executor.ids)
	}
}
