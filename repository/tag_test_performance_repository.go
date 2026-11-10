package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTagTestPerformanceLeaseLost       = errors.New("tag test performance report lease changed")
	ErrTagTestPerformanceCampaignInvalid = errors.New("campaign is not an attributable smart targeting campaign")
)

// TagTestPerformanceRepository owns discovery, durable leasing, per-Campaign
// recomputation, Bundle/tag Test refresh, and global overall refresh. The
// historical name is retained for configuration compatibility. It never locks
// short-link or click rows: ingestion and redirects remain independent.
type TagTestPerformanceRepository interface {
	DiscoverPending(ctx context.Context, at time.Time) error
	ClaimPending(ctx context.Context, limit int, staleBefore, at time.Time) ([]*models.CampaignTagTestReport, error)
	RecomputeCampaign(ctx context.Context, campaignID uint, leaseStartedAt, at time.Time) error
	Fail(ctx context.Context, campaignID uint, leaseStartedAt time.Time, code, message string, retryAt *time.Time, at time.Time) error
}

type TagTestPerformanceRepositoryImpl struct {
	db *gorm.DB
}

const tagTestDiscoveryOverlap = 5 * time.Minute

const discoverPendingTagTestReportsSQL = `
INSERT INTO campaign_tag_test_reports (
    campaign_id, bundle_id, status, calculation_version, attempt_count,
    requested_at, created_at, updated_at
)
SELECT DISTINCT
    campaign.id,
    campaign.bundle_id,
    'not_prepared',
    ?::integer,
    0,
    ?::timestamptz,
    ?::timestamptz,
    ?::timestamptz
FROM campaigns AS campaign
LEFT JOIN campaign_tag_test_reports AS existing
  ON existing.campaign_id = campaign.id
WHERE campaign.bundle_id IS NOT NULL
  AND campaign.phase IN (?, ?)
  AND LOWER(BTRIM(COALESCE(campaign.spec->>'audience_targeting_method', ''))) = ?
  AND EXISTS (
      SELECT 1
      FROM campaign_audience_tag_attributions AS attribution
      WHERE attribution.campaign_id = campaign.id
        AND attribution.bundle_id = campaign.bundle_id
        AND attribution.phase_type = campaign.phase
  )
  AND (
      existing.campaign_id IS NULL
      OR existing.calculation_version <> ?
      OR EXISTS (
          SELECT 1
          FROM short_link_clicks AS click
          WHERE click.campaign_id = campaign.id
            AND COALESCE(click.is_test, FALSE) = FALSE
            AND (
                (click.id > ? AND click.id <= ?)
                OR (click.created_at > ? AND click.created_at <= ?)
            )
      )
      OR EXISTS (
          SELECT 1
          FROM processed_campaigns AS processed
          WHERE processed.campaign_id = campaign.id
            AND processed.updated_at > ?
      )
      OR EXISTS (
          SELECT 1
          FROM campaign_status_jobs AS status_job
          JOIN processed_campaigns AS processed
            ON processed.id = status_job.processed_campaign_id
          WHERE processed.campaign_id = campaign.id
            AND status_job.updated_at > ?
      )
  )
ON CONFLICT (campaign_id) DO UPDATE
SET bundle_id = EXCLUDED.bundle_id,
    status = CASE
        WHEN campaign_tag_test_reports.status = 'preparing'
            THEN campaign_tag_test_reports.status
        ELSE 'not_prepared'
    END,
    calculation_version = EXCLUDED.calculation_version,
    requested_at = EXCLUDED.requested_at,
    started_at = CASE
        WHEN campaign_tag_test_reports.status = 'preparing'
            THEN campaign_tag_test_reports.started_at
        ELSE NULL
    END,
    finished_at = CASE
        WHEN campaign_tag_test_reports.status = 'preparing'
            THEN campaign_tag_test_reports.finished_at
        ELSE NULL
    END,
    next_retry_at = NULL,
    error_code = NULL,
    error_message = NULL,
    updated_at = EXCLUDED.updated_at`

