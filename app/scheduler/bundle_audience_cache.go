package scheduler

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// BundleAudienceSelection is the in-memory representation of a persisted bundle selection snapshot.
type BundleAudienceSelection struct {
	ID            uint
	CorrelationID string
	// IDs is either the bundle usage set or one campaign's immutable allocation.
	IDs map[int64]struct{}
}

// BundleAudienceCache resolves immutable per-campaign allocations. Cross-
// campaign uniqueness lives in the normalized database ledger, not this cache.
type BundleAudienceCache struct {
	repo repository.BundleAudienceSelectionRepository
}

func NewBundleAudienceCache(repo repository.BundleAudienceSelectionRepository) *BundleAudienceCache {
	return &BundleAudienceCache{repo: repo}
}

func (c *BundleAudienceCache) ByCampaignID(ctx context.Context, campaignID uint) (*BundleAudienceSelection, error) {
	if c == nil || c.repo == nil {
		return nil, errors.New("bundle audience cache repository not configured")
	}
	row, err := c.repo.ByCampaignID(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	sel := &BundleAudienceSelection{
		ID:            row.ID,
		CorrelationID: row.CorrelationID,
		IDs:           make(map[int64]struct{}, len(row.SelectedAudienceIDs)),
	}
	for _, id := range row.SelectedAudienceIDs {
		sel.IDs[id] = struct{}{}
	}
	return sel, nil
}

// SaveForCampaign appends a campaign allocation or returns the original row
// when the same campaign is retried.
func (c *BundleAudienceCache) SaveForCampaign(
	ctx context.Context,
	customerID, bundleID, campaignID uint,
	correlationID string,
	ids []int64,
) (*BundleAudienceSelection, error) {
	if c == nil || c.repo == nil {
		return nil, errors.New("bundle audience cache repository not configured")
	}
	row, err := c.repo.InsertForCampaign(ctx, customerID, bundleID, campaignID, correlationID, ids)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errors.New("bundle audience selection repository returned no saved row")
	}
	sel := &BundleAudienceSelection{
		ID:            row.ID,
		CorrelationID: row.CorrelationID,
		IDs:           make(map[int64]struct{}, len(row.SelectedAudienceIDs)),
	}
	for _, id := range row.SelectedAudienceIDs {
		sel.IDs[id] = struct{}{}
	}
	return sel, nil
}
