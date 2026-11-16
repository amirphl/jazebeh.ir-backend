package repository

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionCalculationLatestLookupsAreScopedAndStable(t *testing.T) {
	source, err := os.ReadFile("campaign_targeting_execution_calculation_repository.go")
	if err != nil {
		t.Fatalf("read execution calculation repository: %v", err)
	}
	text := string(source)
	for _, fragment := range []string{
		"LatestByCampaignID", "LatestByInput", "selection_input_hash = ? AND requested_audience_count = ?",
		"Order(\"created_at DESC, id DESC\")",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("execution calculation lookup is missing %q", fragment)
		}
	}
}
