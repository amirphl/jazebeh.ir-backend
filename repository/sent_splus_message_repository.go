package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// SentSplusMessageRepositoryImpl implements SentSplusMessageRepository.
type SentSplusMessageRepositoryImpl struct {
	*BaseRepository[models.SentSplusMessage, models.SentSplusMessageFilter]
}

func NewSentSplusMessageRepository(db *gorm.DB) SentSplusMessageRepository {
	return &SentSplusMessageRepositoryImpl{BaseRepository: NewBaseRepository[models.SentSplusMessage, models.SentSplusMessageFilter](db)}
}

func (r *SentSplusMessageRepositoryImpl) ByID(ctx context.Context, id uint) (*models.SentSplusMessage, error) {
	db := r.getDB(ctx)
	var row models.SentSplusMessage
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *SentSplusMessageRepositoryImpl) applyFilter(db *gorm.DB, f models.SentSplusMessageFilter) *gorm.DB {
	if f.ID != nil {
		db = db.Where("id = ?", *f.ID)
	}
	if f.ProcessedCampaignID != nil {
		db = db.Where("processed_campaign_id = ?", *f.ProcessedCampaignID)
	}
	if f.PhoneNumber != nil {
		db = db.Where("phone_number = ?", *f.PhoneNumber)
	}
	if f.Status != nil {
		db = db.Where("status = ?", *f.Status)
	}
	if f.CreatedAfter != nil {
		db = db.Where("created_at >= ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		db = db.Where("created_at < ?", *f.CreatedBefore)
	}
	return db
}

func (r *SentSplusMessageRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.SentSplusMessageFilter,
	orderBy string,
	limit, offset int,
) ([]*models.SentSplusMessage, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.SentSplusMessage{}), filter)
	if orderBy != "" {
		query = query.Order(orderBy)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	var rows []*models.SentSplusMessage
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SentSplusMessageRepositoryImpl) Count(ctx context.Context, filter models.SentSplusMessageFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.SentSplusMessage{}), filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *SentSplusMessageRepositoryImpl) Exists(ctx context.Context, filter models.SentSplusMessageFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *SentSplusMessageRepositoryImpl) ListByProcessedCampaign(
	ctx context.Context,
	processedCampaignID uint,
	limit, offset int,
) ([]*models.SentSplusMessage, error) {
	filter := models.SentSplusMessageFilter{ProcessedCampaignID: &processedCampaignID}
	return r.ByFilter(ctx, filter, "id ASC", limit, offset)
}

