package businessflow

import (
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

func TestApplyCampaignSpecUpdateDeletesOmittedReplaceFields(t *testing.T) {
	t.Parallel()

	title := "original"
	adLink := "https://example.com/{uid}"
	domain := "jo1n.ir"
	lineNumber := "30001234"
	excelFile := "bb6e946c-4441-4c7b-aeb2-9a8cb424892b"
	scheduledAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	platform := models.CampaignPlatformSMS

	spec := models.CampaignSpec{
		Title:                       &title,
		AdLink:                      &adLink,
		ShortLinkDomain:             &domain,
		ScheduleAt:                  &scheduledAt,
		LineNumber:                  &lineNumber,
		TargetAudienceExcelFileUUID: &excelFile,
		Platform:                    platform,
	}
	updatedTitle := "renamed"
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{Title: &updatedTitle}); err != nil {
		t.Fatalf("applyCampaignSpecUpdate: %v", err)
	}

	if spec.Platform != platform {
		t.Fatalf("platform = %q, want unchanged %q", spec.Platform, platform)
	}
	if spec.AdLink != nil {
		t.Fatalf("ad link = %v, want nil when omitted", *spec.AdLink)
	}
	if spec.ShortLinkDomain != nil {
		t.Fatalf("short-link domain = %v, want nil when omitted", *spec.ShortLinkDomain)
	}
	if spec.ScheduleAt != nil {
		t.Fatalf("schedule = %v, want nil when omitted", spec.ScheduleAt)
	}
	if spec.LineNumber == nil || *spec.LineNumber != lineNumber {
		t.Fatalf("line number = %v, want unchanged", spec.LineNumber)
	}
	if spec.TargetAudienceExcelFileUUID == nil || *spec.TargetAudienceExcelFileUUID != excelFile {
		t.Fatalf("excel file = %v, want unchanged", spec.TargetAudienceExcelFileUUID)
	}
}

func TestApplyCampaignSpecUpdateDeletesEmptyReplaceFields(t *testing.T) {
	t.Parallel()

	adLink := "https://example.com/{uid}"
	domain := "jo1n.ir"
	scheduledAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	empty := ""
	spec := models.CampaignSpec{
		AdLink:          &adLink,
		ShortLinkDomain: &domain,
		ScheduleAt:      &scheduledAt,
		Platform:        models.CampaignPlatformSMS,
	}
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{
		AdLink:          &empty,
		ShortLinkDomain: &empty,
	}); err != nil {
		t.Fatalf("applyCampaignSpecUpdate: %v", err)
	}
	if spec.AdLink != nil || spec.ShortLinkDomain != nil || spec.ScheduleAt != nil {
		t.Fatalf("replace fields were not cleared: %#v", spec)
	}
}

func TestApplyCampaignSpecUpdateAllowsDraftPlatformConfiguration(t *testing.T) {
	t.Parallel()

	lineNumber := "30001234"
	sms := models.CampaignPlatformSMS
	bale := models.CampaignPlatformBale
	spec := models.CampaignSpec{Platform: sms, LineNumber: &lineNumber}

	// A transition to a non-SMS platform clears the incompatible line, but does
	// not require a setting yet: it can be supplied in a later draft update.
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{Platform: &bale}); err != nil {
		t.Fatalf("switching to Bale: %v", err)
	}
	if spec.LineNumber != nil || spec.PlatformSettingsID != nil {
		t.Fatalf("Bale draft config = %#v, want no SMS line or settings", spec)
	}
	settingsID := uint(52)
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{PlatformSettingsID: &settingsID}); err != nil {
		t.Fatalf("setting Bale platform settings: %v", err)
	}
	if spec.PlatformSettingsID == nil || *spec.PlatformSettingsID != settingsID {
		t.Fatalf("Bale platform settings = %v, want %d", spec.PlatformSettingsID, settingsID)
	}

	// The reverse transition clears the incompatible setting, and the line can
	// likewise be supplied later in a separate update.
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{Platform: &sms}); err != nil {
		t.Fatalf("switching to SMS: %v", err)
	}
	if spec.PlatformSettingsID != nil || spec.LineNumber != nil {
		t.Fatalf("SMS draft config = %#v, want no setting or line yet", spec)
	}
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{LineNumber: &lineNumber}); err != nil {
		t.Fatalf("setting SMS line number: %v", err)
	}
	if spec.LineNumber == nil || *spec.LineNumber != lineNumber {
		t.Fatalf("SMS line number = %v, want %q", spec.LineNumber, lineNumber)
	}
}

