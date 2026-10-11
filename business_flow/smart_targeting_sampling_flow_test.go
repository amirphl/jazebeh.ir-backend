package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/lib/pq"
)

type samplingSelectedTagRepositoryStub struct {
	selected    []*models.CampaignSelectedTag
	validateErr error
}

func (s *samplingSelectedTagRepositoryStub) ListAvailable(context.Context, uint, uint, string, string, string, int, int) ([]*models.SmartTargetingTagRow, int64, error) {
	return nil, 0, nil
}

func (s *samplingSelectedTagRepositoryStub) ListAvailableTagIDs(context.Context, uint, string, string, string, int) ([]uint, error) {
	return nil, nil
}

func (s *samplingSelectedTagRepositoryStub) ListSelected(context.Context, uint) ([]*models.CampaignSelectedTag, error) {
	return s.selected, nil
}

func (s *samplingSelectedTagRepositoryStub) Summary(context.Context, uint) (*models.CampaignSelectedTagSummary, error) {
	return &models.CampaignSelectedTagSummary{SelectedTagCount: int64(len(s.selected))}, nil
}

func (s *samplingSelectedTagRepositoryStub) Validate(context.Context, uint, uint) error {
	return s.validateErr
}

func (s *samplingSelectedTagRepositoryStub) Replace(context.Context, uint, uint, uint, []uint) error {
	return nil
}

func (s *samplingSelectedTagRepositoryStub) Clear(context.Context, uint) error {
	return nil
}

func TestSmartTargetingTestCreateRequiresPositiveSampleSizePerTag(t *testing.T) {
	flow := &CampaignFlowImpl{}
	method := models.CampaignAudienceTargetingSmart
	phase := string(models.CampaignPhaseTest)
	title := "test"
	bundleID := uint(3)
	base := dto.CreateCampaignRequest{
		CustomerID: 7, Title: &title, BundleID: &bundleID, Phase: &phase,
		AudienceTargetingMethod: &method, SelectedTagIDs: []uint{9, 2},
	}
	if err := flow.validateCreateCampaignRequest(t.Context(), &base); !errors.Is(err, ErrSmartTargetingSampleSizeRequired) {
		t.Fatalf("missing sample_size_per_tag error = %v", err)
	}

	base.SampleSizePerTag = utils.ToPtr(uint64(0))
	if err := flow.validateCreateCampaignRequest(t.Context(), &base); !errors.Is(err, ErrSmartTargetingSampleSizeInvalid) {
		t.Fatalf("zero sample_size_per_tag error = %v", err)
	}
}

func TestExecutionDoesNotRequireSampleSizePerTag(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	phase := string(models.CampaignPhaseExecution)
	title := "execution"
	bundleID := uint(3)
	platform := models.CampaignPlatformBale
	req := dto.CreateCampaignRequest{
		CustomerID: 7, Title: &title, BundleID: &bundleID, Phase: &phase, Platform: &platform,
		AudienceTargetingMethod: &method, SelectedTagIDs: []uint{9, 2},
	}
	err := (&CampaignFlowImpl{}).validateCreateCampaignRequest(t.Context(), &req)
	if errors.Is(err, ErrSmartTargetingSampleSizeRequired) || errors.Is(err, ErrSmartTargetingSampleSizeInvalid) {
		t.Fatalf("execution unexpectedly depends on sample_size_per_tag: %v", err)
	}
}

func TestSmartTargetingTestSamplingHashPreservesTagOrder(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	campaign := &models.Campaign{
		ID:               17,
		BundleID:         &bundleID,
		SampleSizePerTag: &sampleSize,
		Spec:             models.CampaignSpec{AudienceTargetingMethod: &method},
	}
	first, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2, 5}, []string{"A", "C"})
	if err != nil {
		t.Fatalf("first sampling hash failed: %v", err)
	}
	second, err := smartTargetingTestSamplingHash(campaign, []uint{2, 9, 5}, []string{"A", "C"})
	if err != nil {
		t.Fatalf("second sampling hash failed: %v", err)
	}
	if first == second {
		t.Fatal("sampling input hash must change when the user-defined tag order changes")
	}
}

