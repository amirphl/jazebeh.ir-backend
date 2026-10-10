package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidCampaignSelectedTags = errors.New("one or more selected tags are unavailable for the bundle's targeting source")
var ErrCampaignSelectedTagsNotEditable = errors.New("campaign selected tags cannot be edited in the current state")

type CampaignSelectedTagRepositoryImpl struct {
	db *gorm.DB
}

type campaignSelectedTagSnapshot struct {
	TagID           uint
	DisplayTitle    *string
	AudienceCount   *int64
	BundleFitScore  *float64
	TestPhaseAvgCTR *float64
	OverallAvgCTR   *float64
}

func invalidateCampaignSmartTargetingTestPreview(db *gorm.DB, campaignID uint) error {
	return db.Model(&models.Campaign{}).Where("id = ?", campaignID).Updates(map[string]any{
		"smart_targeting_test_satisfied_tag_ids":     gorm.Expr("ARRAY[]::integer[]"),
		"smart_targeting_test_sampling_input_hash":   nil,
		"smart_targeting_test_sampling_previewed_at": nil,
		// This selection is the concrete output of the invalidated preview. Clear
		// the pointer with the preview fields so the campaign cannot retain a
		// stale active selection reference.
		"active_smart_targeting_test_selection_id": nil,
		"num_audience": uint64(0),
		"updated_at":   utils.UTCNow(),
	}).Error
}

func NewCampaignSelectedTagRepository(db *gorm.DB) CampaignSelectedTagRepository {
	return &CampaignSelectedTagRepositoryImpl{db: db}
}

