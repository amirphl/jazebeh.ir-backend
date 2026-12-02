package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BundleActionRepository owns action-file state and the Bundle-scoped ATR
// read models. All expensive work is claimed by a worker; handlers never
// parse spreadsheets or aggregate delivery histories.
type BundleActionRepository interface {
	Create(context.Context, *models.BundleActionFile) error
	ByID(context.Context, int64) (*models.BundleActionFile, error)
	List(context.Context, uint, int, int) ([]*models.BundleActionFile, int64, error)
	RequestDelete(context.Context, int64, uint, time.Time) error
	ClaimPending(context.Context, int, time.Time, time.Time) ([]*models.BundleActionFile, error)
	StoreUIDsAndResult(context.Context, int64, uint, []string, int64, int64, int64, time.Time) error
	Complete(context.Context, int64, time.Time) error
	Fail(context.Context, int64, time.Time, string, string) error
	Summary(context.Context, uint) (*models.BundleActionSummary, error)
	CampaignMetric(context.Context, uint, uint) (*models.BundleActionCampaignMetric, error)
	TagMetrics(context.Context, uint) ([]*models.BundleActionTagMetric, error)
	ActiveUIDSet(context.Context, uint, []string) (map[string]bool, error)
}

type BundleActionRepositoryImpl struct{ db *gorm.DB }

func NewBundleActionRepository(db *gorm.DB) BundleActionRepository {
	return &BundleActionRepositoryImpl{db: db}
}
func (r *BundleActionRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

func (r *BundleActionRepositoryImpl) Create(ctx context.Context, row *models.BundleActionFile) error {
	return r.getDB(ctx).Create(row).Error
}
func (r *BundleActionRepositoryImpl) ByID(ctx context.Context, id int64) (*models.BundleActionFile, error) {
	var row models.BundleActionFile
	if err := r.getDB(ctx).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}
func (r *BundleActionRepositoryImpl) List(ctx context.Context, bundleID uint, limit, offset int) ([]*models.BundleActionFile, int64, error) {
	db := r.getDB(ctx).Where("bundle_id = ?", bundleID)
	var n int64
	if err := db.Model(&models.BundleActionFile{}).Count(&n).Error; err != nil {
		return nil, 0, err
	}
	var rows []*models.BundleActionFile
	// The API contract is newest uploaded file first. Keep ID as a tie-breaker
	// so offset pagination remains deterministic when uploads share a timestamp.
	err := db.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, n, err
}
func (r *BundleActionRepositoryImpl) RequestDelete(ctx context.Context, id int64, customerID uint, at time.Time) error {
	return r.getDB(ctx).Model(&models.BundleActionFile{}).Where("id = ? AND status = ?", id, models.BundleActionFileProcessed).Updates(map[string]any{"status": models.BundleActionFileDeletePending, "deleted_by_customer_id": customerID, "updated_at": at}).Error
}

func (r *BundleActionRepositoryImpl) ClaimPending(ctx context.Context, limit int, stale, at time.Time) ([]*models.BundleActionFile, error) {
	if limit < 1 {
		limit = 1
	}
	var out []*models.BundleActionFile
	err := r.getDB(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []*models.BundleActionFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status IN ? OR (status = ? AND started_at < ?)", []models.BundleActionFileStatus{models.BundleActionFilePending, models.BundleActionFileDeletePending}, models.BundleActionFileProcessing, stale).Order("created_at,id").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			next := models.BundleActionFileProcessing
			if row.Status == models.BundleActionFileDeletePending {
				next = models.BundleActionFileDeletePending
			}
			if err := tx.Model(&models.BundleActionFile{}).Where("id=?", row.ID).Updates(map[string]any{"status": next, "started_at": at, "updated_at": at, "error_code": nil, "error_message": nil}).Error; err != nil {
				return err
			}
			row.Status = next
			row.StartedAt = &at
			out = append(out, row)
		}
		return nil
	})
	return out, err
}

