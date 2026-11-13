package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

// SmartTargetingExecutionCalculationExecutor owns the expensive, non-
// reserving candidate scan. It is intentionally distinct from campaign
// finalization so an HTTP timeout can never leave money or capacity half done.
type SmartTargetingExecutionCalculationExecutor interface {
	ExecuteSmartTargetingExecutionCalculation(context.Context, int64, time.Time) error
}

type SmartTargetingExecutionCalculationScheduler struct {
	executor        SmartTargetingExecutionCalculationExecutor
	repo            repository.CampaignTargetingExecutionCalculationRepository
	logger          *log.Logger
	pollInterval    time.Duration
	maxParallelRuns int
	mu              sync.Mutex
	inFlight        map[int64]struct{}
}

func NewSmartTargetingExecutionCalculationScheduler(executor SmartTargetingExecutionCalculationExecutor, repo repository.CampaignTargetingExecutionCalculationRepository, logger *log.Logger, poll time.Duration, maxRuns int) *SmartTargetingExecutionCalculationScheduler {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if maxRuns <= 0 {
		maxRuns = 1
	}
	if logger == nil {
		logger = log.Default()
	}
	return &SmartTargetingExecutionCalculationScheduler{executor: executor, repo: repo, logger: logger, pollInterval: poll, maxParallelRuns: maxRuns, inFlight: make(map[int64]struct{})}
}
func (s *SmartTargetingExecutionCalculationScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	var once sync.Once
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		s.runOnce(ctx, &wg)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx, &wg)
			}
		}
	}()
	return func() { once.Do(func() { cancel(); wg.Wait() }) }
}
func (s *SmartTargetingExecutionCalculationScheduler) runOnce(parent context.Context, wg *sync.WaitGroup) {
	if s.executor == nil || s.repo == nil {
		return
	}
	s.mu.Lock()
	slots := s.maxParallelRuns - len(s.inFlight)
	s.mu.Unlock()
	if slots <= 0 {
		return
	}
	now := time.Now().UTC()
	rows, err := s.repo.ClaimPending(parent, slots, now.Add(-smartTargetingCalculationLeaseDuration), now)
	if err != nil {
		s.logger.Printf("smart targeting execution calculation scheduler: claim failed: %v", err)
		return
	}
	for _, row := range rows {
		if row == nil || row.ID == 0 || row.StartedAt == nil || !s.mark(row.ID) {
			continue
		}
		wg.Add(1)
		go func(id int64, lease time.Time) {
			defer wg.Done()
			defer s.unmark(id)
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("smart targeting execution calculation scheduler: calculation %d panicked: %v", id, recovered)
					failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := s.repo.Finish(failCtx, id, lease, models.CampaignTargetingExecutionCalculationFailed, "EXECUTION_AUDIENCE_CALCULATION_PANICKED", "Execution audience calculation worker panicked", utils.UTCNow()); err != nil {
						s.logger.Printf("smart targeting execution calculation scheduler: could not fail panicked calculation %d: %v", id, err)
					}
				}
			}()
			jobCtx, cancel := context.WithTimeout(parent, smartTargetingCalculationJobTimeout)
			defer cancel()
			if err := s.executor.ExecuteSmartTargetingExecutionCalculation(jobCtx, id, lease); err != nil {
				s.logger.Printf("smart targeting execution calculation scheduler: calculation %d failed: %v", id, err)
			}
		}(row.ID, *row.StartedAt)
	}
}
func (s *SmartTargetingExecutionCalculationScheduler) mark(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.inFlight) >= s.maxParallelRuns {
		return false
	}
	if _, ok := s.inFlight[id]; ok {
		return false
	}
	s.inFlight[id] = struct{}{}
	return true
}
func (s *SmartTargetingExecutionCalculationScheduler) unmark(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}