func (r *CampaignSelectedTagRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func applySmartTagSearch(query *gorm.DB, search string) *gorm.DB {
	if search == "" {
		return query
	}
	return query.Where(
		`(COALESCE(available_tags.tag_display_title, '') ILIKE ? ESCAPE '\'
		  OR COALESCE(available_tags.tag_name, '') ILIKE ? ESCAPE '\')`,
		"%"+escapeLike(search)+"%",
		"%"+escapeLike(search)+"%",
	)
}

func smartTagOrder(sortBy, direction string) (string, error) {
	direction = strings.ToUpper(direction)
	if direction != "ASC" && direction != "DESC" {
		return "", fmt.Errorf("invalid sort direction")
	}

	var expression string
	switch sortBy {
	case "database_order":
		return "available_tags.tag_id ASC", nil
	case "execution_default":
		if direction != "DESC" {
			return "", fmt.Errorf("invalid execution default sort direction")
		}
		return `tag_test_summary.test_phase_avg_ctr DESC NULLS LAST,
available_tags.bundle_persona_fit_score DESC NULLS LAST,
available_tags.tag_id ASC`, nil
	case "tag_capacity":
		expression = "available_tags.tag_audience_count"
	case "bundle_persona_fit_score":
		expression = "available_tags.bundle_persona_fit_score"
	case "test_phase_avg_ctr":
		expression = "tag_test_summary.test_phase_avg_ctr"
	case "overall_avg_ctr":
		expression = "tag_overall_summary.overall_avg_ctr"
	default:
		return "", fmt.Errorf("invalid sort field")
	}
	return expression + " " + direction + " NULLS LAST, available_tags.tag_id ASC", nil
}

// availableSmartTagsQuery exposes exactly one coherent tag source per bundle.
// A completed evaluation snapshot is authoritative when it contains scores;
// otherwise the query falls back to the active live tag catalog. The fallback
// is deliberately all-or-nothing so a partial merge cannot combine tag
// metadata captured at different points in time.
func availableSmartTagsQuery(db *gorm.DB, bundleID uint) *gorm.DB {
	return db.Table(`(
		SELECT
			scores.tag_id,
			COALESCE(scores.tag_name_snapshot, '') AS tag_name,
			scores.tag_display_title_snapshot AS tag_display_title,
			scores.tag_persona_snapshot AS tag_audience_persona,
			scores.tag_audience_count_snapshot AS tag_audience_count,
			scores.bundle_fit_score AS bundle_persona_fit_score,
			scores.evaluation_run_id,
			scores.fit_level,
			scores.relation_type,
			scores.reason
		FROM current_bundle_tag_scores AS scores
		JOIN tags AS scored_tags
		  ON scored_tags.id = scores.tag_id
		 AND scored_tags.is_active = TRUE
		WHERE scores.bundle_id = ?

		UNION ALL

		SELECT
			tags.id AS tag_id,
			tags.name AS tag_name,
			tags.display_title AS tag_display_title,
			tags.audience_persona AS tag_audience_persona,
			tags.audience_count AS tag_audience_count,
			NULL::numeric AS bundle_persona_fit_score,
			NULL::bigint AS evaluation_run_id,
			NULL::text AS fit_level,
			NULL::text AS relation_type,
			NULL::text AS reason
		FROM tags
		WHERE tags.is_active = TRUE
		  AND NOT EXISTS (
			  SELECT 1
			  FROM current_bundle_tag_scores AS existing_scores
			  JOIN tags AS existing_tags
			    ON existing_tags.id = existing_scores.tag_id
			   AND existing_tags.is_active = TRUE
			  WHERE existing_scores.bundle_id = ?
		  )
	) AS available_tags`, bundleID, bundleID)
}

func (r *CampaignSelectedTagRepositoryImpl) baseAvailableQuery(ctx context.Context, bundleID uint, search string) *gorm.DB {
	query := availableSmartTagsQuery(r.getDB(ctx), bundleID).
		Joins(`LEFT JOIN tag_test_phase_performance_summaries AS tag_test_summary
                 ON tag_test_summary.bundle_id = ?
				AND tag_test_summary.tag_id = available_tags.tag_id`, bundleID).
		Joins(`LEFT JOIN tag_overall_performance_summaries AS tag_overall_summary
				 ON tag_overall_summary.tag_id = available_tags.tag_id`)
	return applySmartTagSearch(query, search)
}

func applyCampaignTestPerformance(query *gorm.DB, campaignID, bundleID uint) *gorm.DB {
	return query.Joins(`LEFT JOIN campaign_tag_test_performances AS campaign_test_performance
                 ON campaign_test_performance.campaign_id = ?
                AND campaign_test_performance.bundle_id = ?
				AND campaign_test_performance.tag_id = available_tags.tag_id
				AND campaign_test_performance.phase_type = 'test'`, campaignID, bundleID)
}

func (r *CampaignSelectedTagRepositoryImpl) ListAvailable(ctx context.Context, bundleID, campaignID uint, search, sortBy, sortDirection string, limit, offset int) ([]*models.SmartTargetingTagRow, int64, error) {
	order, err := smartTagOrder(sortBy, sortDirection)
	if err != nil {
		return nil, 0, err
	}

	countQuery := r.baseAvailableQuery(ctx, bundleID, search)
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]*models.SmartTargetingTagRow, 0)
	query := r.baseAvailableQuery(ctx, bundleID, search).
		Select(`available_tags.tag_id,
                 available_tags.tag_name,
                 available_tags.tag_display_title,
                 available_tags.tag_audience_persona,
                 available_tags.tag_audience_count,
                 available_tags.bundle_persona_fit_score,
                 available_tags.evaluation_run_id,
                 available_tags.fit_level,
                 available_tags.relation_type,
                 available_tags.reason,
                 tag_test_summary.test_phase_avg_ctr,
                 tag_test_summary.total_test_selected_count,
                 tag_test_summary.total_test_sent_count,
                 tag_test_summary.total_test_delivered_count,
                 tag_test_summary.total_test_click_count,
                 campaign_test_performance.selected_count,
                 campaign_test_performance.sent_count,
                 campaign_test_performance.delivered_count,
                 campaign_test_performance.click_count,
                 campaign_test_performance.test_campaign_ctr,
				 tag_overall_summary.overall_avg_ctr,
                 EXISTS (
                     SELECT 1 FROM campaign_selected_tags AS selected
                     WHERE selected.campaign_id = ?
                       AND selected.bundle_id = ?
                       AND selected.tag_id = available_tags.tag_id
				 ) AS selected`, campaignID, bundleID)
	query = applyCampaignTestPerformance(query, campaignID, bundleID).
		Order(order)
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *CampaignSelectedTagRepositoryImpl) ListAvailableTagIDs(ctx context.Context, bundleID uint, search, sortBy, sortDirection string, limit int) ([]uint, error) {
	order, err := smartTagOrder(sortBy, sortDirection)
	if err != nil {
		return nil, err
	}
	var ids []uint
	query := r.baseAvailableQuery(ctx, bundleID, search).Select("available_tags.tag_id").Order(order)
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Scan(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *CampaignSelectedTagRepositoryImpl) ListSelected(ctx context.Context, campaignID uint) ([]*models.CampaignSelectedTag, error) {
	rows := make([]*models.CampaignSelectedTag, 0)
	err := orderedCampaignSelectedTagsQuery(r.getDB(ctx), campaignID).Find(&rows).Error
	return rows, err
}

func orderedCampaignSelectedTagsQuery(db *gorm.DB, campaignID uint) *gorm.DB {
	return db.Where("campaign_id = ?", campaignID).Order("selection_order ASC, id ASC")
}

func (r *CampaignSelectedTagRepositoryImpl) Summary(ctx context.Context, campaignID uint) (*models.CampaignSelectedTagSummary, error) {
	var summary models.CampaignSelectedTagSummary
	err := r.getDB(ctx).Model(&models.CampaignSelectedTag{}).
		Select("COUNT(*) AS selected_tag_count, COALESCE(SUM(tag_audience_count_snapshot), 0) AS selected_raw_capacity").
		Where("campaign_id = ?", campaignID).
		Scan(&summary).Error
	return &summary, err
}

func (r *CampaignSelectedTagRepositoryImpl) Validate(ctx context.Context, campaignID, bundleID uint) error {
	var total int64
	if err := r.getDB(ctx).Model(&models.CampaignSelectedTag{}).Where("campaign_id = ?", campaignID).Count(&total).Error; err != nil {
		return err
	}
	var valid int64
	err := r.getDB(ctx).Table("campaign_selected_tags AS selected").
		Where(
			"selected.campaign_id = ? AND selected.bundle_id = ? AND selected.tag_id IN (?)",
			campaignID,
			bundleID,
			availableSmartTagsQuery(r.getDB(ctx), bundleID).Select("available_tags.tag_id"),
		).
		Count(&valid).Error
	if err != nil {
		return err
	}
	if valid != total {
		return ErrInvalidCampaignSelectedTags
	}
	return nil
}

func (r *CampaignSelectedTagRepositoryImpl) Replace(ctx context.Context, campaignID, bundleID, selectedByCustomerID uint, tagIDs []uint) error {
	db := r.getDB(ctx)
	var campaign models.Campaign
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "bundle_id", "status", "spec").First(&campaign, campaignID).Error; err != nil {
		return err
	}
	if campaign.BundleID == nil || *campaign.BundleID != bundleID {
		return ErrInvalidCampaignSelectedTags
	}
	if !campaign.IsEditable() || !campaign.Spec.UsesSmartTargeting() {
		return ErrCampaignSelectedTagsNotEditable
	}

	snapshots := make([]campaignSelectedTagSnapshot, 0, len(tagIDs))
	if len(tagIDs) > 0 {
		err := availableSmartTagsQuery(db, bundleID).
			Joins(`LEFT JOIN tag_test_phase_performance_summaries AS tag_test_summary
                     ON tag_test_summary.bundle_id = ?
					AND tag_test_summary.tag_id = available_tags.tag_id`, bundleID).
			Joins(`LEFT JOIN tag_overall_performance_summaries AS tag_overall_summary
					 ON tag_overall_summary.tag_id = available_tags.tag_id`).
			Select(`available_tags.tag_id,
				COALESCE(
					NULLIF(BTRIM(available_tags.tag_display_title), ''),
					NULLIF(BTRIM(available_tags.tag_name), '')
				) AS display_title,
				available_tags.tag_audience_count AS audience_count,
				available_tags.bundle_persona_fit_score AS bundle_fit_score,
				tag_test_summary.test_phase_avg_ctr,
				tag_overall_summary.overall_avg_ctr`).
			Where("available_tags.tag_id IN ?", tagIDs).
			Order("available_tags.tag_id ASC").
			Scan(&snapshots).Error
		if err != nil {
			return err
		}
		if len(snapshots) != len(tagIDs) {
			return ErrInvalidCampaignSelectedTags
		}
		for _, item := range snapshots {
			if item.AudienceCount != nil && *item.AudienceCount < 0 {
				return ErrInvalidCampaignSelectedTags
			}
		}
	}
	current, err := r.ListSelected(ctx, campaignID)
	if err != nil {
		return err
	}
	if sameOrderedCampaignSelectedTags(current, tagIDs) {
		return nil
	}
	if err := invalidateCampaignSmartTargetingTestPreview(db, campaignID); err != nil {
		return err
	}

	if err := db.Where("campaign_id = ?", campaignID).Delete(&models.CampaignSelectedTag{}).Error; err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return nil
	}

	rows, err := buildCampaignSelectedTagRows(campaignID, bundleID, selectedByCustomerID, tagIDs, snapshots, utils.UTCNow())
	if err != nil {
		return err
	}
	return db.CreateInBatches(rows, 500).Error
}

