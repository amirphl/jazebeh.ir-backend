package repository

import (
	"os"
	"strings"
	"testing"
)

func TestCampaignAudienceReportJobClaimIsTransactionalAndLeaseGuarded(t *testing.T) {
	source, err := os.ReadFile("campaign_audience_report_job_repository.go")
	if err != nil {
		t.Fatalf("read repository source: %v", err)
	}
	for _, want := range []string{"Transaction(func(tx *gorm.DB)", "FOR UPDATE SKIP LOCKED", "lease_token", "Where(\"id=? AND lease_token=?\""} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("report job repository must contain %q", want)
		}
	}
}
