package scheduler

import (
	"context"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestMockExternalShortLinkClient(t *testing.T) {
	client := NewMockExternalShortLinkClient()
	ctx := context.Background()
	if err := client.UploadMappings(ctx, []*models.ShortLink{{UID: "abc", LongLink: "https://example.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.mappings["abc"]; !ok {
		t.Fatal("mapping was not stored")
	}
	page, err := client.FetchClicks(ctx, 42, 10)
	if err != nil || page.NextAfterID != 42 || page.HasMore || len(page.Clicks) != 0 {
		t.Fatalf("unexpected page: %+v, %v", page, err)
	}
	if err := client.AcknowledgeClicks(ctx, 42); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := client.UploadMappings(canceled, nil); err == nil {
		t.Fatal("expected context cancellation")
	}
}
