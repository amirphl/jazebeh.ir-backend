// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// PaymentAdminHandlerInterface defines the contract for admin payment handlers.
type PaymentAdminHandlerInterface interface {
	ChargeWallet(c fiber.Ctx) error
	PreviewWalletChargeImpact(c fiber.Ctx) error
	ListDepositReceipts(c fiber.Ctx) error
	ListTransactions(c fiber.Ctx) error
	GetDepositReceiptFile(c fiber.Ctx) error
	UpdateDepositReceiptStatus(c fiber.Ctx) error
	AddInvoiceToTransaction(c fiber.Ctx) error
}

// PaymentAdminHandler handles admin payment HTTP requests.
type PaymentAdminHandler struct {
	paymentAdminFlow businessflow.PaymentAdminFlow
	validator        *validator.Validate
}

// NewPaymentAdminHandler creates a new admin payment handler.
func NewPaymentAdminHandler(paymentAdminFlow businessflow.PaymentAdminFlow) PaymentAdminHandlerInterface {
	return &PaymentAdminHandler{
		paymentAdminFlow: paymentAdminFlow,
		validator:        validator.New(),
	}
}

func (h *PaymentAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PaymentAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// ChargeWallet directly charges a customer's wallet without payment gateway redirect.
// @Summary Charge Wallet By Admin
// @Description Admin endpoint to directly charge a customer wallet (manual card-to-card/offline payment settlement)
// @Tags Payments Admin
// @Accept json
// @Produce json
// @Param request body dto.AdminChargeWalletRequest true "Admin wallet charge payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminChargeWalletResponse} "Wallet charged successfully by admin"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized admin"
// @Failure 404 {object} dto.APIResponse "Customer or wallet not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/charge-wallet [post]
func (h *PaymentAdminHandler) ChargeWallet(c fiber.Ctx) error {
	var req dto.AdminChargeWalletRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	req.AdminNote = strings.TrimSpace(req.AdminNote)
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = strings.TrimSpace(c.Get("Idempotency-Key"))
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = strings.TrimSpace(c.Get("X-Idempotency-Key"))
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	adminID, ok := c.Locals("admin_id").(uint)
	if !ok || adminID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Admin ID not found in context", "MISSING_ADMIN_ID", nil)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	metadata.SetRequestID(strings.TrimSpace(c.Get("X-Request-ID")))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/charge-wallet", 30*time.Second)
	defer cancel()
	result, err := h.paymentAdminFlow.AdminChargeWallet(
		ctx,
		&req,
		metadata,
		adminID,
	)
	if err != nil {
		if businessErr, ok := err.(*businessflow.BusinessError); ok {
			if businessErr.Code == "IDEMPOTENCY_KEY_REQUIRED" {
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Idempotency key is required", "IDEMPOTENCY_KEY_REQUIRED", nil)
			}
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsReferrerAgencyIDRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Referrer agency ID is required", "REFERRER_AGENCY_ID_REQUIRED", nil)
		}
		if businessflow.IsAgencyDiscountNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency discount not found", "AGENCY_DISCOUNT_NOT_FOUND", nil)
		}
		if businessflow.IsAmountTooLow(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Amount is too low", "AMOUNT_TOO_LOW", nil)
		}
		if businessflow.IsAmountNotMultiple(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Amount must be a multiple of the required increment",
				"AMOUNT_NOT_MULTIPLE",
				nil,
			)
		}
		if businessflow.IsTaxWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Tax wallet not found", "TAX_WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsSystemWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "System wallet not found", "SYSTEM_WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
		}
		if businessflow.IsTaxWalletBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusNotFound,
				"Tax wallet balance snapshot not found",
				"TAX_WALLET_BALANCE_SNAPSHOT_NOT_FOUND",
				nil,
			)
		}
		if businessflow.IsSystemWalletBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusNotFound,
				"System wallet balance snapshot not found",
				"SYSTEM_WALLET_BALANCE_SNAPSHOT_NOT_FOUND",
				nil,
			)
		}

		log.Println("Admin wallet charging failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Wallet charging by admin failed",
			"WALLET_CHARGING_BY_ADMIN_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Wallet charged successfully by admin", result)
}

