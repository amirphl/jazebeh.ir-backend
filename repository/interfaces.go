// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
)

// RepositoryContext key for transaction in context
type contextKey string

const TxContextKey contextKey = "tx"

type Repository[T any, F any] interface {
	ByFilter(ctx context.Context, filter F, orderBy string, limit, offset int) ([]*T, error)
	Save(ctx context.Context, entity *T) error
	SaveBatch(ctx context.Context, entities []*T) error
	Count(ctx context.Context, filter F) (int64, error)
	Exists(ctx context.Context, filter F) (bool, error)
}

// AccountTypeRepository defines operations for account types
type AccountTypeRepository interface {
	Repository[models.AccountType, models.AccountTypeFilter]
	ByID(ctx context.Context, id uint) (*models.AccountType, error)
	ByTypeName(ctx context.Context, typeName string) (*models.AccountType, error)
}

// AdminRepository defines operations for platform admins
type AdminRepository interface {
	Repository[models.Admin, models.AdminFilter]
	ByID(ctx context.Context, id uint) (*models.Admin, error)
	ByUUID(ctx context.Context, uuid string) (*models.Admin, error)
	ByUsername(ctx context.Context, username string) (*models.Admin, error)
}

// BotRepository defines operations for bots
type BotRepository interface {
	Repository[models.Bot, models.BotFilter]
	ByID(ctx context.Context, id uint) (*models.Bot, error)
	ByUUID(ctx context.Context, uuid string) (*models.Bot, error)
	ByUsername(ctx context.Context, username string) (*models.Bot, error)
}

// AudienceProfileRepository defines operations for audience profiles
type AudienceProfileRepository interface {
	Repository[models.AudienceProfile, models.AudienceProfileFilter]
	ByID(ctx context.Context, id uint) (*models.AudienceProfile, error)
	ByIDs(ctx context.Context, ids []int64) ([]*models.AudienceProfile, error)
	ByUID(ctx context.Context, uid string) (*models.AudienceProfile, error)
	ByUIDs(ctx context.Context, uids []string) ([]*models.AudienceProfile, error)
	SelectCampaignCandidates(ctx context.Context, filter models.AudienceProfileFilter, excludeIDs []int64, limit int) ([]*models.AudienceProfile, error)
}

// LineNumberRepository defines operations for line numbers
type LineNumberRepository interface {
	Repository[models.LineNumber, models.LineNumberFilter]
	ByID(ctx context.Context, id uint) (*models.LineNumber, error)
	ByUUID(ctx context.Context, uuid string) (*models.LineNumber, error)
	ByValue(ctx context.Context, value string) (*models.LineNumber, error)
	Update(ctx context.Context, line *models.LineNumber) error
	UpdateBatch(ctx context.Context, lines []*models.LineNumber) error
}

// PlatformBasePriceRepository defines operations for platform base prices.
type PlatformBasePriceRepository interface {
	Insert(ctx context.Context, p *models.PlatformBasePrice) error
	UpdatePriceByPlatform(ctx context.Context, platform string, price uint64) error
	LatestByPlatform(ctx context.Context, platform string) (*models.PlatformBasePrice, error)
	List(ctx context.Context) ([]*models.PlatformBasePrice, error)
}

// PagePriceRepository defines operations for platform page prices (append-only).
type PagePriceRepository interface {
	Insert(ctx context.Context, p *models.PagePrice) error
	LatestByPlatform(ctx context.Context, platform string) (*models.PagePrice, error)
	ListLatest(ctx context.Context) ([]*models.PagePrice, error)
}

