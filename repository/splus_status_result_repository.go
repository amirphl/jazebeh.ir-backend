package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SplusStatusResultRepositoryImpl implements SplusStatusResultRepository.
type SplusStatusResultRepositoryImpl struct {
	*BaseRepository[models.SplusStatusResult, any]
}

type SplusStatusAggregates struct {
	AggregatedTotalRecords   int64
	AggregatedTotalSent      int64
	AggregatedTotalParts     int64
	AggregatedDeliveredParts int64
	AggregatedUndelivered    int64
	AggregatedUnknown        int64
}

type SplusTrackingResult struct {
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
	Metadata              json.RawMessage `json:"metadata"              gorm:"column:metadata;type:jsonb"`
}

func NewSplusStatusResultRepository(db *gorm.DB) SplusStatusResultRepository {
	return &SplusStatusResultRepositoryImpl{BaseRepository: NewBaseRepository[models.SplusStatusResult, any](db)}
}

func (r *SplusStatusResultRepositoryImpl) SaveBatch(ctx context.Context, rows []*models.SplusStatusResult) error {
	if len(rows) == 0 {
		return nil
	}

	type aggKey struct {
		pcID       uint
		trackingID string
	}
	seen := make(map[aggKey]int, len(rows))
	deduped := make([]*models.SplusStatusResult, 0, len(rows))
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
			// Keep the latest row for a duplicate conflict key in the same batch.
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
			"created_at":              clause.Expr{SQL: "LEAST(splus_status_results.created_at, EXCLUDED.created_at)"},
		}),
	}).Create(&deduped).Error
}

// ByFilter: no filter fields, just order/limit/offset.
func (r *SplusStatusResultRepositoryImpl) ByFilter(
	ctx context.Context,
	_ any,
	orderBy string,
	limit, offset int,
) ([]*models.SplusStatusResult, error) {
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
	var rows []*models.SplusStatusResult
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SplusStatusResultRepositoryImpl) Count(ctx context.Context, _ any) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	if err := db.Model(&models.SplusStatusResult{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *SplusStatusResultRepositoryImpl) AggregateByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) (*SplusStatusAggregates, error) {
	// TODO: Aggregate on status too.
	// TODO: Query optimization: maintain a summary table updated on insert instead of aggregating on the fly.
	db := r.getDB(ctx)
	var agg SplusStatusAggregates
	if err := db.Table("splus_status_results").
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

func (r *SplusStatusResultRepositoryImpl) TrackingResultsByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) ([]SplusTrackingResult, error) {
	db := r.getDB(ctx)
	trackingResults := make([]SplusTrackingResult, 0)
	if err := db.Table("splus_status_results AS ssr").
		Select(`
			ap.uid AS audience_profile_uid,
			COALESCE(ssm.phone_number, '') AS phone_number,
			ssr.tracking_id,
			ssr.total_parts,
			ssr.total_delivered_parts,
			ssr.total_undelivered_parts,
			ssr.total_unknown_parts,
			COALESCE(NULLIF(BTRIM(ssr.metadata->>'normalizedStatus'), ''), ssm.status::text, ssr.status) AS status,
			COALESCE(ssr.server_id, ssm.server_id) AS server_id,
			ssm.error_code,
			ssm.description,
			ssr.metadata`).
		Joins(`
			LEFT JOIN LATERAL (
				SELECT phone_number, tracking_id, status, server_id, error_code, description
				FROM sent_splus_messages AS ssm
				WHERE ssm.processed_campaign_id = ssr.processed_campaign_id
					AND ssm.tracking_id = ssr.tracking_id
				ORDER BY ssm.id DESC
				LIMIT 1
			) AS ssm ON TRUE`).
		Joins(`
				LEFT JOIN audience_profiles AS ap
					ON ssm.phone_number <> '' AND ap.phone_number = ssm.phone_number`).
		Where("ssr.processed_campaign_id = ?", processedCampaignID).
		Order("ssr.tracking_id ASC").
		Scan(&trackingResults).Error; err != nil {
		return nil, err
	}
	return trackingResults, nil
}

func (r *SplusStatusResultRepositoryImpl) Exists(ctx context.Context, filter any) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
