// Package businessflow contains the core business logic and use cases for payment workflows
package businessflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/image/draw"
	"gorm.io/gorm"
)

// PaymentFlow handles the complete payment business logic
type PaymentFlow interface {
	ChargeWallet(ctx context.Context, req *dto.ChargeWalletRequest, metadata *ClientMetadata) (*dto.ChargeWalletResponse, error)
	PaymentCallback(ctx context.Context, callback *dto.AtipayRequest, metadata *ClientMetadata) (string, error)
	GetTransactionHistory(
		ctx context.Context,
		req *dto.GetTransactionHistoryRequest,
		metadata *ClientMetadata,
	) (*dto.TransactionHistoryResponse, error)
	GetWalletBalance(ctx context.Context, req *dto.GetWalletBalanceRequest, metadata *ClientMetadata) (*dto.GetWalletBalanceResponse, error)
	SubmitDepositReceipt(
		ctx context.Context,
		req *dto.SubmitDepositReceiptRequest,
		metadata *ClientMetadata,
	) (*dto.SubmitDepositReceiptResponse, error)
	ListDepositReceipts(ctx context.Context, customerID uint, lang string) (*dto.ListDepositReceiptsResponse, error)
	PreviewProformaInvoice(ctx context.Context, customerID uint, receiptUUID string, lang string) (*dto.ProformaPreviewResponse, error)
	PreviewProformaInvoiceByAmount(
		ctx context.Context,
		customerID uint,
		amountWithTax uint64,
		lang string,
	) (*dto.ProformaPreviewResponse, error)
	DownloadDepositReceiptFile(ctx context.Context, customerID uint, receiptUUID string) ([]byte, string, string, error)
	UpdateDepositReceiptFile(ctx context.Context, customerID uint, receiptUUID string, req *dto.UpdateDepositReceiptFileRequest) error
	DeleteDepositReceiptFile(ctx context.Context, customerID uint, receiptUUID string) error
	NotifyInvoiceIssueRequest(
		ctx context.Context,
		req *dto.NotifyInvoiceIssueRequest,
		customerID uint,
		metadata *ClientMetadata,
	) (*dto.NotifyInvoiceIssueResponse, error)
}

// PaymentFlowImpl implements the payment business flow
type PaymentFlowImpl struct {
	paymentRequestRepo  repository.PaymentRequestRepository
	walletRepo          repository.WalletRepository
	customerRepo        repository.CustomerRepository
	campaignRepo        repository.CampaignRepository
	auditRepo           repository.AuditLogRepository
	balanceSnapshotRepo repository.BalanceSnapshotRepository
	transactionRepo     repository.TransactionRepository
	agencyDiscountRepo  repository.AgencyDiscountRepository
	depositReceiptRepo  repository.DepositReceiptRepository
	multimediaRepo      repository.MultimediaAssetRepository
	notifier            services.SMSService
	adminCfg            config.AdminConfig
	messageCfg          config.MessageConfig
	cacheCfg            config.CacheConfig
	rc                  *redis.Client
	db                  *gorm.DB

	// Atipay configuration
	atipayCfg     config.AtipayConfig
	sysCfg        config.SystemConfig
	deploymentCfg config.DeploymentConfig
}

// NewPaymentFlow creates a new payment flow instance
func NewPaymentFlow(
	paymentRequestRepo repository.PaymentRequestRepository,
	walletRepo repository.WalletRepository,
	customerRepo repository.CustomerRepository,
	campaignRepo repository.CampaignRepository,
	auditRepo repository.AuditLogRepository,
	balanceSnapshotRepo repository.BalanceSnapshotRepository,
	transactionRepo repository.TransactionRepository,
	agencyDiscountRepo repository.AgencyDiscountRepository,
	depositReceiptRepo repository.DepositReceiptRepository,
	multimediaRepo repository.MultimediaAssetRepository,
	notifier services.SMSService,
	adminCfg config.AdminConfig,
	messageCfg config.MessageConfig,
	cacheCfg config.CacheConfig,
	rc *redis.Client,
	db *gorm.DB,
	atipayCfg config.AtipayConfig,
	sysCfg config.SystemConfig,
	deploymentCfg config.DeploymentConfig,
) PaymentFlow {
	return &PaymentFlowImpl{
		paymentRequestRepo:  paymentRequestRepo,
		walletRepo:          walletRepo,
		customerRepo:        customerRepo,
		campaignRepo:        campaignRepo,
		auditRepo:           auditRepo,
		balanceSnapshotRepo: balanceSnapshotRepo,
		transactionRepo:     transactionRepo,
		agencyDiscountRepo:  agencyDiscountRepo,
		depositReceiptRepo:  depositReceiptRepo,
		multimediaRepo:      multimediaRepo,
		notifier:            notifier,
		adminCfg:            adminCfg,
		messageCfg:          messageCfg,
		cacheCfg:            cacheCfg,
		rc:                  rc,
		db:                  db,
		atipayCfg:           atipayCfg,
		sysCfg:              sysCfg,
		deploymentCfg:       deploymentCfg,
	}
}

// ChargeWallet handles the complete process of charging a wallet
func (p *PaymentFlowImpl) ChargeWallet(
	ctx context.Context,
	req *dto.ChargeWalletRequest,
	metadata *ClientMetadata,
) (*dto.ChargeWalletResponse, error) {
	var customer models.Customer
	var paymentRequest *models.PaymentRequest
	var atipayToken string

	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		var err error
		customer, err = getCustomer(txCtx, p.customerRepo, req.CustomerID)
		if err != nil {
			return err
		}

		// Admin customer numbers can bypass the public wallet charge amount restrictions.
		if err := p.validateChargeWalletRequest(req, customer.RepresentativeMobile); err != nil {
			return err
		}

		// Check if customer has a wallet, create one if it doesn't exist
		wallet, err := p.walletRepo.ByCustomerID(txCtx, customer.ID)
		if err != nil {
			return err
		}
		// Update customer with wallet reference
		customer.Wallet = wallet

		// Create payment request
		paymentRequest, err = p.createPaymentRequest(txCtx, customer, req.AmountWithTax, req.Lang)
		if err != nil {
			return err
		}

		atipayToken, err = p.callAtipayGetToken(txCtx, customer, *paymentRequest)
		if err != nil {
			return err
		}

		// Update payment request with Atipay token
		// State: Tokenized -> Pending
		paymentRequest.AtipayToken = atipayToken
		paymentRequest.AtipayStatus = "OK"
		paymentRequest.Status = models.PaymentRequestStatusTokenized
		paymentRequest.StatusReason = "payment request tokenized successfully"
		paymentRequest.UpdatedAt = utils.UTCNow()
		if err := p.paymentRequestRepo.Update(txCtx, paymentRequest); err != nil {
			return err
		}

		paymentRequest.Status = models.PaymentRequestStatusPending
		paymentRequest.StatusReason = "payment request pending"
		paymentRequest.UpdatedAt = utils.UTCNow()
		if err := p.paymentRequestRepo.Update(txCtx, paymentRequest); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		errMsg := fmt.Sprintf("Charge wallet failed for customer %d: %s", customer.ID, err.Error())
		_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionWalletChargeFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("CHARGE_WALLET_FAILED", "Failed to charge wallet", err)
	}

	// Create success audit log
	msg := fmt.Sprintf("Generated payment token for payment request %d for customer %d", paymentRequest.ID, customer.ID)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionWalletChargeCompleted, msg, true, nil, metadata)

	// Build resp
	resp := &dto.ChargeWalletResponse{
		Message: "Generated payment token successfully",
		Success: true,
		Token:   atipayToken,
	}

	return resp, nil
}

// validateChargeWalletRequest validates the business rules for charging a wallet.
func (p *PaymentFlowImpl) validateChargeWalletRequest(req *dto.ChargeWalletRequest, representativeMobile string) error {
	req.Lang = strings.ToUpper(strings.TrimSpace(req.Lang))
	if req.Lang == "" {
		req.Lang = "EN"
	}
	if req.Lang != "EN" && req.Lang != "FA" {
		return ErrInvalidLanguage
	}

	if p.adminCfg.HasMobile(representativeMobile) {
		return nil
	}

	// Validate amount (minimum 1000 Tomans and must be multiple of 1000)
	if req.AmountWithTax < 1000 {
		return ErrAmountTooLow
	}
	if req.AmountWithTax%1000 != 0 {
		return ErrAmountNotMultiple
	}

	return nil
}