// CustomerRepository defines operations for customers
type CustomerRepository interface {
	Repository[models.Customer, models.CustomerFilter]
	ByID(ctx context.Context, id uint) (*models.Customer, error)
	ByEmail(ctx context.Context, email string) (*models.Customer, error)
	ByMobile(ctx context.Context, mobile string) (*models.Customer, error)
	ByUUID(ctx context.Context, uuid string) (*models.Customer, error)
	ByAgencyRefererCode(ctx context.Context, agencyRefererCode string) (*models.Customer, error)
	ByNationalID(ctx context.Context, nationalID string) (*models.Customer, error)
	ListByAgency(ctx context.Context, agencyID uint) ([]*models.Customer, error)
	ListActiveCustomers(ctx context.Context, limit, offset int) ([]*models.Customer, error)
	UpdatePassword(ctx context.Context, customerID uint, passwordHash string) error
	UpdateVerificationStatus(ctx context.Context, customerID uint, isMobileVerified, isEmailVerified *bool, mobileVerifiedAt, emailVerifiedAt *time.Time) error
	FindByIDs(ctx context.Context, ids []uint) ([]*models.Customer, error)
	UpdateActiveStatus(ctx context.Context, customerID uint, isActive bool) error
}

// CustomerSessionRepository defines operations for customer sessions
type CustomerSessionRepository interface {
	Repository[models.CustomerSession, models.CustomerSessionFilter]
	ByID(ctx context.Context, id uint) (*models.CustomerSession, error)
	BySessionToken(ctx context.Context, token string) (*models.CustomerSession, error)
	ByRefreshToken(ctx context.Context, token string) (*models.CustomerSession, error)
	ListActiveSessionsByCustomer(ctx context.Context, customerID uint) ([]*models.CustomerSession, error)
	GetLatestByCorrelationID(ctx context.Context, correlationID uuid.UUID) (*models.CustomerSession, error)
	GetHistoryByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.CustomerSession, error)
	Update(ctx context.Context, session *models.CustomerSession) error
}

// AuditLogRepository defines operations for audit logs
type AuditLogRepository interface {
	Repository[models.AuditLog, models.AuditLogFilter]
	ByID(ctx context.Context, id uint) (*models.AuditLog, error)
	ListByCustomer(ctx context.Context, customerID uint, limit, offset int) ([]*models.AuditLog, error)
	ListByAction(ctx context.Context, action string, limit, offset int) ([]*models.AuditLog, error)
	ListFailedActions(ctx context.Context, limit, offset int) ([]*models.AuditLog, error)
	ListSecurityEvents(ctx context.Context, limit, offset int) ([]*models.AuditLog, error)
}

// BundleRepository defines operations for bundles.
type BundleRepository interface {
	Repository[models.Bundle, models.BundleFilter]
	ByID(ctx context.Context, id uint) (*models.Bundle, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.Bundle, error)
	Update(ctx context.Context, bundle *models.Bundle) error
}

// CampaignRepository defines the interface for campaign data access
type CampaignRepository interface {
	Repository[models.Campaign, models.CampaignFilter]
	ByID(ctx context.Context, id uint) (*models.Campaign, error)
	ByUUID(ctx context.Context, uuid string) (*models.Campaign, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.Campaign, error)
	ByCustomerIDAndIDs(ctx context.Context, customerID uint, campaignIDs []uint) ([]*models.Campaign, error)
	ByCustomerIDAndBundleIDs(ctx context.Context, customerID uint, bundleIDs []uint) ([]*models.Campaign, error)
	ByStatus(ctx context.Context, status models.CampaignStatus, limit, offset int) ([]*models.Campaign, error)
	Update(ctx context.Context, campaign models.Campaign) error
	UpdateStatistics(ctx context.Context, id uint, stats json.RawMessage) error
	AppendTrackingResults(ctx context.Context, id uint, items json.RawMessage) error
	UpdateStatus(ctx context.Context, id uint, status models.CampaignStatus) error
	MarkHidden(ctx context.Context, customerID uint, campaignIDs []uint) (int64, error)
	MarkVisible(ctx context.Context, customerID uint, campaignIDs []uint) (int64, error)
	CountByCustomerID(ctx context.Context, customerID uint) (int, error)
	CountByStatus(ctx context.Context, status models.CampaignStatus) (int, error)
	GetPendingApproval(ctx context.Context, limit, offset int) ([]*models.Campaign, error)
	GetScheduledCampaigns(ctx context.Context, from, to time.Time) ([]*models.Campaign, error)
	AggregateClickCountsByCampaignIDs(ctx context.Context, campaignIDs []uint) (map[uint]int64, error)
	AggregateClickCountsByCustomerIDs(ctx context.Context, customerIDs []uint) (map[uint]int64, error)
	AggregateTotalSentByCustomerIDs(ctx context.Context, customerIDs []uint) (map[uint]uint64, error)
}

