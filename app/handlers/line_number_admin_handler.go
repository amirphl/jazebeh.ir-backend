// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// LineNumberAdminHandlerInterface defines handler methods for admin line number operations
type LineNumberAdminHandlerInterface interface {
	CreateLineNumber(c fiber.Ctx) error
	ListLineNumbers(c fiber.Ctx) error
	UpdateLineNumbersBatch(c fiber.Ctx) error
	UpdateLineNumberPriceFactor(c fiber.Ctx) error
	GetLineNumbersReport(c fiber.Ctx) error
}

// LineNumberAdminHandler implements admin line number endpoints
type LineNumberAdminHandler struct {
	flow      businessflow.AdminLineNumberFlow
	validator *validator.Validate
}

func NewLineNumberAdminHandler(flow businessflow.AdminLineNumberFlow) LineNumberAdminHandlerInterface {
	return &LineNumberAdminHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

func (h *LineNumberAdminHandler) ErrorResponse(c fiber.Ctx, status int, message, code string, details any) error {
	return c.Status(status).JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: code, Details: details}})
}

func (h *LineNumberAdminHandler) SuccessResponse(c fiber.Ctx, status int, message string, data any) error {
	return c.Status(status).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// CreateLineNumber creates a new line number or updates an existing one (admin only)
// @Summary Create Line Number (Admin)
// @Description Create a line number with name (optional), unique value, price factor, priority (optional), and is_active (optional). If the line number already exists, its mutable fields are updated.
// @Tags Admin Line Numbers
// @Accept json
// @Produce json
// @Param request body dto.AdminCreateLineNumberRequest true "Create line number payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminLineNumberDTO}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 500 {object} dto.APIResponse "Create or update failed"
// @Router /api/v1/admin/line-numbers/ [post]
func (h *LineNumberAdminHandler) CreateLineNumber(c fiber.Ctx) error {
	var req dto.AdminCreateLineNumberRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/line-numbers/", 30*time.Second)
	defer cancel()
	res, err := h.flow.Create(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsLineNumberValueRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is required", "LINE_NUMBER_VALUE_REQUIRED", nil)
		}
		if businessflow.IsPriceFactorInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Price factor must be greater than zero", "PRICE_FACTOR_INVALID", nil)
		}
		log.Println("Create line number failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Create or update line number failed", "LINE_NUMBER_CREATE_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Line number created or updated", res)
}

// ListLineNumbers returns all line numbers (admin)
// @Summary List Line Numbers (Admin)
// @Description Retrieve all line numbers
// @Tags Admin Line Numbers
// @Produce json
// @Success 200 {object} dto.APIResponse{data=[]dto.AdminLineNumberDTO}
// @Failure 500 {object} dto.APIResponse "List failed"
// @Router /api/v1/admin/line-numbers/ [get]
func (h *LineNumberAdminHandler) ListLineNumbers(c fiber.Ctx) error {
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/line-numbers/", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListAll(ctx, metadata)
	if err != nil {
		log.Println("List line numbers failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "List line numbers failed", "LINE_NUMBER_LIST_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Line numbers retrieved", res)
}

// UpdateLineNumbersBatch updates a list of line numbers in batch (admin)
// @Summary Batch Update Line Numbers (Admin)
// @Description Update multiple line numbers; all IDs must exist
// @Tags Admin Line Numbers
// @Accept json
// @Produce json
// @Param request body dto.AdminUpdateLineNumbersRequest true "Batch update payload"
// @Success 200 {object} dto.APIResponse{data=object{updated=bool}}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 500 {object} dto.APIResponse "Update failed"
// @Router /api/v1/admin/line-numbers/ [put]
func (h *LineNumberAdminHandler) UpdateLineNumbersBatch(c fiber.Ctx) error {
	var req dto.AdminUpdateLineNumbersRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/line-numbers/", 30*time.Second)
	defer cancel()
	if err := h.flow.UpdateBatch(ctx, &req, metadata); err != nil {
		if businessflow.IsLineNumberValueRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is required", "LINE_NUMBER_VALUE_REQUIRED", nil)
		}
		if businessflow.IsPriceFactorInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Price factor must be greater than zero", "PRICE_FACTOR_INVALID", nil)
		}
		if businessflow.IsLineNumberAlreadyExists(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number already exists", "LINE_NUMBER_ALREADY_EXISTS", nil)
		}
		log.Println("Batch update line numbers failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Batch update failed", "LINE_NUMBER_BATCH_UPDATE_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Line numbers updated", fiber.Map{"updated": true})
}

// UpdateLineNumberPriceFactor updates the price factor of one line number (admin).
// @Summary Update Line Number Price Factor (Admin)
// @Description Update the price factor for a line number identified by its value.
// @Tags Admin Line Numbers
// @Accept json
// @Produce json
// @Param request body dto.AdminUpdateLineNumberPriceFactorRequest true "Price factor update payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminLineNumberDTO}
// @Failure 400 {object} dto.APIResponse "Invalid request or validation error"
// @Failure 404 {object} dto.APIResponse "Line number not found"
// @Failure 500 {object} dto.APIResponse "Price factor update failed"
// @Router /api/v1/admin/line-numbers/price-factor [put]
func (h *LineNumberAdminHandler) UpdateLineNumberPriceFactor(c fiber.Ctx) error {
	var req dto.AdminUpdateLineNumberPriceFactorRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/line-numbers/price-factor", 30*time.Second)
	defer cancel()
	res, err := h.flow.UpdatePriceFactor(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsLineNumberValueRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is required", "LINE_NUMBER_VALUE_REQUIRED", nil)
		}
		if businessflow.IsPriceFactorInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Price factor must be greater than zero", "PRICE_FACTOR_INVALID", nil)
		}
		if businessflow.IsLineNumberNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Line number not found", "LINE_NUMBER_NOT_FOUND", nil)
		}
		log.Println("Update line number price factor failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Update line number price factor failed", "LINE_NUMBER_PRICE_FACTOR_UPDATE_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Line number price factor updated", res)
}

// GetLineNumbersReport returns aggregate report for line numbers (admin)
// @Summary Line Numbers Report (Admin)
// @Description Retrieve report per line number (totals)
// @Tags Admin Line Numbers
// @Produce json
// @Success 200 {object} dto.APIResponse{data=[]dto.AdminLineNumberReportItem}
// @Failure 500 {object} dto.APIResponse "Report generation failed"
// @Router /api/v1/admin/line-numbers/report [get]
func (h *LineNumberAdminHandler) GetLineNumbersReport(c fiber.Ctx) error {
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/line-numbers/report", 30*time.Second)
	defer cancel()
	items, err := h.flow.GetReport(ctx, metadata)
	if err != nil {
		log.Println("Line numbers report failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Report generation failed", "LINE_NUMBER_REPORT_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Line numbers report", items)
}

func (h *LineNumberAdminHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
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
