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
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// PaymentHandlerInterface defines the contract for payment handlers
type PaymentHandlerInterface interface {
	ChargeWallet(c fiber.Ctx) error
	PaymentCallback(c fiber.Ctx) error
	GetTransactionHistory(c fiber.Ctx) error
	GetWalletBalance(c fiber.Ctx) error
	SubmitDepositReceipt(c fiber.Ctx) error
	ListDepositReceipts(c fiber.Ctx) error
	PreviewProformaInvoice(c fiber.Ctx) error
	PreviewProformaInvoiceByAmount(c fiber.Ctx) error
	DownloadDepositReceiptFile(c fiber.Ctx) error
	UpdateDepositReceiptFile(c fiber.Ctx) error
	DeleteDepositReceiptFile(c fiber.Ctx) error
	NotifyInvoiceIssueRequest(c fiber.Ctx) error
}

// PaymentHandler handles payment-related HTTP requests
type PaymentHandler struct {
	paymentFlow businessflow.PaymentFlow
	validator   *validator.Validate
}

func (h *PaymentHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *PaymentHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// NewPaymentHandler creates a new payment handler
func NewPaymentHandler(paymentFlow businessflow.PaymentFlow) *PaymentHandler {
	handler := &PaymentHandler{
		paymentFlow: paymentFlow,
		validator:   validator.New(),
	}

	// Setup custom validations
	handler.setupCustomValidations()

	return handler
}

// ChargeWallet handles the wallet charging process
// @Summary Charge Wallet
// @Description Charge a customer's wallet with the specified amount using Atipay payment gateway
// @Tags Payments
// @Accept json
// @Produce json
// @Param request body dto.ChargeWalletRequest true "Wallet charging data"
// @Success 200 {object} dto.APIResponse{data=dto.ChargeWalletResponse} "Wallet charged successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/charge-wallet [post]
func (h *PaymentHandler) ChargeWallet(c fiber.Ctx) error {
	var req dto.ChargeWalletRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Validate request
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Get client information
	ipAddress := c.IP()
	userAgent := c.Get("User-Agent")
	metadata := businessflow.NewClientMetadata(ipAddress, userAgent)

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Set the authenticated customer ID in the request
	req.CustomerID = customerID

	// Call business logic with proper context
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/charge-wallet", 30*time.Second)
	defer cancel()
	result, err := h.paymentFlow.ChargeWallet(ctx, &req, metadata)
	if err != nil {
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
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
		if businessflow.IsInvalidLanguage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language (allowed: FA, EN)", "INVALID_LANGUAGE", nil)
		}
		if businessflow.IsAtipayTokenEmpty(err) {
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to get payment token", "ATIPAY_TOKEN_ERROR", nil)
		}

		log.Println("Wallet charging failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Wallet charging failed", "WALLET_CHARGING_FAILED", nil)
	}

	// Successful wallet charging
	return h.SuccessResponse(c, fiber.StatusOK, "Wallet charged successfully", fiber.Map{
		"message": result.Message,
		"success": result.Success,
		"token":   result.Token,
	})
}

