package businessflow

import (
	"context"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// PlatformSettingsAdminFlow defines admin operations for platform settings.
type PlatformSettingsAdminFlow interface {
	ChangePlatformSettingsStatusByAdmin(
		ctx context.Context,
		req *dto.AdminChangePlatformSettingsStatusRequest,
		metadata *ClientMetadata,
	) (*dto.AdminChangePlatformSettingsStatusResponse, error)
	ListPlatformSettingsByAdmin(ctx context.Context) (*dto.AdminListPlatformSettingsResponse, error)
	AddMetadataByAdmin(
		ctx context.Context,
		req *dto.AdminAddPlatformSettingsMetadataRequest,
		metadata *ClientMetadata,
	) (*dto.AdminAddPlatformSettingsMetadataResponse, error)
}

type PlatformSettingsAdminFlowImpl struct {
	platformSettingsRepo repository.PlatformSettingsRepository
	multimediaRepo       repository.MultimediaAssetRepository
}

func NewPlatformSettingsAdminFlow(
	platformSettingsRepo repository.PlatformSettingsRepository,
	multimediaRepo repository.MultimediaAssetRepository,
) PlatformSettingsAdminFlow {
	return &PlatformSettingsAdminFlowImpl{
		platformSettingsRepo: platformSettingsRepo,
		multimediaRepo:       multimediaRepo,
	}
}

func (f *PlatformSettingsAdminFlowImpl) ChangePlatformSettingsStatusByAdmin(
	ctx context.Context,
	req *dto.AdminChangePlatformSettingsStatusRequest,
	metadata *ClientMetadata,
) (*dto.AdminChangePlatformSettingsStatusResponse, error) {
	_ = metadata
	if req == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "request is required", nil)
	}
	if req.ID == 0 {
		return nil, NewBusinessError("PLATFORM_SETTINGS_ID_REQUIRED", "platform settings id is required", nil)
	}

	targetStatus := models.PlatformSettingsStatus(strings.ToLower(strings.TrimSpace(req.Status)))
	if !targetStatus.Valid() {
		return nil, NewBusinessError("INVALID_PLATFORM_SETTINGS_STATUS", "invalid platform settings status", nil)
	}
	if targetStatus == models.PlatformSettingsStatusInitialized {
		return nil, NewBusinessError("PLATFORM_SETTINGS_STATUS_CHANGE_NOT_ALLOWED", "can not move status to initialized", nil)
	}

	row, err := f.platformSettingsRepo.ByID(ctx, req.ID)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_LOOKUP_FAILED", "failed to lookup platform settings", err)
	}
	if row == nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_NOT_FOUND", "platform settings not found", nil)
	}

	if err := f.platformSettingsRepo.UpdateStatus(ctx, req.ID, targetStatus); err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_STATUS_UPDATE_FAILED", "failed to update platform settings status", err)
	}

	return &dto.AdminChangePlatformSettingsStatusResponse{
		Message: "Platform settings status changed successfully",
		ID:      req.ID,
		Status:  string(targetStatus),
	}, nil
}

func (f *PlatformSettingsAdminFlowImpl) ListPlatformSettingsByAdmin(ctx context.Context) (*dto.AdminListPlatformSettingsResponse, error) {
	rows, err := f.platformSettingsRepo.ByFilter(ctx, models.PlatformSettingsFilter{}, "id DESC", 0, 0)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_LIST_FAILED", "failed to list platform settings", err)
	}

	items := make([]dto.AdminPlatformSettingsItem, 0, len(rows))
	for _, row := range rows {
		var multimediaUUID *string
		if row.MultimediaID != nil {
			asset, err := f.multimediaRepo.ByID(ctx, *row.MultimediaID)
			if err != nil {
				return nil, NewBusinessError("PLATFORM_SETTINGS_LIST_FAILED", "failed to lookup multimedia asset", err)
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
				return nil, NewBusinessError("PLATFORM_SETTINGS_LIST_FAILED", "failed to lookup business license asset", err)
			}
			if asset != nil {
				u := asset.UUID.String()
				businessLicenseUUID = &u
			}
		}

		items = append(items, dto.AdminPlatformSettingsItem{
			ID:                  row.ID,
			UUID:                row.UUID.String(),
			CustomerID:          row.CustomerID,
			Platform:            row.Platform,
			Name:                row.Name,
			Description:         row.Description,
			Website:             row.Website,
			MultimediaUUID:      multimediaUUID,
			BusinessLicenseUUID: businessLicenseUUID,
			Metadata:            row.Metadata,
			Status:              string(row.Status),
			CreatedAt:           row.CreatedAt.Format(time.RFC3339),
			UpdatedAt:           row.UpdatedAt.Format(time.RFC3339),
		})
	}

	return &dto.AdminListPlatformSettingsResponse{
		Message: "Platform settings retrieved",
		Items:   items,
	}, nil
}

func (f *PlatformSettingsAdminFlowImpl) AddMetadataByAdmin(
	ctx context.Context,
	req *dto.AdminAddPlatformSettingsMetadataRequest,
	metadata *ClientMetadata,
) (*dto.AdminAddPlatformSettingsMetadataResponse, error) {
	_ = metadata
	if req == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "request is required", nil)
	}
	if req.ID == 0 {
		return nil, NewBusinessError("PLATFORM_SETTINGS_ID_REQUIRED", "platform settings id is required", nil)
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return nil, NewBusinessError("PLATFORM_SETTINGS_METADATA_KEY_REQUIRED", "metadata key is required", nil)
	}

	row, err := f.platformSettingsRepo.ByID(ctx, req.ID)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_LOOKUP_FAILED", "failed to lookup platform settings", err)
	}
	if row == nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_NOT_FOUND", "platform settings not found", nil)
	}

	if err := f.platformSettingsRepo.AppendMetadata(ctx, req.ID, key, req.Value); err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_METADATA_UPDATE_FAILED", "failed to append metadata", err)
	}

	updated, err := f.platformSettingsRepo.ByID(ctx, req.ID)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_LOOKUP_FAILED", "failed to lookup platform settings", err)
	}
	if updated == nil {
		return nil, NewBusinessError("PLATFORM_SETTINGS_NOT_FOUND", "platform settings not found", nil)
	}
	if updated.Metadata == nil {
		updated.Metadata = map[string]any{}
	}

	return &dto.AdminAddPlatformSettingsMetadataResponse{
		Message:  "Platform settings metadata updated successfully",
		ID:       updated.ID,
		Metadata: updated.Metadata,
	}, nil
}
