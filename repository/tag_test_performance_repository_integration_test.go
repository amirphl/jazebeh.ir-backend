package repository

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestTagTestPerformanceRepositoryIntegration is intentionally opt-in because
// it needs a disposable database with the complete migration history applied.
// It exercises real PostgreSQL generated columns, attribution, deduplication,
// zero-delivery nullability, late updates, materialized reads, and idempotency.
func TestTagTestPerformanceRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_TAG_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_TAG_TEST_POSTGRES_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}

	seedTagTestPerformanceIntegration(t, db)
	repo := NewTagTestPerformanceRepository(db)
	lease := time.Date(2026, time.August, 17, 8, 0, 0, 0, time.UTC)
	calculatedAt := lease.Add(time.Minute)
	if err := repo.RecomputeCampaign(context.Background(), 1900000001, lease, calculatedAt); err != nil {
		t.Fatalf("first recomputation: %v", err)
	}

	assertTagTestPerformanceIntegration(t, db, 2, 1, 1, 1, 1, 1)

	// A late delivery for audience 2 changes the denominator. Extra click rows
	// for audience 1 must still count as one audience-level click.
	if err := db.Exec(`
UPDATE sms_status_results
SET total_parts = 1, total_delivered_parts = 1
WHERE processed_campaign_id = 1900000001 AND tracking_id = 'trk-a2'`).Error; err != nil {
		t.Fatalf("update late delivery: %v", err)
	}
	if err := db.Exec(`
INSERT INTO short_link_clicks (short_link_id, uid, campaign_id, phone_number, user_agent)
VALUES (1900000099, 'duplicate-a1', 1900000001, '+989000000001', 'Mobile Safari')`).Error; err != nil {
		t.Fatalf("insert late duplicate click: %v", err)
	}
	secondLease := lease.Add(2 * time.Minute)
	if err := db.Exec(`
UPDATE campaign_tag_test_reports
SET status = 'preparing', requested_at = ?, started_at = ?, finished_at = NULL,
    next_retry_at = NULL, updated_at = ?
WHERE campaign_id = 1900000001`, secondLease.Add(-time.Second), secondLease, secondLease).Error; err != nil {
		t.Fatalf("lease second recomputation: %v", err)
	}
	if err := repo.RecomputeCampaign(context.Background(), 1900000001, secondLease, secondLease.Add(time.Minute)); err != nil {
		t.Fatalf("second recomputation: %v", err)
	}

	assertTagTestPerformanceIntegration(t, db, 2, 2, 1, 0.5, 2, 0.5)

	// Materializing a second Campaign for Tag A must produce a weighted Bundle
	// result: (1 + 0) clicks / (2 + 1) deliveries = 1/3. Recomputing the first
	// Campaign again must replace, not add to, its contribution.
	thirdLease := lease.Add(4 * time.Minute)
	if err := db.Exec(`
UPDATE campaign_tag_test_reports
SET status = 'preparing', requested_at = ?, started_at = ?, finished_at = NULL,
    next_retry_at = NULL, updated_at = ?
WHERE campaign_id = 1900000002`, thirdLease.Add(-time.Second), thirdLease, thirdLease).Error; err != nil {
		t.Fatalf("lease second Campaign recomputation: %v", err)
	}
	if err := repo.RecomputeCampaign(context.Background(), 1900000002, thirdLease, thirdLease.Add(time.Minute)); err != nil {
		t.Fatalf("second Campaign recomputation: %v", err)
	}
	assertTagTestPerformanceIntegration(t, db, 2, 2, 1, 0.5, 3, 1.0/3.0)

	var secondPerformance models.CampaignTagTestPerformance
	if err := db.Where("campaign_id = ? AND tag_id = ?", 1900000002, 1900000001).
		First(&secondPerformance).Error; err != nil {
		t.Fatalf("read second Campaign performance: %v", err)
	}
	if secondPerformance.DeliveredCount != 1 || secondPerformance.ClickCount != 0 ||
		secondPerformance.TestCampaignCTR == nil || *secondPerformance.TestCampaignCTR != 0 {
		t.Fatalf("second Campaign performance = delivered %d, clicks %d, CTR %v", secondPerformance.DeliveredCount, secondPerformance.ClickCount, secondPerformance.TestCampaignCTR)
	}

	fourthLease := lease.Add(6 * time.Minute)
	if err := db.Exec(`
UPDATE campaign_tag_test_reports
SET status = 'preparing', requested_at = ?, started_at = ?, finished_at = NULL,
    next_retry_at = NULL, updated_at = ?
WHERE campaign_id = 1900000001`, fourthLease.Add(-time.Second), fourthLease, fourthLease).Error; err != nil {
		t.Fatalf("lease idempotency recomputation: %v", err)
	}
	if err := repo.RecomputeCampaign(context.Background(), 1900000001, fourthLease, fourthLease.Add(time.Minute)); err != nil {
		t.Fatalf("idempotency recomputation: %v", err)
	}
	assertTagTestPerformanceIntegration(t, db, 2, 2, 1, 0.5, 3, 1.0/3.0)
	assertTagTestPerformanceMaterializedAPIRead(t, db)

	var staleCount int64
	if err := db.Table("campaign_tag_test_performances").
		Where("campaign_id = ? AND tag_id = ?", 1900000001, 1900000004).
		Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale performance: %v", err)
	}
	if staleCount != 0 {
		t.Fatalf("stale unattributed tag rows = %d, want 0", staleCount)
	}

	// Feature 6 reuses the same materialization for Execution Campaigns. Adding
	// one delivered/clicked Tag A audience yields a global weighted CTR of
	// (1 Test + 1 Execution clicks) / (3 Test + 1 Execution deliveries) = 1/2.
	executionLease := lease.Add(8 * time.Minute)
	if err := repo.DiscoverPending(context.Background(), executionLease.Add(-time.Minute)); err != nil {
		t.Fatalf("discover Execution Campaign performance: %v", err)
	}
	var discoveredExecutionReport models.CampaignTagTestReport
	if err := db.First(&discoveredExecutionReport, "campaign_id = ?", 1900000003).Error; err != nil {
		t.Fatalf("read discovered Execution Campaign report: %v", err)
	}
	if discoveredExecutionReport.Status != models.TagTestReportStatusNotPrepared ||
		discoveredExecutionReport.CalculationVersion != models.TagTestPerformanceCalculationVersion {
		t.Fatalf("discovered Execution Campaign report is incorrect: %#v", discoveredExecutionReport)
	}
	if err := db.Exec(`
UPDATE campaign_tag_test_reports
SET status = 'preparing', requested_at = ?, started_at = ?, finished_at = NULL,
    next_retry_at = NULL, updated_at = ?
WHERE campaign_id = 1900000003`, executionLease.Add(-time.Second), executionLease, executionLease).Error; err != nil {
		t.Fatalf("lease Execution Campaign recomputation: %v", err)
	}
	if err := repo.RecomputeCampaign(context.Background(), 1900000003, executionLease, executionLease.Add(time.Minute)); err != nil {
		t.Fatalf("Execution Campaign recomputation: %v", err)
	}

	var executionPerformance models.CampaignTagTestPerformance
	if err := db.Where("campaign_id = ? AND tag_id = ?", 1900000003, 1900000001).
		First(&executionPerformance).Error; err != nil {
		t.Fatalf("read Execution Campaign performance: %v", err)
	}
	if executionPerformance.PhaseType != models.CampaignPhaseExecution ||
		executionPerformance.DeliveredCount != 1 || executionPerformance.ClickCount != 1 {
		t.Fatalf("Execution Campaign performance is incorrect: %#v", executionPerformance)
	}

	assertOverallTagPerformanceIntegration(t, db, 1900000001, 4, 4, 4, 2, 0.5)
	assertOverallTagPerformanceNullCTR(t, db, 1900000003)
	rows, _, err := NewCampaignSelectedTagRepository(db).ListAvailable(
		context.Background(), 1900000001, 1900000003, "", nil, "overall_avg_ctr", "desc", 10, 0,
	)
	if err != nil {
		t.Fatalf("read Execution tag table: %v", err)
	}
	if len(rows) == 0 || rows[0].TagID != 1900000001 || rows[0].OverallAvgCTR == nil ||
		!tagTestCTREqual(*rows[0].OverallAvgCTR, 0.5) || rows[0].TestCampaignCTR != nil || rows[0].DeliveredCount != nil {
		t.Fatalf("Execution tag API row is incorrect: %#v", rows)
	}

	var testSummary models.TagTestPhasePerformanceSummary
	if err := db.Where("bundle_id = ? AND tag_id = ?", 1900000001, 1900000001).First(&testSummary).Error; err != nil {
		t.Fatalf("read Test-only summary after Execution recomputation: %v", err)
	}
	if testSummary.TotalTestDeliveredCount != 3 || testSummary.TotalTestClickCount != 1 ||
		testSummary.TestPhaseAvgCTR == nil || !tagTestCTREqual(*testSummary.TestPhaseAvgCTR, 1.0/3.0) {
		t.Fatalf("Execution performance leaked into Test summary: %#v", testSummary)
	}
}