// PaymentCallback handles the callback from the payment gateway
// @Summary Payment Callback
// @Description Handles the callback from the payment gateway (Atipay)
// @Tags Payments
// @Accept json
// @Accept x-www-form-urlencoded
// @Produce html
// @Param invoice_number path string true "Invoice (reservation) number"
// @Param request body dto.AtipayRequest false "Callback data from Atipay (form or JSON)"
// @Success 200 {string} string "HTML payment result page"
// @Failure 400 {object} dto.APIResponse "Invalid request or validation error"
// @Failure 404 {object} dto.APIResponse "Payment request not found"
// @Failure 409 {object} dto.APIResponse "Payment already processed or expired"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/callback/{invoice_number} [post]
func (h *PaymentHandler) PaymentCallback(c fiber.Ctx) error {
	// Extract invoice number from path
	invoiceNumber := c.Params("invoice_number")

	// Prefer query parameters, then fallback to form and JSON
	callbackReq := dto.AtipayRequest{
		State:             c.Query("state"),
		Status:            c.Query("status"),
		ReferenceNumber:   c.Query("referenceNumber"),
		ReservationNumber: c.Query("reservationNumber"),
		TerminalID:        c.Query("terminalId"),
		TraceNumber:       c.Query("traceNumber"),
		MaskedPAN:         c.Query("maskedPan"),
		RRN:               c.Query("rrn"),
	}

	// Fallback to form values if query is empty
	if callbackReq.State == "" && callbackReq.Status == "" && len(c.Body()) > 0 {
		formReq := dto.AtipayRequest{
			State:             c.FormValue("state"),
			Status:            c.FormValue("status"),
			ReferenceNumber:   c.FormValue("referenceNumber"),
			ReservationNumber: c.FormValue("reservationNumber"),
			TerminalID:        c.FormValue("terminalId"),
			TraceNumber:       c.FormValue("traceNumber"),
			MaskedPAN:         c.FormValue("maskedPan"),
			RRN:               c.FormValue("rrn"),
		}
		callbackReq = formReq
	}

	// Fallback to JSON body if still empty
	if callbackReq.State == "" && len(c.Body()) > 0 {
		var alt dto.AtipayRequest
		if err := c.Bind().JSON(&alt); err == nil {
			callbackReq = alt
		}
	}

	// If reservation number missing, use invoice from path
	if callbackReq.ReservationNumber == "" {
		callbackReq.ReservationNumber = invoiceNumber
	}

	// Fallback: derive status from state if missing (align with Atipay sample)
	switch callbackReq.Status {
	case "", "0":
		// Map common states to numeric codes used in our mapping
		switch callbackReq.State {
		case "OK":
			callbackReq.Status = "2"
		case "CanceledByUser":
			callbackReq.Status = "1"
		case "Failed":
			callbackReq.Status = "3"
		case "SessionIsNull":
			callbackReq.Status = "4"
		case "InvalidParameters":
			callbackReq.Status = "5"
		}
	}

	// Validate request structure
	if err := h.validator.Struct(&callbackReq); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Client metadata
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Process callback
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/callback/"+invoiceNumber, 30*time.Second)
	defer cancel()
	resultHTML, err := h.paymentFlow.PaymentCallback(ctx, &callbackReq, metadata)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}

		if businessflow.IsCallbackRequestNil(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Callback request is required", "CALLBACK_REQUEST_NIL", nil)
		}
		if businessflow.IsReservationNumberRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Reservation number is required", "RESERVATION_NUMBER_REQUIRED", nil)
		}
		if businessflow.IsReferenceNumberRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Reference number is required", "REFERENCE_NUMBER_REQUIRED", nil)
		}
		if businessflow.IsStatusRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Status is required", "STATUS_REQUIRED", nil)
		}
		if businessflow.IsStateRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "State is required", "STATE_REQUIRED", nil)
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

		if businessflow.IsAgencyDiscountNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Agency discount not found", "AGENCY_DISCOUNT_NOT_FOUND", nil)
		}

		if businessflow.IsPaymentRequestNotFound(err) {
			log.Printf("Payment request not found for invoice: %s", callbackReq.ReservationNumber)
			return h.ErrorResponse(c, fiber.StatusNotFound, "Payment request not found", "PAYMENT_REQUEST_NOT_FOUND", nil)
		}
		if businessflow.IsPaymentRequestAlreadyProcessed(err) {
			log.Printf("Payment already processed for invoice: %s", callbackReq.ReservationNumber)
			return h.ErrorResponse(c, fiber.StatusConflict, "Payment already processed", "PAYMENT_ALREADY_PROCESSED", nil)
		}
		if businessflow.IsPaymentRequestExpired(err) {
			log.Printf("Payment request expired for invoice: %s", callbackReq.ReservationNumber)
			return h.ErrorResponse(c, fiber.StatusConflict, "Payment request expired", "PAYMENT_REQUEST_EXPIRED", nil)
		}

		if businessErr, ok := err.(*businessflow.BusinessError); ok {
			switch businessErr.Code {
			case "PAYMENT_CALLBACK_VALIDATION_FAILED":
				return h.ErrorResponse(
					c,
					fiber.StatusBadRequest,
					"Payment callback validation failed",
					"PAYMENT_CALLBACK_VALIDATION_FAILED",
					businessErr.Error(),
				)
			case "PAYMENT_CALLBACK_HTML_GENERATION_FAILED":
				log.Printf("HTML generation failed for payment request: %s, error: %v", callbackReq.ReservationNumber, err)
				return h.ErrorResponse(
					c,
					fiber.StatusInternalServerError,
					"Failed to generate payment result page",
					"HTML_GENERATION_FAILED",
					nil,
				)
			case "PAYMENT_CALLBACK_FAILED":
				log.Printf("Payment callback processing failed for invoice: %s, error: %v", callbackReq.ReservationNumber, err)
				return h.ErrorResponse(
					c,
					fiber.StatusInternalServerError,
					"Payment callback processing failed",
					"PAYMENT_CALLBACK_FAILED",
					nil,
				)
			}
		}

		log.Printf("Unexpected error in payment callback for invoice: %s, error: %v", callbackReq.ReservationNumber, err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Payment callback processing failed", "PAYMENT_CALLBACK_FAILED", nil)
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Status(fiber.StatusOK).SendString(resultHTML)
}

