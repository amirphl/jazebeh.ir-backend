package handlers

import (
	"context"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

type CryptoPaymentHandlerInterface interface {
	CreateRequest(c fiber.Ctx) error
	GetStatus(c fiber.Ctx) error
	ManualVerify(c fiber.Ctx) error
	Webhook(c fiber.Ctx) error
}

type CryptoPaymentHandler struct {
	flow      businessflow.CryptoPaymentFlow
	validator *validator.Validate
	cfg       *config.ProductionConfig
}

func NewCryptoPaymentHandler(flow businessflow.CryptoPaymentFlow, cfg *config.ProductionConfig) *CryptoPaymentHandler {
	return &CryptoPaymentHandler{flow: flow, validator: validator.New(), cfg: cfg}
}

// CreateRequest creates a crypto payment request and returns deposit address
// @Summary Create Crypto Payment Request
// @Description Create a crypto payment request and receive deposit address (with optional memo/tag)
// @Tags Payments
// @Accept json
// @Produce json
// @Param request body dto.CreateCryptoPaymentRequest true "Crypto payment request payload"
// @Success 200 {object} dto.APIResponse{data=dto.CreateCryptoPaymentResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/crypto/payments/request [post]
func (h *CryptoPaymentHandler) CreateRequest(c fiber.Ctx) error {
	if !h.cfg.Crypto.Enabled {
		return cryptoPaymentsDisabled(c)
	}
	var req dto.CreateCryptoPaymentRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Invalid request body", Error: dto.ErrorDetail{Code: "INVALID_REQUEST"}})
	}
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Unauthorized", Error: dto.ErrorDetail{Code: "MISSING_CUSTOMER_ID"}})
	}
	req.CustomerID = customerID
	if err := h.validator.Struct(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Validation failed", Error: dto.ErrorDetail{Code: "VALIDATION_ERROR", Details: err.Error()}})
	}
	meta := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	resp, err := h.flow.CreateRequest(h.requestCtx(c, "/api/v1/crypto/payments/request"), &req, meta)
	if err != nil {
		return mapCryptoErr(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(dto.APIResponse{Success: true, Message: "Crypto payment request created", Data: resp})
}

// GetStatus returns the current status and detected deposits for a crypto payment request
// @Summary Get Crypto Payment Status
// @Description Get the current status of a crypto payment request and list of detected deposits
// @Tags Payments
// @Accept json
// @Produce json
// @Param uuid path string true "Crypto payment request UUID"
// @Success 200 {object} dto.APIResponse{data=dto.GetCryptoPaymentStatusResponse}
// @Failure 401 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/crypto/payments/{uuid}/status [get]
func (h *CryptoPaymentHandler) GetStatus(c fiber.Ctx) error {
	if !h.cfg.Crypto.Enabled {
		return cryptoPaymentsDisabled(c)
	}
	uuid := c.Params("uuid")
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Unauthorized", Error: dto.ErrorDetail{Code: "MISSING_CUSTOMER_ID"}})
	}
	req := dto.GetCryptoPaymentStatusRequest{UUID: uuid, CustomerID: customerID}
	meta := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	resp, err := h.flow.GetStatus(h.requestCtx(c, "/api/v1/crypto/payments/"+uuid+"/status"), &req, meta)
	if err != nil {
		return mapCryptoErr(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(dto.APIResponse{Success: true, Message: "Crypto payment status", Data: resp})
}

// ManualVerify verifies a deposit by tx hash and credits wallet if confirmed
// @Summary Manual Verify Crypto Deposit
// @Description Manually verify a crypto deposit by transaction hash (for troubleshooting or fallback)
// @Tags Payments
// @Accept json
// @Produce json
// @Param request body dto.ManualVerifyCryptoDepositRequest true "Manual verify payload"
// @Success 200 {object} dto.APIResponse{data=dto.ManualVerifyCryptoDepositResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/crypto/payments/verify [post]
func (h *CryptoPaymentHandler) ManualVerify(c fiber.Ctx) error {
	if !h.cfg.Crypto.Enabled {
		return cryptoPaymentsDisabled(c)
	}
	var req dto.ManualVerifyCryptoDepositRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Invalid request body", Error: dto.ErrorDetail{Code: "INVALID_REQUEST"}})
	}
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Unauthorized", Error: dto.ErrorDetail{Code: "MISSING_CUSTOMER_ID"}})
	}
	req.CustomerID = customerID
	if err := h.validator.Struct(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Validation failed", Error: dto.ErrorDetail{Code: "VALIDATION_ERROR", Details: err.Error()}})
	}
	meta := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	resp, err := h.flow.ManualVerify(h.requestCtx(c, "/api/v1/crypto/payments/verify"), &req, meta)
	if err != nil {
		return mapCryptoErr(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(dto.APIResponse{Success: true, Message: "Crypto deposit verified", Data: resp})
}

// Webhook receives provider callbacks (oxapay)
// @Summary Crypto Provider Webhook
// @Description Receives provider callbacks and updates deposit and wallet balances
// @Tags Payments
// @Accept json
// @Produce text/plain
// @Param platform path string true "Provider platform (oxapay)"
// @Success 200 {string} string "OK"
// @Router /api/v1/crypto/providers/{platform}/callback [post]
func (h *CryptoPaymentHandler) Webhook(c fiber.Ctx) error {
	if !h.cfg.Crypto.Enabled {
		return c.Status(fiber.StatusNotFound).SendString("NOT_SUPPORTED")
	}
	platform := c.Params("platform")
	meta := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	switch platform {
	case "oxapay":
		raw := c.Body()
		hmacHeader := c.Get("HMAC")
		if err := h.flow.HandleOxapayWebhook(h.requestCtx(c, "/api/v1/crypto/providers/oxapay/callback"), raw, hmacHeader, h.cfg.Crypto.Oxapay.APIKey, meta); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("ERR")
		}
		// return c.SendString("OK")
		return c.SendString("ok") // TODO: TEST
	default:
		return c.Status(fiber.StatusNotFound).SendString("NOT_SUPPORTED")
	}
}

func cryptoPaymentsDisabled(c fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(dto.APIResponse{
		Success: false,
		Message: "Crypto payments are disabled",
		Error:   dto.ErrorDetail{Code: "CRYPTO_PAYMENTS_DISABLED"},
	})
}

func (h *CryptoPaymentHandler) requestCtx(c fiber.Ctx, endpoint string) context.Context {
	return context.WithValue(context.Background(), utils.EndpointKey, endpoint)
}

func mapCryptoErr(c fiber.Ctx, err error) error {
	switch {
	case businessflow.IsCustomerNotFound(err):
		return c.Status(fiber.StatusUnauthorized).
			JSON(dto.APIResponse{Success: false, Message: "Customer not found", Error: dto.ErrorDetail{Code: "CUSTOMER_NOT_FOUND"}})
	case businessflow.IsAccountInactive(err):
		return c.Status(fiber.StatusForbidden).
			JSON(dto.APIResponse{Success: false, Message: "Account inactive", Error: dto.ErrorDetail{Code: "ACCOUNT_INACTIVE"}})
	case businessflow.IsAgencyDiscountNotFound(err):
		return c.Status(fiber.StatusNotFound).
			JSON(dto.APIResponse{Success: false, Message: "Agency discount not found", Error: dto.ErrorDetail{Code: "AGENCY_DISCOUNT_NOT_FOUND"}})
	case businessflow.IsCryptoUnsupportedPlatform(err):
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Unsupported platform", Error: dto.ErrorDetail{Code: "UNSUPPORTED_PLATFORM"}})
	case businessflow.IsAmountTooLow(err):
		return c.Status(fiber.StatusBadRequest).
			JSON(dto.APIResponse{Success: false, Message: "Amount too low", Error: dto.ErrorDetail{Code: "AMOUNT_TOO_LOW"}})
	default:
		return c.Status(fiber.StatusInternalServerError).
			JSON(dto.APIResponse{Success: false, Message: "Crypto payment operation failed", Error: dto.ErrorDetail{Code: "CRYPTO_OPERATION_FAILED", Details: err.Error()}})
	}
}
