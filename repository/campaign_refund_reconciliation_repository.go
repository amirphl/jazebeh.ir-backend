package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrCampaignRefundReconciliationNoop identifies a terminal condition where
	// no partial refund is due (for example, a fully delivered or cancelled
	// campaign). The scheduler can safely complete the queue row.
	ErrCampaignRefundReconciliationNoop = errors.New("campaign refund reconciliation has no work")
	// ErrCampaignRefundReconciliationManualReview identifies inconsistent,
	// immutable accounting state which must not be retried automatically.
	ErrCampaignRefundReconciliationManualReview = errors.New("campaign refund reconciliation requires manual review")
)

const campaignRefundReconciliationErrorMessageLimit = 1024

// CampaignRefundReconciliationRepository owns durable discovery and leasing.
// All state transitions are guarded by StartedAt, so a worker whose lease was
// reclaimed cannot complete or fail the newer worker's job.
type CampaignRefundReconciliationRepository interface {
	DiscoverPending(ctx context.Context, at time.Time, batchSize int) error
	ClaimPending(ctx context.Context, limit int, staleBefore, eligibleBefore, at, leaseExpiresAt time.Time) ([]*models.CampaignRefundReconciliationJob, error)
	Complete(ctx context.Context, campaignID uint, startedAt, at time.Time) error
	Retry(ctx context.Context, campaignID uint, startedAt time.Time, code, message string, nextAttemptAt, at time.Time) error
	ManualReview(ctx context.Context, campaignID uint, startedAt time.Time, code, message string, at time.Time) error
}

type CampaignRefundReconciliationRepositoryImpl struct{ db *gorm.DB }

func NewCampaignRefundReconciliationRepository(db *gorm.DB) CampaignRefundReconciliationRepository {
	return &CampaignRefundReconciliationRepositoryImpl{db: db}
}

func (r *CampaignRefundReconciliationRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

// DiscoverPending advances a durable, bounded cursor through historical
// executed campaigns. A database trigger enqueues every future execution in
// the status-change transaction, so this is a one-pass backfill rather than a
// recurring full-table query.
func (r *CampaignRefundReconciliationRepositoryImpl) DiscoverPending(ctx context.Context, at time.Time, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 250
	}
	at = at.UTC()
	return WithTransaction(ctx, r.db, func(txCtx context.Context) error {
		db := r.getDB(txCtx)
		state := models.CampaignRefundReconciliationSchedulerState{}
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, int16(1)).Error; err != nil {
			return err
		}

		var scanEnd uint
		if err := db.Raw(`SELECT COALESCE(MAX(id), 0) FROM (
    SELECT id
    FROM campaigns
    WHERE status = 'executed' AND id > ?
    ORDER BY id ASC
    LIMIT ?
) AS scanned`, state.LastCampaignID, batchSize).Scan(&scanEnd).Error; err != nil {
			return err
		}
		if scanEnd <= state.LastCampaignID {
			return nil
		}

		// yamata_try_timestamptz, installed with this queue, converts malformed
		// legacy JSON safely. Bad executed rows are retained as manual-review
		// jobs instead of one malformed value blocking the entire scan.
		if err := db.Exec(`
INSERT INTO campaign_refund_reconciliation_jobs
    (campaign_id, state, eligible_at, attempt_count, error_code, error_message, created_at, updated_at)
SELECT c.id,
       CASE WHEN scheduled.schedule_at IS NULL THEN 'manual_review' ELSE 'pending' END,
       COALESCE(scheduled.schedule_at, ?),
       0,
       CASE WHEN scheduled.schedule_at IS NULL THEN 'CAMPAIGN_REFUND_SCHEDULE_INVALID' END,
       CASE WHEN scheduled.schedule_at IS NULL THEN 'Executed campaign has no valid schedule_at timestamp' END,
       ?, ?
FROM campaigns c
CROSS JOIN LATERAL (
    SELECT yamata_try_timestamptz(c.spec->>'schedule_at') AS schedule_at
) scheduled
WHERE c.status = 'executed'
	  AND c.id > ? AND c.id <= ?
ON CONFLICT (campaign_id) DO NOTHING`, at, at, at, state.LastCampaignID, scanEnd).Error; err != nil {
			return err
		}
		return db.Model(&models.CampaignRefundReconciliationSchedulerState{}).
			Where("id = ?", state.ID).
			Updates(map[string]any{"last_campaign_id": scanEnd, "updated_at": at}).Error
	})
}

func (r *CampaignRefundReconciliationRepositoryImpl) ClaimPending(ctx context.Context, limit int, staleBefore, eligibleBefore, at, leaseExpiresAt time.Time) ([]*models.CampaignRefundReconciliationJob, error) {
	if limit <= 0 {
		limit = 1
	}
	rows := make([]*models.CampaignRefundReconciliationJob, 0, limit)
	err := r.getDB(ctx).Raw(`
WITH claimable AS (
    SELECT campaign_id
    FROM campaign_refund_reconciliation_jobs
    WHERE (state IN ('pending', 'retry') AND eligible_at <= ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
       OR (state = 'processing' AND lease_expires_at < ?)
    ORDER BY eligible_at ASC, campaign_id ASC
    LIMIT ?
    FOR UPDATE SKIP LOCKED
)
UPDATE campaign_refund_reconciliation_jobs j
SET state = 'processing', attempt_count = j.attempt_count + 1,
    started_at = ?, lease_expires_at = ?, next_attempt_at = NULL,
    error_code = NULL, error_message = NULL, updated_at = ?
FROM claimable
WHERE j.campaign_id = claimable.campaign_id
RETURNING j.*`, eligibleBefore, at, staleBefore, limit, at, leaseExpiresAt, at).Scan(&rows).Error
	return rows, err
}

func (r *CampaignRefundReconciliationRepositoryImpl) Complete(ctx context.Context, campaignID uint, startedAt, at time.Time) error {
	return r.getDB(ctx).Exec(`UPDATE campaign_refund_reconciliation_jobs
SET state='completed', completed_at=?, lease_expires_at=NULL, next_attempt_at=NULL, updated_at=?
WHERE campaign_id=? AND state='processing' AND started_at=?`, at, at, campaignID, startedAt).Error
}

func (r *CampaignRefundReconciliationRepositoryImpl) Retry(ctx context.Context, campaignID uint, startedAt time.Time, code, message string, nextAttemptAt, at time.Time) error {
	return r.getDB(ctx).Exec(`UPDATE campaign_refund_reconciliation_jobs
SET state='retry', lease_expires_at=NULL, next_attempt_at=?, error_code=?, error_message=?, updated_at=?
WHERE campaign_id=? AND state='processing' AND started_at=?`, nextAttemptAt, code, truncateCampaignRefundReconciliationError(message), at, campaignID, startedAt).Error
}

func (r *CampaignRefundReconciliationRepositoryImpl) ManualReview(ctx context.Context, campaignID uint, startedAt time.Time, code, message string, at time.Time) error {
	return r.getDB(ctx).Exec(`UPDATE campaign_refund_reconciliation_jobs
SET state='manual_review', lease_expires_at=NULL, next_attempt_at=NULL, error_code=?, error_message=?, updated_at=?
WHERE campaign_id=? AND state='processing' AND started_at=?`, code, truncateCampaignRefundReconciliationError(message), at, campaignID, startedAt).Error
}

func truncateCampaignRefundReconciliationError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= campaignRefundReconciliationErrorMessageLimit {
		return message
	}
	runes := []rune(message)
	for len(string(runes)) > campaignRefundReconciliationErrorMessageLimit {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