func NewTagTestPerformanceRepository(db *gorm.DB) TagTestPerformanceRepository {
	return &TagTestPerformanceRepositoryImpl{db: db}
}

func (r *TagTestPerformanceRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

// DiscoverPending advances the click/status cursors only after every affected
// Campaign has a durable queue row. Retryable failed jobs remain claimable
// without a new source event; terminal invalid Campaigns do not spin forever.
// Campaigns with Feature 4 attribution but no report are discovered as a
// backfill, including zero-click Campaigns.
func (r *TagTestPerformanceRepositoryImpl) DiscoverPending(ctx context.Context, at time.Time) error {
	at = at.UTC()
	return WithTransaction(ctx, r.db, func(txCtx context.Context) error {
		db := r.getDB(txCtx)
		var state models.TagTestPerformanceSchedulerState
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, int16(1)).Error; err != nil {
			return fmt.Errorf("load tag performance scheduler state: %w", err)
		}

		var clickCutoff int64
		if err := db.Raw("SELECT COALESCE(MAX(id), 0) FROM short_link_clicks").Scan(&clickCutoff).Error; err != nil {
			return fmt.Errorf("read click cursor: %w", err)
		}

		args := []any{
			models.TagTestPerformanceCalculationVersion,
			at, at, at,
			models.CampaignPhaseTest,
			models.CampaignPhaseExecution,
			models.CampaignAudienceTargetingSmart,
			models.TagTestPerformanceCalculationVersion,
			state.LastClickID,
			clickCutoff,
			state.LastSourceScanAt.Add(-tagTestDiscoveryOverlap),
			at,
			state.LastSourceScanAt.Add(-tagTestDiscoveryOverlap),
			state.LastSourceScanAt.Add(-tagTestDiscoveryOverlap),
		}
		if err := db.Exec(discoverPendingTagTestReportsSQL, args...).Error; err != nil {
			return fmt.Errorf("enqueue tag performance reports: %w", err)
		}

		if err := db.Model(&models.TagTestPerformanceSchedulerState{}).
			Where("id = ?", state.ID).
			Updates(map[string]any{
				"last_click_id":       clickCutoff,
				"last_source_scan_at": at,
				"updated_at":          at,
			}).Error; err != nil {
			return fmt.Errorf("advance tag performance scheduler state: %w", err)
		}
		return nil
	})
}

// ClaimPending leases reports across scheduler replicas. A process crash is
// recovered by reclaiming a stale preparing row.
func (r *TagTestPerformanceRepositoryImpl) ClaimPending(ctx context.Context, limit int, staleBefore, at time.Time) ([]*models.CampaignTagTestReport, error) {
	if limit <= 0 {
		limit = 1
	}
	rows := make([]*models.CampaignTagTestReport, 0)
	err := r.getDB(ctx).Raw(
		claimPendingTagTestReportsSQL,
		models.TagTestPerformanceCalculationVersion,
		at,
		staleBefore,
		limit,
		at,
		at,
	).Scan(&rows).Error
	return rows, err
}

const claimPendingTagTestReportsSQL = `
WITH claimable AS (
    SELECT campaign_id
    FROM campaign_tag_test_reports
    WHERE calculation_version = ?
      AND (
          status = 'not_prepared'
          OR (status = 'failed' AND next_retry_at <= ?)
          OR (status = 'preparing' AND started_at < ?)
      )
    ORDER BY requested_at ASC, campaign_id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT ?
)
UPDATE campaign_tag_test_reports AS report
SET status = 'preparing',
    attempt_count = report.attempt_count + 1,
    started_at = ?,
    finished_at = NULL,
    next_retry_at = NULL,
    error_code = NULL,
    error_message = NULL,
    updated_at = ?
FROM claimable
WHERE report.campaign_id = claimable.campaign_id
RETURNING report.*`

