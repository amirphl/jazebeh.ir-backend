package businessflow

import "testing"

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
