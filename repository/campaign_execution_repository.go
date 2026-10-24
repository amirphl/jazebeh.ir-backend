package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// LockCampaignForUpdate serializes status transitions and approval side
// effects for one campaign. The caller must provide the transaction through
// repository.WithTransaction's context.
func LockCampaignForUpdate(ctx context.Context, campaignID uint) error {
	tx, err := transactionForLock(ctx)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("SELECT id FROM campaigns WHERE id = ? FOR UPDATE", campaignID).Error
}

func transactionForLock(ctx context.Context) (*gorm.DB, error) {
	tx, ok := ctx.Value(TxContextKey).(*gorm.DB)
	if !ok || tx == nil {
		return nil, errors.New("row lock requires an active transaction")
	}
	return tx, nil
}

func LockBundleForUpdate(ctx context.Context, bundleID uint) error {
	tx, err := transactionForLock(ctx)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("SELECT id FROM bundles WHERE id = ? FOR UPDATE", bundleID).Error
}

// LockWalletForUpdate serializes balance-snapshot mutations for a wallet. A
// campaign row lock alone is not sufficient because different campaigns for
// the same customer can otherwise derive new snapshots from the same balance.
func LockWalletForUpdate(ctx context.Context, walletID uint) error {
	tx, err := transactionForLock(ctx)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("SELECT id FROM wallets WHERE id = ? FOR UPDATE", walletID).Error
}

func LockBundleForShare(ctx context.Context, bundleID uint) error {
	tx, err := transactionForLock(ctx)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("SELECT id FROM bundles WHERE id = ? FOR SHARE", bundleID).Error
}

// ReleaseUnpreparedCampaign settles a failed scheduler run immediately. A
// checkpoint with no persisted delivery intent is retired and requeued; once a
// sent-message intent exists, a provider call may already have succeeded, so
// the campaign becomes interrupted instead of being replayed.
func ReleaseUnpreparedCampaign(ctx context.Context, db *gorm.DB, campaignID uint) error {
	if campaignID == 0 {
		return nil
	}
	return recoverUndeliveredCampaigns(ctx, db, `campaigns.id = ?`, []any{campaignID}, &CampaignExecutionRecoveryResult{})
}

// CampaignExecutionRecoveryResult distinguishes the two safe stale-run
// outcomes. Requeued campaigns contain no persisted send intent and can be
// retried. Interrupted campaigns retain one or more send-intent records and
// deliberately require an explicit operator decision rather than risking
// duplicate sends.
type CampaignExecutionRecoveryResult struct {
	Requeued    int64
	Interrupted int64
}

// RecoverStaleCampaignRuns clears every stale running claim. It is intentionally
// conservative: a campaign returns to approved only if no sent-message intent
// exists in any of its attempts. Otherwise it becomes interrupted, a terminal
// non-runnable state that preserves the exact delivery audit trail.
func RecoverStaleCampaignRuns(ctx context.Context, db *gorm.DB, staleBefore time.Time) (CampaignExecutionRecoveryResult, error) {
	result := CampaignExecutionRecoveryResult{}
	err := recoverUndeliveredCampaigns(ctx, db, `campaigns.updated_at < ?`, []any{staleBefore}, &result)
	return result, err
}

