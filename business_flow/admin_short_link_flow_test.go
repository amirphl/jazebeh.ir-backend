package businessflow

import (
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestNormalizeAdminShortLinkDomainRequiresJzbeHTTPSOrigin(t *testing.T) {
	for _, value := range []string{"jzbe.ir", "https://jzbe.ir", "https://jzbe.ir/"} {
		if got := normalizeDomain(value); got != "https://jzbe.ir" {
			t.Fatalf("normalizeDomain(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"http://jzbe.ir", "https://evil.example", "https://jzbe.ir/x", "https://user@jzbe.ir", "https://jzbe.ir?x=1"} {
		if got := normalizeDomain(value); got != "" {
			t.Fatalf("normalizeDomain(%q) = %q, want empty", value, got)
		}
	}
}

func TestInspectAdminCSVRejectsInvalidDestinationAndCountsSkippedRows(t *testing.T) {
	data := []byte("long_link,name\nhttps://example.com/a,one\n,blank\n")
	total, skipped, err := inspectAdminCSV(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || skipped != 1 {
		t.Fatalf("got total=%d skipped=%d, want 2, 1", total, skipped)
	}
	if _, _, err := inspectAdminCSV([]byte("long_link\nnot a url\n"), 0); err == nil {
		t.Fatal("expected invalid destination error")
	}
}

func TestNextUploadAttempt(t *testing.T) {
	fallback := models.AdminShortLinkUploadJob{}
	if got := nextUploadAttempt(&fallback, fallback.CreatedAt); !got.Equal(fallback.CreatedAt) {
		t.Fatal("expected fallback when no retry time exists")
	}
}
