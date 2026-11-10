package repository

import (
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestRecomputeCampaignTagPerformanceSQLUsesSingleAudienceAttribution(t *testing.T) {
	for _, fragment := range []string{
		"-- (campaign_id, audience_id) is unique",
		"SELECT\n        attribution.campaign_id",
		"attribution.assigned_tag_id AS tag_id",
		"attribution.phase_type",
		"source_campaign.bundle_id = attribution.bundle_id",
		"sent.bundle_audience_selection_id = attributed.bundle_audience_selection_id",
		"delivered.bundle_audience_selection_id = attributed.bundle_audience_selection_id",
		"status.total_parts > 0",
		"status.total_parts = status.total_delivered_parts",
		"SELECT DISTINCT attributed.audience_id",
		"click.phone_number = attributed.phone_number",
		"click.uid IS NOT NULL",
		"ON CONFLICT (campaign_id, tag_id) DO UPDATE",
		"phase_type = EXCLUDED.phase_type",
	} {
		if !strings.Contains(recomputeCampaignTagPerformanceSQL, fragment) {
			t.Fatalf("tag performance query does not contain %q", fragment)
		}
	}
	if strings.Contains(recomputeCampaignTagPerformanceSQL, "FOR UPDATE") {
		t.Fatal("tag performance query must not lock click or short-link rows")
	}
	if strings.Contains(recomputeCampaignTagPerformanceSQL, "UNION ALL") {
		t.Fatal("a platform-specific report query must not scan other delivery platforms")
	}
}

func TestRecomputeCampaignTagPerformanceSQLUsesOnlyRequestedPlatform(t *testing.T) {
	testCases := []struct {
		platform string
		included string
		excluded string
	}{
		{models.CampaignPlatformSMS, "sent_sms", "sent_bale_messages"},
		{models.CampaignPlatformBale, "sent_bale_messages", "sent_sms"},
		{models.CampaignPlatformSPlus, "sent_splus_messages", "sent_rubika_messages"},
		{models.CampaignPlatformRubika, "sent_rubika_messages", "sent_splus_messages"},
	}
	for _, tc := range testCases {
		t.Run(tc.platform, func(t *testing.T) {
			sql, err := recomputeCampaignTagPerformanceSQLForPlatform(tc.platform)
			if err != nil {
				t.Fatalf("build query: %v", err)
			}
			if !strings.Contains(sql, tc.included) {
				t.Fatalf("query for %s does not include %s", tc.platform, tc.included)
			}
			if strings.Contains(sql, tc.excluded) {
				t.Fatalf("query for %s unexpectedly includes %s", tc.platform, tc.excluded)
			}
			if got, want := strings.Count(sql, "?"), 7; got != want {
				t.Fatalf("query bind count = %d, want %d", got, want)
			}
		})
	}
	if _, err := recomputeCampaignTagPerformanceSQLForPlatform("unsupported"); err == nil {
		t.Fatal("unsupported platform built a report query")
	}
}

func TestClaimPendingTagTestReportsSQLDoesNotRetryTerminalFailures(t *testing.T) {
	if !strings.Contains(claimPendingTagTestReportsSQL, "calculation_version = ?") {
		t.Fatal("claim query does not isolate workers by calculation version")
	}
	if !strings.Contains(claimPendingTagTestReportsSQL, "status = 'failed' AND next_retry_at <= ?") {
		t.Fatal("claim query does not require a scheduled retry time for failed reports")
	}
	if strings.Contains(claimPendingTagTestReportsSQL, "next_retry_at IS NULL OR") {
		t.Fatal("claim query retries terminal failed reports")
	}
	if got, want := strings.Count(claimPendingTagTestReportsSQL, "?"), 6; got != want {
		t.Fatalf("claim query bind count = %d, want %d", got, want)
	}
}

func TestDiscoverPendingTagTestReportsSQLBindsSourcesAndCalculationVersion(t *testing.T) {
	if !strings.Contains(discoverPendingTagTestReportsSQL, "existing.calculation_version <> ?") {
		t.Fatal("discovery query does not enqueue stale calculation versions")
	}
	for _, fragment := range []string{"?::integer", "?::timestamptz"} {
		if !strings.Contains(discoverPendingTagTestReportsSQL, fragment) {
			t.Fatalf("discovery query does not cast its INSERT projection parameter %q", fragment)
		}
	}
	for _, fragment := range []string{
		"campaign.phase IN (?, ?)",
		"attribution.phase_type = campaign.phase",
	} {
		if !strings.Contains(discoverPendingTagTestReportsSQL, fragment) {
			t.Fatalf("discovery query does not include %q", fragment)
		}
	}
	if got, want := strings.Count(discoverPendingTagTestReportsSQL, "?"), 14; got != want {
		t.Fatalf("discovery query bind count = %d, want %d", got, want)
	}
}

func TestSummarySQLUsesWeightedTotalsAndStableUpsert(t *testing.T) {
	for _, fragment := range []string{
		"SUM(performance.delivered_count)",
		"SUM(performance.click_count)",
		"?::integer",
		"?::timestamptz",
		"ON CONFLICT (bundle_id, tag_id) DO UPDATE",
		"performance.phase_type = ?",
	} {
		if !strings.Contains(summarySQL, fragment) {
			t.Fatalf("summary query does not contain %q", fragment)
		}
	}
}

func TestOverallSummarySQLUsesWeightedTotalsAndStableUpsert(t *testing.T) {
	for _, fragment := range []string{
		"SUM(performance.selected_count)",
		"SUM(performance.sent_count)",
		"SUM(performance.delivered_count)",
		"SUM(performance.click_count)",
		"GROUP BY performance.tag_id",
		"ON CONFLICT (tag_id) DO UPDATE",
	} {
		if !strings.Contains(overallSummarySQL, fragment) {
			t.Fatalf("overall summary query does not contain %q", fragment)
		}
	}
}

func TestCampaignMaterializationDeletesTagsNoLongerAttributed(t *testing.T) {
	for _, fragment := range []string{
		"DELETE FROM campaign_tag_test_performances",
		"attribution.bundle_id = performance.bundle_id",
		"attribution.phase_type = performance.phase_type",
		"attribution.assigned_tag_id = performance.tag_id",
	} {
		if !strings.Contains(deleteStaleCampaignTagPerformancesSQL, fragment) {
			t.Fatalf("stale Campaign materialization query does not contain %q", fragment)
		}
	}
}

func TestRecomputeCampaignTagPerformanceSQLBindsEverySource(t *testing.T) {
	for _, fragment := range []string{"?::integer", "?::timestamptz"} {
		if !strings.Contains(recomputeCampaignTagPerformanceSQL, fragment) {
			t.Fatalf("tag performance query does not cast its INSERT projection parameter %q", fragment)
		}
	}
	// Campaign + phase, one send source, one delivery source, and three
	// materialization metadata values.
	if got, want := strings.Count(recomputeCampaignTagPerformanceSQL, "?"), 7; got != want {
		t.Fatalf("tag performance query bind count = %d, want %d", got, want)
	}
}
