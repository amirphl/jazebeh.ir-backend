package businessflow

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"gorm.io/gorm"
)

// PlatformBasePriceAdminFlow defines admin operations for platform base prices.
type PlatformBasePriceAdminFlow interface {
	AdminListPlatformBasePrices(ctx context.Context) (*dto.AdminListPlatformBasePricesResponse, error)
	AdminUpdatePlatformBasePrice(
		ctx context.Context,
		req *dto.AdminUpdatePlatformBasePriceRequest,
	) (*dto.AdminUpdatePlatformBasePriceResponse, error)
}

type PlatformBasePriceAdminFlowImpl struct {
	platformBasePriceRepo repository.PlatformBasePriceRepository
	auditRepo             repository.AuditLogRepository
}

func NewPlatformBasePriceAdminFlow(
	platformBasePriceRepo repository.PlatformBasePriceRepository,
	auditRepo repository.AuditLogRepository,
) PlatformBasePriceAdminFlow {
	return &PlatformBasePriceAdminFlowImpl{
		platformBasePriceRepo: platformBasePriceRepo,
		auditRepo:             auditRepo,
	}
}

func (f *PlatformBasePriceAdminFlowImpl) AdminListPlatformBasePrices(
	ctx context.Context,
) (*dto.AdminListPlatformBasePricesResponse, error) {
	rows, err := f.platformBasePriceRepo.List(ctx)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_LIST_FAILED", "failed to list platform base prices", err)
	}

	items := make([]dto.AdminPlatformBasePriceItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, dto.AdminPlatformBasePriceItem{
			Platform: row.Platform,
			Price:    row.Price,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Platform < items[j].Platform
	})

	resp := &dto.AdminListPlatformBasePricesResponse{
		Message: "Platform base prices retrieved successfully",
		Items:   items,
	}

	logAdminAction(
		ctx,
		f.auditRepo,
		models.AuditActionAdminPlatformBasePriceList,
		"Admin listed platform base prices",
		true,
		nil,
		map[string]any{
			"items": len(items),
		},
		nil,
	)

	return resp, nil
}

func (f *PlatformBasePriceAdminFlowImpl) AdminUpdatePlatformBasePrice(
	ctx context.Context,
	req *dto.AdminUpdatePlatformBasePriceRequest,
) (*dto.AdminUpdatePlatformBasePriceResponse, error) {
	if req == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "request is required", nil)
	}

	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform == "" {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_PLATFORM_REQUIRED", "platform is required", ErrCampaignPlatformRequired)
	}
	if !models.IsValidCampaignPlatform(platform) {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_PLATFORM_INVALID", "invalid platform", ErrCampaignPlatformInvalid)
	}
	if req.Price <= 0 {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_INVALID", "price must be greater than zero", ErrPriceFactorInvalid)
	}

	if err := f.platformBasePriceRepo.UpdatePriceByPlatform(ctx, platform, req.Price); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewBusinessError("PLATFORM_BASE_PRICE_NOT_FOUND", "platform base price not found", ErrPlatformBasePriceNotFound)
		}
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_UPDATE_FAILED", "failed to update platform base price", err)
	}

	resp := &dto.AdminUpdatePlatformBasePriceResponse{
		Message:  "Platform base price updated successfully",
		Platform: platform,
		Price:    req.Price,
	}

	logAdminAction(
		ctx,
		f.auditRepo,
		models.AuditActionAdminPlatformBasePriceUpdate,
		"Admin updated platform base price",
		true,
		nil,
		map[string]any{
			"platform": platform,
			"price":    req.Price,
		},
		nil,
	)

	return resp, nil
}