// WalletRepository defines the interface for wallet data access
type WalletRepository interface {
	Repository[models.Wallet, models.WalletFilter]
	ByID(ctx context.Context, id uint) (*models.Wallet, error)
	ByUUID(ctx context.Context, uuid string) (*models.Wallet, error)
	ByCustomerID(ctx context.Context, customerID uint) (*models.Wallet, error)
	SaveWithInitialSnapshot(ctx context.Context, wallet *models.Wallet) error
	GetCurrentBalance(ctx context.Context, walletID uint) (*models.BalanceSnapshot, error)
	GetBalanceAtTime(ctx context.Context, walletID uint, timestamp time.Time) (*models.BalanceSnapshot, error)
	GetBalanceHistory(ctx context.Context, walletID uint, limit, offset int) ([]*models.BalanceSnapshot, error)
}

// TransactionRepository defines the interface for transaction data access
type TransactionRepository interface {
	Repository[models.Transaction, models.TransactionFilter]
	ByID(ctx context.Context, id uint) (*models.Transaction, error)
	ByUUID(ctx context.Context, uuid string) (*models.Transaction, error)
	UpdateMetadata(ctx context.Context, id uint, metadata []byte, updatedAt time.Time) error
	ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.Transaction, error)
	ByWalletID(ctx context.Context, walletID uint, limit, offset int) ([]*models.Transaction, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.Transaction, error)
	ByType(ctx context.Context, transactionType models.TransactionType, limit, offset int) ([]*models.Transaction, error)
	ByStatus(ctx context.Context, status models.TransactionStatus, limit, offset int) ([]*models.Transaction, error)
	ByExternalReference(ctx context.Context, externalReference string) (*models.Transaction, error)
	GetPendingTransactions(ctx context.Context, limit, offset int) ([]*models.Transaction, error)
	GetCompletedTransactions(ctx context.Context, limit, offset int) ([]*models.Transaction, error)
	GetAdminListWithCustomer(ctx context.Context, filter models.TransactionFilter, orderBy string, limit, offset int) ([]*models.Transaction, error)
	// History queries
	GetHistoryWithMetadata(ctx context.Context, walletID uint, customerID uint, startDate, endDate *time.Time, txType *models.TransactionType, status *models.TransactionStatus, limit, offset int) ([]*models.Transaction, int64, error)
	// Reports
	AggregateAgencyTransactionsByCustomers(ctx context.Context, agencyID uint, nameLike string, startDate, endDate *time.Time, orderBy string) ([]*AgencyCustomerTransactionAggregate, error)
	AggregateAgencyTransactionsByDiscounts(ctx context.Context, agencyID uint, customerID uint, orderBy string) ([]*AgencyCustomerDiscountAggregate, error)
	AggregateCustomersShares(ctx context.Context, startDate, endDate *time.Time) ([]*CustomerShareAggregate, error)
	AggregateCustomerTransactionsByDiscounts(ctx context.Context, customerID uint, orderBy string) ([]*AgencyCustomerDiscountAggregate, error)
}

// ACLChangeRequestRepository defines operations for maker-checker requests.
type ACLChangeRequestRepository interface {
	Repository[models.ACLChangeRequest, models.ACLChangeRequestFilter]
	ByUUID(ctx context.Context, id uuid.UUID) (*models.ACLChangeRequest, error)
}

// BalanceSnapshotRepository defines the interface for balance snapshot data access
type BalanceSnapshotRepository interface {
	Repository[models.BalanceSnapshot, models.BalanceSnapshotFilter]
	ByID(ctx context.Context, id uint) (*models.BalanceSnapshot, error)
	ByUUID(ctx context.Context, uuid string) (*models.BalanceSnapshot, error)
	ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.BalanceSnapshot, error)
	ByWalletID(ctx context.Context, walletID uint, limit, offset int) ([]*models.BalanceSnapshot, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.BalanceSnapshot, error)
	GetLatestByWalletID(ctx context.Context, walletID uint) (*models.BalanceSnapshot, error)
	GetLatestByWalletIDBeforeTime(ctx context.Context, walletID uint, timestamp time.Time) (*models.BalanceSnapshot, error)
}

