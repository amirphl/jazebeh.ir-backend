package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CampaignRepositoryImpl implements the CampaignRepository interface
type CampaignRepositoryImpl struct {
	*BaseRepository[models.Campaign, models.CampaignFilter]
}

// NewCampaignRepository creates a new campaign repository
func NewCampaignRepository(db *gorm.DB) CampaignRepository {
	return &CampaignRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Campaign, models.CampaignFilter](db),
	}
}

// statisticsWithoutTrackingResults is the SELECT expression that returns all statistics
// keys except "trackingResults", which can be very large and is excluded from read queries.
const statisticsWithoutTrackingResults = "campaigns.id, campaigns.uuid, campaigns.customer_id, campaigns.hidden, campaigns.status, " +
	"campaigns.created_at, campaigns.updated_at, campaigns.spec, campaigns.comment, " +
	"(campaigns.statistics - 'trackingResults') AS statistics, campaigns.num_audience, " +
	"campaigns.bundle_id, campaigns.phase, campaigns.sample_size_per_tag, " +
	"campaigns.smart_targeting_test_satisfied_tag_ids, campaigns.smart_targeting_test_sampling_input_hash, " +
	"campaigns.smart_targeting_test_sampling_previewed_at, campaigns.smart_targeting_test_sampling_generation, " +
	"campaigns.active_smart_targeting_test_selection_id, " +
	// Campaigns returned by this projection are subsequently updated by status
	// transitions (for example, MoveCampaignToRunning). Keep every persisted
	// scalar field that Update/Save can write here: omitting the reservation
	// version makes GORM write its zero value and disconnect a valid frozen
	// Smart Targeting execution reservation from its campaign.
	"campaigns.smart_targeting_execution_reservation_version"

// ByID retrieves an campaign by ID
func (r *CampaignRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Campaign, error) {
	db := r.getDB(ctx)

	var campaign models.Campaign
	err := db.Select(statisticsWithoutTrackingResults).
		Preload("Customer").
		Preload("Customer.AccountType").
		Preload("Customer.ReferrerAgency").
		Preload("Bundle").
		Last(&campaign, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &campaign, nil
}

// ByUUID retrieves an campaign by UUID
func (r *CampaignRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.Campaign, error) {
	parsedUUID, err := utils.ParseUUID(uuid)
	if err != nil {
		return nil, err
	}

	filter := models.CampaignFilter{UUID: &parsedUUID}
	campaigns, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	if len(campaigns) == 0 {
		return nil, nil
	}

	return campaigns[0], nil
}

// ByCustomerID retrieves campaigns by customer ID with pagination
func (r *CampaignRepositoryImpl) ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.Campaign, error) {
	filter := models.CampaignFilter{CustomerID: &customerID}
	return r.ByFilter(ctx, filter, "created_at DESC", limit, offset)
}

func (r *CampaignRepositoryImpl) ByCustomerIDAndIDs(ctx context.Context, customerID uint, campaignIDs []uint) ([]*models.Campaign, error) {
	if len(campaignIDs) == 0 {
		return []*models.Campaign{}, nil
	}

	db := r.getDB(ctx)
	var campaigns []*models.Campaign
	err := db.Model(&models.Campaign{}).
		Select(statisticsWithoutTrackingResults).
		Where("customer_id = ?", customerID).
		Where("id IN ?", campaignIDs).
		Find(&campaigns).Error
	if err != nil {
		return nil, err
	}

	return campaigns, nil
}

// ByCustomerIDAndBundleIDs retrieves campaigns for a customer that belong to any
// of the provided bundle IDs. Only fields needed by bundle-level aggregation are selected.
func (r *CampaignRepositoryImpl) ByCustomerIDAndBundleIDs(ctx context.Context, customerID uint, bundleIDs []uint) ([]*models.Campaign, error) {
	if len(bundleIDs) == 0 {
		return []*models.Campaign{}, nil
	}

	db := r.getDB(ctx)
	var campaigns []*models.Campaign
	err := db.Model(&models.Campaign{}).
		Select("id", "bundle_id", "phase", "statistics").
		Where("customer_id = ?", customerID).
		Where("bundle_id IN ?", bundleIDs).
		Find(&campaigns).Error
	if err != nil {
		return nil, err
	}

	return campaigns, nil
}

