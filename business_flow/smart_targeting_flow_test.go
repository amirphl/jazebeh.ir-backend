package businessflow

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

type smartTargetingEvaluationReadStub struct {
	currentScoreCount int64
	err               error
}

func (s *smartTargetingEvaluationReadStub) ByBundleID(context.Context, uint) (*models.CurrentBundleTagEvaluationStatus, error) {
	return nil, s.err
}

func (s *smartTargetingEvaluationReadStub) ListByBundleIDs(context.Context, []uint) ([]*models.CurrentBundleTagEvaluationStatus, error) {
	return nil, s.err
}

func (s *smartTargetingEvaluationReadStub) ByRunID(context.Context, int64) (*models.BundleTagEvaluationRunStatus, error) {
	return nil, s.err
}

func (s *smartTargetingEvaluationReadStub) ListPendingRuns(context.Context, int) ([]*models.BundleTagEvaluationRunStatus, error) {
	return nil, s.err
}

func (s *smartTargetingEvaluationReadStub) ListCurrentScoresByBundleID(context.Context, uint, int, int) ([]*models.CurrentBundleTagScore, error) {
	return nil, s.err
}

func (s *smartTargetingEvaluationReadStub) CountCurrentScoresByBundleID(context.Context, uint) (int64, error) {
	return s.currentScoreCount, s.err
}

