package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

var ErrCampaignTargetingTestSamplingStateConflict = errors.New("campaign targeting test sampling calculation state changed")

// CampaignTargetingTestSamplingRepository owns the durable sampling queue.
// The expensive audience scan remains in the business flow.
type CampaignTargetingTestSamplingRepository interface {
	Save(ctx context.Context, row *models.CampaignTargetingTestSamplingCalculation) error
	ByID(ctx context.Context, id int64) (*models.CampaignTargetingTestSamplingCalculation, error)
	LatestByCampaignID(ctx context.Context, campaignID uint) (*models.CampaignTargetingTestSamplingCalculation, error)
	ActiveByCampaignID(ctx context.Context, campaignID uint) (*models.CampaignTargetingTestSamplingCalculation, error)
	LatestByInput(ctx context.Context, campaignID uint, inputHash string) (*models.CampaignTargetingTestSamplingCalculation, error)
	LatestCalculatedByInput(
		ctx context.Context,
		campaignID uint,
		inputHash string,
	) (*models.CampaignTargetingTestSamplingCalculation, error)
	Supersede(ctx context.Context, id int64, code, message string, at time.Time) error
	ClaimPending(ctx context.Context, limit int, staleBefore, at time.Time) ([]*models.CampaignTargetingTestSamplingCalculation, error)
	Complete(
		ctx context.Context,
		id int64,
		leaseStartedAt time.Time,
		tagResults json.RawMessage,
		satisfiedTagCount int,
		effectiveAudienceCount int64,
		campaignCost uint64,
		allocationFingerprint string,
		at time.Time,
	) error
	Fail(ctx context.Context, id int64, leaseStartedAt time.Time, code, message string, at time.Time) error
}

type CampaignTargetingTestSamplingRepositoryImpl struct{ db *gorm.DB }

func NewCampaignTargetingTestSamplingRepository(db *gorm.DB) CampaignTargetingTestSamplingRepository {
	return &CampaignTargetingTestSamplingRepositoryImpl{db: db}
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) Save(
	ctx context.Context,
	row *models.CampaignTargetingTestSamplingCalculation,
) error {
	return r.getDB(ctx).Create(row).Error
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) ByID(
	ctx context.Context,
	id int64,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	var row models.CampaignTargetingTestSamplingCalculation
	if err := r.getDB(ctx).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) LatestByCampaignID(
	ctx context.Context,
	campaignID uint,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return r.latest(ctx, "campaign_id = ?", campaignID)
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) ActiveByCampaignID(
	ctx context.Context,
	campaignID uint,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return r.latest(ctx, "campaign_id = ? AND status = ?", campaignID, models.CampaignTargetingTestSamplingCalculating)
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) LatestByInput(
	ctx context.Context,
	campaignID uint,
	inputHash string,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return r.latest(ctx, "campaign_id = ? AND input_hash = ?", campaignID, inputHash)
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) LatestCalculatedByInput(
	ctx context.Context,
	campaignID uint,
	inputHash string,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	return r.latest(
		ctx,
		"campaign_id = ? AND input_hash = ? AND status = ?",
		campaignID,
		inputHash,
		models.CampaignTargetingTestSamplingCalculated,
	)
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) latest(
	ctx context.Context,
	query string,
	args ...any,
) (*models.CampaignTargetingTestSamplingCalculation, error) {
	var row models.CampaignTargetingTestSamplingCalculation
	err := r.getDB(ctx).Where(query, args...).Order("created_at DESC, id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) Supersede(ctx context.Context, id int64, code, message string, at time.Time) error {
	result := r.getDB(ctx).Model(&models.CampaignTargetingTestSamplingCalculation{}).
		Where("id = ? AND status = ?", id, models.CampaignTargetingTestSamplingCalculating).
		Updates(map[string]any{"status": models.CampaignTargetingTestSamplingFailed, "finished_at": at, "error_code": code, "error_message": message})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingTestSamplingStateConflict
	}
	return nil
}