// createPaymentRequest creates a new payment request record
func (p *PaymentFlowImpl) createPaymentRequest(
	ctx context.Context,
	customer models.Customer,
	amountWithTax uint64,
	lang string,
) (*models.PaymentRequest, error) {
	if customer.ReferrerAgencyID == nil {
		return nil, ErrReferrerAgencyIDRequired
	}

	// Generate unique invoice number
	invoiceNumber := fmt.Sprintf("INV-%s", uuid.New().String())

	// Set expiration time (30 minutes from now)
	expiresAt := time.Now().Add(30 * time.Minute)

	agencyDiscount, err := p.agencyDiscountRepo.GetActiveDiscount(ctx, *customer.ReferrerAgencyID, customer.ID)
	if err != nil {
		return nil, err
	}
	if agencyDiscount == nil {
		return nil, ErrAgencyDiscountNotFound
	}

	scatteredSettlementItems, err := p.calculateScatteredSettlementItems(ctx, customer, amountWithTax)
	if err != nil {
		return nil, err
	}

	metadata, _ := json.Marshal(map[string]any{
		"source":                "wallet_recharge",
		"amount_with_tax":       amountWithTax,
		"system_share_with_tax": scatteredSettlementItems[0].Amount,
		"agency_share_with_tax": scatteredSettlementItems[1].Amount,
		"agency_discount_id":    agencyDiscount.ID,
		"agency_id":             customer.ReferrerAgencyID,
		"customer_id":           customer.ID,
		"payment_channel":       "atipay",
	})

	// Create payment request
	paymentRequest := &models.PaymentRequest{
		UUID:          uuid.New(),
		CorrelationID: uuid.New(),
		CustomerID:    customer.ID,
		WalletID:      customer.Wallet.ID,
		Amount:        amountWithTax,
		Currency:      utils.TomanCurrency,
		Description:   "charge wallet",
		Lang:          lang,
		InvoiceNumber: invoiceNumber,
		CellNumber:    customer.RepresentativeMobile,
		RedirectURL:   fmt.Sprintf("https://%s/api/v1/payments/callback/%s", p.deploymentCfg.Domain, invoiceNumber),
		AtipayToken:   "", // Will be set later
		AtipayStatus:  "", // Will be set later
		// Payment*: "",   // Will be set later
		Status:       models.PaymentRequestStatusCreated,
		StatusReason: "payment request created for wallet charge",
		ExpiresAt:    &expiresAt,
		Metadata:     json.RawMessage(metadata),
	}
	if err := p.paymentRequestRepo.Save(ctx, paymentRequest); err != nil {
		return nil, err
	}

	return paymentRequest, nil
}

type ScatteredSettlementItem struct {
	Amount uint64 `json:"amount"`
	IBAN   string `json:"iban"`
}

func (p *PaymentFlowImpl) calculateScatteredSettlementItems(
	ctx context.Context,
	customer models.Customer,
	amountWithTax uint64,
) ([]ScatteredSettlementItem, error) {
	// Default shares fallback (50/50) if no discount found
	var systemShareWithTax uint64
	var agencyShareWithTax uint64

	discountRate, shebaNumber, err := p.getAgencyDiscountAndIBAN(ctx, customer)
	if err != nil {
		return nil, err
	}

	x := float64(amountWithTax) / (1 - discountRate)
	systemShareWithTax = uint64(x / 2)
	agencyShareWithTax = uint64(amountWithTax - systemShareWithTax)

	scatteredSettlementItems := make([]ScatteredSettlementItem, 0, 2)

	//--------------------------------
	// NOTE: ORDER MATTERS
	// --------------------------------

	systemUser, err := getSystemUser(ctx, p.customerRepo, p.walletRepo, p.sysCfg)
	if err != nil {
		return nil, err
	}
	if systemUser.ShebaNumber == nil {
		return nil, ErrSystemUserShebaNumberNotFound
	}

	scatteredSettlementItems = append(scatteredSettlementItems, ScatteredSettlementItem{
		Amount: systemShareWithTax,
		IBAN:   *systemUser.ShebaNumber,
	})
	scatteredSettlementItems = append(scatteredSettlementItems, ScatteredSettlementItem{
		Amount: agencyShareWithTax,
		IBAN:   shebaNumber,
	})

	return scatteredSettlementItems, nil
}

func (p *PaymentFlowImpl) getAgencyDiscountAndIBAN(ctx context.Context, customer models.Customer) (float64, string, error) {
	// Determine discount and agency IBAN if referrer exists
	var discountRate float64
	var shebaNumber string

	agency, err := getAgency(ctx, p.customerRepo, *customer.ReferrerAgencyID)
	if err != nil {
		return 0, "", err
	}

	shebaNumber, err = ValidateShebaNumber(agency.ShebaNumber)
	if err != nil {
		return 0, "", err
	}

	ad, err := p.agencyDiscountRepo.GetActiveDiscount(ctx, agency.ID, customer.ID)
	if err != nil {
		return 0, "", err
	}

	if ad != nil {
		discountRate = ad.DiscountRate
	}

	return discountRate, shebaNumber, nil
}