// GetTransactionHistory handles the retrieval of transaction history for a customer
// @Summary Get Transaction History
// @Description Retrieve paginated transaction history for the authenticated customer
// @Tags Payments
// @Accept json
// @Produce json
// @Param page query int false "Page number (default: 1)" minimum(1)
// @Param page_size query int false "Number of items per page (default: 20, max: 100)" minimum(1) maximum(100)
// @Param start_date query string false "Start date filter (ISO 8601 format)"
// @Param end_date query string false "End date filter (ISO 8601 format)"
// @Param type query string false "Transaction type filter"
// @Param status query string false "Transaction status filter"
// @Success 200 {object} dto.APIResponse{data=dto.TransactionHistoryResponse} "Transaction history retrieved successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/history [get]
func (h *PaymentHandler) GetTransactionHistory(c fiber.Ctx) error {
	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Parse query parameters
	page := uint(1)
	if pageStr := c.Query("page"); pageStr != "" {
		if parsed, err := strconv.ParseUint(pageStr, 10, 32); err == nil {
			page = uint(parsed)
		}
	}

	pageSize := uint(20)
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if parsed, err := strconv.ParseUint(pageSizeStr, 10, 32); err == nil {
			pageSize = uint(parsed)
		}
	}

	// Parse date filters
	var startDate, endDate *time.Time
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startDateStr); err == nil {
			startDate = &parsed
		}
	}
	if endDateStr := c.Query("end_date"); endDateStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endDateStr); err == nil {
			endDate = &parsed
		}
	}

	// Parse type and status filters
	var transactionType, transactionStatus *string
	if typeStr := c.Query("type"); typeStr != "" {
		transactionType = &typeStr
	}
	if statusStr := c.Query("status"); statusStr != "" {
		transactionStatus = &statusStr
	}

	// Build request
	req := &dto.GetTransactionHistoryRequest{
		CustomerID: customerID,
		Page:       page,
		PageSize:   pageSize,
		StartDate:  startDate,
		EndDate:    endDate,
		Type:       transactionType,
		Status:     transactionStatus,
	}

	// Get client information
	ipAddress := c.IP()
	userAgent := c.Get("User-Agent")
	metadata := businessflow.NewClientMetadata(ipAddress, userAgent)

	// Call business logic
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/history", 30*time.Second)
	defer cancel()
	result, err := h.paymentFlow.GetTransactionHistory(ctx, req, metadata)
	if err != nil {
		if businessflow.IsInvalidPage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
		}
		if businessflow.IsInvalidPageSize(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page size", "INVALID_PAGE_SIZE", nil)
		}
		if businessflow.IsStartDateAfterEndDate(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Start date must be before end date", "START_DATE_AFTER_END_DATE", nil)
		}
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}

		log.Println("Transaction history retrieval failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to retrieve transaction history",
			"TRANSACTION_HISTORY_RETRIEVAL_FAILED",
			nil,
		)
	}

	// Return successful response
	return h.SuccessResponse(c, fiber.StatusOK, "Transaction history retrieved successfully", result)
}

