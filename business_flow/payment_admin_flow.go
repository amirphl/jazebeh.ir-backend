package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PaymentAdminFlow handles admin-only payment business logic.
type PaymentAdminFlow interface {
	AdminChargeWallet(
		ctx context.Context,
		req *dto.AdminChargeWalletRequest,
		metadata *ClientMetadata,
		adminID uint,
	) (*dto.AdminChargeWalletResponse, error)
	AdminPreviewWalletChargeImpact(
		ctx context.Context,
		req *dto.AdminPreviewWalletChargeImpactRequest,
		metadata *ClientMetadata,
		adminID uint,
	) (*dto.AdminPreviewWalletChargeImpactResponse, error)
	AdminListDepositReceipts(
		ctx context.Context,
		f models.DepositReceiptFilter,
		limit, offset int,
		order string,
	) (*dto.ListDepositReceiptsResponse, error)
	AdminListTransactions(
		ctx context.Context,
		req *dto.AdminListTransactionsRequest,
		metadata *ClientMetadata,
	) (*dto.AdminListTransactionsResponse, error)
	AdminGetDepositReceiptFile(ctx context.Context, receiptUUID string) ([]byte, string, string, error)
	AdminUpdateDepositReceiptStatus(
		ctx context.Context,
		req *dto.AdminUpdateDepositReceiptStatusRequest,
		adminID uint,
		metadata *ClientMetadata,
	) (*dto.SubmitDepositReceiptResponse, error)
	AddInvoiceToTransaction(
		ctx context.Context,
		req *dto.AdminAddInvoiceToTransactionRequest,
		adminID uint,
		metadata *ClientMetadata,
	) (*dto.AdminAddInvoiceToTransactionResponse, error)
}

// NewPaymentAdminFlow creates a new admin payment flow instance.
func NewPaymentAdminFlow(
	paymentRequestRepo repository.PaymentRequestRepository,
	walletRepo repository.WalletRepository,
	customerRepo repository.CustomerRepository,
	auditRepo repository.AuditLogRepository,
	balanceSnapshotRepo repository.BalanceSnapshotRepository,
	transactionRepo repository.TransactionRepository,
	agencyDiscountRepo repository.AgencyDiscountRepository,
	depositReceiptRepo repository.DepositReceiptRepository,
	multimediaRepo repository.MultimediaAssetRepository,
	db *gorm.DB,
	atipayCfg config.AtipayConfig,
	sysCfg config.SystemConfig,
	deploymentCfg config.DeploymentConfig,
) PaymentAdminFlow {
	return &PaymentFlowImpl{
		paymentRequestRepo:  paymentRequestRepo,
		walletRepo:          walletRepo,
		customerRepo:        customerRepo,
		auditRepo:           auditRepo,
		balanceSnapshotRepo: balanceSnapshotRepo,
		transactionRepo:     transactionRepo,
		agencyDiscountRepo:  agencyDiscountRepo,
		depositReceiptRepo:  depositReceiptRepo,
		multimediaRepo:      multimediaRepo,
		db:                  db,
		atipayCfg:           atipayCfg,
		sysCfg:              sysCfg,
		deploymentCfg:       deploymentCfg,
	}
}

