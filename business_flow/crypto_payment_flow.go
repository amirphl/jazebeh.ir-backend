// Package businessflow contains the core business logic and use cases for crypto payment workflows
package businessflow

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CryptoPaymentFlow defines crypto payment operations
type CryptoPaymentFlow interface {
	CreateRequest(
		ctx context.Context,
		req *dto.CreateCryptoPaymentRequest,
		metadata *ClientMetadata,
	) (*dto.CreateCryptoPaymentResponse, error)
	GetStatus(
		ctx context.Context,
		req *dto.GetCryptoPaymentStatusRequest,
		metadata *ClientMetadata,
	) (*dto.GetCryptoPaymentStatusResponse, error)
	ManualVerify(
		ctx context.Context,
		req *dto.ManualVerifyCryptoDepositRequest,
		metadata *ClientMetadata,
	) (*dto.ManualVerifyCryptoDepositResponse, error)
	CancelRequest(ctx context.Context, req *dto.CancelCryptoPaymentRequest, metadata *ClientMetadata) error
	HandleOxapayWebhook(ctx context.Context, raw []byte, hmacHeader string, secret string, metadata *ClientMetadata) error
}

// CryptoPaymentFlowImpl implements CryptoPaymentFlow
type CryptoPaymentFlowImpl struct {
	cprRepo             repository.CryptoPaymentRequestRepository
	cdRepo              repository.CryptoDepositRepository
	walletRepo          repository.WalletRepository
	customerRepo        repository.CustomerRepository
	balanceSnapshotRepo repository.BalanceSnapshotRepository
	transactionRepo     repository.TransactionRepository
	auditRepo           repository.AuditLogRepository
	agencyDiscountRepo  repository.AgencyDiscountRepository
	providers           map[string]services.CryptoPaymentProvider // platform -> provider
	db                  *gorm.DB
	sysCfg              config.SystemConfig
	deploymentCfg       config.DeploymentConfig
}

func NewCryptoPaymentFlow(
	cprRepo repository.CryptoPaymentRequestRepository,
	cdRepo repository.CryptoDepositRepository,
	walletRepo repository.WalletRepository,
	customerRepo repository.CustomerRepository,
	balanceSnapshotRepo repository.BalanceSnapshotRepository,
	transactionRepo repository.TransactionRepository,
	auditRepo repository.AuditLogRepository,
	agencyDiscountRepo repository.AgencyDiscountRepository,
	providers map[string]services.CryptoPaymentProvider,
	db *gorm.DB,
	sysCfg config.SystemConfig,
	deploymentCfg config.DeploymentConfig,
) CryptoPaymentFlow {
	return &CryptoPaymentFlowImpl{
		cprRepo:             cprRepo,
		cdRepo:              cdRepo,
		walletRepo:          walletRepo,
		customerRepo:        customerRepo,
		balanceSnapshotRepo: balanceSnapshotRepo,
		transactionRepo:     transactionRepo,
		auditRepo:           auditRepo,
		agencyDiscountRepo:  agencyDiscountRepo,
		providers:           providers,
		db:                  db,
		sysCfg:              sysCfg,
		deploymentCfg:       deploymentCfg,
	}
}