// ByStatus retrieves campaigns by status with pagination
func (r *CampaignRepositoryImpl) ByStatus(ctx context.Context, status models.CampaignStatus, limit, offset int) ([]*models.Campaign, error) {
	filter := models.CampaignFilter{Status: &status}
	return r.ByFilter(ctx, filter, "created_at DESC", limit, offset)
}

// Update updates an campaign
func (r *CampaignRepositoryImpl) Update(ctx context.Context, campaign models.Campaign) error {
	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}

	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				db.Commit()
			}
		}()
	}

	// Set updated_at timestamp
	now := utils.UTCNow()
	campaign.UpdatedAt = &now

	err = db.Save(&campaign).Error
	if err != nil {
		return err
	}

	return nil
}

func (r *CampaignRepositoryImpl) UpdateStatistics(ctx context.Context, id uint, stats json.RawMessage) error {
	db := r.getDB(ctx)
	return db.Model(&models.Campaign{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"statistics": stats,
			"updated_at": utils.UTCNow(),
		}).Error
}

// AppendTrackingResults appends items to the trackingResults array inside statistics
// without reading or rewriting the rest of the JSON, avoiding driver errors on large payloads.
func (r *CampaignRepositoryImpl) AppendTrackingResults(ctx context.Context, id uint, items json.RawMessage) error {
	db := r.getDB(ctx)
	return db.Exec(
		`UPDATE campaigns
		 SET statistics = jsonb_set(
		     COALESCE(statistics, '{}'),
		     '{trackingResults}',
		     COALESCE(statistics->'trackingResults', '[]') || ?::jsonb,
		     true
		 ),
		 updated_at = ?
		 WHERE id = ?`,
		string(items), utils.UTCNow(), id,
	).Error
}

// UpdateStatus updates only the status of an campaign
func (r *CampaignRepositoryImpl) UpdateStatus(ctx context.Context, id uint, status models.CampaignStatus) error {
	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}

	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				db.Commit()
			}
		}()
	}

	now := utils.UTCNow()
	err = db.Model(&models.Campaign{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     status,
			"updated_at": now,
		}).Error

	if err != nil {
		return err
	}

	return nil
}

func (r *CampaignRepositoryImpl) MarkHidden(ctx context.Context, customerID uint, campaignIDs []uint) (int64, error) {
	if len(campaignIDs) == 0 {
		return 0, nil
	}

	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return 0, err
	}

	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				db.Commit()
			}
		}()
	}

	result := db.Model(&models.Campaign{}).
		Where("customer_id = ?", customerID).
		Where("id IN ?", campaignIDs).
		Updates(map[string]any{
			"hidden":     true,
			"updated_at": utils.UTCNow(),
		})
	err = result.Error
	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

func (r *CampaignRepositoryImpl) MarkVisible(ctx context.Context, customerID uint, campaignIDs []uint) (int64, error) {
	if len(campaignIDs) == 0 {
		return 0, nil
	}

	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return 0, err
	}

	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				db.Commit()
			}
		}()
	}

	result := db.Model(&models.Campaign{}).
		Where("customer_id = ?", customerID).
		Where("id IN ?", campaignIDs).
		Updates(map[string]any{
			"hidden":     false,
			"updated_at": utils.UTCNow(),
		})
	err = result.Error
	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// CountByCustomerID counts campaigns by customer ID