type attributableCampaign struct {
	CampaignID uint                 `gorm:"column:campaign_id"`
	BundleID   uint                 `gorm:"column:bundle_id"`
	PhaseType  models.CampaignPhase `gorm:"column:phase_type"`
	Platform   string               `gorm:"column:platform"`
}

// RecomputeCampaign reads the complete source history for one Campaign and
// replaces its materialized values with one aggregate SQL statement. The
// report lease and a short global summary lock make completion and refresh
// safe across concurrent scheduler replicas.
func (r *TagTestPerformanceRepositoryImpl) RecomputeCampaign(ctx context.Context, campaignID uint, leaseStartedAt, at time.Time) error {
	return WithTransaction(ctx, r.db, func(txCtx context.Context) error {
		db := r.getDB(txCtx)

		var report models.CampaignTagTestReport
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("campaign_id = ? AND status = ? AND started_at = ?", campaignID, models.TagTestReportStatusPreparing, leaseStartedAt).
			First(&report).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTagTestPerformanceLeaseLost
		}
		if err != nil {
			return fmt.Errorf("lock tag performance report: %w", err)
		}
		if report.CalculationVersion != models.TagTestPerformanceCalculationVersion {
			// A newer deployment requested a different calculation generation after
			// this worker claimed the row. Release it without writing old-version
			// metrics; a worker running the requested version can claim it next.
			released := db.Model(&models.CampaignTagTestReport{}).
				Where("campaign_id = ? AND status = ? AND started_at = ?", campaignID, models.TagTestReportStatusPreparing, leaseStartedAt).
				Updates(map[string]any{
					"status":     models.TagTestReportStatusNotPrepared,
					"started_at": nil,
					"updated_at": at,
				})
			if released.Error != nil {
				return fmt.Errorf("release stale tag performance calculation version: %w", released.Error)
			}
			if released.RowsAffected != 1 {
				return ErrTagTestPerformanceLeaseLost
			}
			return nil
		}

		var campaign attributableCampaign
		const campaignSQL = `
SELECT campaign.id AS campaign_id,
       campaign.bundle_id,
       campaign.phase AS phase_type,
       LOWER(BTRIM(COALESCE(campaign.spec->>'platform', 'sms'))) AS platform
FROM campaigns AS campaign
WHERE campaign.id = ?
  AND campaign.bundle_id IS NOT NULL
  AND campaign.bundle_id = ?
  AND campaign.phase IN (?, ?)
  AND LOWER(BTRIM(COALESCE(campaign.spec->>'audience_targeting_method', ''))) = ?
  AND EXISTS (
      SELECT 1
      FROM campaign_audience_tag_attributions AS attribution
      WHERE attribution.campaign_id = campaign.id
        AND attribution.bundle_id = campaign.bundle_id
        AND attribution.phase_type = campaign.phase
  )`
		err = db.Raw(
			campaignSQL,
			campaignID,
			report.BundleID,
			models.CampaignPhaseTest,
			models.CampaignPhaseExecution,
			models.CampaignAudienceTargetingSmart,
		).Scan(&campaign).Error
		if err != nil {
			return fmt.Errorf("validate tag performance campaign: %w", err)
		}
		if campaign.CampaignID == 0 || campaign.BundleID == 0 ||
			(campaign.PhaseType != models.CampaignPhaseTest && campaign.PhaseType != models.CampaignPhaseExecution) ||
			!models.IsValidCampaignPlatform(campaign.Platform) {
			return ErrTagTestPerformanceCampaignInvalid
		}
		performanceSQL, err := recomputeCampaignTagPerformanceSQLForPlatform(campaign.Platform)
		if err != nil {
			return ErrTagTestPerformanceCampaignInvalid
		}

		var clickCutoff int64
		if err := db.Raw("SELECT COALESCE(MAX(id), 0) FROM short_link_clicks").Scan(&clickCutoff).Error; err != nil {
			return fmt.Errorf("read tag performance click cutoff: %w", err)
		}

		result := db.Exec(performanceSQL,
			campaignID,
			campaign.PhaseType,
			campaignID,
			models.TagTestPerformanceCalculationVersion,
			at,
			at,
		)
		if result.Error != nil {
			return fmt.Errorf("materialize campaign tag performance: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrTagTestPerformanceCampaignInvalid
		}
		if err := deleteStaleCampaignTagPerformances(db, campaignID); err != nil {
			return err
		}

		// Only the inexpensive materialized summaries are serialized. This global
		// lock is required because different Bundles can update the same tag's
		// overall row; the high-volume Campaign aggregation remains parallel.
		if err := db.Exec("SELECT pg_advisory_xact_lock(845171, 0)").Error; err != nil {
			return fmt.Errorf("lock tag performance summaries: %w", err)
		}

		status := models.TagTestReportStatusPrepared
		if report.RequestedAt.After(leaseStartedAt) {
			status = models.TagTestReportStatusNotPrepared
		}
		updates := map[string]any{
			"status":                   status,
			"finished_at":              at,
			"next_retry_at":            nil,
			"last_calculated_click_id": clickCutoff,
			"error_code":               nil,
			"error_message":            nil,
			"updated_at":               at,
		}
		if status == models.TagTestReportStatusNotPrepared {
			updates["started_at"] = nil
		}
		completion := db.Model(&models.CampaignTagTestReport{}).
			Where("campaign_id = ? AND status = ? AND started_at = ?", campaignID, models.TagTestReportStatusPreparing, leaseStartedAt).
			Updates(updates)
		if completion.Error != nil {
			return fmt.Errorf("complete tag performance report: %w", completion.Error)
		}
		if completion.RowsAffected != 1 {
			return ErrTagTestPerformanceLeaseLost
		}

		if err := refreshTagTestPhaseSummary(db, campaign.BundleID, at); err != nil {
			return err
		}
		if err := refreshTagOverallPerformanceSummary(db, at); err != nil {
			return err
		}
		return nil
	})
}