func TestSmartTargetingTestSamplingHashIncludesEffectiveColorEligibility(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	campaign := &models.Campaign{
		ID:               17,
		BundleID:         &bundleID,
		SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{
			AudienceTargetingMethod: &method,
			Platform:                models.CampaignPlatformBale,
		},
	}

	unrestricted, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2}, []string{"A", "C"})
	if err != nil {
		t.Fatal(err)
	}
	campaign.Spec.Platform = models.CampaignPlatformSMS
	sms, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2}, []string{"A", "C"})
	if err != nil {
		t.Fatal(err)
	}
	if sms == unrestricted {
		t.Fatal("sampling input hash must change when SMS color eligibility becomes active")
	}

	campaign.Spec.Platform = models.CampaignPlatformRubika
	otherUnrestricted, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2}, []string{"A", "C"})
	if err != nil {
		t.Fatal(err)
	}
	if otherUnrestricted != unrestricted {
		t.Fatal("sampling input hash changed between platforms with identical color eligibility")
	}
}

func TestSmartTargetingTestSamplingAudienceQueryRestrictsSMSColors(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	campaign := &models.Campaign{
		ID:               17,
		BundleID:         &bundleID,
		Phase:            models.CampaignPhaseTest,
		SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{
			AudienceTargetingMethod: &method,
			AudienceGrades:          []string{"A"},
			Platform:                models.CampaignPlatformSMS,
		},
	}
	repo := &samplingSelectedTagRepositoryStub{selected: []*models.CampaignSelectedTag{
		{CampaignID: 17, BundleID: 3, TagID: 9, SelectionOrder: 0},
	}}

	input, err := currentSmartTargetingTestSamplingInput(t.Context(), repo, campaign)
	if err != nil {
		t.Fatal(err)
	}
	query := smartTargetingTestSamplingAudienceQuery(bundleID, []int64{9}, input)
	if !query.ApplyBundleAudienceExclusions {
		t.Fatal("Smart Targeting Test sampling must apply Bundle audience exclusions")
	}
	if len(query.AllowedColors) != 2 || query.AllowedColors[0] != "white" || query.AllowedColors[1] != "pink" {
		t.Fatalf("SMS sampling allowed colors = %v, want [white pink]", query.AllowedColors)
	}

	campaign.Spec.Platform = models.CampaignPlatformBale
	input, err = currentSmartTargetingTestSamplingInput(t.Context(), repo, campaign)
	if err != nil {
		t.Fatal(err)
	}
	query = smartTargetingTestSamplingAudienceQuery(bundleID, []int64{9}, input)
	if len(query.AllowedColors) != 0 {
		t.Fatalf("non-SMS sampling allowed colors = %v, want no restriction", query.AllowedColors)
	}
}

func TestCheckedCampaignCostRejectsOverflow(t *testing.T) {
	if _, err := checkedCampaignCost(math.MaxUint64, 2); !errors.Is(err, ErrCampaignCostOverflow) {
		t.Fatalf("overflow error = %v, want ErrCampaignCostOverflow", err)
	}
	if got, err := checkedCampaignCost(13, 7); err != nil || got != 91 {
		t.Fatalf("checked cost = (%d, %v), want (91, nil)", got, err)
	}
}

