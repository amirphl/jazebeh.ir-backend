package businessflow

import (
	"context"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
)

// SegmentPriceFactorFlow defines public operations for segment price factors.
type SegmentPriceFactorFlow interface {
	ListLatestSegmentPriceFactors(ctx context.Context, platform *string) (*dto.ListLatestSegmentPriceFactorsResponse, error)
}

// ListLatestSegmentPriceFactors returns the latest price factor per level3 for authenticated users.
func (f *SegmentPriceFactorFlowImpl) ListLatestSegmentPriceFactors(
	ctx context.Context,
	platform *string,
) (*dto.ListLatestSegmentPriceFactorsResponse, error) {
	normalizedPlatform, err := normalizeAndValidateSegmentPlatform(platform)
	if err != nil {
		return nil, err
	}

	rows, err := f.segmentPriceFactorRepo.ListLatestByLevel3ForPlatform(ctx, normalizedPlatform)
	if err != nil {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_LIST_FAILED", "Failed to list segment price factors", err)
	}

	items := mapSegmentPriceFactorRows(rows)

	return &dto.ListLatestSegmentPriceFactorsResponse{
		Message: "Segment price factors retrieved successfully",
		Items:   items.PublicItems,
	}, nil
}
