package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// SmartTargetingAudienceQuery describes the eligibility rules shared by exact
// capacity, Test preview, and final selection. AllowedColors is empty for
// platforms without a delivery-color restriction.
type SmartTargetingAudienceQuery struct {
	BundleID uint
	// ExcludeActiveTestReservationCampaignID keeps a campaign's own active
	// Test reservation in its capacity population. A finalized Test campaign
	// can refresh capacity before execution; its already-reserved sample is not
	// prior use by another campaign and must not make its own capacity collapse.
	// Zero retains the normal behavior of excluding every active reservation.
	ExcludeActiveTestReservationCampaignID uint
	// ApplyBundleAudienceExclusions enables the manually populated, bundle-
	// scoped exclusion list for Smart Targeting Test capacity, preview, and
	// final selection.
	ApplyBundleAudienceExclusions bool
	TagIDs                        []int64
	ScoreClasses                  []string
	AllowedColors                 []string
}

type SmartTargetingAudienceRepository interface {
	CalculateCapacity(ctx context.Context, query SmartTargetingAudienceQuery) (*SmartTargetingAudienceCapacity, error)
	SelectCandidates(ctx context.Context, query SmartTargetingAudienceQuery, limit int64) ([]*models.AudienceProfile, error)
	CalculateScoreBounds(ctx context.Context, query SmartTargetingAudienceQuery) (*SmartTargetingScoreBounds, error)
	SelectIDsForTag(ctx context.Context, query SmartTargetingAudienceQuery, bounds *SmartTargetingScoreBounds, tagID int64, excludeAudienceIDs []int64, limit int64) ([]int64, error)
	SelectForTag(ctx context.Context, query SmartTargetingAudienceQuery, bounds *SmartTargetingScoreBounds, tagID int64, excludeAudienceIDs []int64, limit int64) ([]*models.AudienceProfile, error)
}

type SmartTargetingAudienceCapacity struct {
	RawUniqueCount      int64 `gorm:"column:raw_unique_count"`
	EligibleUniqueCount int64 `gorm:"column:eligible_unique_count"`
}

// SmartTargetingScoreBounds contains the union-wide score boundaries used by
// every per-tag selection in one operation. Nil boundaries mean the eligible
// union has no scored profiles, so every restricted score class is empty.
type SmartTargetingScoreBounds struct {
	P33 *float64 `gorm:"column:p33"`
	P66 *float64 `gorm:"column:p66"`
}

type smartTargetingAudienceRepository struct{ db *gorm.DB }

func NewSmartTargetingAudienceRepository(db *gorm.DB) SmartTargetingAudienceRepository {
	return &smartTargetingAudienceRepository{db: db}
}

func (r *smartTargetingAudienceRepository) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

const smartTargetingBasePopulationCTE = `
WITH tagged_population AS (
    SELECT ap.id, ap.normalized_score
    FROM audience_profiles AS ap
    WHERE ap.tags && ?::integer[]
      AND ap.phone_number IS NOT NULL
      AND BTRIM(ap.phone_number) <> ''
      AND (?::boolean OR ap.color = ANY(?::text[]))
), candidate_population AS (
    SELECT tagged.*
    FROM tagged_population AS tagged
    WHERE NOT EXISTS (
          SELECT 1
          FROM bundle_audience_selection_members AS used
      WHERE used.bundle_id = ? AND used.audience_id = tagged.id
      )
      AND NOT EXISTS (
          SELECT 1
          FROM campaign_targeting_test_sample_reservations AS reserved
          WHERE reserved.bundle_id = ? AND reserved.audience_id = tagged.id
            AND reserved.state = 'active'
			AND (?::bigint = 0 OR reserved.campaign_id <> ?)
      )
      AND (?::boolean OR NOT EXISTS (
          SELECT 1
          FROM bundle_audience_exclusions AS bundle_exclusion
          WHERE bundle_exclusion.bundle_id = ?
            AND bundle_exclusion.audience_id = tagged.id
      ))
)`

