package businessflow

import (
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/scheduler"
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