// AdminChargeWallet directly charges a customer's wallet without redirecting to Atipay.
func (p *PaymentFlowImpl) AdminChargeWallet(
	ctx context.Context,
	req *dto.AdminChargeWalletRequest,
	metadata *ClientMetadata,
	adminID uint,
) (*dto.AdminChargeWalletResponse, error) {
	if req == nil {
		return nil, NewBusinessError("CHARGE_WALLET_BY_ADMIN_FAILED", "Charge wallet by admin failed", fmt.Errorf("request is nil"))
	}
	req.AdminNote = strings.TrimSpace(req.AdminNote)
	if req.CustomerID == 0 {
		return nil, NewBusinessError(
			"CHARGE_WALLET_BY_ADMIN_FAILED",
			"Charge wallet by admin failed",
			fmt.Errorf("customer_id is required"),
		)
	}
	if err := p.validateChargeWalletRequest(&dto.ChargeWalletRequest{AmountWithTax: req.AmountWithTax}, ""); err != nil {
		return nil, NewBusinessError("CHARGE_WALLET_BY_ADMIN_FAILED", "Charge wallet by admin failed", err)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" && metadata != nil {
		idempotencyKey = strings.TrimSpace(metadata.RequestID)
	}
	if idempotencyKey == "" {
		return nil, NewBusinessError("IDEMPOTENCY_KEY_REQUIRED", "Idempotency key is required", fmt.Errorf("idempotency_key is required"))
	}

	var customer models.Customer
	var paymentRequest *models.PaymentRequest
	isIdempotentReplay := false

	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		var err error

		lockName := "charge_wallet_by_admin:" + idempotencyKey
		if err := p.db.WithContext(txCtx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockName).Error; err != nil {
			return err
		}

		existing, err := p.paymentRequestRepo.FindAdminChargeByIdempotencyKey(txCtx, idempotencyKey)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.CustomerID != req.CustomerID || existing.Amount != req.AmountWithTax {
				return fmt.Errorf("idempotency key already used with different request payload")
			}
			existingAdminID := paymentMetadataAdminID(existing.Metadata)
			if existingAdminID != 0 && existingAdminID != adminID {
				return fmt.Errorf("idempotency key already used by a different admin")
			}
			var existingMetadata map[string]any
			if len(existing.Metadata) > 0 {
				if err := json.Unmarshal(existing.Metadata, &existingMetadata); err != nil {
					return err
				}
			}
			if strings.TrimSpace(toString(existingMetadata["admin_note"])) != req.AdminNote {
				return fmt.Errorf("idempotency key already used with different request payload")
			}
			if existing.Status != models.PaymentRequestStatusCompleted {
				return fmt.Errorf("idempotency key exists but payment is not completed yet")
			}
			customer = models.Customer{ID: req.CustomerID}
			paymentRequest = existing
			isIdempotentReplay = true
			return nil
		}

		customer, err = getCustomer(txCtx, p.customerRepo, req.CustomerID)
		if err != nil {
			return err
		}

		// Ensure customer wallet exists and attach it to customer for payment request creation.
		wallet, err := p.walletRepo.ByCustomerID(txCtx, customer.ID)
		if err != nil {
			return err
		}
		customer.Wallet = wallet

		paymentRequest, err = p.createPaymentRequest(txCtx, customer, req.AmountWithTax, "EN")
		if err != nil {
			return err
		}

		// Mark origin metadata so downstream snapshots/transactions clearly show admin direct charge details.
		var m map[string]any
		if err := json.Unmarshal(paymentRequest.Metadata, &m); err != nil {
			return err
		}
		m["source"] = "wallet_recharge_admin"
		m["admin_id"] = adminID
		m["charged_by"] = "admin"
		m["payment_channel"] = "admin_direct_charge"
		m["admin_charge"] = true
		m["idempotency_key"] = idempotencyKey
		m["admin_note"] = req.AdminNote
		metadataJSON, err := json.Marshal(m)
		if err != nil {
			return err
		}

		syntheticReference := fmt.Sprintf("SYNTHETIC_REF-%s", uuid.New().String())
		syntheticTrace := fmt.Sprintf("SYNTHETIC_TRACE-%s", uuid.New().String())
		syntheticRRN := fmt.Sprintf("SYNTHETIC_RRN-%s", uuid.New().String())

		paymentRequest.Metadata = metadataJSON
		paymentRequest.Description = "charge wallet by admin"
		paymentRequest.RedirectURL = fmt.Sprintf("https://%s/admin/payments/direct-charge", p.deploymentCfg.Domain)
		paymentRequest.AtipayToken = "ADMIN_DIRECT_CHARGE"
		paymentRequest.AtipayStatus = "OK"
		paymentRequest.Status = models.PaymentRequestStatusPending
		paymentRequest.StatusReason = "payment request pending for admin direct charge"
		paymentRequest.UpdatedAt = utils.UTCNow()
		if err := p.paymentRequestRepo.Update(txCtx, paymentRequest); err != nil {
			return err
		}

		callbackReq := &dto.AtipayRequest{
			State:             "OK",
			Status:            "2",
			ReferenceNumber:   syntheticReference,
			ReservationNumber: paymentRequest.InvoiceNumber,
			TerminalID:        "ADMIN_PANEL",
			TraceNumber:       syntheticTrace,
			MaskedPAN:         "ADMIN-DIRECT",
			RRN:               syntheticRRN,
		}
		mapping := p.getPaymentStatusMapping(callbackReq.Status, callbackReq.State)
		mapping.Description = "Payment completed successfully via admin direct charge"
		if err := p.updatePaymentRequest(txCtx, paymentRequest, callbackReq, mapping); err != nil {
			return err
		}

		if err := p.updateBalances(txCtx, paymentRequest, callbackReq); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf("Charge wallet by admin failed for customer %d by admin %d: %s", req.CustomerID, adminID, err.Error())
		_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionWalletChargeFailed, errMsg, false, &errMsg, metadata)
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionWalletChargeFailed,
			"Admin charge wallet",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":       req.CustomerID,
				"admin_id":          adminID,
				"amount_with_tax":   req.AmountWithTax,
				"admin_note":        req.AdminNote,
				"idempotency_key":   idempotencyKey,
				"idempotent_replay": isIdempotentReplay,
			},
			err,
		)
		return nil, NewBusinessError("CHARGE_WALLET_BY_ADMIN_FAILED", "Failed to charge wallet by admin", err)
	}

	msg := fmt.Sprintf("Wallet charged by admin %d for customer %d (payment request %d)", adminID, req.CustomerID, paymentRequest.ID)
	if isIdempotentReplay {
		msg = fmt.Sprintf(
			"Wallet charge idempotency replay by admin %d for customer %d (payment request %d)",
			adminID,
			req.CustomerID,
			paymentRequest.ID,
		)
	}
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionWalletChargeCompleted, msg, true, nil, metadata)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionPaymentCallbackProcessed, msg, true, nil, metadata)
	logAdminAction(ctx, p.auditRepo, models.AuditActionWalletChargeCompleted, "Admin charge wallet", true, &req.CustomerID, map[string]any{
		"customer_id":        req.CustomerID,
		"admin_id":           adminID,
		"amount_with_tax":    req.AmountWithTax,
		"admin_note":         req.AdminNote,
		"idempotency_key":    idempotencyKey,
		"idempotent_replay":  isIdempotentReplay,
		"payment_request_id": paymentRequest.ID,
	}, nil)

	return &dto.AdminChargeWalletResponse{
		Message: func() string {
			if isIdempotentReplay {
				return "Wallet charge already processed (idempotent replay)"
			}
			return "Wallet charged successfully by admin"
		}(),
		Success:          true,
		PaymentRequestID: paymentRequest.ID,
		InvoiceNumber:    paymentRequest.InvoiceNumber,
		ReferenceNumber:  paymentRequest.PaymentReference,
		CustomerID:       req.CustomerID,
		AdminID:          adminID,
		AmountWithTax:    req.AmountWithTax,
	}, nil
}

