package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestExecutionReservationActiveLookupLocksAndPreservesOrder(t *testing.T) {
	db := newAudienceProfileDryRunDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})
	var rows []models.CampaignTargetingExecutionReservation
	statement := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("campaign_id = ? AND state = 'active'", 17).
		Order("selection_order ASC").Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build execution reservation lookup: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, fragment := range []string{"campaign_targeting_execution_reservations", "campaign_id", "state = 'active'", "selection_order asc", "for update"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("execution reservation lookup missing %q:\n%s", fragment, statement.SQL.String())
		}
	}
}

func TestExecutionReservationHeaderRequiresTheCurrentSchemaVersion(t *testing.T) {
	bundleID := uint(3)
	campaign := &models.Campaign{ID: 17, BundleID: &bundleID}
	header := &models.CampaignTargetingExecutionReservationHeader{
		CampaignID: 17, BundleID: 3, Phase: models.CampaignPhaseExecution,
		ReservationVersion:     models.SmartTargetingExecutionReservationSchemaVersion,
		RequestedAudienceCount: 1, CandidateGeneration: models.SmartTargetingCapacityAlgorithmVersion,
		SelectionInputVersion: models.SmartTargetingExecutionReservationSelectionInputVersion,
		SelectionInputHash:    strings.Repeat("a", 64), AllocationFingerprint: strings.Repeat("b", 64), RequestSnapshot: []byte(`{"tag_ids":[9],"score_classes":["A"]}`),
		AllocationFingerprintVersion: models.SmartTargetingExecutionReservationAllocationFingerprintVersion,
	}
	if !validExecutionReservationHeader(header, campaign, 1) {
		t.Fatal("well-formed current-version header was rejected")
	}
	header.ReservationVersion++
	if validExecutionReservationHeader(header, campaign, 1) {
		t.Fatal("unsupported header version was accepted")
	}
}

func TestExecutionReservationHeaderRejectsUnsupportedMetadataVersions(t *testing.T) {
	bundleID := uint(3)
	campaign := &models.Campaign{ID: 17, BundleID: &bundleID}
	header := &models.CampaignTargetingExecutionReservationHeader{
		CampaignID: 17, BundleID: 3, Phase: models.CampaignPhaseExecution,
		ReservationVersion:     models.SmartTargetingExecutionReservationSchemaVersion,
		RequestedAudienceCount: 1, CandidateGeneration: models.SmartTargetingCapacityAlgorithmVersion,
		SelectionInputVersion:        models.SmartTargetingExecutionReservationSelectionInputVersion,
		SelectionInputHash:           strings.Repeat("a", 64),
		AllocationFingerprintVersion: models.SmartTargetingExecutionReservationAllocationFingerprintVersion,
		AllocationFingerprint:        strings.Repeat("b", 64), RequestSnapshot: []byte(`{"tag_ids":[9],"score_classes":["A"]}`),
	}
	header.SelectionInputVersion++
	if validExecutionReservationHeader(header, campaign, 1) {
		t.Fatal("unsupported selection-input version was accepted")
	}
	header.SelectionInputVersion = models.SmartTargetingExecutionReservationSelectionInputVersion
	header.AllocationFingerprintVersion++
	if validExecutionReservationHeader(header, campaign, 1) {
		t.Fatal("unsupported allocation-fingerprint version was accepted")
	}
}

func TestExecutionReservationAvailabilityRequiresTheAssignedTag(t *testing.T) {
	query := strings.ToLower(executionReservationAvailabilityQuery)
	for _, required := range []string{
		"unnest(?::bigint[], ?::bigint[]) as requested(audience_id, assigned_tag_id)",
		"audience.tags @> array[requested.assigned_tag_id]::integer[]",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("execution reservation validation is missing %q:\n%s", required, executionReservationAvailabilityQuery)
		}
	}
}

func TestExecutionReservationScoreLocksUseAStableAudienceOrder(t *testing.T) {
	// Keep overlapping reservations from different Bundles from taking profile
	// row locks in different orders.
	source, err := os.ReadFile("campaign_targeting_execution_reservation_repository.go")
	if err != nil {
		t.Fatalf("read reservation repository source: %v", err)
	}
	if !strings.Contains(string(source), "WHERE id = ANY(?::bigint[]) ORDER BY id FOR UPDATE") {
		t.Fatal("execution reservation profile locks must be ordered by audience ID")
	}
}
