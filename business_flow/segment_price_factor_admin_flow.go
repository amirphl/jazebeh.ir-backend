package businessflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

// SegmentPriceFactorAdminFlow defines admin operations for segment price factors.
type SegmentPriceFactorAdminFlow interface {
	AdminCreateSegmentPriceFactor(
		ctx context.Context,
		req *dto.AdminCreateSegmentPriceFactorRequest,
	) (*dto.AdminCreateSegmentPriceFactorResponse, error)
	AdminListSegmentPriceFactors(ctx context.Context, platform *string) (*dto.AdminListSegmentPriceFactorsResponse, error)
	AdminListLevel3Options(ctx context.Context) (*dto.AdminListLevel3OptionsResponse, error)
}

type SegmentPriceFactorFlowImpl struct {
	segmentPriceFactorRepo repository.SegmentPriceFactorRepository
	audienceSpecRepo       repository.AudienceSpecRepository
}

func NewSegmentPriceFactorFlow(
	segmentPriceFactorRepo repository.SegmentPriceFactorRepository,
	audienceSpecRepo repository.AudienceSpecRepository,
) *SegmentPriceFactorFlowImpl {
	return &SegmentPriceFactorFlowImpl{
		segmentPriceFactorRepo: segmentPriceFactorRepo,
		audienceSpecRepo:       audienceSpecRepo,
	}
}

// AdminCreateSegmentPriceFactor upserts a price factor for a given level3 (latest row wins).
func (f *SegmentPriceFactorFlowImpl) AdminCreateSegmentPriceFactor(
	ctx context.Context,
	req *dto.AdminCreateSegmentPriceFactorRequest,
) (*dto.AdminCreateSegmentPriceFactorResponse, error) {
	level3 := strings.TrimSpace(req.Level3)
	if level3 == "" {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_LEVEL3_REQUIRED", "Level3 is required", ErrLevel3Required)
	}
	if strings.TrimSpace(req.Platform) == "" {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_PLATFORM_REQUIRED", "Platform is required", ErrSegmentPriceFactorPlatformInvalid)
	}
	platform, err := normalizeAndValidateSegmentPlatform(&req.Platform)
	if err != nil {
		return nil, err
	}
	if req.PriceFactor <= 0 {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_PRICE_INVALID", "Price factor must be greater than zero", ErrPriceFactorInvalid)
	}

	row := &models.SegmentPriceFactor{
		Platform:    platform,
		Level3:      level3,
		PriceFactor: req.PriceFactor,
		CreatedAt:   utils.UTCNow(),
		UpdatedAt:   utils.UTCNow(),
	}
	if err := f.segmentPriceFactorRepo.Save(ctx, row); err != nil {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_SAVE_FAILED", "Failed to save segment price factor", err)
	}

	return &dto.AdminCreateSegmentPriceFactorResponse{
		Message: "Segment price factor saved successfully",
	}, nil
}

// AdminListSegmentPriceFactors returns the latest price factor per level3.
func (f *SegmentPriceFactorFlowImpl) AdminListSegmentPriceFactors(
	ctx context.Context,
	platform *string,
) (*dto.AdminListSegmentPriceFactorsResponse, error) {
	normalizedPlatform, err := normalizeAndValidateSegmentPlatform(platform)
	if err != nil {
		return nil, err
	}

	rows, err := f.segmentPriceFactorRepo.ListLatestByLevel3ForPlatform(ctx, normalizedPlatform)
	if err != nil {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_LIST_FAILED", "Failed to list segment price factors", err)
	}

	items := mapSegmentPriceFactorRows(rows)

	return &dto.AdminListSegmentPriceFactorsResponse{
		Message: "Segment price factors retrieved successfully",
		Items:   items.AdminItems,
	}, nil
}

// AdminListLevel3Options returns active, database-backed level3 keys with a
// positive audience. It never reads the legacy audience-spec file.
func (f *SegmentPriceFactorFlowImpl) AdminListLevel3Options(ctx context.Context) (*dto.AdminListLevel3OptionsResponse, error) {
	rows, err := f.audienceSpecRepo.ListActive(ctx)
	if err != nil {
		return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_LEVEL3_LOAD_FAILED", "Failed to load audience spec from database", err)
	}

	set := make(map[string]struct{})
	for _, row := range rows {
		if !row.StatsFound {
			return nil, NewBusinessError(
				"SEGMENT_PRICE_FACTOR_LEVEL3_LOAD_FAILED",
				"An active tag reference has no matching audience statistics",
				fmt.Errorf("active tag %d has no matching src_layer_all_stats row", row.TagID),
			)
		}
		capacity, capacityErr := audienceCapacityForPlatform(row, models.CampaignPlatformBale)
		if capacityErr != nil {
			return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_LEVEL3_LOAD_FAILED", "Audience statistics are invalid", capacityErr)
		}
		if capacity > 0 {
			level3 := strings.TrimSpace(row.Layer3Category)
			if level3 != "" {
				set[level3] = struct{}{}
			}
		}
	}

	items := make([]string, 0, len(set))
	for k := range set {
		items = append(items, k)
	}
	sort.Strings(items)

	return &dto.AdminListLevel3OptionsResponse{
		Message: "Level3 options retrieved successfully",
		Items:   items,
	}, nil
}

func normalizeAndValidateSegmentPlatform(platform *string) (string, error) {
	if platform == nil {
		return models.SegmentPriceFactorPlatformSMS, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(*platform))
	if normalized == "" {
		return models.SegmentPriceFactorPlatformSMS, nil
	}
	if !models.IsValidSegmentPriceFactorPlatform(normalized) {
		err := fmt.Errorf("invalid platform: %s", normalized)
		return "", NewBusinessError("SEGMENT_PRICE_FACTOR_PLATFORM_INVALID", "Invalid segment price factor platform", err)
	}
	return normalized, nil
}

type segmentPriceFactorMappedItems struct {
	AdminItems  []dto.AdminSegmentPriceFactorItem
	PublicItems []dto.SegmentPriceFactorItem
}

func mapSegmentPriceFactorRows(rows []*models.SegmentPriceFactor) segmentPriceFactorMappedItems {
	adminItems := make([]dto.AdminSegmentPriceFactorItem, 0, len(rows))
	publicItems := make([]dto.SegmentPriceFactorItem, 0, len(rows))
	for _, r := range rows {
		createdAt := r.CreatedAt.Format(time.RFC3339)
		adminItems = append(adminItems, dto.AdminSegmentPriceFactorItem{
			Level3:      r.Level3,
			Platform:    r.Platform,
			PriceFactor: r.PriceFactor,
			CreatedAt:   createdAt,
		})
		publicItems = append(publicItems, dto.SegmentPriceFactorItem{
			Level3:      r.Level3,
			Platform:    r.Platform,
			PriceFactor: r.PriceFactor,
			CreatedAt:   createdAt,
		})
	}

	// Sort by level3 for deterministic output
	sort.Slice(adminItems, func(i, j int) bool {
		return strings.Compare(adminItems[i].Level3, adminItems[j].Level3) < 0
	})
	sort.Slice(publicItems, func(i, j int) bool {
		return strings.Compare(publicItems[i].Level3, publicItems[j].Level3) < 0
	})

	return segmentPriceFactorMappedItems{
		AdminItems:  adminItems,
		PublicItems: publicItems,
	}
}