func TestApplyCampaignSpecUpdateSwitchingToSmartClearsExcelInput(t *testing.T) {
	t.Parallel()

	excelFile := "bb6e946c-4441-4c7b-aeb2-9a8cb424892b"
	excelMethod := models.CampaignAudienceTargetingExcel
	smartMethod := models.CampaignAudienceTargetingSmart
	spec := models.CampaignSpec{
		AudienceTargetingMethod:     &excelMethod,
		TargetAudienceExcelFileUUID: &excelFile,
		Platform:                    models.CampaignPlatformSMS,
	}

	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{AudienceTargetingMethod: &smartMethod}); err != nil {
		t.Fatalf("applyCampaignSpecUpdate: %v", err)
	}
	if !spec.UsesSmartTargeting() {
		t.Fatalf("targeting method = %q, want smart targeting", campaignAudienceTargetingMethod(spec))
	}
	if spec.TargetAudienceExcelFileUUID != nil {
		t.Fatalf("excel input = %q, want nil after switching to smart targeting", *spec.TargetAudienceExcelFileUUID)
	}
}

func TestApplyCampaignSpecUpdateClearsIncompatiblePlatformConfiguration(t *testing.T) {
	t.Parallel()

	settingsID := uint(44)
	lineNumber := "30001234"
	rubika := models.CampaignPlatformRubika
	sms := models.CampaignPlatformSMS
	spec := models.CampaignSpec{
		Platform:           rubika,
		PlatformSettingsID: &settingsID,
		LineNumber:         &lineNumber, // tolerate and clean a legacy inconsistent spec
	}
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{Platform: &sms}); err != nil {
		t.Fatalf("switching to SMS: %v", err)
	}
	if spec.PlatformSettingsID != nil {
		t.Fatalf("SMS platform settings = %v, want nil", *spec.PlatformSettingsID)
	}

	bale := models.CampaignPlatformBale
	if err := applyCampaignSpecUpdate(&spec, &dto.UpdateCampaignRequest{Platform: &bale}); err != nil {
		t.Fatalf("switching to Bale: %v", err)
	}
	if spec.LineNumber != nil {
		t.Fatalf("non-SMS line number = %q, want nil", *spec.LineNumber)
	}
	if spec.PlatformSettingsID != nil {
		t.Fatalf("switched platform settings = %v, want nil until a Bale setting is selected", *spec.PlatformSettingsID)
	}
}

func TestBuildCampaignReportRowsUsesAudienceJSONLMapping(t *testing.T) {
	t.Parallel()

	rows := buildCampaignReportRows(
		[]string{"audience-b", "audience-a"},
		map[string]string{"audience-a": "code-a", "audience-b": "code-b"},
		[]string{"code-b"},
	)
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}
	if rows[0] != (campaignReportRow{AudienceProfileUID: "audience-a", Status: "unknown", Clicked: "false"}) {
		t.Fatalf("first row = %#v", rows[0])
	}
	if rows[1] != (campaignReportRow{AudienceProfileUID: "audience-b", Status: "unknown", Clicked: "true"}) {
		t.Fatalf("second row = %#v", rows[1])
	}
}

func TestCalculatePartsDoesNotCapLongSMS(t *testing.T) {
	t.Parallel()

	flow := &CampaignFlowImpl{}
	// countCharacters adds the six-character SMS overhead, producing 397
	// characters here. That requires seven 66-character concatenated parts.
	content := strings.Repeat("a", 391)
	if got := flow.calculateParts(&content, nil, nil, models.CampaignPlatformSMS); got != 7 {
		t.Fatalf("calculateParts(long SMS) = %d, want 7", got)
	}

	if got := flow.calculateParts(utils.ToPtr(strings.Repeat("a", 390)), nil, nil, models.CampaignPlatformSMS); got != 6 {
		t.Fatalf("calculateParts(396-character SMS) = %d, want 6", got)
	}
}
