package handlers

import (
	"context"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
)

type ProfileHandlerInterface interface {
	GetProfile(c fiber.Ctx) error
}

type ProfileHandler struct {
	flow businessflow.ProfileFlow
}

func NewProfileHandler(flow businessflow.ProfileFlow) *ProfileHandler {
	return &ProfileHandler{flow: flow}
}

func (h *ProfileHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *ProfileHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// GetProfile returns the authenticated customer's profile and parent agency
// @Summary Get profile
// @Description Retrieve the authenticated customer's profile and parent agency details (if exists)
// @Tags Profile
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.GetProfileResponse} "Profile retrieved successfully"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/profile [get]
func (h *ProfileHandler) GetProfile(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/profile", 30*time.Second)
	defer cancel()
	res, err := h.flow.GetProfile(ctx, customerID)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to get profile", "GET_PROFILE_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, res.Message, fiber.Map{
		"customer":      res.Customer,
		"parent_agency": res.ParentAgency,
	})
}

func (h *ProfileHandler) createRequestContextWithTimeout(
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
	return ctx, cancel
}
