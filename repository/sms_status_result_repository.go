package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keep each INSERT safely below PostgreSQL's extended-query bind-parameter
// ceiling. SMSStatusResult has several writable columns, so a single 25k-row
// GORM Create would otherwise expand to hundreds of thousands of parameters.
const smsStatusResultWriteBatchSize = 500

// SMSStatusResultRepositoryImpl implements SMSStatusResultRepository
type SMSStatusResultRepositoryImpl struct {
	*BaseRepository[models.SMSStatusResult, any]
}

type SMSStatusAggregates struct {
	AggregatedTotalRecords   int64
	AggregatedTotalSent      int64
	AggregatedTotalParts     int64
	AggregatedDeliveredParts int64
	AggregatedUndelivered    int64
	AggregatedUnknown        int64
}

type SMSTrackingResult struct {
	AudienceProfileUID    *string `json:"audienceProfileUID"    gorm:"column:audience_profile_uid"`
	PhoneNumber           string  `json:"phoneNumber"           gorm:"column:phone_number"`
	TrackingID            string  `json:"trackingID"            gorm:"column:tracking_id"`
	ServerID              *string `json:"serverID,omitempty"    gorm:"column:server_id"`
	TotalParts            *int64  `json:"totalParts"            gorm:"column:total_parts"`
	TotalDeliveredParts   *int64  `json:"totalDeliveredParts"   gorm:"column:total_delivered_parts"`
	TotalUndeliveredParts *int64  `json:"totalUndeliveredParts" gorm:"column:total_undelivered_parts"`
	TotalUnknownParts     *int64  `json:"totalUnknownParts"     gorm:"column:total_unknown_parts"`
	Status                *string `json:"status"                gorm:"column:status"`
}

func NewSMSStatusResultRepository(db *gorm.DB) SMSStatusResultRepository {
	return &SMSStatusResultRepositoryImpl{BaseRepository: NewBaseRepository[models.SMSStatusResult, any](db)}
}

func (r *SMSStatusResultRepositoryImpl) SaveBatch(ctx context.Context, rows []*models.SMSStatusResult) error {
	if len(rows) == 0 {
		return nil
	}

	// Deduplicate by conflict key to avoid ON CONFLICT hitting same row twice in one statement
	type aggKey struct {
		pcID       uint
		trackingID string
	}
	seen := make(map[aggKey]int, len(rows))
	deduped := make([]*models.SMSStatusResult, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		trackingID := strings.TrimSpace(row.TrackingID)
		if trackingID == "" {
			continue
		}
		if row.Provider == "" {
			row.Provider = models.SMSProviderPayamSMS
		}
		if len(row.Metadata) == 0 || !json.Valid(row.Metadata) {
			row.Metadata = json.RawMessage(`{}`)
		}
		row.TrackingID = trackingID
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
			"internal_status":         clause.Expr{SQL: "EXCLUDED.internal_status"},
			"total_parts":             clause.Expr{SQL: "EXCLUDED.total_parts"},
			"total_delivered_parts":   clause.Expr{SQL: "EXCLUDED.total_delivered_parts"},
			"total_undelivered_parts": clause.Expr{SQL: "EXCLUDED.total_undelivered_parts"},
			"total_unknown_parts":     clause.Expr{SQL: "EXCLUDED.total_unknown_parts"},
			"status":                  clause.Expr{SQL: "EXCLUDED.status"},
			"metadata":                clause.Expr{SQL: "EXCLUDED.metadata"},
			"created_at":              clause.Expr{SQL: "LEAST(sms_status_results.created_at, EXCLUDED.created_at)"},
		}),
	}).CreateInBatches(&deduped, smsStatusResultWriteBatchSize).Error
}

// ByFilter: no filter fields, just order/limit/offset
func (r *SMSStatusResultRepositoryImpl) ByFilter(
	ctx context.Context,
	_ any,
	orderBy string,
	limit, offset int,
) ([]*models.SMSStatusResult, error) {
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
	var rows []*models.SMSStatusResult
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SMSStatusResultRepositoryImpl) Count(ctx context.Context, _ any) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	if err := db.Model(&models.SMSStatusResult{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *SMSStatusResultRepositoryImpl) AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*SMSStatusAggregates, error) {
	// TODO: Aggregate on status too.
	// TODO: Query optimization: maintain a summary table updated on insert instead of aggregating on the fly.
	db := r.getDB(ctx)
	var agg SMSStatusAggregates
	if err := db.Table("sms_status_results").
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

func (r *SMSStatusResultRepositoryImpl) TrackingResultsByCampaign(
	ctx context.Context,
	processedCampaignID uint,
) ([]SMSTrackingResult, error) {
	db := r.getDB(ctx)
	trackingResults := make([]SMSTrackingResult, 0)
	if err := db.Table("sms_status_results AS ssr").
		Select(`
				ap.uid AS audience_profile_uid,
				COALESCE(ss.phone_number, '') AS phone_number,
				ssr.tracking_id,
				COALESCE(ssr.server_id, ss.server_id) AS server_id,
				ssr.total_parts,
				ssr.total_delivered_parts,
				ssr.total_undelivered_parts,
				ssr.total_unknown_parts,
				COALESCE(ssr.status, ss.status::text) AS status`).
		Joins(`
				LEFT JOIN LATERAL (
					SELECT phone_number, server_id, status
					FROM sent_sms AS ss
					WHERE ss.processed_campaign_id = ssr.processed_campaign_id
						AND ss.tracking_id = ssr.tracking_id
					ORDER BY ss.id DESC
					LIMIT 1
				) AS ss ON TRUE`).
		Joins(`
				LEFT JOIN audience_profiles AS ap
					ON ss.phone_number <> '' AND ap.phone_number = ss.phone_number`).
		Where("ssr.processed_campaign_id = ?", processedCampaignID).
		Order("ssr.tracking_id ASC").
		Scan(&trackingResults).Error; err != nil {
		return nil, err
	}
	return trackingResults, nil
}

func (r *SMSStatusResultRepositoryImpl) Exists(ctx context.Context, filter any) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