// PreviewWalletChargeImpact calculates the expected balance/share changes for an admin wallet charge.
// @Summary Preview Wallet Charge Impact
// @Description Admin endpoint to preview free balance, credit, and agency share changes before charging a customer wallet
// @Tags Payments Admin
// @Accept json
// @Produce json
// @Param request body dto.AdminPreviewWalletChargeImpactRequest true "Admin wallet charge preview payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminPreviewWalletChargeImpactResponse} "Wallet charge impact calculated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized admin"
// @Failure 404 {object} dto.APIResponse "Customer or discount not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/charge-wallet/preview [post]
func (h *PaymentAdminHandler) PreviewWalletChargeImpact(c fiber.Ctx) error {
	var req dto.AdminPreviewWalletChargeImpactRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	adminID, ok := c.Locals("admin_id").(uint)
	if !ok || adminID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Admin ID not found in context", "MISSING_ADMIN_ID", nil)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	metadata.SetRequestID(strings.TrimSpace(c.Get("X-Request-ID")))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/charge-wallet/preview", 30*time.Second)
	defer cancel()

	result, err := h.paymentAdminFlow.AdminPreviewWalletChargeImpact(ctx, &req, metadata, adminID)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsReferrerAgencyIDRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Referrer agency ID is required", "REFERRER_AGENCY_ID_REQUIRED", nil)
		}
		if businessflow.IsAgencyDiscountNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency discount not found", "AGENCY_DISCOUNT_NOT_FOUND", nil)
		}
		if businessflow.IsAmountTooLow(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Amount is too low", "AMOUNT_TOO_LOW", nil)
		}
		if businessflow.IsAmountNotMultiple(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Amount must be a multiple of the required increment",
				"AMOUNT_NOT_MULTIPLE",
				nil,
			)
		}
		if businessflow.IsAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency not found", "AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Agency account is inactive", "AGENCY_INACTIVE", nil)
		}
		if businessflow.IsShebaNumberInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Agency sheba number is invalid", "SHEBA_NUMBER_INVALID", nil)
		}
		if businessflow.IsSystemUserNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "System user not found", "SYSTEM_USER_NOT_FOUND", nil)
		}
		if businessflow.IsSystemUserShebaNumberNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "System user sheba number not found", "SYSTEM_USER_SHEBA_NUMBER_NOT_FOUND", nil)
		}
		if businessflow.IsSystemWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "System wallet not found", "SYSTEM_WALLET_NOT_FOUND", nil)
		}

		log.Println("Admin wallet charge impact preview failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Wallet charge impact preview failed",
			"WALLET_CHARGE_IMPACT_PREVIEW_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Wallet charge impact calculated successfully", result)
}

