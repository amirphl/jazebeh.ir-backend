package businessflow

import (
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/scheduler"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestFormatPayamSMSResponseItemsDereferencesProviderFields(t *testing.T) {
	serverID := "server-42"
	errorCode := "4007"
	description := "invalid recipient"

	got := formatPayamSMSResponseItems([]scheduler.PayamSMSResponseItem{{
		TrackingID: "test-sms-44-12345678",
		Mobile:     "989351688668",
		ServerID:   &serverID,
		ErrorCode:  &errorCode,
		Desc:       &description,
	}})
	want := `[{tracking_id="test-sms-44-12345678" mobile="989351688668" server_id="server-42" error_code="4007" description="invalid recipient"}]`
	if got != want {
		t.Fatalf("formatted response = %q, want %q", got, want)
	}
}

func TestFormatPayamSMSResponseItemsKeepsMissingFieldsExplicit(t *testing.T) {
	got := formatPayamSMSResponseItems([]scheduler.PayamSMSResponseItem{{
		TrackingID: "test-sms-44-12345678",
		Mobile:     "989351688668",
	}})
	want := `[{tracking_id="test-sms-44-12345678" mobile="989351688668" server_id=null error_code=null description=null}]`
	if got != want {
		t.Fatalf("formatted response = %q, want %q", got, want)
	}
}

func TestCampaignTestShortLinkAllocationRequiresExactlyPositionZero(t *testing.T) {
	zero := 0
	one := 1
	valid := &models.ShortLink{UID: "100000", AllocationPosition: &zero}

	if got, err := campaignTestShortLinkAllocation([]*models.ShortLink{valid}); err != nil || got != valid {
		t.Fatalf("valid allocation = (%v, %v), want (%v, nil)", got, err, valid)
	}
	for name, rows := range map[string][]*models.ShortLink{
		"empty":            {},
		"nil row":          {nil},
		"missing position": {{UID: "100000"}},
		"wrong position":   {{UID: "100000", AllocationPosition: &one}},
		"multiple rows":    {valid, {UID: "100001", AllocationPosition: &one}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := campaignTestShortLinkAllocation(rows); err == nil {
				t.Fatal("expected corrupt allocation error")
			}
		})
	}
}