// GetWalletBalance handles the user wallet balance retrieval process (payment flow)
// @Summary Get User Wallet Balance
// @Description Retrieve the current wallet balance and financial information for the authenticated user
// @Tags Wallet
// @Accept json
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.GetWalletBalanceResponse} "Wallet balance retrieved successfully"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 404 {object} dto.APIResponse "Wallet or snapshot not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/wallet/balance [get]
func (h *PaymentHandler) GetWalletBalance(c fiber.Ctx) error {
	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Create request
	req := &dto.GetWalletBalanceRequest{CustomerID: customerID}

	// Client metadata
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Business call
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/wallet/balance", 30*time.Second)
	defer cancel()
	result, err := h.paymentFlow.GetWalletBalance(ctx, req, metadata)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsWalletNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
		}
		if businessflow.IsBalanceSnapshotNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
		}

		log.Println("Wallet balance retrieval failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Wallet balance retrieval failed",
			"WALLET_BALANCE_RETRIEVAL_FAILED",
			nil,
		)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Wallet balance retrieved successfully", result)
}

// createRequestContextWithTimeout creates a context with custom timeout and request-scoped values
func (h *PaymentHandler) createRequestContextWithTimeout(
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

	return ctx, cancel
}

// setupCustomValidations sets up custom validation rules
func (h *PaymentHandler) setupCustomValidations() {
	// Add custom validation rules if needed
	// Example: h.validator.RegisterValidation("custom_rule", customValidationFunc)
}