func (r *CampaignRepositoryImpl) CountByCustomerID(ctx context.Context, customerID uint) (int, error) {
	filter := models.CampaignFilter{CustomerID: &customerID}
	count, err := r.Count(ctx, filter)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// CountByStatus counts campaigns by status
func (r *CampaignRepositoryImpl) CountByStatus(ctx context.Context, status models.CampaignStatus) (int, error) {
	filter := models.CampaignFilter{Status: &status}
	count, err := r.Count(ctx, filter)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// GetPendingApproval retrieves campaigns waiting for approval
func (r *CampaignRepositoryImpl) GetPendingApproval(ctx context.Context, limit, offset int) ([]*models.Campaign, error) {
	status := models.CampaignStatusWaitingForApproval
	filter := models.CampaignFilter{Status: &status}
	return r.ByFilter(ctx, filter, "created_at ASC", limit, offset)
}

// GetScheduledCampaigns retrieves campaigns scheduled between the given times
func (r *CampaignRepositoryImpl) GetScheduledCampaigns(ctx context.Context, from, to time.Time) ([]*models.Campaign, error) {
	filter := models.CampaignFilter{
		ScheduleAfter:  &from,
		ScheduleBefore: &to,
	}
	return r.ByFilter(ctx, filter, "schedule_at ASC", 0, 0)
}

func excludeAutomatedClickTraffic(db *gorm.DB) *gorm.DB {
	return db.
		Where("COALESCE(is_test, FALSE) = FALSE").
		Where("COALESCE(ip, '') !~ ?", "^(66\\.249\\.|74\\.125\\.)").
		Where(`NOT (
			COALESCE(user_agent, '') ~ 'Chrome'
			AND COALESCE(user_agent, '') !~ '(Edg|OPR|Opera)'
			AND (
				COALESCE(user_agent, '') ~* 'X11; Linux|Linux'
				AND COALESCE(user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'
			)
		)`)
}

// // ClickCounts returns a map of campaign_id -> distinct short_link_click uids
// func (r *CampaignRepositoryImpl) AggregateClickCountsByCampaignIDs(ctx context.Context, campaignIDs []uint) (map[uint]int64, error) {
// 	out := make(map[uint]int64)
// 	if len(campaignIDs) == 0 {
// 		return out, nil
// 	}
// 	type row struct {
// 		CampaignID uint
// 		Clicks     int64
// 	}
// 	var rows []row
// 	db := r.getDB(ctx)
// 	if err := db.Table("short_link_clicks").
// 		Select("campaign_id, COUNT(DISTINCT uid) AS clicks").
// 		Where("campaign_id IN ?", campaignIDs).
// 		Where("COALESCE(ip, '') !~ ?", "^(66\\.249\\.|74\\.125\\.)").
// 		Where(`NOT (
// 			COALESCE(user_agent, '') ~ 'Chrome'
// 			AND COALESCE(user_agent, '') !~ '(Edg|OPR|Opera)'
// 			AND (
// 				COALESCE(user_agent, '') ~* 'X11; Linux|Linux'
// 				AND COALESCE(user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'
// 			)
// 		)`).
// 		Group("campaign_id").
// 		Scan(&rows).Error; err != nil {
// 		return nil, err
// 	}
// 	for _, r := range rows {
// 		out[r.CampaignID] = r.Clicks
// 	}
// 	return out, nil
// }

// AggregateClickCountsByCampaignIDs returns a map of campaign_id -> distinct clicking audience count.
func (r *CampaignRepositoryImpl) AggregateClickCountsByCampaignIDs(ctx context.Context, campaignIDs []uint) (map[uint]int64, error) {
	out := make(map[uint]int64)
	if len(campaignIDs) == 0 {
		return out, nil
	}
	type row struct {
		CampaignID uint
		Clicks     int64
	}
	var rows []row
	db := excludeAutomatedClickTraffic(r.getDB(ctx))
	if err := db.Table("short_link_clicks").
		Select("campaign_id, COUNT(DISTINCT uid) AS clicks").
		Where("campaign_id IN ?", campaignIDs).
		Group("campaign_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CampaignID] = r.Clicks
	}
	return out, nil
}

// // AggregateClickCountsByCustomerIDs returns a map of customer_id -> distinct short_link_click uids.
// func (r *CampaignRepositoryImpl) AggregateClickCountsByCustomerIDs(ctx context.Context, customerIDs []uint) (map[uint]int64, error) {
// 	out := make(map[uint]int64)
// 	if len(customerIDs) == 0 {
// 		return out, nil
// 	}

// 	type row struct {
// 		CustomerID uint
// 		Clicks     int64
// 	}
// 	var rows []row
// 	db := r.getDB(ctx)
// 	if err := db.Table("short_link_clicks sc").
// 		Select("c.customer_id, COUNT(DISTINCT sc.uid) AS clicks").
// 		Joins("JOIN campaigns c ON c.id = sc.campaign_id").
// 		Where("c.customer_id IN ?", customerIDs).
// 		Where("COALESCE(sc.ip, '') !~ ?", "^(66\\.249\\.|74\\.125\\.)").
// 		Where(`NOT (
// 			COALESCE(sc.user_agent, '') ~ 'Chrome'
// 			AND COALESCE(sc.user_agent, '') !~ '(Edg|OPR|Opera)'
// 			AND (
// 				COALESCE(sc.user_agent, '') ~* 'X11; Linux|Linux'
// 				AND COALESCE(sc.user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'
// 			)
// 		)`).
// 		Group("c.customer_id").
// 		Scan(&rows).Error; err != nil {
// 		return nil, err
// 	}
// 	for _, r := range rows {
// 		out[r.CustomerID] = r.Clicks
// 	}
// 	return out, nil
// }

// AggregateClickCountsByCustomerIDs returns a map of customer_id -> sum of per-campaign distinct clicking audience counts.
func (r *CampaignRepositoryImpl) AggregateClickCountsByCustomerIDs(ctx context.Context, customerIDs []uint) (map[uint]int64, error) {
	out := make(map[uint]int64)
	if len(customerIDs) == 0 {
		return out, nil
	}

	type row struct {
		CustomerID uint
		Clicks     int64
	}
	var rows []row
	db := r.getDB(ctx)
	if err := db.Table("campaigns c").
		Select("c.customer_id, COALESCE(SUM(campaign_clicks.clicks), 0) AS clicks").
		Joins(`JOIN (
			SELECT campaign_id, COUNT(DISTINCT uid) AS clicks
			FROM short_link_clicks
			WHERE campaign_id IS NOT NULL
			  AND COALESCE(is_test, FALSE) = FALSE
			  AND COALESCE(ip, '') !~ '^(66\\.249\\.|74\\.125\\.)'
			  AND NOT (
				COALESCE(user_agent, '') ~ 'Chrome'
				AND COALESCE(user_agent, '') !~ '(Edg|OPR|Opera)'
				AND (
					COALESCE(user_agent, '') ~* 'X11; Linux|Linux'
					AND COALESCE(user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'
				)
			  )
			GROUP BY campaign_id
		) AS campaign_clicks ON campaign_clicks.campaign_id = c.id`).
		Where("c.customer_id IN ?", customerIDs).
		Group("c.customer_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CustomerID] = r.Clicks
	}
	return out, nil
}

// AggregateTotalSentByCustomerIDs sums the totalSent statistic across all campaigns for the given customers.
// It returns a map keyed by customer_id with the aggregated totalSent value (0 when missing).
func (r *CampaignRepositoryImpl) AggregateTotalSentByCustomerIDs(ctx context.Context, customerIDs []uint) (map[uint]uint64, error) {
	results := make(map[uint]uint64)
	if len(customerIDs) == 0 {
		return results, nil
	}

	type row struct {
		CustomerID uint   `json:"customer_id"`
		TotalSent  uint64 `json:"total_sent"`
	}

	db := r.getDB(ctx)
	var rows []row
	err := db.Table("campaigns").
		Select(`customer_id, COALESCE(SUM(
			COALESCE(
				NULLIF(statistics->>'aggregatedTotalSent', '')::bigint,
				NULLIF(statistics->>'totalSent', '')::bigint,
				0
			)
		), 0) AS total_sent`).
		Where("customer_id IN ?", customerIDs).
		Group("customer_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		results[row.CustomerID] = row.TotalSent
	}

	return results, nil
}

// ByFilter retrieves campaigns based on filter criteria
func (r *CampaignRepositoryImpl) ByFilter(ctx context.Context, filter models.CampaignFilter, orderBy string, limit, offset int) ([]*models.Campaign, error) {
	db := r.getDB(ctx)

	var campaigns []*models.Campaign
	query := r.applyFilter(db, filter)

	// Apply ordering
	if orderBy != "" {
		query = r.applyOrder(query, orderBy)
	}

	// Apply pagination
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	// Preload relationships and exclude trackingResults from statistics
	query = query.Select(statisticsWithoutTrackingResults).
		Preload("Customer").
		Preload("Customer.AccountType").
		Preload("Customer.ReferrerAgency").
		Preload("Bundle")

	err := query.Find(&campaigns).Error
	if err != nil {
		return nil, err
	}

	return campaigns, nil
}

// scheduleAtSecondaryOrder is the secondary ORDER BY appended after every
// explicit primary sort in ListCampaigns: active campaigns last, then
// campaigns without a schedule, then upcoming soonest-first, then past
// most-recent-first.
const scheduleAtSecondaryOrder = `
	CASE WHEN campaigns.status IN ('initiated', 'in-progress') THEN 1 ELSE 0 END ASC,
	CASE WHEN campaigns.spec->>'schedule_at' IS NULL OR campaigns.spec->>'schedule_at' = '' THEN 0 ELSE 1 END ASC,
	CASE WHEN campaigns.spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (campaigns.spec->>'schedule_at')::timestamptz >= NOW() THEN 0 ELSE 1 END ASC,
	CASE WHEN campaigns.spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (campaigns.spec->>'schedule_at')::timestamptz >= NOW() THEN (campaigns.spec->>'schedule_at')::timestamptz END ASC NULLS LAST,
	CASE WHEN campaigns.spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (campaigns.spec->>'schedule_at')::timestamptz < NOW() THEN (campaigns.spec->>'schedule_at')::timestamptz END DESC NULLS LAST`

func (r *CampaignRepositoryImpl) applyOrder(query *gorm.DB, orderBy string) *gorm.DB {
	switch strings.TrimSpace(orderBy) {
	case "default_schedule_at":
		return query.Order(scheduleAtSecondaryOrder)
	case "oldest":
		return query.Order("campaigns.updated_at ASC").Order(scheduleAtSecondaryOrder)
	case "newest":
		return query.Order("campaigns.updated_at DESC").Order(scheduleAtSecondaryOrder)
	case "phase_test_first":
		return query.Order(`
			CASE campaigns.phase
				WHEN 'test' THEN 0
				WHEN 'execution' THEN 1
				ELSE 2
			END ASC`).Order(scheduleAtSecondaryOrder)
	case "phase_execution_first":
		return query.Order(`
			CASE campaigns.phase
				WHEN 'execution' THEN 0
				WHEN 'test' THEN 1
				ELSE 2
			END ASC`).Order(scheduleAtSecondaryOrder)
	case "highest_click_rate":
		return r.withClickRateOrdering(query, true)
	case "lowest_click_rate":
		return r.withClickRateOrdering(query, false)
	default:
		return query.Order(orderBy)
	}
}

func (r *CampaignRepositoryImpl) withClickRateOrdering(query *gorm.DB, descending bool) *gorm.DB {
	direction := "ASC"
	nulls := "NULLS FIRST"
	if descending {
		direction = "DESC"
		nulls = "NULLS LAST"
	}

	query = query.Joins(`LEFT JOIN (
		SELECT campaign_id, COUNT(DISTINCT uid) AS clicks
		FROM short_link_clicks
		WHERE campaign_id IS NOT NULL
		  AND COALESCE(is_test, FALSE) = FALSE
		  AND COALESCE(ip, '') !~ '^(66\.249\.|74\.125\.)'
		  AND NOT (
			COALESCE(user_agent, '') ~ 'Chrome'
			AND COALESCE(user_agent, '') !~ '(Edg|OPR|Opera)'
			AND (
				COALESCE(user_agent, '') ~* 'X11; Linux|Linux'
				AND COALESCE(user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'
			)
		  )
		GROUP BY campaign_id
	) AS campaign_click_rates ON campaign_click_rates.campaign_id = campaigns.id`)

	clickRateExpr := `CASE
		WHEN COALESCE(NULLIF(campaigns.statistics->>'aggregatedTotalSent', '')::double precision, 0) > 0
		THEN COALESCE(campaign_click_rates.clicks, 0)::double precision /
			COALESCE(NULLIF(campaigns.statistics->>'aggregatedTotalSent', '')::double precision, 0)
		ELSE NULL
	END`

	return query.Order(clause.Expr{
		SQL: fmt.Sprintf("%s %s", clickRateExpr, direction+" "+nulls),
	}).Order(scheduleAtSecondaryOrder)
}

// Count returns the number of campaigns matching the filter
func (r *CampaignRepositoryImpl) Count(ctx context.Context, filter models.CampaignFilter) (int64, error) {
	db := r.getDB(ctx)

	var count int64
	var campaign models.Campaign
	query := r.applyFilter(db.Model(&campaign), filter)

	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if any campaign matching the filter exists
func (r *CampaignRepositoryImpl) Exists(ctx context.Context, filter models.CampaignFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// applyFilter applies filter conditions to the GORM query
func (r *CampaignRepositoryImpl) applyFilter(db *gorm.DB, filter models.CampaignFilter) *gorm.DB {
	if filter.ID != nil {
		db = db.Where("campaigns.id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		db = db.Where("campaigns.uuid = ?", *filter.UUID)
	}
	if filter.CustomerID != nil {
		db = db.Where("campaigns.customer_id = ?", *filter.CustomerID)
	}
	if filter.Hidden != nil {
		db = db.Where("campaigns.hidden = ?", *filter.Hidden)
	}
	if filter.Status != nil {
		db = db.Where("campaigns.status = ?", *filter.Status)
	}
	if filter.CampaignTitle != nil {
		db = db.Where("campaigns.spec->>'title' ILIKE ?", "%"+*filter.CampaignTitle+"%")
	}
	if filter.BundleTitle != nil {
		db = db.Joins("LEFT JOIN bundles ON bundles.id = campaigns.bundle_id").
			Where("bundles.title ILIKE ?", "%"+*filter.BundleTitle+"%")
	}
	if filter.CustomerName != nil {
		searchTerm := "%" + *filter.CustomerName + "%"
		db = db.Joins("LEFT JOIN customers ON customers.id = campaigns.customer_id").
			Where(`(
				customers.representative_first_name ILIKE ?
				OR customers.representative_last_name ILIKE ?
				OR customers.company_name ILIKE ?
			)`, searchTerm, searchTerm, searchTerm)
	}
	if filter.Level1 != nil {
		db = db.Where("spec->>'level1' = ?", *filter.Level1)
	}
	if filter.Sex != nil {
		db = db.Where("spec->>'sex' = ?", *filter.Sex)
	}
	if filter.City != nil {
		db.Where("spec->'city' @> ?::jsonb", fmt.Sprintf(`["%s"]`, *filter.City))
	}
	if filter.LineNumber != nil {
		db = db.Where("spec->>'line_number' = ?", *filter.LineNumber)
	}
	if filter.MediaUUID != nil {
		db = db.Where("(COALESCE(spec->>'media_uuid', spec->>'mediaUuid')) = ?", filter.MediaUUID.String())
	}
	if filter.PlatformSettingsID != nil {
		db = db.Where("(COALESCE(spec->>'platform_settings_id', spec->>'platformSettingsId'))::bigint = ?", *filter.PlatformSettingsID)
	}
	if filter.Platform != nil {
		db = db.Where("COALESCE(NULLIF(spec->>'platform', ''), ?) = ?", models.CampaignPlatformSMS, *filter.Platform)
	}
	if filter.CreatedAfter != nil {
		db = db.Where("campaigns.created_at >= ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		db = db.Where("campaigns.created_at <= ?", *filter.CreatedBefore)
	}
	if filter.UpdatedAfter != nil {
		db = db.Where("campaigns.updated_at > ?", *filter.UpdatedAfter)
	}
	if filter.UpdatedBefore != nil {
		db = db.Where("campaigns.updated_at < ?", *filter.UpdatedBefore)
	}
	if filter.ScheduleAfter != nil {
		db = db.Where("(spec->>'schedule_at')::timestamptz > ?", *filter.ScheduleAfter)
	}
	if filter.ScheduleBefore != nil {
		db = db.Where("(spec->>'schedule_at')::timestamptz < ?", *filter.ScheduleBefore)
	}
	if filter.MinBudget != nil {
		db = db.Where("CAST(spec->>'budget' AS BIGINT) >= ?", *filter.MinBudget)
	}
	if filter.MaxBudget != nil {
		db = db.Where("CAST(spec->>'budget' AS BIGINT) <= ?", *filter.MaxBudget)
	}
	if filter.BundleID != nil {
		db = db.Where("campaigns.bundle_id = ?", *filter.BundleID)
	}
	if filter.Phase != nil {
		db = db.Where("campaigns.phase = ?", *filter.Phase)
	}

	return db
}
