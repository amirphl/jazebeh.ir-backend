package handlers

import (
	"context"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
)

// PlatformBasePriceHandlerInterface defines user-facing platform base price operations.
type PlatformBasePriceHandlerInterface interface {
	List(c fiber.Ctx) error
}

// PlatformBasePriceHandler handles platform base price requests.
type PlatformBasePriceHandler struct {
	flow businessflow.PlatformBasePriceFlow
}

// NewPlatformBasePriceHandler creates a new platform base price handler.
func NewPlatformBasePriceHandler(flow businessflow.PlatformBasePriceFlow) PlatformBasePriceHandlerInterface {
	return &PlatformBasePriceHandler{flow: flow}
}

func (h *PlatformBasePriceHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PlatformBasePriceHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// List lists platform base prices.
// @Summary List platform base prices
// @Description List current platform base prices (authenticated)
// @Tags Platform Base Price
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.ListPlatformBasePricesResponse} "Retrieved"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/platform-base-prices [get]
func (h *PlatformBasePriceHandler) List(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/platform-base-prices", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListPlatformBasePrices(ctx)
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

func (h *PlatformBasePriceHandler) createRequestContextWithTimeout(
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