func TestSmartTargetingTestFinalizationDerivesBudget(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	campaign := &models.Campaign{
		Phase: models.CampaignPhaseTest,
		Spec:  models.CampaignSpec{AudienceTargetingMethod: &method},
	}

	if err := validateCampaignFinalizationBudget(campaign); err != nil {
		t.Fatalf("missing explicit budget error = %v, want nil", err)
	}

	audienceCount, err := checkedSmartTargetingTestAudienceCount(2, 600)
	if err != nil {
		t.Fatalf("effective audience calculation failed: %v", err)
	}
	totalCost, err := checkedCampaignCost(1_200, audienceCount)
	if err != nil {
		t.Fatalf("campaign cost calculation failed: %v", err)
	}
	cost := &dto.CalculateCampaignCostResponse{
		TotalCost:         totalCost,
		NumTargetAudience: audienceCount,
	}
	applyFinalizedCampaignCost(campaign, cost)

	if campaign.Spec.Budget == nil || *campaign.Spec.Budget != cost.TotalCost {
		t.Fatalf("derived budget = %v, want %d", campaign.Spec.Budget, cost.TotalCost)
	}
	if campaign.NumAudience == nil || *campaign.NumAudience != cost.NumTargetAudience {
		t.Fatalf("derived audience count = %v, want %d", campaign.NumAudience, cost.NumTargetAudience)
	}
}

func TestExplicitBudgetRemainsRequiredOutsideSmartTargetingTest(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	campaign := &models.Campaign{
		Phase: models.CampaignPhaseExecution,
		Spec:  models.CampaignSpec{AudienceTargetingMethod: &method},
	}
	if err := validateCampaignFinalizationBudget(campaign); !errors.Is(err, ErrCampaignBudgetRequired) {
		t.Fatalf("missing execution budget error = %v, want ErrCampaignBudgetRequired", err)
	}

	method = models.CampaignAudienceTargetingStandard
	campaign.Phase = models.CampaignPhaseTest
	campaign.Spec.AudienceTargetingMethod = &method
	if err := validateCampaignFinalizationBudget(campaign); !errors.Is(err, ErrCampaignBudgetRequired) {
		t.Fatalf("missing standard Test budget error = %v, want ErrCampaignBudgetRequired", err)
	}
}

func TestUpdateBudgetValidationIgnoresClientDefaultsForSmartTargetingTest(t *testing.T) {
	zero := uint64(0)
	if err := validateUpdateCampaignBudget(&zero, models.CampaignAudienceTargetingSmart, models.CampaignPhaseTest); err != nil {
		t.Fatalf("Smart Targeting Test zero budget error = %v, want nil", err)
	}

	if err := validateUpdateCampaignBudget(&zero, models.CampaignAudienceTargetingSmart, models.CampaignPhaseExecution); !errors.Is(err, ErrCampaignBudgetRequired) {
		t.Fatalf("Smart Targeting Execution zero budget error = %v, want ErrCampaignBudgetRequired", err)
	}

	tooSmall := minCampaignBudget - 1
	if err := validateUpdateCampaignBudget(&tooSmall, models.CampaignAudienceTargetingStandard, models.CampaignPhaseTest); !errors.Is(err, ErrCampaignBudgetOutOfRange) {
		t.Fatalf("standard out-of-range budget error = %v, want ErrCampaignBudgetOutOfRange", err)
	}
}

func TestCheckedSmartTargetingTestAudienceCountFitsDatabaseAndSchedulerRange(t *testing.T) {
	if _, err := checkedSmartTargetingTestAudienceCount(2, math.MaxInt64); !errors.Is(err, ErrSmartTargetingTestAudienceCountOverflow) {
		t.Fatalf("audience overflow error = %v, want ErrSmartTargetingTestAudienceCountOverflow", err)
	}
	if got, err := checkedSmartTargetingTestAudienceCount(3, 600); err != nil || got != 1_800 {
		t.Fatalf("checked audience count = (%d, %v), want (1800, nil)", got, err)
	}
}

