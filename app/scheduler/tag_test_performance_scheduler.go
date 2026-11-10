package scheduler

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

const (
	tagTestPerformanceLeaseDuration = 30 * time.Minute
	// A report is an analytical refresh, never a reason to monopolize a
	// database connection indefinitely or starve customer-facing finalization.
	// It remains below the stale-lease window so a slow worker cannot be
	// reclaimed while it is still legitimately executing.
	tagTestPerformanceRecomputeTimeout = 2 * time.Minute
	tagTestPerformanceRetryBase        = time.Minute
	tagTestPerformanceRetryMax         = time.Hour
)

// TagTestPerformanceScheduler discovers attributable Smart Targeting Test and
// Execution Campaigns touched by new clicks, send progress, or delivery-status
// jobs. The historical name is retained for configuration compatibility. Each
// recomputation reads the Campaign's complete source history.
type TagTestPerformanceScheduler struct {
	repo         repository.TagTestPerformanceRepository
	logger       *log.Logger
	pollInterval time.Duration
	batchSize    int
}

func NewTagTestPerformanceScheduler(repo repository.TagTestPerformanceRepository, logger *log.Logger, pollInterval time.Duration, batchSize int) *TagTestPerformanceScheduler {
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	if batchSize <= 0 {
		batchSize = 25
	}
	if logger == nil {
		logger = log.Default()
	}
	return &TagTestPerformanceScheduler{
		repo:         repo,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

func (s *TagTestPerformanceScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		s.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *TagTestPerformanceScheduler) runOnce(ctx context.Context) {
	if s.repo == nil || ctx.Err() != nil {
		return
	}
	now := time.Now().UTC()
	if err := s.repo.DiscoverPending(ctx, now); err != nil {
		s.logger.Printf("tag test performance scheduler: discovery failed: %v", err)
		return
	}
	reports, err := s.repo.ClaimPending(ctx, s.batchSize, now.Add(-tagTestPerformanceLeaseDuration), now)
	if err != nil {
		s.logger.Printf("tag test performance scheduler: claim failed: %v", err)
		return
	}
	for _, report := range reports {
		if ctx.Err() != nil {
			return
		}
		if report == nil || report.CampaignID == 0 || report.StartedAt == nil {
			continue
		}
		s.recomputeOne(ctx, report)
	}
}

func (s *TagTestPerformanceScheduler) recomputeOne(ctx context.Context, report *models.CampaignTagTestReport) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Printf("tag test performance scheduler: campaign %d panicked: %v", report.CampaignID, recovered)
			s.failReport(ctx, report, "TAG_TEST_REPORT_PANIC", true)
		}
	}()

	workCtx, cancel := context.WithTimeout(ctx, tagTestPerformanceRecomputeTimeout)
	defer cancel()
	err := s.repo.RecomputeCampaign(workCtx, report.CampaignID, *report.StartedAt, time.Now().UTC())
	if err == nil || errors.Is(err, repository.ErrTagTestPerformanceLeaseLost) {
		return
	}
	// The scheduler's parent context is cancelled during shutdown. Do not try
	// to write a failure through that already-cancelled context: it cannot
	// succeed and would incorrectly turn a shutdown-interrupted job into a
	// calculation failure. Its lease will be recovered on the next run.
	if ctx.Err() != nil {
		s.logger.Printf("tag test performance scheduler: campaign %d interrupted: %v", report.CampaignID, ctx.Err())
		return
	}
	s.logger.Printf("tag test performance scheduler: campaign %d recomputation failed: %v", report.CampaignID, err)
	if errors.Is(err, repository.ErrTagTestPerformanceCampaignInvalid) {
		s.failReport(ctx, report, "TAG_TEST_REPORT_CAMPAIGN_INVALID", false)
		return
	}
	s.failReport(ctx, report, "TAG_TEST_REPORT_CALCULATION_FAILED", true)
}

func (s *TagTestPerformanceScheduler) failReport(ctx context.Context, report *models.CampaignTagTestReport, code string, retry bool) {
	failedAt := time.Now().UTC()
	var retryAt *time.Time
	message := "Tag Test performance calculation failed"
	if retry {
		nextAttempt := failedAt.Add(tagTestPerformanceRetryDelay(report.AttemptCount))
		retryAt = &nextAttempt
		message += "; it will be retried"
	} else {
		message = "Campaign is not an attributable Smart Targeting Campaign"
	}
	err := s.repo.Fail(
		ctx,
		report.CampaignID,
		*report.StartedAt,
		code,
		message,
		retryAt,
		failedAt,
	)
	if err != nil && !errors.Is(err, repository.ErrTagTestPerformanceLeaseLost) {
		s.logger.Printf("tag test performance scheduler: campaign %d failure state could not be persisted: %v", report.CampaignID, err)
	}
}

func tagTestPerformanceRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := tagTestPerformanceRetryBase
	for i := 1; i < attempt && delay < tagTestPerformanceRetryMax; i++ {
		delay *= 2
		if delay >= tagTestPerformanceRetryMax {
			return tagTestPerformanceRetryMax
		}
	}
	return delay
}