func (f *CryptoPaymentFlowImpl) CreateRequest(
	ctx context.Context,
	req *dto.CreateCryptoPaymentRequest,
	metadata *ClientMetadata,
) (*dto.CreateCryptoPaymentResponse, error) {
	if req.AmountWithTax < 1000 {
		return nil, ErrAmountTooLow
	}
	if req.Coin == "" || req.Network == "" || req.Platform == "" {
		return nil, NewBusinessError("CRYPTO_REQUEST_VALIDATION_FAILED", "coin/network/platform are required", nil)
	}

	provider, ok := f.providers[req.Platform]
	if !ok {
		return nil, ErrCryptoUnsupportedPlatform
	}

	var customer models.Customer
	var wallet models.Wallet
	var agencyDiscount *models.AgencyDiscount
	var cpr *models.CryptoPaymentRequest
	var paymentURL *string

	err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		var err error
		customer, err = getCustomer(txCtx, f.customerRepo, req.CustomerID)
		if err != nil {
			return err
		}
		wallet, err = getWallet(txCtx, f.walletRepo, customer.ID)
		if err != nil {
			return err
		}
		customer.Wallet = &wallet

		if customer.ReferrerAgencyID == nil {
			return ErrReferrerAgencyIDRequired
		}
		agencyDiscount, err = f.agencyDiscountRepo.GetActiveDiscount(txCtx, *customer.ReferrerAgencyID, customer.ID)
		if err != nil {
			return err
		}
		if agencyDiscount == nil {
			return ErrAgencyDiscountNotFound
		}

		// Compute shares metadata similar to fiat flow
		// scattered, err := f.calculateShares(txCtx, customer, req.AmountWithTax)
		// if err != nil {
		// 	return err
		// }

		metadataMap := map[string]any{
			"source":          "crypto_wallet_recharge",
			"amount_with_tax": req.AmountWithTax,
			// "system_share_with_tax": scattered[0].Amount,
			// "agency_share_with_tax": scattered[1].Amount,
			"system_share_with_tax": req.AmountWithTax,
			"agency_share_with_tax": 0,
			"agency_discount_id":    agencyDiscount.ID,
			"agency_id":             customer.ReferrerAgencyID,
			"customer_id":           customer.ID,
		}
		metaJSON, _ := json.Marshal(metadataMap)

		// Create request in created state
		cpr = &models.CryptoPaymentRequest{
			UUID:            uuid.New(),
			CorrelationID:   uuid.New(),
			CustomerID:      customer.ID,
			WalletID:        wallet.ID,
			FiatAmountToman: req.AmountWithTax,
			FiatCurrency:    utils.TomanCurrency,
			Coin:            models.CryptoCurrency(req.Coin),
			Network:         req.Network,
			Platform:        models.CryptoPlatform(req.Platform),
			// ExpectedCoinAmount: ,
			// ExchangeRate: ,
			// RateSource: ,
			// DepositAddress: ,
			// DepositMemo: ,
			// ProviderRequestID: ,
			Status:       models.CryptoPaymentStatusCreated,
			StatusReason: "crypto payment request created",
			// ExpiresAt: ,
			// DetectedAt: ,
			// ConfirmedAt: ,
			// CreditedAt: ,
			Metadata: json.RawMessage(metaJSON),
		}
		if err := f.cprRepo.Save(txCtx, cpr); err != nil {
			return err
		}

		// Quote and provision address
		quote, err := provider.GetQuote(txCtx, services.QuoteInput{
			FiatAmountToman: req.AmountWithTax,
			Coin:            req.Coin,
			Network:         req.Network,
		})
		if err != nil {
			return NewBusinessError("CRYPTO_PROVIDER_QUOTE_FAILED", "Failed to get quote from provider", fmt.Errorf("%w", err))
		}
		callbackURL := fmt.Sprintf(
			"https://%s/api/v1/crypto/providers/%s/callback",
			f.deploymentCfg.APIDomain,
			strings.ToLower(string(cpr.Platform)),
		)
		prov, err := provider.ProvisionDeposit(txCtx, services.ProvisionInput{
			QuoteInput: services.QuoteInput{
				FiatAmountToman: req.AmountWithTax,
				Coin:            req.Coin,
				Network:         req.Network,
			},
			Label: cpr.UUID.String(),
			// CallbackURL: callbackURL,
			CallbackURL: "", // TODO:
		})
		if err != nil {
			return NewBusinessError("CRYPTO_ADDRESS_PROVISION_FAILED", "Failed to provision deposit address", fmt.Errorf("%w", err))
		}

		cpr.ExpectedCoinAmount = quote.ExpectedCoinAmount
		cpr.ExchangeRate = quote.ExchangeRate
		cpr.DepositAddress = prov.DepositAddress
		cpr.DepositMemo = prov.DepositMemo
		cpr.ProviderRequestID = prov.ProviderRequestID
		cpr.Status = models.CryptoPaymentStatusAddressProvisioned
		cpr.StatusReason = "deposit address provisioned"
		cpr.ExpiresAt = prov.ExpiresAt
		cpr.DetectedAt = nil
		cpr.ConfirmedAt = nil
		cpr.CreditedAt = nil
		if err := f.cprRepo.Update(txCtx, cpr); err != nil {
			return err
		}

		// If provider supports invoice (oxapay), create invoice and store payment URL in metadata
		if strings.EqualFold(req.Platform, "oxapay") {
			if invProv, ok := provider.(interface {
				CreateInvoice(context.Context, services.OxapayInvoiceInput) (*services.OxapayInvoiceResult, error)
			}); ok {
				inv, invErr := invProv.CreateInvoice(txCtx, services.OxapayInvoiceInput{
					FiatAmountToman: req.AmountWithTax,
					CallbackURL:     callbackURL,
					ReturnURL:       fmt.Sprintf("https://%s/dashboard/wallet", f.deploymentCfg.Domain),
					Label:           cpr.UUID.String(),
					LifetimeMin:     60,
					MixedPayment:    true,
					AutoWithdrawal:  false,
					Email:           "",
					ThanksMessage:   "",
					Description:     "Wallet recharge via crypto",
					Sandbox:         false,
				})
				if invErr == nil && inv != nil {
					if inv.PaymentURL != "" {
						pu := inv.PaymentURL
						paymentURL = &pu
					}
					// Persist track_id as provider request id for webhook/status correlation
					if inv.TrackID != "" {
						cpr.ProviderRequestID = inv.TrackID
						_ = f.cprRepo.Update(txCtx, cpr)
					}
					// persist extras to metadata
					var m map[string]any
					_ = json.Unmarshal(cpr.Metadata, &m)
					if m == nil {
						m = map[string]any{}
					}
					m["oxapay_payment_url"] = inv.PaymentURL
					m["oxapay_track_id"] = inv.TrackID
					if inv.ExpiredAt != nil {
						m["oxapay_expired_at"] = inv.ExpiredAt.UTC().Format(time.RFC3339)
					}
					b, _ := json.Marshal(m)
					cpr.Metadata = b
					_ = f.cprRepo.Update(txCtx, cpr)
				}
			}
		}

		// Move to pending
		cpr.Status = models.CryptoPaymentStatusPending
		cpr.StatusReason = "awaiting user crypto payment"
		if cpr.ExpiresAt == nil {
			// default: 60 minutes window
			exp := utils.UTCNow().Add(60 * time.Minute)
			cpr.ExpiresAt = &exp
		}
		if err := f.cprRepo.Update(txCtx, cpr); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf("Create crypto payment request failed for customer %d: %s", req.CustomerID, err.Error())
		_ = createAuditLog(ctx, f.auditRepo, &customer, models.AuditActionWalletChargeFailed, errMsg, false, &errMsg, metadata)
		return nil, NewBusinessError("CRYPTO_CREATE_REQUEST_FAILED", "Failed to create crypto payment request", err)
	}

	expiresStr := (*string)(nil)
	if cpr.ExpiresAt != nil {
		s := cpr.ExpiresAt.UTC().Format(time.RFC3339)
		expiresStr = &s
	}
	memo := (*string)(nil)
	if cpr.DepositMemo != "" {
		m := cpr.DepositMemo
		memo = &m
	}
	resp := &dto.CreateCryptoPaymentResponse{
		RequestUUID:        cpr.UUID.String(),
		DepositAddress:     cpr.DepositAddress,
		DepositMemo:        memo,
		ExpectedCoinAmount: cpr.ExpectedCoinAmount,
		ExchangeRate:       cpr.ExchangeRate,
		RateSource:         "provider",
		ExpiresAt:          expiresStr,
		PaymentURL:         paymentURL,
	}
	msg := fmt.Sprintf("Generated crypto deposit address for request %s", cpr.UUID.String())
	_ = createAuditLog(ctx, f.auditRepo, &customer, models.AuditActionWalletChargeCompleted, msg, true, nil, metadata)
	return resp, nil
}