// ClaimPending atomically leases rows across application replicas. A lease can
// be reclaimed after staleBefore if a process exited before completion.
func (r *CampaignTargetingTestSamplingRepositoryImpl) ClaimPending(
	ctx context.Context,
	limit int,
	staleBefore, at time.Time,
) ([]*models.CampaignTargetingTestSamplingCalculation, error) {
	if limit <= 0 {
		limit = 1
	}
	var rows []*models.CampaignTargetingTestSamplingCalculation
	query := `
WITH claimable AS (
    SELECT id
    FROM campaign_targeting_test_sampling_calculations
    WHERE status = ?
      AND (started_at IS NULL OR started_at < ?)
    ORDER BY created_at ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT ?
)
UPDATE campaign_targeting_test_sampling_calculations AS calculation
SET started_at = ?
FROM claimable
WHERE calculation.id = claimable.id
RETURNING calculation.*`
	return rows, r.getDB(ctx).Raw(query, models.CampaignTargetingTestSamplingCalculating, staleBefore, limit, at).Scan(&rows).Error
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) Complete(
	ctx context.Context,
	id int64,
	leaseStartedAt time.Time,
	tagResults json.RawMessage,
	satisfiedTagCount int,
	effectiveAudienceCount int64,
	campaignCost uint64,
	allocationFingerprint string,
	at time.Time,
) error {
	if len(allocationFingerprint) != 64 {
		return errors.New("campaign targeting test sampling allocation fingerprint must be a 64-character SHA-256 hex digest")
	}
	decodedFingerprint, err := hex.DecodeString(allocationFingerprint)
	if err != nil || hex.EncodeToString(decodedFingerprint) != allocationFingerprint {
		return errors.New("campaign targeting test sampling allocation fingerprint must be a 64-character SHA-256 hex digest")
	}
	result := r.completeQuery(
		ctx,
		id,
		leaseStartedAt,
		tagResults,
		satisfiedTagCount,
		effectiveAudienceCount,
		campaignCost,
		allocationFingerprint,
		at,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingTestSamplingStateConflict
	}
	return nil
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) completeQuery(
	ctx context.Context,
	id int64,
	leaseStartedAt time.Time,
	tagResults json.RawMessage,
	satisfiedTagCount int,
	effectiveAudienceCount int64,
	campaignCost uint64,
	allocationFingerprint string,
	at time.Time,
) *gorm.DB {
	return r.getDB(ctx).Model(&models.CampaignTargetingTestSamplingCalculation{}).
		Where("id = ? AND status = ? AND started_at = ?", id, models.CampaignTargetingTestSamplingCalculating, leaseStartedAt).
		Updates(map[string]any{
			"tag_results": gorm.Expr("?::jsonb", string(tagResults)), "satisfied_tag_count": satisfiedTagCount,
			// database/sql rejects uint64 parameters above MaxInt64. NUMERIC(20,0)
			// supports the complete uint64 range, so send its exact decimal form.
			"effective_audience_count": effectiveAudienceCount, "campaign_cost": gorm.Expr("?::numeric", strconv.FormatUint(campaignCost, 10)),
			"allocation_fingerprint": allocationFingerprint,
			"status":                 models.CampaignTargetingTestSamplingCalculated, "finished_at": at,
			"error_code": nil, "error_message": nil,
		})
}

func (r *CampaignTargetingTestSamplingRepositoryImpl) Fail(
	ctx context.Context,
	id int64,
	leaseStartedAt time.Time,
	code, message string,
	at time.Time,
) error {
	result := r.getDB(ctx).Model(&models.CampaignTargetingTestSamplingCalculation{}).
		Where("id = ? AND status = ? AND started_at = ?", id, models.CampaignTargetingTestSamplingCalculating, leaseStartedAt).
		Updates(map[string]any{"status": models.CampaignTargetingTestSamplingFailed, "finished_at": at, "error_code": code, "error_message": message})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingTestSamplingStateConflict
	}
	return nil
}