func TestCurrentSmartTargetingTestSamplingIntentValidatesOrderedSubset(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	previewedAt := time.Now().UTC()
	campaign := &models.Campaign{
		ID:               17,
		BundleID:         &bundleID,
		Phase:            models.CampaignPhaseTest,
		SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{
			AudienceTargetingMethod: &method,
			AudienceGrades:          []string{"A", "C"},
		},
		SmartTargetingTestSatisfiedTagIDs:     pq.Int64Array{9, 5},
		SmartTargetingTestSamplingPreviewedAt: &previewedAt,
	}
	repo := &samplingSelectedTagRepositoryStub{selected: []*models.CampaignSelectedTag{
		{CampaignID: 17, BundleID: 3, TagID: 9, SelectionOrder: 0},
		{CampaignID: 17, BundleID: 3, TagID: 2, SelectionOrder: 1},
		{CampaignID: 17, BundleID: 3, TagID: 5, SelectionOrder: 2},
	}}
	hash, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2, 5}, []string{"A", "C"})
	if err != nil {
		t.Fatalf("sampling hash failed: %v", err)
	}
	campaign.SmartTargetingTestSamplingInputHash = &hash

	intent, err := currentSmartTargetingTestSamplingIntent(t.Context(), repo, campaign, true)
	if err != nil || intent.effective != 1_200 || len(intent.satisfied) != 2 || intent.satisfied[0] != 9 || intent.satisfied[1] != 5 {
		t.Fatalf("sampling intent = (%#v, %v), want ordered [9 5] with effective 1200", intent, err)
	}

	campaign.SmartTargetingTestSatisfiedTagIDs = pq.Int64Array{5, 9}
	if _, err := currentSmartTargetingTestSamplingIntent(t.Context(), repo, campaign, true); !errors.Is(err, ErrSmartTargetingTestPreviewRequired) {
		t.Fatalf("out-of-order intent error = %v, want preview required", err)
	}
}

func TestCurrentSmartTargetingTestSamplingInputRejectsMalformedSelectionSnapshot(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	campaign := &models.Campaign{
		ID: 17, BundleID: &bundleID, Phase: models.CampaignPhaseTest, SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{AudienceTargetingMethod: &method, AudienceGrades: []string{"A"}},
	}
	repo := &samplingSelectedTagRepositoryStub{selected: []*models.CampaignSelectedTag{
		{CampaignID: 17, BundleID: 3, TagID: 9, SelectionOrder: 1},
	}}
	if _, err := currentSmartTargetingTestSamplingInput(t.Context(), repo, campaign); !errors.Is(err, ErrSmartTargetingTagInvalid) {
		t.Fatalf("malformed selection error = %v, want invalid tag", err)
	}
}

func TestCurrentSmartTargetingTestSamplingInputIncludesTagDisplayNames(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	displayName := "Frequent travelers"
	campaign := &models.Campaign{
		ID: 17, BundleID: &bundleID, Phase: models.CampaignPhaseTest, SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{AudienceTargetingMethod: &method, AudienceGrades: []string{"A"}},
	}
	repo := &samplingSelectedTagRepositoryStub{selected: []*models.CampaignSelectedTag{
		{CampaignID: 17, BundleID: 3, TagID: 9, SelectionOrder: 0, TagDisplayTitleSnapshot: &displayName},
		{CampaignID: 17, BundleID: 3, TagID: 2, SelectionOrder: 1},
	}}
	input, err := currentSmartTargetingTestSamplingInput(t.Context(), repo, campaign)
	if err != nil {
		t.Fatalf("sampling input failed: %v", err)
	}
	if input.displayNames[9] == nil || *input.displayNames[9] != displayName {
		t.Fatalf("tag 9 display name = %v, want %q", input.displayNames[9], displayName)
	}
	if value, exists := input.displayNames[2]; !exists || value != nil {
		t.Fatalf("tag 2 display name = (%v, %t), want (nil, true)", value, exists)
	}
}

