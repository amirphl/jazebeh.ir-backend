package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// CampaignStatusJobRepositoryImpl implements CampaignStatusJobRepository
type CampaignStatusJobRepositoryImpl struct {
	*BaseRepository[models.CampaignStatusJob, any]
}

func NewCampaignStatusJobRepository(db *gorm.DB) CampaignStatusJobRepository {
	return &CampaignStatusJobRepositoryImpl{BaseRepository: NewBaseRepository[models.CampaignStatusJob, any](db)}
}

func (r *CampaignStatusJobRepositoryImpl) ByID(ctx context.Context, id uint) (*models.CampaignStatusJob, error) {
	db := r.getDB(ctx)
	var row models.CampaignStatusJob
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ListDue returns jobs for one platform scheduled before or at 'now' with retry_count < 3.
func (r *CampaignStatusJobRepositoryImpl) ListDue(ctx context.Context, platform string, now time.Time, limit int) ([]*models.CampaignStatusJob, error) {
	if limit <= 0 {
		limit = 100
	}
	db := r.getDB(ctx)
	var rows []*models.CampaignStatusJob
	if err := db.Where("platform = ? AND scheduled_at <= ? AND retry_count < ? AND executed_at IS NULL", platform, now, 3).
		Order("scheduled_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListDueInterleavedByCampaign returns due jobs in round-robin campaign order:
// each processed campaign's earliest job is returned before a second job from
// any campaign. It is intentionally separate from ListDue because only the SMS
// scheduler currently processes processed-campaign groups concurrently.
func (r *CampaignStatusJobRepositoryImpl) ListDueInterleavedByCampaign(ctx context.Context, platform string, now time.Time, limit int) ([]*models.CampaignStatusJob, error) {
	if limit <= 0 {
		limit = 100
	}
	db := r.getDB(ctx)
	rankedDueJobs := db.Table("campaign_status_jobs").
		Select(`campaign_status_jobs.*, ROW_NUMBER() OVER (
			PARTITION BY processed_campaign_id
			ORDER BY scheduled_at ASC, id ASC
		) AS campaign_position`).
		Where("platform = ? AND scheduled_at <= ? AND retry_count < ? AND executed_at IS NULL", platform, now, 3)

	var rows []*models.CampaignStatusJob
	if err := db.Table("(?) AS ranked_due_jobs", rankedDueJobs).
		Order("campaign_position ASC, scheduled_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *CampaignStatusJobRepositoryImpl) SaveBatch(ctx context.Context, jobs []*models.CampaignStatusJob) error {
	return r.BaseRepository.SaveBatch(ctx, jobs)
}

func (r *CampaignStatusJobRepositoryImpl) Update(ctx context.Context, job *models.CampaignStatusJob) error {
	db := r.getDB(ctx)
	return db.Save(job).Error
}

// ByFilter: since no filter is defined, apply order/limit/offset only
func (r *CampaignStatusJobRepositoryImpl) ByFilter(ctx context.Context, _ any, orderBy string, limit, offset int) ([]*models.CampaignStatusJob, error) {
	db := r.getDB(ctx)
	if orderBy != "" {
		db = db.Order(orderBy)
	}
	if limit > 0 {
		db = db.Limit(limit)
	}
	if offset > 0 {
		db = db.Offset(offset)
	}
	var rows []*models.CampaignStatusJob
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *CampaignStatusJobRepositoryImpl) Count(ctx context.Context, _ any) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	if err := db.Model(&models.CampaignStatusJob{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *CampaignStatusJobRepositoryImpl) Exists(ctx context.Context, filter any) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