// PaymentRequestRepository defines the interface for payment request data access
type PaymentRequestRepository interface {
	Repository[models.PaymentRequest, models.PaymentRequestFilter]
	Update(ctx context.Context, request *models.PaymentRequest) error
	LockCustomerInvoiceUUID(ctx context.Context, invoiceUUID string) error
	IsCustomerDepositInvoiceUUIDAlreadyLinked(ctx context.Context, invoiceUUID string) (bool, error)
	FindAdminChargeByIdempotencyKey(ctx context.Context, idempotencyKey string) (*models.PaymentRequest, error)
	ByID(ctx context.Context, id uint) (*models.PaymentRequest, error)
	ByUUID(ctx context.Context, uuid string) (*models.PaymentRequest, error)
	ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.PaymentRequest, error)
	ByInvoiceNumber(ctx context.Context, invoiceNumber string) (*models.PaymentRequest, error)
	ByAtipayToken(ctx context.Context, atipayToken string) (*models.PaymentRequest, error)
	ByPaymentReference(ctx context.Context, paymentReference string) (*models.PaymentRequest, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.PaymentRequest, error)
	ByWalletID(ctx context.Context, walletID uint, limit, offset int) ([]*models.PaymentRequest, error)
	ByStatus(ctx context.Context, status models.PaymentRequestStatus, limit, offset int) ([]*models.PaymentRequest, error)
	GetPendingRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error)
	GetExpiredRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error)
	GetCompletedRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error)
}

// CryptoPaymentRequestRepository defines data access for crypto payment requests
type CryptoPaymentRequestRepository interface {
	Repository[models.CryptoPaymentRequest, models.CryptoPaymentRequestFilter]
	ByID(ctx context.Context, id uint) (*models.CryptoPaymentRequest, error)
	ByUUID(ctx context.Context, uuid string) (*models.CryptoPaymentRequest, error)
	ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.CryptoPaymentRequest, error)
	ByDepositAddress(ctx context.Context, address, memo string) ([]*models.CryptoPaymentRequest, error)
	ByProviderRequestID(ctx context.Context, providerRequestID string) (*models.CryptoPaymentRequest, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.CryptoPaymentRequest, error)
	ByWalletID(ctx context.Context, walletID uint, limit, offset int) ([]*models.CryptoPaymentRequest, error)
	ByStatus(ctx context.Context, status models.CryptoPaymentStatus, limit, offset int) ([]*models.CryptoPaymentRequest, error)
	GetPendingRequests(ctx context.Context, limit, offset int) ([]*models.CryptoPaymentRequest, error)
	Update(ctx context.Context, request *models.CryptoPaymentRequest) error
}

// CryptoDepositRepository defines data access for on-chain deposits (may be provider-sourced)
type CryptoDepositRepository interface {
	Repository[models.CryptoDeposit, models.CryptoDepositFilter]
	ByID(ctx context.Context, id uint) (*models.CryptoDeposit, error)
	ByUUID(ctx context.Context, uuid string) (*models.CryptoDeposit, error)
	ByTxHash(ctx context.Context, txHash string) (*models.CryptoDeposit, error)
	ListUncreditedConfirmed(ctx context.Context, limit, offset int) ([]*models.CryptoDeposit, error)
	Update(ctx context.Context, deposit *models.CryptoDeposit) error
}

// DepositReceiptRepository defines data access for offline deposit receipts.
type DepositReceiptRepository interface {
	Save(ctx context.Context, receipt *models.DepositReceipt) error
	Update(ctx context.Context, receipt *models.DepositReceipt) error
	ByID(ctx context.Context, id uint) (*models.DepositReceipt, error)
	ByUUID(ctx context.Context, uuid string) (*models.DepositReceipt, error)
	List(ctx context.Context, f models.DepositReceiptFilter, limit, offset int, order string) ([]*models.DepositReceipt, error)
}

