package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// ProcessedCampaignRepositoryImpl implements ProcessedCampaignRepository
type ProcessedCampaignRepositoryImpl struct {
	*BaseRepository[models.ProcessedCampaign, models.ProcessedCampaignFilter]
}

func NewProcessedCampaignRepository(db *gorm.DB) ProcessedCampaignRepository {
	return &ProcessedCampaignRepositoryImpl{BaseRepository: NewBaseRepository[models.ProcessedCampaign, models.ProcessedCampaignFilter](db)}
}

func (r *ProcessedCampaignRepositoryImpl) ByID(ctx context.Context, id uint) (*models.ProcessedCampaign, error) {
	db := r.getDB(ctx)
	var row models.ProcessedCampaign
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *ProcessedCampaignRepositoryImpl) ByCampaignID(ctx context.Context, campaignID uint) (*models.ProcessedCampaign, error) {
	db := r.getDB(ctx)
	var row models.ProcessedCampaign
	err := db.Where("campaign_id = ? AND is_current", campaignID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *ProcessedCampaignRepositoryImpl) applyFilter(db *gorm.DB, f models.ProcessedCampaignFilter) *gorm.DB {
	if f.ID != nil {
		db = db.Where("id = ?", *f.ID)
	}
	if f.CampaignID != nil {
		db = db.Where("campaign_id = ?", *f.CampaignID)
	}
	if f.IsCurrent != nil {
		db = db.Where("is_current = ?", *f.IsCurrent)
	}
	if f.CreatedAfter != nil {
		db = db.Where("created_at >= ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		db = db.Where("created_at < ?", *f.CreatedBefore)
	}
	return db
}

func (r *ProcessedCampaignRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.ProcessedCampaignFilter,
	orderBy string,
	limit, offset int,
) ([]*models.ProcessedCampaign, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.ProcessedCampaign{}), filter)
	if orderBy != "" {
		query = query.Order(orderBy)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	var rows []*models.ProcessedCampaign
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *ProcessedCampaignRepositoryImpl) Count(ctx context.Context, filter models.ProcessedCampaignFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.ProcessedCampaign{}), filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *ProcessedCampaignRepositoryImpl) Exists(ctx context.Context, filter models.ProcessedCampaignFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *ProcessedCampaignRepositoryImpl) Update(ctx context.Context, pc *models.ProcessedCampaign) (err error) {
	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}
	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				err = db.Commit().Error
			}
		}()
	}
	err = db.Save(pc).Error
	return err
}

func (r *ProcessedCampaignRepositoryImpl) AppendAudienceData(ctx context.Context, id uint, ids []int64, codes []string) error {
	if len(ids) == 0 {
		return nil
	}
	db := r.getDB(ctx)
	return db.Exec(
		`UPDATE processed_campaigns SET audience_ids = audience_ids || ?, audience_codes = audience_codes || ? WHERE id = ?`,
		pq.Int64Array(ids), pq.StringArray(codes), id,
	).Error
}

func (r *ProcessedCampaignRepositoryImpl) UpdateMeta(ctx context.Context, pc *models.ProcessedCampaign) (err error) {
	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}
	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				err = db.Commit().Error
			}
		}()
	}

	updates := map[string]any{
		"last_audience_id":             pc.LastAudienceID,
		"statistics":                   pc.Statistics,
		"bundle_audience_selection_id": pc.BundleAudienceSelectionID,
		"updated_at":                   pc.UpdatedAt,
	}
	err = db.Model(&models.ProcessedCampaign{}).
		Where("id = ?", pc.ID).
		Updates(updates).Error
	return err
}
