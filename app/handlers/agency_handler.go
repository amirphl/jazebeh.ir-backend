package handlers

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

type AgencyHandlerInterface interface {
	CreateAgencyDiscount(c fiber.Ctx) error
	GetAgencyCustomerReport(c fiber.Ctx) error
	ListAgencyActiveDiscounts(c fiber.Ctx) error
	ListAgencyCustomerDiscounts(c fiber.Ctx) error
	ListAgencyCustomers(c fiber.Ctx) error
}

type AgencyHandler struct {
	flow      businessflow.AgencyFlow
	validator *validator.Validate
}

func NewAgencyHandler(flow businessflow.AgencyFlow) *AgencyHandler {
	return &AgencyHandler{flow: flow, validator: validator.New()}
}

func (h *AgencyHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error:   dto.ErrorDetail{Code: errorCode, Details: details},
	})
}

func (h *AgencyHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// CreateAgencyDiscount creates a new discount for a customer under the agency
// @Summary Create Agency Discount
// @Tags Reports
// @Accept json
// @Produce json
// @Param request body dto.CreateAgencyDiscountRequest true "Create discount payload"
// @Success 201 {object} dto.APIResponse{data=dto.CreateAgencyDiscountResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/reports/agency/discounts [post]
func (h *AgencyHandler) CreateAgencyDiscount(c fiber.Ctx) error {
	agencyID, ok := c.Locals("customer_id").(uint)
	if !ok || agencyID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	var req dto.CreateAgencyDiscountRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	// override agency ID from auth context
	req.AgencyID = agencyID
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/reports/agency/discounts", 30*time.Second)
	defer cancel()
	res, err := h.flow.CreateAgencyDiscount(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency is inactive", "AGENCY_INACTIVE", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsDiscountRateOutOfRange(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Rate must be between 0 and 0.5", "DISCOUNT_RATE_OUT_OF_RANGE", nil)
		}
		if businessflow.IsAgencyCannotCreateDiscountForItself(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Agency cannot create discount for itself",
				"AGENCY_CANNOT_CREATE_DISCOUNT_FOR_ITSELF",
				nil,
			)
		}
		if businessflow.IsCustomerNotUnderAgency(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Customer is not under any agency", "CUSTOMER_NOT_UNDER_AGENCY", nil)
		}

		log.Println("Create agency discount failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create discount", "CREATE_DISCOUNT_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Discount created successfully", res)
}

// GetAgencyCustomerReport handles the retrieval of agency customer aggregated report
// @Summary Get Agency Customer Report
// @Description Retrieve per-customer aggregated stats of campaigns for an agency with pagination and filters
// @Tags Reports
// @Accept json
// @Produce json
// @Param orderby query string false "Sorting (name_asc|name_desc|sent_desc|share_desc)"
// @Param start_date query string false "Start date (RFC3339)"
// @Param end_date query string false "End date (RFC3339)"
// @Param name query string false "Filter by customer name (first+last)"
// @Success 200 {object} dto.APIResponse{data=dto.AgencyCustomerReportResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/reports/agency/customers [get]
func (h *AgencyHandler) GetAgencyCustomerReport(c fiber.Ctx) error {
	// Authorization: require authenticated customer id and that customer is agency
	agencyID, ok := c.Locals("customer_id").(uint)
	if !ok || agencyID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	orderby := c.Query("orderby")
	var startDateStr, endDateStr, name *string
	if v := c.Query("start_date"); v != "" {
		startDateStr = &v
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid start_date format", "VALIDATION_ERROR", nil)
		}
	}
	if v := c.Query("end_date"); v != "" {
		endDateStr = &v
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid end_date format", "VALIDATION_ERROR", nil)
		}
	}
	if v := c.Query("name"); v != "" {
		name = &v
	}
	req := &dto.AgencyCustomerReportRequest{
		AgencyID: uint(agencyID),
		OrderBy:  orderby,
		Filter: &dto.AgencyCustomerReportFilter{
			StartDate: startDateStr,
			EndDate:   endDateStr,
			Name:      name,
		},
	}
	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/reports/agency/customers", 30*time.Second)
	defer cancel()
	res, err := h.flow.GetAgencyCustomerReport(ctx, req, metadata)
	if err != nil {
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency is inactive", "AGENCY_INACTIVE", nil)
		}

		log.Println("Agency customer report retrieval failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to retrieve agency customer report",
			"AGENCY_CUSTOMER_REPORT_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Agency customer report retrieved successfully", res)
}

