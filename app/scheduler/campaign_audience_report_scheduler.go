package scheduler

import (
	"context"
	"log"
	"sync"
	"time"
)

type CampaignAudienceReportExecutor interface {
	ExecuteNext(context.Context, time.Duration) error
	Cleanup(context.Context) error
}
type CampaignAudienceReportScheduler struct {
	flow                     CampaignAudienceReportExecutor
	logger                   *log.Logger
	interval, timeout, lease time.Duration
	maxParallelRuns          int
	mu                       sync.Mutex
	inFlight                 int
}

func NewCampaignAudienceReportScheduler(flow CampaignAudienceReportExecutor, logger *log.Logger, interval, timeout, lease time.Duration, maxParallelRuns int) *CampaignAudienceReportScheduler {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if timeout <= 0 {
		timeout = 6 * time.Hour
	}
	if lease <= timeout {
		lease = timeout + 15*time.Minute
	}
	if maxParallelRuns <= 0 {
		maxParallelRuns = 1
	}
	return &CampaignAudienceReportScheduler{flow: flow, logger: logger, interval: interval, timeout: timeout, lease: lease, maxParallelRuns: maxParallelRuns}
}
func (s *CampaignAudienceReportScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	var once sync.Once
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			s.runOnce(ctx, &workers)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { once.Do(func() { cancel(); workers.Wait() }) }
}
func (s *CampaignAudienceReportScheduler) runOnce(ctx context.Context, workers *sync.WaitGroup) {
	if s.flow == nil {
		return
	}
	if err := s.flow.Cleanup(ctx); err != nil {
		s.logger.Printf("campaign audience report cleanup: %v", err)
	}
	s.mu.Lock()
	slots := s.maxParallelRuns - s.inFlight
	s.mu.Unlock()
	for range slots {
		if !s.reserveSlot() {
			return
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer s.releaseSlot()
			work, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()
			if err := s.flow.ExecuteNext(work, s.lease); err != nil {
				s.logger.Printf("campaign audience report job: %v", err)
			}
		}()
	}
}

func (s *CampaignAudienceReportScheduler) reserveSlot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight >= s.maxParallelRuns {
		return false
	}
	s.inFlight++
	return true
}

func (s *CampaignAudienceReportScheduler) releaseSlot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight--
}