// ListDepositReceipts lists receipts with filters.
// @Summary Admin list deposit receipts
// @Description Lists deposit receipts with optional filters and previews.
// @Tags Payments Admin
// @Produce json
// @Param status query string false "Receipt status (pending|approved|rejected)"
// @Param lang query string false "Language filter (FA or EN)"
// @Param customer_id query int false "Filter by customer ID"
// @Param customer_name query string false "Filter by customer representative/company name"
// @Param limit query int false "Limit (default 50)"
// @Param offset query int false "Offset"
// @Param order query string false "Order by clause (default id DESC)"
// @Success 200 {object} dto.APIResponse{data=dto.ListDepositReceiptsResponse} "Receipts retrieved"
// @Failure 400 {object} dto.APIResponse "Invalid language"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/deposit-receipts [get]
func (h *PaymentAdminHandler) ListDepositReceipts(c fiber.Ctx) error {
	status := c.Query("status")
	lang := c.Query("lang")
	limit, err := strconv.Atoi(c.Query("limit", "50"))
	if err != nil || limit <= 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "limit must be a positive integer", "INVALID_LIMIT", nil)
	}
	offset, err := strconv.Atoi(c.Query("offset", "0"))
	if err != nil || offset < 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "offset must be a non-negative integer", "INVALID_OFFSET", nil)
	}
	order := c.Query("order", "id DESC")
	var f models.DepositReceiptFilter
	if status != "" {
		s := models.DepositReceiptStatus(status)
		f.Status = &s
	}
	if lang != "" {
		l := strings.ToUpper(lang)
		f.Lang = &l
	}
	if customerStr := c.Query("customer_id"); customerStr != "" {
		cid, err := strconv.ParseUint(customerStr, 10, 64)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_id must be a positive integer", "INVALID_CUSTOMER_ID", nil)
		}
		cu := uint(cid)
		f.CustomerID = &cu
	}
	if customerName := strings.TrimSpace(c.Query("customer_name")); customerName != "" {
		f.CustomerName = &customerName
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/deposit-receipts", 30*time.Second)
	defer cancel()
	resp, err := h.paymentAdminFlow.AdminListDepositReceipts(ctx, f, limit, offset, order)
	if err != nil {
		if businessflow.IsInvalidLanguage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		}
		log.Println("Admin list deposit receipts failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to list deposit receipts",
			"ADMIN_LIST_DEPOSIT_RECEIPTS_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Deposit receipts retrieved", resp)
}

