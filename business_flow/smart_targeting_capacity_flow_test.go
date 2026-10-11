package businessflow

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/lib/pq"
)

func TestNormalizeSmartTargetingScoreClasses(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
		err   bool
	}{
		{name: "omitted means all", want: []string{"A", "B", "C"}},
		{name: "canonical sort and case", input: []string{"c", "A"}, want: []string{"A", "C"}},
		{name: "duplicate rejected", input: []string{"A", "a"}, err: true},
		{name: "unknown rejected", input: []string{"D"}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSmartTargetingScoreClasses(tt.input)
			if tt.err {
				if !errors.Is(err, ErrSmartTargetingScoreClassesInvalid) {
					t.Fatalf("error = %v, want score class error", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalize = (%v, %v), want %v", got, err, tt.want)
			}
		})
	}
}

func TestSmartTargetingCapacityHashIsOrderIndependent(t *testing.T) {
	first := smartTargetingTagHash(18, []uint{2, 7, 9})
	second := smartTargetingTagHash(18, []uint{9, 2, 7})
	if first != second {
		t.Fatal("equal canonical tag selections must produce equal hashes")
	}
	if first == smartTargetingTagHash(19, []uint{2, 7, 9}) {
		t.Fatal("campaign ID must participate in the tag hash")
	}
	if smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformSMS, false) == smartTargetingInputHash(first, []string{"A", "C"}, models.CampaignPlatformSMS, false) {
		t.Fatal("score classes must participate in the input hash")
	}
	if smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformSMS, false) != smartTargetingInputHash(first, []string{"A", "B"}, " SMS ", false) {
		t.Fatal("equal targeting inputs must produce equal hashes")
	}
	if smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformSMS, false) == smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformBale, false) {
		t.Fatal("platform must participate in the input hash")
	}
	if smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformSMS, false) == smartTargetingInputHash(first, []string{"A", "B"}, models.CampaignPlatformSMS, true) {
		t.Fatal("Bundle-exclusion eligibility must participate in the input hash")
	}
}

func TestSmartTargetingScoreClassBoundaries(t *testing.T) {
	p33, p66 := 10.0, 20.0
	for _, tt := range []struct {
		score *float64
		want  string
	}{
		{score: nil, want: "unscored"},
		{score: floatPtr(10), want: "C"},
		{score: floatPtr(11), want: "B"},
		{score: floatPtr(20), want: "B"},
		{score: floatPtr(21), want: "A"},
	} {
		if got := smartTargetingScoreClass(tt.score, &p33, &p66); got != tt.want {
			t.Fatalf("score class = %q, want %q", got, tt.want)
		}
	}
	// Equal percentile boundaries intentionally leave class B empty: values at
	// the shared boundary are C and values above it are A.
	if got := smartTargetingScoreClass(floatPtr(10), &p33, &p33); got != "C" {
		t.Fatalf("equal-boundary score class = %q, want C", got)
	}
	if got := smartTargetingScoreClass(floatPtr(11), &p33, &p33); got != "A" {
		t.Fatalf("equal-boundary score class = %q, want A", got)
	}
}

func TestSmartTargetingCapacityAudienceQueryUsesSnapshotEligibility(t *testing.T) {
	calculation := &models.CampaignTargetingCapacityCalculation{
		CampaignID:                    17,
		BundleID:                      3,
		Platform:                      models.CampaignPlatformSMS,
		ApplyBundleAudienceExclusions: true,
		SelectedTagIDs:                pq.Int64Array{2, 9},
		SelectedScoreClasses:          pq.StringArray{"A", "C"},
	}
	query := smartTargetingCapacityAudienceQuery(calculation)
	if query.BundleID != 3 || query.ExcludeActiveTestReservationCampaignID != 17 || !query.ApplyBundleAudienceExclusions || !reflect.DeepEqual(query.TagIDs, []int64{2, 9}) || !reflect.DeepEqual(query.ScoreClasses, []string{"A", "C"}) || !reflect.DeepEqual(query.AllowedColors, []string{"white", "pink"}) {
		t.Fatalf("capacity audience query = %#v", query)
	}
}

