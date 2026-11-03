package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// CampaignRefundReconciliationExecutor performs the financial transaction for
// one leased campaign. The queue owns retries; the executor must be atomic and
// idempotent for a campaign.
type CampaignRefundReconciliationExecutor interface {
	ReconcileUndeliveredCampaignRefund(ctx context.Context, campaignID uint, eligibilityDelay time.Duration) error
}

type CampaignRefundReconciliationScheduler struct {
	executor           CampaignRefundReconciliationExecutor
	repo               repository.CampaignRefundReconciliationRepository
	logger             *log.Logger
	pollInterval       time.Duration
	eligibilityDelay   time.Duration
	jobTimeout         time.Duration
	leaseDuration      time.Duration
	discoveryBatchSize int
	maxParallelRuns    int
	maxAttempts        int
	retryBase          time.Duration
	retryMax           time.Duration

	mu       sync.Mutex
	inFlight map[uint]struct{}
}

func NewCampaignRefundReconciliationScheduler(
	executor CampaignRefundReconciliationExecutor,
	repo repository.CampaignRefundReconciliationRepository,
	logger *log.Logger,
	pollInterval, eligibilityDelay, jobTimeout, leaseDuration time.Duration,
	discoveryBatchSize, maxParallelRuns, maxAttempts int,
	retryBase, retryMax time.Duration,
) *CampaignRefundReconciliationScheduler {
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	if eligibilityDelay <= 0 {
		eligibilityDelay = 72 * time.Hour
	}
	if jobTimeout <= 0 {
		jobTimeout = 30 * time.Second
	}
	if leaseDuration <= jobTimeout {
		leaseDuration = 2 * jobTimeout
	}
	if discoveryBatchSize <= 0 {
		discoveryBatchSize = 250
	}
	if maxParallelRuns <= 0 {
		maxParallelRuns = 1
	}
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	if retryBase <= 0 {
		retryBase = time.Minute
	}
	if retryMax < retryBase {
		retryMax = time.Hour
	}
	if logger == nil {
		logger = log.Default()
	}
	return &CampaignRefundReconciliationScheduler{
		executor: executor, repo: repo, logger: logger, pollInterval: pollInterval,
		eligibilityDelay: eligibilityDelay, jobTimeout: jobTimeout, leaseDuration: leaseDuration, discoveryBatchSize: discoveryBatchSize,
		maxParallelRuns: maxParallelRuns, maxAttempts: maxAttempts, retryBase: retryBase,
		retryMax: retryMax, inFlight: make(map[uint]struct{}),
	}
}

func (s *CampaignRefundReconciliationScheduler) Start(parent context.Context) func() {
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
	return func() { once.Do(func() { cancel(); workers.Wait() }) }
}

func (s *CampaignRefundReconciliationScheduler) runOnce(parent context.Context, workers *sync.WaitGroup) {
	if parent.Err() != nil || s.executor == nil || s.repo == nil {
		return
	}
	now := time.Now().UTC()
	// Discovery advances a bounded historical cursor, while the job index keeps
	// runnable-job claims cheap. It also protects against a missed terminal
	// transition.
	if err := s.repo.DiscoverPending(parent, now, s.discoveryLimit()); err != nil {
		s.logger.Printf("campaign refund scheduler: discovery failed: %v", err)
		return
	}
	slots := s.availableSlots()
	if slots == 0 {
		return
	}
	jobs, err := s.repo.ClaimPending(parent, slots, now, now.Add(-s.eligibilityDelay), now, now.Add(s.leaseDuration))
	if err != nil {
		s.logger.Printf("campaign refund scheduler: claim failed: %v", err)
		return
	}
	for _, job := range jobs {
		if job == nil || job.CampaignID == 0 || job.StartedAt == nil || !s.tryMarkInFlight(job.CampaignID) {
			continue
		}
		workers.Add(1)
		go func(job *models.CampaignRefundReconciliationJob) {
			defer workers.Done()
			defer s.unmarkInFlight(job.CampaignID)
			defer func() {
				if recovered := recover(); recovered != nil {
					s.fail(parent, job, fmt.Sprintf("panic: %v", recovered))
				}
			}()
			jobCtx, cancel := context.WithTimeout(parent, s.jobTimeout)
			err := s.executor.ReconcileUndeliveredCampaignRefund(jobCtx, job.CampaignID, s.eligibilityDelay)
			cancel()
			if err == nil || errors.Is(err, repository.ErrCampaignRefundReconciliationNoop) {
				if completeErr := s.repo.Complete(parent, job.CampaignID, *job.StartedAt, time.Now().UTC()); completeErr != nil {
					s.logger.Printf("campaign refund scheduler: complete campaign %d: %v", job.CampaignID, completeErr)
				}
				return
			}
			if errors.Is(err, repository.ErrCampaignRefundReconciliationManualReview) {
				s.manualReview(parent, job, err.Error())
				return
			}
			s.fail(parent, job, err.Error())
		}(job)
	}
}

func (s *CampaignRefundReconciliationScheduler) fail(ctx context.Context, job *models.CampaignRefundReconciliationJob, message string) {
	at := time.Now().UTC()
	if job.AttemptCount >= s.maxAttempts {
		s.manualReview(ctx, job, message)
		return
	}
	next := at.Add(s.retryDelay(job.AttemptCount))
	if err := s.repo.Retry(ctx, job.CampaignID, *job.StartedAt, "CAMPAIGN_REFUND_RECONCILIATION_FAILED", message, next, at); err != nil {
		s.logger.Printf("campaign refund scheduler: retry campaign %d: %v", job.CampaignID, err)
	}
}

func (s *CampaignRefundReconciliationScheduler) manualReview(ctx context.Context, job *models.CampaignRefundReconciliationJob, message string) {
	if err := s.repo.ManualReview(ctx, job.CampaignID, *job.StartedAt, "CAMPAIGN_REFUND_RECONCILIATION_FAILED", message, time.Now().UTC()); err != nil {
		s.logger.Printf("campaign refund scheduler: manual review campaign %d: %v", job.CampaignID, err)
	}
}

// Discovery only backfills the historical ID cursor. Keep it proportionate to
// the worker pool without turning an initial rollout into an unbounded scan.
func (s *CampaignRefundReconciliationScheduler) discoveryLimit() int {
	return s.discoveryBatchSize
}

func (s *CampaignRefundReconciliationScheduler) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := s.retryBase
	for i := 1; i < attempt && delay < s.retryMax; i++ {
		delay *= 2
		if delay >= s.retryMax {
			return s.retryMax
		}
	}
	return delay
}

func (s *CampaignRefundReconciliationScheduler) availableSlots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return max(0, s.maxParallelRuns-len(s.inFlight))
}
func (s *CampaignRefundReconciliationScheduler) tryMarkInFlight(id uint) bool {
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
func (s *CampaignRefundReconciliationScheduler) unmarkInFlight(id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}
