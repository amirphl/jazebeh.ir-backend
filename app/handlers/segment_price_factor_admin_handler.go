package handlers

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// SegmentPriceFactorAdminHandlerInterface defines admin endpoints for segment price factors.
type SegmentPriceFactorAdminHandlerInterface interface {
	CreateSegmentPriceFactor(c fiber.Ctx) error
	ListSegmentPriceFactors(c fiber.Ctx) error
	ListLevel3Options(c fiber.Ctx) error
}

// SegmentPriceFactorAdminHandler implements admin endpoints for segment price factors.
type SegmentPriceFactorAdminHandler struct {
	flow      businessflow.SegmentPriceFactorAdminFlow
	validator *validator.Validate
}

func NewSegmentPriceFactorAdminHandler(flow businessflow.SegmentPriceFactorAdminFlow) SegmentPriceFactorAdminHandlerInterface {
	return &SegmentPriceFactorAdminHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

func (h *SegmentPriceFactorAdminHandler) ErrorResponse(c fiber.Ctx, status int, message, code string, details any) error {
	return c.Status(status).JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: code, Details: details}})
}

func (h *SegmentPriceFactorAdminHandler) SuccessResponse(c fiber.Ctx, status int, message string, data any) error {
	return c.Status(status).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// CreateSegmentPriceFactor creates or updates a segment price factor for a level3 value.
// @Summary Create/Update Segment Price Factor (Admin)
// @Description Create or update a price factor for a given level3; latest entry wins
// @Tags Admin Segment Price Factors
// @Accept json
// @Produce json
// @Param request body dto.AdminCreateSegmentPriceFactorRequest true "Segment price factor payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminCreateSegmentPriceFactorResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 500 {object} dto.APIResponse "Creation failed"
// @Router /api/v1/admin/segment-price-factors [post]
func (h *SegmentPriceFactorAdminHandler) CreateSegmentPriceFactor(c fiber.Ctx) error {
	var req dto.AdminCreateSegmentPriceFactorRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, e := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, e.Error())
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/segment-price-factors", 30*time.Second)
	defer cancel()
	res, err := h.flow.AdminCreateSegmentPriceFactor(ctx, &req)
	if err != nil {
		if businessflow.IsLevel3Required(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Level3 is required", "LEVEL3_REQUIRED", nil)
		}
		if businessflow.IsSegmentPriceFactorPlatformInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid platform", "INVALID_PLATFORM", nil)
		}
		if businessflow.IsPriceFactorInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Price factor must be greater than zero", "PRICE_FACTOR_INVALID", nil)
		}
		log.Println("Create segment price factor failed:", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Create segment price factor failed",
			"SEGMENT_PRICE_FACTOR_CREATE_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Segment price factor saved", res)
}

// ListSegmentPriceFactors returns the latest price factor per level3.
// @Summary List Segment Price Factors (Admin)
// @Description List the latest price factor per level3
// @Tags Admin Segment Price Factors
// @Produce json
// @Param platform query string false "Platform (sms|rubika|bale|splus), default sms"
// @Success 200 {object} dto.APIResponse{data=dto.AdminListSegmentPriceFactorsResponse}
// @Failure 500 {object} dto.APIResponse "List failed"
// @Router /api/v1/admin/segment-price-factors [get]
func (h *SegmentPriceFactorAdminHandler) ListSegmentPriceFactors(c fiber.Ctx) error {
	var platform *string
	platformRaw := c.Query("platform")
	if platformRaw != "" {
		platform = &platformRaw
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/segment-price-factors", 30*time.Second)
	defer cancel()
	res, err := h.flow.AdminListSegmentPriceFactors(ctx, platform)
	if err != nil {
		if businessflow.IsSegmentPriceFactorPlatformInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid platform", "INVALID_PLATFORM", nil)
		}
		log.Println("List segment price factors failed:", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"List segment price factors failed",
			"SEGMENT_PRICE_FACTOR_LIST_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Segment price factors retrieved", res)
}

// ListLevel3Options returns available level3 options from audience spec.
// @Summary List Level3 Options (Admin)
// @Description Retrieve distinct level3 values with available audience from audience spec
// @Tags Admin Segment Price Factors
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminListLevel3OptionsResponse}
// @Failure 500 {object} dto.APIResponse "List failed"
// @Router /api/v1/admin/segment-price-factors/level3-options [get]
func (h *SegmentPriceFactorAdminHandler) ListLevel3Options(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/segment-price-factors/level3-options", 30*time.Second)
	defer cancel()
	res, err := h.flow.AdminListLevel3Options(ctx)
	if err != nil {
		log.Println("List level3 options failed:", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"List level3 options failed",
			"SEGMENT_PRICE_FACTOR_LEVEL3_LIST_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Level3 options retrieved", res)
}

func (h *SegmentPriceFactorAdminHandler) createRequestContextWithTimeout(
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