func TestCurrentSmartTargetingCapacityRejectsUnavailableSelectedTags(t *testing.T) {
	now := time.Now().UTC()
	bundleID := uint(3)
	method := models.CampaignAudienceTargetingSmart
	campaign := &models.Campaign{
		ID: 17, BundleID: &bundleID,
		Spec: models.CampaignSpec{AudienceTargetingMethod: &method, AudienceGrades: []string{"A", "B", "C"}},
	}
	calculation := &models.CampaignTargetingCapacityCalculation{
		CampaignID: 17, BundleID: bundleID, Status: models.CampaignTargetingCapacityCalculated,
		ExpiresAt: ptrTime(now.Add(time.Hour)),
	}
	selectionRepo := &samplingSelectedTagRepositoryStub{
		selected:    []*models.CampaignSelectedTag{{CampaignID: 17, BundleID: bundleID, TagID: 9}},
		validateErr: repository.ErrInvalidCampaignSelectedTags,
	}
	current, err := isCurrentSmartTargetingCapacity(t.Context(), nil, selectionRepo, calculation, campaign)
	if err != nil || current {
		t.Fatalf("current capacity = (%t, %v), want unavailable selection to be stale", current, err)
	}
}

func TestSmartTargetingCapacityAppliesBundleExclusionsOnlyToTestPhase(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	campaign := &models.Campaign{
		Phase: models.CampaignPhaseTest,
		Spec:  models.CampaignSpec{AudienceTargetingMethod: &method},
	}
	if !smartTargetingCapacityAppliesBundleExclusions(campaign) {
		t.Fatal("Smart Targeting Test capacity must apply Bundle exclusions")
	}
	campaign.Phase = models.CampaignPhaseExecution
	if smartTargetingCapacityAppliesBundleExclusions(campaign) {
		t.Fatal("Smart Targeting execution capacity must not apply Test-only Bundle exclusions")
	}
}

func TestCapacityDTODoesNotExposeStaleCounts(t *testing.T) {
	now := time.Now().UTC()
	row := &models.CampaignTargetingCapacityCalculation{
		ID: 7, CampaignID: 3, BundleID: 4, Status: models.CampaignTargetingCapacityCalculated,
		SelectedScoreClasses: pq.StringArray{"A", "B", "C"}, SelectedTagCount: 2,
		RawAudienceCount: 100, EligibleUniqueAudienceCount: 80, ApprovedCampaignDeduction: 90,
		UsableUniqueAudienceCount: 0, CreatedAt: now,
	}
	stale := capacityDTO(row, false, true)
	if stale.Status != "stale" || !stale.RecalculationRequired || stale.UsableUniqueCount != nil || stale.RawAudienceCount != nil {
		t.Fatalf("stale response leaked a valid result: %#v", stale)
	}
	current := capacityDTO(row, true, false)
	if current.UsableUniqueCount == nil || *current.UsableUniqueCount != 0 || current.ApprovedDeduction == nil || *current.ApprovedDeduction != 90 {
		t.Fatalf("current zero result must remain distinguishable from unavailable: %#v", current)
	}
}