// AgencyDiscountRepository defines the interface for agency discount data access
type AgencyDiscountRepository interface {
	Repository[models.AgencyDiscount, models.AgencyDiscountFilter]
	ByID(ctx context.Context, id uint) (*models.AgencyDiscount, error)
	ByUUID(ctx context.Context, uuid string) (*models.AgencyDiscount, error)
	ByAgencyAndCustomer(ctx context.Context, agencyID, customerID uint) ([]*models.AgencyDiscount, error)
	GetActiveDiscount(ctx context.Context, agencyID, customerID uint) (*models.AgencyDiscount, error)
	ListActiveDiscountsWithCustomer(ctx context.Context, agencyID uint, nameLike, orderBy string) ([]*AgencyDiscountWithCustomer, error)
	ExpireActiveByAgencyAndCustomer(ctx context.Context, agencyID, customerID uint, expiredAt time.Time) error
}

// SegmentPriceFactorRepository defines operations for segment price factors
type SegmentPriceFactorRepository interface {
	Repository[models.SegmentPriceFactor, models.SegmentPriceFactorFilter]
	ListLatestByLevel3(ctx context.Context) ([]*models.SegmentPriceFactor, error)
	LatestByLevel3s(ctx context.Context, level3s []string) (map[string]float64, error)
	ListLatestByLevel3ForPlatform(ctx context.Context, platform string) ([]*models.SegmentPriceFactor, error)
	LatestByLevel3sForPlatform(ctx context.Context, level3s []string, platform string) (map[string]float64, error)
}

// TagRepository defines operations for tags
type TagRepository interface {
	Repository[models.Tag, models.TagFilter]
	ByID(ctx context.Context, id uint) (*models.Tag, error)
	ByName(ctx context.Context, name string) (*models.Tag, error)
	ListByIDs(ctx context.Context, ids []uint) ([]*models.Tag, error)
	ListByNames(ctx context.Context, names []string) ([]*models.Tag, error)
	ListActiveAfterID(ctx context.Context, afterID *uint, limit int) ([]*models.Tag, error)
}

// CampaignSelectedTagRepository owns campaign-level smart-targeting tag
// selections and the read model used by the selection table. Available-tag
// reads prefer the latest completed evaluation snapshot and fall back to the
// active tag catalog when a bundle has no current score rows.
type CampaignSelectedTagRepository interface {
	ListAvailable(ctx context.Context, bundleID, campaignID uint, search, sortBy, sortDirection string, limit, offset int) ([]*models.SmartTargetingTagRow, int64, error)
	ListAvailableTagIDs(ctx context.Context, bundleID uint, search, sortBy, sortDirection string, limit int) ([]uint, error)
	ListSelected(ctx context.Context, campaignID uint) ([]*models.CampaignSelectedTag, error)
	Summary(ctx context.Context, campaignID uint) (*models.CampaignSelectedTagSummary, error)
	Validate(ctx context.Context, campaignID, bundleID uint) error
	Replace(ctx context.Context, campaignID, bundleID, selectedByCustomerID uint, tagIDs []uint) error
	Clear(ctx context.Context, campaignID uint) error
}

type BundleTagEvaluationRunRepository interface {
	Save(ctx context.Context, entity *models.BundleTagEvaluationRun) error
	ByID(ctx context.Context, id int64) (*models.BundleTagEvaluationRun, error)
	ListByBundleID(ctx context.Context, bundleID uint, limit int) ([]*models.BundleTagEvaluationRun, error)
	CountByCustomerIDCreatedBetween(ctx context.Context, customerID uint, start, end time.Time) (int64, error)
}

type BundleTagEvaluationEventRepository interface {
	Save(ctx context.Context, entity *models.BundleTagEvaluationEvent) error
	LatestByRunID(ctx context.Context, runID int64) (*models.BundleTagEvaluationEvent, error)
	ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagEvaluationEvent, error)
	ExistsByRunIDAndType(ctx context.Context, runID int64, eventType string) (bool, error)
	ExistsByBatchIDAndType(ctx context.Context, batchID int64, eventType string) (bool, error)
}

type BundleTagPersonaAnalysisAttemptRepository interface {
	Save(ctx context.Context, entity *models.BundleTagPersonaAnalysisAttempt) error
	LatestByRunID(ctx context.Context, runID int64) (*models.BundleTagPersonaAnalysisAttempt, error)
	ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagPersonaAnalysisAttempt, error)
}

