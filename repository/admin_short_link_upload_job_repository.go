package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AdminShortLinkUploadJobRepository interface {
	Save(context.Context, *models.AdminShortLinkUploadJob) error
	ByID(context.Context, string) (*models.AdminShortLinkUploadJob, error)
	Due(context.Context, time.Time, int) ([]*models.AdminShortLinkUploadJob, error)
	Update(context.Context, *models.AdminShortLinkUploadJob) error
	Claim(context.Context, string, time.Time, time.Duration) (*models.AdminShortLinkUploadJob, bool, error)
	UpdateClaimed(context.Context, *models.AdminShortLinkUploadJob) (bool, error)
	PersistAllocation(context.Context, *models.AdminShortLinkUploadJob, []*models.ShortLink) error
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
	err := r.db.WithContext(ctx).Where("(status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = ? AND lease_expires_at <= ?)", []string{"pending", "retrying"}, now, "processing", now).Order("created_at ASC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func (r *adminShortLinkUploadJobRepository) Claim(ctx context.Context, id string, now time.Time, lease time.Duration) (*models.AdminShortLinkUploadJob, bool, error) {
	token := uuid.NewString()
	expires := now.Add(lease)
	result := r.db.WithContext(ctx).Model(&models.AdminShortLinkUploadJob{}).
		Where("id = ? AND ((status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = ? AND lease_expires_at <= ?))", id, []string{"pending", "retrying"}, now, "processing", now).
		Updates(map[string]any{"status": "processing", "attempts": gorm.Expr("attempts + 1"), "last_error": nil, "lease_token": token, "lease_expires_at": expires, "updated_at": now})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, false, nil
	}
	job, err := r.ByID(ctx, id)
	return job, err == nil && job != nil, err
}

// UpdateClaimed prevents an expired worker from overwriting the state written
// by the worker that recovered its lease.
func (r *adminShortLinkUploadJobRepository) UpdateClaimed(ctx context.Context, j *models.AdminShortLinkUploadJob) (bool, error) {
	if j.LeaseToken == nil {
		return false, nil
	}
	result := r.db.WithContext(ctx).Model(&models.AdminShortLinkUploadJob{}).
		Where("id = ? AND lease_token = ?", j.ID, *j.LeaseToken).Updates(map[string]any{
		"status": j.Status, "total_rows": j.TotalRows, "created": j.Created, "skipped": j.Skipped,
		"published": j.Published, "next_attempt_at": j.NextAttemptAt, "last_error": j.LastError,
		"completed_at": j.CompletedAt, "lease_token": nil, "lease_expires_at": nil,
	})
	return result.RowsAffected == 1, result.Error
}

// PersistAllocation commits all local rows and their progress counters in one
// transaction. A failed batch can therefore never look like a complete
// allocation on the next attempt.
func (r *adminShortLinkUploadJobRepository) PersistAllocation(ctx context.Context, j *models.AdminShortLinkUploadJob, links []*models.ShortLink) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext('short_link_uid_allocation'))").Error; err != nil {
				return err
			}
		}
		if len(links) > 0 {
			if err := tx.CreateInBatches(links, 500).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&models.AdminShortLinkUploadJob{}).
			Where("id = ? AND lease_token = ?", j.ID, j.LeaseToken).
			Updates(map[string]any{"total_rows": j.TotalRows, "created": len(links), "skipped": j.Skipped})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("upload job lease was lost while persisting allocation")
		}
		return nil
	})
}