// TouchRunningCampaign is a lightweight scheduler lease heartbeat. Recovery
// only acts on a stale heartbeat, not merely on the original running transition.
func TouchRunningCampaign(ctx context.Context, db *gorm.DB, campaignID uint) error {
	if db == nil || campaignID == 0 {
		return nil
	}
	result := db.WithContext(ctx).Model(&models.Campaign{}).
		Where("id = ? AND status = ?", campaignID, models.CampaignStatusRunning).
		Updates(map[string]any{"updated_at": utils.UTCNow()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignExecutionClaimLost
	}
	return nil
}

// ErrCampaignExecutionClaimLost means the scheduler lease was recovered or
// completed before a worker reached a provider dispatch boundary.
var ErrCampaignExecutionClaimLost = errors.New("campaign execution claim is no longer running")

const campaignHasDeliveryRecordSQL = `
EXISTS (
    SELECT 1
    FROM processed_campaigns AS processed
    WHERE processed.campaign_id = campaigns.id
      AND (
          -- Excel-targeting writes local AUDIENCE_UID_NOT_FOUND audit rows
          -- before any provider call.  They deliberately have an empty phone
          -- number and must not turn an otherwise undelivered run into an
          -- interrupted, non-retryable campaign.
          EXISTS (SELECT 1 FROM sent_sms AS sent WHERE sent.processed_campaign_id = processed.id AND NULLIF(BTRIM(sent.phone_number), '') IS NOT NULL)
          OR EXISTS (SELECT 1 FROM sent_bale_messages AS sent WHERE sent.processed_campaign_id = processed.id AND NULLIF(BTRIM(sent.phone_number), '') IS NOT NULL)
          OR EXISTS (SELECT 1 FROM sent_rubika_messages AS sent WHERE sent.processed_campaign_id = processed.id AND NULLIF(BTRIM(sent.phone_number), '') IS NOT NULL)
          OR EXISTS (SELECT 1 FROM sent_splus_messages AS sent WHERE sent.processed_campaign_id = processed.id AND NULLIF(BTRIM(sent.phone_number), '') IS NOT NULL)
      )
)`

// recoverUndeliveredCampaigns executes the status transition and checkpoint
// retirement in one transaction. selector is evaluated against campaigns and
// must be accompanied by selectorArgs. When staleResult is non-nil, the helper
// also terminalizes runs with delivery intent. A sent row is written immediately
// before external dispatch, so it is the durable no-replay boundary even if the
// worker dies before persisting a provider response.
func recoverUndeliveredCampaigns(ctx context.Context, db *gorm.DB, selector string, selectorArgs []any, staleResult *CampaignExecutionRecoveryResult) error {
	if db == nil {
		return errors.New("campaign execution recovery database is nil")
	}
	return WithTransaction(ctx, db, func(txCtx context.Context) error {
		tx, err := transactionForLock(txCtx)
		if err != nil {
			return err
		}
		now := utils.UTCNow()
		where := "status = ? AND " + selector
		args := append([]any{models.CampaignStatusRunning}, selectorArgs...)

		// Retire only the current checkpoint for an entirely undelivered
		// campaign. Historical checkpoints remain immutable audit records.
		retire := tx.WithContext(txCtx).Exec(`UPDATE processed_campaigns AS processed
			SET is_current = FALSE
			FROM campaigns
			WHERE processed.campaign_id = campaigns.id
			  AND processed.is_current
			  AND `+where+`
			  AND NOT (`+campaignHasDeliveryRecordSQL+`)`, args...)
		if retire.Error != nil {
			return retire.Error
		}

		requeue := tx.WithContext(txCtx).Model(&models.Campaign{}).
			Where(where, args...).
			Where("NOT (" + campaignHasDeliveryRecordSQL + ")").
			Where(`NOT EXISTS (
				SELECT 1 FROM processed_campaigns
				WHERE processed_campaigns.campaign_id = campaigns.id
				  AND processed_campaigns.is_current
			)`).
			Updates(map[string]any{"status": models.CampaignStatusApproved, "updated_at": now})
		if requeue.Error != nil {
			return requeue.Error
		}
		if staleResult != nil {
			staleResult.Requeued += requeue.RowsAffected
		}

		if staleResult == nil {
			return nil
		}
		interrupted := tx.WithContext(txCtx).Model(&models.Campaign{}).
			Where(where, args...).
			Where(campaignHasDeliveryRecordSQL).
			Updates(map[string]any{"status": models.CampaignStatusInterrupted, "updated_at": now})
		if interrupted.Error != nil {
			return interrupted.Error
		}
		staleResult.Interrupted += interrupted.RowsAffected
		return nil
	})
}

type BundleCampaignAllocation struct {
	CampaignID   uint                  `gorm:"column:campaign_id"`
	NumAudience  *uint64               `gorm:"column:num_audience"`
	Status       models.CampaignStatus `gorm:"column:status"`
	Materialized bool                  `gorm:"column:materialized"`
}

// BundleActiveTestReservation is one immutable concrete reservation that
// currently removes audiences from a Bundle's candidate population.  The name
// is retained for Test compatibility; execution reservations use the same
// shape with their first reservation-row ID as SelectionID.
type BundleActiveTestReservation struct {
	CampaignID    uint  `gorm:"column:campaign_id"`
	SelectionID   int64 `gorm:"column:selection_id"`
	AudienceCount int64 `gorm:"column:audience_count"`
}

// ListBundleCampaignAllocations returns the stable, ordered standard/smart
// reservations used by exact-capacity deductions and fingerprint validation.
// Excel targeting is intentionally excluded: its recipients are explicitly
// reusable and never participate in the bundle audience-selection ledger.
func ListBundleCampaignAllocations(ctx context.Context, db *gorm.DB, bundleID, excludedCampaignID uint) ([]BundleCampaignAllocation, error) {
	queryDB := db.WithContext(ctx)
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		queryDB = tx.WithContext(ctx)
	}
	var rows []BundleCampaignAllocation
	err := bundleCampaignAllocationsQuery(queryDB, bundleID, excludedCampaignID).
		Find(&rows).Error
	return rows, err
}

