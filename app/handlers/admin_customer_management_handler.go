package handlers

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

type AdminCustomerManagementHandlerInterface interface {
	ListCustomers(c fiber.Ctx) error
	GetCustomersShares(c fiber.Ctx) error
	GetCustomerWithCampaigns(c fiber.Ctx) error
	SetCustomerActiveStatus(c fiber.Ctx) error
	GetCustomerDiscountsHistory(c fiber.Ctx) error
}

type AdminCustomerManagementHandler struct {
	flow      businessflow.AdminCustomerManagementFlow
	validator *validator.Validate
}

func NewAdminCustomerManagementHandler(flow businessflow.AdminCustomerManagementFlow) AdminCustomerManagementHandlerInterface {
	return &AdminCustomerManagementHandler{flow: flow, validator: validator.New()}
}

func (h *AdminCustomerManagementHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: errorCode, Details: details}})
}

func (h *AdminCustomerManagementHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// ListCustomers returns all customers except tax and system users
// @Summary Admin List Customers
// @Tags Admin Customer Management
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminListCustomersResponse}
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/customer-management [get]
func (h *AdminCustomerManagementHandler) ListCustomers(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/customer-management", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListCustomers(ctx)
	if err != nil {
		log.Println("Admin list customers failed", err)
		return h.respondAdminCustomerManagementError(c, err, "Failed to retrieve customers list", "GET_ADMIN_CUSTOMERS_LIST_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Customers retrieved successfully", res)
}

// GetCustomersShares returns aggregated shares per customer
// @Summary Admin Customers Shares Report
// @Tags Admin Customer Management
// @Produce json
// @Param start_date query string true "Report period start (RFC3339; maximum period is 31 days)"
// @Param end_date query string true "Report period end (RFC3339)"
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Rows per page (default 50, maximum 100)"
// @Success 200 {object} dto.APIResponse{data=dto.AdminCustomersSharesResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/customer-management/shares [get]
func (h *AdminCustomerManagementHandler) GetCustomersShares(c fiber.Ctx) error {
	var req dto.AdminCustomersSharesRequest
	if v := c.Query("start_date"); v != "" {
		req.StartDate = &v
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid start_date format", "VALIDATION_ERROR", nil)
		}
	}
	if v := c.Query("end_date"); v != "" {
		req.EndDate = &v
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid end_date format", "VALIDATION_ERROR", nil)
		}
	}
	if req.StartDate == nil || req.EndDate == nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "start_date and end_date are required", "VALIDATION_ERROR", nil)
	}
	if v := c.Query("page"); v != "" {
		page, err := strconv.Atoi(v)
		if err != nil || page < 1 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "page must be at least 1", "VALIDATION_ERROR", nil)
		}
		req.Page = page
	}
	if v := c.Query("page_size"); v != "" {
		pageSize, err := strconv.Atoi(v)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "page_size must be between 1 and 100", "VALIDATION_ERROR", nil)
		}
		req.PageSize = pageSize
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/customer-management/shares", 30*time.Second)
	defer cancel()
	res, err := h.flow.GetCustomersShares(ctx, &req)
	if err != nil {
		log.Println("Admin get customers shares failed", err)
		return h.respondAdminCustomerManagementError(c, err, "Failed to retrieve customers shares", "GET_ADMIN_CUSTOMERS_SHARES_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Customers shares retrieved successfully", res)
}

// GetCustomerWithCampaigns returns full customer info with campaigns
// @Summary Admin Get Customer With Campaigns
// @Tags Admin Customer Management
// @Produce json
// @Param customer_id path int true "Customer ID"
// @Success 200 {object} dto.APIResponse{data=dto.AdminCustomerWithCampaignsResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/customer-management/{customer_id} [get]
func (h *AdminCustomerManagementHandler) GetCustomerWithCampaigns(c fiber.Ctx) error {
	cidStr := c.Params("customer_id")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil || cid == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid customer_id", "VALIDATION_ERROR", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/customer-management/"+cidStr, 30*time.Second)
	defer cancel()
	res, err := h.flow.GetCustomerWithCampaigns(ctx, uint(cid))
	if err != nil {
		log.Println("Admin get customer with campaigns failed", err)
		return h.respondAdminCustomerManagementError(c, err, "Failed to retrieve customer details", "GET_ADMIN_CUSTOMER_WITH_CAMPAIGNS_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Customer details retrieved successfully", res)
}

