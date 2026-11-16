package scheduler

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

type executionCalculationSchedulerRepo struct {
	mu              sync.Mutex
	claimed         []*models.CampaignTargetingExecutionCalculation
	claimCalls      int
	lastLimit       int
	lastStaleBefore time.Time
	finishCalls     int
	finishedID      int64
	finishedStatus  models.CampaignTargetingExecutionCalculationStatus
	finishedCode    string
}

func (r *executionCalculationSchedulerRepo) Save(context.Context, *models.CampaignTargetingExecutionCalculation) error {
	return nil
}
func (r *executionCalculationSchedulerRepo) ByID(context.Context, int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) LatestByCampaignID(context.Context, uint) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) LatestByInput(context.Context, uint, string, int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) ActiveByCampaignID(context.Context, uint) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) ReadyByInput(context.Context, uint, string, int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) Members(context.Context, int64) ([]models.CampaignTargetingExecutionCalculationMember, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) ClaimPending(_ context.Context, limit int, staleBefore, _ time.Time) ([]*models.CampaignTargetingExecutionCalculation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCalls++
	r.lastLimit = limit
	r.lastStaleBefore = staleBefore
	rows := r.claimed
	r.claimed = nil
	return rows, nil
}
func (r *executionCalculationSchedulerRepo) Complete(context.Context, int64, time.Time, string, []models.CampaignTargetingExecutionCalculationMember, time.Time) error {
	return nil
}
func (r *executionCalculationSchedulerRepo) Finish(_ context.Context, id int64, _ time.Time, status models.CampaignTargetingExecutionCalculationStatus, code, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishCalls++
	r.finishedID = id
	r.finishedStatus = status
	r.finishedCode = code
	return nil
}
func (r *executionCalculationSchedulerRepo) Supersede(context.Context, int64, time.Time) error {
	return nil
}
func (r *executionCalculationSchedulerRepo) ReadyForUpdate(context.Context, int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return nil, nil
}
func (r *executionCalculationSchedulerRepo) MarkCommitted(context.Context, int64, uint, time.Time) error {
	return nil
}

type executionCalculationSchedulerExecutor struct {
	mu    sync.Mutex
	ids   []int64
	block <-chan struct{}
	panic bool
}

func (e *executionCalculationSchedulerExecutor) ExecuteSmartTargetingExecutionCalculation(_ context.Context, id int64, _ time.Time) error {
	e.mu.Lock()
	e.ids = append(e.ids, id)
	block, shouldPanic := e.block, e.panic
	e.mu.Unlock()
	if shouldPanic {
		panic("test worker failure")
	}
	if block != nil {
		<-block
	}
	return nil
}

func TestSmartTargetingExecutionCalculationSchedulerClaimsAndExecutes(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &executionCalculationSchedulerRepo{claimed: []*models.CampaignTargetingExecutionCalculation{{ID: 41, StartedAt: &startedAt}}}
	executor := &executionCalculationSchedulerExecutor{}
	s := NewSmartTargetingExecutionCalculationScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 1)

	var workers sync.WaitGroup
	s.runOnce(t.Context(), &workers)
	workers.Wait()

	repo.mu.Lock()
	claimCalls, limit, leaseAge := repo.claimCalls, repo.lastLimit, time.Since(repo.lastStaleBefore)
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

func TestSmartTargetingExecutionCalculationSchedulerExecutesDuplicateClaimOnce(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &executionCalculationSchedulerRepo{claimed: []*models.CampaignTargetingExecutionCalculation{{ID: 73, StartedAt: &startedAt}, {ID: 73, StartedAt: &startedAt}}}
	release := make(chan struct{})
	executor := &executionCalculationSchedulerExecutor{block: release}
	s := NewSmartTargetingExecutionCalculationScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 2)

	var workers sync.WaitGroup
	s.runOnce(t.Context(), &workers)
	close(release)
	workers.Wait()

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.ids) != 1 || executor.ids[0] != 73 {
		t.Fatalf("executed IDs = %v, want one execution of 73", executor.ids)
	}
}

func TestSmartTargetingExecutionCalculationSchedulerFailsPanics(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &executionCalculationSchedulerRepo{claimed: []*models.CampaignTargetingExecutionCalculation{{ID: 99, StartedAt: &startedAt}}}
	s := NewSmartTargetingExecutionCalculationScheduler(&executionCalculationSchedulerExecutor{panic: true}, repo, log.New(io.Discard, "", 0), time.Hour, 1)

	var workers sync.WaitGroup
	s.runOnce(t.Context(), &workers)
	workers.Wait()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.finishCalls != 1 || repo.finishedID != 99 || repo.finishedStatus != models.CampaignTargetingExecutionCalculationFailed || repo.finishedCode != "EXECUTION_AUDIENCE_CALCULATION_PANICKED" {
		t.Fatalf("panic finish = calls:%d id:%d status:%q code:%q", repo.finishCalls, repo.finishedID, repo.finishedStatus, repo.finishedCode)
	}
}