// ListBundleActiveTestReservations returns active Test reservations owned by
// other campaigns in the Bundle. They are already excluded by audience
// candidate queries, so callers use this only to make old capacity snapshots
// stale when the candidate population changes.
func ListBundleActiveTestReservations(ctx context.Context, db *gorm.DB, bundleID, excludedCampaignID uint) ([]BundleActiveTestReservation, error) {
	queryDB := db.WithContext(ctx)
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		queryDB = tx.WithContext(ctx)
	}
	var rows []BundleActiveTestReservation
	err := bundleActiveTestReservationsQuery(queryDB, bundleID, excludedCampaignID).
		Find(&rows).Error
	return rows, err
}

// ListBundleActiveExecutionReservations returns execution-phase reservations
// in the same compact fingerprint form as Test snapshots.
func ListBundleActiveExecutionReservations(ctx context.Context, db *gorm.DB, bundleID, excludedCampaignID uint) ([]BundleActiveTestReservation, error) {
	queryDB := db.WithContext(ctx)
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		queryDB = tx.WithContext(ctx)
	}
	var rows []BundleActiveTestReservation
	err := queryDB.Table("campaign_targeting_execution_reservations").
		Select("campaign_id, MIN(id) AS selection_id, COUNT(*) AS audience_count").
		Where("bundle_id = ? AND campaign_id <> ? AND state = 'active'", bundleID, excludedCampaignID).
		Group("campaign_id").Order("campaign_id ASC").Find(&rows).Error
	return rows, err
}

func bundleActiveTestReservationsQuery(db *gorm.DB, bundleID, excludedCampaignID uint) *gorm.DB {
	return db.Table("campaign_targeting_test_sample_reservations").
		Select("campaign_id, selection_id, COUNT(*) AS audience_count").
		Where("bundle_id = ? AND campaign_id <> ? AND state = 'active'", bundleID, excludedCampaignID).
		Group("campaign_id, selection_id").
		Order("campaign_id ASC, selection_id ASC")
}

func bundleCampaignAllocationsQuery(db *gorm.DB, bundleID, excludedCampaignID uint) *gorm.DB {
	return db.Table("campaigns").
		Select(`id AS campaign_id, num_audience, status,
			EXISTS (
				SELECT 1
				FROM bundle_audience_selections AS selection
				WHERE selection.campaign_id = campaigns.id
			) AS materialized`).
		Where("bundle_id = ? AND id <> ? AND status IN ?", bundleID, excludedCampaignID, []models.CampaignStatus{
			models.CampaignStatusApproved, models.CampaignStatusRunning, models.CampaignStatusInterrupted, models.CampaignStatusExecuted,
		}).
		Where(`CASE
			WHEN LOWER(BTRIM(COALESCE(spec->>'audience_targeting_method', ''))) IN (?, ?, ?)
				THEN LOWER(BTRIM(spec->>'audience_targeting_method'))
			WHEN NULLIF(BTRIM(spec->>'target_audience_excel_file_uuid'), '') IS NOT NULL
				THEN ?
			ELSE ?
		END <> ?`,
			models.CampaignAudienceTargetingStandard,
			models.CampaignAudienceTargetingSmart,
			models.CampaignAudienceTargetingExcel,
			models.CampaignAudienceTargetingExcel,
			models.CampaignAudienceTargetingStandard,
			models.CampaignAudienceTargetingExcel,
		).
		Order("id ASC")
}
