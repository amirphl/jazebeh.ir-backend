package businessflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

// PlatformSettingsFlow defines operations for platform settings.
type PlatformSettingsFlow interface {
	CreatePlatformSettings(
		ctx context.Context,
		req *dto.CreatePlatformSettingsRequest,
		metadata *ClientMetadata,
	) (*dto.CreatePlatformSettingsResponse, error)
	ListPlatformSettings(ctx context.Context, customerID uint) (*dto.ListPlatformSettingsResponse, error)
}

// PlatformSettingsFlowImpl implements PlatformSettingsFlow.
type PlatformSettingsFlowImpl struct {
	platformSettingsRepo repository.PlatformSettingsRepository
	multimediaRepo       repository.MultimediaAssetRepository
	notifier             services.NotificationService
	adminCfg             config.AdminConfig
}

// NewPlatformSettingsFlow creates a new platform settings flow.
func NewPlatformSettingsFlow(
	platformSettingsRepo repository.PlatformSettingsRepository,
	multimediaRepo repository.MultimediaAssetRepository,
	notifier services.NotificationService,
	adminCfg config.AdminConfig,
) PlatformSettingsFlow {
	return &PlatformSettingsFlowImpl{
		platformSettingsRepo: platformSettingsRepo,
		multimediaRepo:       multimediaRepo,
		notifier:             notifier,
		adminCfg:             adminCfg,
	}
}

func (f *PlatformSettingsFlowImpl) CreatePlatformSettings(
	ctx context.Context,
	req *dto.CreatePlatformSettingsRequest,
	metadata *ClientMetadata,
) (*dto.CreatePlatformSettingsResponse, error) {
	if req == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "request is required", nil)
	}

	customerIDAny := ctx.Value(utils.CustomerIDKey)
	customerID, ok := customerIDAny.(uint)
	if !ok || customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "customer id is required", nil)
	}

	var normalizedName *string
	if req.Name != nil {
		t := strings.TrimSpace(*req.Name)
		normalizedName = &t
	}
	if normalizedName != nil && *normalizedName != "" {
		exists, err := f.platformSettingsRepo.Exists(ctx, models.PlatformSettingsFilter{
			CustomerID: &customerID,
			Name:       normalizedName,
		})
		if err != nil {
			return nil, NewBusinessError(
				"PLATFORM_SETTINGS_DUPLICATE_CHECK_FAILED",
				"failed to check duplicate platform settings name",
				err,
			)
		}
		if exists {
			return nil, NewBusinessError(
				"PLATFORM_SETTINGS_NAME_ALREADY_EXISTS",
				"platform settings name already exists for this customer",
				ErrPlatformSettingsNameExists,
			)
		}
	}

	status := models.PlatformSettingsStatusInitialized

	var multimediaID *uint
	var multimediaUUID *string
	if req.MultimediaUUID != nil && *req.MultimediaUUID != "" {
		asset, err := f.multimediaRepo.ByUUID(ctx, *req.MultimediaUUID)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			return nil, NewBusinessError("MULTIMEDIA_NOT_FOUND", "multimedia not found", nil)
		}
		multimediaID = &asset.ID
		assetUUID := asset.UUID.String()
		multimediaUUID = &assetUUID
	}

	var businessLicenseID *uint
	var businessLicenseUUID *string
	if req.BusinessLicenseUUID != nil && *req.BusinessLicenseUUID != "" {
		asset, err := f.multimediaRepo.ByUUID(ctx, *req.BusinessLicenseUUID)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			return nil, NewBusinessError("BUSINESS_LICENSE_NOT_FOUND", "business license multimedia not found", nil)
		}
		businessLicenseID = &asset.ID
		assetUUID := asset.UUID.String()
		businessLicenseUUID = &assetUUID
	}

	row := models.PlatformSettings{
		UUID:              uuid.New(),
		CustomerID:        customerID,
		Platform:          req.Platform,
		Name:              normalizedName,
		Description:       req.Description,
		Website:           req.Website,
		MultimediaID:      multimediaID,
		BusinessLicenseID: businessLicenseID,
		Metadata:          map[string]any{},
		Status:            status,
	}

	if err := f.platformSettingsRepo.Save(ctx, &row); err != nil {
		return nil, err
	}

	// Notify admins via SMS (best-effort).
	if f.notifier != nil {
		name := "-"
		if row.Name != nil && strings.TrimSpace(*row.Name) != "" {
			name = strings.TrimSpace(*row.Name)
		}
		msg := fmt.Sprintf("New platform settings created: platform=%s\n name=%s", row.Platform, name)
		go func() {
			for _, mobile := range f.adminCfg.ActiveMobiles() {
				_ = f.notifier.SendSMS(context.Background(), mobile, msg, nil)
			}
		}()
	}

	return &dto.CreatePlatformSettingsResponse{
		Message:             "Platform settings created successfully",
		ID:                  row.ID,
		Platform:            row.Platform,
		Name:                row.Name,
		Description:         row.Description,
		Website:             row.Website,
		MultimediaUUID:      multimediaUUID,
		BusinessLicenseUUID: businessLicenseUUID,
		Status:              string(row.Status),
		CreatedAt:           row.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *PlatformSettingsFlowImpl) ListPlatformSettings(ctx context.Context, customerID uint) (*dto.ListPlatformSettingsResponse, error) {
	if customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "customer id is required", nil)
	}
	rows, err := f.platformSettingsRepo.ByFilter(ctx, models.PlatformSettingsFilter{
		CustomerID: &customerID,
	}, "id DESC", 0, 0)
	if err != nil {
		return nil, err
	}

	items := make([]dto.PlatformSettingsItem, 0, len(rows))
	for _, row := range rows {
		var multimediaUUID *string
		if row.MultimediaID != nil {
			asset, err := f.multimediaRepo.ByID(ctx, *row.MultimediaID)
			if err != nil {
				return nil, err
			}
			if asset != nil {
				u := asset.UUID.String()
				multimediaUUID = &u
			}
		}
		var businessLicenseUUID *string
		if row.BusinessLicenseID != nil {
			asset, err := f.multimediaRepo.ByID(ctx, *row.BusinessLicenseID)
			if err != nil {
				return nil, err
			}
			if asset != nil {
				u := asset.UUID.String()
				businessLicenseUUID = &u
			}
		}
		items = append(items, dto.PlatformSettingsItem{
			ID:                  row.ID,
			Platform:            row.Platform,
			Name:                row.Name,
			Description:         row.Description,
			Website:             row.Website,
			MultimediaUUID:      multimediaUUID,
			BusinessLicenseUUID: businessLicenseUUID,
			Status:              string(row.Status),
			CreatedAt:           row.CreatedAt.Format(time.RFC3339),
			UpdatedAt:           row.UpdatedAt.Format(time.RFC3339),
		})
	}

	return &dto.ListPlatformSettingsResponse{
		Message: "Platform settings retrieved",
		Items:   items,
	}, nil
}