// Execution selection needs delivery columns. Capacity and score-bound work
// use the slim CTE above so PostgreSQL does not materialize UID, phone, or tag
// arrays for count/percentile-only operations.
const smartTargetingSelectionBasePopulationCTE = `
WITH tagged_population AS (
    SELECT ap.id, ap.uid, ap.phone_number, ap.tags, ap.normalized_score
    FROM audience_profiles AS ap
    WHERE ap.tags && ?::integer[]
      AND ap.phone_number IS NOT NULL
      AND BTRIM(ap.phone_number) <> ''
      AND (?::boolean OR ap.color = ANY(?::text[]))
), candidate_population AS (
    SELECT tagged.*
    FROM tagged_population AS tagged
    WHERE NOT EXISTS (
          SELECT 1
          FROM bundle_audience_selection_members AS used
      WHERE used.bundle_id = ? AND used.audience_id = tagged.id
      )
      AND NOT EXISTS (
          SELECT 1
          FROM campaign_targeting_test_sample_reservations AS reserved
          WHERE reserved.bundle_id = ? AND reserved.audience_id = tagged.id
            AND reserved.state = 'active'
			AND (?::bigint = 0 OR reserved.campaign_id <> ?)
      )
      AND (?::boolean OR NOT EXISTS (
          SELECT 1
          FROM bundle_audience_exclusions AS bundle_exclusion
          WHERE bundle_exclusion.bundle_id = ?
            AND bundle_exclusion.audience_id = tagged.id
      ))
)`

const smartTargetingClassifiedPopulationCTE = `, percentile_bounds AS (
    SELECT percentile_disc(ARRAY[0.33, 0.66]::double precision[])
               WITHIN GROUP (ORDER BY normalized_score) AS percentile_values
    FROM candidate_population
    WHERE normalized_score IS NOT NULL
), bounds AS (
    SELECT percentile_values[1] AS p33, percentile_values[2] AS p66
    FROM percentile_bounds
), classified AS (
    SELECT population.*,
        CASE
            WHEN population.normalized_score IS NULL THEN 'unscored'
            WHEN bounds.p33 IS NULL OR bounds.p66 IS NULL THEN 'unscored'
            WHEN population.normalized_score <= bounds.p33 THEN 'C'
            WHEN population.normalized_score <= bounds.p66 THEN 'B'
            ELSE 'A'
        END AS score_class
    FROM candidate_population AS population
    CROSS JOIN bounds
)`

const smartTargetingPopulationCTE = smartTargetingBasePopulationCTE + smartTargetingClassifiedPopulationCTE
const smartTargetingSelectionPopulationCTE = smartTargetingSelectionBasePopulationCTE + smartTargetingClassifiedPopulationCTE

func smartTargetingAllClasses(classes []string) bool {
	return len(classes) == 3 && classes[0] == "A" && classes[1] == "B" && classes[2] == "C"
}

func smartTargetingPopulationArgs(query SmartTargetingAudienceQuery) []any {
	colors := query.AllowedColors
	if colors == nil {
		colors = []string{}
	}
	return []any{
		pq.Array(query.TagIDs),
		len(colors) == 0,
		pq.Array(colors),
		query.BundleID,
		query.BundleID,
		query.ExcludeActiveTestReservationCampaignID,
		query.ExcludeActiveTestReservationCampaignID,
		!query.ApplyBundleAudienceExclusions,
		query.BundleID,
	}
}

