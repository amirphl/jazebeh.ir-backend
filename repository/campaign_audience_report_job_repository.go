package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CampaignAudienceReportJobRepository interface {
	CreateWithinCustomerLimit(context.Context, *models.CampaignAudienceReportJob, int64) (bool, error)
	ByIDForCustomer(context.Context, string, uint) (*models.CampaignAudienceReportJob, error)
	ClaimDue(context.Context, time.Time, time.Duration) (*models.CampaignAudienceReportJob, bool, error)
	UpdateClaimed(context.Context, *models.CampaignAudienceReportJob) (bool, error)
	ExpiredOutputPaths(context.Context, time.Time) ([]string, error)
	MarkExpiredOutput(context.Context, string, time.Time) error
	ReferencedOutputPaths(context.Context) ([]string, error)
	PurgeTerminalBefore(context.Context, time.Time) error
}
type campaignAudienceReportJobRepository struct{ db *gorm.DB }

func NewCampaignAudienceReportJobRepository(db *gorm.DB) CampaignAudienceReportJobRepository {
	return &campaignAudienceReportJobRepository{db: db}
}

// CreateWithinCustomerLimit serializes submissions for a customer before
// counting retained jobs. A count followed by an insert outside this
// transaction would let concurrent requests all pass the limit.
func (r *campaignAudienceReportJobRepository) CreateWithinCustomerLimit(ctx context.Context, j *models.CampaignAudienceReportJob, limit int64) (bool, error) {
	if limit <= 0 {
		return false, errors.New("campaign audience report job limit must be positive")
	}
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "campaign-audience-report-jobs:"+strconv.FormatUint(uint64(j.CustomerID), 10)).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&models.CampaignAudienceReportJob{}).
			Where("customer_id=? AND (status IN ? OR (status=? AND (expires_at IS NULL OR expires_at>?)))", j.CustomerID, []string{models.CampaignAudienceReportJobPending, models.CampaignAudienceReportJobProcessing}, models.CampaignAudienceReportJobCompleted, time.Now().UTC()).
			Count(&count).Error; err != nil {
			return err
		}
		if count >= limit {
			return nil
		}
		if err := tx.Create(j).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
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
func (r *campaignAudienceReportJobRepository) ExpiredOutputPaths(ctx context.Context, now time.Time) ([]string, error) {
	var paths []string
	err := r.db.WithContext(ctx).Model(&models.CampaignAudienceReportJob{}).Where("status=? AND expires_at<=? AND output_path IS NOT NULL", models.CampaignAudienceReportJobCompleted, now).Pluck("output_path", &paths).Error
	return paths, err
}
func (r *campaignAudienceReportJobRepository) MarkExpiredOutput(ctx context.Context, path string, now time.Time) error {
	return r.db.WithContext(ctx).Model(&models.CampaignAudienceReportJob{}).Where("status=? AND expires_at<=? AND output_path=?", models.CampaignAudienceReportJobCompleted, now, path).Updates(map[string]any{"status": models.CampaignAudienceReportJobExpired, "output_path": nil, "updated_at": now}).Error
}

// ReferencedOutputPaths is used to reap files from interrupted attempts. Only
// completed jobs may retain a downloadable report file.
func (r *campaignAudienceReportJobRepository) ReferencedOutputPaths(ctx context.Context) ([]string, error) {
	var paths []string
	err := r.db.WithContext(ctx).
		Model(&models.CampaignAudienceReportJob{}).
		Where("status=? AND output_path IS NOT NULL", models.CampaignAudienceReportJobCompleted).
		Pluck("output_path", &paths).Error
	return paths, err
}

// PurgeTerminalBefore removes only metadata for failed and expired jobs whose
// retention window has elapsed. Completed jobs are retained until their output
// has first been deleted and their status changed to expired.
func (r *campaignAudienceReportJobRepository) PurgeTerminalBefore(ctx context.Context, before time.Time) error {
	return r.db.WithContext(ctx).
		Where("status IN ? AND updated_at < ?", []string{models.CampaignAudienceReportJobFailed, models.CampaignAudienceReportJobExpired}, before).
		Delete(&models.CampaignAudienceReportJob{}).Error
}