// AdminPreviewWalletChargeImpact calculates the effects of an admin wallet charge without modifying state.
func (p *PaymentFlowImpl) AdminPreviewWalletChargeImpact(
	ctx context.Context,
	req *dto.AdminPreviewWalletChargeImpactRequest,
	metadata *ClientMetadata,
	adminID uint,
) (*dto.AdminPreviewWalletChargeImpactResponse, error) {
	if req == nil {
		return nil, NewBusinessError(
			"PREVIEW_WALLET_CHARGE_IMPACT_FAILED",
			"Preview wallet charge impact failed",
			fmt.Errorf("request is nil"),
		)
	}
	if req.CustomerID == 0 {
		return nil, NewBusinessError(
			"PREVIEW_WALLET_CHARGE_IMPACT_FAILED",
			"Preview wallet charge impact failed",
			fmt.Errorf("customer_id is required"),
		)
	}
	if err := p.validateChargeWalletRequest(&dto.ChargeWalletRequest{AmountWithTax: req.AmountWithTax}, ""); err != nil {
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Preview wallet charge impact failed", err)
	}

	customer, err := getCustomer(ctx, p.customerRepo, req.CustomerID)
	if err != nil {
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminPreviewWalletChargeImpactFailed,
			"Admin preview wallet charge impact",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":     req.CustomerID,
				"admin_id":        adminID,
				"amount_with_tax": req.AmountWithTax,
			},
			err,
		)
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Failed to preview wallet charge impact", err)
	}

	if customer.ReferrerAgencyID == nil {
		err = ErrReferrerAgencyIDRequired
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminPreviewWalletChargeImpactFailed,
			"Admin preview wallet charge impact",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":     req.CustomerID,
				"admin_id":        adminID,
				"amount_with_tax": req.AmountWithTax,
			},
			err,
		)
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Failed to preview wallet charge impact", err)
	}

	agencyDiscount, err := p.agencyDiscountRepo.GetActiveDiscount(ctx, *customer.ReferrerAgencyID, customer.ID)
	if err != nil {
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminPreviewWalletChargeImpactFailed,
			"Admin preview wallet charge impact",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":     req.CustomerID,
				"admin_id":        adminID,
				"amount_with_tax": req.AmountWithTax,
			},
			err,
		)
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Failed to preview wallet charge impact", err)
	}
	if agencyDiscount == nil {
		err = ErrAgencyDiscountNotFound
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminPreviewWalletChargeImpactFailed,
			"Admin preview wallet charge impact",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":     req.CustomerID,
				"admin_id":        adminID,
				"amount_with_tax": req.AmountWithTax,
			},
			err,
		)
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Failed to preview wallet charge impact", err)
	}

	scatteredSettlementItems, err := p.calculateScatteredSettlementItems(ctx, customer, req.AmountWithTax)
	if err != nil {
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminPreviewWalletChargeImpactFailed,
			"Admin preview wallet charge impact",
			false,
			&req.CustomerID,
			map[string]any{
				"customer_id":     req.CustomerID,
				"admin_id":        adminID,
				"amount_with_tax": req.AmountWithTax,
			},
			err,
		)
		return nil, NewBusinessError("PREVIEW_WALLET_CHARGE_IMPACT_FAILED", "Failed to preview wallet charge impact", err)
	}

	amount := uint64(float64(req.AmountWithTax) * 10 / 11)
	tax := req.AmountWithTax - amount
	creditIncrease := uint64(float64(amount)/(1-agencyDiscount.DiscountRate)) - amount

	resp := &dto.AdminPreviewWalletChargeImpactResponse{
		Message:            "Wallet charge impact preview calculated successfully",
		Success:            true,
		CustomerID:         customer.ID,
		AgencyID:           *customer.ReferrerAgencyID,
		AgencyDiscountID:   agencyDiscount.ID,
		DiscountRate:       agencyDiscount.DiscountRate,
		AmountWithTax:      req.AmountWithTax,
		Amount:             amount,
		Tax:                tax,
		FreeIncrease:       amount,
		CreditIncrease:     creditIncrease,
		SystemShareWithTax: scatteredSettlementItems[0].Amount,
		AgencyShareWithTax: scatteredSettlementItems[1].Amount,
	}

	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminPreviewWalletChargeImpactSucceeded,
		"Admin preview wallet charge impact",
		true,
		&req.CustomerID,
		map[string]any{
			"customer_id":           req.CustomerID,
			"admin_id":              adminID,
			"amount_with_tax":       req.AmountWithTax,
			"agency_discount_id":    agencyDiscount.ID,
			"discount_rate":         agencyDiscount.DiscountRate,
			"free_increase":         resp.FreeIncrease,
			"credit_increase":       resp.CreditIncrease,
			"system_share_with_tax": resp.SystemShareWithTax,
			"agency_share_with_tax": resp.AgencyShareWithTax,
		},
		nil,
	)

	return resp, nil
}