func (r *smartTargetingAudienceRepository) CalculateCapacity(ctx context.Context, query SmartTargetingAudienceQuery) (*SmartTargetingAudienceCapacity, error) {
	if query.BundleID == 0 || len(query.TagIDs) == 0 || len(query.ScoreClasses) == 0 {
		return nil, fmt.Errorf("invalid smart-targeting audience count query")
	}
	var capacity SmartTargetingAudienceCapacity
	if smartTargetingAllClasses(query.ScoreClasses) {
		sql := smartTargetingBasePopulationCTE + `
SELECT (SELECT COUNT(*) FROM tagged_population) AS raw_unique_count,
       (SELECT COUNT(*) FROM candidate_population) AS eligible_unique_count`
		err := r.getDB(ctx).Raw(sql, smartTargetingPopulationArgs(query)...).Scan(&capacity).Error
		return &capacity, err
	}
	sql := smartTargetingPopulationCTE + `
SELECT (SELECT COUNT(*) FROM tagged_population) AS raw_unique_count,
       COUNT(*) FILTER (WHERE (?::boolean OR score_class = ANY(?::text[]))) AS eligible_unique_count
FROM classified`
	args := append(smartTargetingPopulationArgs(query),
		smartTargetingAllClasses(query.ScoreClasses), pq.Array(query.ScoreClasses))
	err := r.getDB(ctx).Raw(sql, args...).Scan(&capacity).Error
	return &capacity, err
}

func (r *smartTargetingAudienceRepository) SelectCandidates(ctx context.Context, query SmartTargetingAudienceQuery, limit int64) ([]*models.AudienceProfile, error) {
	if query.BundleID == 0 || len(query.TagIDs) == 0 || len(query.ScoreClasses) == 0 || limit <= 0 {
		return nil, fmt.Errorf("invalid smart-targeting audience selection query")
	}
	var rows []*models.AudienceProfile
	if smartTargetingAllClasses(query.ScoreClasses) {
		sql := smartTargetingSelectionBasePopulationCTE + `
SELECT id, uid, phone_number, tags, normalized_score
FROM candidate_population
ORDER BY normalized_score DESC NULLS LAST, id ASC
LIMIT ?`
		args := append(smartTargetingPopulationArgs(query), limit)
		err := r.getDB(ctx).Raw(sql, args...).Scan(&rows).Error
		return rows, err
	}
	sql := smartTargetingSelectionPopulationCTE + `
SELECT id, uid, phone_number, tags, normalized_score
FROM classified
WHERE (?::boolean OR score_class = ANY(?::text[]))
ORDER BY normalized_score DESC NULLS LAST, id ASC
LIMIT ?`
	args := append(smartTargetingPopulationArgs(query),
		smartTargetingAllClasses(query.ScoreClasses), pq.Array(query.ScoreClasses), limit)
	err := r.getDB(ctx).Raw(sql, args...).Scan(&rows).Error
	return rows, err
}

// CalculateScoreBounds computes the selected-tag union's exact p33/p66 once
// per sampling operation. The former per-tag query rebuilt and sorted this
// union for every selected tag.
func (r *smartTargetingAudienceRepository) CalculateScoreBounds(ctx context.Context, query SmartTargetingAudienceQuery) (*SmartTargetingScoreBounds, error) {
	if query.BundleID == 0 || len(query.TagIDs) == 0 || len(query.ScoreClasses) == 0 {
		return nil, fmt.Errorf("invalid smart-targeting score-bound query")
	}
	if smartTargetingAllClasses(query.ScoreClasses) {
		return &SmartTargetingScoreBounds{}, nil
	}
	sql, args := smartTargetingScoreBoundsQuery(query)
	var bounds SmartTargetingScoreBounds
	if err := r.getDB(ctx).Raw(sql, args...).Scan(&bounds).Error; err != nil {
		return nil, err
	}
	return &bounds, nil
}