func (r *SentSplusMessageRepositoryImpl) ListByTrackingIDs(
	ctx context.Context,
	processedCampaignID uint,
	trackingIDs []string,
) ([]*models.SentSplusMessage, error) {
	if len(trackingIDs) == 0 {
		return nil, nil
	}
	normalizedTrackingIDs := make([]string, 0, len(trackingIDs))
	seen := make(map[string]struct{}, len(trackingIDs))
	for _, raw := range trackingIDs {
		trackingID := strings.TrimSpace(raw)
		if trackingID == "" {
			continue
		}
		if _, exists := seen[trackingID]; exists {
			continue
		}
		seen[trackingID] = struct{}{}
		normalizedTrackingIDs = append(normalizedTrackingIDs, trackingID)
	}
	if len(normalizedTrackingIDs) == 0 {
		return nil, nil
	}
	db := r.getDB(ctx)
	rows := make([]*models.SentSplusMessage, 0, len(trackingIDs))
	if err := db.
		Where("processed_campaign_id = ? AND tracking_id IN ?", processedCampaignID, normalizedTrackingIDs).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SentSplusMessageRepositoryImpl) TrackingResultsFromSentRows(
	ctx context.Context,
	processedCampaignID uint,
) ([]SplusTrackingResult, error) {
	type fallbackRow struct {
		AudienceProfileUID *string `gorm:"column:audience_profile_uid"`
		PhoneNumber        string  `gorm:"column:phone_number"`
		TrackingID         string  `gorm:"column:tracking_id"`
		Status             string  `gorm:"column:status"`
		ServerID           *string `gorm:"column:server_id"`
		ErrorCode          *string `gorm:"column:error_code"`
		Description        *string `gorm:"column:description"`
	}

	db := r.getDB(ctx)
	rows := make([]fallbackRow, 0)
	if err := db.Table("sent_splus_messages AS ssm").
		Select(`
			ap.uid AS audience_profile_uid,
			COALESCE(ssm.phone_number, '') AS phone_number,
			ssm.tracking_id,
			ssm.status,
			ssm.server_id,
			ssm.error_code,
			ssm.description`).
		Joins(`
			INNER JOIN (
				SELECT tracking_id, MAX(id) AS latest_id
				FROM sent_splus_messages
				WHERE processed_campaign_id = ?
				GROUP BY tracking_id
			) AS latest
				ON latest.latest_id = ssm.id`, processedCampaignID).
		Joins(`LEFT JOIN audience_profiles AS ap ON ssm.phone_number <> '' AND ap.phone_number = ssm.phone_number`).
		Where("ssm.processed_campaign_id = ?", processedCampaignID).
		Order("ssm.tracking_id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	results := make([]SplusTrackingResult, 0, len(rows))
	for _, row := range rows {
		total := int64(1)
		delivered := int64(0)
		undelivered := int64(0)
		unknown := int64(0)
		status := strings.ToLower(strings.TrimSpace(row.Status))

		switch status {
		case string(models.SplusSendStatusSuccessful):
			delivered = 1
		case string(models.SplusSendStatusUnsuccessful):
			undelivered = 1
		case string(models.SplusSendStatusPending):
			unknown = 1
		default:
			unknown = 1
		}

		statusCopy := status
		if statusCopy == "" {
			statusCopy = string(models.SplusSendStatusPending)
		}
		results = append(results, SplusTrackingResult{
			AudienceProfileUID:    row.AudienceProfileUID,
			PhoneNumber:           row.PhoneNumber,
			TrackingID:            row.TrackingID,
			TotalParts:            &total,
			TotalDeliveredParts:   &delivered,
			TotalUndeliveredParts: &undelivered,
			TotalUnknownParts:     &unknown,
			Status:                &statusCopy,
			ServerID:              row.ServerID,
			ErrorCode:             row.ErrorCode,
			Description:           row.Description,
		})
	}
	return results, nil
}

func (r *SentSplusMessageRepositoryImpl) UpdateSendResultByTrackingIDs(
	ctx context.Context,
	updates []SentSplusSendResultUpdate,
) (err error) {
	if len(updates) == 0 {
		return nil
	}

	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}
	if shouldCommit {
		defer func() {
			if err != nil {
				_ = db.Rollback().Error
				return
			}
			if commitErr := db.Commit().Error; commitErr != nil {
				err = commitErr
			}
		}()
	}

	normalized := make([]SentSplusSendResultUpdate, 0, len(updates))
	indexByTrackingID := make(map[string]int, len(updates))
	for _, u := range updates {
		trackingID := strings.TrimSpace(u.TrackingID)
		if trackingID == "" {
			continue
		}
		if u.PartsDelivered < 0 {
			u.PartsDelivered = 0
		}
		switch strings.ToLower(strings.TrimSpace(string(u.Status))) {
		case string(models.SplusSendStatusSuccessful):
			u.Status = models.SplusSendStatusSuccessful
		case string(models.SplusSendStatusUnsuccessful):
			u.Status = models.SplusSendStatusUnsuccessful
		default:
			u.Status = models.SplusSendStatusPending
		}
		if u.ServerID != nil {
			serverID := strings.TrimSpace(*u.ServerID)
			if serverID == "" {
				u.ServerID = nil
			} else {
				u.ServerID = &serverID
			}
		}
		if u.ErrorCode != nil {
			errorCode := strings.TrimSpace(*u.ErrorCode)
			if errorCode == "" {
				u.ErrorCode = nil
			} else {
				u.ErrorCode = &errorCode
			}
		}
		u.TrackingID = trackingID
		if idx, ok := indexByTrackingID[trackingID]; ok {
			normalized[idx] = u
			continue
		}
		indexByTrackingID[trackingID] = len(normalized)
		normalized = append(normalized, u)
	}
	if len(normalized) == 0 {
		return nil
	}

	args := make([]any, 0, len(normalized)*11)
	buildCaseClause := func(column string, valueFn func(SentSplusSendResultUpdate) any) string {
		var b strings.Builder
		b.WriteString(column)
		b.WriteString(" = CASE tracking_id")
		for _, u := range normalized {
			b.WriteString(" WHEN ? THEN ?")
			args = append(args, u.TrackingID, valueFn(u))
		}
		b.WriteString(" ELSE ")
		b.WriteString(column)
		b.WriteString(" END")
		return b.String()
	}

	setClauses := []string{
		buildCaseClause("status", func(u SentSplusSendResultUpdate) any { return u.Status }),
		buildCaseClause("parts_delivered", func(u SentSplusSendResultUpdate) any { return u.PartsDelivered }),
		buildCaseClause("server_id", func(u SentSplusSendResultUpdate) any { return u.ServerID }),
		buildCaseClause("error_code", func(u SentSplusSendResultUpdate) any { return u.ErrorCode }),
		buildCaseClause("description", func(u SentSplusSendResultUpdate) any { return u.Description }),
		"updated_at = ?",
	}
	args = append(args, utils.UTCNow())

	trackingIDs := make([]string, 0, len(normalized))
	for _, u := range normalized {
		trackingIDs = append(trackingIDs, u.TrackingID)
	}
	args = append(args, trackingIDs, trackingIDs)

	query := "UPDATE sent_splus_messages SET " + strings.Join(setClauses, ", ") +
		" WHERE tracking_id IN ? AND id IN (" +
		"SELECT MAX(id) FROM sent_splus_messages WHERE tracking_id IN ? GROUP BY tracking_id)"
	err = db.Exec(query, args...).Error
	return err
}