type BundleTagEvaluationBatchRepository interface {
	Save(ctx context.Context, entity *models.BundleTagEvaluationBatch) error
	SaveBatch(ctx context.Context, entities []*models.BundleTagEvaluationBatch) error
	ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagEvaluationBatch, error)
}

type BundleTagEvaluationBatchAttemptRepository interface {
	Save(ctx context.Context, entity *models.BundleTagEvaluationBatchAttempt) error
	LatestByBatchID(ctx context.Context, batchID int64) (*models.BundleTagEvaluationBatchAttempt, error)
	ListByBatchID(ctx context.Context, batchID int64) ([]*models.BundleTagEvaluationBatchAttempt, error)
}

type BundleTagScoreRepository interface {
	SaveBatch(ctx context.Context, entities []*models.BundleTagScore) error
	CountByRunID(ctx context.Context, runID int64) (int64, error)
	CountByRunIDAndBatchID(ctx context.Context, runID int64, batchID int64) (int64, error)
	ListByRunID(ctx context.Context, runID int64) ([]*models.BundleTagScore, error)
}

type BundleTagEvaluationReadRepository interface {
	ByBundleID(ctx context.Context, bundleID uint) (*models.CurrentBundleTagEvaluationStatus, error)
	ListByBundleIDs(ctx context.Context, bundleIDs []uint) ([]*models.CurrentBundleTagEvaluationStatus, error)
	ByRunID(ctx context.Context, runID int64) (*models.BundleTagEvaluationRunStatus, error)
	ListPendingRuns(ctx context.Context, limit int) ([]*models.BundleTagEvaluationRunStatus, error)
	ListCurrentScoresByBundleID(ctx context.Context, bundleID uint, limit, offset int) ([]*models.CurrentBundleTagScore, error)
	CountCurrentScoresByBundleID(ctx context.Context, bundleID uint) (int64, error)
}

// ProcessedCampaignRepository defines operations for processed campaigns
type ProcessedCampaignRepository interface {
	Repository[models.ProcessedCampaign, models.ProcessedCampaignFilter]
	ByID(ctx context.Context, id uint) (*models.ProcessedCampaign, error)
	ByCampaignID(ctx context.Context, campaignID uint) (*models.ProcessedCampaign, error)
	Update(ctx context.Context, pc *models.ProcessedCampaign) error
	AppendAudienceData(ctx context.Context, id uint, ids []int64, codes []string) error
	UpdateMeta(ctx context.Context, pc *models.ProcessedCampaign) error
}

// SentSMSProviderUpdate describes provider fields update identified by tracking id
type SentSMSProviderUpdate struct {
	ProcessedCampaignID *uint
	TrackingID          string
	Provider            *models.SMSProvider
	ProviderCustomerID  *int64
	ServerID            *string
	ErrorCode           *string
	Description         *string
	Status              *models.SMSSendStatus
	PartsDelivered      *int
}

// SentBaleSendResultUpdate describes send result fields update identified by tracking id.
type SentBaleSendResultUpdate struct {
	TrackingID     string
	Status         models.BaleSendStatus
	PartsDelivered int
	ServerID       *string
	ErrorCode      *string
	Description    *string
}

// SentSplusSendResultUpdate describes send result fields update identified by tracking id.
type SentSplusSendResultUpdate struct {
	TrackingID     string
	Status         models.SplusSendStatus
	PartsDelivered int
	ServerID       *string
	ErrorCode      *string
	Description    *string
}

// SentRubikaSendResultUpdate describes send result fields update identified by tracking id.
type SentRubikaSendResultUpdate struct {
	TrackingID     string
	Status         models.RubikaSendStatus
	PartsDelivered int
	ServerID       *string
	ErrorCode      *string
	Description    *string
}

// SentSMSRepository defines operations for sent SMS rows
type SentSMSRepository interface {
	Repository[models.SentSMS, models.SentSMSFilter]
	ByID(ctx context.Context, id uint) (*models.SentSMS, error)
	ListByProcessedCampaign(ctx context.Context, processedCampaignID uint, limit, offset int) ([]*models.SentSMS, error)
	ListByTrackingIDs(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentSMS, error)
	UpdateProviderFieldsByTrackingIDs(ctx context.Context, updates []SentSMSProviderUpdate) error
}