func TestSmartTargetingTestSamplingConfigurationInvalidatesOnlyEffectiveChanges(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	phase := string(models.CampaignPhaseTest)
	campaign := &models.Campaign{
		BundleID:         &bundleID,
		Phase:            models.CampaignPhaseTest,
		SampleSizePerTag: &sampleSize,
		Spec: models.CampaignSpec{
			AudienceTargetingMethod: &method,
			AudienceGrades:          []string{"A", "C"},
		},
	}

	unchanged := &dto.UpdateCampaignRequest{
		AudienceTargetingMethod: &method,
		BundleID:                &bundleID,
		Phase:                   &phase,
		SampleSizePerTag:        &sampleSize,
		AudienceGrades:          []string{"C", "A"},
	}
	changed, err := smartTargetingTestSamplingConfigurationChanged(campaign, unchanged)
	if err != nil || changed {
		t.Fatalf("unchanged effective sampling configuration = (%t, %v), want (false, nil)", changed, err)
	}

	changedSampleSize := uint64(601)
	changed, err = smartTargetingTestSamplingConfigurationChanged(campaign, &dto.UpdateCampaignRequest{SampleSizePerTag: &changedSampleSize})
	if err != nil || !changed {
		t.Fatalf("changed sample size = (%t, %v), want (true, nil)", changed, err)
	}

	execution := string(models.CampaignPhaseExecution)
	changed, err = smartTargetingTestSamplingConfigurationChanged(campaign, &dto.UpdateCampaignRequest{Phase: &execution})
	if err != nil || !changed {
		t.Fatalf("changed phase = (%t, %v), want (true, nil)", changed, err)
	}

	campaign.Spec.Platform = models.CampaignPlatformBale
	sms := models.CampaignPlatformSMS
	changed, err = smartTargetingTestSamplingConfigurationChanged(campaign, &dto.UpdateCampaignRequest{Platform: &sms})
	if err != nil || !changed {
		t.Fatalf("enabled SMS color eligibility = (%t, %v), want (true, nil)", changed, err)
	}

	rubika := models.CampaignPlatformRubika
	changed, err = smartTargetingTestSamplingConfigurationChanged(campaign, &dto.UpdateCampaignRequest{Platform: &rubika})
	if err != nil || changed {
		t.Fatalf("unchanged non-SMS color eligibility = (%t, %v), want (false, nil)", changed, err)
	}
}

func TestSmartTargetingTestSamplingCalculationDTOHidesPendingResults(t *testing.T) {
	row := &models.CampaignTargetingTestSamplingCalculation{
		ID: 7, CampaignID: 17, BundleID: 3,
		SelectedTagIDs: pq.Int64Array{9, 2}, SelectedTagCount: 2, SelectedScoreClasses: pq.StringArray{"A", "C"},
		SampleSizePerTag: 600, Status: models.CampaignTargetingTestSamplingCalculating,
		TagResults: json.RawMessage(`[]`), CreatedAt: time.Now().UTC(),
	}
	got, err := smartTargetingTestSamplingCalculationDTO(row, false, false)
	if err != nil {
		t.Fatalf("pending DTO failed: %v", err)
	}
	if got.Status != "calculating" || got.SatisfiedTagCount != nil || got.EffectiveAudienceCount != nil || got.CampaignCost != nil {
		t.Fatalf("pending DTO exposed completed results: %#v", got)
	}
}