// SelectIDsForTag remains available for count-only callers. Test preview
// persistence uses SelectForTag so its immutable snapshot contains the exact
// selected profile IDs and tag attribution.
func (r *smartTargetingAudienceRepository) SelectIDsForTag(ctx context.Context, query SmartTargetingAudienceQuery, bounds *SmartTargetingScoreBounds, tagID int64, excludeAudienceIDs []int64, limit int64) ([]int64, error) {
	sql, args, err := smartTargetingPerTagSelectionQuery(query, bounds, tagID, excludeAudienceIDs, limit, true)
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := r.getDB(ctx).Raw(sql, args...).Scan(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// SelectForTag is the Smart Targeting Test sampling path. Score classes
// restrict eligibility; no random, score, ID, or other ordering is imposed.
// TODO(smart-targeting-random-sampling): Without ORDER BY, PostgreSQL returns
// a planner/storage-dependent LIMIT result. It is therefore not statistically
// random and is not reproducible. ORDER BY random() is deliberately avoided
// because it is prohibitively expensive at the current population size; replace
// this with a performant indexed or seeded sampling strategy.
func (r *smartTargetingAudienceRepository) SelectForTag(ctx context.Context, query SmartTargetingAudienceQuery, bounds *SmartTargetingScoreBounds, tagID int64, excludeAudienceIDs []int64, limit int64) ([]*models.AudienceProfile, error) {
	sql, args, err := smartTargetingPerTagSelectionQuery(query, bounds, tagID, excludeAudienceIDs, limit, false)
	if err != nil {
		return nil, err
	}
	var rows []*models.AudienceProfile
	if err := r.getDB(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func smartTargetingScoreBoundsQuery(query SmartTargetingAudienceQuery) (string, []any) {
	sql := `
	SELECT percentile_values[1] AS p33, percentile_values[2] AS p66
FROM (
    SELECT percentile_disc(ARRAY[0.33, 0.66]::double precision[])
               WITHIN GROUP (ORDER BY ap.normalized_score) AS percentile_values
    FROM audience_profiles AS ap
    WHERE ap.tags && ?::integer[]
      AND ap.phone_number IS NOT NULL
      AND BTRIM(ap.phone_number) <> ''
      AND ap.normalized_score IS NOT NULL`
	args := []any{pq.Array(query.TagIDs)}
	if len(query.AllowedColors) > 0 {
		sql += `
      AND ap.color = ANY(?::text[])`
		args = append(args, pq.Array(query.AllowedColors))
	}
	sql += `
	  AND NOT EXISTS (
          SELECT 1
          FROM bundle_audience_selection_members AS used
          WHERE used.bundle_id = ? AND used.audience_id = ap.id
      )`
	args = append(args, query.BundleID)
	sql += `
      AND NOT EXISTS (
          SELECT 1
      FROM campaign_targeting_test_sample_reservations AS reserved
      WHERE reserved.bundle_id = ? AND reserved.audience_id = ap.id
        AND reserved.state = 'active'
		AND (?::bigint = 0 OR reserved.campaign_id <> ?)
      )`
	args = append(args, query.BundleID, query.ExcludeActiveTestReservationCampaignID, query.ExcludeActiveTestReservationCampaignID)
	if query.ApplyBundleAudienceExclusions {
		sql += `
      AND NOT EXISTS (
          SELECT 1
          FROM bundle_audience_exclusions AS bundle_exclusion
          WHERE bundle_exclusion.bundle_id = ?
            AND bundle_exclusion.audience_id = ap.id
      )`
		args = append(args, query.BundleID)
	}
	sql += `
) AS calculated_bounds`
	return sql, args
}

func smartTargetingPerTagSelectionQuery(query SmartTargetingAudienceQuery, bounds *SmartTargetingScoreBounds, tagID int64, excludeAudienceIDs []int64, limit int64, idsOnly bool) (string, []any, error) {
	if query.BundleID == 0 || len(query.TagIDs) == 0 || len(query.ScoreClasses) == 0 || tagID <= 0 || limit <= 0 {
		return "", nil, fmt.Errorf("invalid smart-targeting per-tag sample query")
	}
	selected := false
	for _, candidateTagID := range query.TagIDs {
		if candidateTagID == tagID {
			selected = true
			break
		}
	}
	if !selected {
		return "", nil, fmt.Errorf("smart-targeting per-tag sample is not in the selected tag set")
	}
	if excludeAudienceIDs == nil {
		excludeAudienceIDs = []int64{}
	}
	columns := "ap.id, ap.uid, ap.phone_number, ap.tags, ap.normalized_score"
	if idsOnly {
		columns = "ap.id"
	}
	sql := `
SELECT ` + columns + `
FROM audience_profiles AS ap
WHERE ap.tags @> ARRAY[?]::integer[]
  AND ap.phone_number IS NOT NULL
  AND BTRIM(ap.phone_number) <> ''`
	args := []any{tagID}
	if len(query.AllowedColors) > 0 {
		sql += `
  AND ap.color = ANY(?::text[])`
		args = append(args, pq.Array(query.AllowedColors))
	}
	sql += `
	  AND NOT EXISTS (
      SELECT 1
      FROM bundle_audience_selection_members AS used
      WHERE used.bundle_id = ? AND used.audience_id = ap.id
  )`
	args = append(args, query.BundleID)
	sql += `
  AND NOT EXISTS (
      SELECT 1
      FROM campaign_targeting_test_sample_reservations AS reserved
      WHERE reserved.bundle_id = ? AND reserved.audience_id = ap.id
        AND reserved.state = 'active'
		AND (?::bigint = 0 OR reserved.campaign_id <> ?)
  )`
	args = append(args, query.BundleID, query.ExcludeActiveTestReservationCampaignID, query.ExcludeActiveTestReservationCampaignID)
	if query.ApplyBundleAudienceExclusions {
		sql += `
  AND NOT EXISTS (
      SELECT 1
      FROM bundle_audience_exclusions AS bundle_exclusion
      WHERE bundle_exclusion.bundle_id = ?
        AND bundle_exclusion.audience_id = ap.id
  )`
		args = append(args, query.BundleID)
	}
	sql += `
  AND NOT EXISTS (
      SELECT 1
      FROM unnest(?::bigint[]) AS earlier(audience_id)
      WHERE earlier.audience_id = ap.id
  )`
	args = append(args, pq.Array(excludeAudienceIDs))

	scorePredicate, scoreArgs, err := smartTargetingPerTagScorePredicate(query.ScoreClasses, bounds)
	if err != nil {
		return "", nil, err
	}
	sql += scorePredicate + `
LIMIT ?`
	args = append(args, scoreArgs...)
	args = append(args, limit)
	return sql, args, nil
}

func smartTargetingPerTagScorePredicate(classes []string, bounds *SmartTargetingScoreBounds) (string, []any, error) {
	key := strings.Join(classes, ",")
	if key == "A,B,C" {
		return "", nil, nil
	}
	if bounds == nil {
		return "", nil, fmt.Errorf("smart-targeting score bounds are required")
	}
	if (bounds.P33 == nil) != (bounds.P66 == nil) {
		return "", nil, fmt.Errorf("smart-targeting score bounds are inconsistent")
	}
	if bounds.P33 == nil {
		return "\n  AND FALSE", nil, nil
	}
	if *bounds.P33 > *bounds.P66 {
		return "", nil, fmt.Errorf("smart-targeting score bounds are invalid")
	}
	p33, p66 := *bounds.P33, *bounds.P66
	switch key {
	case "A":
		return "\n  AND ap.normalized_score > ?::double precision", []any{p66}, nil
	case "B":
		return "\n  AND ap.normalized_score > ?::double precision AND ap.normalized_score <= ?::double precision", []any{p33, p66}, nil
	case "C":
		return "\n  AND ap.normalized_score <= ?::double precision", []any{p33}, nil
	case "A,B":
		return "\n  AND ap.normalized_score > ?::double precision", []any{p33}, nil
	case "A,C":
		return "\n  AND (ap.normalized_score <= ?::double precision OR ap.normalized_score > ?::double precision)", []any{p33, p66}, nil
	case "B,C":
		return "\n  AND ap.normalized_score <= ?::double precision", []any{p66}, nil
	default:
		return "", nil, fmt.Errorf("invalid smart-targeting score classes")
	}
}
