package scheduler

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

type idempotentBundleSelectionRepository struct {
	mu      sync.Mutex
	row     *models.BundleAudienceSelection
	inserts int
}

func (r *idempotentBundleSelectionRepository) ByCampaignID(_ context.Context, campaignID uint) (*models.BundleAudienceSelection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row == nil || r.row.CampaignID == nil || *r.row.CampaignID != campaignID {
		return nil, nil
	}
	clone := *r.row
	return &clone, nil
}

func (r *idempotentBundleSelectionRepository) InsertForCampaign(
	_ context.Context,
	customerID, bundleID, campaignID uint,
	correlationID string,
	ids []int64,
) (*models.BundleAudienceSelection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row == nil {
		r.inserts++
		r.row = &models.BundleAudienceSelection{
			ID: 91, CustomerID: customerID, BundleID: bundleID, CampaignID: &campaignID,
			CorrelationID: correlationID, SelectedAudienceIDs: append([]int64(nil), ids...), AudienceCount: int64(len(ids)),
		}
	}
	clone := *r.row
	return &clone, nil
}

func TestBundleAudienceCacheConcurrentRetryReturnsOneImmutableAllocation(t *testing.T) {
	repo := &idempotentBundleSelectionRepository{}
	cache := NewBundleAudienceCache(repo)
	const callers = 64
	results := make(chan *BundleAudienceSelection, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			selection, err := cache.SaveForCampaign(context.Background(), 1, 2, 3, "campaign:3", []int64{int64(100 + offset)})
			if err != nil {
				errs <- err
				return
			}
			results <- selection
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent save: %v", err)
	}
	var stableID int64
	for result := range results {
		if result.ID != 91 || len(result.IDs) != 1 {
			t.Fatalf("unexpected retry allocation: %#v", result)
		}
		for id := range result.IDs {
			if stableID == 0 {
				stableID = id
			}
			if id != stableID {
				t.Fatalf("retry changed audience from %d to %d", stableID, id)
			}
		}
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.inserts != 1 {
		t.Fatalf("allocation inserts = %d, want 1", repo.inserts)
	}
}

type nilBundleAudienceSelectionRepository struct{}

func (nilBundleAudienceSelectionRepository) ByCampaignID(context.Context, uint) (*models.BundleAudienceSelection, error) {
	return nil, nil
}

func (nilBundleAudienceSelectionRepository) InsertForCampaign(
	context.Context,
	uint,
	uint,
	uint,
	string,
	[]int64,
) (*models.BundleAudienceSelection, error) {
	return nil, nil
}

func TestBundleAudienceCacheRejectsNilSavedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	bundleCache := NewBundleAudienceCache(nilBundleAudienceSelectionRepository{})
	if _, err := bundleCache.SaveForCampaign(ctx, 1, 2, 3, "correlation", []int64{1}); err == nil ||
		!strings.Contains(err.Error(), "no saved row") {
		t.Fatalf("bundle SaveForCampaign must reject a nil repository row, got %v", err)
	}
}