// SubmitDepositReceipt uploads a deposit receipt for manual review (no immediate credit).
// @Summary Submit deposit receipt
// @Description Upload a bank deposit receipt file (base64) for finance review; balance is credited only after approval.
// @Tags Payments
// @Accept json
// @Produce json
// @Param request body dto.SubmitDepositReceiptRequest true "Deposit receipt payload"
// @Success 201 {object} dto.APIResponse{data=dto.SubmitDepositReceiptResponse} "Receipt submitted"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/deposit-receipts [post]
func (h *PaymentHandler) SubmitDepositReceipt(c fiber.Ctx) error {
	var req dto.SubmitDepositReceiptRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	req.CustomerID, _ = c.Locals("customer_id").(uint)
	if req.CustomerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/deposit-receipts", 30*time.Second)
	defer cancel()
	res, err := h.paymentFlow.SubmitDepositReceipt(ctx, &req, metadata)
	if err != nil {
		switch {
		case businessflow.IsInvalidLanguage(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		case businessflow.IsDepositReceiptFileEmpty(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "File is empty", "FILE_EMPTY", nil)
		case businessflow.IsDepositReceiptFileTooLarge(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large (max 5MB)", "FILE_TOO_LARGE", nil)
		case businessflow.IsDepositReceiptFileInvalidType(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Unsupported file type", "INVALID_FILE_TYPE", nil)
		default:
			log.Println("Submit deposit receipt failed", err)
			return h.ErrorResponse(
				c,
				fiber.StatusInternalServerError,
				"Submit deposit receipt failed",
				"SUBMIT_DEPOSIT_RECEIPT_FAILED",
				nil,
			)
		}
	}
	return h.SuccessResponse(c, fiber.StatusCreated, "Deposit receipt submitted", res)
}

// ListDepositReceipts returns current user's receipts.
// @Summary List deposit receipts
// @Description Returns the user's submitted deposit receipts with status and metadata.
// @Tags Payments
// @Produce json
// @Param lang query string false "Language filter (FA or EN)"
// @Success 200 {object} dto.APIResponse{data=dto.ListDepositReceiptsResponse} "Receipts retrieved"
// @Failure 400 {object} dto.APIResponse "Invalid language"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/deposit-receipts [get]
func (h *PaymentHandler) ListDepositReceipts(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	lang := c.Query("lang")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/deposit-receipts", 30*time.Second)
	defer cancel()
	resp, err := h.paymentFlow.ListDepositReceipts(ctx, customerID, lang)
	if err != nil {
		if businessflow.IsInvalidLanguage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		}
		log.Println("List deposit receipts failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list deposit receipts", "LIST_DEPOSIT_RECEIPTS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Deposit receipts retrieved", resp)
}

// PreviewProformaInvoice returns JSON data.
// @Summary Preview proforma invoice
// @Description Returns JSON containing seller/buyer info, amounts, and tax for the requested deposit receipt and language.
// @Tags Payments
// @Produce json
// @Param receipt_uuid query string true "Deposit receipt UUID"
// @Param lang query string false "Language (FA or EN)"
// @Success 200 {object} dto.APIResponse{data=dto.ProformaPreviewResponse} "Preview generated"
// @Failure 400 {object} dto.APIResponse "Invalid receipt or language"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/proforma/preview [get]
func (h *PaymentHandler) PreviewProformaInvoice(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	receiptUUID := c.Query("receipt_uuid")
	lang := c.Query("lang")
	if strings.TrimSpace(receiptUUID) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "receipt_uuid is required", "INVALID_RECEIPT", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/proforma/preview", 30*time.Second)
	defer cancel()
	resp, err := h.paymentFlow.PreviewProformaInvoice(ctx, customerID, receiptUUID, lang)
	if err != nil {
		if businessflow.IsInvalidLanguage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		}
		if businessflow.IsDepositReceiptNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		}
		if businessflow.IsAmountTooLow(err) || businessflow.IsAmountNotMultiple(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid amount", "INVALID_AMOUNT", nil)
		}
		log.Println("Preview proforma failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to preview proforma invoice", "PROFORMA_PREVIEW_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Proforma preview generated", resp)
}

// PreviewProformaInvoiceByAmount returns proforma data using user-provided amount (no receipt).
// @Summary Preview proforma invoice by amount
// @Description Returns proforma invoice data using supplied amount (no receipt).
// @Tags Payments
// @Produce json
// @Param amount query int true "Amount with tax (Toman)"
// @Param lang query string false "Language (FA or EN)"
// @Success 200 {object} dto.APIResponse{data=dto.ProformaPreviewResponse} "Preview generated"
// @Failure 400 {object} dto.APIResponse "Invalid amount or language"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/proforma/preview-by-amount [get]
func (h *PaymentHandler) PreviewProformaInvoiceByAmount(c fiber.Ctx) error {
	customerID, _ := c.Locals("customer_id").(uint)
	if customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	amountStr := c.Query("amount")
	if strings.TrimSpace(amountStr) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Amount is required", "INVALID_AMOUNT", nil)
	}
	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil || amount == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid amount", "INVALID_AMOUNT", nil)
	}
	lang := c.Query("lang")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/proforma/preview-by-amount", 30*time.Second)
	defer cancel()
	resp, err := h.paymentFlow.PreviewProformaInvoiceByAmount(ctx, customerID, amount, lang)
	if err != nil {
		if businessflow.IsInvalidLanguage(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid language", "INVALID_LANGUAGE", nil)
		}
		if businessflow.IsAmountTooLow(err) || businessflow.IsAmountNotMultiple(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid amount", "INVALID_AMOUNT", nil)
		}
		log.Println("Preview proforma by amount failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to preview proforma invoice", "PROFORMA_PREVIEW_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Proforma preview generated", resp)
}

// DownloadDepositReceiptFile returns the uploaded receipt file for the authenticated customer.
// @Summary Download deposit receipt file
// @Description Downloads the original receipt file uploaded by the customer.
// @Tags Payments
// @Produce octet-stream
// @Param receipt_uuid path string true "Deposit receipt UUID"
// @Success 200 {file} binary "Receipt file"
// @Failure 400 {object} dto.APIResponse "Invalid receipt"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/deposit-receipts/{receipt_uuid}/file [get]
func (h *PaymentHandler) DownloadDepositReceiptFile(c fiber.Ctx) error {
	customerID, _ := c.Locals("customer_id").(uint)
	if customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	receiptUUID := c.Params("receipt_uuid")
	if strings.TrimSpace(receiptUUID) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid receipt_uuid", "INVALID_RECEIPT", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/deposit-receipts/"+receiptUUID+"/file", 30*time.Second)
	defer cancel()
	data, filename, contentType, err := h.paymentFlow.DownloadDepositReceiptFile(ctx, customerID, receiptUUID)
	if err != nil {
		if businessflow.IsDepositReceiptNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		}
		if businessflow.IsDepositReceiptFileEmpty(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt file not available", "RECEIPT_FILE_EMPTY", nil)
		}
		log.Println("Download receipt failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to download receipt", "RECEIPT_DOWNLOAD_FAILED", nil)
	}
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Type(contentType, "")
	return c.Status(fiber.StatusOK).Send(data)
}

