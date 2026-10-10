package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

type BundleTagEvaluationRunRepositoryImpl struct {
	*BaseRepository[models.BundleTagEvaluationRun, any]
}

func NewBundleTagEvaluationRunRepository(db *gorm.DB) BundleTagEvaluationRunRepository {
	return &BundleTagEvaluationRunRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagEvaluationRun, any](db),
	}
}

func (r *BundleTagEvaluationRunRepositoryImpl) ByID(ctx context.Context, id int64) (*models.BundleTagEvaluationRun, error) {
	var row models.BundleTagEvaluationRun
	if err := r.getDB(ctx).Last(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *BundleTagEvaluationRunRepositoryImpl) ListByBundleID(ctx context.Context, bundleID uint, limit int) ([]*models.BundleTagEvaluationRun, error) {
	query := r.getDB(ctx).Model(&models.BundleTagEvaluationRun{}).
		Where("bundle_id = ?", bundleID).
		Order("created_at DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []*models.BundleTagEvaluationRun
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BundleTagEvaluationRunRepositoryImpl) CountByCustomerIDCreatedBetween(ctx context.Context, customerID uint, start, end time.Time) (int64, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationRun{}).
		Where("customer_id = ? AND created_at >= ? AND created_at < ?", customerID, start, end).
		Count(&count).Error
	return count, err
}

type BundleTagEvaluationEventRepositoryImpl struct {
	*BaseRepository[models.BundleTagEvaluationEvent, any]
}

func NewBundleTagEvaluationEventRepository(db *gorm.DB) BundleTagEvaluationEventRepository {
	return &BundleTagEvaluationEventRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagEvaluationEvent, any](db),
	}
}

func (r *BundleTagEvaluationEventRepositoryImpl) LatestByRunID(ctx context.Context, runID int64) (*models.BundleTagEvaluationEvent, error) {
	var row models.BundleTagEvaluationEvent
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationEvent{}).
		Where("evaluation_run_id = ?", runID).
		Order("created_at DESC, id DESC").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *BundleTagEvaluationEventRepositoryImpl) ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagEvaluationEvent, error) {
	var rows []*models.BundleTagEvaluationEvent
	if err := r.getDB(ctx).Model(&models.BundleTagEvaluationEvent{}).
		Where("evaluation_run_id = ?", runID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BundleTagEvaluationEventRepositoryImpl) ExistsByRunIDAndType(ctx context.Context, runID int64, eventType string) (bool, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationEvent{}).
		Where("evaluation_run_id = ? AND event_type = ?", runID, eventType).
		Count(&count).Error
	return count > 0, err
}

func (r *BundleTagEvaluationEventRepositoryImpl) ExistsByBatchIDAndType(ctx context.Context, batchID int64, eventType string) (bool, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationEvent{}).
		Where("batch_id = ? AND event_type = ?", batchID, eventType).
		Count(&count).Error
	return count > 0, err
}

type BundleTagPersonaAnalysisAttemptRepositoryImpl struct {
	*BaseRepository[models.BundleTagPersonaAnalysisAttempt, any]
}

func NewBundleTagPersonaAnalysisAttemptRepository(db *gorm.DB) BundleTagPersonaAnalysisAttemptRepository {
	return &BundleTagPersonaAnalysisAttemptRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagPersonaAnalysisAttempt, any](db),
	}
}

func (r *BundleTagPersonaAnalysisAttemptRepositoryImpl) LatestByRunID(ctx context.Context, runID int64) (*models.BundleTagPersonaAnalysisAttempt, error) {
	var row models.BundleTagPersonaAnalysisAttempt
	err := r.getDB(ctx).Model(&models.BundleTagPersonaAnalysisAttempt{}).
		Where("evaluation_run_id = ?", runID).
		Order("attempt_number DESC, created_at DESC").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *BundleTagPersonaAnalysisAttemptRepositoryImpl) ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagPersonaAnalysisAttempt, error) {
	var rows []*models.BundleTagPersonaAnalysisAttempt
	if err := r.getDB(ctx).Model(&models.BundleTagPersonaAnalysisAttempt{}).
		Where("evaluation_run_id = ?", runID).
		Order("attempt_number ASC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type BundleTagEvaluationBatchRepositoryImpl struct {
	*BaseRepository[models.BundleTagEvaluationBatch, any]
}

func NewBundleTagEvaluationBatchRepository(db *gorm.DB) BundleTagEvaluationBatchRepository {
	return &BundleTagEvaluationBatchRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagEvaluationBatch, any](db),
	}
}

func (r *BundleTagEvaluationBatchRepositoryImpl) ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagEvaluationBatch, error) {
	var rows []*models.BundleTagEvaluationBatch
	if err := r.getDB(ctx).Model(&models.BundleTagEvaluationBatch{}).
		Where("evaluation_run_id = ?", runID).
		Order("batch_number ASC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type BundleTagEvaluationBatchAttemptRepositoryImpl struct {
	*BaseRepository[models.BundleTagEvaluationBatchAttempt, any]
}

func NewBundleTagEvaluationBatchAttemptRepository(db *gorm.DB) BundleTagEvaluationBatchAttemptRepository {
	return &BundleTagEvaluationBatchAttemptRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagEvaluationBatchAttempt, any](db),
	}
}