// Fail persists only a stable error code and sanitized message. Database SQL
// and stack details remain in application logs, never in API-readable state.
func (r *TagTestPerformanceRepositoryImpl) Fail(ctx context.Context, campaignID uint, leaseStartedAt time.Time, code, message string, retryAt *time.Time, at time.Time) error {
	if len(code) > 64 {
		code = code[:64]
	}
	if len(message) > 255 {
		message = message[:255]
	}
	result := r.getDB(ctx).Model(&models.CampaignTagTestReport{}).
		Where("campaign_id = ? AND status = ? AND started_at = ?", campaignID, models.TagTestReportStatusPreparing, leaseStartedAt).
		Updates(map[string]any{
			"status":        models.TagTestReportStatusFailed,
			"finished_at":   at,
			"next_retry_at": retryAt,
			"error_code":    code,
			"error_message": message,
			"updated_at":    at,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTagTestPerformanceLeaseLost
	}
	return nil
}

const summarySQL = `
INSERT INTO tag_test_phase_performance_summaries (
    bundle_id,
    tag_id,
    total_test_selected_count,
    total_test_sent_count,
    total_test_delivered_count,
    total_test_click_count,
    calculation_version,
    created_at,
    updated_at
)
SELECT
    performance.bundle_id,
    performance.tag_id,
    SUM(performance.selected_count),
    SUM(performance.sent_count),
    SUM(performance.delivered_count),
    SUM(performance.click_count),
    ?::integer,
    ?::timestamptz,
    ?::timestamptz
FROM campaign_tag_test_performances AS performance
WHERE performance.bundle_id = ?
  AND performance.phase_type = ?
GROUP BY performance.bundle_id, performance.tag_id
ON CONFLICT (bundle_id, tag_id) DO UPDATE
SET total_test_selected_count = EXCLUDED.total_test_selected_count,
    total_test_sent_count = EXCLUDED.total_test_sent_count,
    total_test_delivered_count = EXCLUDED.total_test_delivered_count,
    total_test_click_count = EXCLUDED.total_test_click_count,
    calculation_version = EXCLUDED.calculation_version,
    updated_at = EXCLUDED.updated_at`

func refreshTagTestPhaseSummary(db *gorm.DB, bundleID uint, at time.Time) error {
	if err := db.Exec(summarySQL, models.TagTestPerformanceCalculationVersion, at, at, bundleID, models.CampaignPhaseTest).Error; err != nil {
		return fmt.Errorf("refresh tag test performance summary: %w", err)
	}
	const deleteStaleSQL = `
DELETE FROM tag_test_phase_performance_summaries AS summary
WHERE summary.bundle_id = ?
  AND NOT EXISTS (
      SELECT 1
      FROM campaign_tag_test_performances AS performance
      WHERE performance.bundle_id = summary.bundle_id
        AND performance.tag_id = summary.tag_id
        AND performance.phase_type = ?
  )`
	if err := db.Exec(deleteStaleSQL, bundleID, models.CampaignPhaseTest).Error; err != nil {
		return fmt.Errorf("delete stale tag test performance summaries: %w", err)
	}
	return nil
}

const overallSummarySQL = `
INSERT INTO tag_overall_performance_summaries (
    tag_id,
    total_selected_count,
    total_sent_count,
    total_delivered_count,
    total_click_count,
    calculation_version,
    created_at,
    updated_at
)
SELECT
    performance.tag_id,
    SUM(performance.selected_count),
    SUM(performance.sent_count),
    SUM(performance.delivered_count),
    SUM(performance.click_count),
    ?::integer,
    ?::timestamptz,
    ?::timestamptz
FROM campaign_tag_test_performances AS performance
GROUP BY performance.tag_id
ON CONFLICT (tag_id) DO UPDATE
SET total_selected_count = EXCLUDED.total_selected_count,
    total_sent_count = EXCLUDED.total_sent_count,
    total_delivered_count = EXCLUDED.total_delivered_count,
    total_click_count = EXCLUDED.total_click_count,
    calculation_version = EXCLUDED.calculation_version,
    updated_at = EXCLUDED.updated_at`

func refreshTagOverallPerformanceSummary(db *gorm.DB, at time.Time) error {
	if err := db.Exec(overallSummarySQL, models.TagTestPerformanceCalculationVersion, at, at).Error; err != nil {
		return fmt.Errorf("refresh overall tag performance summary: %w", err)
	}
	const deleteStaleSQL = `
DELETE FROM tag_overall_performance_summaries AS summary
WHERE NOT EXISTS (
    SELECT 1
    FROM campaign_tag_test_performances AS performance
    WHERE performance.tag_id = summary.tag_id
)`
	if err := db.Exec(deleteStaleSQL).Error; err != nil {
		return fmt.Errorf("delete stale overall tag performance summaries: %w", err)
	}
	return nil
}

const deleteStaleCampaignTagPerformancesSQL = `
DELETE FROM campaign_tag_test_performances AS performance
WHERE performance.campaign_id = ?
  AND NOT EXISTS (
      SELECT 1
      FROM campaign_audience_tag_attributions AS attribution
      JOIN campaigns AS campaign
        ON campaign.id = attribution.campaign_id
       AND campaign.bundle_id = attribution.bundle_id
      WHERE attribution.campaign_id = performance.campaign_id
        AND attribution.bundle_id = performance.bundle_id
        AND attribution.phase_type = performance.phase_type
        AND attribution.assigned_tag_id = performance.tag_id
  )`

func deleteStaleCampaignTagPerformances(db *gorm.DB, campaignID uint) error {
	if err := db.Exec(deleteStaleCampaignTagPerformancesSQL, campaignID).Error; err != nil {
		return fmt.Errorf("delete stale campaign tag performance: %w", err)
	}
	return nil
}

// tagTestPerformanceSourceSQL contains only constant query fragments. Keeping
// each platform's delivery history isolated is important: a campaign has one
// delivery platform, so scanning the other three large sent/status tables is
// pure overhead and can starve the campaign-finalization path.
type tagTestPerformanceSourceSQL struct {
	sentRecipients      string
	deliveredRecipients string
}

var tagTestPerformanceSources = map[string]tagTestPerformanceSourceSQL{
	models.CampaignPlatformSMS: {
		sentRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    WHERE processed.campaign_id = ?`,
		deliveredRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_sms AS sent ON sent.processed_campaign_id = processed.id
    JOIN sms_status_results AS status
      ON status.processed_campaign_id = sent.processed_campaign_id
     AND status.tracking_id = sent.tracking_id
    WHERE processed.campaign_id = ?
      AND status.total_parts IS NOT NULL
      AND status.total_delivered_parts IS NOT NULL
      AND status.total_parts > 0
      AND status.total_parts = status.total_delivered_parts`,
	},
	models.CampaignPlatformBale: {
		sentRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE processed.campaign_id = ?`,
		deliveredRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_bale_messages AS sent ON sent.processed_campaign_id = processed.id
    JOIN bale_status_results AS status
      ON status.processed_campaign_id = sent.processed_campaign_id
     AND status.tracking_id = sent.tracking_id
    WHERE processed.campaign_id = ?
      AND status.total_parts IS NOT NULL
      AND status.total_delivered_parts IS NOT NULL
      AND status.total_parts > 0
      AND status.total_parts = status.total_delivered_parts`,
	},
	models.CampaignPlatformSPlus: {
		sentRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE processed.campaign_id = ?`,
		deliveredRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_splus_messages AS sent ON sent.processed_campaign_id = processed.id
    JOIN splus_status_results AS status
      ON status.processed_campaign_id = sent.processed_campaign_id
     AND status.tracking_id = sent.tracking_id
    WHERE processed.campaign_id = ?
      AND status.total_parts IS NOT NULL
      AND status.total_delivered_parts IS NOT NULL
      AND status.total_parts > 0
      AND status.total_parts = status.total_delivered_parts`,
	},
	models.CampaignPlatformRubika: {
		sentRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    WHERE processed.campaign_id = ?`,
		deliveredRecipients: `
    SELECT processed.bundle_audience_selection_id, sent.phone_number
    FROM processed_campaigns AS processed
    JOIN sent_rubika_messages AS sent ON sent.processed_campaign_id = processed.id
    JOIN rubika_status_results AS status
      ON status.processed_campaign_id = sent.processed_campaign_id
     AND status.tracking_id = sent.tracking_id
    WHERE processed.campaign_id = ?
      AND status.total_parts IS NOT NULL
      AND status.total_delivered_parts IS NOT NULL
      AND status.total_parts > 0
      AND status.total_parts = status.total_delivered_parts`,
	},
}

func recomputeCampaignTagPerformanceSQLForPlatform(platform string) (string, error) {
	source, ok := tagTestPerformanceSources[platform]
	if !ok {
		return "", fmt.Errorf("unsupported campaign platform %q", platform)
	}
	return fmt.Sprintf(`
WITH attributed AS (
    -- (campaign_id, audience_id) is unique, so this does not need a sort or
    -- DISTINCT ON before joining the campaign's delivery history.
    SELECT
        attribution.campaign_id,
        attribution.bundle_id,
        attribution.bundle_audience_selection_id,
        attribution.audience_id,
        attribution.assigned_tag_id AS tag_id,
        attribution.phase_type,
        profile.phone_number
    FROM campaign_audience_tag_attributions AS attribution
    JOIN campaigns AS source_campaign
      ON source_campaign.id = attribution.campaign_id
     AND source_campaign.bundle_id = attribution.bundle_id
    JOIN audience_profiles AS profile
      ON profile.id = attribution.audience_id
    WHERE attribution.campaign_id = ?
      AND attribution.phase_type = ?
),
sent_recipients AS (
%s
),
delivered_recipients AS (
%s
),
sent_attributed AS (
    SELECT DISTINCT attributed.audience_id
    FROM attributed
    JOIN sent_recipients AS sent
      ON sent.bundle_audience_selection_id = attributed.bundle_audience_selection_id
     AND sent.phone_number = attributed.phone_number
),
delivered_attributed AS (
    SELECT DISTINCT attributed.audience_id
    FROM attributed
    JOIN delivered_recipients AS delivered
      ON delivered.bundle_audience_selection_id = attributed.bundle_audience_selection_id
     AND delivered.phone_number = attributed.phone_number
),
clicked_attributed AS (
    SELECT DISTINCT attributed.audience_id
    FROM attributed
    JOIN short_link_clicks AS click
      ON click.campaign_id = attributed.campaign_id
     AND click.phone_number = attributed.phone_number
    WHERE click.uid IS NOT NULL
      AND %s
),
tag_stats AS (
    SELECT
        attributed.campaign_id,
        attributed.bundle_id,
        attributed.tag_id,
        attributed.phase_type,
        COALESCE(
            NULLIF(BTRIM(selected.tag_display_title_snapshot), ''),
            NULLIF(BTRIM(tag.display_title), ''),
            tag.name
        ) AS tag_display_title_snapshot,
        selected.bundle_persona_fit_score_snapshot,
        COUNT(*) AS selected_count,
        COUNT(sent.audience_id) AS sent_count,
        COUNT(delivered.audience_id) AS delivered_count,
        COUNT(clicked.audience_id) AS click_count
    FROM attributed
    JOIN tags AS tag ON tag.id = attributed.tag_id
    LEFT JOIN campaign_selected_tags AS selected
      ON selected.campaign_id = attributed.campaign_id
     AND selected.tag_id = attributed.tag_id
    LEFT JOIN sent_attributed AS sent ON sent.audience_id = attributed.audience_id
    LEFT JOIN delivered_attributed AS delivered ON delivered.audience_id = attributed.audience_id
    LEFT JOIN clicked_attributed AS clicked ON clicked.audience_id = attributed.audience_id
    GROUP BY
        attributed.campaign_id,
        attributed.bundle_id,
        attributed.tag_id,
        attributed.phase_type,
        selected.tag_display_title_snapshot,
        selected.bundle_persona_fit_score_snapshot,
        tag.display_title,
        tag.name
)
INSERT INTO campaign_tag_test_performances (
    campaign_id,
    bundle_id,
    tag_id,
    phase_type,
    tag_display_title_snapshot,
    bundle_persona_fit_score_snapshot,
    selected_count,
    sent_count,
    delivered_count,
    click_count,
    calculation_version,
    created_at,
    updated_at
)
SELECT
    campaign_id,
    bundle_id,
    tag_id,
    phase_type,
    tag_display_title_snapshot,
    bundle_persona_fit_score_snapshot,
    selected_count,
    sent_count,
    delivered_count,
    click_count,
    ?::integer,
    ?::timestamptz,
    ?::timestamptz
FROM tag_stats
ON CONFLICT (campaign_id, tag_id) DO UPDATE
SET bundle_id = EXCLUDED.bundle_id,
    phase_type = EXCLUDED.phase_type,
    tag_display_title_snapshot = EXCLUDED.tag_display_title_snapshot,
    bundle_persona_fit_score_snapshot = EXCLUDED.bundle_persona_fit_score_snapshot,
    selected_count = EXCLUDED.selected_count,
    sent_count = EXCLUDED.sent_count,
    delivered_count = EXCLUDED.delivered_count,
    click_count = EXCLUDED.click_count,
    calculation_version = EXCLUDED.calculation_version,
    updated_at = EXCLUDED.updated_at`, source.sentRecipients, source.deliveredRecipients, nonAutomatedClickTrafficSQL("click")), nil
}

// recomputeCampaignTagPerformanceSQL remains available to SQL-shape tests and
// represents the most common platform. Runtime code selects its source query
// from the campaign's persisted platform instead.
var recomputeCampaignTagPerformanceSQL, _ = recomputeCampaignTagPerformanceSQLForPlatform(models.CampaignPlatformSMS)
