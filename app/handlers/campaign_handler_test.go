package handlers

import (
	"math"
	"testing"
)

func TestMaxPageForSmartTargetingOffset(t *testing.T) {
	if got := maxPageForSmartTargetingOffset(1); got != math.MaxInt {
		t.Fatalf("page size 1 maximum = %d, want %d", got, math.MaxInt)
	}
	if got, want := maxPageForSmartTargetingOffset(100), math.MaxInt/100+1; got != want {
		t.Fatalf("page size 100 maximum = %d, want %d", got, want)
	}
}

func TestParseOptionalNonNegativeInt64Query(t *testing.T) {
	tests := []struct {
		value   string
		want    *int64
		wantErr bool
	}{
		{value: ""},
		{value: "0", want: int64Ptr(0)},
		{value: "123", want: int64Ptr(123)},
		{value: "-1", wantErr: true},
		{value: "not-a-number", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseOptionalNonNegativeInt64Query(tt.value)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parseOptionalNonNegativeInt64Query(%q) error = %v, wantErr %t", tt.value, err, tt.wantErr)
		}
		if tt.want == nil {
			if got != nil {
				t.Fatalf("parseOptionalNonNegativeInt64Query(%q) = %d, want nil", tt.value, *got)
			}
		} else if got == nil || *got != *tt.want {
			t.Fatalf("parseOptionalNonNegativeInt64Query(%q) = %v, want %d", tt.value, got, *tt.want)
		}
	}
}

func int64Ptr(value int64) *int64 { return &value }
