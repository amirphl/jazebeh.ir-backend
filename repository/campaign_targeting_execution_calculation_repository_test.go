package repository

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionCalculationLatestLookupsAreScopedAndStable(t *testing.T) {
	source, err := os.ReadFile("campaign_targeting_execution_calculation_repository.go")
	if err != nil {
		t.Fatalf("read execution calculation repository: %v", err)
	}
	text := string(source)
	for _, fragment := range []string{
		"LatestByCampaignID", "LatestByInput", "selection_input_hash = ? AND requested_audience_count = ?",
		"Order(\"created_at DESC, id DESC\")",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("execution calculation lookup is missing %q", fragment)
		}
	}
}

func TestCommittedExecutionClaimMigrationGuardsEveryBundleClaimPath(t *testing.T) {
	source, err := os.ReadFile("../migrations/0170_guard_committed_execution_audience_claims.sql")
	if err != nil {
		t.Fatalf("read committed execution claim migration: %v", err)
	}
	text := strings.ToLower(string(source))
	for _, fragment := range []string{
		"guard_committed_execution_calculation_claim",
		"pg_advisory_xact_lock(845172, new.bundle_id)",
		"bundle_audience_selection_members",
		"campaign_targeting_test_sample_reservations",
		"campaign_targeting_execution_reservations",
		"committed.status = 'committed'",
		"trg_guard_committed_execution_calculation_claim",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("committed execution claim migration is missing %q", fragment)
		}
	}
}

func TestExcelBundleAudienceDuplicateMigrationKeepsSmartClaimsExclusiveWithinSmartTargeting(t *testing.T) {
	source, err := os.ReadFile("../migrations/0171_allow_excel_bundle_audience_duplicates.sql")
	if err != nil {
		t.Fatalf("read Excel duplicate migration: %v", err)
	}
	sql := string(source)
	for _, fragment := range []string{
		"DROP CONSTRAINT IF EXISTS uk_bundle_aud_sel_member_bundle_audience",
		"idx_bundle_aud_sel_member_bundle_audience",
		"owner_is_excel",
		"audience_targeting_method",
		"campaign_targeting_execution_calculation_members",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("Excel duplicate migration is missing %q", fragment)
		}
	}
}

func TestExecutionCalculationCommitChecksConcreteMembersForAllocationConflicts(t *testing.T) {
	query := strings.ToLower(executionCalculationCommitConflictQuery)
	for _, fragment := range []string{
		"candidate.status = 'ready'",
		"bundle_audience_selection_members",
		"bundle_audience_selections",
		"audience_targeting_method",
		"campaign_targeting_test_sample_reservations",
		"campaign_targeting_execution_reservations",
		"committed.status = 'committed'",
		"bundle_audience_exclusions",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("execution commit conflict query is missing %q:\n%s", fragment, executionCalculationCommitConflictQuery)
		}
	}
	if strings.Contains(query, "audience_profiles") {
		t.Fatalf("execution commit must not revalidate immutable audience profiles:\n%s", executionCalculationCommitConflictQuery)
	}
}