// AdminListDepositReceipts lists receipts with optional filters.
func (p *PaymentFlowImpl) AdminListDepositReceipts(
	ctx context.Context,
	f models.DepositReceiptFilter,
	limit, offset int,
	order string,
) (*dto.ListDepositReceiptsResponse, error) {
	items, err := p.depositReceiptRepo.List(ctx, f, limit, offset, order)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LIST_DEPOSIT_RECEIPTS_FAILED", "Failed to list deposit receipts", err)
	}

	customerIDs := make([]uint, 0, len(items))
	seenCustomerIDs := make(map[uint]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if _, ok := seenCustomerIDs[item.CustomerID]; ok {
			continue
		}
		seenCustomerIDs[item.CustomerID] = struct{}{}
		customerIDs = append(customerIDs, item.CustomerID)
	}

	customerNamesByID := make(map[uint]string, len(customerIDs))
	if len(customerIDs) > 0 {
		customers, err := p.customerRepo.FindByIDs(ctx, customerIDs)
		if err != nil {
			return nil, NewBusinessError("ADMIN_LIST_DEPOSIT_RECEIPTS_FAILED", "Failed to list deposit receipts", err)
		}
		for _, customer := range customers {
			if customer == nil {
				continue
			}
			customerNamesByID[customer.ID] = buildCustomerDisplayName(*customer)
		}
	}

	resp := &dto.ListDepositReceiptsResponse{Items: make([]dto.DepositReceiptItem, 0, len(items))}
	for _, r := range items {
		preview, previewType := buildReceiptPreview(r.FileData, r.ContentType)
		resp.Items = append(resp.Items, dto.DepositReceiptItem{
			UUID:             r.UUID.String(),
			CustomerID:       r.CustomerID,
			CustomerFullName: customerNamesByID[r.CustomerID],
			Amount:           r.Amount,
			Currency:         r.Currency,
			Status:           string(r.Status),
			StatusReason:     r.StatusReason,
			RejectionNote:    r.RejectionNote,
			Lang:             r.Lang,
			FileName:         r.FileName,
			ContentType:      r.ContentType,
			FileSize:         r.FileSize,
			PreviewBase64:    preview,
			PreviewType:      previewType,
			CreatedAt:        r.CreatedAt,
		})
	}
	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminDepositReceiptReviewed,
		"Admin listed deposit receipts",
		true,
		nil,
		map[string]any{
			"limit":      limit,
			"offset":     offset,
			"order":      order,
			"filter":     f,
			"item_count": len(resp.Items),
		},
		nil,
	)
	return resp, nil
}