func seedTagTestPerformanceIntegration(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO customers (
            id, uuid, account_type_id, representative_first_name, representative_last_name,
            representative_mobile, email, password_hash
        ) VALUES (
            1900000001, 'f5000000-0000-4000-8000-000000000001', 1,
            'Feature', 'Test', '+989000000000',
            'feature5-integration@example.com', 'not-a-real-password'
        )`,
		`INSERT INTO bundles (id, title, customer_id)
         VALUES (1900000001, 'Feature 5 integration Bundle', 1900000001)`,
		`INSERT INTO tags (id, name, display_title, audience_count) VALUES
            (1900000001, 'feature5-tag-a', 'Tag A', 2),
            (1900000002, 'feature5-tag-b', 'Tag B', 1),
            (1900000003, 'feature5-tag-c', 'Tag C', 1),
            (1900000004, 'feature5-tag-stale', 'Stale tag', 0)`,
		`INSERT INTO campaigns (
            id, customer_id, status, spec, bundle_id, phase, num_audience
        ) VALUES
        (
            1900000001, 1900000001, 'approved',
            '{"audience_targeting_method":"smart_targeting","platform":"sms"}'::jsonb,
            1900000001, 'test', 4
        ),
        (
            1900000002, 1900000001, 'approved',
            '{"audience_targeting_method":"smart_targeting","platform":"sms"}'::jsonb,
            1900000001, 'test', 1
		),
		(
			1900000003, 1900000001, 'approved',
			'{"audience_targeting_method":"smart_targeting","platform":"sms"}'::jsonb,
			1900000001, 'execution', 1
        )`,
		`INSERT INTO campaign_selected_tags (
            campaign_id, bundle_id, tag_id, selection_order,
            bundle_persona_fit_score_snapshot, tag_display_title_snapshot,
            tag_audience_count_snapshot, selected_by_customer_id
        ) VALUES
            (1900000001, 1900000001, 1900000001, 0, 90, 'Tag A snapshot', 2, 1900000001),
            (1900000001, 1900000001, 1900000002, 1, 80, 'Tag B snapshot', 1, 1900000001),
			(1900000001, 1900000001, 1900000003, 2, 70, 'Tag C snapshot', 1, 1900000001),
			(1900000002, 1900000001, 1900000001, 0, 90, 'Tag A snapshot', 1, 1900000001),
			(1900000003, 1900000001, 1900000001, 0, 90, 'Tag A execution snapshot', 1, 1900000001)`,
		`INSERT INTO audience_profiles (id, uid, phone_number, tags, color) VALUES
            (1900000001, 'feature5-a1', '+989000000001', ARRAY[1900000001,1900000002], 'white'),
            (1900000002, 'feature5-a2', '+989000000002', ARRAY[1900000001], 'white'),
            (1900000003, 'feature5-b1', '+989000000003', ARRAY[1900000002], 'white'),
			(1900000004, 'feature5-c1', '+989000000004', ARRAY[1900000003], 'white'),
			(1900000005, 'feature5-a3', '+989000000005', ARRAY[1900000001], 'white'),
			(1900000006, 'feature6-a1', '+989000000006', ARRAY[1900000001,1900000002], 'white')`,
		`INSERT INTO bundle_audience_selections (
            id, customer_id, bundle_id, campaign_id, correlation_id, audience_count
        ) VALUES
        (
            1900000001, 1900000001, 1900000001, 1900000001,
            'feature5-integration-selection', 4
        ),
        (
            1900000002, 1900000001, 1900000001, 1900000002,
            'feature5-integration-selection-2', 1
		),
		(
			1900000003, 1900000001, 1900000001, 1900000003,
			'feature6-integration-selection', 1
        )`,
		`INSERT INTO bundle_audience_selection_members (
            selection_id, bundle_id, audience_id, selection_order
        ) VALUES
            (1900000001, 1900000001, 1900000001, 0),
            (1900000001, 1900000001, 1900000002, 1),
            (1900000001, 1900000001, 1900000003, 2),
			(1900000001, 1900000001, 1900000004, 3),
			(1900000002, 1900000001, 1900000005, 0),
			(1900000003, 1900000001, 1900000006, 0)`,
		`INSERT INTO campaign_audience_tag_attributions (
            campaign_id, bundle_id, bundle_audience_selection_id, audience_id,
            assigned_tag_id, phase_type, selection_method, selection_order
        ) VALUES
            (1900000001, 1900000001, 1900000001, 1900000001, 1900000001, 'test', 'random_per_tag', 0),
            (1900000001, 1900000001, 1900000001, 1900000002, 1900000001, 'test', 'random_per_tag', 1),
            (1900000001, 1900000001, 1900000001, 1900000003, 1900000002, 'test', 'random_per_tag', 2),
			(1900000001, 1900000001, 1900000001, 1900000004, 1900000003, 'test', 'random_per_tag', 3),
			(1900000002, 1900000001, 1900000002, 1900000005, 1900000001, 'test', 'random_per_tag', 0),
			(1900000003, 1900000001, 1900000003, 1900000006, 1900000001, 'execution', 'score_desc', 0)`,
		`INSERT INTO processed_campaigns (
            id, campaign_id, campaign_json, audience_ids, audience_codes,
            statistics, bundle_audience_selection_id
        ) VALUES
        (
            1900000001, 1900000001, '{"bundle_id":1900000001,"platform":"sms"}'::jsonb,
            ARRAY[1900000001,1900000002,1900000003,1900000004],
            ARRAY['feature5-a1','feature5-a2','feature5-b1','feature5-c1'],
            '{}'::jsonb, 1900000001
        ),
        (
            1900000002, 1900000002, '{"bundle_id":1900000001,"platform":"sms"}'::jsonb,
            ARRAY[1900000005], ARRAY['feature5-a3'], '{}'::jsonb, 1900000002
		),
		(
			1900000003, 1900000003, '{"bundle_id":1900000001,"platform":"sms"}'::jsonb,
			ARRAY[1900000006], ARRAY['feature6-a1'], '{}'::jsonb, 1900000003
        )`,
		`INSERT INTO sent_sms (processed_campaign_id, phone_number, tracking_id, status) VALUES
            (1900000001, '+989000000001', 'trk-a1', 'successful'),
            (1900000001, '+989000000001', 'trk-a1', 'successful'),
            (1900000001, '+989000000002', 'trk-a2', 'successful'),
            (1900000001, '+989000000003', 'trk-b1', 'successful'),
			(1900000001, '+989000000004', 'trk-c1', 'successful'),
			(1900000002, '+989000000005', 'trk-a3', 'successful'),
			(1900000003, '+989000000006', 'trk-exec-a1', 'successful')`,
		`INSERT INTO campaign_status_jobs (
            id, processed_campaign_id, correlation_id, tracking_ids,
            scheduled_at, platform
        ) VALUES
        (
            1900000001, 1900000001, 'feature5-status-job',
            ARRAY['trk-a1','trk-a2','trk-b1','trk-c1'], CURRENT_TIMESTAMP, 'sms'
        ),
        (
            1900000002, 1900000002, 'feature5-status-job-2',
            ARRAY['trk-a3'], CURRENT_TIMESTAMP, 'sms'
		),
		(
			1900000003, 1900000003, 'feature6-status-job',
			ARRAY['trk-exec-a1'], CURRENT_TIMESTAMP, 'sms'
        )`,
		`INSERT INTO sms_status_results (
            job_id, processed_campaign_id, tracking_id, total_parts,
            total_delivered_parts, total_undelivered_parts, total_unknown_parts
        ) VALUES
            (1900000001, 1900000001, 'trk-a1', 2, 2, 0, 0),
            (1900000001, 1900000001, 'trk-a2', 0, 0, 0, 0),
			(1900000001, 1900000001, 'trk-b1', 1, 1, 0, 0),
			(1900000002, 1900000002, 'trk-a3', 1, 1, 0, 0),
			(1900000003, 1900000003, 'trk-exec-a1', 1, 1, 0, 0)`,
		`INSERT INTO short_link_clicks (
            short_link_id, uid, campaign_id, phone_number, user_agent, ip
        ) VALUES
            (1900000001, 'click-a1-1', 1900000001, '+989000000001', 'Mobile Safari', '1.1.1.1'),
            (1900000002, 'click-a1-2', 1900000001, '+989000000001', 'Mobile Safari', '1.1.1.1'),
            (1900000003, NULL, 1900000001, '+989000000002', 'Mobile Safari', '1.1.1.2'),
			(1900000004, 'bot-a2', 1900000001, '+989000000002', 'Mobile Safari', '66.249.1.1'),
			(1900000005, 'click-exec-a1', 1900000003, '+989000000006', 'Mobile Safari', '1.1.1.6')`,
		`INSERT INTO campaign_tag_test_reports (
            campaign_id, bundle_id, status, calculation_version, attempt_count,
            requested_at, started_at
        ) VALUES
        (
			1900000001, 1900000001, 'preparing', 2, 1,
            TIMESTAMPTZ '2026-08-17 07:59:00+00', TIMESTAMPTZ '2026-08-17 08:00:00+00'
        ),
        (
			1900000002, 1900000001, 'not_prepared', 2, 0,
            TIMESTAMPTZ '2026-08-17 07:59:00+00', NULL
        )`,
		`INSERT INTO campaign_tag_test_performances (
			campaign_id, bundle_id, tag_id, phase_type, tag_display_title_snapshot,
            selected_count, sent_count, delivered_count, click_count,
            calculation_version
        ) VALUES (
			1900000001, 1900000001, 1900000004, 'test', 'stale', 0, 0, 0, 0, 2
        )`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed integration database: %v\nSQL: %s", err, statement)
		}
	}
}

func assertTagTestPerformanceIntegration(
	t *testing.T,
	db *gorm.DB,
	wantSelected, wantDelivered, wantClicks int64,
	wantCampaignCTR float64,
	wantSummaryDelivered int64,
	wantSummaryCTR float64,
) {
	t.Helper()
	var performance models.CampaignTagTestPerformance
	if err := db.Where("campaign_id = ? AND tag_id = ?", 1900000001, 1900000001).
		First(&performance).Error; err != nil {
		t.Fatalf("read Tag A Campaign performance: %v", err)
	}
	if performance.SelectedCount != wantSelected || performance.SentCount != wantSelected ||
		performance.DeliveredCount != wantDelivered || performance.ClickCount != wantClicks {
		t.Fatalf(
			"Tag A counts = selected %d, sent %d, delivered %d, clicks %d; want %d/%d/%d/%d",
			performance.SelectedCount,
			performance.SentCount,
			performance.DeliveredCount,
			performance.ClickCount,
			wantSelected,
			wantSelected,
			wantDelivered,
			wantClicks,
		)
	}
	if performance.TestCampaignCTR == nil || !tagTestCTREqual(*performance.TestCampaignCTR, wantCampaignCTR) {
		t.Fatalf("Tag A Campaign CTR = %v, want %v", performance.TestCampaignCTR, wantCampaignCTR)
	}

	var zeroDelivery models.CampaignTagTestPerformance
	if err := db.Where("campaign_id = ? AND tag_id = ?", 1900000001, 1900000003).
		First(&zeroDelivery).Error; err != nil {
		t.Fatalf("read zero-delivery Campaign performance: %v", err)
	}
	if zeroDelivery.TestCampaignCTR != nil {
		t.Fatalf("zero-delivery Campaign CTR = %v, want nil", *zeroDelivery.TestCampaignCTR)
	}

	var summary models.TagTestPhasePerformanceSummary
	if err := db.Where("bundle_id = ? AND tag_id = ?", 1900000001, 1900000001).
		First(&summary).Error; err != nil {
		t.Fatalf("read Tag A summary: %v", err)
	}
	if summary.TotalTestDeliveredCount != wantSummaryDelivered || summary.TotalTestClickCount != wantClicks ||
		summary.TestPhaseAvgCTR == nil || !tagTestCTREqual(*summary.TestPhaseAvgCTR, wantSummaryCTR) {
		t.Fatalf(
			"Tag A summary = delivered %d, clicks %d, CTR %v; want %d/%d/%v",
			summary.TotalTestDeliveredCount,
			summary.TotalTestClickCount,
			summary.TestPhaseAvgCTR,
			wantSummaryDelivered,
			wantClicks,
			wantSummaryCTR,
		)
	}

	var report models.CampaignTagTestReport
	if err := db.First(&report, "campaign_id = ?", 1900000001).Error; err != nil {
		t.Fatalf("read report state: %v", err)
	}
	if report.Status != models.TagTestReportStatusPrepared || report.FinishedAt == nil {
		t.Fatalf("report state = %q, finished_at %v", report.Status, report.FinishedAt)
	}
}

func tagTestCTREqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}

func assertOverallTagPerformanceIntegration(
	t *testing.T,
	db *gorm.DB,
	tagID uint,
	wantSelected, wantSent, wantDelivered, wantClicks int64,
	wantCTR float64,
) {
	t.Helper()
	var summary models.TagOverallPerformanceSummary
	if err := db.First(&summary, "tag_id = ?", tagID).Error; err != nil {
		t.Fatalf("read overall tag performance: %v", err)
	}
	if summary.TotalSelectedCount != wantSelected || summary.TotalSentCount != wantSent ||
		summary.TotalDeliveredCount != wantDelivered || summary.TotalClickCount != wantClicks ||
		summary.OverallAvgCTR == nil || !tagTestCTREqual(*summary.OverallAvgCTR, wantCTR) {
		t.Fatalf(
			"overall tag performance = selected %d, sent %d, delivered %d, clicks %d, CTR %v; want %d/%d/%d/%d/%v",
			summary.TotalSelectedCount,
			summary.TotalSentCount,
			summary.TotalDeliveredCount,
			summary.TotalClickCount,
			summary.OverallAvgCTR,
			wantSelected,
			wantSent,
			wantDelivered,
			wantClicks,
			wantCTR,
		)
	}
}

func assertOverallTagPerformanceNullCTR(t *testing.T, db *gorm.DB, tagID uint) {
	t.Helper()
	var summary models.TagOverallPerformanceSummary
	if err := db.First(&summary, "tag_id = ?", tagID).Error; err != nil {
		t.Fatalf("read zero-delivery overall tag performance: %v", err)
	}
	if summary.TotalDeliveredCount != 0 || summary.OverallAvgCTR != nil {
		t.Fatalf("zero-delivery overall tag performance = %#v, want null CTR", summary)
	}
}

func assertTagTestPerformanceMaterializedAPIRead(t *testing.T, db *gorm.DB) {
	t.Helper()
	repo := NewCampaignSelectedTagRepository(db)
	rows, total, err := repo.ListAvailable(
		context.Background(),
		1900000001,
		1900000001,
		"",
		nil,
		"test_phase_avg_ctr",
		"desc",
		10,
		0,
	)
	if err != nil {
		t.Fatalf("read materialized Smart Targeting tags: %v", err)
	}
	if total != 4 || len(rows) != 4 {
		t.Fatalf("materialized Smart Targeting tags = total %d, rows %d; want 4/4", total, len(rows))
	}
	if rows[0].TagID != 1900000001 || rows[0].TestPhaseAvgCTR == nil ||
		!tagTestCTREqual(*rows[0].TestPhaseAvgCTR, 1.0/3.0) ||
		rows[0].TestCampaignCTR == nil || !tagTestCTREqual(*rows[0].TestCampaignCTR, 0.5) ||
		rows[0].TotalTestDeliveredCount == nil || *rows[0].TotalTestDeliveredCount != 3 ||
		rows[0].DeliveredCount == nil || *rows[0].DeliveredCount != 2 {
		t.Fatalf("Tag A materialized API row is incorrect: %#v", rows[0])
	}
	if rows[1].TagID != 1900000002 || rows[1].TestPhaseAvgCTR == nil || *rows[1].TestPhaseAvgCTR != 0 {
		t.Fatalf("zero-click Tag B did not sort after Tag A: %#v", rows[1])
	}
	if rows[2].TagID != 1900000003 || rows[2].TestPhaseAvgCTR != nil ||
		rows[2].TestCampaignCTR != nil || rows[2].DeliveredCount == nil || *rows[2].DeliveredCount != 0 {
		t.Fatalf("zero-delivery Tag C nullability is incorrect: %#v", rows[2])
	}
	if rows[3].TagID != 1900000004 || rows[3].TestPhaseAvgCTR != nil || rows[3].Selected {
		t.Fatalf("unattributed Tag row is incorrect: %#v", rows[3])
	}
}