func TestSmartTargetingTestSamplingCalculationDTOExposesOrderedCompletedResults(t *testing.T) {
	firstDisplayName := "Frequent travelers"
	secondDisplayName := "Online shoppers"
	results := []dto.SmartTargetingTestSamplingTagResult{
		{TagID: 9, TagDisplayName: &firstDisplayName, SelectionOrder: 0, Satisfied: true, AvailableCount: 600},
		{TagID: 2, TagDisplayName: &secondDisplayName, SelectionOrder: 1, Satisfied: false, AvailableCount: 417},
	}
	raw, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Now().UTC()
	row := &models.CampaignTargetingTestSamplingCalculation{
		ID: 8, CampaignID: 17, BundleID: 3,
		SelectedTagIDs: pq.Int64Array{9, 2}, SelectedTagCount: 2, SelectedScoreClasses: pq.StringArray{"A", "C"},
		SampleSizePerTag: 600, Status: models.CampaignTargetingTestSamplingCalculated,
		TagResults: raw, SatisfiedTagCount: 1, EffectiveAudienceCount: 600, CampaignCost: 720_000,
		FinishedAt: &finishedAt, CreatedAt: finishedAt.Add(-time.Minute),
	}
	got, err := smartTargetingTestSamplingCalculationDTO(row, true, false)
	if err != nil {
		t.Fatalf("calculated DTO failed: %v", err)
	}
	if !got.IsCurrent || len(got.TagSamplingOrder) != 2 || got.TagSamplingOrder[0] != 9 || got.TagSamplingOrder[1] != 2 {
		t.Fatalf("calculated DTO order/current = %#v", got)
	}
	if len(got.SatisfiedTags) != 1 || got.SatisfiedTags[0].TagID != 9 || got.SatisfiedTags[0].TagDisplayName == nil || *got.SatisfiedTags[0].TagDisplayName != firstDisplayName ||
		len(got.UnsatisfiedTags) != 1 || got.UnsatisfiedTags[0].TagID != 2 || got.UnsatisfiedTags[0].TagDisplayName == nil || *got.UnsatisfiedTags[0].TagDisplayName != secondDisplayName {
		t.Fatalf("calculated DTO results = %#v", got)
	}
	if got.SatisfiedTagCount == nil || *got.SatisfiedTagCount != 1 || got.EffectiveAudienceCount == nil || *got.EffectiveAudienceCount != 600 || got.CampaignCost == nil || *got.CampaignCost != 720_000 {
		t.Fatalf("calculated DTO aggregates = %#v", got)
	}
}

func TestSmartTargetingTestSamplingCalculationDTOHidesStaleCompletedResults(t *testing.T) {
	finishedAt := time.Now().UTC()
	row := &models.CampaignTargetingTestSamplingCalculation{
		ID: 8, CampaignID: 17, BundleID: 3,
		SelectedTagIDs: pq.Int64Array{9}, SelectedTagCount: 1, SelectedScoreClasses: pq.StringArray{"A"},
		SampleSizePerTag: 600, Status: models.CampaignTargetingTestSamplingCalculated,
		TagResults:        json.RawMessage(`[{"tag_id":9,"selection_order":0,"satisfied":true,"available_count":600}]`),
		SatisfiedTagCount: 1, EffectiveAudienceCount: 600, CampaignCost: 720_000,
		FinishedAt: &finishedAt, CreatedAt: finishedAt.Add(-time.Minute),
	}
	got, err := smartTargetingTestSamplingCalculationDTO(row, false, true)
	if err != nil {
		t.Fatalf("stale calculated DTO failed: %v", err)
	}
	if got.Status != "stale" || !got.RecalculationRequired || got.SatisfiedTags != nil || got.UnsatisfiedTags != nil ||
		got.SatisfiedTagCount != nil || got.EffectiveAudienceCount != nil || got.CampaignCost != nil {
		t.Fatalf("stale calculated DTO exposed completed results: %#v", got)
	}
}

func TestSmartTargetingTestSamplingCalculationDTORejectsInconsistentCompletedResults(t *testing.T) {
	finishedAt := time.Now().UTC()
	row := &models.CampaignTargetingTestSamplingCalculation{
		ID: 8, CampaignID: 17, BundleID: 3,
		SelectedTagIDs: pq.Int64Array{9}, SelectedTagCount: 1, SelectedScoreClasses: pq.StringArray{"A"},
		SampleSizePerTag: 600, Status: models.CampaignTargetingTestSamplingCalculated,
		TagResults:        json.RawMessage(`[{"tag_id":9,"selection_order":0,"satisfied":true,"available_count":599}]`),
		SatisfiedTagCount: 1, EffectiveAudienceCount: 600, CampaignCost: 720_000,
		FinishedAt: &finishedAt, CreatedAt: finishedAt.Add(-time.Minute),
	}
	if _, err := smartTargetingTestSamplingCalculationDTO(row, true, false); !errors.Is(err, ErrSmartTargetingTestPreviewRequired) {
		t.Fatalf("inconsistent calculated DTO error = %v, want preview required", err)
	}
}

