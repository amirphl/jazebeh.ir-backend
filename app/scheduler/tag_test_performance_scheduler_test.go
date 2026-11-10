package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type tagTestPerformanceSchedulerRepo struct {
	discoverErr  error
	claimErr     error
	recomputeErr error
	reports      []*models.CampaignTagTestReport
	discovered   int
	claimed      int
	recomputed   []uint
	hasDeadline  bool
	failed       []uint
	failureCode  string
	failureText  string
	retryAt      *time.Time
}

func (r *tagTestPerformanceSchedulerRepo) DiscoverPending(context.Context, time.Time) error {
	r.discovered++
	return r.discoverErr
}

func (r *tagTestPerformanceSchedulerRepo) ClaimPending(context.Context, int, time.Time, time.Time) ([]*models.CampaignTagTestReport, error) {
	r.claimed++
	return r.reports, r.claimErr
}

func (r *tagTestPerformanceSchedulerRepo) RecomputeCampaign(ctx context.Context, campaignID uint, _, _ time.Time) error {
	r.recomputed = append(r.recomputed, campaignID)
	_, r.hasDeadline = ctx.Deadline()
	return r.recomputeErr
}

func (r *tagTestPerformanceSchedulerRepo) Fail(_ context.Context, campaignID uint, _ time.Time, code, message string, retryAt *time.Time, _ time.Time) error {
	r.failed = append(r.failed, campaignID)
	r.failureCode = code
	r.failureText = message
	r.retryAt = retryAt
	return nil
}

func TestTagTestPerformanceSchedulerDiscoversAndRecomputesClaimedCampaigns(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &tagTestPerformanceSchedulerRepo{reports: []*models.CampaignTagTestReport{
		{CampaignID: 7, StartedAt: &startedAt},
		{CampaignID: 9, StartedAt: &startedAt},
	}}
	scheduler := NewTagTestPerformanceScheduler(repo, log.New(io.Discard, "", 0), time.Hour, 10)

	scheduler.runOnce(t.Context())

	if repo.discovered != 1 || repo.claimed != 1 {
		t.Fatalf("discovery/claim calls = %d/%d, want 1/1", repo.discovered, repo.claimed)
	}
	if len(repo.recomputed) != 2 || repo.recomputed[0] != 7 || repo.recomputed[1] != 9 {
		t.Fatalf("recomputed campaigns = %v, want [7 9]", repo.recomputed)
	}
	if len(repo.failed) != 0 {
		t.Fatalf("failed campaigns = %v, want none", repo.failed)
	}
	if !repo.hasDeadline {
		t.Fatal("report recomputation did not receive a deadline")
	}
}

func TestTagTestPerformanceSchedulerPersistsSanitizedFailure(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &tagTestPerformanceSchedulerRepo{
		recomputeErr: errors.New("internal sql text must stay in logs"),
		reports:      []*models.CampaignTagTestReport{{CampaignID: 12, StartedAt: &startedAt, AttemptCount: 2}},
	}
	scheduler := NewTagTestPerformanceScheduler(repo, log.New(io.Discard, "", 0), time.Hour, 10)

	scheduler.runOnce(t.Context())

	if len(repo.failed) != 1 || repo.failed[0] != 12 {
		t.Fatalf("failed campaigns = %v, want [12]", repo.failed)
	}
	if repo.failureCode != "TAG_TEST_REPORT_CALCULATION_FAILED" {
		t.Fatalf("failure code = %q", repo.failureCode)
	}
	if repo.failureText == "" || repo.failureText == repo.recomputeErr.Error() {
		t.Fatalf("failure text was not sanitized: %q", repo.failureText)
	}
	if repo.retryAt == nil {
		t.Fatal("transient calculation failure did not schedule a retry")
	}
}

func TestTagTestPerformanceSchedulerDoesNotRetryInvalidCampaign(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &tagTestPerformanceSchedulerRepo{
		recomputeErr: repository.ErrTagTestPerformanceCampaignInvalid,
		reports:      []*models.CampaignTagTestReport{{CampaignID: 13, StartedAt: &startedAt}},
	}
	scheduler := NewTagTestPerformanceScheduler(repo, log.New(io.Discard, "", 0), time.Hour, 10)

	scheduler.runOnce(t.Context())

	if len(repo.failed) != 1 || repo.failureCode != "TAG_TEST_REPORT_CAMPAIGN_INVALID" {
		t.Fatalf("failure = campaigns %v, code %q", repo.failed, repo.failureCode)
	}
	if repo.retryAt != nil {
		t.Fatalf("invalid Campaign retry = %s, want none", *repo.retryAt)
	}
	if strings.Contains(repo.failureText, "retried") {
		t.Fatalf("permanent failure text promises retry: %q", repo.failureText)
	}
}

func TestTagTestPerformanceSchedulerDoesNotPersistFailureAfterShutdownCancellation(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &tagTestPerformanceSchedulerRepo{recomputeErr: context.Canceled}
	scheduler := NewTagTestPerformanceScheduler(repo, log.New(io.Discard, "", 0), time.Hour, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	scheduler.recomputeOne(ctx, &models.CampaignTagTestReport{CampaignID: 14, StartedAt: &startedAt})

	if len(repo.recomputed) != 1 || repo.recomputed[0] != 14 {
		t.Fatalf("recomputed campaigns = %v, want [14]", repo.recomputed)
	}
	if len(repo.failed) != 0 {
		t.Fatalf("shutdown-cancelled job was persisted as failed: %v", repo.failed)
	}
}

func TestTagTestPerformanceRetryDelayCaps(t *testing.T) {
	if got := tagTestPerformanceRetryDelay(1); got != time.Minute {
		t.Fatalf("retry delay(1) = %s, want 1m", got)
	}
	if got := tagTestPerformanceRetryDelay(100); got != time.Hour {
		t.Fatalf("retry delay(100) = %s, want 1h", got)
	}
}