// StoreUIDsAndResult persists the syntactically valid UID set and resolves its
// per-file diagnostics in the same transaction.  These snapshots explain the
// result of this upload; live ATRs continue to be derived from current source
// data when the file is completed.
func (r *BundleActionRepositoryImpl) StoreUIDsAndResult(ctx context.Context, fileID int64, bundleID uint, uids []string, total, duplicates, invalid int64, at time.Time) error {
	return r.getDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bundle_action_file_id = ?", fileID).Delete(&models.BundleActionFileUID{}).Error; err != nil {
			return err
		}
		for start := 0; start < len(uids); start += 1000 {
			end := start + 1000
			if end > len(uids) {
				end = len(uids)
			}
			rows := make([]models.BundleActionFileUID, 0, end-start)
			for _, uid := range uids[start:end] {
				rows = append(rows, models.BundleActionFileUID{BundleActionFileID: fileID, BundleID: bundleID, UID: uid, ValidationStatus: "pending", CreatedAt: at})
			}
			if len(rows) > 0 {
				if err := tx.Create(&rows).Error; err != nil {
					return err
				}
			}
		}

		// The lateral lookup ensures that a UID is resolved only inside this
		// Bundle.  The bundle-member uniqueness constraint makes this one row.
		const resolveUIDs = `
WITH resolved AS (
    SELECT u.id,
           c.id AS campaign_id,
           attr.assigned_tag_id,
           c.phase AS phase_type,
           CASE
             WHEN c.id IS NULL THEN 'outside_bundle'
             WHEN attr.assigned_tag_id IS NULL THEN 'unassigned_tag'
             WHEN NOT EXISTS (
                 SELECT 1
                 FROM processed_campaigns p
                 JOIN sent_sms x ON x.processed_campaign_id=p.id
                 JOIN sms_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id
                 WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id
                   AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
                 UNION ALL
                 SELECT 1 FROM processed_campaigns p JOIN sent_bale_messages x ON x.processed_campaign_id=p.id JOIN bale_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id
                 WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id
                   AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
                 UNION ALL
                 SELECT 1 FROM processed_campaigns p JOIN sent_rubika_messages x ON x.processed_campaign_id=p.id JOIN rubika_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id
                 WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id
                   AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
                 UNION ALL
                 SELECT 1 FROM processed_campaigns p JOIN sent_splus_messages x ON x.processed_campaign_id=p.id JOIN splus_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id
                 WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id
                   AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
             ) THEN 'missing_delivery'
             ELSE 'eligible'
           END AS validation_status
    FROM bundle_action_file_uids u
    LEFT JOIN LATERAL (
        SELECT m.selection_id, m.audience_id
        FROM bundle_audience_selection_members m
        JOIN audience_profiles a0 ON a0.id=m.audience_id
        WHERE m.bundle_id=u.bundle_id AND a0.uid=u.uid
        LIMIT 1
    ) member ON TRUE
    LEFT JOIN bundle_audience_selections s ON s.id=member.selection_id AND s.bundle_id=u.bundle_id
    LEFT JOIN campaigns c ON c.id=s.campaign_id AND c.bundle_id=u.bundle_id
    LEFT JOIN audience_profiles a ON a.id=member.audience_id
    LEFT JOIN campaign_audience_tag_attributions attr
      ON attr.campaign_id=c.id AND attr.bundle_id=u.bundle_id AND attr.audience_id=a.id
    WHERE u.bundle_action_file_id=?
)
UPDATE bundle_action_file_uids u
SET campaign_id=resolved.campaign_id,
    assigned_tag_id=resolved.assigned_tag_id,
    phase_type=resolved.phase_type,
    validation_status=resolved.validation_status
FROM resolved
WHERE u.id=resolved.id`
		if err := tx.Exec(resolveUIDs, fileID).Error; err != nil {
			return err
		}

		const updateResult = `
UPDATE bundle_action_files f
SET total_row_count=?,
    unique_uid_count=?,
    duplicate_in_file_count=?,
    invalid_uid_count=?,
    new_action_uid_count=(
      SELECT COUNT(*) FROM bundle_action_file_uids u
      WHERE u.bundle_action_file_id=f.id
        AND NOT EXISTS (
          SELECT 1 FROM bundle_action_file_uids other_u
          JOIN bundle_action_files other_f ON other_f.id=other_u.bundle_action_file_id
          WHERE other_f.bundle_id=f.bundle_id AND other_f.status='processed'
            AND other_f.id<>f.id AND other_u.uid=u.uid
        )
    ),
    duplicate_in_other_files_count=(
      SELECT COUNT(*) FROM bundle_action_file_uids u
      WHERE u.bundle_action_file_id=f.id
        AND EXISTS (
          SELECT 1 FROM bundle_action_file_uids other_u
          JOIN bundle_action_files other_f ON other_f.id=other_u.bundle_action_file_id
          WHERE other_f.bundle_id=f.bundle_id AND other_f.status='processed'
            AND other_f.id<>f.id AND other_u.uid=u.uid
        )
    ),
    outside_bundle_count=(SELECT COUNT(*) FROM bundle_action_file_uids WHERE bundle_action_file_id=f.id AND validation_status='outside_bundle'),
    unassigned_tag_count=(SELECT COUNT(*) FROM bundle_action_file_uids WHERE bundle_action_file_id=f.id AND validation_status='unassigned_tag'),
    missing_delivery_count=(SELECT COUNT(*) FROM bundle_action_file_uids WHERE bundle_action_file_id=f.id AND validation_status='missing_delivery'),
    eligible_action_uid_count=(SELECT COUNT(*) FROM bundle_action_file_uids WHERE bundle_action_file_id=f.id AND validation_status='eligible'),
    updated_at=?
WHERE f.id=? AND f.status='processing'`
		result := tx.Exec(updateResult, total, len(uids), duplicates, invalid, at, fileID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("action file %d is no longer being processed", fileID)
		}
		return nil
	})
}
func (r *BundleActionRepositoryImpl) Fail(ctx context.Context, id int64, at time.Time, code, message string) error {
	if len(code) > 64 {
		code = code[:64]
	}
	if len(message) > 255 {
		message = message[:255]
	}
	return r.getDB(ctx).Model(&models.BundleActionFile{}).Where("id=? AND status=?", id, models.BundleActionFileProcessing).Updates(map[string]any{"status": models.BundleActionFileFailed, "processed_at": at, "error_code": code, "error_message": message, "updated_at": at}).Error
}