// ListAgencyActiveDiscounts lists active, non-expired discounts with customer info
// @Summary List Active Discounts of Agency
// @Tags Reports
// @Produce json
// @Param name query string false "Filter by user name (first+last)"
// @Success 200 {object} dto.APIResponse{data=dto.ListAgencyActiveDiscountsResponse}
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/reports/agency/discounts/active [get]
func (h *AgencyHandler) ListAgencyActiveDiscounts(c fiber.Ctx) error {
	agencyID, ok := c.Locals("customer_id").(uint)
	if !ok || agencyID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	var name *string
	if v := c.Query("name"); v != "" {
		name = &v
	}
	req := &dto.ListAgencyActiveDiscountsRequest{
		AgencyID: uint(agencyID),
		Filter: &dto.ListAgencyActiveDiscountsFilter{
			Name: name,
		},
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/reports/agency/discounts/active", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListAgencyActiveDiscounts(ctx, req, metadata)
	if err != nil {
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency is inactive", "AGENCY_INACTIVE", nil)
		}

		log.Println("List active discounts failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list active discounts", "LIST_ACTIVE_DISCOUNTS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Active discounts retrieved successfully", res)
}

// ListAgencyCustomerDiscounts lists discounts of a specific customer and aggregates
// @Summary List Customer Discounts Under Agency
// @Tags Reports
// @Produce json
// @Param customer_id path int true "Customer ID"
// @Success 200 {object} dto.APIResponse{data=dto.ListAgencyCustomerDiscountsResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/reports/agency/customers/{customer_id}/discounts [get]
func (h *AgencyHandler) ListAgencyCustomerDiscounts(c fiber.Ctx) error {
	agencyID, ok := c.Locals("customer_id").(uint)
	if !ok || agencyID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	cidStr := c.Params("customer_id")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil || cid == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid customer_id", "VALIDATION_ERROR", nil)
	}
	req := &dto.ListAgencyCustomerDiscountsRequest{AgencyID: agencyID, CustomerID: uint(cid)}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/reports/agency/customers/"+cidStr+"/discounts", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListAgencyCustomerDiscounts(ctx, req, metadata)
	if err != nil {
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency is inactive", "AGENCY_INACTIVE", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsAgencyCannotListDiscountsForItself(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Agency cannot list discounts for itself",
				"AGENCY_CANNOT_LIST_DISCOUNTS_FOR_ITSELF",
				nil,
			)
		}
		if businessflow.IsCustomerNotUnderAgency(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Customer is not under any agency", "CUSTOMER_NOT_UNDER_AGENCY", nil)
		}

		log.Println("List customer discounts failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to list customer discounts",
			"LIST_CUSTOMER_DISCOUNTS_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Customer discounts retrieved successfully", res)
}

// ListAgencyCustomers returns active customers under the authenticated agency
// @Summary List Agency Customers
// @Tags Reports
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.ListAgencyCustomersResponse}
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/reports/agency/customers/list [get]
func (h *AgencyHandler) ListAgencyCustomers(c fiber.Ctx) error {
	agencyID, ok := c.Locals("customer_id").(uint)
	if !ok || agencyID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	req := &dto.ListAgencyCustomersRequest{AgencyID: agencyID}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/reports/agency/customers/list", 30*time.Second)
	defer cancel()
	res, err := h.flow.ListAgencyCustomers(ctx, req, metadata)
	if err != nil {
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency is inactive", "AGENCY_INACTIVE", nil)
		}

		log.Println("List agency customers failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list agency customers", "LIST_AGENCY_CUSTOMERS_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Agency customers retrieved successfully", res)
}

func (h *AgencyHandler) createRequestContextWithTimeout(
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