// ListTransactions returns admin transaction list for customer credit increase operations.
// @Summary Admin list transactions
// @Description Lists filtered transactions where source=payment_callback_increase_customer_free_plus_credit and
// operation=increase_customer_free_plus_credit, including full customer details per transaction.
// @Tags Payments Admin
// @Produce json
// @Param page query int false "Page number (default 1)" minimum(1)
// @Param page_size query int false "Page size (default 20, max 100)" minimum(1) maximum(100)
// @Param start_date query string false "Start date (RFC3339)"
// @Param end_date query string false "End date (RFC3339)"
// @Param customer_id query int false "Optional customer filter"
// @Param customer_name query string false "Optional customer name/company filter"
// @Success 200 {object} dto.APIResponse{data=dto.AdminListTransactionsResponse} "Transactions retrieved"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/transactions [get]
func (h *PaymentAdminHandler) ListTransactions(c fiber.Ctx) error {
	page := uint(1)
	if pageStr := c.Query("page"); pageStr != "" {
		parsed, err := strconv.ParseUint(pageStr, 10, 32)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "page must be a positive integer", "INVALID_PAGE", nil)
		}
		page = uint(parsed)
	}

	pageSize := uint(20)
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		parsed, err := strconv.ParseUint(pageSizeStr, 10, 32)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "page_size must be a positive integer", "INVALID_PAGE_SIZE", nil)
		}
		pageSize = uint(parsed)
	}

	var startDate, endDate *time.Time
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		parsed, err := time.Parse(time.RFC3339, startDateStr)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "start_date must be RFC3339 format", "INVALID_START_DATE", nil)
		}
		startDate = &parsed
	}
	if endDateStr := c.Query("end_date"); endDateStr != "" {
		parsed, err := time.Parse(time.RFC3339, endDateStr)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "end_date must be RFC3339 format", "INVALID_END_DATE", nil)
		}
		endDate = &parsed
	}

	var customerID *uint
	if customerIDStr := c.Query("customer_id"); customerIDStr != "" {
		parsed, err := strconv.ParseUint(customerIDStr, 10, 64)
		if err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_id must be a positive integer", "INVALID_CUSTOMER_ID", nil)
		}
		cid := uint(parsed)
		customerID = &cid
	}
	var customerName *string
	if v := strings.TrimSpace(c.Query("customer_name")); v != "" {
		customerName = &v
	}

	req := &dto.AdminListTransactionsRequest{
		Page:         page,
		PageSize:     pageSize,
		StartDate:    startDate,
		EndDate:      endDate,
		CustomerID:   customerID,
		CustomerName: customerName,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	metadata.SetRequestID(strings.TrimSpace(c.Get("X-Request-ID")))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/transactions", 30*time.Second)
	defer cancel()
	res, err := h.paymentAdminFlow.AdminListTransactions(
		ctx,
		req,
		metadata,
	)
	if err != nil {
		switch {
		case businessflow.IsInvalidPage(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
		case businessflow.IsInvalidPageSize(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page size", "INVALID_PAGE_SIZE", nil)
		case businessflow.IsStartDateAfterEndDate(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Start date must be before end date", "START_DATE_AFTER_END_DATE", nil)
		default:
			log.Println("Admin list transactions failed", err)
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list transactions", "ADMIN_LIST_TRANSACTIONS_FAILED", nil)
		}
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved", res)
}

// GetDepositReceiptFile downloads the uploaded file.
// @Summary Admin download deposit receipt file
// @Description Downloads the receipt file by UUID (no ownership check).
// @Tags Payments Admin
// @Produce octet-stream
// @Param uuid path string true "Deposit receipt UUID"
// @Success 200 {file} binary "Receipt file"
// @Failure 404 {object} dto.APIResponse "Receipt not found"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/deposit-receipts/{uuid}/file [get]
func (h *PaymentAdminHandler) GetDepositReceiptFile(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/deposit-receipts/"+uuid+"/file", 30*time.Second)
	defer cancel()
	data, filename, contentType, err := h.paymentAdminFlow.AdminGetDepositReceiptFile(ctx, uuid)
	if err != nil {
		if businessflow.IsDepositReceiptNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		}
		log.Println("Admin get receipt file failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to download receipt file", "ADMIN_DOWNLOAD_RECEIPT_FAILED", nil)
	}
	// ext := ""
	// if dot := strings.LastIndex(filename, "."); dot == -1 {
	// 	if guessed := mimeFromContentType(contentType); guessed != "" {
	// 		ext = guessed
	// 	}
	// }
	// finalName := filename
	// if ext != "" && !strings.HasSuffix(strings.ToLower(filename), ext) {
	// 	finalName = filename + ext
	// }
	// // Ensure browser gets a filename with extension
	// c.Attachment(finalName)
	// if contentType != "" {
	// 	c.Type(contentType, "")
	// }
	// return c.Status(fiber.StatusOK).Send(data)
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Type(contentType, "")
	return c.Status(fiber.StatusOK).Send(data)
}

// UpdateDepositReceiptStatus approves/rejects a receipt.
// @Summary Admin update deposit receipt status
// @Description Approve or reject a deposit receipt; on approval customer_invoice_uuid is required and is linked in payment metadata.
// @Tags Payments Admin
// @Accept json
// @Produce json
// @Param request body dto.AdminUpdateDepositReceiptStatusRequest true "Status update payload"
// @Success 200 {object} dto.APIResponse{data=dto.SubmitDepositReceiptResponse} "Status updated"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Receipt not found"
// @Failure 409 {object} dto.APIResponse "Receipt already finalized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/deposit-receipts/status [post]
func (h *PaymentAdminHandler) UpdateDepositReceiptStatus(c fiber.Ctx) error {
	var req dto.AdminUpdateDepositReceiptStatusRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}
	adminID, ok := c.Locals("admin_id").(uint)
	if !ok || adminID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Admin ID not found in context", "MISSING_ADMIN_ID", nil)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	metadata.SetRequestID(strings.TrimSpace(c.Get("X-Request-ID")))

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/deposit-receipts/status", 30*time.Second)
	defer cancel()
	res, err := h.paymentAdminFlow.AdminUpdateDepositReceiptStatus(
		ctx,
		&req,
		adminID,
		metadata,
	)
	if err != nil {
		switch {
		case businessflow.IsDepositReceiptNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		case businessflow.IsDepositReceiptAlreadyApproved(err):
			return h.ErrorResponse(c, fiber.StatusConflict, "Receipt already approved", "RECEIPT_ALREADY_APPROVED", nil)
		case businessflow.IsDepositReceiptAlreadyRejected(err):
			return h.ErrorResponse(c, fiber.StatusConflict, "Receipt already rejected", "RECEIPT_ALREADY_REJECTED", nil)
		case businessflow.IsDepositReceiptInvalidStatus(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "action must be either approve or reject", "INVALID_RECEIPT_ACTION", nil)
		case businessflow.IsDepositReceiptInvoiceRequired(err):
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"customer_invoice_uuid is required when approving a receipt",
				"INVOICE_UUID_REQUIRED",
				nil,
			)
		case businessflow.IsDepositReceiptInvoiceInvalid(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_invoice_uuid is invalid", "INVOICE_UUID_INVALID", nil)
		case businessflow.IsDepositReceiptInvoiceDuplicate(err):
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"customer_invoice_uuid is already linked to another payment",
				"INVOICE_UUID_ALREADY_USED",
				nil,
			)
		case businessflow.IsInvalidLanguage(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		default:
			log.Println("Admin update receipt status failed", err)
			return h.ErrorResponse(
				c,
				fiber.StatusInternalServerError,
				"Failed to update receipt status",
				"ADMIN_UPDATE_RECEIPT_FAILED",
				nil,
			)
		}
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Receipt status updated", res)
}

// AddInvoiceToTransaction links invoice_uuid into metadata of a transaction resolved by transaction_id.
// @Summary Admin link invoice to transaction
// @Description Resolves transaction by transaction_uuid and merges customer_invoice_uuid into transaction metadata.
// @Tags Payments Admin
// @Accept json
// @Produce json
// @Param request body dto.AdminAddInvoiceToTransactionRequest true "Invoice linking payload"
// @Success 200 {object} dto.APIResponse{data=dto.AdminAddInvoiceToTransactionResponse} "Invoice linked"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Transaction not found"
// @Failure 409 {object} dto.APIResponse "Invoice mismatch with existing metadata"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/payments/transactions/invoice [post]
func (h *PaymentAdminHandler) AddInvoiceToTransaction(c fiber.Ctx) error {
	var req dto.AdminAddInvoiceToTransactionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}
	adminID, ok := c.Locals("admin_id").(uint)
	if !ok || adminID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Admin ID not found in context", "MISSING_ADMIN_ID", nil)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	metadata.SetRequestID(strings.TrimSpace(c.Get("X-Request-ID")))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/payments/transactions/invoice", 30*time.Second)
	defer cancel()
	res, err := h.paymentAdminFlow.AddInvoiceToTransaction(
		ctx,
		&req,
		adminID,
		metadata,
	)
	if err != nil {
		switch {
		case businessflow.IsTransactionUUIDInvalid(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "transaction_uuid is invalid", "TRANSACTION_UUID_INVALID", nil)
		case businessflow.IsTransactionNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found", "TRANSACTION_NOT_FOUND", nil)
		case businessflow.IsPaymentRequestNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Payment request not found", "PAYMENT_REQUEST_NOT_FOUND", nil)
		case businessflow.IsInvoiceUUIDRequired(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_invoice_uuid is required", "INVOICE_UUID_REQUIRED", nil)
		case businessflow.IsInvoiceUUIDInvalid(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "customer_invoice_uuid is invalid", "INVOICE_UUID_INVALID", nil)
		case businessflow.IsInvoiceUUIDMismatch(err):
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"customer_invoice_uuid conflicts with existing transaction metadata",
				"INVOICE_UUID_MISMATCH",
				nil,
			)
		default:
			log.Println("Admin add invoice to transaction failed", err)
			return h.ErrorResponse(
				c,
				fiber.StatusInternalServerError,
				"Failed to add invoice to transaction",
				"ADMIN_ADD_INVOICE_FAILED",
				nil,
			)
		}
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Invoice linked to transaction", res)
}

func (h *PaymentAdminHandler) createRequestContextWithTimeout(
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