func (r *BundleActionRepositoryImpl) Complete(ctx context.Context, id int64, at time.Time) error {
	return r.getDB(ctx).Transaction(func(tx *gorm.DB) error {
		var file models.BundleActionFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, id).Error; err != nil {
			return err
		}
		// Refresh replaces every metric row for this Bundle.  Two files in the
		// same Bundle may finish concurrently, so serialize the whole transition
		// on the Bundle row; a file-row lock alone cannot prevent one refresh from
		// publishing a snapshot that omits the other's uncommitted completion.
		if err := tx.Exec("SELECT id FROM bundles WHERE id = ? FOR UPDATE", file.BundleID).Error; err != nil {
			return err
		}
		if file.Status == models.BundleActionFileDeletePending {
			if err := tx.Model(&models.BundleActionFile{}).Where("id=?", id).Updates(map[string]any{"status": models.BundleActionFileDeleted, "deleted_at": at, "processed_at": at, "updated_at": at}).Error; err != nil {
				return err
			}
		} else if file.Status == models.BundleActionFileProcessing {
			if err := tx.Model(&models.BundleActionFile{}).Where("id=?", id).Updates(map[string]any{"status": models.BundleActionFileProcessed, "processed_at": at, "updated_at": at}).Error; err != nil {
				return err
			}
		} else {
			return fmt.Errorf("action file %d is not claimable", id)
		}
		if err := refreshBundleActionMetrics(tx, file.BundleID, at); err != nil {
			return err
		}
		return tx.Model(&models.Bundle{}).Where("id=?", file.BundleID).Updates(map[string]any{"action_data_updated_at": at, "updated_at": at}).Error
	})
}

// eligibleActionAudienceSQL intentionally joins immutable selection and tag
// attribution rows. It never consults audience_profiles.tags, which are live
// mutable profile metadata rather than historical campaign attribution.
const eligibleActionAudienceSQL = `
WITH active_actions AS (
 SELECT DISTINCT uid FROM bundle_action_file_uids u JOIN bundle_action_files f ON f.id=u.bundle_action_file_id WHERE f.bundle_id=? AND f.status='processed'
), eligible AS (
 SELECT DISTINCT c.id campaign_id, a.id audience_id, a.uid, attr.assigned_tag_id tag_id, c.phase
 FROM bundle_audience_selection_members m
 JOIN bundle_audience_selections s ON s.id=m.selection_id AND s.bundle_id=m.bundle_id
 JOIN campaigns c ON c.id=s.campaign_id AND c.bundle_id=m.bundle_id
 JOIN audience_profiles a ON a.id=m.audience_id
 JOIN campaign_audience_tag_attributions attr ON attr.campaign_id=c.id AND attr.bundle_id=m.bundle_id AND attr.audience_id=a.id
 WHERE m.bundle_id=? AND EXISTS (
   SELECT 1 FROM processed_campaigns p JOIN sent_sms x ON x.processed_campaign_id=p.id JOIN sms_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
   UNION ALL SELECT 1 FROM processed_campaigns p JOIN sent_bale_messages x ON x.processed_campaign_id=p.id JOIN bale_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
   UNION ALL SELECT 1 FROM processed_campaigns p JOIN sent_rubika_messages x ON x.processed_campaign_id=p.id JOIN rubika_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
   UNION ALL SELECT 1 FROM processed_campaigns p JOIN sent_splus_messages x ON x.processed_campaign_id=p.id JOIN splus_status_results st ON st.processed_campaign_id=x.processed_campaign_id AND st.tracking_id=x.tracking_id WHERE p.is_current AND p.campaign_id=c.id AND p.bundle_audience_selection_id=s.id AND x.phone_number=a.phone_number AND st.total_parts>0 AND st.total_parts=st.total_delivered_parts
 )
), marked AS (SELECT e.*, EXISTS(SELECT 1 FROM active_actions ac WHERE ac.uid=e.uid) action FROM eligible e)`

