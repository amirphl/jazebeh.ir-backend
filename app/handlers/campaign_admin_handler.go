// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// CampaignAdminHandlerInterface defines the contract for campaign admin handlers
type CampaignAdminHandlerInterface interface {
	ListCampaigns(c fiber.Ctx) error
	GetCampaign(c fiber.Ctx) error
	ApproveCampaign(c fiber.Ctx) error
	RejectCampaign(c fiber.Ctx) error
	CancelCampaign(c fiber.Ctx) error
	RescheduleCampaign(c fiber.Ctx) error
	UpdatePagePrice(c fiber.Ctx) error
	GetPagePrices(c fiber.Ctx) error
}

// CampaignAdminHandler handles campaign-related HTTP requests
type CampaignAdminHandler struct {
	campaignFlow businessflow.AdminCampaignFlow
	validator    *validator.Validate
}

func NewCampaignAdminHandler(flow businessflow.AdminCampaignFlow) CampaignAdminHandlerInterface {
	h := &CampaignAdminHandler{
		campaignFlow: flow,
		validator:    validator.New(),
	}
	h.setupCustomValidations()
	return h
}

func (h *CampaignAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *CampaignAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// ListCampaigns returns campaigns filtered for admin
// @Summary Admin List Campaigns
// @Description Retrieve campaigns by campaign title, bundle title, customer name, status, start date, and end date
// @Tags Admin Campaigns
// @Produce json
// @Param campaign_title query string false "Filter by campaign title (contains)"
// @Param bundle_title query string false "Filter by bundle title (contains)"
// @Param customer_name query string false "Filter by customer name (contains, matches first/last name or company)"
// @Param status query string false "Filter by status (initiated|in-progress|waiting-for-approval|approved|rejected|expired)"
// @Param start_date query string false "Filter created_at >= start_date (RFC3339)"
// @Param end_date query string false "Filter created_at <= end_date (RFC3339)"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Page size" default(10) maximum(100)
// @Success 200 {object} dto.APIResponse{data=dto.AdminListCampaignsResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns [get]
func (h *CampaignAdminHandler) ListCampaigns(c fiber.Ctx) error {
	pageStr := c.Query("page", "1")
	limitStr := c.Query("limit", "10")
	campaignTitle := strings.TrimSpace(c.Query("campaign_title"))
	bundleTitle := strings.TrimSpace(c.Query("bundle_title"))
	customerName := strings.TrimSpace(c.Query("customer_name"))
	status := c.Query("status")
	startStr := c.Query("start_date")
	endStr := c.Query("end_date")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page <= 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page format", "INVALID_PAGE", nil)
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid limit format", "INVALID_LIMIT", nil)
	}

	filter := dto.AdminListCampaignsFilter{
		Page:  page,
		Limit: limit,
	}
	if campaignTitle != "" {
		filter.CampaignTitle = &campaignTitle
	}
	if bundleTitle != "" {
		filter.BundleTitle = &bundleTitle
	}
	if customerName != "" {
		filter.CustomerName = &customerName
	}
	if status != "" {
		filter.Status = &status
	}
	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			filter.StartDate = &t
		} else {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid start_date format", "INVALID_DATE", nil)
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			filter.EndDate = &t
		} else {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid end_date format", "INVALID_DATE", nil)
		}
	}

	if err := h.validator.Struct(filter); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns", 30*time.Second)
	defer cancel()
	resp, err := h.campaignFlow.ListCampaigns(ctx, filter)
	if err != nil {
		if businessflow.IsStartDateAfterEndDate(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "End date must be after start date", "INVALID_DATE_RANGE", nil)
		}
		log.Println("Admin list campaigns failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list campaigns", "ADMIN_LIST_CAMPAIGNS_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaigns retrieved successfully", fiber.Map{
		"message":    resp.Message,
		"items":      resp.Items,
		"pagination": resp.Pagination,
	})
}