// AdminListTransactions returns admin-facing filtered transactions for customer balance increase operation.
func (p *PaymentFlowImpl) AdminListTransactions(
	ctx context.Context,
	req *dto.AdminListTransactionsRequest,
	metadata *ClientMetadata,
) (resp *dto.AdminListTransactionsResponse, err error) {
	defer func() {
		if err != nil {
			err = NewBusinessError("ADMIN_LIST_TRANSACTIONS_FAILED", "List transactions failed", err)
		}
	}()

	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if err := p.validateAdminListTransactionsRequest(req); err != nil {
		return nil, err
	}

	filter := models.TransactionFilter{
		Source:       utils.ToPtr(models.TransactionSourceIncreaseCustomerFreePlusCredit),
		Operation:    utils.ToPtr("increase_customer_free_plus_credit"),
		CustomerID:   req.CustomerID,
		CustomerName: req.CustomerName,
	}
	if req.StartDate != nil {
		filter.CreatedAfter = req.StartDate
	}
	if req.EndDate != nil {
		filter.CreatedBefore = req.EndDate
	}

	offset := (req.Page - 1) * req.PageSize
	transactions, err := p.transactionRepo.GetAdminListWithCustomer(ctx, filter, "id DESC", int(req.PageSize), int(offset))
	if err != nil {
		return nil, err
	}
	totalCount, err := p.transactionRepo.Count(ctx, filter)
	if err != nil {
		return nil, err
	}

	items := make([]dto.AdminTransactionItem, 0, len(transactions))
	for _, transaction := range transactions {
		item, err := p.convertTransactionHistoryRecordToTransactionHistoryItem(transaction)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	resp = &dto.AdminListTransactionsResponse{
		Items:      items,
		Pagination: p.calculatePaginationInfo(req.Page, req.PageSize, uint(totalCount)),
	}

	msg := fmt.Sprintf("Admin listed transactions: %d items", len(items))
	_ = createAuditLog(ctx, p.auditRepo, nil, models.AuditActionAdminListTransactions, msg, true, nil, metadata)
	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminListTransactions,
		"Admin listed transactions",
		true,
		req.CustomerID,
		map[string]any{
			"page":          req.Page,
			"page_size":     req.PageSize,
			"total_count":   totalCount,
			"item_count":    len(items),
			"start_date":    req.StartDate,
			"end_date":      req.EndDate,
			"customer_id":   req.CustomerID,
			"customer_name": req.CustomerName,
			"operation":     "increase_customer_free_plus_credit",
			"source":        models.TransactionSourceIncreaseCustomerFreePlusCredit,
		},
		nil,
	)
	return resp, nil
}

// AdminGetDepositReceiptFile returns raw file bytes and metadata.
func (p *PaymentFlowImpl) AdminGetDepositReceiptFile(ctx context.Context, receiptUUID string) ([]byte, string, string, error) {
	rec, err := p.depositReceiptRepo.ByUUID(ctx, receiptUUID)
	if err != nil {
		return nil, "", "", err
	}
	if rec == nil {
		return nil, "", "", ErrDepositReceiptNotFound
	}

	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminDownloadDepositReceiptFile,
		"Admin download deposit receipt file",
		true,
		&rec.CustomerID,
		map[string]any{
			"receipt_uuid":   receiptUUID,
			"file_name":      rec.FileName,
			"content_type":   rec.ContentType,
			"file_size":      rec.FileSize,
			"receipt_status": rec.Status,
		},
		nil,
	)

	return rec.FileData, rec.FileName, rec.ContentType, nil
}

// AddInvoiceToTransaction links an issued invoice UUID to a transaction resolved by transaction UUID.
func (p *PaymentFlowImpl) AddInvoiceToTransaction(
	ctx context.Context,
	req *dto.AdminAddInvoiceToTransactionRequest,
	adminID uint,
	metadata *ClientMetadata,
) (*dto.AdminAddInvoiceToTransactionResponse, error) {
	if req == nil {
		return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", fmt.Errorf("nil request"))
	}

	transactionUUID := strings.TrimSpace(req.TransactionUUID)
	customerInvoiceUUID := strings.TrimSpace(req.CustomerInvoiceUUID)
	if transactionUUID == "" {
		return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", ErrTransactionUUIDInvalid)
	}
	if _, err := uuid.Parse(transactionUUID); err != nil {
		return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", ErrTransactionUUIDInvalid)
	}
	if err := p.validateCustomerInvoiceUUIDForApproval(ctx, customerInvoiceUUID); err != nil {
		if errors.Is(err, ErrDepositReceiptInvoiceRequired) {
			return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", ErrInvoiceUUIDRequired)
		}
		if errors.Is(err, ErrDepositReceiptInvoiceInvalid) {
			return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", ErrInvoiceUUIDInvalid)
		}
		return nil, NewBusinessError("ADMIN_ADD_INVOICE_INVALID", "Invalid request", err)
	}

	updated := false
	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		trx, err := p.transactionRepo.ByUUID(txCtx, transactionUUID)
		if err != nil {
			return err
		}
		if trx == nil {
			return ErrTransactionNotFound
		}

		meta := map[string]any{}
		if len(trx.Metadata) > 0 {
			if err := json.Unmarshal(trx.Metadata, &meta); err != nil {
				return err
			}
		}

		if meta["source"] != models.TransactionSourceIncreaseCustomerFreePlusCredit {
			return ErrTransactionNotFound
		}
		if meta["operation"] != "increase_customer_free_plus_credit" {
			return ErrTransactionNotFound
		}

		retrievedCustomerInvoiceUUID := customerInvoiceUUIDFromMetadata(meta)
		if retrievedCustomerInvoiceUUID != nil {
			if strings.TrimSpace(*retrievedCustomerInvoiceUUID) != customerInvoiceUUID {
				return ErrInvoiceUUIDMismatch
			}
			updated = false
			return nil
		}

		meta["customer_invoice_uuid"] = customerInvoiceUUID
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		trx.Metadata = metaJSON
		trx.UpdatedAt = utils.UTCNow()
		if err := p.transactionRepo.UpdateMetadata(txCtx, trx.ID, trx.Metadata, trx.UpdatedAt); err != nil {
			return err
		}

		updated = true
		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf(
			"Admin %d failed to attach customer invoice %s to transaction %s: %v",
			adminID,
			customerInvoiceUUID,
			transactionUUID,
			err,
		)
		_ = createAuditLog(ctx, p.auditRepo, nil, models.AuditActionAdminAttachInvoiceToTransaction, errMsg, false, &errMsg, metadata)
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminAttachInvoiceToTransaction,
			"Admin attached customer invoice to transaction",
			false,
			nil,
			map[string]any{
				"transaction_uuid":      transactionUUID,
				"customer_invoice_uuid": customerInvoiceUUID,
			},
			err,
		)
		return nil, NewBusinessError("ADMIN_ADD_INVOICE_FAILED", "Failed to add invoice to transaction", err)
	}

	resp := &dto.AdminAddInvoiceToTransactionResponse{
		Success: updated,
	}

	msg := fmt.Sprintf("Admin %d attached customer invoice %s to transaction %s", adminID, customerInvoiceUUID, transactionUUID)
	_ = createAuditLog(ctx, p.auditRepo, nil, models.AuditActionAdminAttachInvoiceToTransaction, msg, true, nil, metadata)
	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminAttachInvoiceToTransaction,
		"Admin attached customer invoice to transaction",
		true,
		nil,
		map[string]any{
			"transaction_uuid":      transactionUUID,
			"customer_invoice_uuid": customerInvoiceUUID,
			"updated":               updated,
		},
		nil,
	)
	return resp, nil
}

