package models

import "testing"

func TestFormatShortLinkUIDUsesFixedSixCharacterBase36(t *testing.T) {
	tests := []struct {
		value uint64
		want  string
	}{
		{0, "000000"},
		{35, "00000z"},
		{36, "000010"},
		{36*36*36*36*36 - 1, "0zzzzz"},
		{36 * 36 * 36 * 36 * 36, "100000"},
		{ShortLinkUIDCapacity - 1, "zzzzzz"},
	}
	for _, test := range tests {
		got, err := FormatShortLinkUID(test.value)
		if err != nil {
			t.Fatalf("FormatShortLinkUID(%d): %v", test.value, err)
		}
		if got != test.want {
			t.Fatalf("FormatShortLinkUID(%d) = %q, want %q", test.value, got, test.want)
		}
	}
	if _, err := FormatShortLinkUID(ShortLinkUIDCapacity); err == nil {
		t.Fatal("expected exhaustion error")
	}
}
