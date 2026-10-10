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

func TestActiveTestReservationBundleQueryOnlyFindsActiveCampaignReservation(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	var rows []models.CampaignTargetingTestSampleReservation
	statement := activeTestReservationBundleQuery(db, 17).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build active reservation Bundle lookup query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"\"bundle_id\"", "campaign_id", "state = 'active'", "order by bundle_id", "limit"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("active reservation Bundle lookup query is missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
}
