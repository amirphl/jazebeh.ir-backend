package models

import (
	"testing"

	"github.com/lib/pq"
)

func TestCampaignAudienceReportJobCampaignIDsUsePostgresArrayValue(t *testing.T) {
	ids := pq.Int64Array{1, 42}
	value, err := ids.Value()
	if err != nil {
		t.Fatalf("array value: %v", err)
	}
	if got, want := value.(string), "{1,42}"; got != want {
		t.Fatalf("array value = %q, want %q", got, want)
	}
}
