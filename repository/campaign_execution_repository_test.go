package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestCampaignExecutionRecoveryTreatsEveryPlatformSendIntentAsNoReplayBoundary(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"sent_sms", "sent_bale_messages", "sent_rubika_messages", "sent_splus_messages"} {
		if !strings.Contains(campaignHasDeliveryRecordSQL, table) {
			t.Fatalf("recovery no-replay predicate is missing %s", table)
		}
	}
	if got := strings.Count(campaignHasDeliveryRecordSQL, "NULLIF(BTRIM(sent.phone_number), '') IS NOT NULL"); got != 4 {
		t.Fatalf("recovery must exclude all four empty-phone local audit row types, found %d safeguards", got)
	}
	if !models.CampaignStatusInterrupted.Valid() {
		t.Fatal("interrupted campaign status must be persisted as a valid status")
	}
}

func TestBundleCampaignAllocationsExcludeExplicitAndLegacyExcelTargeting(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	var rows []BundleCampaignAllocation
	statement := bundleCampaignAllocationsQuery(db, 44, 55).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build bundle allocation query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, required := range []string{
		"audience_targeting_method",
		"target_audience_excel_file_uuid",
		"case",
		"bundle_audience_selections",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("bundle allocation query does not contain %q:\n%s", required, sql)
		}
	}
	if strings.Contains(sql, "then 'excel'") || strings.Contains(sql, "<> 'excel'") {
		t.Fatalf("bundle allocation query interpolated targeting values:\n%s", sql)
	}

	excelArgs := 0
	for _, value := range statement.Vars {
		if text, ok := value.(string); ok && text == "excel" {
			excelArgs++
		}
	}
	if excelArgs != 3 {
		t.Fatalf("Excel targeting bind count = %d, want 3", excelArgs)
	}
}

func TestBundleActiveTestReservationsFingerprintQueryIsScopedAndStable(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	var rows []BundleActiveTestReservation
	statement := bundleActiveTestReservationsQuery(db, 44, 55).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build active Test reservation fingerprint query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, required := range []string{
		"campaign_targeting_test_sample_reservations", "state = 'active'", "campaign_id <>",
		"count(*) as audience_count", "group by campaign_id, selection_id", "order by campaign_id asc, selection_id asc",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("active Test reservation fingerprint query does not contain %q:\n%s", required, statement.SQL.String())
		}
	}
}

func TestBundleActiveExecutionReservationsIncludeCommittedCalculations(t *testing.T) {
	source, err := os.ReadFile("campaign_execution_repository.go")
	if err != nil {
		t.Fatalf("read execution repository source: %v", err)
	}
	for _, fragment := range []string{
		"campaign_targeting_execution_calculations",
		"campaign_targeting_execution_calculation_members",
		"calculation.status = 'committed'",
		"UNION ALL",
	} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("execution reservation fingerprint is missing %q", fragment)
		}
	}
}

func TestBundleCampaignAllocationsRetainInterruptedCampaignAudience(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	var rows []BundleCampaignAllocation
	statement := bundleCampaignAllocationsQuery(db, 44, 55).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build bundle allocation query: %v", statement.Error)
	}
	found := false
	for _, value := range statement.Vars {
		if status, ok := value.(models.CampaignStatus); ok && status == models.CampaignStatusInterrupted {
			found = true
			break
		}
		if statuses, ok := value.([]models.CampaignStatus); ok {
			for _, status := range statuses {
				if status == models.CampaignStatusInterrupted {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("interrupted campaigns must retain their allocation: %#v", statement.Vars)
	}
}