func (r *BundleTagEvaluationBatchAttemptRepositoryImpl) LatestByBatchID(ctx context.Context, batchID int64) (*models.BundleTagEvaluationBatchAttempt, error) {
	var row models.BundleTagEvaluationBatchAttempt
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationBatchAttempt{}).
		Where("batch_id = ?", batchID).
		Order("attempt_number DESC, created_at DESC").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *BundleTagEvaluationBatchAttemptRepositoryImpl) ListByBatchID(ctx context.Context, batchID int64) ([]*models.BundleTagEvaluationBatchAttempt, error) {
	var rows []*models.BundleTagEvaluationBatchAttempt
	if err := r.getDB(ctx).Model(&models.BundleTagEvaluationBatchAttempt{}).
		Where("batch_id = ?", batchID).
		Order("attempt_number ASC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type BundleTagScoreRepositoryImpl struct {
	*BaseRepository[models.BundleTagScore, any]
}

func NewBundleTagScoreRepository(db *gorm.DB) BundleTagScoreRepository {
	return &BundleTagScoreRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BundleTagScore, any](db),
	}
}

func (r *BundleTagScoreRepositoryImpl) CountByRunID(ctx context.Context, runID int64) (int64, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.BundleTagScore{}).
		Where("evaluation_run_id = ?", runID).
		Count(&count).Error
	return count, err
}

func (r *BundleTagScoreRepositoryImpl) CountByRunIDAndBatchID(ctx context.Context, runID int64, batchID int64) (int64, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.BundleTagScore{}).
		Where("evaluation_run_id = ? AND batch_id = ?", runID, batchID).
		Count(&count).Error
	return count, err
}

func (r *BundleTagScoreRepositoryImpl) ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagScore, error) {
	var rows []*models.BundleTagScore
	if err := r.getDB(ctx).Model(&models.BundleTagScore{}).
		Where("evaluation_run_id = ?", runID).
		Order("tag_id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type BundleTagEvaluationReadRepositoryImpl struct {
	db *gorm.DB
}

func NewBundleTagEvaluationReadRepository(db *gorm.DB) BundleTagEvaluationReadRepository {
	return &BundleTagEvaluationReadRepositoryImpl{db: db}
}

func (r *BundleTagEvaluationReadRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

func (r *BundleTagEvaluationReadRepositoryImpl) ByBundleID(ctx context.Context, bundleID uint) (*models.CurrentBundleTagEvaluationStatus, error) {
	var row models.CurrentBundleTagEvaluationStatus
	err := r.getDB(ctx).Model(&models.CurrentBundleTagEvaluationStatus{}).
		Where("bundle_id = ?", bundleID).
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.BundleID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *BundleTagEvaluationReadRepositoryImpl) ListByBundleIDs(ctx context.Context, bundleIDs []uint) ([]*models.CurrentBundleTagEvaluationStatus, error) {
	if len(bundleIDs) == 0 {
		return []*models.CurrentBundleTagEvaluationStatus{}, nil
	}
	var rows []*models.CurrentBundleTagEvaluationStatus
	err := r.getDB(ctx).Model(&models.CurrentBundleTagEvaluationStatus{}).
		Where("bundle_id IN ?", bundleIDs).
		Find(&rows).Error
	return rows, err
}

func (r *BundleTagEvaluationReadRepositoryImpl) ByRunID(ctx context.Context, runID int64) (*models.BundleTagEvaluationRunStatus, error) {
	var row models.BundleTagEvaluationRunStatus
	err := r.getDB(ctx).Model(&models.BundleTagEvaluationRunStatus{}).
		Where("evaluation_run_id = ?", runID).
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.EvaluationRunID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *BundleTagEvaluationReadRepositoryImpl) ListPendingRuns(ctx context.Context, limit int) ([]*models.BundleTagEvaluationRunStatus, error) {
	query := r.getDB(ctx).Model(&models.BundleTagEvaluationRunStatus{}).
		// The event log contains many resumable intermediate event types. Query
		// the derived state instead of maintaining an incomplete event allowlist.
		Where("run_status IN ?", []string{"created", "evaluating"}).
		Order("evaluation_created_at ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []*models.BundleTagEvaluationRunStatus
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BundleTagEvaluationReadRepositoryImpl) ListCurrentScoresByBundleID(ctx context.Context, bundleID uint, limit, offset int) ([]*models.CurrentBundleTagScore, error) {
	query := r.getDB(ctx).Model(&models.CurrentBundleTagScore{}).
		Joins("JOIN tags AS active_tags ON active_tags.id = current_bundle_tag_scores.tag_id AND active_tags.is_active = ?", true).
		Where("bundle_id = ?", bundleID).
		Order("bundle_fit_score DESC, tag_id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	var rows []*models.CurrentBundleTagScore
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BundleTagEvaluationReadRepositoryImpl) CountCurrentScoresByBundleID(ctx context.Context, bundleID uint) (int64, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.CurrentBundleTagScore{}).
		Joins("JOIN tags AS active_tags ON active_tags.id = current_bundle_tag_scores.tag_id AND active_tags.is_active = ?", true).
		Where("bundle_id = ?", bundleID).
		Count(&count).Error
	return count, err
}
