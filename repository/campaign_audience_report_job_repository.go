package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CampaignAudienceReportJobRepository interface {
	Create(context.Context, *models.CampaignAudienceReportJob) error
	ByIDForCustomer(context.Context, string, uint) (*models.CampaignAudienceReportJob, error)
	ClaimDue(context.Context, time.Time, time.Duration) (*models.CampaignAudienceReportJob, bool, error)
	UpdateClaimed(context.Context, *models.CampaignAudienceReportJob) (bool, error)
	ExpireAndPurge(context.Context, time.Time) ([]string, error)
}
type campaignAudienceReportJobRepository struct{ db *gorm.DB }

func NewCampaignAudienceReportJobRepository(db *gorm.DB) CampaignAudienceReportJobRepository {
	return &campaignAudienceReportJobRepository{db: db}
}
func (r *campaignAudienceReportJobRepository) Create(ctx context.Context, j *models.CampaignAudienceReportJob) error {
	return r.db.WithContext(ctx).Create(j).Error
}
func (r *campaignAudienceReportJobRepository) ByIDForCustomer(ctx context.Context, id string, customerID uint) (*models.CampaignAudienceReportJob, error) {
	var j models.CampaignAudienceReportJob
	if err := r.db.WithContext(ctx).Where("id=? AND customer_id=?", id, customerID).First(&j).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}
func (r *campaignAudienceReportJobRepository) ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (*models.CampaignAudienceReportJob, bool, error) {
	var out models.CampaignAudienceReportJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Raw("SELECT id FROM campaign_audience_report_jobs WHERE status=? OR (status=? AND lease_expires_at<?) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1", models.CampaignAudienceReportJobPending, models.CampaignAudienceReportJobProcessing, now).Scan(&ids).Error; err != nil || len(ids) == 0 {
			return err
		}
		token := uuid.NewString()
		if err := tx.Model(&models.CampaignAudienceReportJob{}).Where("id=?", ids[0]).Updates(map[string]any{"status": models.CampaignAudienceReportJobProcessing, "attempts": gorm.Expr("attempts+1"), "lease_token": token, "lease_expires_at": now.Add(lease), "started_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.First(&out, "id=?", ids[0]).Error
	})
	if err != nil {
		return nil, false, err
	}
	if out.ID == "" {
		return nil, false, nil
	}
	return &out, true, nil
}
func (r *campaignAudienceReportJobRepository) UpdateClaimed(ctx context.Context, j *models.CampaignAudienceReportJob) (bool, error) {
	if j.LeaseToken == nil {
		return false, nil
	}
	q := r.db.WithContext(ctx).Model(&models.CampaignAudienceReportJob{}).Where("id=? AND lease_token=?", j.ID, *j.LeaseToken).Updates(map[string]any{"status": j.Status, "row_count": j.RowCount, "sheet_count": j.SheetCount, "byte_size": j.ByteSize, "output_path": j.OutputPath, "error_code": j.ErrorCode, "error_message": j.ErrorMessage, "completed_at": j.CompletedAt, "expires_at": j.ExpiresAt, "lease_token": nil, "lease_expires_at": nil, "updated_at": time.Now().UTC()})
	return q.RowsAffected == 1, q.Error
}
func (r *campaignAudienceReportJobRepository) ExpireAndPurge(ctx context.Context, now time.Time) ([]string, error) {
	var paths []string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT output_path FROM campaign_audience_report_jobs WHERE status=? AND expires_at<=? AND output_path IS NOT NULL FOR UPDATE", models.CampaignAudienceReportJobCompleted, now).Scan(&paths).Error; err != nil {
			return err
		}
		return tx.Model(&models.CampaignAudienceReportJob{}).Where("status=? AND expires_at<=?", models.CampaignAudienceReportJobCompleted, now).Updates(map[string]any{"status": models.CampaignAudienceReportJobExpired, "output_path": nil, "updated_at": now}).Error
	})
	return paths, err
}