func TestCapacityDTOStatesKeepPendingAndFailedCountsHidden(t *testing.T) {
	now := time.Now().UTC()
	message := "failed"
	for _, tt := range []struct {
		name   string
		status models.CampaignTargetingCapacityCalculationStatus
		want   string
	}{
		{name: "pending", status: models.CampaignTargetingCapacityCalculating, want: "calculating"},
		{name: "failed", status: models.CampaignTargetingCapacityFailed, want: "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := &models.CampaignTargetingCapacityCalculation{
				ID: 1, Status: tt.status, RawAudienceCount: 100, EligibleUniqueAudienceCount: 80,
				UsableUniqueAudienceCount: 70, ErrorMessage: &message, CreatedAt: now,
			}
			response := capacityDTO(row, false, false)
			if response.Status != tt.want || response.RawAudienceCount != nil || response.UsableUniqueCount != nil {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestCanCalculateSmartTargetingCapacityAcrossPreExecutionLifecycle(t *testing.T) {
	allowed := []models.CampaignStatus{
		models.CampaignStatusInitiated,
		models.CampaignStatusInProgress,
		models.CampaignStatusWaitingForApproval,
		models.CampaignStatusApproved,
	}
	for _, status := range allowed {
		if !canCalculateSmartTargetingCapacity(&models.Campaign{Status: status}) {
			t.Fatalf("status %q must allow exact-capacity refresh", status)
		}
	}
	for _, status := range []models.CampaignStatus{models.CampaignStatusRunning, models.CampaignStatusExecuted, models.CampaignStatusCancelled} {
		if canCalculateSmartTargetingCapacity(&models.Campaign{Status: status}) {
			t.Fatalf("status %q must not allow exact-capacity refresh", status)
		}
	}
}

func TestCalculationExpiryCoversScheduledExecution(t *testing.T) {
	now := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	farSchedule := now.Add(30 * 24 * time.Hour)
	campaign := &models.Campaign{Spec: models.CampaignSpec{ScheduleAt: &farSchedule}}
	if got, want := calculationExpiry(now, campaign), farSchedule.Add(smartTargetingCapacityTTL); !got.Equal(want) {
		t.Fatalf("scheduled expiry = %s, want %s", got, want)
	}

	nearSchedule := now.Add(time.Hour)
	campaign.Spec.ScheduleAt = &nearSchedule
	if got, want := calculationExpiry(now, campaign), nearSchedule.Add(smartTargetingCapacityTTL); !got.Equal(want) {
		t.Fatalf("near-schedule expiry = %s, want %s", got, want)
	}
}

func TestSmartTargetingBundleAllocationFingerprintTracksSamplingPopulationChanges(t *testing.T) {
	audience := uint64(600)
	rows := []repository.BundleCampaignAllocation{
		{CampaignID: 11, NumAudience: &audience, Status: models.CampaignStatusApproved, Materialized: false},
	}
	deduction, before, err := smartTargetingBundleAllocationStateFromRows(3, rows)
	if err != nil || deduction != 600 {
		t.Fatalf("allocation state = (%d, %q, %v), want deduction 600", deduction, before, err)
	}

	rows[0].Materialized = true
	deduction, after, err := smartTargetingBundleAllocationStateFromRows(3, rows)
	if err != nil || deduction != 0 {
		t.Fatalf("materialized allocation state = (%d, %q, %v), want deduction 0", deduction, after, err)
	}
	if before == after {
		t.Fatal("allocation fingerprint did not change when audience materialization changed")
	}

	if _, _, err := smartTargetingBundleAllocationStateFromRows(3, []repository.BundleCampaignAllocation{{CampaignID: 12, Status: models.CampaignStatusApproved}}); err == nil {
		t.Fatal("allocation fingerprint accepted a reservation without an audience count")
	}
}

func TestSmartTargetingBundleAllocationFingerprintTracksActiveTestReservationsWithoutDoubleDeduction(t *testing.T) {
	audience := uint64(600)
	allocations := []repository.BundleCampaignAllocation{
		{CampaignID: 11, NumAudience: &audience, Status: models.CampaignStatusApproved, Materialized: false},
	}
	deduction, withoutReservation, err := smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(3, allocations, nil)
	if err != nil || deduction != 600 {
		t.Fatalf("allocation state without Test reservation = (%d, %q, %v), want deduction 600", deduction, withoutReservation, err)
	}

	deduction, withReservation, err := smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(3, allocations, []repository.BundleActiveTestReservation{
		{CampaignID: 23, SelectionID: 41, AudienceCount: 600},
	})
	if err != nil || deduction != 600 {
		t.Fatalf("allocation state with Test reservation = (%d, %q, %v), want unchanged deduction 600", deduction, withReservation, err)
	}
	if withoutReservation == withReservation {
		t.Fatal("allocation fingerprint did not change when an active Test reservation changed the candidate population")
	}

	_, reordered, err := smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(3, allocations, []repository.BundleActiveTestReservation{
		{CampaignID: 29, SelectionID: 45, AudienceCount: 200},
		{CampaignID: 23, SelectionID: 41, AudienceCount: 600},
	})
	if err != nil {
		t.Fatalf("build reservation fingerprint: %v", err)
	}
	_, reorderedAgain, err := smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(3, allocations, []repository.BundleActiveTestReservation{
		{CampaignID: 23, SelectionID: 41, AudienceCount: 600},
		{CampaignID: 29, SelectionID: 45, AudienceCount: 200},
	})
	if err != nil || reordered != reorderedAgain {
		t.Fatalf("active Test reservation fingerprint must be deterministic: %q, %q, %v", reordered, reorderedAgain, err)
	}

	if _, _, err := smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(3, allocations, []repository.BundleActiveTestReservation{{CampaignID: 23, SelectionID: 41}}); err == nil {
		t.Fatal("allocation fingerprint accepted an active Test reservation without members")
	}
}

func floatPtr(value float64) *float64 { return &value }