// UpdateDepositReceiptFile replaces the receipt file while status is pending.
// @Summary Update deposit receipt file
// @Description Replaces the uploaded receipt file if the receipt is still pending.
// @Tags Payments
// @Accept json
// @Produce json
// @Param receipt_uuid path string true "Deposit receipt UUID"
// @Param request body dto.UpdateDepositReceiptFileRequest true "New receipt file payload"
// @Success 200 {object} dto.APIResponse "File updated"
// @Failure 400 {object} dto.APIResponse "Invalid request or receipt"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 409 {object} dto.APIResponse "Receipt already finalized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/deposit-receipts/{receipt_uuid}/file [put]
func (h *PaymentHandler) UpdateDepositReceiptFile(c fiber.Ctx) error {
	customerID, _ := c.Locals("customer_id").(uint)
	if customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	receiptUUID := c.Params("receipt_uuid")
	if strings.TrimSpace(receiptUUID) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid receipt_uuid", "INVALID_RECEIPT", nil)
	}
	var req dto.UpdateDepositReceiptFileRequest
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

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/deposit-receipts/"+receiptUUID+"/file", 30*time.Second)
	defer cancel()
	err := h.paymentFlow.UpdateDepositReceiptFile(ctx, customerID, receiptUUID, &req)
	if err != nil {
		switch {
		case businessflow.IsDepositReceiptNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		case businessflow.IsDepositReceiptAlreadyFinalized(err):
			return h.ErrorResponse(c, fiber.StatusConflict, "Receipt already finalized", "RECEIPT_FINALIZED", nil)
		case businessflow.IsDepositReceiptFileTooLarge(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large (max 5MB)", "FILE_TOO_LARGE", nil)
		case businessflow.IsDepositReceiptFileInvalidType(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Unsupported file type", "INVALID_FILE_TYPE", nil)
		default:
			log.Println("Update receipt file failed", err)
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update receipt file", "RECEIPT_FILE_UPDATE_FAILED", nil)
		}
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Receipt file updated", fiber.Map{"ok": true})
}

// DeleteDepositReceiptFile clears the stored file while status is pending.
// @Summary Delete deposit receipt file
// @Description Removes the uploaded receipt file content (keeps receipt record) if pending.
// @Tags Payments
// @Produce json
// @Param receipt_uuid path string true "Deposit receipt UUID"
// @Success 200 {object} dto.APIResponse "File removed"
// @Failure 400 {object} dto.APIResponse "Invalid receipt"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 409 {object} dto.APIResponse "Receipt already finalized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/payments/deposit-receipts/{receipt_uuid}/file [delete]
func (h *PaymentHandler) DeleteDepositReceiptFile(c fiber.Ctx) error {
	customerID, _ := c.Locals("customer_id").(uint)
	if customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	receiptUUID := c.Params("receipt_uuid")
	if strings.TrimSpace(receiptUUID) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid receipt_uuid", "INVALID_RECEIPT", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/deposit-receipts/"+receiptUUID+"/file", 30*time.Second)
	defer cancel()
	err := h.paymentFlow.DeleteDepositReceiptFile(ctx, customerID, receiptUUID)
	if err != nil {
		switch {
		case businessflow.IsDepositReceiptNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Receipt not found", "RECEIPT_NOT_FOUND", nil)
		case businessflow.IsDepositReceiptAlreadyFinalized(err):
			return h.ErrorResponse(c, fiber.StatusConflict, "Receipt already finalized", "RECEIPT_FINALIZED", nil)
		default:
			log.Println("Delete receipt file failed", err)
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to delete receipt file", "RECEIPT_FILE_DELETE_FAILED", nil)
		}
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Receipt file removed", fiber.Map{"ok": true})
}

// NotifyInvoiceIssueRequest sends an SMS notification to a deposit reviewer to issue invoice.
// @Summary Notify admin to issue invoice
// @Description Customer requests invoice issuance for a transaction. Rate-limited to once every 24h per customer.
// @Tags Payments
// @Accept json
// @Produce json
// @Param request body dto.NotifyInvoiceIssueRequest true "Transaction UUID payload"
// @Success 200 {object} dto.APIResponse{data=dto.NotifyInvoiceIssueResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 403 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 409 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/payments/transactions/invoice-issue-request [post]
func (h *PaymentHandler) NotifyInvoiceIssueRequest(c fiber.Ctx) error {
	var req dto.NotifyInvoiceIssueRequest
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

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/payments/transactions/invoice-issue-request", 30*time.Second)
	defer cancel()
	resp, err := h.paymentFlow.NotifyInvoiceIssueRequest(
		ctx,
		&req,
		customerID,
		metadata,
	)
	if err != nil {
		switch {
		case businessflow.IsTransactionUUIDInvalid(err):
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction UUID", "INVALID_TRANSACTION_UUID", nil)
		case businessflow.IsTransactionNotFound(err):
			return h.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found", "TRANSACTION_NOT_FOUND", nil)
		case errors.Is(err, businessflow.ErrForbidden):
			return h.ErrorResponse(c, fiber.StatusForbidden, "Forbidden", "FORBIDDEN", nil)
		case businessflow.IsInvoiceIssueRequestRateLimited(err):
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"You have already submitted an invoice issue request in the last 24 hours. Please try again later.",
				"INVOICE_ISSUE_REQUEST_RATE_LIMITED",
				nil,
			)
		case businessflow.IsInvoiceIssueAlreadyAssigned(err):
			return h.ErrorResponse(
				c,
				fiber.StatusConflict,
				"Invoice is already linked to this transaction",
				"INVOICE_ALREADY_ASSIGNED",
				nil,
			)
		default:
			log.Println("Notify invoice issue request failed", err)
			return h.ErrorResponse(
				c,
				fiber.StatusInternalServerError,
				"Failed to submit invoice issue request",
				"INVOICE_ISSUE_REQUEST_FAILED",
				nil,
			)
		}
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Invoice issue request submitted", resp)
}
