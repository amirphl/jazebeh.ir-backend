package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

// MockExternalShortLinkClient simulates the external mapping and click API.
// Mappings live for the lifetime of this process; clicks are not synthesized.
type MockExternalShortLinkClient struct {
	mu       sync.Mutex
	mappings map[string]models.ShortLink
}

func NewMockExternalShortLinkClient() *MockExternalShortLinkClient {
	return &MockExternalShortLinkClient{mappings: make(map[string]models.ShortLink)}
}

func (c *MockExternalShortLinkClient) UploadMappings(ctx context.Context, links []*models.ShortLink) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, link := range links {
		if link == nil || link.UID == "" {
			return fmt.Errorf("mock short-link: empty mapping")
		}
	}
	for _, link := range links {
		c.mappings[link.UID] = *link
	}
	log.Printf("mock provider=shortlink action=upload count=%d", len(links))
	observability.RecordMockOperation("shortlink", "upload", "success", len(links))
	return nil
}

func (c *MockExternalShortLinkClient) FetchClicks(ctx context.Context, afterID int64, _ int) (*ExternalShortLinkClickPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &ExternalShortLinkClickPage{Clicks: []models.ExternalShortLinkClick{}, NextAfterID: afterID}, nil
}

func (c *MockExternalShortLinkClient) AcknowledgeClicks(ctx context.Context, _ int64) error {
	return ctx.Err()
}
