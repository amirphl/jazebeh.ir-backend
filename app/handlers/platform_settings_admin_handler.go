package handlers

import (
	"context"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

type PlatformSettingsAdminHandlerInterface interface {
	List(c fiber.Ctx) error
	ChangeStatus(c fiber.Ctx) error
	AddMetadata(c fiber.Ctx) error
}

type PlatformSettingsAdminHandler struct {
	flow      businessflow.PlatformSettingsAdminFlow
	validator *validator.Validate
}

func NewPlatformSettingsAdminHandler(flow businessflow.PlatformSettingsAdminFlow) PlatformSettingsAdminHandlerInterface {
	return &PlatformSettingsAdminHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

func (h *PlatformSettingsAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PlatformSettingsAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// List lists all platform settings for admins.
// @Summary Admin list platform settings
// @Description List platform settings (admin)
// @Tags Admin Platform Settings
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminListPlatformSettingsResponse} "Retrieved"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/platform-settings [get]
func (h *PlatformSettingsAdminHandler) List(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/platform-settings", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListPlatformSettingsByAdmin(ctx)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "PLATFORM_SETTINGS_LIST_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list platform settings", be.Code, nil)
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list platform settings", "PLATFORM_SETTINGS_LIST_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Platform settings retrieved", res)
}

// ChangeStatus changes platform settings status by admin.
// @Summary Admin change platform settings status
// @Description Move status to in-progress, active, or inactive (initialized is forbidden)
// @Tags Admin Platform Settings
// @Accept json
// @Produce json
// @Param request body dto.AdminChangePlatformSettingsStatusRequest true "Status change payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminChangePlatformSettingsStatusResponse} "Updated"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Platform settings not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/platform-settings/status [put]
func (h *PlatformSettingsAdminHandler) ChangeStatus(c fiber.Ctx) error {
	var req dto.AdminChangePlatformSettingsStatusRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, e := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, e.Error())
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/platform-settings/status", 30*time.Second)
	defer cancel()
	res, err := h.flow.ChangePlatformSettingsStatusByAdmin(ctx, &req, metadata)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_REQUEST",
				"PLATFORM_SETTINGS_ID_REQUIRED",
				"INVALID_PLATFORM_SETTINGS_STATUS",
				"PLATFORM_SETTINGS_STATUS_CHANGE_NOT_ALLOWED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, nil)
			case "PLATFORM_SETTINGS_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Platform settings not found", be.Code, nil)
			case "PLATFORM_SETTINGS_LOOKUP_FAILED", "PLATFORM_SETTINGS_STATUS_UPDATE_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to change platform settings status", be.Code, nil)
			}
		}
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to change platform settings status",
			"PLATFORM_SETTINGS_STATUS_UPDATE_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Platform settings status changed successfully", res)
}

// AddMetadata appends metadata key-value into platform settings metadata json.
// @Summary Admin add platform settings metadata
// @Description Add a key/value pair into platform settings metadata (merge, does not overwrite whole object)
// @Tags Admin Platform Settings
// @Accept json
// @Produce json
// @Param request body dto.AdminAddPlatformSettingsMetadataRequest true "Metadata append payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminAddPlatformSettingsMetadataResponse} "Updated"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Platform settings not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/platform-settings/metadata [put]
func (h *PlatformSettingsAdminHandler) AddMetadata(c fiber.Ctx) error {
	var req dto.AdminAddPlatformSettingsMetadataRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, e := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, e.Error())
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/platform-settings/metadata", 30*time.Second)
	defer cancel()
	res, err := h.flow.AddMetadataByAdmin(ctx, &req, metadata)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_REQUEST", "PLATFORM_SETTINGS_ID_REQUIRED", "PLATFORM_SETTINGS_METADATA_KEY_REQUIRED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, nil)
			case "PLATFORM_SETTINGS_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Platform settings not found", be.Code, nil)
			case "PLATFORM_SETTINGS_LOOKUP_FAILED", "PLATFORM_SETTINGS_METADATA_UPDATE_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update platform settings metadata", be.Code, nil)
			}
		}
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to update platform settings metadata",
			"PLATFORM_SETTINGS_METADATA_UPDATE_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Platform settings metadata updated successfully", res)
}

func (h *PlatformSettingsAdminHandler) createRequestContextWithTimeout(
	c fiber.Ctx,
	endpoint string,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	if adminID, ok := middleware.GetAdminIDFromContext(c); ok {
		ctx = context.WithValue(ctx, utils.AdminIDKey, adminID)
	}
	return ctx, cancel
}