// SentBaleMessageRepository defines operations for sent Bale message rows.
type SentBaleMessageRepository interface {
	Repository[models.SentBaleMessage, models.SentBaleMessageFilter]
	ByID(ctx context.Context, id uint) (*models.SentBaleMessage, error)
	ListByProcessedCampaign(ctx context.Context, processedCampaignID uint, limit, offset int) ([]*models.SentBaleMessage, error)
	ListByTrackingIDs(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentBaleMessage, error)
	TrackingResultsFromSentRows(ctx context.Context, processedCampaignID uint) ([]BaleTrackingResult, error)
	UpdateSendResultByTrackingIDs(ctx context.Context, processedCampaignID uint, updates []SentBaleSendResultUpdate) error
}

// SentSplusMessageRepository defines operations for sent Splus message rows.
type SentSplusMessageRepository interface {
	Repository[models.SentSplusMessage, models.SentSplusMessageFilter]
	ByID(ctx context.Context, id uint) (*models.SentSplusMessage, error)
	ListByProcessedCampaign(ctx context.Context, processedCampaignID uint, limit, offset int) ([]*models.SentSplusMessage, error)
	ListByTrackingIDs(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentSplusMessage, error)
	TrackingResultsFromSentRows(ctx context.Context, processedCampaignID uint) ([]SplusTrackingResult, error)
	UpdateSendResultByTrackingIDs(ctx context.Context, updates []SentSplusSendResultUpdate) error
}

// SentRubikaMessageRepository defines operations for sent Rubika message rows.
type SentRubikaMessageRepository interface {
	Repository[models.SentRubikaMessage, models.SentRubikaMessageFilter]
	ByID(ctx context.Context, id uint) (*models.SentRubikaMessage, error)
	ListByProcessedCampaign(ctx context.Context, processedCampaignID uint, limit, offset int) ([]*models.SentRubikaMessage, error)
	ListByTrackingIDs(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentRubikaMessage, error)
	TrackingResultsFromSentRows(ctx context.Context, processedCampaignID uint) ([]RubikaTrackingResult, error)
	UpdateSendResultByTrackingIDs(ctx context.Context, updates []SentRubikaSendResultUpdate) error
}

// CampaignStatusJobRepository defines operations for cross-platform status check jobs.
type CampaignStatusJobRepository interface {
	Repository[models.CampaignStatusJob, any]
	ByID(ctx context.Context, id uint) (*models.CampaignStatusJob, error)
	SaveBatch(ctx context.Context, jobs []*models.CampaignStatusJob) error
	ListDue(ctx context.Context, platform string, now time.Time, limit int) ([]*models.CampaignStatusJob, error)
	Update(ctx context.Context, job *models.CampaignStatusJob) error
}

// SMSStatusResultRepository defines operations for SMS status check results
type SMSStatusResultRepository interface {
	Repository[models.SMSStatusResult, any]
	SaveBatch(ctx context.Context, rows []*models.SMSStatusResult) error
	AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*SMSStatusAggregates, error)
	TrackingResultsByCampaign(ctx context.Context, processedCampaignID uint) ([]SMSTrackingResult, error)
}

// BaleStatusResultRepository defines operations for Bale/Najva status check results.
type BaleStatusResultRepository interface {
	Repository[models.BaleStatusResult, any]
	SaveBatch(ctx context.Context, rows []*models.BaleStatusResult) error
	AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*BaleStatusAggregates, error)
	TrackingResultsByCampaign(ctx context.Context, processedCampaignID uint) ([]BaleTrackingResult, error)
}

// SplusStatusResultRepository defines operations for Splus status check results.
type SplusStatusResultRepository interface {
	Repository[models.SplusStatusResult, any]
	SaveBatch(ctx context.Context, rows []*models.SplusStatusResult) error
	AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*SplusStatusAggregates, error)
	TrackingResultsByCampaign(ctx context.Context, processedCampaignID uint) ([]SplusTrackingResult, error)
}

