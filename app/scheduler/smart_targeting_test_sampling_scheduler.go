package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type SmartTargetingTestSamplingExecutor interface {
	ExecuteSmartTargetingTestSamplingCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) error
}

// SmartTargetingTestSamplingScheduler leases durable sampling jobs and runs
// them outside the API request lifecycle.
type SmartTargetingTestSamplingScheduler struct {
	executor        SmartTargetingTestSamplingExecutor
	repo            repository.CampaignTargetingTestSamplingRepository
	logger          *log.Logger
	pollInterval    time.Duration
	maxParallelRuns int

	mu       sync.Mutex
	inFlight map[int64]struct{}
}

func NewSmartTargetingTestSamplingScheduler(
	executor SmartTargetingTestSamplingExecutor,
	repo repository.CampaignTargetingTestSamplingRepository,
	logger *log.Logger,
	pollInterval time.Duration,
	maxParallelRuns int,
) *SmartTargetingTestSamplingScheduler {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if maxParallelRuns <= 0 {
		maxParallelRuns = 1
	}
	if logger == nil {
		logger = log.Default()
	}
	return &SmartTargetingTestSamplingScheduler{
		executor: executor, repo: repo, logger: logger, pollInterval: pollInterval,
		maxParallelRuns: maxParallelRuns, inFlight: make(map[int64]struct{}),
	}
}

func (s *SmartTargetingTestSamplingScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	var once sync.Once
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		s.runOnce(ctx, &workers)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx, &workers)
			}
		}
	}()
	return func() {
		once.Do(func() {
			cancel()
			workers.Wait()
		})
	}
}

func (s *SmartTargetingTestSamplingScheduler) runOnce(parent context.Context, workers *sync.WaitGroup) {
	if s.executor == nil || s.repo == nil {
		return
	}
	now := time.Now().UTC()
	slots := s.availableSlots()
	if slots == 0 {
		return
	}
	rows, err := s.repo.ClaimPending(parent, slots, now.Add(-smartTargetingCalculationLeaseDuration), now)
	if err != nil {
		s.logger.Printf("smart targeting test sampling scheduler: claim pending calculations failed: %v", err)
		return
	}
	for _, row := range rows {
		if row == nil || row.ID == 0 || row.StartedAt == nil || !s.tryMarkInFlight(row.ID) {
			continue
		}
		workers.Add(1)
		go func(id int64, leaseStartedAt time.Time) {
			defer workers.Done()
			defer s.unmarkInFlight(id)
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("smart targeting test sampling scheduler: calculation %d panicked: %v", id, recovered)
				}
			}()
			jobCtx, cancel := context.WithTimeout(parent, smartTargetingCalculationJobTimeout)
			defer cancel()
			if err := s.executor.ExecuteSmartTargetingTestSamplingCalculation(jobCtx, id, leaseStartedAt); err != nil {
				s.logger.Printf("smart targeting test sampling scheduler: calculation %d failed: %v", id, err)
			}
		}(row.ID, *row.StartedAt)
	}
}

func (s *SmartTargetingTestSamplingScheduler) availableSlots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := s.maxParallelRuns - len(s.inFlight)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *SmartTargetingTestSamplingScheduler) tryMarkInFlight(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.inFlight) >= s.maxParallelRuns {
		return false
	}
	if _, exists := s.inFlight[id]; exists {
		return false
	}
	s.inFlight[id] = struct{}{}
	return true
}

func (s *SmartTargetingTestSamplingScheduler) unmarkInFlight(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}
