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

type PlatformBasePriceAdminHandlerInterface interface {
	List(c fiber.Ctx) error
	Update(c fiber.Ctx) error
}

type PlatformBasePriceAdminHandler struct {
	flow      businessflow.PlatformBasePriceAdminFlow
	validator *validator.Validate
}

func NewPlatformBasePriceAdminHandler(flow businessflow.PlatformBasePriceAdminFlow) PlatformBasePriceAdminHandlerInterface {
	return &PlatformBasePriceAdminHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

func (h *PlatformBasePriceAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PlatformBasePriceAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// List lists platform base prices by admin.
// @Summary Admin list platform base prices
// @Description List current platform base prices
// @Tags Admin Platform Base Price
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminListPlatformBasePricesResponse} "Retrieved"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/platform-base-prices [get]
func (h *PlatformBasePriceAdminHandler) List(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/platform-base-prices", 30*time.Second)
	defer cancel()
	res, err := h.flow.AdminListPlatformBasePrices(ctx)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			if be.Code == "PLATFORM_BASE_PRICE_LIST_FAILED" {
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list platform base prices", be.Code, nil)
			}
		}
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to list platform base prices",
			"PLATFORM_BASE_PRICE_LIST_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Platform base prices retrieved successfully", res)
}

// Update updates platform base price by admin.
// @Summary Admin update platform base price
// @Description Update base price for a platform
// @Tags Admin Platform Base Price
// @Accept json
// @Produce json
// @Param request body dto.AdminUpdatePlatformBasePriceRequest true "Platform base price payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminUpdatePlatformBasePriceResponse} "Updated"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Platform base price not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/platform-base-prices [put]
func (h *PlatformBasePriceAdminHandler) Update(c fiber.Ctx) error {
	var req dto.AdminUpdatePlatformBasePriceRequest
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

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/platform-base-prices", 30*time.Second)
	defer cancel()
	res, err := h.flow.AdminUpdatePlatformBasePrice(ctx, &req)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_REQUEST",
				"PLATFORM_BASE_PRICE_PLATFORM_REQUIRED",
				"PLATFORM_BASE_PRICE_PLATFORM_INVALID",
				"PLATFORM_BASE_PRICE_INVALID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, nil)
			case "PLATFORM_BASE_PRICE_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Platform base price not found", be.Code, nil)
			case "PLATFORM_BASE_PRICE_UPDATE_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update platform base price", be.Code, nil)
			}
		}
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to update platform base price",
			"PLATFORM_BASE_PRICE_UPDATE_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Platform base price updated successfully", res)
}

func (h *PlatformBasePriceAdminHandler) createRequestContextWithTimeout(
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