func refreshBundleActionMetrics(tx *gorm.DB, bundleID uint, at time.Time) error {
	var active int64
	if err := tx.Model(&models.BundleActionFile{}).Where("bundle_id=? AND status=?", bundleID, models.BundleActionFileProcessed).Count(&active).Error; err != nil {
		return err
	}
	if err := tx.Where("bundle_id=?", bundleID).Delete(&models.BundleActionCampaignMetric{}).Error; err != nil {
		return err
	}
	if err := tx.Where("bundle_id=?", bundleID).Delete(&models.BundleActionTagMetric{}).Error; err != nil {
		return err
	}
	if active == 0 {
		return tx.Exec(`INSERT INTO bundle_action_summaries(bundle_id,has_active_action_files,action_count,eligible_delivered_count,updated_at) VALUES (?,false,0,0,?) ON CONFLICT(bundle_id) DO UPDATE SET has_active_action_files=false,action_count=0,eligible_delivered_count=0,updated_at=EXCLUDED.updated_at`, bundleID, at).Error
	}
	base := eligibleActionAudienceSQL
	if err := tx.Exec(base+` INSERT INTO bundle_action_campaign_metrics(bundle_id,campaign_id,action_count,eligible_delivered_count,updated_at) SELECT ?,campaign_id,COUNT(*) FILTER (WHERE action),COUNT(*),? FROM marked GROUP BY campaign_id`, bundleID, bundleID, at).Error; err != nil {
		return fmt.Errorf("refresh campaign atr: %w", err)
	}
	if err := tx.Exec(base+` INSERT INTO bundle_action_tag_metrics(bundle_id,tag_id,test_action_count,test_eligible_delivered_count,overall_action_count,overall_eligible_delivered_count,updated_at) SELECT ?,tag_id,COUNT(*) FILTER(WHERE phase='test' AND action),COUNT(*) FILTER(WHERE phase='test'),COUNT(*) FILTER(WHERE action),COUNT(*),? FROM marked GROUP BY tag_id`, bundleID, bundleID, at).Error; err != nil {
		return fmt.Errorf("refresh tag atr: %w", err)
	}
	return tx.Exec(base+` INSERT INTO bundle_action_summaries(bundle_id,has_active_action_files,action_count,eligible_delivered_count,updated_at) SELECT ?,true,COUNT(*) FILTER(WHERE action),COUNT(*),? FROM marked ON CONFLICT(bundle_id) DO UPDATE SET has_active_action_files=true,action_count=EXCLUDED.action_count,eligible_delivered_count=EXCLUDED.eligible_delivered_count,updated_at=EXCLUDED.updated_at`, bundleID, bundleID, at).Error
}
func (r *BundleActionRepositoryImpl) Summary(ctx context.Context, bundleID uint) (*models.BundleActionSummary, error) {
	var x models.BundleActionSummary
	err := r.getDB(ctx).Where("bundle_id=?", bundleID).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &models.BundleActionSummary{BundleID: bundleID}, nil
	}
	return &x, err
}
func (r *BundleActionRepositoryImpl) CampaignMetric(ctx context.Context, bundleID, campaignID uint) (*models.BundleActionCampaignMetric, error) {
	var x models.BundleActionCampaignMetric
	err := r.getDB(ctx).Where("bundle_id=? AND campaign_id=?", bundleID, campaignID).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &models.BundleActionCampaignMetric{BundleID: bundleID, CampaignID: campaignID}, nil
	}
	return &x, err
}
func (r *BundleActionRepositoryImpl) TagMetrics(ctx context.Context, bundleID uint) ([]*models.BundleActionTagMetric, error) {
	var x []*models.BundleActionTagMetric
	return x, r.getDB(ctx).Where("bundle_id=?", bundleID).Order("tag_id").Find(&x).Error
}
func (r *BundleActionRepositoryImpl) ActiveUIDSet(ctx context.Context, bundleID uint, uids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(uids) == 0 {
		return out, nil
	}
	var found []string
	err := r.getDB(ctx).Table("bundle_action_file_uids u").Select("DISTINCT u.uid").Joins("JOIN bundle_action_files f ON f.id=u.bundle_action_file_id").Where("u.bundle_id=? AND f.status=? AND u.uid IN ?", bundleID, models.BundleActionFileProcessed, uids).Scan(&found).Error
	for _, uid := range found {
		out[uid] = true
	}
	return out, err
}

var _ = utils.UTCNow
