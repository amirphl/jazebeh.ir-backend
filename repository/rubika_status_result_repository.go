package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RubikaStatusResultRepositoryImpl implements RubikaStatusResultRepository.
type RubikaStatusResultRepositoryImpl struct {
	*BaseRepository[models.RubikaStatusResult, any]
}

type RubikaStatusAggregates struct {
	AggregatedTotalRecords   int64
	AggregatedTotalSent      int64
	AggregatedTotalParts     int64
	AggregatedDeliveredParts int64
	AggregatedUndelivered    int64
	AggregatedUnknown        int64
}

type RubikaTrackingResult struct {
	AudienceProfileUID    *string         `json:"audienceProfileUID"    gorm:"column:audience_profile_uid"`
	PhoneNumber           string          `json:"phoneNumber"           gorm:"column:phone_number"`
	TrackingID            string          `json:"trackingID"            gorm:"column:tracking_id"`
	TotalParts            *int64          `json:"totalParts"            gorm:"column:total_parts"`
	TotalDeliveredParts   *int64          `json:"totalDeliveredParts"   gorm:"column:total_delivered_parts"`
	TotalUndeliveredParts *int64          `json:"totalUndeliveredParts" gorm:"column:total_undelivered_parts"`
	TotalUnknownParts     *int64          `json:"totalUnknownParts"     gorm:"column:total_unknown_parts"`
	Status                *string         `json:"status"                gorm:"column:status"`
	ServerID              *string         `json:"serverID,omitempty"    gorm:"column:server_id"`
	ErrorCode             *string         `json:"errorCode,omitempty"   gorm:"column:error_code"`
	Description           *string         `json:"description,omitempty" gorm:"column:description"`
	Metadata              json.RawMessage `json:"metadata,omitempty"    gorm:"column:metadata"`
}

func NewRubikaStatusResultRepository(db *gorm.DB) RubikaStatusResultRepository {
	return &RubikaStatusResultRepositoryImpl{BaseRepository: NewBaseRepository[models.RubikaStatusResult, any](db)}
}

func (r *RubikaStatusResultRepositoryImpl) SaveBatch(ctx context.Context, rows []*models.RubikaStatusResult) error {
	if len(rows) == 0 {
		return nil
	}

	type aggKey struct {
		pcID       uint
		trackingID string
	}
	seen := make(map[aggKey]int, len(rows))
	deduped := make([]*models.RubikaStatusResult, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		trackingID := strings.TrimSpace(row.TrackingID)
		if trackingID == "" {
			continue
		}
		row.TrackingID = trackingID
		if len(row.Metadata) == 0 || !json.Valid(row.Metadata) {
			row.Metadata = json.RawMessage(`{}`)
		}
		key := aggKey{pcID: row.ProcessedCampaignID, trackingID: trackingID}
		if idx, exists := seen[key]; exists {
			deduped[idx] = row
			continue
		}
		seen[key] = len(deduped)
		deduped = append(deduped, row)
	}
	if len(deduped) == 0 {
		return nil
	}

	db := r.getDB(ctx)
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "processed_campaign_id"}, {Name: "tracking_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"job_id":                  clause.Expr{SQL: "EXCLUDED.job_id"},
			"server_id":               clause.Expr{SQL: "EXCLUDED.server_id"},
			"provider_status_code":    clause.Expr{SQL: "EXCLUDED.provider_status_code"},
			"provider_status_text":    clause.Expr{SQL: "EXCLUDED.provider_status_text"},
			"total_parts":             clause.Expr{SQL: "EXCLUDED.total_parts"},
			"total_delivered_parts":   clause.Expr{SQL: "EXCLUDED.total_delivered_parts"},
			"total_undelivered_parts": clause.Expr{SQL: "EXCLUDED.total_undelivered_parts"},
			"total_unknown_parts":     clause.Expr{SQL: "EXCLUDED.total_unknown_parts"},
			"status":                  clause.Expr{SQL: "EXCLUDED.status"},
			"metadata":                clause.Expr{SQL: "EXCLUDED.metadata"},
			"created_at":              clause.Expr{SQL: "LEAST(rubika_status_results.created_at, EXCLUDED.created_at)"},
		}),
	}).Create(&deduped).Error
}

func (r *RubikaStatusResultRepositoryImpl) ByFilter(
	ctx context.Context,
	_ any,
	orderBy string,
	limit, offset int,
) ([]*models.RubikaStatusResult, error) {
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
	var rows []*models.RubikaStatusResult
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *RubikaStatusResultRepositoryImpl) Count(ctx context.Context, _ any) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	if err := db.Model(&models.RubikaStatusResult{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *RubikaStatusResultRepositoryImpl) AggregateByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) (*RubikaStatusAggregates, error) {
	db := r.getDB(ctx)
	var agg RubikaStatusAggregates
	if err := db.Table("rubika_status_results").
		Select(`
			COUNT(*) AS aggregated_total_records,
			COALESCE(SUM(CASE WHEN total_parts = total_delivered_parts THEN 1 ELSE 0 END), 0) AS aggregated_total_sent,
			COALESCE(SUM(total_parts),0) AS aggregated_total_parts,
			COALESCE(SUM(total_delivered_parts),0) AS aggregated_delivered_parts,
			COALESCE(SUM(total_undelivered_parts),0) AS aggregated_undelivered,
			COALESCE(SUM(total_unknown_parts),0) AS aggregated_unknown`).
		Where("processed_campaign_id = ?", processedCampaignID).
		Scan(&agg).Error; err != nil {
		return nil, err
	}
	return &agg, nil
}

func (r *RubikaStatusResultRepositoryImpl) TrackingResultsByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) ([]RubikaTrackingResult, error) {
	db := r.getDB(ctx)
	trackingResults := make([]RubikaTrackingResult, 0)
	if err := db.Table("rubika_status_results AS rsr").
		Select(`
			ap.uid AS audience_profile_uid,
			COALESCE(srm.phone_number, '') AS phone_number,
			rsr.tracking_id,
			rsr.total_parts,
			rsr.total_delivered_parts,
			rsr.total_undelivered_parts,
			rsr.total_unknown_parts,
			COALESCE(NULLIF(BTRIM(rsr.metadata->>'normalizedStatus'), ''), srm.status::text, rsr.status) AS status,
			COALESCE(rsr.server_id, srm.server_id) AS server_id,
			srm.error_code,
			srm.description,
			rsr.metadata`).
		Joins(`
			LEFT JOIN LATERAL (
				SELECT phone_number, tracking_id, status, server_id, error_code, description
				FROM sent_rubika_messages AS srm
				WHERE srm.processed_campaign_id = rsr.processed_campaign_id
					AND srm.tracking_id = rsr.tracking_id
				ORDER BY srm.id DESC
				LIMIT 1
			) AS srm ON TRUE`).
		Joins(`
				LEFT JOIN audience_profiles AS ap
					ON srm.phone_number <> '' AND ap.phone_number = srm.phone_number`).
		Where("rsr.processed_campaign_id = ?", processedCampaignID).
		Order("rsr.tracking_id ASC").
		Scan(&trackingResults).Error; err != nil {
		return nil, err
	}
	return trackingResults, nil
}

func (r *RubikaStatusResultRepositoryImpl) Exists(ctx context.Context, filter any) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
