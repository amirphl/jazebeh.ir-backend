package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// SentRubikaMessageRepositoryImpl implements SentRubikaMessageRepository.
type SentRubikaMessageRepositoryImpl struct {
	*BaseRepository[models.SentRubikaMessage, models.SentRubikaMessageFilter]
}

func NewSentRubikaMessageRepository(db *gorm.DB) SentRubikaMessageRepository {
	return &SentRubikaMessageRepositoryImpl{BaseRepository: NewBaseRepository[models.SentRubikaMessage, models.SentRubikaMessageFilter](db)}
}

func (r *SentRubikaMessageRepositoryImpl) ByID(ctx context.Context, id uint) (*models.SentRubikaMessage, error) {
	db := r.getDB(ctx)
	var row models.SentRubikaMessage
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *SentRubikaMessageRepositoryImpl) ListByProcessedCampaign(
	ctx context.Context,
	processedCampaignID uint,
	limit, offset int,
) ([]*models.SentRubikaMessage, error) {
	filter := models.SentRubikaMessageFilter{ProcessedCampaignID: &processedCampaignID}
	return r.ByFilter(ctx, filter, "id ASC", limit, offset)
}

func (r *SentRubikaMessageRepositoryImpl) ListByTrackingIDs(
	ctx context.Context,
	processedCampaignID uint,
	trackingIDs []string,
) ([]*models.SentRubikaMessage, error) {
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
	rows := make([]*models.SentRubikaMessage, 0, len(trackingIDs))
	if err := db.
		Where("processed_campaign_id = ? AND tracking_id IN ?", processedCampaignID, normalizedTrackingIDs).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SentRubikaMessageRepositoryImpl) TrackingResultsFromSentRows(
	ctx context.Context,
	processedCampaignID uint,
) ([]RubikaTrackingResult, error) {
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
	if err := db.Table("sent_rubika_messages AS srm").
		Select(`
			ap.uid AS audience_profile_uid,
			COALESCE(srm.phone_number, '') AS phone_number,
			srm.tracking_id,
			srm.status,
			srm.server_id,
			srm.error_code,
			srm.description`).
		Joins(`
			INNER JOIN (
				SELECT tracking_id, MAX(id) AS latest_id
				FROM sent_rubika_messages
				WHERE processed_campaign_id = ?
				GROUP BY tracking_id
			) AS latest
				ON latest.latest_id = srm.id`, processedCampaignID).
		Joins(`LEFT JOIN audience_profiles AS ap ON srm.phone_number <> '' AND ap.phone_number = srm.phone_number`).
		Where("srm.processed_campaign_id = ?", processedCampaignID).
		Order("srm.tracking_id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	results := make([]RubikaTrackingResult, 0, len(rows))
	for _, row := range rows {
		total := int64(1)
		delivered := int64(0)
		undelivered := int64(0)
		unknown := int64(0)
		status := strings.ToLower(strings.TrimSpace(row.Status))

		switch status {
		case string(models.RubikaSendStatusSuccessful):
			delivered = 1
		case string(models.RubikaSendStatusUnsuccessful):
			undelivered = 1
		case string(models.RubikaSendStatusPending):
			unknown = 1
		default:
			unknown = 1
		}

		statusCopy := status
		if statusCopy == "" {
			statusCopy = string(models.RubikaSendStatusPending)
		}
		results = append(results, RubikaTrackingResult{
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

func (r *SentRubikaMessageRepositoryImpl) applyFilter(db *gorm.DB, f models.SentRubikaMessageFilter) *gorm.DB {
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

func (r *SentRubikaMessageRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.SentRubikaMessageFilter,
	orderBy string,
	limit, offset int,
) ([]*models.SentRubikaMessage, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.SentRubikaMessage{}), filter)
	if orderBy != "" {
		query = query.Order(orderBy)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	var rows []*models.SentRubikaMessage
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SentRubikaMessageRepositoryImpl) Count(ctx context.Context, filter models.SentRubikaMessageFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.SentRubikaMessage{}), filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *SentRubikaMessageRepositoryImpl) Exists(ctx context.Context, filter models.SentRubikaMessageFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *SentRubikaMessageRepositoryImpl) UpdateSendResultByTrackingIDs(
	ctx context.Context,
	updates []SentRubikaSendResultUpdate,
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

	normalized := make([]SentRubikaSendResultUpdate, 0, len(updates))
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
		case string(models.RubikaSendStatusSuccessful):
			u.Status = models.RubikaSendStatusSuccessful
		case string(models.RubikaSendStatusUnsuccessful):
			u.Status = models.RubikaSendStatusUnsuccessful
		default:
			u.Status = models.RubikaSendStatusPending
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
	buildCaseClause := func(column string, valueFn func(SentRubikaSendResultUpdate) any) string {
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
		buildCaseClause("status", func(u SentRubikaSendResultUpdate) any { return u.Status }),
		buildCaseClause("parts_delivered", func(u SentRubikaSendResultUpdate) any { return u.PartsDelivered }),
		buildCaseClause("server_id", func(u SentRubikaSendResultUpdate) any { return u.ServerID }),
		buildCaseClause("error_code", func(u SentRubikaSendResultUpdate) any { return u.ErrorCode }),
		buildCaseClause("description", func(u SentRubikaSendResultUpdate) any { return u.Description }),
		"updated_at = ?",
	}
	args = append(args, utils.UTCNow())

	trackingIDs := make([]string, 0, len(normalized))
	for _, u := range normalized {
		trackingIDs = append(trackingIDs, u.TrackingID)
	}
	args = append(args, trackingIDs, trackingIDs)

	query := "UPDATE sent_rubika_messages SET " + strings.Join(setClauses, ", ") +
		" WHERE tracking_id IN ? AND id IN (" +
		"SELECT MAX(id) FROM sent_rubika_messages WHERE tracking_id IN ? GROUP BY tracking_id)"
	err = db.Exec(query, args...).Error
	return err
}