// callAtipayGetToken calls Atipay's get-token API
func (p *PaymentFlowImpl) callAtipayGetToken(
	ctx context.Context,
	customer models.Customer,
	paymentRequest models.PaymentRequest,
) (string, error) {
	scatteredSettlementItems, err := p.calculateScatteredSettlementItems(ctx, customer, paymentRequest.Amount)
	if err != nil {
		return "", err
	}
	amountWithTaxIRR := paymentRequest.Amount * 10 // TO IRR
	scatteredSettlementItems[0].Amount *= 10       // TO IRR
	scatteredSettlementItems[1].Amount *= 10       // TO IRR

	refinedScatteredSettlementItems := make([]ScatteredSettlementItem, 0)
	for _, item := range scatteredSettlementItems {
		if item.Amount > 0 {
			refinedScatteredSettlementItems = append(refinedScatteredSettlementItems, item)
		}
	}

	// Merge scatteredSettlementItems with same sheba number (IBAN)
	for i := 0; i < len(refinedScatteredSettlementItems); i++ {
		for j := i + 1; j < len(refinedScatteredSettlementItems); j++ {
			if refinedScatteredSettlementItems[i].IBAN == refinedScatteredSettlementItems[j].IBAN {
				refinedScatteredSettlementItems[i].Amount += refinedScatteredSettlementItems[j].Amount
				refinedScatteredSettlementItems = append(refinedScatteredSettlementItems[:j], refinedScatteredSettlementItems[j+1:]...)
			}
		}
	}

	// Prepare Atipay request payload
	atipayPayload := map[string]any{
		"amount":        amountWithTaxIRR,
		"cellNumber":    paymentRequest.CellNumber,
		"description":   paymentRequest.Description,
		"invoiceNumber": paymentRequest.InvoiceNumber,
		"redirectUrl":   paymentRequest.RedirectURL,
		"apiKey":        p.atipayCfg.APIKey,
		"terminal":      p.atipayCfg.Terminal,
	}

	systemUser, err := getSystemUser(ctx, p.customerRepo, p.walletRepo, p.sysCfg)
	if err != nil {
		return "", err
	}
	if systemUser.ShebaNumber == nil {
		return "", ErrSystemUserShebaNumberNotFound
	}

	// if len(refinedScatteredSettlementItems) > 1 ||
	// 	(len(refinedScatteredSettlementItems) == 1 && refinedScatteredSettlementItems[0].IBAN != *systemUser.ShebaNumber) {
	// 	atipayPayload["scatteredSettlementItems"] = refinedScatteredSettlementItems
	// }
	atipayPayload["scatteredSettlementItems"] = refinedScatteredSettlementItems

	// Convert to JSON
	payloadBytes, err := json.Marshal(atipayPayload)
	if err != nil {
		return "", err
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://mipg.atipay.net/v1/get-token", bytes.NewReader(payloadBytes))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Make HTTP request
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("atipay API returned non-OK status: %d", resp.StatusCode)
	}

	// Parse response body
	var atipayResponse struct {
		Status           string `json:"status"`
		Token            string `json:"token"`
		Message          string `json:"message,omitempty"`
		ParsiMessage     string `json:"faMessage,omitempty"`
		ErrorCode        string `json:"errorCode,omitempty"`
		ErrorDescription string `json:"errorDescription,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&atipayResponse); err != nil {
		return "", err
	}

	// Check for errors in response
	if atipayResponse.Status != "1" {
		errorMsg := "unknown error"
		if atipayResponse.Message != "" {
			errorMsg = atipayResponse.Message
		}
		if atipayResponse.ErrorCode != "" {
			errorMsg = fmt.Sprintf("%s (code: %s)", errorMsg, atipayResponse.ErrorCode)
		}
		if atipayResponse.ErrorDescription != "" {
			errorMsg = fmt.Sprintf("%s (description: %s)", errorMsg, atipayResponse.ErrorDescription)
		}
		if atipayResponse.ParsiMessage != "" {
			errorMsg = fmt.Sprintf("%s (persian message: %s)", errorMsg, atipayResponse.ParsiMessage)
		}
		return "", fmt.Errorf("atipay API error: %s", errorMsg)
	}

	// Validate token
	if atipayResponse.Token == "" {
		return "", ErrAtipayTokenEmpty
	}

	return atipayResponse.Token, nil
}

// PaymentCallback handles the callback from Atipay after payment completion
func (p *PaymentFlowImpl) PaymentCallback(ctx context.Context, atipayRequest *dto.AtipayRequest, metadata *ClientMetadata) (string, error) {
	// Validate callback data
	if err := p.validateCallbackRequest(atipayRequest); err != nil {
		return "", NewBusinessError("PAYMENT_CALLBACK_VALIDATION_FAILED", "Payment callback validation failed", err)
	}

	var customer models.Customer
	var paymentRequest *models.PaymentRequest
	var mapping PaymentStatusMapping

	// Process callback within transaction
	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		var err error

		// Find the payment request by reservation number (our invoice number)
		paymentRequest, err = p.paymentRequestRepo.ByInvoiceNumber(txCtx, atipayRequest.ReservationNumber)
		if err != nil {
			return err
		}
		if paymentRequest == nil {
			return ErrPaymentRequestNotFound
		}

		customer, err = getCustomer(txCtx, p.customerRepo, paymentRequest.CustomerID)
		if err != nil {
			return err
		}

		// Check if this callback has already been processed
		if paymentRequest.Status != models.PaymentRequestStatusPending {
			return ErrPaymentRequestAlreadyProcessed
		}

		if paymentRequest.ExpiresAt != nil && paymentRequest.ExpiresAt.Before(utils.UTCNow()) {
			return ErrPaymentRequestExpired
		}

		// Determine payment status based on callback
		mapping = p.getPaymentStatusMapping(atipayRequest.Status, atipayRequest.State)

		// Update payment request with callback data
		if err := p.updatePaymentRequest(txCtx, paymentRequest, atipayRequest, mapping); err != nil {
			return err
		}

		// If payment was successful, increase customer balance
		if mapping.Success {
			// Verify payment with Atipay before finalizing
			verificationResult, err := p.verifyPaymentWithAtipay(txCtx, atipayRequest.ReferenceNumber)
			if err != nil {
				// Failed but don't return error to avoid rollback
				mapping.Status = models.PaymentRequestStatusFailed
				mapping.Success = false
				mapping.Message = "Payment verification failed (step 1)"
				mapping.Description = "Payment verification failed (step 1): " + err.Error()

				// Update payment request status
				if err := p.updatePaymentRequest(txCtx, paymentRequest, atipayRequest, mapping); err != nil {
					return err
				}

				// Create audit log for verification failure
				errMsg := fmt.Sprintf("Payment verification failed (step 1) for payment request %d: %s", paymentRequest.ID, err.Error())
				_ = createAuditLog(
					txCtx,
					p.auditRepo,
					&customer,
					models.AuditActionPaymentFailed,
					mapping.Description,
					false,
					&errMsg,
					metadata,
				)

				return nil
			}

			// Check if verified amount matches the original amount
			if uint64(verificationResult.AmountIRR) != paymentRequest.Amount*10 { // Convert Tomans to Rials
				// Amount mismatch - mark payment as failed and refund will occur
				mapping.Status = models.PaymentRequestStatusFailed
				mapping.Success = false
				mapping.Message = "Payment verification failed (step 2): amount mismatch"
				mapping.Description = fmt.Sprintf("Verified amount (%f Rials) does not match original amount (%d Rials)",
					verificationResult.AmountIRR, paymentRequest.Amount*10)

				// Update payment request status
				if err := p.updatePaymentRequest(txCtx, paymentRequest, atipayRequest, mapping); err != nil {
					return err
				}

				// Create audit log for verification failure
				errMsg := fmt.Sprintf(
					"Payment verification failed (step 2) for payment request %d: amount mismatch (verified: %f Rials, original: %d Rials)",
					paymentRequest.ID,
					verificationResult.AmountIRR,
					paymentRequest.Amount*10,
				)
				_ = createAuditLog(
					txCtx,
					p.auditRepo,
					&customer,
					models.AuditActionPaymentFailed,
					mapping.Description,
					false,
					&errMsg,
					metadata,
				)

				return nil
			}

			// Verification successful - proceed with balance increase
			if err := p.updateBalances(txCtx, paymentRequest, atipayRequest); err != nil {
				// Failed but don't return error to avoid rollback
				mapping.Status = models.PaymentRequestStatusFailed
				mapping.Success = false
				mapping.Message = "Increase customer balance failed (step 3)"
				mapping.Description = "Increase customer balance failed (step 3): " + err.Error()

				// Update payment request status
				if err := p.updatePaymentRequest(txCtx, paymentRequest, atipayRequest, mapping); err != nil {
					return err
				}

				// Create audit log for increase customer balance failure
				errMsg := fmt.Sprintf(
					"Increase customer balance failed (step 3) for payment request %d: %s",
					paymentRequest.ID,
					err.Error(),
				)
				_ = createAuditLog(
					txCtx,
					p.auditRepo,
					&customer,
					models.AuditActionPaymentFailed,
					mapping.Description,
					false,
					&errMsg,
					metadata,
				)

				return nil
			}

			// Create audit log for successful balance increase
			msg := fmt.Sprintf("Wallet balance increased for payment request %d", paymentRequest.ID)
			_ = createAuditLog(txCtx, p.auditRepo, &customer, models.AuditActionWalletChargeCompleted, msg, true, nil, metadata)
		}

		return nil
	})

	paymentRequestID := uint(0)
	if paymentRequest != nil {
		paymentRequestID = paymentRequest.ID
	}
	if err != nil {
		errMsg := fmt.Sprintf("Payment callback failed for payment request %d: %s", paymentRequestID, err.Error())
		_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionPaymentCallbackProcessed, errMsg, false, &errMsg, metadata)

		return "", NewBusinessError("PAYMENT_CALLBACK_FAILED", "Payment callback failed", err)
	}

	msg := fmt.Sprintf("Payment callback processed for payment request %d", paymentRequestID)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionPaymentCallbackProcessed, msg, true, nil, metadata)

	// Generate HTML response based on payment status
	htmlResponse, err := p.generatePaymentResultHTML(ctx, paymentRequest, atipayRequest, mapping)
	if err != nil {
		return "", NewBusinessError("PAYMENT_CALLBACK_HTML_GENERATION_FAILED", "Failed to generate HTML response", err)
	}

	return htmlResponse, nil
}

// validateCallbackRequest validates the callback request from Atipay
func (p *PaymentFlowImpl) validateCallbackRequest(callback *dto.AtipayRequest) error {
	if callback == nil {
		return ErrCallbackRequestNil
	}
	if callback.ReservationNumber == "" {
		return ErrReservationNumberRequired
	}
	if callback.Status == "" {
		return ErrStatusRequired
	}
	if callback.State == "" {
		return ErrStateRequired
	}
	if mapping := p.getPaymentStatusMapping(callback.Status, callback.State); mapping.Success && callback.ReferenceNumber == "" {
		return ErrReferenceNumberRequired
	}
	return nil
}

// PaymentStatusMapping maps Atipay status codes to our payment statuses
type PaymentStatusMapping struct {
	Status      models.PaymentRequestStatus
	Success     bool
	Message     string
	Description string
}

// atipayStatusMappings defines the mapping from Atipay status/state to our payment statuses
var atipayStatusMappings = map[string]PaymentStatusMapping{
	"2_OK": {
		Status:      models.PaymentRequestStatusCompleted,
		Success:     true,
		Message:     "Payment completed successfully",
		Description: "Payment completed successfully via Atipay",
	},
	"1_CanceledByUser": {
		Status:      models.PaymentRequestStatusCancelled,
		Success:     false,
		Message:     "Payment cancelled by customer",
		Description: "Payment cancelled by user via Atipay",
	},
	"3_Failed": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Payment failed",
		Description: "Payment failed via Atipay",
	},
	"4_SessionIsNull": {
		Status:      models.PaymentRequestStatusExpired,
		Success:     false,
		Message:     "Payment session expired",
		Description: "Payment session expired via Atipay",
	},
	"5_InvalidParameters": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Invalid payment parameters",
		Description: "Invalid payment parameters via Atipay",
	},
	"8_MerchantIpAddressIsInvalid": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Merchant IP address invalid",
		Description: "Merchant IP address invalid via Atipay",
	},
	"10_TokenNotFound": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Payment token not found",
		Description: "Payment token not found via Atipay",
	},
	"11_TokenRequired": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Payment token required",
		Description: "Payment token required via Atipay",
	},
	"12_TerminalNotFound": {
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     "Payment terminal not found",
		Description: "Payment terminal not found via Atipay",
	},
}

// getPaymentStatusMapping determines the payment status based on Atipay callback data
func (p *PaymentFlowImpl) getPaymentStatusMapping(status, state string) PaymentStatusMapping {
	// Try exact match first
	key := fmt.Sprintf("%s_%s", status, state)
	if mapping, exists := atipayStatusMappings[key]; exists {
		return mapping
	}

	// Try status-only match for some cases
	if mapping, exists := atipayStatusMappings[fmt.Sprintf("%s_", status)]; exists {
		return mapping
	}

	// Default to failed with unknown status
	return PaymentStatusMapping{
		Status:      models.PaymentRequestStatusFailed,
		Success:     false,
		Message:     fmt.Sprintf("Unknown payment status: %s, state: %s", status, state),
		Description: fmt.Sprintf("Unknown payment status: %s, state: %s via Atipay", status, state),
	}
}

// updatePaymentRequest updates the payment request with callback data
func (p *PaymentFlowImpl) updatePaymentRequest(
	ctx context.Context,
	paymentRequest *models.PaymentRequest,
	atipayRequest *dto.AtipayRequest,
	mapping PaymentStatusMapping,
) error {
	paymentRequest.Status = mapping.Status
	paymentRequest.StatusReason = mapping.Description
	paymentRequest.UpdatedAt = utils.UTCNow()

	// Only update payment details for successful payments
	if mapping.Success {
		paymentRequest.PaymentReference = atipayRequest.ReferenceNumber
		paymentRequest.PaymentReservation = atipayRequest.ReservationNumber
		paymentRequest.PaymentTerminal = atipayRequest.TerminalID
		paymentRequest.PaymentTrace = atipayRequest.TraceNumber
		paymentRequest.PaymentMaskedPAN = atipayRequest.MaskedPAN
		paymentRequest.PaymentRRN = atipayRequest.RRN
		paymentRequest.PaymentState = atipayRequest.State
		paymentRequest.PaymentStatus = atipayRequest.Status
	}

	// Save updated payment request
	if err := p.paymentRequestRepo.Update(ctx, paymentRequest); err != nil {
		return err
	}

	return nil
}

func (p *PaymentFlowImpl) updateBalances(
	ctx context.Context,
	paymentRequest *models.PaymentRequest,
	atipayRequest *dto.AtipayRequest,
) error {
	_, err := getCustomer(ctx, p.customerRepo, paymentRequest.CustomerID)
	if err != nil {
		return err
	}

	// unmarshal metadata
	var m map[string]any
	if err := json.Unmarshal(paymentRequest.Metadata, &m); err != nil {
		return err
	}

	realWithTax := uint64(m["amount_with_tax"].(float64))
	systemShareWithTax := uint64(m["system_share_with_tax"].(float64))
	agencyShareWithTax := uint64(m["agency_share_with_tax"].(float64))
	agencyDiscountID := uint(m["agency_discount_id"].(float64))
	agencyID := uint(m["agency_id"].(float64))

	agencyWallet, err := getWallet(ctx, p.walletRepo, agencyID)
	if err != nil {
		return err
	}

	// Get tax wallet
	taxWallet, err := getTaxWallet(ctx, p.walletRepo, p.sysCfg)
	if err != nil {
		return err
	}

	systemWallet, err := getSystemWallet(ctx, p.walletRepo, p.sysCfg)
	if err != nil {
		return err
	}

	// Get current balance snapshot for customer wallet
	customerBalance, err := getLatestBalanceSnapshot(ctx, p.walletRepo, paymentRequest.WalletID)
	if err != nil {
		return err
	}

	agencyBalance, err := getLatestBalanceSnapshot(ctx, p.walletRepo, agencyWallet.ID)
	if err != nil {
		return err
	}

	// Get current tax wallet balance
	taxBalance, err := getLatestTaxWalletBalanceSnapshot(ctx, p.walletRepo, taxWallet.ID)
	if err != nil {
		return err
	}

	// Get current system wallet balance
	systemBalance, err := getLatestSystemWalletBalanceSnapshot(ctx, p.walletRepo, systemWallet.ID)
	if err != nil {
		return err
	}

	agencyDiscount, err := p.agencyDiscountRepo.ByID(ctx, agencyDiscountID)
	if err != nil {
		return err
	}
	if agencyDiscount == nil {
		return ErrAgencyDiscountNotFound
	}

	real := uint64(realWithTax * 10 / 11)
	tax := realWithTax - real
	realSystemShare := uint64(systemShareWithTax * 10 / 11)
	taxSystemShare := systemShareWithTax - realSystemShare
	realAgencyShare := uint64(agencyShareWithTax * 10 / 11)
	taxAgencyShare := agencyShareWithTax - realAgencyShare
	customerCredit := uint64(float64(real)/(1-agencyDiscount.DiscountRate)) - real

	metadata := map[string]any{
		"customer_id":           paymentRequest.CustomerID,
		"agency_id":             agencyID,
		"agency_discount_id":    agencyDiscountID,
		"source":                "payment_callback",
		"operation":             "increase_balance",
		"payment_request_id":    paymentRequest.ID,
		"amount_with_tax":       realWithTax,
		"amount":                real,
		"tax":                   tax,
		"customer_credit":       customerCredit,
		"system_share_with_tax": systemShareWithTax,
		"system_share":          realSystemShare,
		"system_share_tax":      taxSystemShare,
		"agency_share_with_tax": agencyShareWithTax,
		"agency_share":          realAgencyShare,
		"agency_share_tax":      taxAgencyShare,
		"atipay_response":       atipayRequest,
		"payment_channel":       m["payment_channel"],
	}
	if source, ok := m["source"]; ok {
		metadata["payment_request_source"] = source
	}
	if adminID, ok := m["admin_id"]; ok {
		metadata["admin_id"] = adminID
		metadata["payment_channel"] = "admin_direct_charge"
	}
	if customerInvoiceUUID, ok := m["customer_invoice_uuid"]; ok {
		metadata["customer_invoice_uuid"] = customerInvoiceUUID
	}

	// Update customer wallet balance
	newCustomerFreeBalance := customerBalance.FreeBalance + real
	newCustomerCreditBalance := customerBalance.CreditBalance + customerCredit
	metadata["source"] = models.TransactionSourceIncreaseCustomerFreePlusCredit
	metadata["operation"] = "increase_customer_free_plus_credit"
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	newCustomerBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      paymentRequest.CorrelationID,
		WalletID:           paymentRequest.WalletID,
		CustomerID:         paymentRequest.CustomerID,
		FreeBalance:        newCustomerFreeBalance,
		FrozenBalance:      customerBalance.FrozenBalance,
		LockedBalance:      customerBalance.LockedBalance,
		CreditBalance:      newCustomerCreditBalance,
		SpentOnCampaign:    customerBalance.SpentOnCampaign,
		AgencyShareWithTax: customerBalance.AgencyShareWithTax,
		TotalBalance:       newCustomerFreeBalance + newCustomerCreditBalance + customerBalance.FrozenBalance + customerBalance.LockedBalance + customerBalance.SpentOnCampaign + customerBalance.AgencyShareWithTax,
		Reason:             "wallet_recharge",
		Description:        fmt.Sprintf("Wallet recharged via Atipay (payment request %d)", paymentRequest.ID),
		Metadata:           metadataJSON,
	}
	if err := p.balanceSnapshotRepo.Save(ctx, newCustomerBS); err != nil {
		return err
	}

	// Create transaction record for customer wallet
	customerBalanceBefore, err := customerBalance.GetBalanceMap()
	if err != nil {
		return err
	}
	customerBalanceAfter, err := newCustomerBS.GetBalanceMap()
	if err != nil {
		return err
	}
	customerDepositTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     paymentRequest.CorrelationID,
		Type:              models.TransactionTypeDeposit,
		Status:            models.TransactionStatusCompleted,
		Amount:            real + customerCredit,
		Currency:          utils.TomanCurrency,
		WalletID:          paymentRequest.WalletID,
		CustomerID:        paymentRequest.CustomerID,
		BalanceBefore:     customerBalanceBefore,
		BalanceAfter:      customerBalanceAfter,
		ExternalReference: atipayRequest.ReferenceNumber,
		ExternalTrace:     atipayRequest.TraceNumber,
		ExternalRRN:       atipayRequest.RRN,
		ExternalMaskedPAN: atipayRequest.MaskedPAN,
		Description:       fmt.Sprintf("Wallet recharge (payment request %d)", paymentRequest.ID),
		Metadata:          metadataJSON,
	}
	if err := p.transactionRepo.Save(ctx, customerDepositTx); err != nil {
		return err
	}

	// Update agency wallet balance
	newAgencyShareWithTax := agencyBalance.AgencyShareWithTax + agencyShareWithTax
	metadata["source"] = models.TransactionSourceIncreaseAgencyShareWithTax
	metadata["operation"] = "increase_agency_share_with_tax"
	metadataJSON, err = json.Marshal(metadata)
	if err != nil {
		return err
	}
	newAgencyBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      paymentRequest.CorrelationID,
		WalletID:           agencyWallet.ID,
		CustomerID:         agencyWallet.CustomerID,
		FreeBalance:        agencyBalance.FreeBalance,
		FrozenBalance:      agencyBalance.FrozenBalance,
		LockedBalance:      agencyBalance.LockedBalance,
		CreditBalance:      agencyBalance.CreditBalance,
		SpentOnCampaign:    agencyBalance.SpentOnCampaign,
		AgencyShareWithTax: newAgencyShareWithTax,
		TotalBalance:       agencyBalance.FreeBalance + agencyBalance.FrozenBalance + agencyBalance.LockedBalance + agencyBalance.CreditBalance + agencyBalance.SpentOnCampaign + newAgencyShareWithTax,
		Reason:             "agency_share_with_tax",
		Description:        fmt.Sprintf("Agency share for payment request %d", paymentRequest.ID),
		Metadata:           metadataJSON,
	}
	if err := p.balanceSnapshotRepo.Save(ctx, newAgencyBS); err != nil {
		return err
	}

	// Create transaction record for agency wallet
	agencyBalanceBefore, err := agencyBalance.GetBalanceMap()
	if err != nil {
		return err
	}
	agencyBalanceAfter, err := newAgencyBS.GetBalanceMap()
	if err != nil {
		return err
	}
	agencyChargeTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     paymentRequest.CorrelationID,
		Type:              models.TransactionTypeChargeAgencyShareWithTax,
		Status:            models.TransactionStatusCompleted,
		Amount:            agencyShareWithTax,
		Currency:          utils.TomanCurrency,
		WalletID:          agencyWallet.ID,
		CustomerID:        agencyWallet.CustomerID,
		BalanceBefore:     agencyBalanceBefore,
		BalanceAfter:      agencyBalanceAfter,
		ExternalReference: atipayRequest.ReferenceNumber,
		ExternalTrace:     atipayRequest.TraceNumber,
		ExternalRRN:       atipayRequest.RRN,
		ExternalMaskedPAN: atipayRequest.MaskedPAN,
		Description:       fmt.Sprintf("Agency share for payment request %d", paymentRequest.ID),
		Metadata:          metadataJSON,
	}
	if err := p.transactionRepo.Save(ctx, agencyChargeTx); err != nil {
		return err
	}

	// Update tax wallet balance
	newTaxLockedBalance := taxBalance.LockedBalance + taxSystemShare
	metadata["source"] = models.TransactionSourceIncreaseTaxSystemShare
	metadata["operation"] = "increase_tax_locked"
	metadataJSON, err = json.Marshal(metadata)
	if err != nil {
		return err
	}
	newTaxBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      paymentRequest.CorrelationID,
		WalletID:           taxWallet.ID,
		CustomerID:         taxWallet.CustomerID,
		FreeBalance:        taxBalance.FreeBalance,
		FrozenBalance:      taxBalance.FrozenBalance,
		LockedBalance:      newTaxLockedBalance,
		CreditBalance:      taxBalance.CreditBalance,
		SpentOnCampaign:    taxBalance.SpentOnCampaign,
		AgencyShareWithTax: taxBalance.AgencyShareWithTax,
		TotalBalance:       taxBalance.FreeBalance + taxBalance.FrozenBalance + newTaxLockedBalance + taxBalance.CreditBalance + taxBalance.SpentOnCampaign + taxBalance.AgencyShareWithTax,
		Reason:             "tax_collection",
		Description:        fmt.Sprintf("Tax collection for payment request %d", paymentRequest.ID),
		Metadata:           metadataJSON,
	}
	if err := p.balanceSnapshotRepo.Save(ctx, newTaxBS); err != nil {
		return err
	}

	// Create transaction record for tax wallet
	taxBalanceBefore, err := taxBalance.GetBalanceMap()
	if err != nil {
		return err
	}
	taxBalanceAfter, err := newTaxBS.GetBalanceMap()
	if err != nil {
		return err
	}
	taxLockTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     paymentRequest.CorrelationID,
		Type:              models.TransactionTypeLock,
		Status:            models.TransactionStatusCompleted,
		Amount:            taxSystemShare,
		Currency:          utils.TomanCurrency,
		WalletID:          taxWallet.ID,
		CustomerID:        taxWallet.CustomerID,
		BalanceBefore:     taxBalanceBefore,
		BalanceAfter:      taxBalanceAfter,
		ExternalReference: atipayRequest.ReferenceNumber,
		ExternalTrace:     atipayRequest.TraceNumber,
		ExternalRRN:       atipayRequest.RRN,
		ExternalMaskedPAN: atipayRequest.MaskedPAN,
		Description:       fmt.Sprintf("Tax collection for payment request %d", paymentRequest.ID),
		Metadata:          metadataJSON,
	}
	if err := p.transactionRepo.Save(ctx, taxLockTx); err != nil {
		return err
	}

	// Update system wallet balance
	newSystemLockedBalance := systemBalance.LockedBalance + realSystemShare
	metadata["source"] = models.TransactionSourceIncreaseRealSystemShare
	metadata["operation"] = "increase_system_locked"
	metadataJSON, err = json.Marshal(metadata)
	if err != nil {
		return err
	}
	newSystemBalanceSnapshot := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      paymentRequest.CorrelationID,
		WalletID:           systemWallet.ID,
		CustomerID:         systemWallet.CustomerID,
		FreeBalance:        systemBalance.FreeBalance,
		FrozenBalance:      systemBalance.FrozenBalance,
		LockedBalance:      newSystemLockedBalance,
		CreditBalance:      systemBalance.CreditBalance,
		SpentOnCampaign:    systemBalance.SpentOnCampaign,
		AgencyShareWithTax: systemBalance.AgencyShareWithTax,
		TotalBalance:       systemBalance.FreeBalance + systemBalance.FrozenBalance + newSystemLockedBalance + systemBalance.CreditBalance + systemBalance.SpentOnCampaign + systemBalance.AgencyShareWithTax,
		Reason:             "real_system_share",
		Description:        fmt.Sprintf("System share for payment request %d", paymentRequest.ID),
		Metadata:           metadataJSON,
	}
	if err := p.balanceSnapshotRepo.Save(ctx, newSystemBalanceSnapshot); err != nil {
		return err
	}

	// Create transaction record for system wallet
	systemBalanceBefore, err := systemBalance.GetBalanceMap()
	if err != nil {
		return err
	}
	systemBalanceAfter, err := newSystemBalanceSnapshot.GetBalanceMap()
	if err != nil {
		return err
	}
	systemLockTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     paymentRequest.CorrelationID,
		Type:              models.TransactionTypeLock,
		Status:            models.TransactionStatusCompleted,
		Amount:            realSystemShare,
		Currency:          utils.TomanCurrency,
		WalletID:          systemWallet.ID,
		CustomerID:        systemWallet.CustomerID,
		BalanceBefore:     systemBalanceBefore,
		BalanceAfter:      systemBalanceAfter,
		ExternalReference: atipayRequest.ReferenceNumber,
		ExternalTrace:     atipayRequest.TraceNumber,
		ExternalRRN:       atipayRequest.RRN,
		ExternalMaskedPAN: atipayRequest.MaskedPAN,
		Description:       fmt.Sprintf("Real system share for payment request %d", paymentRequest.ID),
		Metadata:          metadataJSON,
	}
	if err := p.transactionRepo.Save(ctx, systemLockTx); err != nil {
		return err
	}

	return nil
}

// generatePaymentResultHTML generates HTML response based on payment status
func (p *PaymentFlowImpl) generatePaymentResultHTML(
	ctx context.Context,
	paymentRequest *models.PaymentRequest,
	atipayRequest *dto.AtipayRequest,
	mapping PaymentStatusMapping,
) (string, error) {
	// Read template files
	var templateContent string
	var err error
	lang := strings.ToUpper(strings.TrimSpace(paymentRequest.Lang))
	if lang == "" {
		lang = "EN"
	}

	if mapping.Success {
		filename := "templates/payment_success.html"
		if lang == "FA" {
			filename = "templates/payment_success_fa.html"
		}
		templateContent, err = p.readTemplate(filename)
	} else {
		filename := "templates/payment_failure.html"
		if lang == "FA" {
			filename = "templates/payment_failure_fa.html"
		}
		templateContent, err = p.readTemplate(filename)
	}

	if err != nil {
		return "", err
	}

	var m map[string]any
	if err := json.Unmarshal(paymentRequest.Metadata, &m); err != nil {
		return "", err
	}

	realWithTax := uint64(m["amount_with_tax"].(float64))
	agencyDiscountID := uint(m["agency_discount_id"].(float64))

	agencyDiscount, err := p.agencyDiscountRepo.ByID(ctx, agencyDiscountID)
	if err != nil {
		return "", err
	}
	if agencyDiscount == nil {
		return "", ErrAgencyDiscountNotFound
	}

	real := uint64(realWithTax * 10 / 11)
	tax := realWithTax - real
	customerCredit := uint64(float64(real)/(1-agencyDiscount.DiscountRate)) - real

	// Prepare template data
	data := map[string]any{
		"Status":          mapping.Status,
		"Message":         mapping.Message,
		"TotalAmount":     realWithTax,
		"TaxAmount":       tax,
		"NetAmount":       real,
		"CreditAmount":    customerCredit,
		"ReferenceNumber": atipayRequest.ReferenceNumber,
		"TraceNumber":     atipayRequest.TraceNumber,
		"RRN":             atipayRequest.RRN,
		"MaskedPAN":       atipayRequest.MaskedPAN,
		"ProcessedAt":     utils.UTCNow().Format("2006-01-02 15:04:05"),
	}

	// Simple template replacement (you could use a proper template engine like html/template)
	html := templateContent
	for key, value := range data {
		if value != nil {
			placeholder := "{{." + key + "}}"
			html = strings.ReplaceAll(html, placeholder, fmt.Sprintf("%v", value))
		}
	}

	return html, nil
}

// readTemplate reads a template file from the filesystem
func (p *PaymentFlowImpl) readTemplate(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// AtipayVerificationResponse represents the response from Atipay's verify-payment API
type AtipayVerificationResponse struct {
	AmountIRR float64 `json:"amount"`
}

// verifyPaymentWithAtipay calls Atipay's verify-payment API to finalize the transaction
func (p *PaymentFlowImpl) verifyPaymentWithAtipay(ctx context.Context, referenceNumber string) (*AtipayVerificationResponse, error) {
	// Prepare Atipay verification request payload
	verificationPayload := map[string]any{
		"referenceNumber": referenceNumber,
		"apiKey":          p.atipayCfg.APIKey,
	}

	// Convert to JSON
	payloadBytes, err := json.Marshal(verificationPayload)
	if err != nil {
		return nil, err
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://mipg.atipay.net/v1/verify-payment", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Make HTTP request
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("atipay verification API returned non-OK status: %d", resp.StatusCode)
	}

	// Parse response body
	var atipayVerificationResponse AtipayVerificationResponse
	if err := json.NewDecoder(resp.Body).Decode(&atipayVerificationResponse); err != nil {
		return nil, err
	}

	return &atipayVerificationResponse, nil
}

// GetTransactionHistory retrieves the transaction history for a customer with pagination and filtering
func (p *PaymentFlowImpl) GetTransactionHistory(
	ctx context.Context,
	req *dto.GetTransactionHistoryRequest,
	metadata *ClientMetadata,
) (resp *dto.TransactionHistoryResponse, err error) {
	defer func() {
		if err != nil {
			err = NewBusinessError("GET_TRANSACTION_HISTORY_FAILED", "Get transaction history failed", err)
		}
	}()

	// Validate business rules
	if err := p.validateGetTransactionHistoryRequest(req); err != nil {
		return nil, err
	}

	// Get customer to verify they exist and are active
	customer, err := getCustomer(ctx, p.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Get customer's wallet
	wallet, err := getWallet(ctx, p.walletRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Calculate offset for pagination
	offset := (req.Page - 1) * req.PageSize

	var txType *models.TransactionType
	if req.Type != nil {
		txType = utils.ToPtr(models.TransactionType(*req.Type))
	}
	var status *models.TransactionStatus
	if req.Status != nil {
		status = utils.ToPtr(models.TransactionStatus(*req.Status))
	}

	records, totalCount, err := p.transactionRepo.GetHistoryWithMetadata(
		ctx,
		wallet.ID,
		customer.ID,
		req.StartDate,
		req.EndDate,
		txType,
		status,
		int(req.PageSize),
		int(offset),
	)
	if err != nil {
		return nil, err
	}

	// Convert transactions to transaction history items
	items := make([]dto.TransactionHistoryItem, 0)
	for _, record := range records {
		item, err := p.convertTransactionToTransactionHistoryItem(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	// Calculate pagination info
	pagination := p.calculatePaginationInfo(req.Page, req.PageSize, uint(totalCount))

	// Create audit log for successful retrieval
	msg := fmt.Sprintf("Transaction history retrieved: %d items for customer %d", len(items), req.CustomerID)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionTransactionHistoryRetrieved, msg, true, nil, metadata)

	resp = &dto.TransactionHistoryResponse{
		Items:      items,
		Pagination: pagination,
	}

	return resp, nil
}

// validateGetTransactionHistoryRequest validates the transaction history request
func (p *PaymentFlowImpl) validateGetTransactionHistoryRequest(req *dto.GetTransactionHistoryRequest) error {
	if req.Page < 1 {
		return ErrInvalidPage
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		return ErrInvalidPageSize
	}
	if req.StartDate != nil && req.EndDate != nil && req.StartDate.After(*req.EndDate) {
		return ErrStartDateAfterEndDate
	}
	return nil
}

func (p *PaymentFlowImpl) convertTransactionToTransactionHistoryItem(transaction *models.Transaction) (dto.TransactionHistoryItem, error) {
	if transaction == nil {
		return dto.TransactionHistoryItem{}, fmt.Errorf("transaction history record is nil")
	}

	// Get human-readable status
	status := dto.TransactionStatusDisplay[transaction.Status]
	if status == "" {
		status = string(transaction.Status)
	}

	// Prepare external reference
	// var externalRef *string
	// if transaction.ExternalReference != "" {
	// 	externalRef = &transaction.ExternalReference
	// }

	var balanceBefore map[string]uint64
	var balanceAfter map[string]uint64

	if err := json.Unmarshal(transaction.BalanceBefore, &balanceBefore); err != nil {
		return dto.TransactionHistoryItem{}, err
	}
	if err := json.Unmarshal(transaction.BalanceAfter, &balanceAfter); err != nil {
		return dto.TransactionHistoryItem{}, err
	}

	// Parse metadata for source/operation and financial breakdown
	meta := map[string]any{}
	_ = json.Unmarshal(transaction.Metadata, &meta)

	source := toString(meta["source"])
	operation := toString(meta["operation"])
	if operation == "" {
		operation = dto.TransactionTypeDisplay[transaction.Type]
		if operation == "" {
			operation = string(transaction.Type)
		}
	}
	amount := toUint64(meta["amount"])
	customerCredit := toUint64(meta["customer_credit"])
	agencyShareWithTax := toUint64(meta["agency_share_with_tax"])
	refund := toUint64(meta["refund_amount"])
	depositMethod := deriveDepositMethod(meta)

	customerInvoiceUUID := customerInvoiceUUIDFromMetadata(meta)

	// Apply case-specific rules
	switch {
	case source == models.TransactionSourceIncreaseCustomerFreePlusCredit && operation == "increase_customer_free_plus_credit":
		// amount & customerCredit already parsed; agency share forced to 0
		agencyShareWithTax = 0
		refund = 0
	case source == models.TransactionSourceIncreaseAgencyShareWithTax && operation == "increase_agency_share_with_tax":
		// For agency share entries, zero out amount/credit and keep agency share
		amount = 0
		customerCredit = 0
		refund = 0
	case source == "campaign_partial_refund" && operation == "partial_undelivered_messages_refund":
		amount = 0
		customerCredit = 0
		agencyShareWithTax = 0
	case source == "admin_campaign_cancel" && (operation == "cancel_campaign_refund_frozen_missed_approval_deadline" || operation == "cancel_campaign_refund_spent"):
		amount = 0
		customerCredit = 0
		agencyShareWithTax = 0
		if refund == 0 {
			refund = transaction.Amount
		}
	default:
		// Fallback to transaction amount
		if amount == 0 {
			amount = transaction.Amount
		}
	}

	return dto.TransactionHistoryItem{
		UUID: transaction.UUID.String(),
		// Status:              status,
		Amount:             amount,
		CustomerCredit:     customerCredit,
		AgencyShareWithTax: agencyShareWithTax,
		Refund:             refund,
		// Currency:           transaction.Currency,
		// Operation:          operation,
		// Source:             source,
		DepositMethod: depositMethod,
		DateTime:      transaction.CreatedAt,
		// ExternalRef:         externalRef,
		// BalanceBefore:       balanceBefore,
		// BalanceAfter:        balanceAfter,
		CustomerInvoiceUUID: customerInvoiceUUID,
		// Metadata:           meta,
	}, nil
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

func deriveDepositMethod(metadata map[string]any) string {
	paymentChannel := strings.TrimSpace(toString(metadata["payment_channel"]))
	switch paymentChannel {
	case "atipay":
		return "payment_gateway"
	case "deposit_receipt_manual":
		return "deposit_receipt"
	case "admin_direct_charge":
		return "admin_charge"
	}

	switch strings.TrimSpace(toString(metadata["payment_request_source"])) {
	case "wallet_recharge":
		return "payment_gateway"
	case "deposit_receipt":
		return "deposit_receipt"
	case "wallet_recharge_admin":
		return "admin_charge"
	}

	switch strings.TrimSpace(toString(metadata["source"])) {
	case "wallet_recharge":
		return "payment_gateway"
	case "deposit_receipt":
		return "deposit_receipt"
	case "wallet_recharge_admin":
		return "admin_charge"
	}

	return ""
}

func toUint64(v any) uint64 {
	switch t := v.(type) {
	case nil:
		return 0
	case uint64:
		return t
	case uint:
		return uint64(t)
	case int64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case float64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case json.Number:
		if i, err := t.Int64(); err == nil && i > 0 {
			return uint64(i)
		}
		if f, err := t.Float64(); err == nil && f > 0 {
			return uint64(f)
		}
		return 0
	default:
		return 0
	}
}

// calculatePaginationInfo calculates pagination metadata
func (p *PaymentFlowImpl) calculatePaginationInfo(page, pageSize, totalItems uint) dto.TransactionHistoryPaginationInfo {
	totalPages := (totalItems + pageSize - 1) / pageSize // Ceiling division

	return dto.TransactionHistoryPaginationInfo{
		CurrentPage: page,
		PageSize:    pageSize,
		TotalItems:  totalItems,
		TotalPages:  totalPages,
		HasNext:     page < totalPages,
		HasPrevious: page > 1,
	}
}

// GetWalletBalance retrieves the current wallet balance for a customer
func (p *PaymentFlowImpl) GetWalletBalance(
	ctx context.Context,
	req *dto.GetWalletBalanceRequest,
	metadata *ClientMetadata,
) (*dto.GetWalletBalanceResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("GET_WALLET_BALANCE_FAILED", "Get wallet balance failed", err)
		}
	}()

	// Verify customer exists and is active
	_, err = getCustomer(ctx, p.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Find wallet
	wallet, err := getWallet(ctx, p.walletRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Latest balance latestSnapshot
	latestSnapshot, err := getLatestBalanceSnapshot(ctx, p.walletRepo, wallet.ID)
	if err != nil {
		return nil, err
	}

	resp := &dto.GetWalletBalanceResponse{
		Message:            "Wallet balance retrieved successfully",
		Free:               latestSnapshot.FreeBalance,
		Locked:             latestSnapshot.LockedBalance,
		Frozen:             latestSnapshot.FrozenBalance,
		Credit:             latestSnapshot.CreditBalance,
		SpentOnCamapigns:   latestSnapshot.SpentOnCampaign,
		AgencyShareWithTax: latestSnapshot.AgencyShareWithTax,
		Total:              latestSnapshot.TotalBalance,
		Currency:           utils.TomanCurrency,
		LastUpdated:        latestSnapshot.CreatedAt.Format(time.RFC3339),
	}

	return resp, nil
}

// buildReceiptPreview creates a medium-size preview as base64 JPEG for images; empty for PDFs/others.
func buildReceiptPreview(data []byte, contentType string) (string, string) {
	if len(data) == 0 {
		return "", ""
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(ct, "application/pdf") {
		return "", ""
	}
	var img image.Image
	var err error
	reader := bytes.NewReader(data)
	switch {
	case strings.HasPrefix(ct, "image/png"):
		img, err = png.Decode(reader)
	case strings.HasPrefix(ct, "image/jpeg"), strings.HasPrefix(ct, "image/jpg"):
		img, err = jpeg.Decode(reader)
	default:
		return "", ""
	}
	if err != nil || img == nil {
		return "", ""
	}

	// Resize to max width 480px preserving aspect ratio
	maxW := 480
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w == 0 || h == 0 {
		return "", ""
	}
	if w > maxW {
		newW := maxW
		newH := int(float64(h) * (float64(newW) / float64(w)))
		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return "", ""
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return encoded, "image/jpeg"
}

// SubmitDepositReceipt lets customer upload a deposit receipt for manual review.
func (p *PaymentFlowImpl) SubmitDepositReceipt(
	ctx context.Context,
	req *dto.SubmitDepositReceiptRequest,
	metadata *ClientMetadata,
) (*dto.SubmitDepositReceiptResponse, error) {
	if req == nil {
		return nil, NewBusinessError("DEPOSIT_RECEIPT_INVALID_REQUEST", "Submit deposit receipt failed", fmt.Errorf("request is nil"))
	}
	lang := strings.ToUpper(strings.TrimSpace(req.Lang))
	if lang == "" {
		lang = "EN"
	}
	if lang != "EN" && lang != "FA" {
		return nil, ErrInvalidLanguage
	}
	// default to empty slice to satisfy NOT NULL
	data := []byte{}
	if req.FileSize < 0 {
		return nil, ErrDepositReceiptFileEmpty
	}
	if req.FileSize > 0 {
		if req.FileSize > 5*1024*1024 {
			return nil, ErrDepositReceiptFileTooLarge
		}
		ct := strings.ToLower(strings.TrimSpace(req.ContentType))
		allowed := map[string]bool{
			"image/jpeg":               true,
			"image/png":                true,
			"application/pdf":          true,
			"image/jpg":                true,
			"application/octet-stream": true, // fallback when browsers mislabel
		}
		if !allowed[ct] {
			return nil, ErrDepositReceiptFileInvalidType
		}
		var err error
		data, err = base64.StdEncoding.DecodeString(req.FileBase64)
		if err != nil {
			return nil, NewBusinessError("DEPOSIT_RECEIPT_DECODE_FAILED", "Failed to decode file", err)
		}
		if int64(len(data)) != req.FileSize {
			req.FileSize = int64(len(data))
			if req.FileSize > 5*1024*1024 {
				return nil, ErrDepositReceiptFileTooLarge
			}
		}
	}
	ct := ""
	if req.FileSize > 0 {
		ct = strings.ToLower(strings.TrimSpace(req.ContentType))
	}

	var receiptUUID string
	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		customer, err := getCustomer(txCtx, p.customerRepo, req.CustomerID)
		if err != nil {
			return err
		}
		invoiceNumber := fmt.Sprintf("PRF-%s", uuid.New().String())
		rec := &models.DepositReceipt{
			CustomerID:    customer.ID,
			Amount:        req.Amount,
			Currency:      utils.TomanCurrency,
			Status:        models.DepositReceiptStatusPending,
			FileName:      req.FileName,
			ContentType:   ct,
			FileSize:      req.FileSize,
			FileData:      data,
			Lang:          lang,
			InvoiceNumber: invoiceNumber,
			StatusReason:  "Submitted by customer",
		}
		if err := p.depositReceiptRepo.Save(txCtx, rec); err != nil {
			return err
		}
		receiptUUID = rec.UUID.String()

		desc := fmt.Sprintf("Deposit receipt submitted amount %d by customer %d", req.Amount, req.CustomerID)
		_ = createAuditLog(txCtx, p.auditRepo, &customer, models.AuditActionDepositReceiptSubmitted, desc, true, nil, metadata)
		return nil
	})
	if err != nil {
		return nil, NewBusinessError("DEPOSIT_RECEIPT_SUBMIT_FAILED", "Failed to submit deposit receipt", err)
	}

	// Notify deposit reviewers via SMS (best-effort).
	if p.notifier != nil {
		msg := strings.TrimSpace(p.messageCfg.DepositReceiptSubmittedTemplate)
		if msg == "" {
			msg = "A recharge has been completed in the Jazebeh platform. Please generate the related invoice through the admin panel and upload it."
		}
		go func() {
			for _, mobile := range p.adminCfg.ActiveDepositReviewers() {
				_ = p.notifier.SendSMS(context.Background(), mobile, msg, nil)
			}
		}()
	}

	return &dto.SubmitDepositReceiptResponse{
		Success:     true,
		Message:     "Deposit receipt submitted for review",
		ReceiptUUID: receiptUUID,
		Status:      string(models.DepositReceiptStatusPending),
	}, nil
}

// ListDepositReceipts lists receipts for a customer.
func (p *PaymentFlowImpl) ListDepositReceipts(ctx context.Context, customerID uint, lang string) (*dto.ListDepositReceiptsResponse, error) {
	lang = strings.ToUpper(strings.TrimSpace(lang))
	if lang != "" && lang != "EN" && lang != "FA" {
		return nil, ErrInvalidLanguage
	}
	f := models.DepositReceiptFilter{CustomerID: &customerID}
	if lang != "" {
		f.Lang = &lang
	}
	items, err := p.depositReceiptRepo.List(ctx, f, 50, 0, "id DESC")
	if err != nil {
		return nil, NewBusinessError("DEPOSIT_RECEIPT_LIST_FAILED", "Failed to list deposit receipts", err)
	}
	resp := &dto.ListDepositReceiptsResponse{Items: make([]dto.DepositReceiptItem, 0, len(items))}
	for _, r := range items {
		preview, previewType := buildReceiptPreview(r.FileData, r.ContentType)
		resp.Items = append(resp.Items, dto.DepositReceiptItem{
			UUID:          r.UUID.String(),
			CustomerID:    r.CustomerID,
			Amount:        r.Amount,
			Currency:      r.Currency,
			Status:        string(r.Status),
			StatusReason:  r.StatusReason,
			RejectionNote: r.RejectionNote,
			Lang:          r.Lang,
			FileName:      r.FileName,
			ContentType:   r.ContentType,
			FileSize:      r.FileSize,
			PreviewBase64: preview,
			PreviewType:   previewType,
			CreatedAt:     r.CreatedAt,
		})
	}
	return resp, nil
}

// PreviewProformaInvoice builds data for a proforma invoice JSON preview.
func (p *PaymentFlowImpl) PreviewProformaInvoice(
	ctx context.Context,
	customerID uint,
	receiptUUID string,
	lang string,
) (*dto.ProformaPreviewResponse, error) {
	lang = strings.ToUpper(strings.TrimSpace(lang))
	if lang == "" {
		lang = "EN"
	}
	if lang != "EN" && lang != "FA" {
		return nil, ErrInvalidLanguage
	}
	receipt, err := p.depositReceiptRepo.ByUUID(ctx, receiptUUID)
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.CustomerID != customerID {
		return nil, ErrDepositReceiptNotFound
	}
	amountWithTax := receipt.Amount
	customer, err := getCustomer(ctx, p.customerRepo, customerID)
	if err != nil {
		return nil, err
	}
	now := utils.UTCNow()
	invoiceNumber := receipt.InvoiceNumber
	real := uint64(float64(amountWithTax) * 10 / 11)
	tax := amountWithTax - real
	serviceDesc := "Jazebeh wallet top-up"
	notes := "This is a proforma invoice. Final invoice will be issued after payment confirmation."
	sellerName := "Jazebeh Platform"
	if lang == "FA" {
		serviceDesc = "شارژ کیف پول جاذبه"
		notes = "این پیش‌فاکتور است؛ فاکتور نهایی پس از پرداخت و تأیید صادر می‌شود."
		sellerName = "پلتفرم جاذبه"
	}
	buyerName := strings.TrimSpace(customer.RepresentativeFirstName + " " + customer.RepresentativeLastName)
	if buyerName == "" && customer.CompanyName != nil {
		buyerName = *customer.CompanyName
	}
	data := map[string]any{
		"invoice_number":  invoiceNumber,
		"date":            now.Format("2006-01-02"),
		"amount_with_tax": amountWithTax,
		"amount":          real,
		"tax":             tax,
		"service": map[string]any{
			"description": serviceDesc,
		},
		"buyer": map[string]any{
			"customer_uuid": customer.UUID.String(),
			"name":          buyerName,
			"company_name":  customer.CompanyName,
			"mobile":        customer.RepresentativeMobile,
			"company_phone": customer.CompanyPhone,
			"address":       customer.CompanyAddress,
			"national_id":   customer.NationalID,
			"postal_code":   customer.PostalCode,
			"email":         customer.Email,
		},
		"seller": map[string]any{
			"name":           sellerName,
			"economic_code":  "N/A",
			"national_id":    "N/A",
			"sheba":          "N/A",
			"bank_name":      "N/A",
			"account_number": "N/A",
			"card_number":    "N/A",
			"iban":           "N/A",
		},
		"notes": notes,
		"lang":  lang,
	}
	return &dto.ProformaPreviewResponse{Success: true, Data: data}, nil
}

// PreviewProformaInvoiceByAmount builds proforma data using a user-supplied amount (no receipt).
func (p *PaymentFlowImpl) PreviewProformaInvoiceByAmount(
	ctx context.Context,
	customerID uint,
	amountWithTax uint64,
	lang string,
) (*dto.ProformaPreviewResponse, error) {
	lang = strings.ToUpper(strings.TrimSpace(lang))
	if lang == "" {
		lang = "EN"
	}
	if lang != "EN" && lang != "FA" {
		return nil, ErrInvalidLanguage
	}
	// if amountWithTax < 1000 {
	// 	return nil, ErrAmountTooLow
	// }
	// if amountWithTax%1000 != 0 {
	// 	return nil, ErrAmountNotMultiple
	// }
	customer, err := getCustomer(ctx, p.customerRepo, customerID)
	if err != nil {
		return nil, err
	}
	now := utils.UTCNow()
	invoiceNumber := "-"
	real := uint64(float64(amountWithTax) * 10 / 11)
	tax := amountWithTax - real
	serviceDesc := "Jazebeh wallet top-up"
	notes := "This is a proforma invoice. Final invoice will be issued after payment confirmation."
	sellerName := "Jazebeh Platform"
	if lang == "FA" {
		serviceDesc = "شارژ کیف پول جاذبه"
		notes = "این پیش‌فاکتور است؛ فاکتور نهایی پس از پرداخت و تأیید صادر می‌شود."
		sellerName = "پلتفرم جاذبه"
	}
	buyerName := strings.TrimSpace(customer.RepresentativeFirstName + " " + customer.RepresentativeLastName)
	if buyerName == "" && customer.CompanyName != nil {
		buyerName = *customer.CompanyName
	}

	data := map[string]any{
		"invoice_number":  invoiceNumber,
		"date":            now.Format("2006-01-02"),
		"amount_with_tax": amountWithTax,
		"amount":          real,
		"tax":             tax,
		"service": map[string]any{
			"description": serviceDesc,
		},
		"buyer": map[string]any{
			"customer_uuid": customer.UUID.String(),
			"name":          buyerName,
			"company_name":  customer.CompanyName,
			"mobile":        customer.RepresentativeMobile,
			"company_phone": customer.CompanyPhone,
			"address":       customer.CompanyAddress,
			"national_id":   customer.NationalID,
			"postal_code":   customer.PostalCode,
			"email":         customer.Email,
		},
		"seller": map[string]any{
			"name":           sellerName,
			"economic_code":  "N/A",
			"national_id":    "N/A",
			"sheba":          "N/A",
			"bank_name":      "N/A",
			"account_number": "N/A",
			"card_number":    "N/A",
			"iban":           "N/A",
		},
		"notes": notes,
		"lang":  lang,
	}
	return &dto.ProformaPreviewResponse{Success: true, Data: data}, nil
}

// DownloadDepositReceiptFile returns raw receipt file bytes with metadata.
func (p *PaymentFlowImpl) DownloadDepositReceiptFile(
	ctx context.Context,
	customerID uint,
	receiptUUID string,
) ([]byte, string, string, error) {
	rec, err := p.depositReceiptRepo.ByUUID(ctx, receiptUUID)
	if err != nil {
		return nil, "", "", err
	}
	if rec == nil || rec.CustomerID != customerID {
		return nil, "", "", ErrDepositReceiptNotFound
	}
	if len(rec.FileData) == 0 {
		return nil, "", "", ErrDepositReceiptFileEmpty
	}
	return rec.FileData, rec.FileName, rec.ContentType, nil
}

// UpdateDepositReceiptFile replaces the file if receipt is still pending.
func (p *PaymentFlowImpl) UpdateDepositReceiptFile(
	ctx context.Context,
	customerID uint,
	receiptUUID string,
	req *dto.UpdateDepositReceiptFileRequest,
) error {
	if req == nil {
		return NewBusinessError("DEPOSIT_RECEIPT_INVALID_REQUEST", "Invalid request", fmt.Errorf("nil request"))
	}
	if req.FileSize <= 0 || req.FileSize > 5*1024*1024 {
		return ErrDepositReceiptFileTooLarge
	}
	ct := strings.ToLower(strings.TrimSpace(req.ContentType))
	allowed := map[string]bool{
		"image/jpeg":               true,
		"image/png":                true,
		"application/pdf":          true,
		"image/jpg":                true,
		"application/octet-stream": true,
	}
	if !allowed[ct] {
		return ErrDepositReceiptFileInvalidType
	}
	data, err := base64.StdEncoding.DecodeString(req.FileBase64)
	if err != nil {
		return NewBusinessError("DEPOSIT_RECEIPT_DECODE_FAILED", "Failed to decode file", err)
	}
	if int64(len(data)) != req.FileSize {
		req.FileSize = int64(len(data))
		if req.FileSize > 5*1024*1024 {
			return ErrDepositReceiptFileTooLarge
		}
	}

	return repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		rec, err := p.depositReceiptRepo.ByUUID(txCtx, receiptUUID)
		if err != nil {
			return err
		}
		if rec == nil || rec.CustomerID != customerID {
			return ErrDepositReceiptNotFound
		}
		if rec.Status != models.DepositReceiptStatusPending {
			return ErrDepositReceiptAlreadyFinalized
		}
		rec.FileName = req.FileName
		rec.ContentType = ct
		rec.FileSize = req.FileSize
		rec.FileData = data
		rec.StatusReason = "file updated"
		rec.UpdatedAt = utils.UTCNow()
		return p.depositReceiptRepo.Update(txCtx, rec)
	})
}

// DeleteDepositReceiptFile removes stored file bytes (metadata-only receipt kept) if pending.
func (p *PaymentFlowImpl) DeleteDepositReceiptFile(ctx context.Context, customerID uint, receiptUUID string) error {
	return repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		rec, err := p.depositReceiptRepo.ByUUID(txCtx, receiptUUID)
		if err != nil {
			return err
		}
		if rec == nil || rec.CustomerID != customerID {
			return ErrDepositReceiptNotFound
		}
		if rec.Status != models.DepositReceiptStatusPending {
			return ErrDepositReceiptAlreadyFinalized
		}
		rec.FileData = []byte{}
		rec.FileSize = 0
		rec.ContentType = ""
		rec.FileName = ""
		rec.StatusReason = "file removed"
		rec.UpdatedAt = utils.UTCNow()
		return p.depositReceiptRepo.Update(txCtx, rec)
	})
}

func (p *PaymentFlowImpl) NotifyInvoiceIssueRequest(
	ctx context.Context,
	req *dto.NotifyInvoiceIssueRequest,
	customerID uint,
	metadata *ClientMetadata,
) (*dto.NotifyInvoiceIssueResponse, error) {
	if req == nil || strings.TrimSpace(req.TransactionUUID) == "" {
		return nil, ErrTransactionUUIDInvalid
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.TransactionUUID)); err != nil {
		return nil, ErrTransactionUUIDInvalid
	}
	if p.rc == nil {
		return nil, ErrCacheNotAvailable
	}

	customer, err := getCustomer(ctx, p.customerRepo, customerID)
	if err != nil {
		return nil, err
	}
	tx, err := p.transactionRepo.ByUUID(ctx, req.TransactionUUID)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, ErrTransactionNotFound
	}
	if tx.CustomerID != customerID {
		return nil, ErrForbidden
	}
	if len(tx.Metadata) > 0 {
		var txMeta map[string]any
		if err := json.Unmarshal(tx.Metadata, &txMeta); err != nil {
			return nil, err
		}
		if customerInvoiceUUID := customerInvoiceUUIDFromMetadata(txMeta); customerInvoiceUUID != nil &&
			strings.TrimSpace(*customerInvoiceUUID) != "" {
			return nil, ErrInvoiceIssueAlreadyAssigned
		}
	}

	reviewers := p.adminCfg.ActiveDepositReviewers()
	if len(reviewers) == 0 {
		return nil, NewBusinessError("INVOICE_ISSUE_NOTIFY_FAILED", "No deposit receipt reviewer configured", ErrNotFound)
	}
	reviewerPhone := reviewers[0]

	rlKey := redisKey(p.cacheCfg, fmt.Sprintf("invoice_issue_request:%d", customerID))
	allowed, err := p.rc.SetNX(ctx, rlKey, req.TransactionUUID, 24*time.Hour).Result()
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrInvoiceIssueRequestRateLimited
	}

	fullName := strings.TrimSpace(customer.RepresentativeFirstName + " " + customer.RepresentativeLastName)
	if fullName == "" {
		fullName = fmt.Sprintf("customer-%d", customer.ID)
	}
	companyName := "-"
	if customer.CompanyName != nil && strings.TrimSpace(*customer.CompanyName) != "" {
		companyName = strings.TrimSpace(*customer.CompanyName)
	}

	template := strings.TrimSpace(p.messageCfg.InvoiceIssueRequestTemplate)
	msg := fmt.Sprintf(template, fullName, companyName)
	if smsErr := p.notifier.SendSMS(ctx, reviewerPhone, msg, utils.ToPtr(int64(customerID))); smsErr != nil {
		_ = p.rc.Del(ctx, rlKey).Err()
		return nil, smsErr
	}

	auditMsg := fmt.Sprintf("Customer %d requested invoice issuance notification for transaction %s", customerID, req.TransactionUUID)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionInvoiceIssueRequested, auditMsg, true, nil, metadata)

	return &dto.NotifyInvoiceIssueResponse{
		Success: true,
		Message: "Invoice issue request submitted successfully",
	}, nil
}