// SetCustomerActiveStatus toggles customer's active status
// @Summary Admin Set Customer Active Status
// @Tags Admin Customer Management
// @Accept json
// @Produce json
// @Param body body dto.AdminSetCustomerActiveStatusRequest true "Customer active status"
// @Success 200 {object} dto.APIResponse{data=dto.AdminSetCustomerActiveStatusResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 403 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/customer-management/active-status [post]
func (h *AdminCustomerManagementHandler) SetCustomerActiveStatus(c fiber.Ctx) error {
	var req dto.AdminSetCustomerActiveStatusRequest
	if err := c.Bind().Body(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "VALIDATION_ERROR", nil)
	}
	if err := h.validator.Struct(req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation error", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/customer-management/active-status", 30*time.Second)
	defer cancel()
	res, err := h.flow.SetCustomerActiveStatus(ctx, &req)
	if err != nil {
		log.Println("Admin set customer active status failed", err)
		return h.respondAdminCustomerManagementError(c, err, "Failed to set customer active status", "SET_CUSTOMER_ACTIVE_STATUS_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Customer active status updated", res)
}

// GetCustomerDiscountsHistory returns discount usage per customer
// @Summary Admin List Customer Discounts History
// @Tags Admin Customer Management
// @Produce json
// @Param customer_id path int true "Customer ID"
// @Success 200 {object} dto.APIResponse{data=dto.AdminCustomerDiscountHistoryResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/customer-management/{customer_id}/discounts [get]
func (h *AdminCustomerManagementHandler) GetCustomerDiscountsHistory(c fiber.Ctx) error {
	cidStr := c.Params("customer_id")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil || cid == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid customer_id", "VALIDATION_ERROR", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/customer-management/"+cidStr+"/discounts", 30*time.Second)
	defer cancel()
	res, err := h.flow.GetCustomerDiscountsHistory(ctx, uint(cid))
	if err != nil {
		log.Println("Admin list customer discounts history failed", err)
		return h.respondAdminCustomerManagementError(c, err, "Failed to list customer discounts history", "LIST_CUSTOMER_DISCOUNTS_HISTORY_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Customer discounts history retrieved successfully", res)
}

func (h *AdminCustomerManagementHandler) respondAdminCustomerManagementError(
	c fiber.Ctx,
	err error,
	defaultMessage string,
	defaultCode string,
) error {
	if businessflow.IsCustomerNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
	}
	if businessflow.IsAccountInactive(err) {
		return h.ErrorResponse(c, fiber.StatusForbidden, "System and Tax users cannot be deactivated", "FORBIDDEN_OPERATION", nil)
	}

	var be *businessflow.BusinessError
	if errors.As(err, &be) {
		switch be.Code {
		case "VALIDATION_ERROR":
			return h.ErrorResponse(c, fiber.StatusBadRequest, be.Message, be.Code, nil)
		case "CUSTOMER_NOT_FOUND":
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", be.Code, nil)
		case "FORBIDDEN_OPERATION":
			return h.ErrorResponse(c, fiber.StatusForbidden, be.Message, be.Code, nil)
		case "GET_ADMIN_CUSTOMERS_SHARES_FAILED",
			"GET_ADMIN_CUSTOMERS_LIST_FAILED",
			"GET_ADMIN_CUSTOMER_FAILED",
			"GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED",
			"GET_ADMIN_CUSTOMER_DISCOUNTS_HISTORY_FAILED",
			"SET_CUSTOMER_ACTIVE_STATUS_FAILED":
			return h.ErrorResponse(c, fiber.StatusInternalServerError, be.Message, be.Code, nil)
		}
	}

	return h.ErrorResponse(c, fiber.StatusInternalServerError, defaultMessage, defaultCode, nil)
}

func (h *AdminCustomerManagementHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
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
