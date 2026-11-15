package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type BundleActionFileExecutor interface {
	ExecuteBundleActionFile(context.Context, int64, time.Time) error
}

type BundleActionFileScheduler struct {
	flow     BundleActionFileExecutor
	repo     repository.BundleActionRepository
	logger   *log.Logger
	interval time.Duration
	maxRuns  int
}

func NewBundleActionFileScheduler(flow BundleActionFileExecutor, repo repository.BundleActionRepository, logger *log.Logger, interval time.Duration, maxRuns int) *BundleActionFileScheduler {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if maxRuns < 1 {
		maxRuns = 1
	}
	if logger == nil {
		logger = log.Default()
	}
	return &BundleActionFileScheduler{flow: flow, repo: repo, logger: logger, interval: interval, maxRuns: maxRuns}
}

func (s *BundleActionFileScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			s.runOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func (s *BundleActionFileScheduler) runOnce(ctx context.Context) {
	if s.flow == nil || s.repo == nil {
		return
	}
	rows, err := s.repo.ClaimPending(ctx, s.maxRuns, time.Now().UTC().Add(-30*time.Minute), time.Now().UTC())
	if err != nil {
		s.logger.Printf("bundle action file scheduler: claim failed: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, row := range rows {
		if row == nil || row.StartedAt == nil {
			continue
		}
		wg.Add(1)
		go func(id int64, lease time.Time) {
			defer wg.Done()
			work, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			if err := s.flow.ExecuteBundleActionFile(work, id, lease); err != nil {
				s.logger.Printf("bundle action file scheduler: file %d failed: %v", id, err)
			}
		}(row.ID, *row.StartedAt)
	}
	wg.Wait()
}