// GetCampaign returns a single campaign by ID
// @Summary Admin Get Campaign
// @Tags Admin Campaigns
// @Produce json
// @Param id path string true "Campaign ID"
// @Success 200 {object} dto.APIResponse{data=dto.AdminGetCampaignResponse}
// @Failure 404 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/campaigns/{id} [get]
func (h *CampaignAdminHandler) GetCampaign(c fiber.Ctx) error {
	id := c.Params("id")
	idUint, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign ID", "INVALID_CAMPAIGN_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/"+id, 30*time.Second)
	defer cancel()
	resp, err := h.campaignFlow.GetCampaign(ctx, uint(idUint))
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		log.Println("Admin get campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to get campaign", "ADMIN_GET_CAMPAIGN_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign retrieved successfully", resp)
}

// ApproveCampaign approves a campaign
// @Summary Approve Campaign
// @Description Approve a campaign waiting for approval; moves reserved funds from frozen to locked
// @Tags Admin Campaigns
// @Accept json
// @Produce json
// @Param request body dto.AdminApproveCampaignRequest true "Approval payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminApproveCampaignResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 409 {object} dto.APIResponse "Invalid state"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/approve [post]
func (h *CampaignAdminHandler) ApproveCampaign(c fiber.Ctx) error {
	var req dto.AdminApproveCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/approve", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.ApproveCampaign(ctx, &req)
	if err != nil {
		if errors.Is(err, businessflow.ErrSmartTargetingTestPreviewRequired) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				businessflow.ErrSmartTargetingTestPreviewRequired.Error(),
				"SMART_TARGETING_TEST_PREVIEW_REQUIRED",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingTestNoSatisfiedTags) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				businessflow.ErrSmartTargetingTestNoSatisfiedTags.Error(),
				"SMART_TARGETING_TEST_NO_SATISFIED_TAGS",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingSampleSizeRequired) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				businessflow.ErrSmartTargetingSampleSizeRequired.Error(),
				"SMART_TARGETING_SAMPLE_SIZE_REQUIRED",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingSampleSizeInvalid) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				businessflow.ErrSmartTargetingSampleSizeInvalid.Error(),
				"SMART_TARGETING_SAMPLE_SIZE_INVALID",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingTestAudienceCountOverflow) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				businessflow.ErrSmartTargetingTestAudienceCountOverflow.Error(),
				"SMART_TARGETING_TEST_AUDIENCE_COUNT_OVERFLOW",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingCapacityPending) {
			var pending *businessflow.SmartTargetingCapacityPendingError
			if errors.As(err, &pending) && pending != nil {
				return h.ErrorResponse(
					c,
					fiber.StatusConflict,
					"Exact Smart Targeting capacity calculation is pending; please wait and retry approval",
					"SMART_TARGETING_CAPACITY_PENDING",
					pending.Response,
				)
			}
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Exact Smart Targeting capacity calculation is pending; please wait and retry approval",
				"SMART_TARGETING_CAPACITY_PENDING",
				nil,
			)
		}
		if errors.Is(err, businessflow.ErrSmartTargetingExactCapacityRequired) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"A current exact Smart Targeting capacity calculation is required before approval",
				"SMART_TARGETING_EXACT_CAPACITY_REQUIRED",
				nil,
			)
		}
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignNotWaitingForApproval(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Invalid campaign state for approval", "INVALID_STATE", nil)
		}
		if businessflow.IsScheduleTimeTooSoon(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Schedule time is too soon", "SCHEDULE_TIME_TOO_SOON", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
		}
		if businessflow.IsFreezeTransactionNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Freeze transaction not found", "FREEZE_TRANSACTION_NOT_FOUND", nil)
		}
		if businessflow.IsMultipleFreezeTransactionsFound(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Multiple freeze transactions found", "MULTIPLE_FREEZE_TRANSACTIONS_FOUND", nil)
		}
		if businessflow.IsInsufficientFunds(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Insufficient funds", "INSUFFICIENT_FUNDS", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		log.Println("Admin approve campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to approve campaign", "ADMIN_APPROVE_CAMPAIGN_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign approved successfully", res)
}