// RubikaStatusResultRepository defines operations for Rubika status check results.
type RubikaStatusResultRepository interface {
	Repository[models.RubikaStatusResult, any]
	SaveBatch(ctx context.Context, rows []*models.RubikaStatusResult) error
	AggregateByCampaign(ctx context.Context, processedCampaignID uint) (*RubikaStatusAggregates, error)
	TrackingResultsByCampaign(ctx context.Context, processedCampaignID uint) ([]RubikaTrackingResult, error)
}

// MultimediaAssetRepository defines operations for multimedia assets
type MultimediaAssetRepository interface {
	Repository[models.MultimediaAsset, models.MultimediaAssetFilter]
	ByID(ctx context.Context, id uint) (*models.MultimediaAsset, error)
	ByUUID(ctx context.Context, uuid string) (*models.MultimediaAsset, error)
	ExistsByUUID(ctx context.Context, uuid string) (bool, error)
	ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.MultimediaAsset, error)
}

// PlatformSettingsRepository defines operations for platform settings
type PlatformSettingsRepository interface {
	Repository[models.PlatformSettings, models.PlatformSettingsFilter]
	ByID(ctx context.Context, id uint) (*models.PlatformSettings, error)
	ByUUID(ctx context.Context, uuid string) (*models.PlatformSettings, error)
	UpdateStatus(ctx context.Context, id uint, status models.PlatformSettingsStatus) error
	AppendMetadata(ctx context.Context, id uint, key, value string) error
}

// TicketRepository defines operations for tickets
type TicketRepository interface {
	Repository[models.Ticket, models.TicketFilter]
	ByID(ctx context.Context, id uint) (*models.Ticket, error)
	ByUUID(ctx context.Context, uuid string) (*models.Ticket, error)
	ByCorrelationID(ctx context.Context, correlationID string) ([]*models.Ticket, error)
}

// ShortLinkRepository defines operations for short links
type ShortLinkRepository interface {
	Repository[models.ShortLink, models.ShortLinkFilter]
	ByID(ctx context.Context, id uint) (*models.ShortLink, error)
	ByUID(ctx context.Context, uid string) (*models.ShortLink, error)
	ByUIDs(ctx context.Context, uids []string) ([]*models.ShortLink, error)
	ListPendingExternalPublication(ctx context.Context, limit int) ([]*models.ShortLink, error)
	MarkExternallyPublished(ctx context.Context, uids []string, publishedAt time.Time) error
	ListByScenarioWithClicks(ctx context.Context, scenarioID uint, orderBy string) ([]*models.ShortLink, error)
	ListWithClicksDetailsByScenario(ctx context.Context, scenarioID uint, orderBy string) ([]*ShortLinkWithClick, error)
	ListWithClicksDetailsByScenarioRange(ctx context.Context, scenarioFrom, scenarioTo uint, orderBy string) ([]*ShortLinkWithClick, error)
	ListWithClicksDetailsByScenarioNameRegex(ctx context.Context, pattern string, orderBy string) ([]*ShortLinkWithClick, error)
	ListWithClicksDetailsByScenarioNameLike(ctx context.Context, pattern string, orderBy string) ([]*ShortLinkWithClick, error)
	GetLastScenarioID(ctx context.Context) (uint, error)
	GetMaxUIDSince(ctx context.Context, since time.Time) (string, error)
}

// ExternalShortLinkSyncRepository atomically imports external clicks and advances their cursor.
type ExternalShortLinkSyncRepository interface {
	Cursor(ctx context.Context, source string) (int64, error)
	ImportPage(ctx context.Context, source string, clicks []models.ExternalShortLinkClick, throughClickID int64) error
}

// ShortLinkClickRepository defines operations for short link clicks
type ShortLinkClickRepository interface {
	Repository[models.ShortLinkClick, any]
	ByID(ctx context.Context, id uint) (*models.ShortLinkClick, error)
	// DistinctShortLinkUIDsByCampaignID returns the distinct short-link UIDs (codes) that
	// received at least one click for the given campaign. Used for the campaign click report.
	DistinctShortLinkUIDsByCampaignID(ctx context.Context, campaignID uint) ([]string, error)
}