func sameOrderedCampaignSelectedTags(current []*models.CampaignSelectedTag, tagIDs []uint) bool {
	if len(current) != len(tagIDs) {
		return false
	}
	for position, row := range current {
		if row == nil || row.TagID != tagIDs[position] || row.SelectionOrder != position {
			return false
		}
	}
	return true
}

// buildCampaignSelectedTagRows joins unordered database snapshots back to the
// exact caller-supplied tag order. Snapshot queries use IN predicates and must
// never be trusted to retain request order.
func buildCampaignSelectedTagRows(
	campaignID, bundleID, selectedByCustomerID uint,
	tagIDs []uint,
	snapshots []campaignSelectedTagSnapshot,
	now time.Time,
) ([]*models.CampaignSelectedTag, error) {
	snapshotByTag := make(map[uint]campaignSelectedTagSnapshot, len(snapshots))
	for _, item := range snapshots {
		snapshotByTag[item.TagID] = item
	}
	rows := make([]*models.CampaignSelectedTag, 0, len(snapshots))
	for position, tagID := range tagIDs {
		item, ok := snapshotByTag[tagID]
		if !ok {
			return nil, ErrInvalidCampaignSelectedTags
		}
		rows = append(rows, &models.CampaignSelectedTag{
			CampaignID: campaignID, BundleID: bundleID, TagID: item.TagID,
			SelectionOrder:                position,
			BundlePersonaFitScoreSnapshot: item.BundleFitScore,
			TagDisplayTitleSnapshot:       item.DisplayTitle,
			TagAudienceCountSnapshot:      item.AudienceCount,
			// Preserve the latest materialized metrics at selection time. Nil
			// remains distinct from a real measured zero CTR.
			TestPhaseAvgCTRSnapshot: item.TestPhaseAvgCTR,
			OverallAvgCTRSnapshot:   item.OverallAvgCTR,
			SelectedByCustomerID:    selectedByCustomerID,
			CreatedAt:               now, UpdatedAt: now,
		})
	}
	return rows, nil
}

func (r *CampaignSelectedTagRepositoryImpl) Clear(ctx context.Context, campaignID uint) error {
	db := r.getDB(ctx)
	if err := db.Where("campaign_id = ?", campaignID).Delete(&models.CampaignSelectedTag{}).Error; err != nil {
		return err
	}
	return invalidateCampaignSmartTargetingTestPreview(db, campaignID)
}