// RejectCampaign rejects a campaign and refunds frozen funds
// @Summary Reject Campaign
// @Description Reject a campaign waiting for approval; refunds reserved funds to free balance
// @Tags Admin Campaigns
// @Accept json
// @Produce json
// @Param request body dto.AdminRejectCampaignRequest true "Rejection payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminRejectCampaignResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 409 {object} dto.APIResponse "Invalid state"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/reject [post]
func (h *CampaignAdminHandler) RejectCampaign(c fiber.Ctx) error {
	var req dto.AdminRejectCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/reject", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.RejectCampaign(ctx, &req)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignNotWaitingForApproval(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Invalid campaign state for approval", "INVALID_STATE", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
		}
		if businessflow.IsFreezeTransactionNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Freeze transaction not found", "FREEZE_TRANSACTION_NOT_FOUND", nil)
		}
		if businessflow.IsMultipleFreezeTransactionsFound(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Multiple freeze transactions found", "MULTIPLE_FREEZE_TRANSACTIONS_FOUND", nil)
		}
		if businessflow.IsInsufficientFunds(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Insufficient funds", "INSUFFICIENT_FUNDS", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		log.Println("Admin reject campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to reject campaign", "ADMIN_REJECT_CAMPAIGN_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign rejected successfully", res)
}

// RescheduleCampaign updates the scheduled time for a campaign (admin-only).
// @Summary Reschedule Campaign
// @Description Admin reschedules an eligible campaign; schedule_at must be UTC and its Tehran-local time must be between 08:00 and 21:00. A
// waiting-for-approval campaign whose deadline was missed may still be rescheduled.
// @Tags Admin Campaigns
// @Accept json
// @Produce json
// @Param request body dto.AdminRescheduleCampaignRequest true "Reschedule payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminRescheduleCampaignResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 409 {object} dto.APIResponse "Invalid state"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/reschedule [post]
func (h *CampaignAdminHandler) RescheduleCampaign(c fiber.Ctx) error {
	var req dto.AdminRescheduleCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/reschedule", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.RescheduleCampaign(ctx, &req)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignRescheduleNotAllowed(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Campaign cannot be rescheduled in its current status", "INVALID_STATE", nil)
		}
		if businessflow.IsScheduleTimeNotPresent(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Schedule time is required", "SCHEDULE_TIME_REQUIRED", nil)
		}
		if businessflow.IsScheduleTimeTooCloseToCurrent(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Current schedule is too close; reschedule must happen at least 5 minutes before scheduled time",
				"SCHEDULE_TIME_TOO_CLOSE_TO_CURRENT",
				nil,
			)
		}
		if businessflow.IsScheduleTimeTooSoon(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Schedule time must be at least 5 minutes in the future",
				"SCHEDULE_TIME_TOO_SOON",
				nil,
			)
		}
		if businessflow.IsScheduleTimeMustBeUTC(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Schedule time must be in UTC (offset +00:00)",
				"SCHEDULE_TIME_MUST_BE_UTC",
				nil,
			)
		}
		if businessflow.IsScheduleTimeOutsideWindow(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Converted Tehran schedule time must be between 08:00 and 21:00",
				"SCHEDULE_TIME_OUTSIDE_WINDOW",
				nil,
			)
		}
		var be *businessflow.BusinessError
		if errors.As(err, &be) && be.Code != "" {
			return h.ErrorResponse(c, fiber.StatusBadRequest, be.Message, be.Code, nil)
		}
		log.Println("Admin reschedule campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to reschedule campaign", "ADMIN_RESCHEDULE_CAMPAIGN_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaign rescheduled successfully", res)
}

