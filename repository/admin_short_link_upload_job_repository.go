package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

type AdminShortLinkUploadJobRepository interface {
	Save(context.Context, *models.AdminShortLinkUploadJob) error
	ByID(context.Context, string) (*models.AdminShortLinkUploadJob, error)
	Due(context.Context, time.Time, int) ([]*models.AdminShortLinkUploadJob, error)
	Update(context.Context, *models.AdminShortLinkUploadJob) error
}

type adminShortLinkUploadJobRepository struct{ db *gorm.DB }

func NewAdminShortLinkUploadJobRepository(db *gorm.DB) AdminShortLinkUploadJobRepository {
	return &adminShortLinkUploadJobRepository{db: db}
}
func (r *adminShortLinkUploadJobRepository) Save(ctx context.Context, j *models.AdminShortLinkUploadJob) error {
	return r.db.WithContext(ctx).Create(j).Error
}
func (r *adminShortLinkUploadJobRepository) Update(ctx context.Context, j *models.AdminShortLinkUploadJob) error {
	return r.db.WithContext(ctx).Save(j).Error
}
func (r *adminShortLinkUploadJobRepository) ByID(ctx context.Context, id string) (*models.AdminShortLinkUploadJob, error) {
	var j models.AdminShortLinkUploadJob
	if err := r.db.WithContext(ctx).First(&j, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}
func (r *adminShortLinkUploadJobRepository) Due(ctx context.Context, now time.Time, limit int) ([]*models.AdminShortLinkUploadJob, error) {
	if limit <= 0 {
		limit = 20
	}
	var jobs []*models.AdminShortLinkUploadJob
	err := r.db.WithContext(ctx).Where("status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", []string{"pending", "retrying"}, now).Order("created_at ASC").Limit(limit).Find(&jobs).Error
	return jobs, err
}