func (f *CryptoPaymentFlowImpl) GetStatus(
	ctx context.Context,
	req *dto.GetCryptoPaymentStatusRequest,
	metadata *ClientMetadata,
) (*dto.GetCryptoPaymentStatusResponse, error) {
	if req.UUID == "" {
		return nil, NewBusinessError("CRYPTO_STATUS_VALIDATION_FAILED", "uuid is required", nil)
	}

	var cpr *models.CryptoPaymentRequest
	var customer models.Customer
	var provider services.CryptoPaymentProvider
	var deposits []*models.CryptoDeposit
	err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		var err error
		uid, err := uuid.Parse(req.UUID)
		if err != nil {
			return err
		}
		cpr, err = f.cprRepo.ByUUID(txCtx, uid.String())
		if err != nil {
			return err
		}
		if cpr == nil {
			return ErrCryptoRequestNotFound
		}
		customer, err = getCustomer(txCtx, f.customerRepo, cpr.CustomerID)
		if err != nil {
			return err
		}
		if customer.ID != req.CustomerID {
			return ErrCustomerNotFound
		}

		provider = f.providers[string(cpr.Platform)]
		if provider == nil {
			return ErrCryptoUnsupportedPlatform
		}

		// mark expired if time passed and still pending
		if cpr.Status == models.CryptoPaymentStatusPending && cpr.ExpiresAt != nil && cpr.ExpiresAt.Before(utils.UTCNow()) {
			cpr.Status = models.CryptoPaymentStatusExpired
			cpr.StatusReason = "payment window expired"
			_ = f.cprRepo.Update(txCtx, cpr)
		}

		// Special handling: Oxapay invoice status via track_id
		if strings.EqualFold(string(cpr.Platform), "oxapay") {
			var meta map[string]any
			_ = json.Unmarshal(cpr.Metadata, &meta)
			if meta != nil {
				trackID, _ := meta["oxapay_track_id"].(string)
				if strings.TrimSpace(trackID) != "" {
					if infoProv, ok := provider.(interface {
						GetPaymentInfo(context.Context, string) (*services.OxapayPaymentInfo, error)
					}); ok {
						info, ierr := infoProv.GetPaymentInfo(txCtx, trackID)
						if ierr == nil && info != nil {
							// update request status from invoice status table
							st, reason := mapOxapayInvoiceStatus(info.Status)
							cpr.Status = st
							cpr.StatusReason = "oxapay:" + reason
							_ = f.cprRepo.Update(txCtx, cpr)

							b, _ := json.Marshal(info)

							// upsert txs
							for _, t := range info.Txs {
								existing, _ := f.cdRepo.ByTxHash(txCtx, t.TxHash)
								if existing == nil {
									dep := &models.CryptoDeposit{
										UUID:                   uuid.New(),
										CorrelationID:          cpr.CorrelationID,
										CryptoPaymentRequestID: &cpr.ID,
										CustomerID:             cpr.CustomerID,
										WalletID:               cpr.WalletID,
										Coin:                   cpr.Coin,
										Network:                cpr.Network,
										Platform:               cpr.Platform,
										TxHash:                 t.TxHash,
										FromAddress:            "",
										ToAddress:              t.Address,
										AmountCoin:             fmt.Sprintf("%g", t.Amount),
										Confirmations:          t.Confirmations,
										RequiredConfirmations:  0,
										// BlockHeight: ,
										// DetectedAt: ,
										// ConfirmedAt: ,
										// CreditedAt: ,
										Status:   mapOxapayTxStatus(t.Status),
										Metadata: b,
									}
									if t.Date > 0 {
										dt := time.Unix(t.Date, 0).UTC()
										dep.DetectedAt = &dt
									}
									if strings.EqualFold(t.Status, "confirmed") {
										now := utils.UTCNow()
										dep.ConfirmedAt = &now
									}
									_ = f.cdRepo.Save(txCtx, dep)
								} else {
									existing.Confirmations = t.Confirmations
									existing.Status = mapOxapayTxStatus(t.Status)
									existing.Metadata = b
									if strings.EqualFold(t.Status, "confirmed") && existing.ConfirmedAt == nil {
										now := utils.UTCNow()
										existing.ConfirmedAt = &now
									}
									_ = f.cdRepo.Update(txCtx, existing)
								}
							}
							// credit on invoice paid
							if strings.EqualFold(info.Status, "paid") || strings.EqualFold(info.Status, "manual_accept") {
								// fetch current deposits for this request
								ds, _ := f.cdRepo.ByFilter(
									txCtx,
									models.CryptoDepositFilter{CryptoPaymentRequestID: &cpr.ID},
									"id ASC",
									100,
									0,
								)
								for _, d := range ds {
									if d.CreditedAt == nil && d.ConfirmedAt != nil {
										if err := f.creditOnConfirmed(txCtx, cpr, d, metadata); err != nil {
											// proceed but update status reason
											s := fmt.Sprintf("credit failed: %v", err)
											cpr.Status = models.CryptoPaymentStatusFailed
											cpr.StatusReason = s
											_ = f.cprRepo.Update(txCtx, cpr)
											log.Printf("credit on confirmed failed: %v", err)
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// pull provider deposits if any (for providers with polling)
		provDeposits, perr := provider.GetDeposits(txCtx, cpr.ProviderRequestID)
		if perr == nil && len(provDeposits) > 0 {
			for _, d := range provDeposits {
				dep := &models.CryptoDeposit{
					UUID:                   uuid.New(),
					CorrelationID:          cpr.CorrelationID,
					CryptoPaymentRequestID: &cpr.ID,
					CustomerID:             cpr.CustomerID,
					WalletID:               cpr.WalletID,
					Coin:                   cpr.Coin,
					Network:                cpr.Network,
					Platform:               cpr.Platform,
					TxHash:                 d.TxHash,
					FromAddress:            "",
					ToAddress:              d.ToAddress,
					DestinationTag:         d.DestinationTag,
					AmountCoin:             d.AmountCoin,
					Confirmations:          d.Confirmations,
					RequiredConfirmations:  d.RequiredConfirmations,
					// BlockHeight: ,
					// DetectedAt:             d.DetectedAt,
					// ConfirmedAt:            d.ConfirmedAt,
					// CreditedAt:             d.CreditedAt,
					Status: d.Status,
				}
				dep.DetectedAt = d.DetectedAt
				dep.ConfirmedAt = d.ConfirmedAt
				dep.CreditedAt = d.CreditedAt
				_ = f.cdRepo.Save(txCtx, dep)
			}
		}
		// fetch current deposits
		deposits, err = f.cdRepo.ByFilter(txCtx, models.CryptoDepositFilter{
			CryptoPaymentRequestID: &cpr.ID,
		}, "created_at ASC", 100, 0)
		if err != nil {
			return err
		}

		// finalize on confirmed not yet credited
		for _, dep := range deposits {
			// For OxaPay, only auto-credit after invoice is paid (mapped to Confirmed)
			// if strings.EqualFold(string(cpr.Platform), "oxapay") && cpr.Status != models.CryptoPaymentStatusConfirmed {
			// 	continue
			// }
			if dep.CreditedAt == nil && dep.ConfirmedAt != nil && cpr.CreditedAt == nil {
				if err := f.creditOnConfirmed(txCtx, cpr, dep, metadata); err != nil {
					// proceed but update status reason
					s := fmt.Sprintf("credit failed: %v", err)
					cpr.Status = models.CryptoPaymentStatusFailed
					cpr.StatusReason = s
					_ = f.cprRepo.Update(txCtx, cpr)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, NewBusinessError("CRYPTO_STATUS_FAILED", "Failed to get crypto payment status", err)
	}

	depos := make([]dto.DepositInfoDTO, 0, len(deposits))
	for _, d := range deposits {
		depos = append(depos, dto.DepositInfoDTO{
			TxHash:                d.TxHash,
			AmountCoin:            d.AmountCoin,
			Confirmations:         d.Confirmations,
			RequiredConfirmations: d.RequiredConfirmations,
			Status:                d.Status,
			DetectedAt:            dto.FormatTime(d.DetectedAt),
			ConfirmedAt:           dto.FormatTime(d.ConfirmedAt),
			CreditedAt:            dto.FormatTime(d.CreditedAt),
		})
	}
	expiresStr := dto.FormatTime(cpr.ExpiresAt)
	memo := (*string)(nil)
	if cpr.DepositMemo != "" {
		m := cpr.DepositMemo
		memo = &m
	}
	resp := &dto.GetCryptoPaymentStatusResponse{
		Status:       string(cpr.Status),
		StatusReason: cpr.StatusReason,
		FiatAmount:   cpr.FiatAmountToman,
		Coin:         string(cpr.Coin),
		Network:      cpr.Network,
		Platform:     string(cpr.Platform),
		ExpectedCoin: cpr.ExpectedCoinAmount,
		DepositAddr:  cpr.DepositAddress,
		DepositMemo:  memo,
		Deposits:     depos,
		ExpiresAt:    expiresStr,
	}
	return resp, nil
}

// TODO: Test
func (f *CryptoPaymentFlowImpl) ManualVerify(
	ctx context.Context,
	req *dto.ManualVerifyCryptoDepositRequest,
	metadata *ClientMetadata,
) (*dto.ManualVerifyCryptoDepositResponse, error) {
	if req.RequestUUID == "" || req.TxHash == "" {
		return nil, NewBusinessError("CRYPTO_VERIFY_VALIDATION_FAILED", "request_uuid and tx_hash are required", nil)
	}
	uid, err := uuid.Parse(req.RequestUUID)
	if err != nil {
		return nil, err
	}

	var cpr *models.CryptoPaymentRequest
	var customer models.Customer
	var dep *models.CryptoDeposit
	var provider services.CryptoPaymentProvider
	err = repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		var err error
		cpr, err = f.cprRepo.ByUUID(txCtx, uid.String())
		if err != nil {
			return err
		}
		if cpr == nil {
			return ErrCryptoRequestNotFound
		}
		customer, err = getCustomer(txCtx, f.customerRepo, cpr.CustomerID)
		if err != nil {
			return err
		}
		if customer.ID != req.CustomerID {
			return ErrCustomerNotFound
		}

		provider = f.providers[string(cpr.Platform)]
		if provider == nil {
			return ErrCryptoUnsupportedPlatform
		}

		info, err := provider.VerifyTx(txCtx, req.TxHash)
		if err != nil {
			return NewBusinessError("CRYPTO_PROVIDER_VERIFY_FAILED", "Provider verify tx failed", err)
		}

		// upsert deposit
		existing, _ := f.cdRepo.ByTxHash(txCtx, req.TxHash)
		if existing == nil {
			dep = &models.CryptoDeposit{
				UUID:                   uuid.New(),
				CorrelationID:          cpr.CorrelationID,
				CryptoPaymentRequestID: &cpr.ID,
				CustomerID:             cpr.CustomerID,
				WalletID:               cpr.WalletID,
				Coin:                   cpr.Coin,
				Network:                cpr.Network,
				Platform:               cpr.Platform,
				TxHash:                 info.TxHash,
				// FromAddress: ,
				ToAddress:             info.ToAddress,
				DestinationTag:        info.DestinationTag,
				AmountCoin:            info.AmountCoin,
				Confirmations:         info.Confirmations,
				RequiredConfirmations: info.RequiredConfirmations,
				// BlockHeight: ,
				DetectedAt:  info.DetectedAt,
				ConfirmedAt: info.ConfirmedAt,
				CreditedAt:  info.CreditedAt,
				Status:      info.Status,
			}
			if err := f.cdRepo.Save(txCtx, dep); err != nil {
				return err
			}
		} else {
			dep = existing
			dep.Confirmations = info.Confirmations
			dep.RequiredConfirmations = info.RequiredConfirmations
			dep.ConfirmedAt = info.ConfirmedAt
			dep.CreditedAt = info.CreditedAt
			dep.Status = info.Status
			if err := f.cdRepo.Update(txCtx, dep); err != nil {
				return err
			}
		}

		if dep.CreditedAt == nil && dep.ConfirmedAt != nil && cpr.CreditedAt == nil {
			// ISSUE: cpr.CreditedAt == nil
			if err := f.creditOnConfirmed(txCtx, cpr, dep, metadata); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, NewBusinessError("CRYPTO_VERIFY_FAILED", "Manual deposit verification failed", err)
	}

	resp := &dto.ManualVerifyCryptoDepositResponse{
		Status:     string(models.CryptoPaymentStatusCredited),
		Credited:   dep.CreditedAt != nil,
		CreditedAt: dto.FormatTime(dep.CreditedAt),
	}
	return resp, nil
}

// TODO: Test
func (f *CryptoPaymentFlowImpl) CancelRequest(ctx context.Context, req *dto.CancelCryptoPaymentRequest, metadata *ClientMetadata) error {
	uid, err := uuid.Parse(req.UUID)
	if err != nil {
		return err
	}
	err = repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		cpr, err := f.cprRepo.ByUUID(txCtx, uid.String())
		if err != nil {
			return err
		}
		if cpr == nil {
			return ErrCryptoRequestNotFound
		}
		if cpr.CustomerID != req.CustomerID {
			return ErrCustomerNotFound
		}
		if cpr.IsFinal() {
			return ErrCryptoRequestAlreadyFinalized
		}
		// Disallow cancel if any deposit detected
		deps, err := f.cdRepo.ByFilter(txCtx, models.CryptoDepositFilter{
			CryptoPaymentRequestID: &cpr.ID,
		}, "id ASC", 1, 0)
		if err != nil {
			return err
		}
		if len(deps) > 0 {
			return NewBusinessError("CRYPTO_CANCEL_NOT_ALLOWED", "Cannot cancel after deposit detected", nil)
		}
		cpr.Status = models.CryptoPaymentStatusCancelled
		cpr.StatusReason = "cancelled by user"
		cpr.UpdatedAt = utils.UTCNow()
		if err := f.cprRepo.Update(txCtx, cpr); err != nil {
			return err
		}
		cust := models.Customer{ID: cpr.CustomerID}
		msg := fmt.Sprintf("Crypto payment request %s cancelled", cpr.UUID.String())
		_ = createAuditLog(txCtx, f.auditRepo, &cust, models.AuditActionWalletChargeFailed, msg, true, nil, metadata)
		return nil
	})
	if err != nil {
		return NewBusinessError("CRYPTO_CANCEL_FAILED", "Failed to cancel crypto payment request", err)
	}
	return nil
}

// calculateShares mirrors payment_flow.calculateScatteredSettlementItems minimally for metadata preparation
// func (f *CryptoPaymentFlowImpl) calculateShares(ctx context.Context, customer models.Customer, amountWithTax uint64)
// ([]ScatteredSettlementItem, error) {
// 	var systemShareWithTax uint64
// 	var agencyShareWithTax uint64

// 	discountRate, _, err := f.getAgencyDiscountRate(ctx, customer)
// 	if err != nil {
// 		return nil, err
// 	}
// 	x := float64(amountWithTax) / (1 - discountRate)
// 	systemShareWithTax = uint64(x / 2)
// 	agencyShareWithTax = uint64(amountWithTax - systemShareWithTax)
// 	items := []ScatteredSettlementItem{
// 		{Amount: systemShareWithTax, IBAN: ""},
// 		{Amount: agencyShareWithTax, IBAN: ""},
// 	}
// 	return items, nil
// }

func (f *CryptoPaymentFlowImpl) HandleOxapayWebhook(
	ctx context.Context,
	raw []byte,
	hmacHeader string,
	secret string,
	metadata *ClientMetadata,
) error {
	if len(raw) == 0 || hmacHeader == "" {
		return NewBusinessError("CRYPTO_WEBHOOK_INVALID", "missing body or HMAC header", nil)
	}
	if !verifyOxapayHMAC(raw, hmacHeader, secret) {
		return NewBusinessError("CRYPTO_WEBHOOK_FORBIDDEN", "invalid HMAC signature", nil)
	}
	var payload dto.OxapayWebhookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return NewBusinessError("CRYPTO_WEBHOOK_INVALID", "invalid json", err)
	}
	if strings.ToLower(payload.Type) != "static_address" && strings.ToLower(payload.Type) != "invoice" {
		return nil
	}
	// Use first tx for address matching (handled inside transaction if needed)
	if len(payload.Txs) == 0 {
		return nil
	}
	// Upsert deposit per txs
	return repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		// Resolve by track_id first for invoice/static_address callbacks
		var cpr *models.CryptoPaymentRequest
		var err error
		if payload.TrackID != "" {
			cpr, err = f.cprRepo.ByProviderRequestID(txCtx, payload.TrackID)
			if err != nil {
				return err
			}
		}
		if cpr == nil {
			// fallback: by address (first tx)
			addr := payload.Txs[0].Address
			reqs, err := f.cprRepo.ByDepositAddress(txCtx, addr, "")
			if err != nil {
				return err
			}
			if len(reqs) == 0 {
				return ErrCryptoRequestNotFound
			}
			cpr = reqs[0]
		}
		// Update request status from invoice status mapping (paying/paid)
		st, reason := mapOxapayInvoiceStatus(payload.Status)
		cpr.Status = st
		cpr.StatusReason = "oxapay:" + reason
		_ = f.cprRepo.Update(txCtx, cpr)

		for _, t := range payload.Txs {
			dep, _ := f.cdRepo.ByTxHash(txCtx, t.TxHash)
			if dep == nil {
				dep = &models.CryptoDeposit{
					UUID:                   uuid.New(),
					CorrelationID:          cpr.CorrelationID,
					CryptoPaymentRequestID: &cpr.ID,
					CustomerID:             cpr.CustomerID,
					WalletID:               cpr.WalletID,
					Coin:                   cpr.Coin,
					Network:                cpr.Network,
					Platform:               cpr.Platform,
					TxHash:                 t.TxHash,
					FromAddress:            t.SenderAddress,
					ToAddress:              t.Address,
					// DestinationTag: ,
					AmountCoin:            fmt.Sprintf("%g", t.SentAmount),
					Confirmations:         t.Confirmations,
					RequiredConfirmations: 0,
					// BlockHeight: ,
					// DetectedAt: ,
					// ConfirmedAt: ,
					// CreditedAt: ,
					Status:   mapOxapayTxStatus(t.Status),
					Metadata: raw,
				}
				if t.Date > 0 {
					dt := time.Unix(t.Date, 0).UTC()
					dep.DetectedAt = &dt
				}
				if strings.EqualFold(t.Status, "confirmed") {
					now := utils.UTCNow()
					dep.ConfirmedAt = &now
				}
				if err := f.cdRepo.Save(txCtx, dep); err != nil {
					return err
				}
			} else {
				dep.Confirmations = t.Confirmations
				dep.Status = mapOxapayTxStatus(t.Status)
				if strings.EqualFold(t.Status, "confirmed") && dep.ConfirmedAt == nil {
					now := utils.UTCNow()
					dep.ConfirmedAt = &now
				}
				if err := f.cdRepo.Update(txCtx, dep); err != nil {
					return err
				}
				// Credit on Paid once per request
				if (strings.EqualFold(payload.Status, "paid") || strings.EqualFold(payload.Status, "manual_accept")) && dep.ConfirmedAt != nil && dep.CreditedAt == nil && cpr.CreditedAt == nil {
					if err := f.creditOnConfirmed(txCtx, cpr, dep, metadata); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func mapOxapayTxStatus(s string) string {
	s = strings.ToLower(s)
	switch s {
	case "confirming", "paying":
		return "detected"
	case "confirmed", "paid":
		return "confirmed"
	default:
		return s
	}
}

func mapOxapayInvoiceStatus(s string) (models.CryptoPaymentStatus, string) {
	sx := strings.ToLower(strings.TrimSpace(s))
	switch sx {
	case "new", "waiting", "paying":
		return models.CryptoPaymentStatusPending, sx
	case "paid", "manual_accept":
		return models.CryptoPaymentStatusConfirmed, sx
	case "underpaid":
		return models.CryptoPaymentStatusPending, "underpaid"
	case "expired":
		return models.CryptoPaymentStatusExpired, sx
	case "refunding", "refunded":
		return models.CryptoPaymentStatusFailed, sx
	default:
		return models.CryptoPaymentStatusPending, sx
	}
}

func verifyOxapayHMAC(raw []byte, hmacHeader, secret string) bool {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	return strings.EqualFold(expected, hmacHeader)
}

func (f *CryptoPaymentFlowImpl) getAgencyDiscountRate(ctx context.Context, customer models.Customer) (float64, string, error) {
	agency, err := getAgency(ctx, f.customerRepo, *customer.ReferrerAgencyID)
	if err != nil {
		return 0, "", err
	}
	ad, err := f.agencyDiscountRepo.GetActiveDiscount(ctx, agency.ID, customer.ID)
	if err != nil {
		return 0, "", err
	}
	var discountRate float64
	if ad != nil {
		discountRate = ad.DiscountRate
	}
	sheba := ""
	if agency.ShebaNumber != nil {
		sheba = *agency.ShebaNumber
	}
	return discountRate, sheba, nil
}

func (f *CryptoPaymentFlowImpl) creditOnConfirmed(
	ctx context.Context,
	cpr *models.CryptoPaymentRequest,
	dep *models.CryptoDeposit,
	metadata *ClientMetadata,
) error {
	// decode metadata
	var m map[string]any
	if err := json.Unmarshal(cpr.Metadata, &m); err != nil {
		return err
	}
	realWithTax := uint64(m["amount_with_tax"].(float64))
	systemShareWithTax := uint64(m["system_share_with_tax"].(float64))
	agencyShareWithTax := uint64(m["agency_share_with_tax"].(float64))
	agencyDiscountID := uint(m["agency_discount_id"].(float64))
	agencyID := uint(m["agency_id"].(float64))

	// wallets & balances
	agencyWallet, err := getWallet(ctx, f.walletRepo, agencyID)
	if err != nil {
		return err
	}
	taxWallet, err := getTaxWallet(ctx, f.walletRepo, f.sysCfg)
	if err != nil {
		return err
	}
	systemWallet, err := getSystemWallet(ctx, f.walletRepo, f.sysCfg)
	if err != nil {
		return err
	}
	customerBalance, err := getLatestBalanceSnapshot(ctx, f.walletRepo, cpr.WalletID)
	if err != nil {
		return err
	}
	agencyBalance, err := getLatestBalanceSnapshot(ctx, f.walletRepo, agencyWallet.ID)
	if err != nil {
		return err
	}
	taxBalance, err := getLatestTaxWalletBalanceSnapshot(ctx, f.walletRepo, taxWallet.ID)
	if err != nil {
		return err
	}
	systemBalance, err := getLatestSystemWalletBalanceSnapshot(ctx, f.walletRepo, systemWallet.ID)
	if err != nil {
		return err
	}
	agencyDiscount, err := f.agencyDiscountRepo.ByID(ctx, agencyDiscountID)
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

	metadataMap := map[string]any{
		"customer_id":               cpr.CustomerID,
		"agency_id":                 agencyID,
		"agency_discount_id":        agencyDiscountID,
		"source":                    "crypto_payment_callback",
		"operation":                 "increase_balance",
		"crypto_payment_request_id": cpr.ID,
		"amount_with_tax":           realWithTax,
		"amount":                    real,
		"tax":                       tax,
		"system_share_with_tax":     systemShareWithTax,
		"system_share":              realSystemShare,
		"system_share_tax":          taxSystemShare,
		"agency_share_with_tax":     agencyShareWithTax,
		"agency_share":              realAgencyShare,
		"agency_share_tax":          taxAgencyShare,
		"customer_credit":           customerCredit,
		"tx_hash":                   dep.TxHash,
	}

	// Update customer balance
	newCustomerFree := customerBalance.FreeBalance + real
	newCustomerCredit := customerBalance.CreditBalance + customerCredit
	metadataMap["source"] = "crypto_increase_customer_free_plus_credit"
	metadataMap["operation"] = "increase_customer_free_plus_credit"
	b, _ := json.Marshal(metadataMap)
	newCustomerBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      cpr.CorrelationID,
		WalletID:           cpr.WalletID,
		CustomerID:         cpr.CustomerID,
		FreeBalance:        newCustomerFree,
		FrozenBalance:      customerBalance.FrozenBalance,
		LockedBalance:      customerBalance.LockedBalance,
		CreditBalance:      newCustomerCredit,
		SpentOnCampaign:    customerBalance.SpentOnCampaign,
		AgencyShareWithTax: customerBalance.AgencyShareWithTax,
		TotalBalance:       newCustomerFree + newCustomerCredit + customerBalance.FrozenBalance + customerBalance.LockedBalance + customerBalance.SpentOnCampaign + customerBalance.AgencyShareWithTax,
		Reason:             "crypto_wallet_recharge",
		Description:        fmt.Sprintf("Wallet recharged via crypto (request %d)", cpr.ID),
		Metadata:           b,
	}
	if err := f.balanceSnapshotRepo.Save(ctx, newCustomerBS); err != nil {
		return err
	}
	before, _ := customerBalance.GetBalanceMap()
	after, _ := newCustomerBS.GetBalanceMap()
	customerDepositTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     cpr.CorrelationID,
		Type:              models.TransactionTypeDeposit,
		Status:            models.TransactionStatusCompleted,
		Amount:            real + customerCredit,
		Currency:          utils.TomanCurrency,
		WalletID:          cpr.WalletID,
		CustomerID:        cpr.CustomerID,
		BalanceBefore:     before,
		BalanceAfter:      after,
		ExternalReference: dep.TxHash,
		ExternalTrace:     dep.TxHash,
		ExternalRRN:       "",
		ExternalMaskedPAN: "",
		Description:       fmt.Sprintf("Crypto wallet recharge (request %d)", cpr.ID),
		Metadata:          b,
	}
	if err := f.transactionRepo.Save(ctx, customerDepositTx); err != nil {
		return err
	}

	// Update agency balance
	newAgencyShareWithTax := agencyBalance.AgencyShareWithTax + agencyShareWithTax
	metadataMap["source"] = "crypto_increase_agency_share_with_tax"
	metadataMap["operation"] = "increase_agency_share_with_tax"
	b, _ = json.Marshal(metadataMap)
	agencyBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      cpr.CorrelationID,
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
		Description:        fmt.Sprintf("Agency share for crypto request %d", cpr.ID),
		Metadata:           b,
	}
	if err := f.balanceSnapshotRepo.Save(ctx, agencyBS); err != nil {
		return err
	}
	ab, _ := agencyBalance.GetBalanceMap()
	aa, _ := agencyBS.GetBalanceMap()
	agencyChargeTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     cpr.CorrelationID,
		Type:              models.TransactionTypeChargeAgencyShareWithTax,
		Status:            models.TransactionStatusCompleted,
		Amount:            agencyShareWithTax,
		Currency:          utils.TomanCurrency,
		WalletID:          agencyWallet.ID,
		CustomerID:        agencyWallet.CustomerID,
		BalanceBefore:     ab,
		BalanceAfter:      aa,
		ExternalReference: dep.TxHash,
		ExternalTrace:     dep.TxHash,
		ExternalRRN:       "",
		ExternalMaskedPAN: "",
		Description:       fmt.Sprintf("Agency share for crypto request %d", cpr.ID),
		Metadata:          b,
	}
	if err := f.transactionRepo.Save(ctx, agencyChargeTx); err != nil {
		return err
	}

	// Tax locked
	newTaxLocked := taxBalance.LockedBalance + taxSystemShare
	metadataMap["source"] = "crypto_increase_tax_locked_(tax_system_share)"
	metadataMap["operation"] = "increase_tax_locked"
	b, _ = json.Marshal(metadataMap)
	taxBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      cpr.CorrelationID,
		WalletID:           taxWallet.ID,
		CustomerID:         taxWallet.CustomerID,
		FreeBalance:        taxBalance.FreeBalance,
		FrozenBalance:      taxBalance.FrozenBalance,
		LockedBalance:      newTaxLocked,
		CreditBalance:      taxBalance.CreditBalance,
		SpentOnCampaign:    taxBalance.SpentOnCampaign,
		AgencyShareWithTax: taxBalance.AgencyShareWithTax,
		TotalBalance:       taxBalance.FreeBalance + taxBalance.FrozenBalance + newTaxLocked + taxBalance.CreditBalance + taxBalance.SpentOnCampaign + taxBalance.AgencyShareWithTax,
		Reason:             "tax_collection",
		Description:        fmt.Sprintf("Tax collection for crypto request %d", cpr.ID),
		Metadata:           b,
	}
	if err := f.balanceSnapshotRepo.Save(ctx, taxBS); err != nil {
		return err
	}
	tbb, _ := taxBalance.GetBalanceMap()
	tba, _ := taxBS.GetBalanceMap()
	taxTx := &models.Transaction{UUID: uuid.New(),
		CorrelationID:     cpr.CorrelationID,
		Type:              models.TransactionTypeLock,
		Status:            models.TransactionStatusCompleted,
		Amount:            taxSystemShare,
		Currency:          utils.TomanCurrency,
		WalletID:          taxWallet.ID,
		CustomerID:        taxWallet.CustomerID,
		BalanceBefore:     tbb,
		BalanceAfter:      tba,
		ExternalReference: dep.TxHash,
		ExternalTrace:     dep.TxHash,
		ExternalRRN:       "",
		ExternalMaskedPAN: "",
		Description:       fmt.Sprintf("Tax collection for crypto request %d", cpr.ID), Metadata: b,
	}
	if err := f.transactionRepo.Save(ctx, taxTx); err != nil {
		return err
	}

	// System locked
	newSystemLocked := systemBalance.LockedBalance + realSystemShare
	metadataMap["source"] = "crypto_increase_system_locked_(real_system_share)"
	metadataMap["operation"] = "increase_system_locked"
	b, _ = json.Marshal(metadataMap)
	sysBS := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      cpr.CorrelationID,
		WalletID:           systemWallet.ID,
		CustomerID:         systemWallet.CustomerID,
		FreeBalance:        systemBalance.FreeBalance,
		FrozenBalance:      systemBalance.FrozenBalance,
		LockedBalance:      newSystemLocked,
		CreditBalance:      systemBalance.CreditBalance,
		SpentOnCampaign:    systemBalance.SpentOnCampaign,
		AgencyShareWithTax: systemBalance.AgencyShareWithTax,
		TotalBalance:       systemBalance.FreeBalance + systemBalance.FrozenBalance + newSystemLocked + systemBalance.CreditBalance + systemBalance.SpentOnCampaign + systemBalance.AgencyShareWithTax,
		Reason:             "real_system_share",
		Description:        fmt.Sprintf("System share for crypto request %d", cpr.ID),
		Metadata:           b,
	}
	if err := f.balanceSnapshotRepo.Save(ctx, sysBS); err != nil {
		return err
	}
	sb, _ := systemBalance.GetBalanceMap()
	sa, _ := sysBS.GetBalanceMap()
	sysTx := &models.Transaction{
		UUID:              uuid.New(),
		CorrelationID:     cpr.CorrelationID,
		Type:              models.TransactionTypeLock,
		Status:            models.TransactionStatusCompleted,
		Amount:            realSystemShare,
		Currency:          utils.TomanCurrency,
		WalletID:          systemWallet.ID,
		CustomerID:        systemWallet.CustomerID,
		BalanceBefore:     sb,
		BalanceAfter:      sa,
		ExternalReference: dep.TxHash,
		ExternalTrace:     dep.TxHash,
		ExternalRRN:       "",
		ExternalMaskedPAN: "",
		Description:       fmt.Sprintf("Real system share for crypto request %d", cpr.ID), Metadata: b,
	}
	if err := f.transactionRepo.Save(ctx, sysTx); err != nil {
		return err
	}

	// finalize request and deposit
	now := utils.UTCNow()
	dep.CreditedAt = &now
	if err := f.cdRepo.Update(ctx, dep); err != nil {
		return err
	}
	cpr.CreditedAt = &now
	cpr.ConfirmedAt = dep.ConfirmedAt
	cpr.DetectedAt = dep.DetectedAt
	cpr.Status = models.CryptoPaymentStatusCredited
	cpr.StatusReason = "crypto payment credited"
	if err := f.cprRepo.Update(ctx, cpr); err != nil {
		return err
	}

	msg := fmt.Sprintf("Crypto payment credited for request %d", cpr.ID)
	cust := models.Customer{
		ID: cpr.CustomerID,
		Wallet: &models.Wallet{
			ID: cpr.WalletID,
		},
	}
	_ = createAuditLog(ctx, f.auditRepo, &cust, models.AuditActionWalletChargeCompleted, msg, true, nil, metadata)
	return nil
}