func TestSamplingCalculationFreshnessIgnoresRevisionButRejectsChangedSamplingInput(t *testing.T) {
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(3)
	sampleSize := uint64(600)
	currentRevision := time.Now().UTC()
	calculationRevision := currentRevision.Add(-time.Minute)
	campaign := &models.Campaign{
		ID: 17, BundleID: &bundleID, Phase: models.CampaignPhaseTest, SampleSizePerTag: &sampleSize,
		Status: models.CampaignStatusInProgress, SmartTargetingTestSamplingGeneration: 4,
		Spec:      models.CampaignSpec{AudienceTargetingMethod: &method, AudienceGrades: []string{"A", "C"}},
		UpdatedAt: &currentRevision,
	}
	repo := &samplingSelectedTagRepositoryStub{selected: []*models.CampaignSelectedTag{
		{CampaignID: 17, BundleID: 3, TagID: 9, SelectionOrder: 0},
		{CampaignID: 17, BundleID: 3, TagID: 2, SelectionOrder: 1},
	}}
	hash, err := smartTargetingTestSamplingHash(campaign, []uint{9, 2}, []string{"A", "C"})
	if err != nil {
		t.Fatal(err)
	}
	row := &models.CampaignTargetingTestSamplingCalculation{
		CampaignID: 17, BundleID: 3, SelectedTagIDs: pq.Int64Array{9, 2}, SelectedTagCount: 2, SelectedScoreClasses: pq.StringArray{"A", "C"},
		SampleSizePerTag: 600, InputHash: hash, CalculationVersion: smartTargetingTestSamplingCalculationVersion, CampaignUpdatedAt: &calculationRevision,
		Status: models.CampaignTargetingTestSamplingCalculating, Generation: 4,
	}
	currentInput, err := currentSmartTargetingTestSamplingInput(t.Context(), repo, campaign)
	if err != nil {
		t.Fatal(err)
	}
	if !reusableActiveSmartTargetingTestSampling(campaign, row, currentInput) {
		t.Fatal("same-input active calculation was not reusable after only updated_at changed")
	}
	row.Generation = 0
	if reusableActiveSmartTargetingTestSampling(campaign, row, currentInput) {
		t.Fatal("generation-zero active calculation was reusable")
	}
	row.Generation = 4
	campaign.SmartTargetingTestSamplingGeneration = 5
	if reusableActiveSmartTargetingTestSampling(campaign, row, currentInput) {
		t.Fatal("mismatched-generation active calculation was reusable")
	}
	campaign.SmartTargetingTestSamplingGeneration = 4
	input, gotSampleSize, err := currentSmartTargetingTestSamplingCalculationInput(t.Context(), repo, campaign, row)
	if err != nil || gotSampleSize != 600 || len(input.order) != 2 || input.order[0] != 9 {
		t.Fatalf("snapshot input = (%#v, %d, %v)", input, gotSampleSize, err)
	}

	changedSampleSize := uint64(601)
	campaign.SampleSizePerTag = &changedSampleSize
	if reusableActiveSmartTargetingTestSampling(campaign, row, currentInput) {
		t.Fatal("active calculation remained reusable after sample size changed")
	}
	if _, _, err := currentSmartTargetingTestSamplingCalculationInput(t.Context(), repo, campaign, row); !errors.Is(err, ErrSmartTargetingTestPreviewRequired) {
		t.Fatalf("changed campaign snapshot error = %v, want preview required", err)
	}
}
