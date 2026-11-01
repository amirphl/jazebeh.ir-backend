package repository

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateCampaignRefundReconciliationError(t *testing.T) {
	input := strings.Repeat("€", 600)
	got := truncateCampaignRefundReconciliationError(input)
	if len(got) > campaignRefundReconciliationErrorMessageLimit {
		t.Fatalf("error length = %d, limit = %d", len(got), campaignRefundReconciliationErrorMessageLimit)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated error is invalid UTF-8")
	}
}
