package repository

import (
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestExternalStoredLongURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "scheme-less", raw: "example.com/offer", want: "https://example.com/offer"},
		{name: "protocol-relative", raw: "//example.com/offer", want: "https://example.com/offer"},
		{name: "existing HTTPS is untouched", raw: "https://example.com/offer", want: "https://example.com/offer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := externalStoredLongURL(test.raw)
			if err != nil {
				t.Fatalf("externalStoredLongURL(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("externalStoredLongURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestExternalStoredLongURLRejectsNonWebScheme(t *testing.T) {
	if _, err := externalStoredLongURL("mailto:person@example.com"); err == nil {
		t.Fatal("externalStoredLongURL() error = nil, want error")
	}
}

func orderedExternalClick(clickID int64) models.ExternalShortLinkClick {
	return models.ExternalShortLinkClick{
		ClickID:   clickID,
		ShortCode: "abc1",
		LinkID:    7,
		LongURL:   "https://example.com/destination",
		ClickedAt: time.Now().UTC(),
	}
}

func TestAnomalousExternalClickReasonRejectsUnorderedIDs(t *testing.T) {
	tests := []struct {
		name    string
		clickID int64
		highest int64
		want    string
	}{
		{name: "in order", clickID: 11, highest: 10, want: ""},
		{name: "first row", clickID: 1, highest: 0, want: ""},
		{name: "zero id", clickID: 0, highest: 10, want: "external click ID is not a positive integer"},
		{name: "negative id", clickID: -3, highest: 0, want: "external click ID is not a positive integer"},
		{name: "repeated id", clickID: 10, highest: 10, want: "external click ID is not strictly increasing"},
		{name: "lowered id", clickID: 9, highest: 10, want: "external click ID is not strictly increasing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := anomalousExternalClickReason(orderedExternalClick(test.clickID), test.highest); got != test.want {
				t.Fatalf("anomalousExternalClickReason(id=%d, highest=%d) = %q, want %q", test.clickID, test.highest, got, test.want)
			}
		})
	}
}

func TestInvalidExternalClickReasonIgnoresOrderingAnomalies(t *testing.T) {
	// IDs are classified by anomalousExternalClickReason so that a row with a
	// usable ID keeps advancing the cursor after its payload is quarantined.
	complete := orderedExternalClick(11)
	if got := invalidExternalClickReason(complete); got != "" {
		t.Fatalf("invalidExternalClickReason(complete) = %q, want no reason", got)
	}
	unusableID := complete
	unusableID.ClickID = 0
	if got := invalidExternalClickReason(unusableID); got != "" {
		t.Fatalf("invalidExternalClickReason(id=0) = %q, want no reason", got)
	}
	missing := complete
	missing.LongURL = "   "
	if got := invalidExternalClickReason(missing); got != "missing required click fields" {
		t.Fatalf("invalidExternalClickReason(blank url) = %q, want the missing-fields reason", got)
	}
}

func TestExternalClickDeadLetterKeepsTheRowPayload(t *testing.T) {
	click := orderedExternalClick(42)
	deadLetter := externalClickDeadLetter("external_shortlink", click, "external click ID is not strictly increasing")
	if deadLetter.Source != "external_shortlink" || deadLetter.ExternalClickID != 42 {
		t.Fatalf("dead letter key = (%q, %d), want (external_shortlink, 42)", deadLetter.Source, deadLetter.ExternalClickID)
	}
	if deadLetter.Reason != "external click ID is not strictly increasing" {
		t.Fatalf("reason = %q", deadLetter.Reason)
	}
	if deadLetter.Payload == "" || deadLetter.Payload[0] != '{' {
		t.Fatalf("payload = %q, want the serialized click", deadLetter.Payload)
	}
}

func TestDeadLetterReasonSummaryIsDeterministic(t *testing.T) {
	deadLetters := []*models.ExternalShortLinkClickDeadLetter{
		{Reason: "b"},
		{Reason: "a"},
		{Reason: "a"},
	}
	if got, want := deadLetterReasonSummary(deadLetters), `"a":2,"b":1`; got != want {
		t.Fatalf("deadLetterReasonSummary() = %q, want %q", got, want)
	}
}