// AdminUpdateDepositReceiptStatus approves or rejects a receipt and on approval credits wallet like normal flow.
func (p *PaymentFlowImpl) AdminUpdateDepositReceiptStatus(
	ctx context.Context,
	req *dto.AdminUpdateDepositReceiptStatusRequest,
	adminID uint,
	metadata *ClientMetadata,
) (*dto.SubmitDepositReceiptResponse, error) {
	if req == nil {
		return nil, NewBusinessError("ADMIN_RECEIPT_UPDATE_INVALID", "Invalid request", fmt.Errorf("nil request"))
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "approve" && action != "reject" {
		return nil, ErrDepositReceiptInvalidStatus
	}
	if action == "approve" {
		// if err := p.validateCustomerInvoiceUUIDForApproval(ctx, req.CustomerInvoiceUUID); err != nil {
		// 	return nil, NewBusinessError("ADMIN_RECEIPT_UPDATE_INVALID", "Invalid request", err)
		// }
	}

	var customer models.Customer
	var receipt *models.DepositReceipt
	err := repository.WithTransaction(ctx, p.db, func(txCtx context.Context) error {
		var err error
		receipt, err = p.depositReceiptRepo.ByUUID(txCtx, req.ReceiptUUID)
		if err != nil {
			return err
		}
		if receipt == nil {
			return ErrDepositReceiptNotFound
		}
		if receipt.Status == models.DepositReceiptStatusApproved {
			return ErrDepositReceiptAlreadyApproved
		}
		if receipt.Status == models.DepositReceiptStatusRejected {
			return ErrDepositReceiptAlreadyRejected
		}
		if action == "approve" {
			// if err := p.paymentRequestRepo.LockCustomerInvoiceUUID(txCtx, req.CustomerInvoiceUUID); err != nil {
			// 	return err
			// }
			// used, err := p.paymentRequestRepo.IsCustomerDepositInvoiceUUIDAlreadyLinked(txCtx, req.CustomerInvoiceUUID)
			// if err != nil {
			// 	return err
			// }
			// if used {
			// 	return ErrDepositReceiptInvoiceDuplicate
			// }
		}

		if action == "reject" {
			receipt.Status = models.DepositReceiptStatusRejected
			receipt.StatusReason = "Rejected by admin"
			receipt.ReviewerID = &adminID
			if req.Reason != "" {
				receipt.RejectionNote = &req.Reason
			}
			if err := p.depositReceiptRepo.Update(txCtx, receipt); err != nil {
				return err
			}
			desc := fmt.Sprintf("Deposit receipt %s rejected by admin %d", req.ReceiptUUID, adminID)
			_ = createAuditLog(txCtx, p.auditRepo, nil, models.AuditActionAdminUpdateDepositReceiptStatus, desc, true, nil, metadata)
			return nil
		}

		// Approve path mirrors AdminChargeWallet + PaymentCallback success.
		customer, err = getCustomer(txCtx, p.customerRepo, receipt.CustomerID)
		if err != nil {
			return err
		}
		wallet, err := p.walletRepo.ByCustomerID(txCtx, customer.ID)
		if err != nil {
			return err
		}
		customer.Wallet = wallet

		paymentRequest, err := p.createPaymentRequest(txCtx, customer, receipt.Amount, receipt.Lang)
		if err != nil {
			return err
		}

		// Mark metadata
		var m map[string]any
		if err := json.Unmarshal(paymentRequest.Metadata, &m); err != nil {
			return err
		}
		m["source"] = "deposit_receipt"
		m["deposit_receipt_uuid"] = receipt.UUID.String()
		m["deposit_invoice_number"] = receipt.InvoiceNumber
		// m["customer_invoice_uuid"] = req.CustomerInvoiceUUID
		m["admin_id"] = adminID
		m["payment_channel"] = "deposit_receipt_manual"
		metaJSON, err := json.Marshal(m)
		if err != nil {
			return err
		}

		paymentRequest.Metadata = metaJSON
		paymentRequest.Description = "wallet charge via deposit receipt"
		paymentRequest.RedirectURL = fmt.Sprintf("https://%s/api/v1/payments/deposit-receipt", p.deploymentCfg.Domain)
		paymentRequest.AtipayToken = "ADMIN_DEPOSIT_RECEIPT"
		paymentRequest.AtipayStatus = "OK"
		paymentRequest.Status = models.PaymentRequestStatusPending
		paymentRequest.StatusReason = "payment request pending for deposit receipt approval"
		paymentRequest.UpdatedAt = utils.UTCNow()
		if err := p.paymentRequestRepo.Update(txCtx, paymentRequest); err != nil {
			return err
		}

		callbackReq := &dto.AtipayRequest{
			State:             "OK",
			Status:            "2",
			ReferenceNumber:   fmt.Sprintf("DEPOSIT_RECEIPT-REF-%s", uuid.New().String()),
			ReservationNumber: paymentRequest.InvoiceNumber,
			TerminalID:        "DEPOSIT_RECEIPT",
			TraceNumber:       fmt.Sprintf("DEPOSIT_RECEIPT-TRACE-%s", uuid.New().String()),
			MaskedPAN:         "DEPOSIT_RECEIPT",
			RRN:               fmt.Sprintf("DEPOSIT_RECEIPT-RRN-%s", uuid.New().String()),
		}
		mapping := p.getPaymentStatusMapping(callbackReq.Status, callbackReq.State)
		mapping.Description = "Payment completed via deposit receipt approval"
		if err := p.updatePaymentRequest(txCtx, paymentRequest, callbackReq, mapping); err != nil {
			return err
		}
		if err := p.updateBalances(txCtx, paymentRequest, callbackReq); err != nil {
			return err
		}

		receipt.Status = models.DepositReceiptStatusApproved
		receipt.StatusReason = "Approved and credited"
		receipt.ReviewerID = &adminID
		if err := p.depositReceiptRepo.Update(txCtx, receipt); err != nil {
			return err
		}

		// desc := fmt.Sprintf("Deposit receipt %s approved by admin %d and credited (customer_invoice_uuid=%s)", req.ReceiptUUID, adminID,
		// req.CustomerInvoiceUUID)
		desc := fmt.Sprintf("Deposit receipt %s approved by admin %d and credited", req.ReceiptUUID, adminID)
		_ = createAuditLog(txCtx, p.auditRepo, &customer, models.AuditActionAdminUpdateDepositReceiptStatus, desc, true, nil, metadata)
		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf("Admin %d failed to update receipt %s: %v", adminID, req.ReceiptUUID, err)
		_ = createAuditLog(ctx, p.auditRepo, nil, models.AuditActionAdminUpdateDepositReceiptStatus, errMsg, false, &errMsg, metadata)
		logAdminAction(
			ctx,
			p.auditRepo,
			models.AuditActionAdminUpdateDepositReceiptStatus,
			"Admin updated deposit receipt status",
			false,
			nil,
			map[string]any{
				"receipt_uuid": req.ReceiptUUID,
				"action":       action,
				// "customer_invoice_uuid": req.CustomerInvoiceUUID,
			},
			err,
		)
		return nil, NewBusinessError("ADMIN_RECEIPT_UPDATE_FAILED", "Failed to update deposit receipt", err)
	}

	msg := fmt.Sprintf("Admin %d updated receipt %s to %s", adminID, req.ReceiptUUID, req.Action)
	_ = createAuditLog(ctx, p.auditRepo, &customer, models.AuditActionAdminUpdateDepositReceiptStatus, msg, true, nil, metadata)
	logAdminAction(
		ctx,
		p.auditRepo,
		models.AuditActionAdminUpdateDepositReceiptStatus,
		"Admin updated deposit receipt status",
		true,
		&receipt.CustomerID,
		map[string]any{
			"receipt_uuid": req.ReceiptUUID,
			"action":       action,
			// "customer_invoice_uuid": req.CustomerInvoiceUUID,
			"customer_id":      receipt.CustomerID,
			"resulting_status": receipt.Status,
		},
		nil,
	)

	return &dto.SubmitDepositReceiptResponse{
		Success:     true,
		Message:     fmt.Sprintf("Receipt %s %sed", req.ReceiptUUID, req.Action),
		ReceiptUUID: req.ReceiptUUID,
		Status:      string(receipt.Status),
	}, nil
}

func paymentMetadataAdminID(raw json.RawMessage) uint {
	if len(raw) == 0 {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	v, ok := m["admin_id"]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		if x <= 0 {
			return 0
		}
		return uint(x)
	case int:
		if x <= 0 {
			return 0
		}
		return uint(x)
	case string:
		i, err := strconv.ParseUint(strings.TrimSpace(x), 10, 64)
		if err != nil || i == 0 {
			return 0
		}
		return uint(i)
	default:
		return 0
	}
}

func (p *PaymentFlowImpl) validateAdminListTransactionsRequest(req *dto.AdminListTransactionsRequest) error {
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

// convertTransactionHistoryRecordToTransactionHistoryItem converts a history projection into a transaction history item DTO.
func (p *PaymentFlowImpl) convertTransactionHistoryRecordToTransactionHistoryItem(
	transaction *models.Transaction,
) (dto.AdminTransactionItem, error) {
	if transaction == nil {
		return dto.AdminTransactionItem{}, fmt.Errorf("transaction history record is nil")
	}

	// Get human-readable status
	status := dto.TransactionStatusDisplay[transaction.Status]
	if status == "" {
		status = string(transaction.Status)
	}

	// Prepare external reference
	var externalRef *string
	if transaction.ExternalReference != "" {
		externalRef = &transaction.ExternalReference
	}

	var balanceBefore map[string]uint64
	var balanceAfter map[string]uint64

	if err := json.Unmarshal(transaction.BalanceBefore, &balanceBefore); err != nil {
		return dto.AdminTransactionItem{}, err
	}
	if err := json.Unmarshal(transaction.BalanceAfter, &balanceAfter); err != nil {
		return dto.AdminTransactionItem{}, err
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
	depositMethod := deriveDepositMethod(meta)

	customerInvoiceUUID := customerInvoiceUUIDFromMetadata(meta)
	customerFullName := buildAdminTransactionCustomerFullName(transaction.Customer)

	return dto.AdminTransactionItem{
		UUID:             transaction.UUID.String(),
		CustomerID:       transaction.CustomerID,
		CustomerFullName: customerFullName,
		Customer: dto.AdminTransactionCustomerInfo{
			RepresentativeFirstName: transaction.Customer.RepresentativeFirstName,
			RepresentativeLastName:  transaction.Customer.RepresentativeLastName,
			FullName:                customerFullName,
			RepresentativeMobile:    transaction.Customer.RepresentativeMobile,
			Email:                   transaction.Customer.Email,
			CompanyName:             transaction.Customer.CompanyName,
			CompanyPhone:            transaction.Customer.CompanyPhone,
			CompanyAddress:          transaction.Customer.CompanyAddress,
			PostalCode:              transaction.Customer.PostalCode,
			NationalID:              transaction.Customer.NationalID,
			AccountType:             transaction.Customer.AccountType.TypeName,
		},
		Status:              status,
		Amount:              amount,
		CustomerCredit:      customerCredit,
		AgencyShareWithTax:  agencyShareWithTax,
		Currency:            transaction.Currency,
		Operation:           operation,
		Source:              source,
		DepositMethod:       depositMethod,
		DateTime:            transaction.CreatedAt,
		ExternalRef:         externalRef,
		BalanceBefore:       balanceBefore,
		BalanceAfter:        balanceAfter,
		CustomerInvoiceUUID: customerInvoiceUUID,
		// Metadata:           meta,
	}, nil
}

func buildAdminTransactionCustomerFullName(customer models.Customer) string {
	return buildCustomerDisplayName(customer)
}

func buildCustomerDisplayName(customer models.Customer) string {
	fullName := strings.TrimSpace(customer.RepresentativeFirstName + " " + customer.RepresentativeLastName)
	if fullName != "" {
		return fullName
	}
	if customer.CompanyName != nil {
		return strings.TrimSpace(*customer.CompanyName)
	}
	return ""
}

func customerInvoiceUUIDFromMetadata(metadata map[string]any) *string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["customer_invoice_uuid"]
	if !ok || raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil
	}
	return utils.ToPtr(strings.TrimSpace(s))
}

func (p *PaymentFlowImpl) validateCustomerInvoiceUUIDForApproval(ctx context.Context, customerInvoiceUUID string) error {
	customerInvoiceUUID = strings.TrimSpace(customerInvoiceUUID)
	if customerInvoiceUUID == "" {
		return ErrDepositReceiptInvoiceRequired
	}
	if _, err := uuid.Parse(customerInvoiceUUID); err != nil {
		return ErrDepositReceiptInvoiceInvalid
	}
	exists, err := p.multimediaRepo.ExistsByUUID(ctx, customerInvoiceUUID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrDepositReceiptInvoiceInvalid
	}
	return nil
}