// CancelCampaign cancels an approved campaign or a waiting-for-approval campaign that missed its deadline.
// @Summary Cancel Campaign
// @Description Cancel an approved campaign by admin and refund consumed budget to customer. Approved campaigns still require at least 2
// minutes before schedule_at; a waiting-for-approval campaign that already missed schedule_at may also be cancelled.
// @Tags Admin Campaigns
// @Accept json
// @Produce json
// @Param request body dto.AdminCancelCampaignRequest true "Cancellation payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminCancelCampaignResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 409 {object} dto.APIResponse "Invalid state"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/cancel [post]
func (h *CampaignAdminHandler) CancelCampaign(c fiber.Ctx) error {
	var req dto.AdminCancelCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/cancel", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.CancelCampaign(ctx, &req)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignNotApproved(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Invalid campaign state for cancel", "INVALID_STATE", nil)
		}
		if businessflow.IsScheduleTimeTooCloseToCancel(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Current schedule is too close; cancel must happen at least 2 minutes before scheduled time",
				"SCHEDULE_TIME_TOO_CLOSE_TO_CANCEL",
				nil,
			)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignDebitTransactionNotFound(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Campaign debit transaction not found",
				"CAMPAIGN_DEBIT_TRANSACTION_NOT_FOUND",
				nil,
			)
		}
		if businessflow.IsMultipleCampaignDebitTransactionsFound(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Multiple campaign debit transactions found",
				"MULTIPLE_CAMPAIGN_DEBIT_TRANSACTIONS_FOUND",
				nil,
			)
		}
		if businessflow.IsInsufficientFunds(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Insufficient funds", "INSUFFICIENT_FUNDS", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		log.Println("Admin cancel campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to cancel campaign", "ADMIN_CANCEL_CAMPAIGN_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign cancelled successfully", res)
}

// UpdatePagePrice inserts a new page price record for a platform.
// @Summary Update Page Price
// @Description Insert a new page price version for a platform (insert-only)
// @Tags Admin Campaigns
// @Accept json
// @Produce json
// @Param request body dto.AdminUpdatePagePriceRequest true "Page price payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminUpdatePagePriceResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/page-prices [put]
func (h *CampaignAdminHandler) UpdatePagePrice(c fiber.Ctx) error {
	var req dto.AdminUpdatePagePriceRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/page-prices", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.UpdatePagePrice(ctx, &req)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_REQUEST", "PAGE_PRICE_PLATFORM_REQUIRED", "PAGE_PRICE_PLATFORM_INVALID", "PAGE_PRICE_INVALID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request", be.Code, nil)
			}
		}
		log.Println("Admin update page price failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update page price", "PAGE_PRICE_INSERT_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Page price updated successfully", res)
}

// GetPagePrices returns latest page price per platform.
// @Summary Get Page Prices
// @Description Return latest page price for all platforms
// @Tags Admin Campaigns
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminGetPagePricesResponse}
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/campaigns/page-prices [get]
func (h *CampaignAdminHandler) GetPagePrices(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/campaigns/page-prices", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.GetPagePrices(ctx)
	if err != nil {
		log.Println("Admin get page prices failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to get page prices", "PAGE_PRICE_LIST_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Page prices retrieved successfully", res)
}

// createRequestContextWithTimeout creates a context with custom timeout and request-scoped values
func (h *CampaignAdminHandler) createRequestContextWithTimeout(
	c fiber.Ctx,
	endpoint string,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	// Create context with custom timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Add request-scoped values for observability
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel) // Store cancel function for cleanup
	if adminID, ok := middleware.GetAdminIDFromContext(c); ok {
		ctx = context.WithValue(ctx, utils.AdminIDKey, adminID)
	}

	return ctx, cancel
}

// setupCustomValidations sets up custom validation rules
func (h *CampaignAdminHandler) setupCustomValidations() {
	// Add custom validation rules if needed
	// Example: h.validator.RegisterValidation("custom_rule", customValidationFunc)
}
