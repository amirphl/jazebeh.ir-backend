// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// PlatformSettingsHandlerInterface defines the contract for platform settings handlers.
type PlatformSettingsHandlerInterface interface {
	Create(c fiber.Ctx) error
	List(c fiber.Ctx) error
}

// PlatformSettingsHandler handles platform settings requests.
type PlatformSettingsHandler struct {
	flow      businessflow.PlatformSettingsFlow
	validator *validator.Validate
}

// NewPlatformSettingsHandler creates a new platform settings handler.
func NewPlatformSettingsHandler(flow businessflow.PlatformSettingsFlow) *PlatformSettingsHandler {
	return &PlatformSettingsHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

func (h *PlatformSettingsHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PlatformSettingsHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// Create creates platform settings.
// @Summary Create platform settings
// @Description Create platform settings (authenticated)
// @Tags Platform Settings
// @Accept json
// @Produce json
// @Param request body dto.CreatePlatformSettingsRequest true "Platform settings payload"
// @Success 201 {object} dto.APIResponse{data=dto.CreatePlatformSettingsResponse} "Created"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 409 {object} dto.APIResponse "Duplicate name"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/platform-settings [post]
func (h *PlatformSettingsHandler) Create(c fiber.Ctx) error {
	var req dto.CreatePlatformSettingsRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/platform-settings", 30*time.Second)
	defer cancel()
	res, err := h.flow.CreatePlatformSettings(ctx, &req, metadata)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_REQUEST":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, be.Error())
			case "MULTIMEDIA_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Multimedia not found", be.Code, be.Error())
			case "BUSINESS_LICENSE_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Business license multimedia not found", be.Code, be.Error())
			case "PLATFORM_SETTINGS_DUPLICATE_CHECK_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to check duplicate platform settings name", be.Code, nil)
			case "PLATFORM_SETTINGS_NAME_ALREADY_EXISTS":
				return h.ErrorResponse(c, fiber.StatusConflict, "Platform settings name already exists", be.Code, nil)
			case "MISSING_CUSTOMER_ID":
				return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to create platform settings",
			"PLATFORM_SETTINGS_CREATE_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Platform settings created successfully", res)
}

// List lists platform settings.
// @Summary List platform settings
// @Description List platform settings (authenticated)
// @Tags Platform Settings
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.ListPlatformSettingsResponse} "Retrieved"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/platform-settings [get]
func (h *PlatformSettingsHandler) List(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/platform-settings", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListPlatformSettings(ctx, customerID)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			if be.Code == "MISSING_CUSTOMER_ID" {
				return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list platform settings", "PLATFORM_SETTINGS_LIST_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Platform settings retrieved", res)
}

func (h *PlatformSettingsHandler) createRequestContextWithTimeout(
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
	if customerID, ok := c.Locals("customer_id").(uint); ok && customerID != 0 {
		ctx = context.WithValue(ctx, utils.CustomerIDKey, customerID)
	}
	return ctx, cancel
}
