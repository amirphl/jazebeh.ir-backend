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