func TestEvaluationAvailableRequiresCurrentScoreRows(t *testing.T) {
	tests := []struct {
		name  string
		count int64
		want  bool
	}{
		{name: "evaluated snapshot", count: 3, want: true},
		{name: "no evaluated tag rows falls back to tags", count: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := &SmartTargetingFlowImpl{
				evaluationRepo: &smartTargetingEvaluationReadStub{currentScoreCount: tt.count},
			}
			got, err := flow.evaluationAvailable(t.Context(), 12)
			if err != nil {
				t.Fatalf("evaluationAvailable() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("evaluationAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSmartTargetingPageOffsetRejectsOverflow(t *testing.T) {
	maxSafePage := math.MaxInt/100 + 1
	offset, err := smartTargetingPageOffset(maxSafePage, 100)
	if err != nil {
		t.Fatalf("max safe page rejected: %v", err)
	}
	if offset < 0 {
		t.Fatalf("max safe offset wrapped: %d", offset)
	}
	if _, err := smartTargetingPageOffset(maxSafePage+1, 100); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("overflowing page error = %v, want ErrInvalidPage", err)
	}
}

func TestNormalizeSmartTargetingQueryDefaults(t *testing.T) {
	tests := []struct {
		name      string
		evaluated bool
		execution bool
		wantSort  string
		wantDir   string
	}{
		{name: "execution with Test CTR fallback ordering", evaluated: true, execution: true, wantSort: "execution_default", wantDir: "desc"},
		{name: "execution without evaluation", evaluated: false, execution: true, wantSort: "execution_default", wantDir: "desc"},
		{name: "evaluated", evaluated: true, wantSort: "bundle_persona_fit_score", wantDir: "desc"},
		{name: "not evaluated", evaluated: false, wantSort: "database_order", wantDir: "asc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search, sortBy, direction, err := normalizeSmartTargetingQuery("  فارسی  ", "", "", tt.evaluated, tt.execution)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if search != "فارسی" || sortBy != tt.wantSort || direction != tt.wantDir {
				t.Fatalf("got (%q, %q, %q)", search, sortBy, direction)
			}
		})
	}
}

func TestNormalizeSmartTargetingQueryValidation(t *testing.T) {
	if _, _, _, err := normalizeSmartTargetingQuery("", "bundle_persona_fit_score", "desc", false, false); !errors.Is(err, ErrSmartTargetingScoreUnavailable) {
		t.Fatalf("expected score unavailable, got %v", err)
	}
	if _, _, _, err := normalizeSmartTargetingQuery("", "display_title", "asc", true, false); !errors.Is(err, ErrSmartTargetingSortInvalid) {
		t.Fatalf("expected invalid sort, got %v", err)
	}
	if _, _, _, err := normalizeSmartTargetingQuery("", "database_order", "asc", false, false); !errors.Is(err, ErrSmartTargetingSortInvalid) {
		t.Fatalf("expected internal database order to be rejected, got %v", err)
	}
	if _, _, _, err := normalizeSmartTargetingQuery("", "tag_capacity", "sideways", true, false); !errors.Is(err, ErrSmartTargetingSortInvalid) {
		t.Fatalf("expected invalid direction, got %v", err)
	}
	if _, _, _, err := normalizeSmartTargetingQuery(strings.Repeat("ی", 201), "", "", false, false); !errors.Is(err, ErrSmartTargetingSearchTooLong) {
		t.Fatalf("expected search-too-long, got %v", err)
	}
	if _, _, _, err := normalizeSmartTargetingQuery("", "execution_default", "desc", true, true); !errors.Is(err, ErrSmartTargetingSortInvalid) {
		t.Fatalf("expected internal execution default to be rejected, got %v", err)
	}
}

func TestNormalizeSelectedTagIDs(t *testing.T) {
	ids, err := normalizeSelectedTagIDs([]uint{9, 2, 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []uint{9, 2, 5}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
	if _, err := normalizeSelectedTagIDs(nil); !errors.Is(err, ErrSmartTargetingTagsRequired) {
		t.Fatalf("expected required error, got %v", err)
	}
	if _, err := normalizeSelectedTagIDs([]uint{1, 1}); !errors.Is(err, ErrSmartTargetingTagInvalid) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	if _, err := normalizeSelectedTagIDs([]uint{0}); !errors.Is(err, ErrSmartTargetingTagInvalid) {
		t.Fatalf("expected invalid-tag error, got %v", err)
	}
}

func TestCanonicalSmartTargetingCapacityTagIDsDoesNotChangeSelectionOrder(t *testing.T) {
	selected := []uint{9, 2, 5}
	canonical := canonicalSmartTargetingCapacityTagIDs(selected)
	if len(canonical) != 3 || canonical[0] != 2 || canonical[1] != 5 || canonical[2] != 9 {
		t.Fatalf("canonical capacity IDs = %v, want [2 5 9]", canonical)
	}
	if selected[0] != 9 || selected[1] != 2 || selected[2] != 5 {
		t.Fatalf("selection order mutated to %v", selected)
	}
}

func TestSelectionResponsePreservesRepositoryOrder(t *testing.T) {
	response := selectionResponse([]*models.CampaignSelectedTag{
		{TagID: 9, SelectionOrder: 0},
		{TagID: 2, SelectionOrder: 1},
		{TagID: 5, SelectionOrder: 2},
	}, &models.CampaignSelectedTagSummary{SelectedTagCount: 3})
	want := []uint{9, 2, 5}
	if len(response.SelectedTagIDs) != len(want) {
		t.Fatalf("selected tag IDs = %v, want %v", response.SelectedTagIDs, want)
	}
	for i := range want {
		if response.SelectedTagIDs[i] != want[i] {
			t.Fatalf("selected tag IDs = %v, want %v", response.SelectedTagIDs, want)
		}
	}
}

func TestSmartTargetingTagItemMapsEveryField(t *testing.T) {
	displayTitle := "Display title"
	audiencePersona := "Audience persona"
	capacity := int64(123)
	fitScore := 87.5
	evaluationRunID := int64(15)
	fitLevel := "high"
	relationType := "direct"
	reason := "Strong match"
	testCTR := 0.12
	overallCTR := 0.34
	row := &models.SmartTargetingTagRow{
		TagID:                 9,
		TagName:               "tag-name",
		TagDisplayTitle:       &displayTitle,
		TagAudiencePersona:    &audiencePersona,
		TagAudienceCount:      &capacity,
		BundlePersonaFitScore: &fitScore,
		EvaluationRunID:       &evaluationRunID,
		FitLevel:              &fitLevel,
		RelationType:          &relationType,
		Reason:                &reason,
		TestPhaseAvgCTR:       &testCTR,
		OverallAvgCTR:         &overallCTR,
		Selected:              true,
	}

	got := smartTargetingTagItem(row)
	if got.TagID != row.TagID ||
		got.TagDisplayTitle != row.TagDisplayTitle ||
		got.TagCapacity != row.TagAudienceCount ||
		got.BundlePersonaFitScore != row.BundlePersonaFitScore ||
		got.EvaluationRunID != row.EvaluationRunID ||
		got.FitLevel != row.FitLevel ||
		got.RelationType != row.RelationType ||
		got.TestPhaseAvgCTR != row.TestPhaseAvgCTR ||
		got.OverallAvgCTR != row.OverallAvgCTR ||
		got.Selected != row.Selected {
		t.Fatalf("incomplete Smart Targeting tag mapping: %#v", got)
	}
}

func TestCampaignAudienceTargetingMethod(t *testing.T) {
	if got := campaignAudienceTargetingMethod(models.CampaignSpec{}); got != models.CampaignAudienceTargetingStandard {
		t.Fatalf("legacy campaign method = %q", got)
	}
	smart := models.CampaignAudienceTargetingSmart
	if got := campaignAudienceTargetingMethod(models.CampaignSpec{AudienceTargetingMethod: &smart}); got != smart {
		t.Fatalf("smart campaign method = %q", got)
	}
	excelUUID := "4a54766e-4330-4cff-8658-bcd3c742b469"
	if got := campaignAudienceTargetingMethod(models.CampaignSpec{TargetAudienceExcelFileUUID: &excelUUID}); got != models.CampaignAudienceTargetingExcel {
		t.Fatalf("legacy Excel campaign method = %q", got)
	}
	if got := campaignAudienceTargetingMethod(models.CampaignSpec{
		AudienceTargetingMethod:     &smart,
		TargetAudienceExcelFileUUID: &excelUUID,
	}); got != models.CampaignAudienceTargetingSmart {
		t.Fatalf("Smart priority campaign method = %q", got)
	}
	standard := models.CampaignAudienceTargetingStandard
	if got := campaignAudienceTargetingMethod(models.CampaignSpec{
		AudienceTargetingMethod:     &standard,
		TargetAudienceExcelFileUUID: &excelUUID,
	}); got != models.CampaignAudienceTargetingStandard {
		t.Fatalf("explicit standard campaign method = %q", got)
	}
}

func TestResolveAudienceTargetingMethodBackwardCompatibility(t *testing.T) {
	excelUUID := "4a54766e-4330-4cff-8658-bcd3c742b469"
	got, err := resolveAudienceTargetingMethod(nil, &excelUUID)
	if err != nil || got != models.CampaignAudienceTargetingExcel {
		t.Fatalf("legacy Excel payload = (%q, %v)", got, err)
	}

	standard := models.CampaignAudienceTargetingStandard
	got, err = resolveAudienceTargetingMethod(&standard, &excelUUID)
	if err != nil || got != models.CampaignAudienceTargetingStandard {
		t.Fatalf("explicit standard-plus-file payload = (%q, %v)", got, err)
	}

	smart := models.CampaignAudienceTargetingSmart
	got, err = resolveAudienceTargetingMethod(&smart, &excelUUID)
	if err != nil || got != models.CampaignAudienceTargetingSmart {
		t.Fatalf("Smart-plus-file payload = (%q, %v)", got, err)
	}
	excel := models.CampaignAudienceTargetingExcel
	got, err = resolveAudienceTargetingMethodWithSelectedTags(&excel, []uint{9}, &excelUUID)
	if err != nil || got != models.CampaignAudienceTargetingExcel {
		t.Fatalf("explicit Excel-plus-selected-tags payload = (%q, %v)", got, err)
	}

	got, err = resolveAudienceTargetingMethodWithSelectedTags(nil, []uint{9}, &excelUUID)
	if err != nil || got != models.CampaignAudienceTargetingSmart {
		t.Fatalf("method-less Smart-plus-file payload = (%q, %v)", got, err)
	}
}

func TestValidateCreateSmartTargetingAllowsEmptyLevelsAndRequiresTags(t *testing.T) {
	title := "Smart campaign"
	phase := models.CampaignPhaseExecution.String()
	platform := models.CampaignPlatformSMS
	method := models.CampaignAudienceTargetingSmart
	bundleID := uint(1)
	emptyLevel1 := ""
	req := &dto.CreateCampaignRequest{
		CustomerID:              1,
		Title:                   &title,
		Level1:                  &emptyLevel1,
		Level2s:                 []string{""},
		Level3s:                 []string{""},
		BundleID:                &bundleID,
		Phase:                   &phase,
		Platform:                &platform,
		AudienceTargetingMethod: &method,
		SelectedTagIDs:          []uint{9},
	}

	flow := &CampaignFlowImpl{}
	if err := flow.validateCreateCampaignRequest(t.Context(), req); err != nil {
		t.Fatalf("Smart request with empty levels failed: %v", err)
	}

	req.SelectedTagIDs = nil
	if err := flow.validateCreateCampaignRequest(t.Context(), req); !errors.Is(err, ErrSmartTargetingTagsRequired) {
		t.Fatalf("Smart request without tags error = %v", err)
	}
}
