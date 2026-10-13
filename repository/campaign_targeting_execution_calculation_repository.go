package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCampaignTargetingExecutionCalculationStateConflict = errors.New("execution audience calculation state changed")

// executionCalculationCommitConflictQuery checks immutable proposal members
// against claims that already exist in the same Bundle. Callers hold the
// Bundle UPDATE lock, so this is short and is the authoritative conflict check
// after an intentionally advisory background calculation.
const executionCalculationCommitConflictQuery = `
SELECT COUNT(*)
FROM campaign_targeting_execution_calculation_members AS member
JOIN campaign_targeting_execution_calculations AS candidate
  ON candidate.id = member.calculation_id
WHERE candidate.id = ? AND candidate.campaign_id = ?
  AND candidate.status = 'ready'
  AND (
    EXISTS (SELECT 1 FROM bundle_audience_selection_members AS materialized
            JOIN bundle_audience_selections AS materialized_selection ON materialized_selection.id = materialized.selection_id
            JOIN campaigns AS materialized_campaign ON materialized_campaign.id = materialized_selection.campaign_id
            WHERE materialized.bundle_id = candidate.bundle_id AND materialized.audience_id = member.audience_id
              AND LOWER(BTRIM(COALESCE(materialized_campaign.spec->>'audience_targeting_method', ''))) <> 'excel')
    OR EXISTS (SELECT 1 FROM bundle_audience_exclusions AS excluded
               WHERE excluded.bundle_id = candidate.bundle_id AND excluded.audience_id = member.audience_id)
    OR EXISTS (SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved
               WHERE test_reserved.bundle_id = candidate.bundle_id AND test_reserved.audience_id = member.audience_id
                 AND test_reserved.state = 'active' AND test_reserved.campaign_id <> candidate.campaign_id)
    OR EXISTS (SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved
               WHERE execution_reserved.bundle_id = candidate.bundle_id AND execution_reserved.audience_id = member.audience_id
                 AND execution_reserved.state = 'active' AND execution_reserved.campaign_id <> candidate.campaign_id)
    OR EXISTS (
      SELECT 1
      FROM campaign_targeting_execution_calculation_members AS committed_member
      JOIN campaign_targeting_execution_calculations AS committed
        ON committed.id = committed_member.calculation_id
      WHERE committed_member.audience_id = member.audience_id
        AND committed.bundle_id = candidate.bundle_id AND committed.status = 'committed'
        AND committed.id <> candidate.id
    )
  )`

type CampaignTargetingExecutionCalculationRepository interface {
	Save(context.Context, *models.CampaignTargetingExecutionCalculation) error
	ByID(context.Context, int64) (*models.CampaignTargetingExecutionCalculation, error)
	LatestByCampaignID(context.Context, uint) (*models.CampaignTargetingExecutionCalculation, error)
	LatestByInput(context.Context, uint, string, int64) (*models.CampaignTargetingExecutionCalculation, error)
	ActiveByCampaignID(context.Context, uint) (*models.CampaignTargetingExecutionCalculation, error)
	ReadyByInput(context.Context, uint, string, int64) (*models.CampaignTargetingExecutionCalculation, error)
	Members(context.Context, int64) ([]models.CampaignTargetingExecutionCalculationMember, error)
	ClaimPending(context.Context, int, time.Time, time.Time) ([]*models.CampaignTargetingExecutionCalculation, error)
	Complete(context.Context, int64, time.Time, string, []models.CampaignTargetingExecutionCalculationMember, time.Time) error
	Finish(context.Context, int64, time.Time, models.CampaignTargetingExecutionCalculationStatus, string, string, time.Time) error
	Supersede(context.Context, int64, time.Time) error
	ReadyForUpdate(context.Context, int64) (*models.CampaignTargetingExecutionCalculation, error)
	MarkCommitted(context.Context, int64, uint, time.Time) error
}

type CampaignTargetingExecutionCalculationRepositoryImpl struct{ db *gorm.DB }

func NewCampaignTargetingExecutionCalculationRepository(db *gorm.DB) CampaignTargetingExecutionCalculationRepository {
	return &CampaignTargetingExecutionCalculationRepositoryImpl{db: db}
}

func (r *CampaignTargetingExecutionCalculationRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) Save(ctx context.Context, row *models.CampaignTargetingExecutionCalculation) error {
	return r.getDB(ctx).Create(row).Error
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) ByID(ctx context.Context, id int64) (*models.CampaignTargetingExecutionCalculation, error) {
	var row models.CampaignTargetingExecutionCalculation
	if err := r.getDB(ctx).First(&row, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &row, nil
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) LatestByCampaignID(ctx context.Context, campaignID uint) (*models.CampaignTargetingExecutionCalculation, error) {
	return r.latest(ctx, "campaign_id = ?", campaignID)
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) LatestByInput(ctx context.Context, campaignID uint, hash string, requested int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return r.latest(ctx, "campaign_id = ? AND selection_input_hash = ? AND requested_audience_count = ?", campaignID, hash, requested)
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) ActiveByCampaignID(ctx context.Context, campaignID uint) (*models.CampaignTargetingExecutionCalculation, error) {
	return r.latest(ctx, "campaign_id = ? AND status = ?", campaignID, models.CampaignTargetingExecutionCalculationPending)
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) latest(ctx context.Context, query string, args ...any) (*models.CampaignTargetingExecutionCalculation, error) {
	var row models.CampaignTargetingExecutionCalculation
	err := r.getDB(ctx).Where(query, args...).Order("created_at DESC, id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) ReadyByInput(ctx context.Context, campaignID uint, hash string, requested int64) (*models.CampaignTargetingExecutionCalculation, error) {
	return r.latest(ctx, "campaign_id = ? AND selection_input_hash = ? AND requested_audience_count = ? AND status = ?", campaignID, hash, requested, models.CampaignTargetingExecutionCalculationReady)
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) Members(ctx context.Context, id int64) ([]models.CampaignTargetingExecutionCalculationMember, error) {
	if _, err := transactionForLock(ctx); err != nil {
		return nil, err
	}
	var rows []models.CampaignTargetingExecutionCalculationMember
	return rows, r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("calculation_id = ?", id).Order("selection_order ASC").Find(&rows).Error
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) ClaimPending(ctx context.Context, limit int, staleBefore, at time.Time) ([]*models.CampaignTargetingExecutionCalculation, error) {
	if limit <= 0 {
		limit = 1
	}
	var rows []*models.CampaignTargetingExecutionCalculation
	q := `WITH claimable AS (SELECT id FROM campaign_targeting_execution_calculations WHERE status = ? AND (started_at IS NULL OR started_at < ?) ORDER BY created_at ASC, id ASC FOR UPDATE SKIP LOCKED LIMIT ?) UPDATE campaign_targeting_execution_calculations AS c SET started_at = ? FROM claimable WHERE c.id = claimable.id RETURNING c.*`
	return rows, r.getDB(ctx).Raw(q, models.CampaignTargetingExecutionCalculationPending, staleBefore, limit, at).Scan(&rows).Error
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) Complete(ctx context.Context, id int64, lease time.Time, fingerprint string, members []models.CampaignTargetingExecutionCalculationMember, at time.Time) error {
	if _, err := transactionForLock(ctx); err != nil {
		return err
	}
	if len(fingerprint) != 64 || len(members) == 0 {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	db := r.getDB(ctx)
	seen := make(map[int64]struct{}, len(members))
	for i := range members {
		if members[i].SelectionOrder != int64(i) || members[i].AudienceID <= 0 || members[i].AssignedTagID == 0 {
			return ErrCampaignTargetingExecutionCalculationStateConflict
		}
		if _, duplicate := seen[members[i].AudienceID]; duplicate {
			return ErrCampaignTargetingExecutionCalculationStateConflict
		}
		seen[members[i].AudienceID] = struct{}{}
		members[i].CalculationID = id
	}
	result := db.Model(&models.CampaignTargetingExecutionCalculation{}).Where("id = ? AND status = ? AND started_at = ? AND requested_audience_count = ?", id, models.CampaignTargetingExecutionCalculationPending, lease, int64(len(members))).Updates(map[string]any{"status": models.CampaignTargetingExecutionCalculationReady, "allocation_fingerprint": fingerprint, "finished_at": at, "error_code": nil, "error_message": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	return db.CreateInBatches(&members, 1000).Error
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) Finish(ctx context.Context, id int64, lease time.Time, status models.CampaignTargetingExecutionCalculationStatus, code, message string, at time.Time) error {
	if status != models.CampaignTargetingExecutionCalculationFailed && status != models.CampaignTargetingExecutionCalculationStale {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	result := r.getDB(ctx).Model(&models.CampaignTargetingExecutionCalculation{}).Where("id = ? AND status = ? AND started_at = ?", id, models.CampaignTargetingExecutionCalculationPending, lease).Updates(map[string]any{"status": status, "finished_at": at, "error_code": code, "error_message": message})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	return nil
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) Supersede(ctx context.Context, id int64, at time.Time) error {
	result := r.getDB(ctx).Model(&models.CampaignTargetingExecutionCalculation{}).Where("id = ? AND status = ?", id, models.CampaignTargetingExecutionCalculationPending).Updates(map[string]any{"status": models.CampaignTargetingExecutionCalculationStale, "finished_at": at, "error_code": "SUPERSEDED", "error_message": "Campaign inputs changed"})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	return nil
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) ReadyForUpdate(ctx context.Context, id int64) (*models.CampaignTargetingExecutionCalculation, error) {
	if _, err := transactionForLock(ctx); err != nil {
		return nil, err
	}
	var row models.CampaignTargetingExecutionCalculation
	err := r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", id, models.CampaignTargetingExecutionCalculationReady).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
func (r *CampaignTargetingExecutionCalculationRepositoryImpl) MarkCommitted(ctx context.Context, id int64, campaignID uint, at time.Time) error {
	if _, err := transactionForLock(ctx); err != nil {
		return err
	}
	var conflicts int64
	if err := r.getDB(ctx).Raw(executionCalculationCommitConflictQuery, id, campaignID).Scan(&conflicts).Error; err != nil {
		return err
	}
	if conflicts != 0 {
		return ErrSmartTargetingExecutionReservationConflict
	}
	result := r.getDB(ctx).Model(&models.CampaignTargetingExecutionCalculation{}).Where("id = ? AND campaign_id = ? AND status = ?", id, campaignID, models.CampaignTargetingExecutionCalculationReady).Updates(map[string]any{"status": models.CampaignTargetingExecutionCalculationCommitted, "committed_at": at})
	if result.Error != nil {
		var pgErr *pq.Error
		if errors.As(result.Error, &pgErr) && string(pgErr.Code) == "23505" {
			return fmt.Errorf("commit execution audience calculation: %w", ErrSmartTargetingExecutionReservationConflict)
		}
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCampaignTargetingExecutionCalculationStateConflict
	}
	return nil
}
