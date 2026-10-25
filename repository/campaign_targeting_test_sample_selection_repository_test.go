package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

func TestActiveTestSampleReservationClaimLocksAllActiveRows(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	var rows []models.CampaignTargetingTestSampleReservation
	statement := activeReservationsForUpdateQuery(db, 17, 31).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build active reservation claim query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"campaign_id", "selection_id", "state = 'active'", "for update"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("active reservation claim query is missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
}

func TestReserveTestSampleSelectionChecksCompositeKeyBundleExclusions(t *testing.T) {
	// bundle_audience_exclusions is keyed by (bundle_id, audience_id), so it
	// intentionally has no surrogate id column. Keep the availability query
	// aligned with that schema; this path is shared by Test sampling and admin
	// approval.
	if !strings.Contains(testSampleSelectionAvailabilityQuery, "excluded.audience_id IS NOT NULL") {
		t.Fatalf("reservation availability query must test the exclusion composite key:\n%s", testSampleSelectionAvailabilityQuery)
	}
	if strings.Contains(testSampleSelectionAvailabilityQuery, "excluded.id IS NOT NULL") {
		t.Fatalf("reservation availability query references nonexistent bundle exclusion id column:\n%s", testSampleSelectionAvailabilityQuery)
	}
}

func TestReserveTestSampleSelectionRechecksCurrentTargetingEligibility(t *testing.T) {
	query := strings.ToLower(testSampleSelectionAvailabilityQuery)
	for _, fragment := range []string{
		"campaign_targeting_capacity_calculations",
		"audience.tags @> array[member.assigned_tag_id]::integer[]",
		"calculation.allowed_colors",
		"audience.normalized_score is distinct from member.audience_score",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("reservation availability query is missing current targeting validation %q:\n%s", fragment, testSampleSelectionAvailabilityQuery)
		}
	}
}

func TestMaterializeTestSampleReservationsRequiresEveryExpectedActiveRow(t *testing.T) {
	for _, tt := range []struct {
		name     string
		expected int64
		affected int64
		wantErr  bool
	}{
		{name: "exact", expected: 2, affected: 2},
		{name: "zero expected", expected: 0, affected: 0, wantErr: true},
		{name: "released before materialization", expected: 2, affected: 0, wantErr: true},
		{name: "partial materialization", expected: 2, affected: 1, wantErr: true},
		{name: "unexpected extra rows", expected: 2, affected: 3, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := requireExpectedReservationRows(tt.expected, tt.affected)
			if (err != nil) != tt.wantErr {
				t.Fatalf("requireExpectedReservationRows(%d, %d) error = %v, want error=%t", tt.expected, tt.affected, err, tt.wantErr)
			}
		})
	}
}

func TestMaterializeTestSampleReservationQueryOnlyUpdatesActiveRows(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	now := time.Date(2026, time.August, 16, 10, 0, 0, 0, time.UTC)
	statement := materializeActiveReservationsQuery(db, 17, 31).
		Updates(map[string]any{"state": "materialized", "materialized_at": now}).Statement
	if statement.Error != nil {
		t.Fatalf("build materialization query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"update", "campaign_id", "selection_id", "state = 'active'"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("materialization query is missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
}

func TestActiveTestReservationBundleIDsQueryOnlyFindsActiveCampaignReservationsInLockOrder(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	var rows []models.CampaignTargetingTestSampleReservation
	statement := activeTestReservationBundleIDsQuery(db, 17).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build active reservation Bundle lookup query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"distinct", "bundle_id", "campaign_id", "state = 'active'", "order by bundle_id"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("active reservation Bundle lookup query is missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
	if strings.Contains(sql, "limit") {
		t.Fatalf("active reservation Bundle lookup must lock every affected Bundle, not only the first:\n%s", statement.SQL.String())
	}
}

func TestReleaseTestSampleReservationQueryOnlyReleasesActiveRows(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	now := time.Date(2026, time.August, 16, 10, 0, 0, 0, time.UTC)
	statement := releaseActiveTestReservationsQuery(db, 17).
		Updates(map[string]any{"state": "released", "released_at": now}).Statement
	if statement.Error != nil {
		t.Fatalf("build release query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"update", "campaign_id", "state = 'active'", "released_at"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("release query is missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
	if strings.Contains(sql, "materialized_at") {
		t.Fatalf("release query must not modify materialized reservations:\n%s", statement.SQL.String())
	}
}
