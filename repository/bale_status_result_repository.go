package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BaleStatusResultRepositoryImpl implements BaleStatusResultRepository.
type BaleStatusResultRepositoryImpl struct {
	*BaseRepository[models.BaleStatusResult, any]
}

type BaleStatusAggregates struct {
	AggregatedTotalRecords   int64
	AggregatedTotalSent      int64
	AggregatedTotalParts     int64
	AggregatedDeliveredParts int64
	AggregatedUndelivered    int64
	AggregatedUnknown        int64
}

type BaleTrackingResult struct {
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

func NewBaleStatusResultRepository(db *gorm.DB) BaleStatusResultRepository {
	return &BaleStatusResultRepositoryImpl{BaseRepository: NewBaseRepository[models.BaleStatusResult, any](db)}
}

func (r *BaleStatusResultRepositoryImpl) SaveBatch(ctx context.Context, rows []*models.BaleStatusResult) error {
	if len(rows) == 0 {
		return nil
	}

	type aggKey struct {
		pcID       uint
		trackingID string
	}
	seen := make(map[aggKey]int, len(rows))
	deduped := make([]*models.BaleStatusResult, 0, len(rows))
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
			"provider":                clause.Expr{SQL: "EXCLUDED.provider"},
			"provider_status_code":    clause.Expr{SQL: "EXCLUDED.provider_status_code"},
			"provider_status_text":    clause.Expr{SQL: "EXCLUDED.provider_status_text"},
			"total_parts":             clause.Expr{SQL: "EXCLUDED.total_parts"},
			"total_delivered_parts":   clause.Expr{SQL: "EXCLUDED.total_delivered_parts"},
			"total_undelivered_parts": clause.Expr{SQL: "EXCLUDED.total_undelivered_parts"},
			"total_unknown_parts":     clause.Expr{SQL: "EXCLUDED.total_unknown_parts"},
			"status":                  clause.Expr{SQL: "EXCLUDED.status"},
			"metadata":                clause.Expr{SQL: "EXCLUDED.metadata"},
			"created_at":              clause.Expr{SQL: "LEAST(bale_status_results.created_at, EXCLUDED.created_at)"},
		}),
	}).Create(&deduped).Error
}

// ByFilter: no filter fields, just order/limit/offset.
func (r *BaleStatusResultRepositoryImpl) ByFilter(
	ctx context.Context,
	_ any,
	orderBy string,
	limit, offset int,
) ([]*models.BaleStatusResult, error) {
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
	var rows []*models.BaleStatusResult
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *BaleStatusResultRepositoryImpl) Count(ctx context.Context, _ any) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	if err := db.Model(&models.BaleStatusResult{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *BaleStatusResultRepositoryImpl) AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*BaleStatusAggregates, error) {
	// TODO: Aggregate on status too.
	// TODO: Query optimization: maintain a summary table updated on insert instead of aggregating on the fly.
	db := r.getDB(ctx)
	var agg BaleStatusAggregates
	if err := db.Table("bale_status_results").
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

func (r *BaleStatusResultRepositoryImpl) TrackingResultsByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) ([]BaleTrackingResult, error) {
	db := r.getDB(ctx)
	trackingResults := make([]BaleTrackingResult, 0)
	if err := db.Table("bale_status_results AS bsr").
		Select(`
			ap.uid AS audience_profile_uid,
			COALESCE(sbm.phone_number, '') AS phone_number,
			bsr.tracking_id,
			bsr.total_parts,
			bsr.total_delivered_parts,
			bsr.total_undelivered_parts,
			bsr.total_unknown_parts,
			COALESCE(NULLIF(BTRIM(bsr.metadata->>'normalizedStatus'), ''), sbm.status::text, bsr.status) AS status,
			COALESCE(bsr.server_id, sbm.server_id) AS server_id,
			sbm.error_code,
			sbm.description,
			bsr.metadata`).
		Joins(`
			LEFT JOIN LATERAL (
				SELECT phone_number, tracking_id, status, server_id, error_code, description
				FROM sent_bale_messages AS sbm
				WHERE sbm.processed_campaign_id = bsr.processed_campaign_id
					AND sbm.tracking_id = bsr.tracking_id
					AND sbm.is_current
				ORDER BY sbm.id DESC
				LIMIT 1
			) AS sbm ON TRUE`).
		Joins(`
				LEFT JOIN audience_profiles AS ap
					ON sbm.phone_number <> '' AND ap.phone_number = sbm.phone_number`).
		Where("bsr.processed_campaign_id = ?", processedCampaignID).
		Order("bsr.tracking_id ASC").
		Scan(&trackingResults).Error; err != nil {
		return nil, err
	}
	return trackingResults, nil
}

func (r *BaleStatusResultRepositoryImpl) Exists(ctx context.Context, filter any) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
