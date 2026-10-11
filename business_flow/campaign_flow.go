// Package businessflow contains the core business logic and use cases for campaign workflows
package businessflow

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
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
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// CampaignFlow handles the campaign business logic
type CampaignFlow interface {
	CreateCampaign(ctx context.Context, req *dto.CreateCampaignRequest, metadata *ClientMetadata) (*dto.CreateCampaignResponse, error)
	UpdateCampaign(ctx context.Context, req *dto.UpdateCampaignRequest, metadata *ClientMetadata) (*dto.UpdateCampaignResponse, error)
	CalculateCampaignCapacity(ctx context.Context, req *dto.CalculateCampaignCapacityRequest, metadata *ClientMetadata) (*dto.CalculateCampaignCapacityResponse, error)
	CalculateCampaignCost(ctx context.Context, req *dto.CalculateCampaignCostRequest, metadata *ClientMetadata) (*dto.CalculateCampaignCostResponse, error)
	CalculateCampaignCostV2(ctx context.Context, req *dto.CalculateCampaignCostV2Request, metadata *ClientMetadata) (*dto.CalculateCampaignCostResponse, error)
	ListCampaigns(ctx context.Context, req *dto.ListCampaignsRequest, metadata *ClientMetadata) (*dto.ListCampaignsResponse, error)
	GetLastInitiatedCampaign(ctx context.Context, customerID uint, metadata *ClientMetadata) (*dto.GetLastInitiatedCampaignResponse, error)
	GetPagePrices(ctx context.Context) (*dto.GetPagePricesResponse, error)
	ListAudienceSpec(ctx context.Context, platform *string) (*dto.ListAudienceSpecResponse, error)
	GetApprovedRunningSummary(ctx context.Context, customerID uint) (*dto.CampaignsSummaryResponse, error)
	CancelCampaign(ctx context.Context, req *dto.CancelCampaignRequest, metadata *ClientMetadata) (*dto.CancelCampaignResponse, error)
	HideCampaigns(ctx context.Context, req *dto.HideCampaignsRequest, metadata *ClientMetadata) (*dto.HideCampaignsResponse, error)
	UnhideCampaigns(ctx context.Context, req *dto.UnhideCampaignsRequest, metadata *ClientMetadata) (*dto.UnhideCampaignsResponse, error)
	CloneCampaign(ctx context.Context, req *dto.CloneCampaignRequest, metadata *ClientMetadata) (*dto.CloneCampaignResponse, error)
	ExportCampaignReport(ctx context.Context, campaignID string) ([]byte, error)
	ExportCampaignClickReport(ctx context.Context, campaignUUID string) ([]byte, error)
	SendCampaignTestMessage(ctx context.Context, req *dto.SendCampaignTestMessageRequest, metadata *ClientMetadata) (*dto.SendCampaignTestMessageResponse, error)
	StartSmartTargetingTestSampling(ctx context.Context, req *dto.SmartTargetingTestSamplingPreviewRequest, metadata *ClientMetadata) (*dto.SmartTargetingTestSamplingCalculationResponse, error)
	GetCurrentSmartTargetingTestSampling(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingTestSamplingCalculationResponse, error)
	GetSmartTargetingTestSamplingByID(ctx context.Context, customerID uint, campaignUUID string, calculationID int64) (*dto.SmartTargetingTestSamplingCalculationResponse, error)
	ExecuteSmartTargetingTestSamplingCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) error
}

// CampaignFlowImpl implements the campaign business flow
type CampaignFlowImpl struct {
	campaignRepo            repository.CampaignRepository
	bundleRepo              repository.BundleRepository
	shortLinkRepo           repository.ShortLinkRepository
	customerRepo            repository.CustomerRepository
	multimediaRepo          repository.MultimediaAssetRepository
	platformSettingsRepo    repository.PlatformSettingsRepository
	walletRepo              repository.WalletRepository
	balanceSnapshotRepo     repository.BalanceSnapshotRepository
	transactionRepo         repository.TransactionRepository
	auditRepo               repository.AuditLogRepository
	lineNumberRepo          repository.LineNumberRepository
	segmentPriceRepo        repository.SegmentPriceFactorRepository
	platformBaseRepo        repository.PlatformBasePriceRepository
	pagePriceRepo           repository.PagePriceRepository
	processedCampaignRepo   repository.ProcessedCampaignRepository
	smsStatusResultRepo     repository.SMSStatusResultRepository
	shortLinkClickRepo      repository.ShortLinkClickRepository
	selectedTagRepo         repository.CampaignSelectedTagRepository
	capacityCalculationRepo repository.CampaignTargetingCapacityRepository
	samplingCalculationRepo repository.CampaignTargetingTestSamplingRepository
	audienceSpecRepo        repository.AudienceSpecRepository
	notifier                services.NotificationService
	adminConfig             config.AdminConfig
	cacheConfig             config.CacheConfig
	botConfig               config.BotConfig
	payamSMSConfig          config.PayamSMSConfig
	candooSMSConfig         config.CandooSMSConfig
	baleConfig              config.BaleConfig
	rubikaConfig            config.RubikaConfig
	splusConfig             config.SplusConfig
	irHTTPSProxy            string
	shortLinkPublisher      ShortLinkMappingPublisher
	rc                      *redis.Client
	db                      *gorm.DB
}

const (
	minCampaignBudget            = uint64(100_000)
	maxCampaignBudget            = uint64(160_000_000)
	defaultSegmentPriceFactor    = 1.0
	defaultLineNumberPriceFactor = 1.0
	undeliveredRefundDelay       = 72 * time.Hour
	audienceGradeA               = "A"
	audienceGradeB               = "B"
	audienceGradeC               = "C"
)

var tehranLoc *time.Location

var allowedShortLinkDomains = []string{"jo1n.ir", "joinsahel.ir", "jzbe.ir"}

// NewCampaignFlow creates a new campaign flow instance
func NewCampaignFlow(
	campaignRepo repository.CampaignRepository,
	bundleRepo repository.BundleRepository,
	shortLinkRepo repository.ShortLinkRepository,
	customerRepo repository.CustomerRepository,
	multimediaRepo repository.MultimediaAssetRepository,
	platformSettingsRepo repository.PlatformSettingsRepository,
	walletRepo repository.WalletRepository,
	balanceSnapshotRepo repository.BalanceSnapshotRepository,
	transactionRepo repository.TransactionRepository,
	auditRepo repository.AuditLogRepository,
	lineNumberRepo repository.LineNumberRepository,
	segmentPriceRepo repository.SegmentPriceFactorRepository,
	platformBaseRepo repository.PlatformBasePriceRepository,
	pagePriceRepo repository.PagePriceRepository,
	processedCampaignRepo repository.ProcessedCampaignRepository,
	smsStatusResultRepo repository.SMSStatusResultRepository,
	shortLinkClickRepo repository.ShortLinkClickRepository,
	selectedTagRepo repository.CampaignSelectedTagRepository,
	capacityCalculationRepo repository.CampaignTargetingCapacityRepository,
	samplingCalculationRepo repository.CampaignTargetingTestSamplingRepository,
	audienceSpecRepo repository.AudienceSpecRepository,
	db *gorm.DB,
	rc *redis.Client,
	notifier services.NotificationService,
	adminConfig config.AdminConfig,
	cacheConfig config.CacheConfig,
	botConfig config.BotConfig,
	payamSMSConfig config.PayamSMSConfig,
	candooSMSConfig config.CandooSMSConfig,
	baleConfig config.BaleConfig,
	rubikaConfig config.RubikaConfig,
	splusConfig config.SplusConfig,
	irHTTPSProxy string,
	shortLinkPublishers ...ShortLinkMappingPublisher,
) CampaignFlow {
	var shortLinkPublisher ShortLinkMappingPublisher
	if len(shortLinkPublishers) > 0 {
		shortLinkPublisher = shortLinkPublishers[0]
	}
	return &CampaignFlowImpl{
		campaignRepo:            campaignRepo,
		bundleRepo:              bundleRepo,
		shortLinkRepo:           shortLinkRepo,
		customerRepo:            customerRepo,
		multimediaRepo:          multimediaRepo,
		platformSettingsRepo:    platformSettingsRepo,
		walletRepo:              walletRepo,
		balanceSnapshotRepo:     balanceSnapshotRepo,
		transactionRepo:         transactionRepo,
		auditRepo:               auditRepo,
		lineNumberRepo:          lineNumberRepo,
		segmentPriceRepo:        segmentPriceRepo,
		platformBaseRepo:        platformBaseRepo,
		pagePriceRepo:           pagePriceRepo,
		processedCampaignRepo:   processedCampaignRepo,
		smsStatusResultRepo:     smsStatusResultRepo,
		shortLinkClickRepo:      shortLinkClickRepo,
		selectedTagRepo:         selectedTagRepo,
		capacityCalculationRepo: capacityCalculationRepo,
		samplingCalculationRepo: samplingCalculationRepo,
		audienceSpecRepo:        audienceSpecRepo,
		notifier:                notifier,
		adminConfig:             adminConfig,
		cacheConfig:             cacheConfig,
		botConfig:               botConfig,
		payamSMSConfig:          payamSMSConfig,
		candooSMSConfig:         candooSMSConfig,
		baleConfig:              baleConfig,
		rubikaConfig:            rubikaConfig,
		splusConfig:             splusConfig,
		irHTTPSProxy:            irHTTPSProxy,
		shortLinkPublisher:      shortLinkPublisher,
		rc:                      rc,
		db:                      db,
	}
}

// CreateCampaign handles the complete campaign creation process
func (s *CampaignFlowImpl) CreateCampaign(ctx context.Context, req *dto.CreateCampaignRequest, metadata *ClientMetadata) (*dto.CreateCampaignResponse, error) {
	// Validate business rules
	if err := s.validateCreateCampaignRequest(ctx, req); err != nil {
		return nil, NewBusinessError("CAMPAIGN_VALIDATION_FAILED", "Campaign validation failed", err)
	}

	customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CUSTOMER_LOOKUP_FAILED", "Failed to lookup customer", err)
	}

	shortLinkDomain, err := sanitizeShortLinkDomain(req.ShortLinkDomain)
	if err != nil {
		return nil, NewBusinessError("SHORT_LINK_DOMAIN_INVALID", "Invalid short link domain", err)
	}
	req.ShortLinkDomain = shortLinkDomain

	category, job, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, req.Category, req.Job, true)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_VALIDATION_FAILED", "Campaign validation failed", err)
	}
	req.Category = category
	req.Job = job

	sanitizedPlatform, err := sanitizeCampaignPlatform(req.Platform)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_VALIDATION_FAILED", "Campaign validation failed", err)
	}
	req.Platform = &sanitizedPlatform
	targetingMethod := *req.AudienceTargetingMethod
	level3sForValidation := req.Level3s
	excelFileForValidation := req.TargetAudienceExcelFileUUID
	if targetingMethod != models.CampaignAudienceTargetingStandard {
		level3sForValidation = nil
	}
	if targetingMethod != models.CampaignAudienceTargetingExcel {
		excelFileForValidation = nil
	}

	if err := s.ensureCreateCampaignRefs(
		ctx,
		req.CustomerID,
		req.BundleID,
		req.Phase,
		req.LineNumber,
		level3sForValidation,
		sanitizedPlatform,
		req.MediaUUID,
		excelFileForValidation,
		req.PlatformSettingsID,
	); err != nil {
		return nil, NewBusinessError("CAMPAIGN_VALIDATION_FAILED", "Campaign validation failed", err)
	}

	// Use transaction for atomicity
	var campaign *models.Campaign

	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		var err error
		campaign, err = s.createCampaign(txCtx, req, &customer)
		if err != nil {
			return err
		}
		if campaign.Spec.UsesSmartTargeting() {
			return s.selectedTagRepo.Replace(txCtx, campaign.ID, *campaign.BundleID, customer.ID, req.SelectedTagIDs)
		}
		return nil
	})

	if err != nil {
		errMsg := fmt.Sprintf("Campaign creation failed: %s", err.Error())
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignCreationFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("CAMPAIGN_CREATION_FAILED", "Campaign creation failed", err)
	}

	// Log successful creation
	msg := fmt.Sprintf("Campaign created successfully: %s", campaign.UUID.String())
	_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignCreated, msg, true, nil, metadata)

	// Build resp
	resp := &dto.CreateCampaignResponse{
		Message:   "Campaign created successfully",
		ID:        campaign.ID,
		UUID:      campaign.UUID.String(),
		Status:    string(campaign.Status),
		CreatedAt: campaign.CreatedAt.Format(time.RFC3339),
	}

	return resp, nil
}

// UpdateCampaign handles the campaign update process
func (s *CampaignFlowImpl) UpdateCampaign(ctx context.Context, req *dto.UpdateCampaignRequest, metadata *ClientMetadata) (*dto.UpdateCampaignResponse, error) {
	// Validate business rules
	if err := s.validateUpdateCampaignRequest(req); err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}

	// Get customer
	customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CUSTOMER_LOOKUP_FAILED", "Failed to lookup customer", err)
	}

	// Get existing campaign
	campaign, err := getCampaign(ctx, s.campaignRepo, req.UUID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}

	// Check if campaign can be updated (only initiated campaigns can be updated)
	if !canUpdateCampaign(campaign.Status) {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Campaign cannot be updated in current status", ErrCampaignUpdateNotAllowed)
	}

	// if req.ScheduleAt == nil && campaign.Spec.ScheduleAt == nil {
	// 	req.ScheduleAt = utils.ToPtr(utils.UTCNow().Add(time.Hour))
	// 	campaign.Spec.ScheduleAt = req.ScheduleAt
	// }

	// Validate schedule time must be at least 10 minutes in the future
	// scheduleTime := req.ScheduleAt
	// if scheduleTime == nil {
	// 	scheduleTime = campaign.Spec.ScheduleAt
	// }
	// if scheduleTime != nil && !scheduleTime.IsZero() {
	// 	if scheduleTime.Before(utils.UTCNow().Add(10 * time.Minute)) {
	// 		return nil, NewBusinessError("INVALID_SCHEDULE_TIME", "Schedule time must be at least 10 minutes in the future", ErrScheduleTimeTooSoon)
	// 	}
	// }

	ensureCampaignSpecDefaults(&campaign.Spec)
	if req.ShortLinkDomain != nil && strings.TrimSpace(*req.ShortLinkDomain) != "" {
		sanitizedShortLinkDomain, err := sanitizeShortLinkDomain(req.ShortLinkDomain)
		if err != nil {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
		}
		req.ShortLinkDomain = sanitizedShortLinkDomain
	} else {
		// These request fields use replace semantics: absent, null, and empty
		// all explicitly remove the stored value.
		req.ShortLinkDomain = nil
	}

	// Category and job are a pair for agencies, but an omitted field in a
	// partial update means "keep the stored value", not "erase it".
	if req.Category != nil || req.Job != nil {
		category, job := campaign.Spec.Category, campaign.Spec.Job
		if req.Category != nil {
			category = req.Category
		}
		if req.Job != nil {
			job = req.Job
		}
		sanitizedCategory, sanitizedJob, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, category, job, true)
		if err != nil {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
		}
		if req.Category != nil {
			req.Category = sanitizedCategory
		}
		if req.Job != nil {
			req.Job = sanitizedJob
		}
	}

	if req.Platform != nil {
		sanitizedPlatform, err := sanitizeCampaignPlatform(req.Platform)
		if err != nil {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
		}
		req.Platform = &sanitizedPlatform
	}

	finalize := req.Finalize != nil && *req.Finalize
	if err := s.prepareAudienceTargetingUpdate(ctx, req, &campaign); err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}
	samplingConfigurationChanged, err := smartTargetingTestSamplingConfigurationChanged(&campaign, req)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}
	finalPhase := campaign.Phase
	if req.Phase != nil {
		finalPhase = campaignPhaseOrDefault(req.Phase)
	}
	finalTargetingMethod := campaignAudienceTargetingMethod(campaign.Spec)
	if req.AudienceTargetingMethod != nil {
		finalTargetingMethod = *req.AudienceTargetingMethod
	}
	if err := validateUpdateCampaignBudget(req.Budget, finalTargetingMethod, finalPhase); err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}
	if finalTargetingMethod == models.CampaignAudienceTargetingSmart && finalPhase == models.CampaignPhaseTest {
		// Budget is derived during finalization. Ignore zero defaults and any
		// stale explicit values submitted by full-form clients.
		req.Budget = nil
	}
	finalSampleSize := campaign.SampleSizePerTag
	if req.SampleSizePerTag != nil {
		finalSampleSize = req.SampleSizePerTag
	}
	if finalTargetingMethod == models.CampaignAudienceTargetingSmart && finalPhase == models.CampaignPhaseTest {
		if finalSampleSize == nil {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", ErrSmartTargetingSampleSizeRequired)
		}
		if *finalSampleSize == 0 || *finalSampleSize > math.MaxInt64 {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", ErrSmartTargetingSampleSizeInvalid)
		}
	}
	// Validate references against the effective post-update campaign. Validating
	// the sparse request itself makes an omitted platform look like SMS and
	// makes an omitted line/settings record look absent during finalization.
	candidate := campaign
	candidateSpec := campaign.Spec
	if err := applyCampaignSpecUpdate(&candidateSpec, req); err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}
	candidate.Spec = candidateSpec
	if req.BundleID != nil {
		candidate.BundleID = req.BundleID
	}
	if req.Phase != nil {
		candidate.Phase = campaignPhaseOrDefault(req.Phase)
	}

	level3sForValidation := candidate.Spec.Level3s
	if candidate.Spec.UsesSmartTargeting() || candidate.Spec.UsesExcelTargeting() {
		level3sForValidation = nil
	}
	excelFileForValidation := candidate.Spec.TargetAudienceExcelFileUUID
	if !candidate.Spec.UsesExcelTargeting() {
		excelFileForValidation = nil
	}

	if err := s.ensureUpdateCampaignRefs(
		ctx,
		customer.ID,
		candidate.BundleID,
		campaignPhasePtr(candidate.Phase),
		candidate.Spec.LineNumber,
		level3sForValidation,
		candidate.Spec.Platform,
		candidate.Spec.MediaUUID,
		excelFileForValidation,
		candidate.Spec.PlatformSettingsID,
		finalize,
	); err != nil {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_VALIDATION_FAILED", "Campaign update validation failed", err)
	}

	// TODO:
	// Title, Sex, City, Adlink, Content, Budget: DTO validation
	// Level1, Level2s, Level3s, Tags, Sex, City: Ensure exist in file/database/cache
	// Adlink: validate URL format and length
	// Content: validate link anchor text and length
	// Schedule time must be in future and at least 10 minutes from now
	// Line number: validate exist in database and is available for the campaign schedule time (not reserved by another campaign)
	// * Move line number and segment price factor queries to ensureCreateCampaignRefs function

	// Phase 1: persist spec changes in a short transaction.
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := s.updateCampaign(txCtx, req, &campaign); err != nil {
			return err
		}
		if samplingConfigurationChanged {
			if err := s.clearCampaignSmartTargetingTestSamplingPreview(txCtx, campaign.ID); err != nil {
				return err
			}
		}
		if campaign.Spec.UsesSmartTargeting() {
			if req.SelectedTagIDs != nil {
				return s.selectedTagRepo.Replace(txCtx, campaign.ID, *campaign.BundleID, customer.ID, *req.SelectedTagIDs)
			}
			return nil
		}
		// Keep selections while another method is active. They are inactive now,
		// but may be reused if the customer switches back to Smart Targeting.
		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf("Campaign update failed for campaign %d: %s", campaign.ID, err.Error())
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignUpdateFailed, errMsg, false, &errMsg, metadata)
		return nil, NewBusinessError("CAMPAIGN_UPDATE_FAILED", "Campaign update failed", err)
	}

	// Re-read the committed campaign so subsequent logic sees the updated spec.
	campaign, err = getCampaign(ctx, s.campaignRepo, req.UUID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign after update", err)
	}

	// Draft saves, including sample_size_per_tag edits, persist configuration
	// only. Exact capacity, sampling and pricing are execution/finalization work.
	if !finalize {
		msg := fmt.Sprintf("Campaign updated successfully: %d", campaign.ID)
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignUpdated, msg, true, nil, metadata)
		return &dto.UpdateCampaignResponse{Message: "Campaign updated successfully"}, nil
	}
	finalizationRevision := campaign.UpdatedAt

	// Capacity check outside any transaction: avoids holding a DB connection
	// while doing Redis/file I/O and in-memory computation.
	usingTargetAudienceExcelFile := campaign.Spec.UsesExcelTargeting()
	capacity, err := s.CalculateCampaignCapacity(ctx, &dto.CalculateCampaignCapacityRequest{
		CampaignID: campaign.ID,
		CustomerID: campaign.CustomerID,
	}, metadata)
	if err != nil {
		return nil, NewBusinessError("CAPACITY_CALCULATION_FAILED", "Failed to calculate campaign capacity", err)
	}
	isSmartTargetingTest := campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest
	if !usingTargetAudienceExcelFile && !isSmartTargetingTest && capacity.Capacity < utils.MinAcceptableCampaignCapacity {
		return nil, NewBusinessError("INSUFFICIENT_CAPACITY", "Insufficient campaign capacity", ErrInsufficientCampaignCapacity)
	}

	// --- Finalize path ---
	// Pre-compute all pricing inputs outside the DB transaction so the
	// transaction that touches money is as short as possible.
	if err := s.canFinalizeCampaign(ctx, &campaign, customer); err != nil {
		return nil, NewBusinessError("CAMPAIGN_FINALIZE_NOT_ALLOWED", "Campaign cannot be finalized", err)
	}

	lineNumberPriceFactor := defaultLineNumberPriceFactor
	if campaign.Spec.Platform == models.CampaignPlatformSMS {
		lineNumberPriceFactor, err = s.fetchLineNumberPriceFactor(ctx, campaign.Spec.LineNumber)
		if err != nil {
			return nil, NewBusinessError("LINE_NUMBER_PRICE_FACTOR_FETCH_FAILED", "Failed to fetch line number price factor", err)
		}
	}

	segmentPriceFactor := defaultSegmentPriceFactor
	if !usingTargetAudienceExcelFile && !campaign.Spec.UsesSmartTargeting() {
		segmentPriceFactor, err = s.fetchSegmentPriceFactor(ctx, campaign.Spec.Level3s, campaign.Spec.Platform)
		if err != nil {
			return nil, NewBusinessError("SEGMENT_PRICE_FACTOR_FETCH_FAILED", "Failed to fetch segment price factor", err)
		}
	}

	pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, campaign.Spec.Platform)
	if err != nil {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_FETCH_FAILED", "Failed to fetch platform base price", err)
	}
	if pbp == nil {
		return nil, NewBusinessError("PLATFORM_BASE_PRICE_NOT_FOUND", "Platform base price not found", ErrPlatformBasePriceNotFound)
	}
	pp, err := s.pagePriceRepo.LatestByPlatform(ctx, campaign.Spec.Platform)
	if err != nil {
		return nil, NewBusinessError("PAGE_PRICE_FETCH_FAILED", "Failed to fetch page price", err)
	}
	if pp == nil {
		return nil, NewBusinessError("PAGE_PRICE_NOT_FOUND", "Page price not found", ErrPagePriceNotFound)
	}

	cost, err := s.CalculateCampaignCost(ctx, &dto.CalculateCampaignCostRequest{
		CampaignID: campaign.ID,
		CustomerID: campaign.CustomerID,
	}, metadata)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_COST_CALCULATION_FAILED", "Failed to calculate campaign cost", err)
	}

	numPages := s.calculateParts(
		campaign.Spec.Content,
		campaign.Spec.AdLink,
		campaign.Spec.ShortLinkDomain,
		campaign.Spec.Platform,
	)

	// Phase 2: atomic financial operations only — keep this transaction as
	// short as possible (no network calls, no heavy computation).
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := repository.LockCampaignForUpdate(txCtx, campaign.ID); err != nil {
			return err
		}
		lockedCampaign, err := s.campaignRepo.ByID(txCtx, campaign.ID)
		if err != nil {
			return err
		}
		if lockedCampaign == nil {
			return ErrCampaignNotFound
		}
		if !lockedCampaign.IsEditable() {
			return ErrCampaignUpdateNotAllowed
		}
		if finalizationRevision == nil || lockedCampaign.UpdatedAt == nil || !lockedCampaign.UpdatedAt.Equal(*finalizationRevision) {
			return NewBusinessError("CAMPAIGN_FINALIZE_STALE", "Campaign configuration changed while finalization was in progress", ErrInvalidState)
		}
		if lockedCampaign.Spec.UsesSmartTargeting() && lockedCampaign.Phase == models.CampaignPhaseTest {
			if err := s.requireCurrentSmartTargetingTestSampling(txCtx, lockedCampaign); err != nil {
				return err
			}
			intent, err := currentSmartTargetingTestSamplingIntent(txCtx, s.selectedTagRepo, lockedCampaign, true)
			if err != nil {
				return err
			}
			if intent.effective != cost.NumTargetAudience {
				return ErrSmartTargetingTestPreviewRequired
			}
			if lockedCampaign.BundleID == nil || *lockedCampaign.BundleID == 0 {
				return ErrBundleNotFound
			}
			if err := repository.LockBundleForUpdate(txCtx, *lockedCampaign.BundleID); err != nil {
				return err
			}
			if err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ReserveForCampaign(txCtx, lockedCampaign); err != nil {
				if errors.Is(err, repository.ErrSmartTargetingTestSelectionUnavailable) || errors.Is(err, repository.ErrSmartTargetingTestSelectionConflict) {
					return ErrSmartTargetingTestPreviewRequired
				}
				return err
			}
		}
		campaign = *lockedCampaign
		campaign.Status = models.CampaignStatusWaitingForApproval
		applyFinalizedCampaignCost(&campaign, cost)
		campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
		if err := s.campaignRepo.Update(txCtx, campaign); err != nil {
			return err
		}

		wallet, err := getWallet(txCtx, s.walletRepo, campaign.CustomerID)
		if err != nil {
			return err
		}
		if err := repository.LockWalletForUpdate(txCtx, wallet.ID); err != nil {
			return err
		}
		customer.Wallet = &wallet

		latestBalance, err := getLatestBalanceSnapshot(txCtx, s.walletRepo, wallet.ID)
		if err != nil {
			return err
		}

		availableBalance := latestBalance.FreeBalance + latestBalance.CreditBalance
		if availableBalance < cost.TotalCost {
			return ErrInsufficientFunds
		}

		newFreeBalance := latestBalance.FreeBalance
		newCreditBalance := latestBalance.CreditBalance
		remaining := cost.TotalCost
		if remaining <= newFreeBalance {
			newFreeBalance -= remaining
		} else {
			remaining -= newFreeBalance
			newFreeBalance = 0
			newCreditBalance -= remaining
		}
		newFrozenBalance := latestBalance.FrozenBalance + cost.TotalCost

		meta := map[string]any{
			"source":                   "campaign_update",
			"operation":                "reserve_budget",
			"campaign_id":              campaign.ID,
			"amount":                   cost.TotalCost,
			"currency":                 utils.TomanCurrency,
			"campaign_spec":            campaign.Spec,
			"base_price":               pbp.Price,
			"platform_base_price":      pbp.Price, // TODO: Later replace all "base_price" with "platform_base_price"
			"page_price":               pp.Price,
			"num_pages":                numPages,
			"line_number_price_factor": lineNumberPriceFactor,
			"segment_price_factor":     segmentPriceFactor,
		}
		metaBytes, _ := json.Marshal(meta)

		corrID := uuid.New()

		newSnapshot := &models.BalanceSnapshot{
			UUID:               uuid.New(),
			CorrelationID:      corrID,
			WalletID:           wallet.ID,
			CustomerID:         customer.ID,
			FreeBalance:        newFreeBalance,
			FrozenBalance:      newFrozenBalance,
			CreditBalance:      newCreditBalance,
			LockedBalance:      latestBalance.LockedBalance,
			SpentOnCampaign:    latestBalance.SpentOnCampaign,
			AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			TotalBalance:       newFreeBalance + newFrozenBalance + newCreditBalance + latestBalance.LockedBalance + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
			Reason:             "campaign_budget_reserved_waiting_for_approval",
			Description:        fmt.Sprintf("Budget reserved for campaign %d", campaign.ID),
			Metadata:           metaBytes,
			CreatedAt:          utils.UTCNow(),
			UpdatedAt:          utils.UTCNow(),
		}
		if err := s.balanceSnapshotRepo.Save(txCtx, newSnapshot); err != nil {
			return err
		}

		beforeMap, err := latestBalance.GetBalanceMap()
		if err != nil {
			return err
		}
		afterMap, err := newSnapshot.GetBalanceMap()
		if err != nil {
			return err
		}

		freezeTx := &models.Transaction{
			UUID:          uuid.New(),
			CorrelationID: corrID,
			Type:          models.TransactionTypeFreeze,
			Status:        models.TransactionStatusCompleted,
			Amount:        cost.TotalCost,
			Currency:      utils.TomanCurrency,
			WalletID:      wallet.ID,
			CustomerID:    customer.ID,
			BalanceBefore: beforeMap,
			BalanceAfter:  afterMap,
			Description:   fmt.Sprintf("Campaign budget reserved: %d Tomans for campaign %d", cost.TotalCost, campaign.ID),
			Metadata:      metaBytes,
			CreatedAt:     utils.UTCNow(),
			UpdatedAt:     utils.UTCNow(),
		}
		return s.transactionRepo.Save(txCtx, freezeTx)
	})

	if err != nil {
		errMsg := fmt.Sprintf("Campaign update failed for campaign %d: %s", campaign.ID, err.Error())
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignUpdateFailed, errMsg, false, &errMsg, metadata)
		return nil, NewBusinessError("CAMPAIGN_UPDATE_FAILED", "Campaign update failed", err)
	}

	// Send admin notifications after the transaction commits so network
	// latency does not extend the transaction lifetime.
	if s.notifier != nil {
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			subject := campaign.UUID.String()
			if campaign.Spec.Title != nil {
				subject = *campaign.Spec.Title
			}
			msg := fmt.Sprintf("New campaign pending approval:\n%s", subject)
			for _, mobile := range s.adminConfig.ActiveMobiles() {
				_ = s.notifier.SendSMS(notifyCtx, mobile, msg, nil)
			}
		}()
	}

	// Log successful update
	msg := fmt.Sprintf("Campaign updated successfully: %d", campaign.ID)
	_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignUpdated, msg, true, nil, metadata)

	// Build resp
	resp := &dto.UpdateCampaignResponse{
		Message: "Campaign updated successfully",
	}

	return resp, nil
}

// applyFinalizedCampaignCost stores the authoritative pricing result. Smart
// Targeting Test campaigns do not accept a user-controlled budget: their
// budget is exactly satisfied tags * sample size per tag * price per message.
func applyFinalizedCampaignCost(campaign *models.Campaign, cost *dto.CalculateCampaignCostResponse) {
	if campaign == nil || cost == nil {
		return
	}
	campaign.NumAudience = utils.ToPtr(cost.NumTargetAudience)
	if campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest {
		campaign.Spec.Budget = utils.ToPtr(cost.TotalCost)
	}
}

// smartTargetingTestSamplingConfigurationChanged compares effective values,
// not merely field presence. Full-form clients commonly resubmit unchanged
// values while finalizing; that must not discard a current sampling preview.
func smartTargetingTestSamplingConfigurationChanged(campaign *models.Campaign, req *dto.UpdateCampaignRequest) (bool, error) {
	if campaign == nil || req == nil {
		return false, nil
	}

	currentMethod := campaignAudienceTargetingMethod(campaign.Spec)
	finalMethod := currentMethod
	if req.AudienceTargetingMethod != nil {
		var err error
		finalMethod, err = sanitizeAudienceTargetingMethod(req.AudienceTargetingMethod)
		if err != nil {
			return false, err
		}
	}
	if finalMethod != currentMethod {
		return true, nil
	}

	if req.BundleID != nil && (campaign.BundleID == nil || *req.BundleID != *campaign.BundleID) {
		return true, nil
	}
	if req.Phase != nil && campaignPhaseOrDefault(req.Phase) != campaign.Phase {
		return true, nil
	}
	if req.SampleSizePerTag != nil && (campaign.SampleSizePerTag == nil || *req.SampleSizePerTag != *campaign.SampleSizePerTag) {
		return true, nil
	}
	if req.AudienceGrades != nil {
		currentClasses, err := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
		if err != nil {
			return false, err
		}
		finalClasses, err := normalizeSmartTargetingScoreClasses(req.AudienceGrades)
		if err != nil {
			return false, err
		}
		if !slices.Equal(currentClasses, finalClasses) {
			return true, nil
		}
	}
	if req.Platform != nil && !slices.Equal(
		models.SmartTargetingAllowedColors(campaign.Spec.Platform),
		models.SmartTargetingAllowedColors(*req.Platform),
	) {
		return true, nil
	}
	return false, nil
}

// CloneCampaign clones an existing campaign for the same customer with a fresh identity and reset state.
func (s *CampaignFlowImpl) CloneCampaign(ctx context.Context, req *dto.CloneCampaignRequest, metadata *ClientMetadata) (*dto.CloneCampaignResponse, error) {
	if req == nil || strings.TrimSpace(req.UUID) == "" {
		return nil, NewBusinessError("CLONE_CAMPAIGN_VALIDATION_FAILED", "Campaign UUID is required", ErrCampaignUUIDRequired)
	}

	customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CUSTOMER_LOOKUP_FAILED", "Failed to lookup customer", err)
	}

	src, err := getCampaign(ctx, s.campaignRepo, req.UUID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}

	ensureCampaignSpecDefaults(&src.Spec)
	src.Spec.ScheduleAt = nil // Clear schedule to avoid cloning campaigns with past schedule times

	clone := models.Campaign{
		UUID:             uuid.New(),
		CustomerID:       src.CustomerID,
		Status:           models.CampaignStatusInitiated,
		Spec:             src.Spec,
		Comment:          nil,
		Statistics:       json.RawMessage(`{}`),
		NumAudience:      utils.ToPtr(uint64(0)),
		BundleID:         src.BundleID,
		Phase:            src.Phase,
		SampleSizePerTag: src.SampleSizePerTag,
	}

	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := s.campaignRepo.Save(txCtx, &clone); err != nil {
			return err
		}
		if !clone.Spec.UsesSmartTargeting() {
			return nil
		}
		selected, err := s.selectedTagRepo.ListSelected(txCtx, src.ID)
		if err != nil {
			return err
		}
		ids := make([]uint, 0, len(selected))
		for _, item := range selected {
			ids = append(ids, item.TagID)
		}
		if len(ids) == 0 || clone.BundleID == nil {
			return ErrSmartTargetingTagsRequired
		}
		return s.selectedTagRepo.Replace(txCtx, clone.ID, *clone.BundleID, customer.ID, ids)
	})
	if err != nil {
		errMsg := fmt.Sprintf("Campaign clone failed from %s: %v", src.UUID.String(), err)
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignCreationFailed, errMsg, false, &errMsg, metadata)
		return nil, NewBusinessError("CAMPAIGN_CLONE_FAILED", "Campaign clone failed", err)
	}

	msg := fmt.Sprintf("Campaign cloned from %s to %s", src.UUID.String(), clone.UUID.String())
	_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignCreated, msg, true, nil, metadata)

	return &dto.CloneCampaignResponse{
		Message:   "Campaign cloned successfully",
		ID:        clone.ID,
		UUID:      clone.UUID.String(),
		Status:    string(clone.Status),
		CreatedAt: clone.CreatedAt.Format(time.RFC3339),
	}, nil
}

// CancelCampaign allows a customer to cancel their own campaign and refunds budget according to current campaign status.
func (s *CampaignFlowImpl) CancelCampaign(ctx context.Context, req *dto.CancelCampaignRequest, metadata *ClientMetadata) (*dto.CancelCampaignResponse, error) {
	// NOTE: Idempotency
	if req == nil || req.CampaignID == 0 {
		return nil, NewBusinessError("CANCEL_CAMPAIGN_VALIDATION_FAILED", "campaign_id is required", ErrCampaignNotFound)
	}

	if !s.tryAcquireFlowLock(ctx, fmt.Sprintf("cancel_campaign:%d", req.CustomerID), 20*time.Second) {
		return nil, NewBusinessError("CANCEL_CAMPAIGN_BUSY", "cancel campaign request is already in progress", ErrInvalidState)
	}

	var campaign *models.Campaign

	err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := repository.LockCampaignForUpdate(txCtx, req.CampaignID); err != nil {
			return err
		}
		var err error
		campaign, err = s.campaignRepo.ByID(txCtx, req.CampaignID)
		if err != nil {
			return err
		}
		if campaign == nil {
			return ErrCampaignNotFound
		}
		if campaign.CustomerID != req.CustomerID {
			return ErrCampaignAccessDenied
		}
		if !canCancelCampaign(campaign.Status) {
			return ErrCampaignNotWaitingForApproval
		}

		customer, err := getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
		if err != nil {
			return err
		}

		wallet, err := getWallet(txCtx, s.walletRepo, campaign.CustomerID)
		if err != nil {
			return err
		}
		if err := repository.LockWalletForUpdate(txCtx, wallet.ID); err != nil {
			return err
		}
		latestBalance, err := getLatestBalanceSnapshot(txCtx, s.walletRepo, wallet.ID)
		if err != nil {
			return err
		}

		switch campaign.Status {
		case models.CampaignStatusWaitingForApproval:
			freezeTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
				CustomerID: &campaign.CustomerID,
				CampaignID: &campaign.ID,
				Source:     utils.ToPtr("campaign_update"),
				Operation:  utils.ToPtr("reserve_budget"),
				Type:       utils.ToPtr(models.TransactionTypeFreeze),
				Status:     utils.ToPtr(models.TransactionStatusCompleted),
			}, "id DESC", 0, 0)
			if err != nil {
				return err
			}
			if len(freezeTxs) == 0 {
				return ErrFreezeTransactionNotFound
			}
			if len(freezeTxs) > 1 {
				return ErrMultipleFreezeTransactionsFound
			}
			freezeTx := freezeTxs[0]

			amount := freezeTx.Amount
			if latestBalance.FrozenBalance < amount {
				return ErrInsufficientFunds
			}

			meta := map[string]any{
				"source":      "campaign_cancel",
				"operation":   "cancel_campaign_refund_frozen",
				"campaign_id": campaign.ID,
				"comment":     req.Comment,
			}
			metaBytes, _ := json.Marshal(meta)

			// TODO:
			// newFrozen := latestBalance.FrozenBalance - amount
			// // Restore free/credit split exactly as it was taken during the freeze.
			// freeRefund, creditRefund := computeFreezeRefundSplit(freezeTx, amount)
			// newFree := latestBalance.FreeBalance + freeRefund
			// newCredit := latestBalance.CreditBalance + creditRefund

			// newSnap := &models.BalanceSnapshot{
			// 	UUID:               uuid.New(),
			// 	CorrelationID:      freezeTx.CorrelationID,
			// 	WalletID:           wallet.ID,
			// 	CustomerID:         customer.ID,
			// 	FreeBalance:        newFree,
			// 	FrozenBalance:      newFrozen,
			// 	LockedBalance:      latestBalance.LockedBalance,
			// 	CreditBalance:      newCredit,
			// 	SpentOnCampaign:    latestBalance.SpentOnCampaign,
			// 	AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			// 	TotalBalance:       newFree + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
			// 	Reason:             "campaign_cancelled_budget_refund",
			// 	Description:        fmt.Sprintf("Refund reserved budget for cancelled campaign %d", campaign.ID),
			// 	Metadata:           metaBytes,
			// }

			newFrozen := latestBalance.FrozenBalance - amount
			newCredit := latestBalance.CreditBalance + amount

			newSnap := &models.BalanceSnapshot{
				UUID:               uuid.New(),
				CorrelationID:      freezeTx.CorrelationID,
				WalletID:           wallet.ID,
				CustomerID:         customer.ID,
				FreeBalance:        latestBalance.FreeBalance,
				FrozenBalance:      newFrozen,
				LockedBalance:      latestBalance.LockedBalance,
				CreditBalance:      newCredit,
				SpentOnCampaign:    latestBalance.SpentOnCampaign,
				AgencyShareWithTax: latestBalance.AgencyShareWithTax,
				TotalBalance:       latestBalance.FreeBalance + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
				Reason:             "campaign_cancelled_budget_refund",
				Description:        fmt.Sprintf("Refund reserved budget for cancelled campaign %d", campaign.ID),
				Metadata:           metaBytes,
			}
			if err := s.balanceSnapshotRepo.Save(txCtx, newSnap); err != nil {
				return err
			}

			beforeMap, err := latestBalance.GetBalanceMap()
			if err != nil {
				return err
			}
			afterMap, err := newSnap.GetBalanceMap()
			if err != nil {
				return err
			}

			refundTx := &models.Transaction{
				UUID:          uuid.New(),
				CorrelationID: freezeTx.CorrelationID,
				Type:          models.TransactionTypeRefund,
				Status:        models.TransactionStatusCompleted,
				Amount:        amount,
				Currency:      utils.TomanCurrency,
				WalletID:      wallet.ID,
				CustomerID:    customer.ID,
				BalanceBefore: beforeMap,
				BalanceAfter:  afterMap,
				Description:   fmt.Sprintf("Refund reserved budget for cancelled campaign %d", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}
		case models.CampaignStatusApproved:
			if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() {
				return ErrCampaignNotWaitingForApproval
			}

			now := utils.UTCNow()
			if campaign.Spec.ScheduleAt.Before(now.Add(10 * time.Minute)) {
				return ErrScheduleTimeTooSoon
			}

			debitTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
				CustomerID: &campaign.CustomerID,
				CampaignID: &campaign.ID,
				Source:     utils.ToPtr("admin_campaign_approve"),
				Operation:  utils.ToPtr("approve_campaign_budget_consume"),
				Type:       utils.ToPtr(models.TransactionTypeFee),
				Status:     utils.ToPtr(models.TransactionStatusCompleted),
			}, "id DESC", 0, 0)
			if err != nil {
				return err
			}
			if len(debitTxs) == 0 {
				return ErrCampaignDebitTransactionNotFound
			}
			if len(debitTxs) > 1 {
				return ErrMultipleCampaignDebitTransactionsFound
			}
			debitTx := debitTxs[0]

			amount := debitTx.Amount
			if latestBalance.SpentOnCampaign < amount {
				return ErrInsufficientFunds
			}

			// TODO:
			// // Recover the original FreeBalance/CreditBalance split by looking up the
			// // freeze transaction that shares the same CorrelationID as the debit tx.
			// debitCorrID := debitTx.CorrelationID
			// origFreezeTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
			// 	CorrelationID: &debitCorrID,
			// 	CustomerID:    &campaign.CustomerID,
			// 	Type:          utils.ToPtr(models.TransactionTypeFreeze),
			// 	Status:        utils.ToPtr(models.TransactionStatusCompleted),
			// }, "id ASC", 1, 0)
			// if err != nil {
			// 	return err
			// }
			// var freeRefund, creditRefund uint64
			// if len(origFreezeTxs) > 0 {
			// 	freeRefund, creditRefund = computeFreezeRefundSplit(origFreezeTxs[0], amount)
			// } else {
			// 	// No freeze tx found; conservatively return all to free balance.
			// 	freeRefund = amount
			// }

			meta := map[string]any{
				"source":      "campaign_cancel",
				"operation":   "cancel_campaign_refund_spent_after_approval",
				"campaign_id": campaign.ID,
				"comment":     req.Comment,
			}
			metaBytes, _ := json.Marshal(meta)

			// TODO:
			// newFree := latestBalance.FreeBalance + freeRefund
			// newCredit := latestBalance.CreditBalance + creditRefund
			newCredit := latestBalance.CreditBalance + amount
			newSpentOnCampaign := latestBalance.SpentOnCampaign - amount

			// TODO:
			// newSnap := &models.BalanceSnapshot{
			// 	UUID:               uuid.New(),
			// 	CorrelationID:      debitTx.CorrelationID,
			// 	WalletID:           wallet.ID,
			// 	CustomerID:         customer.ID,
			// 	FreeBalance:        newFree,
			// 	FrozenBalance:      latestBalance.FrozenBalance,
			// 	LockedBalance:      latestBalance.LockedBalance,
			// 	CreditBalance:      newCredit,
			// 	SpentOnCampaign:    newSpentOnCampaign,
			// 	AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			// 	TotalBalance:       newFree + latestBalance.FrozenBalance + latestBalance.LockedBalance + newCredit + newSpentOnCampaign + latestBalance.AgencyShareWithTax,
			// 	Reason:             "campaign_cancelled_budget_refund_after_approval",
			// 	Description:        fmt.Sprintf("Refund spent budget for cancelled campaign %d after approval", campaign.ID),
			// 	Metadata:           metaBytes,
			// }
			newSnap := &models.BalanceSnapshot{
				UUID:               uuid.New(),
				CorrelationID:      debitTx.CorrelationID,
				WalletID:           wallet.ID,
				CustomerID:         customer.ID,
				FreeBalance:        latestBalance.FreeBalance,
				FrozenBalance:      latestBalance.FrozenBalance,
				LockedBalance:      latestBalance.LockedBalance,
				CreditBalance:      newCredit,
				SpentOnCampaign:    newSpentOnCampaign,
				AgencyShareWithTax: latestBalance.AgencyShareWithTax,
				TotalBalance:       latestBalance.FreeBalance + latestBalance.FrozenBalance + latestBalance.LockedBalance + newCredit + newSpentOnCampaign + latestBalance.AgencyShareWithTax,
				Reason:             "campaign_cancelled_budget_refund_after_approval",
				Description:        fmt.Sprintf("Refund spent budget for cancelled campaign %d after approval", campaign.ID),
				Metadata:           metaBytes,
			}
			if err := s.balanceSnapshotRepo.Save(txCtx, newSnap); err != nil {
				return err
			}

			beforeMap, err := latestBalance.GetBalanceMap()
			if err != nil {
				return err
			}
			afterMap, err := newSnap.GetBalanceMap()
			if err != nil {
				return err
			}

			refundTx := &models.Transaction{
				UUID:          uuid.New(),
				CorrelationID: debitTx.CorrelationID,
				Type:          models.TransactionTypeRefund,
				Status:        models.TransactionStatusCompleted,
				Amount:        amount,
				Currency:      utils.TomanCurrency,
				WalletID:      wallet.ID,
				CustomerID:    customer.ID,
				BalanceBefore: beforeMap,
				BalanceAfter:  afterMap,
				Description:   fmt.Sprintf("Refund spent budget for cancelled campaign %d after approval", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}
		default:
			return ErrCampaignNotWaitingForApproval
		}
		if err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ReleaseForCampaign(txCtx, campaign.ID); err != nil {
			return err
		}

		campaign.Status = models.CampaignStatusCancelled
		if req.Comment != nil && strings.TrimSpace(*req.Comment) != "" {
			comment := strings.TrimSpace(*req.Comment)
			campaign.Comment = &comment
		}
		campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
		if err := s.campaignRepo.Update(txCtx, *campaign); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, NewBusinessError("CANCEL_CAMPAIGN_FAILED", "Failed to cancel campaign", err)
	}

	return &dto.CancelCampaignResponse{
		Message: "Campaign cancelled successfully",
	}, nil
}

func (s *CampaignFlowImpl) HideCampaigns(ctx context.Context, req *dto.HideCampaignsRequest, metadata *ClientMetadata) (*dto.HideCampaignsResponse, error) {
	_ = metadata

	if req == nil {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "request is required", ErrInvalidState)
	}
	if req.CustomerID == 0 {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "customer id is required", ErrCustomerNotFound)
	}

	campaignIDs := normalizeCampaignIDs(req.CampaignIDs)
	if len(campaignIDs) == 0 {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "at least one campaign id is required", ErrCampaignNotFound)
	}

	if _, err := getCustomer(ctx, s.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "failed to lookup customer", err)
	}

	ownedCampaigns, err := s.campaignRepo.ByCustomerIDAndIDs(ctx, req.CustomerID, campaignIDs)
	if err != nil {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "failed to fetch campaigns", err)
	}
	if len(ownedCampaigns) != len(campaignIDs) {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "one or more campaigns were not found", ErrCampaignNotFound)
	}

	updatedCount, err := s.campaignRepo.MarkHidden(ctx, req.CustomerID, campaignIDs)
	if err != nil {
		return nil, NewBusinessError("HIDE_CAMPAIGNS_FAILED", "failed to hide campaigns", err)
	}

	return &dto.HideCampaignsResponse{
		Message:      "Campaigns hidden successfully",
		UpdatedCount: updatedCount,
	}, nil
}

func (s *CampaignFlowImpl) UnhideCampaigns(ctx context.Context, req *dto.UnhideCampaignsRequest, metadata *ClientMetadata) (*dto.UnhideCampaignsResponse, error) {
	_ = metadata

	if req == nil {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "request is required", ErrInvalidState)
	}
	if req.CustomerID == 0 {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "customer id is required", ErrCustomerNotFound)
	}

	campaignIDs := normalizeCampaignIDs(req.CampaignIDs)
	if len(campaignIDs) == 0 {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "at least one campaign id is required", ErrCampaignNotFound)
	}

	if _, err := getCustomer(ctx, s.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "failed to lookup customer", err)
	}

	ownedCampaigns, err := s.campaignRepo.ByCustomerIDAndIDs(ctx, req.CustomerID, campaignIDs)
	if err != nil {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "failed to fetch campaigns", err)
	}
	if len(ownedCampaigns) != len(campaignIDs) {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "one or more campaigns were not found", ErrCampaignNotFound)
	}

	updatedCount, err := s.campaignRepo.MarkVisible(ctx, req.CustomerID, campaignIDs)
	if err != nil {
		return nil, NewBusinessError("UNHIDE_CAMPAIGNS_FAILED", "failed to unhide campaigns", err)
	}

	return &dto.UnhideCampaignsResponse{
		Message:      "Campaigns unhidden successfully",
		UpdatedCount: updatedCount,
	}, nil
}

type campaignReportRow struct {
	AudienceProfileUID string
	Status             string
	Clicked            string
}

var campaignReportHeaders = []string{
	"Audience Profile UID",
	"Status",
	"Clicked",
}

func (s *CampaignFlowImpl) ExportCampaignReport(ctx context.Context, campaignUUID string) ([]byte, error) {
	campaignUUID = strings.TrimSpace(campaignUUID)
	if campaignUUID == "" {
		return nil, NewBusinessError("CAMPAIGN_UUID_REQUIRED", "campaign uuid is required", ErrCampaignUUIDRequired)
	}
	parsedCampaignUUID, err := uuid.Parse(campaignUUID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_UUID_INVALID", "campaign uuid is invalid", ErrCampaignUUIDRequired)
	}
	campaignUUID = parsedCampaignUUID.String()

	customerID, ok := ctx.Value(utils.CustomerIDKey).(uint)
	if !ok || customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "customer id is required", ErrCustomerNotFound)
	}

	customer, err := getCustomer(ctx, s.customerRepo, customerID)
	if err != nil {
		return nil, NewBusinessError("CUSTOMER_LOOKUP_FAILED", "failed to lookup customer", err)
	}
	auditFailure := func(message string, e error) {
		errMsg := e.Error()
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignReportExportFailed, message, false, &errMsg, nil)
	}

	campaign, err := getCampaign(ctx, s.campaignRepo, campaignUUID, customerID)
	if err != nil {
		auditFailure(fmt.Sprintf("Campaign report export failed for campaign UUID %s", campaignUUID), err)
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "failed to lookup campaign", err)
	}

	// The audience JSONL file is the durable report input: it preserves the
	// audience-profile UID to generated-short-code relationship, unlike the
	// campaign statistics projection used for ordinary campaign reads.
	allUIDs, uidToCode, err := readCampaignAudienceUIDs(campaign.ID)
	if err != nil {
		auditFailure(fmt.Sprintf("Campaign report export failed for campaign %s", campaign.UUID.String()), err)
		if os.IsNotExist(err) {
			return nil, NewBusinessError("AUDIENCE_REPORT_NOT_AVAILABLE", "audience report data is not available (may have expired or not yet pushed)", nil)
		}
		return nil, NewBusinessError("CAMPAIGN_CLICK_MAPPING_READ_FAILED", "failed to read campaign click mapping", err)
	}
	if len(allUIDs) == 0 {
		return nil, NewBusinessError("AUDIENCE_REPORT_NOT_AVAILABLE", "audience report data is not available (may have expired or not yet pushed)", nil)
	}

	clickedCodes, err := s.shortLinkClickRepo.DistinctShortLinkUIDsByCampaignID(ctx, campaign.ID)
	if err != nil {
		auditFailure(fmt.Sprintf("Campaign report export failed for campaign %s", campaign.UUID.String()), err)
		return nil, NewBusinessError("CAMPAIGN_CLICK_LOOKUP_FAILED", "failed to load campaign clicks", err)
	}
	rows := buildCampaignReportRows(allUIDs, uidToCode, clickedCodes)

	reportBytes, err := buildCampaignReportExcel(rows)
	if err != nil {
		auditFailure(fmt.Sprintf("Campaign report export failed for campaign %s", campaign.UUID.String()), err)
		return nil, NewBusinessError("CAMPAIGN_REPORT_EXPORT_FAILED", "failed to generate campaign report", err)
	}

	msg := fmt.Sprintf("Campaign report exported for %s", campaign.UUID.String())
	_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignReportExported, msg, true, nil, nil)

	return reportBytes, nil
}

// buildCampaignReportRows makes the report deterministic from the durable
// audience UID file and the set of clicked short-link codes. Delivery status
// is not represented in that file, so it is explicitly reported as unknown
// rather than inferred from a statistics projection that omits tracking rows.
func buildCampaignReportRows(allUIDs []string, uidToCode map[string]string, clickedCodes []string) []campaignReportRow {
	clickedCodeSet := make(map[string]struct{}, len(clickedCodes))
	for _, code := range clickedCodes {
		clickedCodeSet[code] = struct{}{}
	}

	sortedUIDs := append([]string(nil), allUIDs...)
	sort.Strings(sortedUIDs)
	rows := make([]campaignReportRow, 0, len(sortedUIDs))
	for _, audienceUID := range sortedUIDs {
		_, clicked := clickedCodeSet[uidToCode[audienceUID]]
		rows = append(rows, campaignReportRow{
			AudienceProfileUID: audienceUID,
			Status:             "unknown",
			Clicked:            strconv.FormatBool(clicked),
		})
	}
	return rows
}

// CalculateCampaignCapacity handles the campaign capacity calculation process
func (s *CampaignFlowImpl) CalculateCampaignCapacity(ctx context.Context, req *dto.CalculateCampaignCapacityRequest, metadata *ClientMetadata) (*dto.CalculateCampaignCapacityResponse, error) {
	if err := s.validateCalculateCampaignCapacityRequest(req); err != nil {
		return nil, NewBusinessError("CALCULATE_CAMPAIGN_CAPACITY_VALIDATION_FAILED", "Campaign capacity calculation validation failed", err)
	}

	campaign, err := getCampaignByID(ctx, s.campaignRepo, req.CampaignID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	ensureCampaignSpecDefaults(&campaign.Spec)

	usingTargetAudienceExcelFile := campaign.Spec.UsesExcelTargeting()
	if campaign.Spec.UsesSmartTargeting() {
		if campaign.Phase == models.CampaignPhaseTest {
			if campaign.SampleSizePerTag == nil {
				return nil, ErrSmartTargetingSampleSizeRequired
			}
			if *campaign.SampleSizePerTag == 0 || *campaign.SampleSizePerTag > math.MaxInt64 {
				return nil, ErrSmartTargetingSampleSizeInvalid
			}
		}
		if s.capacityCalculationRepo == nil {
			return nil, NewBusinessError("SMART_TARGETING_CAPACITY_UNAVAILABLE", "Exact Smart Targeting capacity calculation is unavailable", ErrSmartTargetingExactCapacityRequired)
		}
		exact, err := EnsureCurrentSmartTargetingCapacity(ctx, s.db, s.campaignRepo, s.selectedTagRepo, s.capacityCalculationRepo, &campaign)
		if err != nil {
			if errors.Is(err, ErrSmartTargetingCapacityPending) {
				return nil, NewBusinessError("SMART_TARGETING_CAPACITY_PENDING", "Exact Smart Targeting capacity calculation was submitted; please wait and retry", err)
			}
			return nil, NewBusinessError("SMART_TARGETING_EXACT_CAPACITY_LOOKUP_FAILED", "Failed to validate exact Smart Targeting capacity", err)
		}
		if exact.UsableUniqueAudienceCount < 0 {
			return nil, NewBusinessError("SMART_TARGETING_CAPACITY_INVALID", "Exact Smart Targeting capacity is invalid", ErrInvalidState)
		}
		return &dto.CalculateCampaignCapacityResponse{
			Message: "Campaign capacity calculated successfully", Capacity: uint64(exact.UsableUniqueAudienceCount),
			AudienceGradeCapacity: map[string]uint64{audienceGradeA: 0, audienceGradeB: 0, audienceGradeC: 0},
		}, nil
	}
	if !usingTargetAudienceExcelFile {
		if campaign.Spec.Level1 == nil {
			return nil, NewBusinessError("CALCULATE_CAMPAIGN_CAPACITY_VALIDATION_FAILED", "Campaign capacity calculation validation failed", ErrCampaignLevel1Required)
		}
		if len(campaign.Spec.Level2s) == 0 {
			return nil, NewBusinessError("CALCULATE_CAMPAIGN_CAPACITY_VALIDATION_FAILED", "Campaign capacity calculation validation failed", ErrCampaignLevel2sRequired)
		}
		if len(campaign.Spec.Level3s) == 0 {
			return nil, NewBusinessError("CALCULATE_CAMPAIGN_CAPACITY_VALIDATION_FAILED", "Campaign capacity calculation validation failed", ErrCampaignLevel3sRequired)
		}
		if len(campaign.Spec.Tags) == 0 {
			return nil, NewBusinessError("CALCULATE_CAMPAIGN_CAPACITY_VALIDATION_FAILED", "Campaign capacity calculation validation failed", ErrCampaignTagsRequired)
		}
	}
	if usingTargetAudienceExcelFile {
		if campaign.Spec.TargetAudienceExcelFileUUID == nil ||
			strings.TrimSpace(*campaign.Spec.TargetAudienceExcelFileUUID) == "" {
			return nil, NewBusinessError("EXCEL_FILE_REQUIRED", "Excel targeting requires a target audience file", ErrCampaignTargetAudienceExcelFileInvalid)
		}
		count, err := s.CountTargetAudienceFromExcelFile(ctx, campaign.CustomerID, strings.TrimSpace(*campaign.Spec.TargetAudienceExcelFileUUID))
		if err != nil {
			switch {
			case errors.Is(err, os.ErrNotExist):
				return nil, NewBusinessError("EXCEL_MEDIA_NOT_FOUND", "Excel file not found", ErrCampaignTargetAudienceExcelMediaNotFound)
			case errors.Is(err, ErrCampaignTargetAudienceExcelFileInvalid):
				return nil, NewBusinessError("EXCEL_FILE_INVALID", "Excel file is invalid", ErrCampaignTargetAudienceExcelFileInvalid)
			default:
				return nil, NewBusinessError("EXCEL_CAPACITY_READ_FAILED", "Failed to read excel audience", err)
			}
		}
		return &dto.CalculateCampaignCapacityResponse{
			Message:               "Campaign capacity calculated successfully",
			Capacity:              count,
			AudienceGradeCapacity: map[string]uint64{audienceGradeA: 0, audienceGradeB: 0, audienceGradeC: 0},
		}, nil
	}

	// Fetch the database-backed audience spec (with Redis used only as a
	// five-minute cache).
	specResp, err := s.ListAudienceSpec(ctx, &campaign.Spec.Platform)
	if err != nil {
		return nil, NewBusinessError("LIST_AUDIENCE_SPEC_FAILED", "Failed to load audience spec", err)
	}

	var capacity uint64
	audienceGradeCapacity := map[string]uint64{
		audienceGradeA: 0,
		audienceGradeB: 0,
		audienceGradeC: 0,
	}

	// Build a set of requested tags for quick lookup
	tagSet := make(map[string]struct{}, len(campaign.Spec.Tags))
	for _, t := range campaign.Spec.Tags {
		if t != "" {
			tagSet[t] = struct{}{}
		}
	}

	// Sum available audience only for requested Level1/Level2/Level3 keys
	// Respect provided tags: if tags set is empty, count all items; otherwise only items with matching tags.
	if campaign.Spec.Level1 != nil {
		l1k := *campaign.Spec.Level1
		l1map, ok := specResp.Spec[l1k]
		if ok {
			// prepare lookups for requested level2s and level3s
			level2Set := make(map[string]struct{}, len(campaign.Spec.Level2s))
			for _, l2 := range campaign.Spec.Level2s {
				if l2 != "" {
					level2Set[l2] = struct{}{}
				}
			}
			level3Set := make(map[string]struct{}, len(campaign.Spec.Level3s))
			for _, l3 := range campaign.Spec.Level3s {
				if l3 != "" {
					level3Set[l3] = struct{}{}
				}
			}

			for l2k, node := range l1map {
				// skip level2s not requested
				if len(level2Set) > 0 {
					if _, ok := level2Set[l2k]; !ok {
						continue
					}
				}
				if len(node.Items) == 0 && len(node.Metadata) == 0 {
					continue
				}
				for l3k, item := range node.Items {
					// skip level3s not requested
					if len(level3Set) > 0 {
						if _, ok := level3Set[l3k]; !ok {
							continue
						}
					}
					if len(tagSet) > 0 {
						matched := false
						for _, it := range item.Tags {
							if _, ok := tagSet[it]; ok {
								matched = true
								break
							}
						}
						if !matched {
							continue
						}
					}

					selectedCapacity, gradeCapacity, capacityErr := calculateAudienceSpecItemCapacity(
						campaign.Spec.Platform,
						campaign.Spec.AudienceGrades,
						item,
					)
					if capacityErr != nil {
						return nil, NewBusinessError("AUDIENCE_STATS_INVALID", "Audience statistics are invalid", capacityErr)
					}
					capacity, capacityErr = addAudienceCapacity(capacity, selectedCapacity)
					if capacityErr != nil {
						return nil, NewBusinessError("AUDIENCE_CAPACITY_OVERFLOW", "Audience capacity exceeds the supported range", capacityErr)
					}
					for grade, gradeValue := range gradeCapacity {
						audienceGradeCapacity[grade], capacityErr = addAudienceCapacity(audienceGradeCapacity[grade], gradeValue)
						if capacityErr != nil {
							return nil, NewBusinessError("AUDIENCE_CAPACITY_OVERFLOW", "Audience capacity exceeds the supported range", capacityErr)
						}
					}
				}
			}
		}
	}

	return &dto.CalculateCampaignCapacityResponse{
		Message:               "Campaign capacity calculated successfully",
		Capacity:              capacity,
		AudienceGradeCapacity: audienceGradeCapacity,
	}, nil
}

func calculateAudienceSpecItemCapacity(platform string, selectedGrades []string, item dto.AudienceSpecItem) (uint64, map[string]uint64, error) {
	if !models.IsValidCampaignPlatform(platform) {
		return 0, nil, ErrCampaignPlatformInvalid
	}

	gradeCapacity := make(map[string]uint64, 3)
	for _, grade := range []string{audienceGradeA, audienceGradeB, audienceGradeC} {
		capacity, err := audienceSpecItemGradeCapacity(platform, grade, item)
		if err != nil {
			return 0, nil, err
		}
		gradeCapacity[grade] = capacity
	}

	var selectedCapacity uint64
	for _, grade := range campaignAudienceGradesOrDefault(selectedGrades) {
		grade = strings.ToUpper(strings.TrimSpace(grade))
		capacity, ok := gradeCapacity[grade]
		if !ok {
			return 0, nil, ErrCampaignAudienceGradesInvalid
		}
		var err error
		selectedCapacity, err = addAudienceCapacity(selectedCapacity, capacity)
		if err != nil {
			return 0, nil, err
		}
	}
	return selectedCapacity, gradeCapacity, nil
}

func audienceSpecItemGradeCapacity(platform, grade string, item dto.AudienceSpecItem) (uint64, error) {
	var white, pink, black int64
	switch grade {
	case audienceGradeA:
		white, pink, black = item.BestWhite, item.BestPink, item.BestBlack
	case audienceGradeB:
		white, pink, black = item.GoodWhite, item.GoodPink, item.GoodBlack
	case audienceGradeC:
		white, pink, black = item.WeakWhite, item.WeakPink, item.WeakBlack
	default:
		return 0, ErrCampaignAudienceGradesInvalid
	}
	if white < 0 || pink < 0 || black < 0 {
		return 0, fmt.Errorf("negative audience grade statistics")
	}

	capacity := uint64(white)
	var err error
	if platform == models.CampaignPlatformSMS {
		return addAudienceCapacity(capacity, uint64(pink)/3)
	}
	capacity, err = addAudienceCapacity(capacity, uint64(pink))
	if err != nil {
		return 0, err
	}
	return addAudienceCapacity(capacity, uint64(black))
}

func addAudienceCapacity(total, value uint64) (uint64, error) {
	if ^uint64(0)-total < value {
		return 0, fmt.Errorf("audience capacity overflow")
	}
	return total + value, nil
}

// CalculateCampaignCost handles the campaign cost calculation process
func (s *CampaignFlowImpl) CalculateCampaignCost(ctx context.Context, req *dto.CalculateCampaignCostRequest, metadata *ClientMetadata) (*dto.CalculateCampaignCostResponse, error) {
	if req.CampaignID == 0 {
		return nil, NewBusinessError("CALCULATE_CAMPAIGN_COST_VALIDATION_FAILED", "Campaign cost calculation validation failed", ErrCampaignNotFound)
	}

	campaign, err := getCampaignByID(ctx, s.campaignRepo, req.CampaignID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}

	pricePerMsg, availableCapacity, err := s.computeCostInputs(ctx, campaign, metadata)
	if err != nil {
		return nil, err
	}

	numTargetAudience := availableCapacity
	if campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest {
		if currentErr := s.requireCurrentSmartTargetingTestSampling(ctx, &campaign); currentErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_PREVIEW_REQUIRED", "A current Smart Targeting Test sampling preview is required", currentErr)
		}
		intent, intentErr := currentSmartTargetingTestSamplingIntent(ctx, s.selectedTagRepo, &campaign, true)
		if intentErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_PREVIEW_REQUIRED", "A current Smart Targeting Test sampling preview is required", intentErr)
		}
		numTargetAudience = intent.effective
		availableCapacity = intent.effective
	}
	budget := campaign.Spec.Budget
	if req.Budget != nil {
		budget = req.Budget
	}
	if budget != nil && !(campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest) {
		numTargetAudience = uint64(math.Min(float64(availableCapacity), float64(*budget)/float64(pricePerMsg)))
	}

	totalCost, err := checkedCampaignCost(pricePerMsg, numTargetAudience)
	if err != nil {
		return nil, err
	}

	if req.CustomerID != 0 {
		customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
		if err == nil && s.adminConfig.HasMobile(customer.RepresentativeMobile) {
			totalCost = 0
		}
	}

	return &dto.CalculateCampaignCostResponse{
		Message:           "Campaign cost calculated successfully",
		TotalCost:         totalCost,
		NumTargetAudience: numTargetAudience,
		MaxTargetAudience: availableCapacity,
	}, nil
}

// CalculateCampaignCostV2 calculates required cost for desired num_messages
// and caps num_messages by available audience capacity.
func (s *CampaignFlowImpl) CalculateCampaignCostV2(ctx context.Context, req *dto.CalculateCampaignCostV2Request, metadata *ClientMetadata) (*dto.CalculateCampaignCostResponse, error) {
	campaign, err := getCampaignByID(ctx, s.campaignRepo, req.CampaignID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}

	pricePerMsg, availableCapacity, err := s.computeCostInputs(ctx, campaign, metadata)
	if err != nil {
		return nil, err
	}

	numTargetAudience := req.NumMessages
	if campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest {
		if currentErr := s.requireCurrentSmartTargetingTestSampling(ctx, &campaign); currentErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_PREVIEW_REQUIRED", "A current Smart Targeting Test sampling preview is required", currentErr)
		}
		intent, intentErr := currentSmartTargetingTestSamplingIntent(ctx, s.selectedTagRepo, &campaign, true)
		if intentErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_PREVIEW_REQUIRED", "A current Smart Targeting Test sampling preview is required", intentErr)
		}
		numTargetAudience = intent.effective
		availableCapacity = intent.effective
	}
	if campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseExecution && numTargetAudience > availableCapacity {
		return nil, NewBusinessError("SMART_TARGETING_REQUEST_EXCEEDS_CAPACITY", "The requested audience count exceeds the exact usable capacity.", ErrInsufficientCampaignCapacity)
	}
	if numTargetAudience > availableCapacity {
		numTargetAudience = availableCapacity
	}

	totalCost, err := checkedCampaignCost(pricePerMsg, numTargetAudience)
	if err != nil {
		return nil, err
	}

	if req.CustomerID != 0 {
		customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
		if err == nil && s.adminConfig.HasMobile(customer.RepresentativeMobile) {
			totalCost = 0
		}
	}

	return &dto.CalculateCampaignCostResponse{
		Message:           "Campaign cost calculated successfully",
		TotalCost:         totalCost,
		NumTargetAudience: numTargetAudience,
		MaxTargetAudience: availableCapacity,
	}, nil
}

func (s *CampaignFlowImpl) computePricePerMessage(ctx context.Context, campaign models.Campaign) (uint64, error) {
	platform, err := sanitizeCampaignPlatform(&campaign.Spec.Platform)
	if err != nil {
		return 0, NewBusinessError("CAMPAIGN_VALIDATION_FAILED", "Campaign validation failed", err)
	}

	if platform == models.CampaignPlatformSMS && campaign.Spec.LineNumber == nil {
		return 0, NewBusinessError("LINE_NUMBER_REQUIRED", "Line number is required for SMS campaigns", ErrCampaignLineNumberRequired)
	}

	usingTargetAudienceExcelFile := campaign.Spec.UsesExcelTargeting()
	if len(campaign.Spec.Level3s) == 0 && !usingTargetAudienceExcelFile && !campaign.Spec.UsesSmartTargeting() {
		return 0, NewBusinessError("LEVEL3_REQUIRED", "At least one level3 option or target audience Excel file is required for cost calculation", ErrLevel3Required)
	}

	// Pricing constants
	numParts := s.calculateParts(
		campaign.Spec.Content,
		campaign.Spec.AdLink,
		campaign.Spec.ShortLinkDomain,
		platform,
	)

	lineNumberFactor := defaultLineNumberPriceFactor
	if platform == models.CampaignPlatformSMS && campaign.Spec.LineNumber != nil && strings.TrimSpace(*campaign.Spec.LineNumber) != "" {
		var err error
		lineNumberFactor, err = s.fetchLineNumberPriceFactor(ctx, campaign.Spec.LineNumber)
		if err != nil {
			return 0, NewBusinessError("LINE_NUMBER_PRICE_FACTOR_FETCH_FAILED", "Failed to fetch line number price factor", err)
		}
	}

	segmentPriceFactor := defaultSegmentPriceFactor
	if len(campaign.Spec.Level3s) > 0 && !usingTargetAudienceExcelFile && !campaign.Spec.UsesSmartTargeting() {
		maxFactor, err := s.fetchSegmentPriceFactor(ctx, campaign.Spec.Level3s, platform)
		if err != nil {
			if errors.Is(err, ErrSegmentPriceFactorNotFound) {
				s.notifyMissingSegmentPriceFactor(campaign.Spec.Level3s)
				return 0, NewBusinessError("SEGMENT_PRICE_FACTOR_NOT_FOUND", "Segment price factor not found for provided level3 options", ErrSegmentPriceFactorNotFound)
			}
			return 0, NewBusinessError("SEGMENT_PRICE_FACTOR_FETCH_FAILED", "Failed to fetch segment price factors", err)
		}
		if maxFactor == 0 {
			s.notifyMissingSegmentPriceFactor(campaign.Spec.Level3s)
			return 0, NewBusinessError("SEGMENT_PRICE_FACTOR_NOT_FOUND", "Segment price factor not found for provided level3 options", ErrSegmentPriceFactorNotFound)
		}
		segmentPriceFactor = maxFactor
	}

	pricePerMsg := uint64(0)

	pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, platform)
	if err != nil {
		return 0, NewBusinessError("PLATFORM_BASE_PRICE_FETCH_FAILED", "Failed to fetch platform base price", err)
	}
	if pbp == nil {
		return 0, NewBusinessError("PLATFORM_BASE_PRICE_NOT_FOUND", "Platform base price not found for platform "+platform, ErrPlatformBasePriceNotFound)
	}
	pp, err := s.pagePriceRepo.LatestByPlatform(ctx, platform)
	if err != nil {
		return 0, NewBusinessError("PAGE_PRICE_FETCH_FAILED", "Failed to fetch page price", err)
	}
	if pp == nil {
		return 0, NewBusinessError("PAGE_PRICE_NOT_FOUND", "Page price not found for platform "+platform, ErrPagePriceNotFound)
	}
	pagePrice := float64(pp.Price)
	numPages := float64(numParts)
	if platform == models.CampaignPlatformSMS {
		pricePerMsg = pbp.Price*uint64(lineNumberFactor*numPages) + uint64(segmentPriceFactor*pagePrice)
	} else {
		pricePerMsg = pbp.Price*uint64(1*1) + uint64(segmentPriceFactor*pagePrice)
	}
	return pricePerMsg, nil
}

func (s *CampaignFlowImpl) computeCostInputs(
	ctx context.Context,
	campaign models.Campaign,
	metadata *ClientMetadata,
) (uint64, uint64, error) {
	pricePerMsg, err := s.computePricePerMessage(ctx, campaign)
	if err != nil {
		return 0, 0, err
	}

	// Smart Targeting cost must use the persisted exact generation rather than
	// Feature 2's raw sum. Pending, failed, expired, or fingerprint-stale
	// generations are deliberately rejected so budget reservations cannot use a
	// capacity that is no longer operationally valid.
	if campaign.Spec.UsesSmartTargeting() {
		if s.capacityCalculationRepo == nil {
			return 0, 0, NewBusinessError("SMART_TARGETING_EXACT_CAPACITY_REQUIRED", "A current exact Smart Targeting capacity calculation is required", ErrSmartTargetingExactCapacityRequired)
		}
		exact, err := EnsureCurrentSmartTargetingCapacity(ctx, s.db, s.campaignRepo, s.selectedTagRepo, s.capacityCalculationRepo, &campaign)
		if err != nil {
			if errors.Is(err, ErrSmartTargetingCapacityPending) {
				return 0, 0, NewBusinessError("SMART_TARGETING_CAPACITY_PENDING", "Exact Smart Targeting capacity calculation was submitted; please wait and retry", err)
			}
			return 0, 0, NewBusinessError("SMART_TARGETING_EXACT_CAPACITY_LOOKUP_FAILED", "Failed to validate exact Smart Targeting capacity", err)
		}
		if exact.UsableUniqueAudienceCount < 0 {
			return 0, 0, NewBusinessError("SMART_TARGETING_EXACT_CAPACITY_INVALID", "Exact Smart Targeting capacity is invalid", ErrInvalidState)
		}
		return pricePerMsg, uint64(exact.UsableUniqueAudienceCount), nil
	}

	// Calculate campaign capacity (target audience size) for standard and Excel
	// targeting. Their established behavior remains unchanged.
	capacityResp, err := s.CalculateCampaignCapacity(ctx, &dto.CalculateCampaignCapacityRequest{
		CampaignID: campaign.ID,
		CustomerID: campaign.CustomerID,
	}, metadata)
	if err != nil {
		return 0, 0, NewBusinessError("CAPACITY_CALCULATION_FAILED", "Failed to calculate campaign capacity", err)
	}
	return pricePerMsg, capacityResp.Capacity, nil
}

func (s *CampaignFlowImpl) fetchLineNumberPriceFactor(ctx context.Context, lineNumber *string) (float64, error) {
	if lineNumber == nil || strings.TrimSpace(*lineNumber) == "" {
		return 0, ErrLineNumberNotFound
	}

	ln, err := s.lineNumberRepo.ByValue(ctx, *lineNumber)
	if err != nil {
		return 0, err
	}
	if ln == nil {
		return 0, ErrLineNumberNotFound
	}
	if !utils.IsTrue(ln.IsActive) {
		return 0, ErrLineNumberNotActive
	}

	return ln.PriceFactor, nil
}

func (s *CampaignFlowImpl) fetchSegmentPriceFactor(ctx context.Context, level3s []string, platform string) (float64, error) {
	factors, err := s.segmentPriceRepo.LatestByLevel3sForPlatform(ctx, level3s, platform)
	if err != nil {
		return 0, err
	}
	maxFactor := float64(0)
	for _, l3 := range level3s {
		if f, ok := factors[l3]; ok && f > maxFactor {
			maxFactor = f
		}
	}
	if maxFactor == 0 {
		return 0, ErrSegmentPriceFactorNotFound
	}

	return maxFactor, nil
}

func (s *CampaignFlowImpl) notifyMissingSegmentPriceFactor(level3s []string) {
	if s.notifier == nil {
		return
	}
	msg := fmt.Sprintf("Segment price factor missing for level3: %s", strings.Join(level3s, ","))
	go func() {
		for _, mobile := range s.adminConfig.ActiveMobiles() {
			_ = s.notifier.SendSMS(context.Background(), mobile, msg, nil)
		}
	}()
}

// campaignDisplayEnrichments holds computed pricing and settings data for building a GetCampaignResponse.
type campaignDisplayEnrichments struct {
	platformBasePrice    *uint64
	linePriceFactor      *float64
	segmentPriceFactor   *float64
	platformSettingsName *string
}

// fetchCampaignDisplayEnrichments computes display pricing and settings data for a campaign.
func (s *CampaignFlowImpl) fetchCampaignDisplayEnrichments(ctx context.Context, c *models.Campaign) (campaignDisplayEnrichments, error) {
	var e campaignDisplayEnrichments

	pbp, err := s.resolvePlatformBasePrice(ctx, c.ID, c.Spec.Platform)
	if err != nil {
		return e, err
	}
	e.platformBasePrice = pbp

	lpf, err := s.resolveLinePriceFactor(ctx, c.ID, c.Spec.LineNumber)
	if err != nil {
		return e, err
	}
	e.linePriceFactor = lpf

	spf, err := s.resolveSegmentPriceFactor(ctx, c.ID, c.Spec.Level3s, c.Spec.Platform)
	if err != nil {
		return e, err
	}
	e.segmentPriceFactor = spf

	name, err := s.resolvePlatformSettingsName(ctx, c.Spec.PlatformSettingsID)
	if err != nil {
		return e, err
	}
	e.platformSettingsName = name

	return e, nil
}

// resolvePlatformBasePrice reads base price from transaction metadata,
// falling back to the platform base price repo if not found.
func (s *CampaignFlowImpl) resolvePlatformBasePrice(ctx context.Context, campaignID uint, platform string) (*uint64, error) {
	basePrice, err := s.readPlatformBasePriceFromMetadata(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if basePrice != nil {
		return basePrice, nil
	}

	pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	if pbp == nil {
		return nil, nil
	}
	return &pbp.Price, nil
}

// readPlatformBasePriceFromMetadata reads the base_price from the latest
// campaign finalization transaction metadata.
func (s *CampaignFlowImpl) readPlatformBasePriceFromMetadata(ctx context.Context, campaignID uint) (*uint64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil
	}

	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil
	}

	basePrice, ok := parseMetadataUint64(meta["base_price"])
	if !ok {
		return nil, nil
	}
	return &basePrice, nil
}

// resolveSegmentPriceFactor reads segment price factor from transaction metadata,
// falling back to the segment price repo if not found.
func (s *CampaignFlowImpl) resolveSegmentPriceFactor(ctx context.Context, campaignID uint, level3s []string, platform string) (*float64, error) {
	spf, err := s.readSegmentPriceFromMetadata(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if spf != nil {
		return spf, nil
	}
	if len(level3s) == 0 {
		return nil, nil
	}
	factor, err := s.fetchSegmentPriceFactor(ctx, level3s, platform)
	if err != nil {
		if errors.Is(err, ErrSegmentPriceFactorNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &factor, nil
}

// readSegmentPriceFromMetadata reads the segment_price_factor from the latest
// campaign finalization transaction metadata.
func (s *CampaignFlowImpl) readSegmentPriceFromMetadata(ctx context.Context, campaignID uint) (*float64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil
	}
	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil
	}
	return parseMetadataFloat(meta["segment_price_factor"]), nil
}

// resolveLinePriceFactor reads line price factor from transaction metadata,
// falling back to the line number repo if not found.
func (s *CampaignFlowImpl) resolveLinePriceFactor(ctx context.Context, campaignID uint, lineNumber *string) (*float64, error) {
	lpf, err := s.readLinePriceFromMetadata(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if lpf != nil {
		return lpf, nil
	}
	if lineNumber == nil {
		return nil, nil
	}
	ln, err := s.lineNumberRepo.ByValue(ctx, *lineNumber)
	if err != nil {
		return nil, err
	}
	if ln == nil {
		return nil, nil
	}
	return &ln.PriceFactor, nil
}

// readLinePriceFromMetadata reads the line_number_price_factor from the latest
// campaign finalization transaction metadata.
func (s *CampaignFlowImpl) readLinePriceFromMetadata(ctx context.Context, campaignID uint) (*float64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil
	}
	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil
	}
	return parseMetadataFloat(meta["line_number_price_factor"]), nil
}

// resolvePlatformSettingsName returns the Name of a platform settings record by ID.
func (s *CampaignFlowImpl) resolvePlatformSettingsName(ctx context.Context, id *uint) (*string, error) {
	if id == nil || *id == 0 {
		return nil, nil
	}
	settings, err := s.platformSettingsRepo.ByID(ctx, *id)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, nil
	}
	return settings.Name, nil
}

func sanitizeAudienceTargetingMethod(method *string) (string, error) {
	if method == nil || strings.TrimSpace(*method) == "" {
		return models.CampaignAudienceTargetingStandard, nil
	}
	value := strings.ToLower(strings.TrimSpace(*method))
	if !models.IsValidCampaignAudienceTargetingMethod(value) {
		return "", ErrCampaignAudienceTargetingMethodInvalid
	}
	return value, nil
}

// resolveAudienceTargetingMethod preserves the legacy two-field resolver for
// callers that cannot submit Smart Targeting selections.
func resolveAudienceTargetingMethod(method *string, targetAudienceExcelFileUUID *string) (string, error) {
	return resolveAudienceTargetingMethodWithSelectedTags(method, nil, targetAudienceExcelFileUUID)
}

func resolveAudienceTargetingMethodWithSelectedTags(method *string, selectedTagIDs []uint, targetAudienceExcelFileUUID *string) (string, error) {
	// An explicit method always wins. The UI intentionally retains values from
	// previously explored methods, so those values must never override it.
	if method != nil && strings.TrimSpace(*method) != "" {
		return sanitizeAudienceTargetingMethod(method)
	}

	// Older clients do not submit audience_targeting_method. Resolve their
	// payload deterministically, with Smart Targeting taking precedence over
	// Excel and standard targeting.
	if len(selectedTagIDs) > 0 {
		return models.CampaignAudienceTargetingSmart, nil
	}
	if targetAudienceExcelFileUUID != nil && strings.TrimSpace(*targetAudienceExcelFileUUID) != "" {
		return models.CampaignAudienceTargetingExcel, nil
	}
	return models.CampaignAudienceTargetingStandard, nil
}

func campaignAudienceTargetingMethod(spec models.CampaignSpec) string {
	return spec.EffectiveAudienceTargetingMethod()
}

func (s *CampaignFlowImpl) prepareAudienceTargetingUpdate(ctx context.Context, req *dto.UpdateCampaignRequest, campaign *models.Campaign) error {
	currentMethod := campaignAudienceTargetingMethod(campaign.Spec)
	finalMethod := currentMethod

	if req.AudienceTargetingMethod != nil {
		method, err := sanitizeAudienceTargetingMethod(req.AudienceTargetingMethod)
		if err != nil {
			return err
		}
		finalMethod = method
	} else if req.SelectedTagIDs != nil && len(*req.SelectedTagIDs) > 0 {
		// Method-less legacy updates use the same priority as creates. A supplied
		// non-empty selection is enough to make Smart Targeting active.
		finalMethod = models.CampaignAudienceTargetingSmart
	} else if req.TargetAudienceExcelFileUUID != nil && strings.TrimSpace(*req.TargetAudienceExcelFileUUID) != "" {
		finalMethod = models.CampaignAudienceTargetingExcel
	}
	req.AudienceTargetingMethod = &finalMethod

	if finalMethod != models.CampaignAudienceTargetingSmart {
		if finalMethod == models.CampaignAudienceTargetingExcel {
			excelFileUUID := campaign.Spec.TargetAudienceExcelFileUUID
			if req.TargetAudienceExcelFileUUID != nil {
				excelFileUUID = req.TargetAudienceExcelFileUUID
			}
			if excelFileUUID == nil || strings.TrimSpace(*excelFileUUID) == "" {
				return ErrCampaignTargetAudienceExcelFileInvalid
			}
		}
		return nil
	}

	if req.SelectedTagIDs != nil {
		normalized, err := normalizeSelectedTagIDs(*req.SelectedTagIDs)
		if err != nil {
			return err
		}
		req.SelectedTagIDs = &normalized
		return nil
	}

	bundleChanging := req.BundleID != nil && (campaign.BundleID == nil || *req.BundleID != *campaign.BundleID)
	if currentMethod != models.CampaignAudienceTargetingSmart || bundleChanging {
		return ErrSmartTargetingTagsRequired
	}
	summary, err := s.selectedTagRepo.Summary(ctx, campaign.ID)
	if err != nil {
		return err
	}
	if summary.SelectedTagCount == 0 {
		return ErrSmartTargetingTagsRequired
	}
	return nil
}

func campaignPhasePtr(phase models.CampaignPhase) *string {
	if !phase.Valid() {
		return nil
	}
	value := phase.String()
	return &value
}

func campaignPhaseOrDefault(phase *string) models.CampaignPhase {
	if phase == nil {
		return models.CampaignPhaseExecution
	}
	trimmed := strings.TrimSpace(*phase)
	campaignPhase := models.CampaignPhase(trimmed)
	if !campaignPhase.Valid() {
		return models.CampaignPhaseExecution
	}
	return campaignPhase
}

func validateCampaignPhaseInput(phase *string, required bool) error {
	if phase == nil {
		if required {
			return ErrCampaignPhaseRequired
		}
		return nil
	}

	trimmed := strings.TrimSpace(*phase)
	if trimmed == "" {
		if required {
			return ErrCampaignPhaseRequired
		}
		return ErrCampaignPhaseInvalid
	}

	if !models.CampaignPhase(trimmed).Valid() {
		return ErrCampaignPhaseInvalid
	}

	return nil
}

func normalizeCampaignIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}

	normalized := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			normalized = append(normalized, id)
		}
	}
	if len(normalized) == 0 {
		return nil
	}

	slices.Sort(normalized)
	return slices.Compact(normalized)
}

// buildCampaignResponse constructs a GetCampaignResponse from a campaign model and its display enrichments.
func buildCampaignResponse(c *models.Campaign, e campaignDisplayEnrichments) dto.GetCampaignResponse {
	ensureCampaignSpecDefaults(&c.Spec)

	var bundleTitle *string
	if c.Bundle != nil {
		bundleTitle = &c.Bundle.Title
	}

	return dto.GetCampaignResponse{
		ID:                          c.ID,
		UUID:                        c.UUID.String(),
		Hidden:                      c.Hidden,
		Status:                      c.Status.String(),
		CreatedAt:                   c.CreatedAt,
		UpdatedAt:                   c.UpdatedAt,
		Title:                       c.Spec.Title,
		Level1:                      c.Spec.Level1,
		Level2s:                     c.Spec.Level2s,
		Level3s:                     c.Spec.Level3s,
		Tags:                        c.Spec.Tags,
		TargetingMethod:             campaignAudienceTargetingMethod(c.Spec),
		Sex:                         c.Spec.Sex,
		City:                        c.Spec.City,
		AdLink:                      c.Spec.AdLink,
		Content:                     c.Spec.Content,
		ShortLinkDomain:             c.Spec.ShortLinkDomain,
		Category:                    c.Spec.Category,
		Job:                         c.Spec.Job,
		ScheduleAt:                  c.Spec.ScheduleAt,
		LineNumber:                  c.Spec.LineNumber,
		MediaUUID:                   c.Spec.MediaUUID,
		PlatformSettingsID:          c.Spec.PlatformSettingsID,
		Platform:                    c.Spec.Platform,
		PlatformBasePrice:           e.platformBasePrice,
		LinePriceFactor:             e.linePriceFactor,
		SegmentPriceFactor:          e.segmentPriceFactor,
		PlatformSettingsName:        e.platformSettingsName,
		Budget:                      c.Spec.Budget,
		NumAudience:                 c.NumAudience,
		SampleSizePerTag:            c.SampleSizePerTag,
		Comment:                     c.Comment,
		BundleID:                    c.BundleID,
		BundleTitle:                 bundleTitle,
		Phase:                       campaignPhasePtr(c.Phase),
		AudienceGrades:              campaignAudienceGradesOrDefault(c.Spec.AudienceGrades),
		TargetAudienceExcelFileUUID: c.Spec.TargetAudienceExcelFileUUID,
	}
}

// ListCampaigns retrieves user's campaigns with pagination, ordering and filters
func (s *CampaignFlowImpl) ListCampaigns(ctx context.Context, req *dto.ListCampaignsRequest, metadata *ClientMetadata) (*dto.ListCampaignsResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_CAMPAIGNS_FAILED", "Failed to list campaigns", err)
		}
	}()

	// Validate customer
	_, err = getCustomer(ctx, s.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// if s.tryAcquireFlowLock(ctx, fmt.Sprintf("list_campaigns_expire:%d", req.CustomerID), 20*time.Second) {
	// 	if err := s.expireCustomerCampaigns(ctx, req.CustomerID); err != nil {
	// 		return nil, err
	// 	}
	// }

	if s.tryAcquireFlowLock(ctx, fmt.Sprintf("list_campaigns_reconcile_refund:%d", req.CustomerID), 20*time.Second) {
		if err := s.reconcileUndeliveredCampaignRefunds(ctx, req.CustomerID); err != nil {
			return nil, err
		}
	}

	// Normalize pagination
	page := max(1, req.Page)
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Build filter
	filter := models.CampaignFilter{
		CustomerID: &req.CustomerID,
	}
	if req.Filter != nil {
		if req.Filter.CampaignTitle != nil {
			title := strings.TrimSpace(*req.Filter.CampaignTitle)
			if title != "" {
				filter.CampaignTitle = &title
			}
		}
		if req.Filter.BundleTitle != nil {
			bundleTitle := strings.TrimSpace(*req.Filter.BundleTitle)
			if bundleTitle != "" {
				filter.BundleTitle = &bundleTitle
			}
		}
		if req.Filter.CustomerName != nil {
			customerName := strings.TrimSpace(*req.Filter.CustomerName)
			if customerName != "" {
				filter.CustomerName = &customerName
			}
		}
		if req.Filter.Status != nil {
			statusValue := strings.TrimSpace(*req.Filter.Status)
			status := models.CampaignStatus(statusValue)
			if status.Valid() {
				filter.Status = &status
			}
		}
		if req.Filter.BundleID != nil && *req.Filter.BundleID > 0 {
			filter.BundleID = req.Filter.BundleID
		}
		if req.Filter.Platform != nil {
			platform := strings.ToLower(strings.TrimSpace(*req.Filter.Platform))
			if models.IsValidCampaignPlatform(platform) {
				filter.Platform = &platform
			}
		}
		if req.Filter.StartDate != nil {
			filter.CreatedAfter = req.Filter.StartDate
		}
		if req.Filter.EndDate != nil {
			filter.CreatedBefore = req.Filter.EndDate
		}
		if req.Filter.Phase != nil {
			phaseValue := strings.TrimSpace(*req.Filter.Phase)
			phase := models.CampaignPhase(phaseValue)
			if phase.Valid() {
				filter.Phase = &phase
			}
		}
		if req.Filter.StartDate != nil && req.Filter.EndDate != nil && req.Filter.EndDate.Before(*req.Filter.StartDate) {
			return nil, ErrStartDateAfterEndDate
		}
	}

	// Order by. Default order mirrors admin campaign listing:
	// initiated and in-progress campaigns at the end, then campaigns without a
	// schedule first, upcoming schedules before past ones, upcoming sorted
	// soonest-first and past sorted most-recent-first.
	// Named sentinel values are resolved inside applyOrder in the repository,
	// which appends the schedule_at secondary ordering to each of them.
	orderBy := "default_schedule_at"
	switch req.OrderBy {
	case "oldest", "newest", "phase_test_first", "phase_execution_first", "highest_click_rate", "lowest_click_rate":
		orderBy = req.OrderBy
	}

	// Count total
	total64, err := s.campaignRepo.Count(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Fetch rows
	rows, err := s.campaignRepo.ByFilter(ctx, filter, orderBy, limit, offset)
	if err != nil {
		return nil, err
	}

	// Precompute click counts per campaign
	campaignIDs := make([]uint, 0, len(rows))
	for _, c := range rows {
		campaignIDs = append(campaignIDs, c.ID)
	}
	clickCounts, err := s.campaignRepo.AggregateClickCountsByCampaignIDs(ctx, campaignIDs)
	if err != nil {
		return nil, err
	}

	// Map to response items
	items := make([]dto.GetCampaignResponse, 0, len(rows))
	for _, c := range rows {
		var statsMap map[string]any
		if len(c.Statistics) > 0 {
			_ = json.Unmarshal(c.Statistics, &statsMap)
		}
		clicks := clickCounts[c.ID]
		totalClicks := clicks
		// computeClickRate returns nil when aggregatedTotalSent is 0 so callers can
		// distinguish "campaign not yet executed" from "0% click-through rate".
		// Note: reconcileUndeliveredCampaignRefunds (called above) only appends
		// "undeliveredRefund*" keys to Statistics; it never overwrites
		// aggregatedTotalSent, so the value read here is always the authoritative
		// delivery count set by the campaign scheduler.
		clickRate := computeClickRate(clicks, parseAggregatedTotalSentFromMap(statsMap))

		enrichments, err := s.fetchCampaignDisplayEnrichments(ctx, c)
		if err != nil {
			return nil, err
		}

		item := buildCampaignResponse(c, enrichments)
		item.Statistics = statsMap
		item.ClickRate = clickRate
		item.TotalClicks = &totalClicks

		items = append(items, item)
	}

	// Build pagination
	totalPages := int((total64 + int64(limit) - 1) / int64(limit))

	return &dto.ListCampaignsResponse{
		Message: "Campaigns retrieved successfully",
		Items:   items,
		Pagination: dto.PaginationInfo{
			Total:      total64,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}, nil
}

// GetLastInitiatedCampaign retrieves the most recent initiated or in-progress campaign for the given customer.
func (s *CampaignFlowImpl) GetLastInitiatedCampaign(ctx context.Context, customerID uint, metadata *ClientMetadata) (*dto.GetLastInitiatedCampaignResponse, error) {
	_, err := getCustomer(ctx, s.customerRepo, customerID)
	if err != nil {
		return nil, NewBusinessError("GET_LAST_INITIATED_CAMPAIGN_FAILED", "Failed to get last initiated campaign", err)
	}

	fetchLatest := func(status models.CampaignStatus) (*models.Campaign, error) {
		rows, err := s.campaignRepo.ByFilter(ctx, models.CampaignFilter{
			CustomerID: &customerID,
			Status:     &status,
		}, "created_at DESC", 1, 0)
		if err != nil {
			return nil, err
		}

		if len(rows) == 0 {
			return nil, nil
		}

		return rows[0], nil
	}

	initiatedCampaign, err := fetchLatest(models.CampaignStatusInitiated)
	if err != nil {
		return nil, NewBusinessError("GET_LAST_INITIATED_CAMPAIGN_FAILED", "Failed to get last initiated campaign", err)
	}

	inProgressCampaign, err := fetchLatest(models.CampaignStatusInProgress)
	if err != nil {
		return nil, NewBusinessError("GET_LAST_INITIATED_CAMPAIGN_FAILED", "Failed to get last initiated campaign", err)
	}

	var c *models.Campaign
	switch {
	case initiatedCampaign == nil && inProgressCampaign == nil:
		return &dto.GetLastInitiatedCampaignResponse{
			Message: "No initiated or in-progress campaign found",
			Item:    nil,
		}, nil
	case initiatedCampaign == nil:
		c = inProgressCampaign
	case inProgressCampaign == nil:
		c = initiatedCampaign
	default:
		if inProgressCampaign.CreatedAt.After(initiatedCampaign.CreatedAt) {
			c = inProgressCampaign
		} else {
			c = initiatedCampaign
		}
	}
	enrichments, err := s.fetchCampaignDisplayEnrichments(ctx, c)
	if err != nil {
		return nil, NewBusinessError("GET_LAST_INITIATED_CAMPAIGN_FAILED", "Failed to get last initiated campaign", err)
	}

	item := buildCampaignResponse(c, enrichments)

	return &dto.GetLastInitiatedCampaignResponse{
		Message: "Last initiated campaign retrieved successfully",
		Item:    &item,
	}, nil
}

// validateCreateCampaignRequest validates the campaign creation request
func (s *CampaignFlowImpl) validateCreateCampaignRequest(ctx context.Context, req *dto.CreateCampaignRequest) error {
	targetingMethod, err := resolveAudienceTargetingMethodWithSelectedTags(req.AudienceTargetingMethod, req.SelectedTagIDs, req.TargetAudienceExcelFileUUID)
	if err != nil {
		return err
	}
	req.AudienceTargetingMethod = &targetingMethod
	usingSmartTargeting := targetingMethod == models.CampaignAudienceTargetingSmart
	usingExcelTargeting := targetingMethod == models.CampaignAudienceTargetingExcel

	if req.CustomerID == 0 {
		return ErrCustomerNotFound
	}
	if req.BundleID == nil || *req.BundleID == 0 {
		return ErrBundleNotFound
	}
	if err := validateCampaignPhaseInput(req.Phase, true); err != nil {
		return err
	}
	if req.Title == nil || (req.Title != nil && *req.Title == "") {
		return ErrCampaignTitleRequired
	}
	if req.Content != nil && *req.Content == "" {
		return ErrCampaignContentRequired
	}
	if usingSmartTargeting {
		normalized, err := normalizeSelectedTagIDs(req.SelectedTagIDs)
		if err != nil {
			return err
		}
		req.SelectedTagIDs = normalized
		if campaignPhaseOrDefault(req.Phase) == models.CampaignPhaseTest {
			if req.SampleSizePerTag == nil {
				return ErrSmartTargetingSampleSizeRequired
			}
			if *req.SampleSizePerTag == 0 || *req.SampleSizePerTag > math.MaxInt64 {
				return ErrSmartTargetingSampleSizeInvalid
			}
		}
	}
	if usingExcelTargeting {
		if req.TargetAudienceExcelFileUUID == nil || strings.TrimSpace(*req.TargetAudienceExcelFileUUID) == "" {
			return ErrCampaignTargetAudienceExcelFileInvalid
		}
		media, err := s.multimediaRepo.ByUUID(ctx, strings.TrimSpace(*req.TargetAudienceExcelFileUUID))
		if err != nil {
			return err
		}
		if media == nil || media.CustomerID != req.CustomerID {
			return ErrCampaignTargetAudienceExcelMediaNotFound
		}
	} else if !usingSmartTargeting {
		if req.Level1 == nil || (req.Level1 != nil && *req.Level1 == "") {
			return ErrCampaignLevel1Required
		}
		if req.Level2s == nil || (req.Level2s != nil && len(req.Level2s) == 0) {
			return ErrCampaignLevel2sRequired
		}
		if req.Level3s == nil || (req.Level3s != nil && len(req.Level3s) == 0) {
			return ErrCampaignLevel3sRequired
		}
		if req.Tags == nil || (req.Tags != nil && len(req.Tags) == 0) {
			return ErrCampaignTagsRequired
		}
	}
	if req.LineNumber != nil && *req.LineNumber == "" {
		return ErrCampaignLineNumberRequired
	}
	if req.Budget != nil {
		if *req.Budget <= 0 {
			return ErrCampaignBudgetRequired
		}
		if *req.Budget < minCampaignBudget || *req.Budget > maxCampaignBudget {
			return ErrCampaignBudgetOutOfRange
		}
	}
	if req.Sex != nil && *req.Sex == "" {
		return ErrCampaignSexRequired
	}
	if req.City != nil && len(req.City) == 0 {
		return ErrCampaignCityRequired
	}
	if req.AdLink != nil && *req.AdLink == "" {
		return ErrCampaignAdLinkRequired
	}
	if req.Category != nil && strings.TrimSpace(*req.Category) == "" {
		return ErrAgencyCategoryJobRequired
	}
	if req.Job != nil && strings.TrimSpace(*req.Job) == "" {
		return ErrAgencyCategoryJobRequired
	}
	if req.Platform != nil && strings.TrimSpace(*req.Platform) == "" {
		return ErrCampaignPlatformRequired
	}

	// Validate schedule time must be at least 10 minutes in the future
	scheduleTime := req.ScheduleAt
	if scheduleTime != nil && !scheduleTime.IsZero() {
		if scheduleTime.Before(utils.UTCNow().Add(10 * time.Minute)) {
			return ErrScheduleTimeTooSoon
		}
		if !isScheduleWithinTehranWindow(*scheduleTime) {
			return ErrScheduleTimeOutsideWindow
		}
	}

	if len(req.City) > 0 {
		if slices.Contains(req.City, "") {
			return ErrCampaignCityRequired
		}
	}

	if !usingSmartTargeting && !usingExcelTargeting && len(req.Level2s) > 0 {
		if slices.Contains(req.Level2s, "") {
			return ErrCampaignLevel2sRequired
		}
	}

	if !usingSmartTargeting && !usingExcelTargeting && len(req.Level3s) > 0 {
		if slices.Contains(req.Level3s, "") {
			return ErrCampaignLevel3sRequired
		}
	}

	if !usingSmartTargeting && !usingExcelTargeting && len(req.Tags) > 0 {
		if slices.Contains(req.Tags, "") {
			return ErrCampaignTagsRequired
		}
	}
	if normalizedGrades, err := sanitizeAudienceGrades(req.AudienceGrades); err != nil {
		return err
	} else {
		req.AudienceGrades = normalizedGrades
	}

	if _, err := sanitizeShortLinkDomain(req.ShortLinkDomain); err != nil {
		return err
	}

	_, err = sanitizeCampaignPlatform(req.Platform)
	if err != nil {
		return err
	}

	return nil
}

func isScheduleWithinTehranWindow(t time.Time) bool {
	loc := getTehranLocation()
	tt := t.In(time.UTC).In(loc)
	minutes := tt.Hour()*60 + tt.Minute()
	return minutes >= 8*60 && minutes <= 21*60
}

func getTehranLocation() *time.Location {
	if tehranLoc == nil || tehranLoc.String() != "Asia/Tehran" {
		if loaded, err := time.LoadLocation("Asia/Tehran"); err == nil {
			tehranLoc = loaded
		} else {
			tehranLoc = time.FixedZone("Asia/Tehran", 3*3600+1800)
		}
	}
	return tehranLoc
}

// createCampaign creates the campaign in the database
func (s *CampaignFlowImpl) createCampaign(ctx context.Context, req *dto.CreateCampaignRequest, customer *models.Customer) (*models.Campaign, error) {
	shortLinkDomain, err := sanitizeShortLinkDomain(req.ShortLinkDomain)
	if err != nil {
		return nil, err
	}
	platform, err := sanitizeCampaignPlatform(req.Platform)
	if err != nil {
		return nil, err
	}

	// Build campaign spec
	spec := models.CampaignSpec{}
	targetingMethod, err := resolveAudienceTargetingMethodWithSelectedTags(req.AudienceTargetingMethod, req.SelectedTagIDs, req.TargetAudienceExcelFileUUID)
	if err != nil {
		return nil, err
	}
	spec.AudienceTargetingMethod = &targetingMethod

	if req.Title != nil && *req.Title != "" {
		spec.Title = req.Title
	}
	// Preserve values for all targeting methods. Only the resolved method is
	// used for validation and audience calculation, but retaining the rest lets
	// users switch methods without losing work.
	if req.Level1 != nil && *req.Level1 != "" {
		spec.Level1 = req.Level1
	}
	if len(req.Level2s) > 0 {
		spec.Level2s = req.Level2s
	}
	if len(req.Level3s) > 0 {
		spec.Level3s = req.Level3s
	}
	if len(req.Tags) > 0 {
		spec.Tags = req.Tags
	}
	if req.TargetAudienceExcelFileUUID != nil && strings.TrimSpace(*req.TargetAudienceExcelFileUUID) != "" {
		excelFileUUID := strings.TrimSpace(*req.TargetAudienceExcelFileUUID)
		spec.TargetAudienceExcelFileUUID = &excelFileUUID
	}
	if req.Sex != nil && *req.Sex != "" {
		spec.Sex = req.Sex
	}
	if len(req.City) > 0 {
		spec.City = req.City
	}
	if req.AdLink != nil && *req.AdLink != "" {
		spec.AdLink = req.AdLink
	}
	if req.Content != nil && *req.Content != "" {
		spec.Content = req.Content
	}
	spec.ShortLinkDomain = shortLinkDomain
	if req.Category != nil && *req.Category != "" {
		spec.Category = req.Category
	}
	if req.Job != nil && *req.Job != "" {
		spec.Job = req.Job
	}
	if req.ScheduleAt != nil {
		spec.ScheduleAt = req.ScheduleAt
	}
	if req.LineNumber != nil && *req.LineNumber != "" {
		spec.LineNumber = req.LineNumber
	}
	if req.MediaUUID != nil {
		spec.MediaUUID = req.MediaUUID
	}
	if req.PlatformSettingsID != nil && *req.PlatformSettingsID != 0 {
		spec.PlatformSettingsID = req.PlatformSettingsID
	}
	spec.Platform = platform
	if req.Budget != nil && *req.Budget != 0 {
		spec.Budget = req.Budget
	}
	spec.AudienceGrades = campaignAudienceGradesOrDefault(req.AudienceGrades)

	uid := uuid.New()

	// Save to database
	err = s.campaignRepo.Save(ctx, &models.Campaign{
		UUID:             uid,
		CustomerID:       customer.ID,
		Status:           models.CampaignStatusInitiated,
		Spec:             spec,
		BundleID:         req.BundleID,
		Phase:            campaignPhaseOrDefault(req.Phase),
		SampleSizePerTag: req.SampleSizePerTag,
	})
	if err != nil {
		return nil, err
	}

	// Get the created campaign with ID
	c, err := s.campaignRepo.ByUUID(ctx, uid.String())
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (s *CampaignFlowImpl) ensureCampaignBundleAndPhase(ctx context.Context, customerID uint, bundleID *uint, phase *string, required bool) error {
	if required && (bundleID == nil || *bundleID == 0) {
		return ErrBundleNotFound
	}
	if err := validateCampaignPhaseInput(phase, required); err != nil {
		return err
	}
	if bundleID == nil || *bundleID == 0 {
		return nil
	}

	bundle, err := s.bundleRepo.ByID(ctx, *bundleID)
	if err != nil {
		return err
	}
	if bundle == nil {
		return ErrBundleNotFound
	}
	if bundle.CustomerID != customerID {
		return ErrBundleAccessDenied
	}

	return nil
}

func (s *CampaignFlowImpl) ensureCreateCampaignRefs(
	ctx context.Context,
	customerID uint,
	bundleID *uint,
	phase *string,
	lineNumber *string,
	level3s []string,
	platform string,
	mediaUUID *uuid.UUID,
	targetAudienceExcelFileUUID *string,
	platformSettingsID *uint,
) error {
	if err := s.ensureCampaignBundleAndPhase(ctx, customerID, bundleID, phase, true); err != nil {
		return err
	}

	if platform != models.CampaignPlatformSMS && lineNumber != nil {
		return ErrCampaignLineNumberNotApplicable
	}

	if platform == models.CampaignPlatformSMS && platformSettingsID != nil && *platformSettingsID != 0 {
		return ErrCampaignPlatformSettingNotApplicable
	}

	// if platform == models.CampaignPlatformSMS && lineNumber == nil {
	// 	return ErrCampaignLineNumberRequired
	// }

	if lineNumber != nil {
		_, err := s.fetchLineNumberPriceFactor(ctx, lineNumber)
		if err != nil {
			return err
		}
	}

	usingTargetAudienceFromExcelFile := targetAudienceExcelFileUUID != nil && strings.TrimSpace(*targetAudienceExcelFileUUID) != ""
	if len(level3s) > 0 && !usingTargetAudienceFromExcelFile {
		_, err := s.fetchSegmentPriceFactor(ctx, level3s, platform)
		if err != nil {
			return err
		}
	}

	if mediaUUID != nil {
		mediaRows, err := s.multimediaRepo.ByFilter(ctx, models.MultimediaAssetFilter{
			UUID:       mediaUUID,
			CustomerID: &customerID,
		}, "", 1, 0)
		if err != nil {
			return err
		}
		if len(mediaRows) == 0 {
			return ErrCampaignMediaNotFound
		}
	}

	if usingTargetAudienceFromExcelFile {
		count, err := s.CountTargetAudienceFromExcelFile(ctx, customerID, strings.TrimSpace(*targetAudienceExcelFileUUID))
		if err != nil {
			switch {
			case errors.Is(err, os.ErrNotExist):
				return ErrCampaignTargetAudienceExcelMediaNotFound
			case errors.Is(err, ErrCampaignTargetAudienceExcelFileInvalid):
				return ErrCampaignTargetAudienceExcelFileInvalid
			default:
				return err
			}
		}
		if count == 0 {
			return ErrCampaignTargetAudienceExcelFileInvalid
		}
	}

	// if platform != models.CampaignPlatformSMS && (platformSettingsID == nil || *platformSettingsID == 0) {
	// 	return ErrCampaignPlatformSettingRequired
	// }

	if platformSettingsID != nil && *platformSettingsID != 0 {
		platformFilter := platform
		settingsRows, err := s.platformSettingsRepo.ByFilter(ctx, models.PlatformSettingsFilter{
			ID:         platformSettingsID,
			CustomerID: &customerID,
			Platform:   &platformFilter,
			Status:     utils.ToPtr(models.PlatformSettingsStatusActive),
		}, "", 1, 0)
		if err != nil {
			return err
		}
		if len(settingsRows) == 0 {
			return ErrCampaignPlatformSettingNotFound
		}
	}

	return nil
}

func (s *CampaignFlowImpl) ensureUpdateCampaignRefs(
	ctx context.Context,
	customerID uint,
	bundleID *uint,
	phase *string,
	lineNumber *string,
	level3s []string,
	platform string,
	mediaUUID *uuid.UUID,
	targetAudienceExcelFileUUID *string,
	platformSettingsID *uint,
	finalize bool,
) error {
	if err := s.ensureCampaignBundleAndPhase(ctx, customerID, bundleID, phase, false); err != nil {
		return err
	}
	if platform != models.CampaignPlatformSMS && lineNumber != nil {
		return ErrCampaignLineNumberNotApplicable
	}
	if platform == models.CampaignPlatformSMS && platformSettingsID != nil && *platformSettingsID != 0 {
		return ErrCampaignPlatformSettingNotApplicable
	}

	usingTargetAudienceExcelFile := targetAudienceExcelFileUUID != nil && strings.TrimSpace(*targetAudienceExcelFileUUID) != ""

	if finalize {
		if platform == models.CampaignPlatformSMS && lineNumber == nil {
			return ErrCampaignLineNumberRequired
		}

		if platform != models.CampaignPlatformSMS && (platformSettingsID == nil || *platformSettingsID == 0) {
			return ErrCampaignPlatformSettingRequired
		}

		if platform != models.CampaignPlatformSMS && platformSettingsID != nil && *platformSettingsID != 0 {
			settingsRows, err := s.platformSettingsRepo.ByFilter(ctx, models.PlatformSettingsFilter{
				ID:         platformSettingsID,
				CustomerID: &customerID,
				Platform:   &platform,
				Status:     utils.ToPtr(models.PlatformSettingsStatusActive),
			}, "", 1, 0)
			if err != nil {
				return err
			}
			if len(settingsRows) == 0 {
				return ErrCampaignPlatformSettingNotFound
			}
		}
	}

	if lineNumber != nil {
		_, err := s.fetchLineNumberPriceFactor(ctx, lineNumber)
		if err != nil {
			return err
		}
	}

	if len(level3s) > 0 && !usingTargetAudienceExcelFile {
		_, err := s.fetchSegmentPriceFactor(ctx, level3s, platform)
		if err != nil {
			return err
		}
	}

	if mediaUUID != nil {
		mediaRows, err := s.multimediaRepo.ByFilter(ctx, models.MultimediaAssetFilter{
			UUID:       mediaUUID,
			CustomerID: &customerID,
		}, "", 1, 0)
		if err != nil {
			return err
		}
		if len(mediaRows) == 0 {
			return ErrCampaignMediaNotFound
		}
	}

	if usingTargetAudienceExcelFile {
		count, err := s.CountTargetAudienceFromExcelFile(ctx, customerID, strings.TrimSpace(*targetAudienceExcelFileUUID))
		if err != nil {
			switch {
			case errors.Is(err, os.ErrNotExist):
				return ErrCampaignTargetAudienceExcelMediaNotFound
			case errors.Is(err, ErrCampaignTargetAudienceExcelFileInvalid):
				return ErrCampaignTargetAudienceExcelFileInvalid
			default:
				return err
			}
		}
		if count == 0 {
			return ErrCampaignTargetAudienceExcelFileInvalid
		}
	}

	return nil
}

// validateUpdateCampaignRequest validates the campaign update request
func (s *CampaignFlowImpl) validateUpdateCampaignRequest(req *dto.UpdateCampaignRequest) error {
	if req.UUID == "" {
		return ErrCampaignUUIDRequired
	}

	if req.CustomerID == 0 {
		return ErrCustomerNotFound
	}

	// At least one field should be provided for update
	hasUpdateFields := req.Title != nil || req.Level1 != nil || len(req.Level2s) > 0 || len(req.Level3s) > 0 ||
		req.BundleID != nil || req.Phase != nil ||
		req.AudienceTargetingMethod != nil || req.SelectedTagIDs != nil ||
		req.TargetAudienceExcelFileUUID != nil || len(req.Tags) > 0 || req.AudienceGrades != nil || req.Sex != nil || len(req.City) > 0 ||
		req.AdLink != nil || req.Content != nil ||
		req.ScheduleAt != nil || req.LineNumber != nil || req.Budget != nil || req.ShortLinkDomain != nil ||
		req.Category != nil || req.Job != nil ||
		req.MediaUUID != nil || req.PlatformSettingsID != nil || req.Platform != nil || req.SampleSizePerTag != nil

	if !hasUpdateFields {
		return ErrCampaignUpdateRequired
	}

	if req.BundleID != nil && *req.BundleID == 0 {
		return ErrBundleNotFound
	}
	if normalizedGrades, err := sanitizeAudienceGrades(req.AudienceGrades); err != nil {
		return err
	} else {
		req.AudienceGrades = normalizedGrades
	}
	if err := validateCampaignPhaseInput(req.Phase, false); err != nil {
		return err
	}

	// if req.ScheduleAt != nil && !req.ScheduleAt.IsZero() {
	// 	if !isScheduleWithinTehranWindow(*req.ScheduleAt) {
	// 		return ErrScheduleTimeOutsideWindow
	// 	}
	// }

	return nil
}

func validateUpdateCampaignBudget(budget *uint64, targetingMethod string, phase models.CampaignPhase) error {
	if targetingMethod == models.CampaignAudienceTargetingSmart && phase == models.CampaignPhaseTest {
		return nil
	}
	if budget == nil {
		return nil
	}
	if *budget == 0 {
		return ErrCampaignBudgetRequired
	}
	if *budget < minCampaignBudget || *budget > maxCampaignBudget {
		return ErrCampaignBudgetOutOfRange
	}
	return nil
}

// validateCalculateCampaignCapacityRequest validates the request
func (s *CampaignFlowImpl) validateCalculateCampaignCapacityRequest(req *dto.CalculateCampaignCapacityRequest) error {
	if req.CampaignID == 0 {
		return ErrCampaignNotFound
	}
	if req.CustomerID == 0 {
		return ErrCustomerNotFound
	}

	return nil
}

func (s *CampaignFlowImpl) canFinalizeCampaign(ctx context.Context, campaign *models.Campaign, customer models.Customer) error {
	if campaign == nil {
		return ErrCampaignNotFound
	}
	usingTargetAudienceExcelFile := campaign.Spec.UsesExcelTargeting()

	if campaign.Spec.Title == nil || *campaign.Spec.Title == "" {
		return ErrCampaignTitleRequired
	}
	if campaign.Spec.UsesSmartTargeting() {
		if campaign.Phase == models.CampaignPhaseTest {
			if campaign.SampleSizePerTag == nil {
				return ErrSmartTargetingSampleSizeRequired
			}
			if *campaign.SampleSizePerTag == 0 || *campaign.SampleSizePerTag > math.MaxInt64 {
				return ErrSmartTargetingSampleSizeInvalid
			}
		}
		if campaign.Phase == models.CampaignPhaseTest {
			if err := s.requireCurrentSmartTargetingTestSampling(ctx, campaign); err != nil {
				return err
			}
			if _, err := currentSmartTargetingTestSamplingIntent(ctx, s.selectedTagRepo, campaign, true); err != nil {
				return err
			}
		}
		summary, err := s.selectedTagRepo.Summary(ctx, campaign.ID)
		if err != nil {
			return err
		}
		if summary.SelectedTagCount == 0 {
			return ErrSmartTargetingTagsRequired
		}
		if campaign.BundleID == nil {
			return ErrBundleNotFound
		}
		if err := s.selectedTagRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
			if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
				return ErrSmartTargetingTagInvalid
			}
			return err
		}
	} else if usingTargetAudienceExcelFile {
		if campaign.Spec.TargetAudienceExcelFileUUID == nil ||
			strings.TrimSpace(*campaign.Spec.TargetAudienceExcelFileUUID) == "" {
			return ErrCampaignTargetAudienceExcelFileInvalid
		}
	} else {
		if campaign.Spec.Level1 == nil || *campaign.Spec.Level1 == "" {
			return ErrCampaignLevel1Required
		}
		if campaign.Spec.Level2s == nil {
			return ErrCampaignLevel2sRequired
		}
		if len(campaign.Spec.Level2s) == 0 {
			return ErrCampaignLevel2sRequired
		}
		if campaign.Spec.Level3s == nil {
			return ErrCampaignLevel3sRequired
		}
		if len(campaign.Spec.Level3s) == 0 {
			return ErrCampaignLevel3sRequired
		}
		if campaign.Spec.Tags == nil {
			return ErrCampaignTagsRequired
		}
		if len(campaign.Spec.Tags) == 0 {
			return ErrCampaignTagsRequired
		}
	}
	if campaign.Spec.Content == nil || *campaign.Spec.Content == "" {
		return ErrCampaignContentRequired
	}
	if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() {
		campaign.Spec.ScheduleAt = utils.ToPtr(utils.UTCNow().Add(20 * time.Minute))
		// return ErrScheduleTimeNotPresent
	}
	if campaign.Spec.ScheduleAt.Before(utils.UTCNow().Add(10 * time.Minute)) {
		return ErrScheduleTimeTooSoon
	}
	if !isScheduleWithinTehranWindow(*campaign.Spec.ScheduleAt) {
		return ErrScheduleTimeOutsideWindow
	}
	if campaign.Spec.Platform == models.CampaignPlatformSMS && (campaign.Spec.LineNumber == nil || *campaign.Spec.LineNumber == "") {
		return ErrCampaignLineNumberRequired
	}
	if err := validateCampaignFinalizationBudget(campaign); err != nil {
		return err
	}
	if _, err := sanitizeShortLinkDomain(campaign.Spec.ShortLinkDomain); err != nil {
		return err
	}
	if _, _, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, campaign.Spec.Category, campaign.Spec.Job, true); err != nil {
		return err
	}
	if _, err := sanitizeCampaignPlatform(utils.ToPtr(campaign.Spec.Platform)); err != nil {
		return err
	}
	targetingMethod := campaignAudienceTargetingMethod(campaign.Spec)
	level3sForValidation := campaign.Spec.Level3s
	if targetingMethod != models.CampaignAudienceTargetingStandard {
		level3sForValidation = nil
	}
	excelFileForValidation := campaign.Spec.TargetAudienceExcelFileUUID
	if targetingMethod != models.CampaignAudienceTargetingExcel {
		excelFileForValidation = nil
	}
	if err := s.ensureUpdateCampaignRefs(
		ctx,
		campaign.CustomerID,
		campaign.BundleID,
		campaignPhasePtr(campaign.Phase),
		campaign.Spec.LineNumber,
		level3sForValidation,
		campaign.Spec.Platform,
		campaign.Spec.MediaUUID,
		excelFileForValidation,
		campaign.Spec.PlatformSettingsID,
		true,
	); err != nil {
		return err
	}

	return nil
}

func validateCampaignFinalizationBudget(campaign *models.Campaign) error {
	if campaign == nil {
		return ErrCampaignNotFound
	}
	if campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest {
		return nil
	}
	if campaign.Spec.Budget == nil || *campaign.Spec.Budget <= 0 {
		return ErrCampaignBudgetRequired
	}
	return nil
}

// applyCampaignSpecUpdate applies the campaign update semantics. Most fields
// are sparse patches, while ad link, schedule, and short-link domain are
// replace fields: absent, null, or empty removes their stored value.
func applyCampaignSpecUpdate(spec *models.CampaignSpec, req *dto.UpdateCampaignRequest) error {
	if spec == nil || req == nil {
		return nil
	}
	ensureCampaignSpecDefaults(spec)
	previousPlatform := spec.Platform
	if req.AudienceTargetingMethod != nil {
		method, err := sanitizeAudienceTargetingMethod(req.AudienceTargetingMethod)
		if err != nil {
			return err
		}
		spec.AudienceTargetingMethod = &method
	}

	if req.Title != nil && *req.Title != "" {
		spec.Title = req.Title
	}
	// Targeting inputs are independently editable. Keep inactive-method data
	// so a later method switch can reuse it; the resolved method controls which
	// values are used by validation, capacity, pricing, and finalization.
	if req.Level1 != nil && *req.Level1 != "" {
		spec.Level1 = req.Level1
	}
	if len(req.Level2s) > 0 {
		spec.Level2s = req.Level2s
	}
	if len(req.Level3s) > 0 {
		spec.Level3s = req.Level3s
	}
	if len(req.Tags) > 0 {
		spec.Tags = req.Tags
	}
	if req.TargetAudienceExcelFileUUID != nil {
		excelFileUUID := strings.TrimSpace(*req.TargetAudienceExcelFileUUID)
		if excelFileUUID != "" {
			spec.TargetAudienceExcelFileUUID = &excelFileUUID
		} else {
			spec.TargetAudienceExcelFileUUID = nil
		}
	}
	if req.Sex != nil && *req.Sex != "" {
		spec.Sex = req.Sex
	}
	if len(req.City) > 0 {
		spec.City = req.City
	}
	if req.AdLink != nil && strings.TrimSpace(*req.AdLink) != "" {
		spec.AdLink = req.AdLink
	} else {
		spec.AdLink = nil
	}
	if req.Content != nil && *req.Content != "" {
		spec.Content = req.Content
	}
	if req.Category != nil && *req.Category != "" {
		spec.Category = req.Category
	}
	if req.Job != nil && *req.Job != "" {
		spec.Job = req.Job
	}
	if req.ShortLinkDomain == nil || strings.TrimSpace(*req.ShortLinkDomain) == "" {
		spec.ShortLinkDomain = nil
	} else {
		spec.ShortLinkDomain = req.ShortLinkDomain
	}
	if req.ScheduleAt != nil {
		spec.ScheduleAt = req.ScheduleAt
	} else {
		spec.ScheduleAt = nil
	}
	if req.LineNumber != nil && *req.LineNumber != "" {
		spec.LineNumber = req.LineNumber
	} else if req.LineNumber != nil {
		spec.LineNumber = nil
	}
	if req.MediaUUID != nil {
		spec.MediaUUID = req.MediaUUID
	}
	if req.PlatformSettingsID != nil && *req.PlatformSettingsID != 0 {
		spec.PlatformSettingsID = req.PlatformSettingsID
	} else if req.PlatformSettingsID != nil {
		spec.PlatformSettingsID = nil
	}
	if req.Platform != nil {
		platform, err := sanitizeCampaignPlatform(req.Platform)
		if err != nil {
			return err
		}
		spec.Platform = platform
	}
	if req.Budget != nil && *req.Budget != 0 {
		spec.Budget = req.Budget
	}
	if req.AudienceGrades != nil {
		spec.AudienceGrades = campaignAudienceGradesOrDefault(req.AudienceGrades)
	}
	// Excel input is deliberately not retained when Smart Targeting is active:
	// it is not used by the mode and can make later UI/API reads appear to be
	// Excel-targeted data that is still relevant.
	if spec.UsesSmartTargeting() {
		spec.TargetAudienceExcelFileUUID = nil
	}
	// A line number belongs exclusively to SMS. Platform settings are bound to
	// one non-SMS platform, so a platform transition must not carry either
	// configuration into an incompatible campaign.
	if spec.Platform == models.CampaignPlatformSMS {
		spec.PlatformSettingsID = nil
	} else {
		spec.LineNumber = nil
		if previousPlatform != spec.Platform && req.PlatformSettingsID == nil {
			spec.PlatformSettingsID = nil
		}
	}
	ensureCampaignSpecDefaults(spec)
	return nil
}

// updateCampaign updates the campaign in the database.
func (s *CampaignFlowImpl) updateCampaign(ctx context.Context, req *dto.UpdateCampaignRequest, existingCampaign *models.Campaign) error {
	// Update campaign spec with new values.
	spec := existingCampaign.Spec
	if err := applyCampaignSpecUpdate(&spec, req); err != nil {
		return err
	}

	// Update the campaign spec
	existingCampaign.Spec = spec
	if req.BundleID != nil {
		existingCampaign.BundleID = req.BundleID
		existingCampaign.Bundle = nil // Clear cached bundle to force reload with new ID
	}
	if req.Phase != nil {
		existingCampaign.Phase = campaignPhaseOrDefault(req.Phase)
	}
	if req.SampleSizePerTag != nil {
		if *req.SampleSizePerTag == 0 || *req.SampleSizePerTag > math.MaxInt64 {
			return ErrSmartTargetingSampleSizeInvalid
		}
		existingCampaign.SampleSizePerTag = req.SampleSizePerTag
	}
	existingCampaign.Status = models.CampaignStatusInProgress
	existingCampaign.UpdatedAt = utils.ToPtr(utils.UTCNow())

	// Save to database
	err := s.campaignRepo.Update(ctx, *existingCampaign)
	if err != nil {
		return err
	}

	return nil
}

func (s *CampaignFlowImpl) expireCustomerCampaigns(ctx context.Context, customerID uint) error {
	// NOTE: Idempotency
	cutoff := utils.UTCNow().Add(-6 * time.Hour)

	st := models.CampaignStatusWaitingForApproval
	rows, err := s.campaignRepo.ByFilter(ctx, models.CampaignFilter{
		CustomerID: &customerID,
		Status:     &st,
	}, "", 0, 0)
	if err != nil {
		return err
	}

	for _, c := range rows {
		if c.Spec.ScheduleAt == nil || c.Spec.ScheduleAt.IsZero() {
			continue
		}
		if !c.Spec.ScheduleAt.Before(cutoff) {
			continue
		}

		if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
			if err := repository.LockCampaignForUpdate(txCtx, c.ID); err != nil {
				return err
			}
			campaign, err := s.campaignRepo.ByID(txCtx, c.ID)
			if err != nil {
				return err
			}
			if campaign == nil {
				return ErrCampaignNotFound
			}
			if campaign.CustomerID != customerID {
				return ErrCampaignAccessDenied
			}
			if campaign.Status != models.CampaignStatusWaitingForApproval {
				return nil
			}
			if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() || !campaign.Spec.ScheduleAt.Before(cutoff) {
				return nil
			}

			customer, err := getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
			if err != nil {
				return err
			}

			wallet, err := getWallet(txCtx, s.walletRepo, campaign.CustomerID)
			if err != nil {
				return err
			}
			if err := repository.LockWalletForUpdate(txCtx, wallet.ID); err != nil {
				return err
			}
			latestBalance, err := getLatestBalanceSnapshot(txCtx, s.walletRepo, wallet.ID)
			if err != nil {
				return err
			}

			freezeTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
				CustomerID: &campaign.CustomerID,
				CampaignID: &campaign.ID,
				Source:     utils.ToPtr("campaign_update"),
				Operation:  utils.ToPtr("reserve_budget"),
				Type:       utils.ToPtr(models.TransactionTypeFreeze),
				Status:     utils.ToPtr(models.TransactionStatusCompleted),
			}, "id DESC", 0, 0)
			if err != nil {
				return err
			}
			if len(freezeTxs) == 0 {
				return ErrFreezeTransactionNotFound
			}
			if len(freezeTxs) > 1 {
				return ErrMultipleFreezeTransactionsFound
			}
			freezeTx := freezeTxs[0]

			amount := freezeTx.Amount
			if latestBalance.FrozenBalance < amount {
				return ErrInsufficientFunds
			}

			meta := map[string]any{
				"source":      "campaign_expire",
				"operation":   "expire_campaign_refund_frozen",
				"campaign_id": campaign.ID,
				"comment":     "campaign_auto_expired_due_to_schedule_time",
			}
			metaBytes, _ := json.Marshal(meta)

			// TODO:
			// newFrozen := latestBalance.FrozenBalance - amount
			// // Restore free/credit split exactly as it was taken during the freeze.
			// freeRefund, creditRefund := computeFreezeRefundSplit(freezeTx, amount)
			// newFree := latestBalance.FreeBalance + freeRefund
			// newCredit := latestBalance.CreditBalance + creditRefund

			// newSnap := &models.BalanceSnapshot{
			// 	UUID:               uuid.New(),
			// 	CorrelationID:      freezeTx.CorrelationID,
			// 	WalletID:           wallet.ID,
			// 	CustomerID:         customer.ID,
			// 	FreeBalance:        newFree,
			// 	FrozenBalance:      newFrozen,
			// 	LockedBalance:      latestBalance.LockedBalance,
			// 	CreditBalance:      newCredit,
			// 	SpentOnCampaign:    latestBalance.SpentOnCampaign,
			// 	AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			// 	TotalBalance:       newFree + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
			// 	Reason:             "campaign_expired_budget_refund",
			// 	Description:        fmt.Sprintf("Refund reserved budget for expired campaign %d", campaign.ID),
			// 	Metadata:           metaBytes,
			// }

			newFrozen := latestBalance.FrozenBalance - amount
			newCredit := latestBalance.CreditBalance + amount

			newSnap := &models.BalanceSnapshot{
				UUID:               uuid.New(),
				CorrelationID:      freezeTx.CorrelationID,
				WalletID:           wallet.ID,
				CustomerID:         customer.ID,
				FreeBalance:        latestBalance.FreeBalance,
				FrozenBalance:      newFrozen,
				LockedBalance:      latestBalance.LockedBalance,
				CreditBalance:      newCredit,
				SpentOnCampaign:    latestBalance.SpentOnCampaign,
				AgencyShareWithTax: latestBalance.AgencyShareWithTax,
				TotalBalance:       latestBalance.FreeBalance + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
				Reason:             "campaign_expired_budget_refund",
				Description:        fmt.Sprintf("Refund reserved budget for expired campaign %d", campaign.ID),
				Metadata:           metaBytes,
			}
			if err := s.balanceSnapshotRepo.Save(txCtx, newSnap); err != nil {
				return err
			}

			beforeMap, err := latestBalance.GetBalanceMap()
			if err != nil {
				return err
			}
			afterMap, err := newSnap.GetBalanceMap()
			if err != nil {
				return err
			}

			refundTx := &models.Transaction{
				UUID:          uuid.New(),
				CorrelationID: freezeTx.CorrelationID,
				Type:          models.TransactionTypeRefund,
				Status:        models.TransactionStatusCompleted,
				Amount:        amount,
				Currency:      utils.TomanCurrency,
				WalletID:      wallet.ID,
				CustomerID:    customer.ID,
				BalanceBefore: beforeMap,
				BalanceAfter:  afterMap,
				Description:   fmt.Sprintf("Refund reserved budget for expired campaign %d", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}

			if err := s.campaignRepo.UpdateStatus(txCtx, campaign.ID, models.CampaignStatusExpired); err != nil {
				return err
			}
			if err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ReleaseForCampaign(txCtx, campaign.ID); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

// reconcileUndeliveredCampaignRefunds runs a best-effort reconciliation pass to refund
// executed campaigns that under-delivered relative to their intended audience.
func (s *CampaignFlowImpl) reconcileUndeliveredCampaignRefunds(ctx context.Context, customerID uint) error {
	// NOTE: Idempotency

	if customerID == 0 {
		return nil
	}
	var auditCustomer *models.Customer
	if customer, err := getCustomer(ctx, s.customerRepo, customerID); err == nil {
		auditCustomer = &customer
	}

	status := models.CampaignStatusExecuted
	cutoff := utils.UTCNow().Add(-undeliveredRefundDelay)
	rows, err := s.campaignRepo.ByFilter(ctx, models.CampaignFilter{
		CustomerID:     &customerID,
		Status:         &status,
		ScheduleBefore: &cutoff,
	}, "id DESC", 0, 0)
	if err != nil {
		return err
	}

	for _, c := range rows {
		if c == nil {
			continue
		}
		if hasProcessedUndeliveredRefund(c.Statistics) {
			continue
		}
		if hasUndeliveredRefundError(c.Statistics) {
			continue
		}

		err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
			campaign, err := s.campaignRepo.ByID(txCtx, c.ID)
			if err != nil {
				return err
			}
			if campaign == nil {
				return nil
			}
			if campaign.Status != models.CampaignStatusExecuted {
				return nil
			}
			if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() {
				return nil
			}
			if campaign.Spec.ScheduleAt.After(utils.UTCNow().Add(-undeliveredRefundDelay)) {
				return nil
			}
			if campaign.NumAudience == nil || *campaign.NumAudience == 0 {
				log.Printf("reconcileUndeliveredCampaignRefunds: campaign %d has nil or zero num_audience, skipping refund", campaign.ID)
				return nil
			}

			// Serialize refund reconciliation per campaign to prevent duplicate refunds
			// under concurrent list/get requests.
			if tx, ok := txCtx.Value(repository.TxContextKey).(*gorm.DB); ok && tx != nil {
				if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(campaign.ID)).Error; err != nil {
					return err
				}
			}

			if hasProcessedUndeliveredRefund(campaign.Statistics) {
				return nil
			}
			if hasUndeliveredRefundError(campaign.Statistics) {
				return nil
			}

			existing, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
				CustomerID: &campaign.CustomerID,
				CampaignID: &campaign.ID,
				Source:     utils.ToPtr("campaign_partial_refund"),
				Operation:  utils.ToPtr("partial_undelivered_messages_refund"),
				Type:       utils.ToPtr(models.TransactionTypeRefund),
				Status:     utils.ToPtr(models.TransactionStatusCompleted),
			}, "id DESC", 1, 0)
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				return nil
			}

			aggregatedTotalSent, ok := parseAggregatedTotalSent(campaign.Statistics)
			if !ok {
				return nil
			}
			// Smart Targeting Test keeps the finalized preview count in
			// NumAudience. If scheduler-time best effort prepares fewer rows, the
			// existing sent-count delta refunds that shortfall without a special
			// Feature 4 refund path.
			if aggregatedTotalSent >= *campaign.NumAudience {
				return nil
			}

			missing := *campaign.NumAudience - aggregatedTotalSent
			if missing == 0 {
				return nil
			}

			var costPerMessage uint64
			if costPerMessage, ok = s.resolveCampaignCostPerMessageFromMetadata(txCtx, campaign); !ok {
				log.Printf("reconcileUndeliveredCampaignRefunds: cannot resolve cost per message for campaign %d, skipping refund reconciliation", campaign.ID)
				return nil
			}

			if costPerMessage == 0 {
				return nil
			}
			if missing > math.MaxUint64/costPerMessage {
				return fmt.Errorf("refund amount overflow for campaign=%d", campaign.ID)
			}
			refundAmount := missing * costPerMessage
			if refundAmount == 0 {
				return nil
			}

			debitTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
				CustomerID: &campaign.CustomerID,
				CampaignID: &campaign.ID,
				Source:     utils.ToPtr("admin_campaign_approve"),
				Operation:  utils.ToPtr("approve_campaign_budget_consume"),
				Type:       utils.ToPtr(models.TransactionTypeFee),
				Status:     utils.ToPtr(models.TransactionStatusCompleted),
			}, "id DESC", 1, 0)
			if err != nil {
				return err
			}
			if len(debitTxs) == 0 {
				return ErrCampaignDebitTransactionNotFound
			}
			debitTx := debitTxs[0]

			if debitTx.Amount < refundAmount {
				return fmt.Errorf("refund amount %d exceeds campaign debit amount %d for campaign=%d", refundAmount, debitTx.Amount, campaign.ID)
			}

			customer, err := getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
			if err != nil {
				return err
			}
			wallet, err := getWallet(txCtx, s.walletRepo, campaign.CustomerID)
			if err != nil {
				return err
			}
			if err := repository.LockWalletForUpdate(txCtx, wallet.ID); err != nil {
				return err
			}
			latestBalance, err := getLatestBalanceSnapshot(txCtx, s.walletRepo, wallet.ID)
			if err != nil {
				return err
			}
			if latestBalance.SpentOnCampaign < refundAmount {
				return ErrInsufficientFunds
			}

			// TODO:
			// // Recover the original FreeBalance/CreditBalance split by looking up the
			// // freeze transaction that shares the same CorrelationID as the debit tx.
			// debitCorrID := debitTx.CorrelationID
			// origFreezeTxs, err := s.transactionRepo.ByFilter(txCtx, models.TransactionFilter{
			// 	CorrelationID: &debitCorrID,
			// 	CustomerID:    &campaign.CustomerID,
			// 	Type:          utils.ToPtr(models.TransactionTypeFreeze),
			// 	Status:        utils.ToPtr(models.TransactionStatusCompleted),
			// }, "id ASC", 1, 0)
			// if err != nil {
			// 	return err
			// }
			// var freeRefund, creditRefund uint64
			// if len(origFreezeTxs) > 0 {
			// 	freeRefund, creditRefund = computeFreezeRefundSplit(origFreezeTxs[0], refundAmount)
			// } else {
			// 	// No freeze tx found; conservatively return all to free balance.
			// 	freeRefund = refundAmount
			// }

			meta := map[string]any{
				"source":                "campaign_partial_refund",
				"operation":             "partial_undelivered_messages_refund",
				"campaign_id":           campaign.ID,
				"scheduled_at":          campaign.Spec.ScheduleAt.UTC().Format(time.RFC3339),
				"num_audience":          *campaign.NumAudience,
				"aggregated_total_sent": aggregatedTotalSent,
				"missing_messages":      missing,
				"cost_per_message":      costPerMessage,
				"refund_amount":         refundAmount,
			}
			metaBytes, _ := json.Marshal(meta)

			// newFree := latestBalance.FreeBalance + freeRefund
			// newCredit := latestBalance.CreditBalance + creditRefund
			// newSpentOnCampaign := latestBalance.SpentOnCampaign - refundAmount

			// newSnap := &models.BalanceSnapshot{
			// 	UUID:               uuid.New(),
			// 	CorrelationID:      debitTx.CorrelationID,
			// 	WalletID:           wallet.ID,
			// 	CustomerID:         customer.ID,
			// 	FreeBalance:        newFree,
			// 	FrozenBalance:      latestBalance.FrozenBalance,
			// 	LockedBalance:      latestBalance.LockedBalance,
			// 	CreditBalance:      newCredit,
			// 	SpentOnCampaign:    newSpentOnCampaign,
			// 	AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			// 	TotalBalance:       newFree + latestBalance.FrozenBalance + latestBalance.LockedBalance + newCredit + newSpentOnCampaign + latestBalance.AgencyShareWithTax,
			// 	Reason:             "campaign_partial_refund_for_undelivered_messages",
			// 	Description:        fmt.Sprintf("Refund undelivered messages for campaign %d", campaign.ID),
			// 	Metadata:           metaBytes,
			// }
			newCredit := latestBalance.CreditBalance + refundAmount
			newSpentOnCampaign := latestBalance.SpentOnCampaign - refundAmount

			newSnap := &models.BalanceSnapshot{
				UUID:               uuid.New(),
				CorrelationID:      debitTx.CorrelationID,
				WalletID:           wallet.ID,
				CustomerID:         customer.ID,
				FreeBalance:        latestBalance.FreeBalance,
				FrozenBalance:      latestBalance.FrozenBalance,
				LockedBalance:      latestBalance.LockedBalance,
				CreditBalance:      newCredit,
				SpentOnCampaign:    newSpentOnCampaign,
				AgencyShareWithTax: latestBalance.AgencyShareWithTax,
				TotalBalance:       latestBalance.FreeBalance + latestBalance.FrozenBalance + latestBalance.LockedBalance + newCredit + newSpentOnCampaign + latestBalance.AgencyShareWithTax,
				Reason:             "campaign_partial_refund_for_undelivered_messages",
				Description:        fmt.Sprintf("Refund undelivered messages for campaign %d", campaign.ID),
				Metadata:           metaBytes,
			}
			if err := s.balanceSnapshotRepo.Save(txCtx, newSnap); err != nil {
				return err
			}

			beforeMap, err := latestBalance.GetBalanceMap()
			if err != nil {
				return err
			}
			afterMap, err := newSnap.GetBalanceMap()
			if err != nil {
				return err
			}

			refundTx := &models.Transaction{
				UUID:          uuid.New(),
				CorrelationID: debitTx.CorrelationID,
				Type:          models.TransactionTypeRefund,
				Status:        models.TransactionStatusCompleted,
				Amount:        refundAmount,
				Currency:      utils.TomanCurrency,
				WalletID:      wallet.ID,
				CustomerID:    customer.ID,
				BalanceBefore: beforeMap,
				BalanceAfter:  afterMap,
				Description:   fmt.Sprintf("Partial refund for undelivered messages in campaign %d", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}

			statsMap := map[string]any{}
			if len(campaign.Statistics) > 0 {
				_ = json.Unmarshal(campaign.Statistics, &statsMap)
			}
			statsMap["undeliveredRefundProcessed"] = true
			statsMap["undeliveredRefundProcessedAt"] = utils.UTCNow().Format(time.RFC3339)
			statsMap["undeliveredRefundAmount"] = refundAmount
			statsMap["undeliveredRefundMissingMessages"] = missing
			statsMap["undeliveredRefundCostPerMessage"] = costPerMessage
			statsMap["undeliveredRefundAggregatedTotalSent"] = aggregatedTotalSent
			statsBytes, _ := json.Marshal(statsMap)

			campaign.Statistics = statsBytes
			campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
			if err := s.campaignRepo.Update(txCtx, *campaign); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			errMsg := fmt.Sprintf("Undelivered refund reconciliation failed for campaign %d: %v", c.ID, err)
			if auditCustomer != nil {
				_ = s.createAuditLog(ctx, auditCustomer, models.AuditActionCampaignRefundReconcileFailed, errMsg, false, &errMsg, nil)
			}
			log.Printf("%s", errMsg)
			// Mark the campaign so it is never retried again. The refund transaction
			// was rolled back, so we persist the error flag in a separate (non-transactional) update.
			if markErr := s.markCampaignRefundError(ctx, c.ID, err); markErr != nil {
				log.Printf("reconcileUndeliveredCampaignRefunds: failed to mark refund error for campaign %d: %v", c.ID, markErr)
			}
			// Best-effort reconciliation: do not block listing/getting campaigns.
			continue
		}
	}

	return nil
}

// markCampaignRefundError writes an error flag into the campaign Statistics so
// reconcileUndeliveredCampaignRefunds skips it on future invocations.
// It runs outside any transaction because the failed refund transaction was already rolled back.
func (s *CampaignFlowImpl) markCampaignRefundError(ctx context.Context, campaignID uint, refundErr error) error {
	campaign, err := s.campaignRepo.ByID(ctx, campaignID)
	if err != nil {
		return err
	}
	if campaign == nil {
		return nil
	}

	statsMap := map[string]any{}
	if len(campaign.Statistics) > 0 {
		_ = json.Unmarshal(campaign.Statistics, &statsMap)
	}
	statsMap["undeliveredRefundError"] = true
	statsMap["undeliveredRefundErrorAt"] = utils.UTCNow().Format(time.RFC3339)
	statsMap["undeliveredRefundErrorMsg"] = refundErr.Error()
	statsBytes, _ := json.Marshal(statsMap)

	campaign.Statistics = statsBytes
	campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
	return s.campaignRepo.Update(ctx, *campaign)
}

func (s *CampaignFlowImpl) tryAcquireFlowLock(ctx context.Context, suffix string, ttl time.Duration) bool {
	if s.rc == nil {
		return true
	}
	lockKey := redisKey(s.cacheConfig, "flow_lock:"+suffix)
	ok, err := s.rc.SetNX(ctx, lockKey, "1", ttl).Result()
	if err != nil {
		// Best-effort idempotency lock: continue when redis is unavailable.
		return true
	}
	return ok
}

func parseAggregatedTotalSent(stats json.RawMessage) (uint64, bool) {
	if len(stats) == 0 {
		return 0, false
	}
	var statsMap map[string]any
	if err := json.Unmarshal(stats, &statsMap); err != nil {
		return 0, false
	}
	v, ok := statsMap["aggregatedTotalSent"]
	if !ok {
		return 0, false
	}

	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil || f < 0 {
			return 0, false
		}
		return uint64(f), true
	default:
		return 0, false
	}
}

func hasProcessedUndeliveredRefund(stats json.RawMessage) bool {
	if len(stats) == 0 {
		return false
	}
	var statsMap map[string]any
	if err := json.Unmarshal(stats, &statsMap); err != nil {
		return false
	}
	raw, ok := statsMap["undeliveredRefundProcessed"]
	if !ok {
		return false
	}
	b, ok := raw.(bool)
	return ok && b
}

func hasUndeliveredRefundError(stats json.RawMessage) bool {
	if len(stats) == 0 {
		return false
	}
	var statsMap map[string]any
	if err := json.Unmarshal(stats, &statsMap); err != nil {
		return false
	}
	raw, ok := statsMap["undeliveredRefundError"]
	if !ok {
		return false
	}
	b, ok := raw.(bool)
	return ok && b
}

func (s *CampaignFlowImpl) resolveCampaignCostPerMessageFromMetadata(ctx context.Context, campaign *models.Campaign) (uint64, bool) {
	if campaign == nil {
		return 0, false
	}

	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CustomerID: &campaign.CustomerID,
		CampaignID: &campaign.ID,
		Source:     utils.ToPtr("campaign_update"),
		Operation:  utils.ToPtr("reserve_budget"),
		Type:       utils.ToPtr(models.TransactionTypeFreeze),
		Status:     utils.ToPtr(models.TransactionStatusCompleted),
	}, "id DESC", 1, 0)
	if err != nil || len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return 0, false
	}

	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return 0, false
	}

	basePrice, ok := parseMetadataUint64(meta["base_price"])
	if !ok || basePrice == 0 {
		pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, campaign.Spec.Platform)
		if err != nil || pbp == nil || pbp.Price == 0 {
			return 0, false
		}
		basePrice = pbp.Price
	}
	pagePrice, ok := parseMetadataUint64(meta["page_price"])
	if !ok || pagePrice == 0 {
		pp, err := s.pagePriceRepo.LatestByPlatform(ctx, campaign.Spec.Platform)
		if err != nil || pp == nil || pp.Price == 0 {
			return 0, false
		}
		pagePrice = pp.Price
	}

	numPages, ok := parseMetadataUint64(meta["num_pages"])
	if !ok || numPages == 0 {
		numPages = s.calculateParts(
			campaign.Spec.Content,
			campaign.Spec.AdLink,
			campaign.Spec.ShortLinkDomain,
			campaign.Spec.Platform,
		)
	}

	f := parseMetadataFloat(meta["line_number_price_factor"])
	lineFactor := defaultLineNumberPriceFactor
	if f != nil && *f > 0 {
		lineFactor = *f
	}

	segmentFactor := defaultSegmentPriceFactor
	f = parseMetadataFloat(meta["segment_price_factor"])
	if f != nil && *f > 0 {
		segmentFactor = *f
	}

	pagePriceFloat := float64(pagePrice)
	numPagesFloat := float64(numPages)
	if campaign.Spec.Platform == models.CampaignPlatformSMS {
		return basePrice*uint64(lineFactor*numPagesFloat) + uint64(segmentFactor*pagePriceFloat), true
	}
	return basePrice*uint64(1*1) + uint64(segmentFactor*pagePriceFloat), true
}

func parseMetadataUint64(value any) (uint64, bool) {
	switch v := value.(type) {
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		if err != nil || f < 0 {
			return 0, false
		}
		return uint64(f), true
	default:
		return 0, false
	}
}

// computeFreezeRefundSplit determines how much of refundAmount should be restored
// to FreeBalance vs CreditBalance by reading the balance change recorded in the
// original freeze transaction.
//
// When a campaign budget is frozen, FreeBalance is drained first and any remainder
// is taken from CreditBalance. A refund must reverse that exact split so that a
// customer's real money (FreeBalance) is returned to FreeBalance and not silently
// converted to CreditBalance.
//
// The function applies "free-first" semantics: up to the amount originally taken
// from FreeBalance is restored there; any remaining refund goes to CreditBalance.
// If the balance snapshots cannot be parsed, the entire refundAmount is returned as
// freeRefund so no real money is lost.
func computeFreezeRefundSplit(freezeTx *models.Transaction, refundAmount uint64) (freeRefund, creditRefund uint64) {
	if refundAmount == 0 {
		return 0, 0
	}
	if len(freezeTx.BalanceBefore) == 0 || len(freezeTx.BalanceAfter) == 0 {
		// Cannot determine origin; conservatively return all to free balance.
		return refundAmount, 0
	}

	var balBefore, balAfter map[string]any
	if err := json.Unmarshal(freezeTx.BalanceBefore, &balBefore); err != nil {
		return refundAmount, 0
	}
	if err := json.Unmarshal(freezeTx.BalanceAfter, &balAfter); err != nil {
		return refundAmount, 0
	}

	freeBefore, _ := parseMetadataUint64(balBefore["free"])
	freeAfter, _ := parseMetadataUint64(balAfter["free"])

	// How much of the freeze came from FreeBalance.
	var freeReduced uint64
	if freeBefore > freeAfter {
		freeReduced = freeBefore - freeAfter
	}
	if freeReduced > refundAmount {
		freeReduced = refundAmount
	}

	return freeReduced, refundAmount - freeReduced
}

func (s *CampaignFlowImpl) GetPagePrices(ctx context.Context) (*dto.GetPagePricesResponse, error) {
	rows, err := s.pagePriceRepo.ListLatest(ctx)
	if err != nil {
		return nil, NewBusinessError("PAGE_PRICE_LIST_FAILED", "failed to list page prices", err)
	}

	items := make([]dto.PagePriceItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, dto.PagePriceItem{
			Platform:  row.Platform,
			Price:     row.Price,
			CreatedAt: row.CreatedAt,
		})
	}
	slices.SortFunc(items, func(a, b dto.PagePriceItem) int {
		return strings.Compare(a.Platform, b.Platform)
	})

	return &dto.GetPagePricesResponse{
		Message: "Page prices retrieved successfully",
		Items:   items,
	}, nil
}

func (s *CampaignFlowImpl) shouldHideTestAudience(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	customerID, ok := ctx.Value(utils.CustomerIDKey).(uint)
	if !ok || customerID == 0 {
		return true
	}
	customer, err := getCustomer(ctx, s.customerRepo, customerID)
	if err != nil {
		return true
	}
	return !s.adminConfig.HasMobile(customer.RepresentativeMobile)
}

func filterAudienceSpecLayer(spec dto.AudienceSpec, layer1 string) dto.AudienceSpec {
	if len(spec) == 0 {
		return spec
	}
	out := make(dto.AudienceSpec, len(spec))
	for l1, l2map := range spec {
		if l1 == layer1 {
			continue
		}
		out[l1] = l2map
	}
	return out
}

func (s *CampaignFlowImpl) GetApprovedRunningSummary(ctx context.Context, customerID uint) (*dto.CampaignsSummaryResponse, error) {
	if customerID == 0 {
		return nil, NewBusinessError("CUSTOMER_ID_REQUIRED", "customer_id must be greater than 0", ErrCustomerNotFound)
	}

	// Build counts using repository Count with combined filters
	custID := customerID
	statusApproved := models.CampaignStatusApproved
	statusRunning := models.CampaignStatusRunning

	approvedCount64, err := s.campaignRepo.Count(ctx, models.CampaignFilter{CustomerID: &custID, Status: &statusApproved})
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_COUNT_FAILED", "Failed to count approved campaigns", err)
	}
	runningCount64, err := s.campaignRepo.Count(ctx, models.CampaignFilter{CustomerID: &custID, Status: &statusRunning})
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_COUNT_FAILED", "Failed to count running campaigns", err)
	}

	approved := int(approvedCount64)
	running := int(runningCount64)
	resp := &dto.CampaignsSummaryResponse{
		Message:       "Campaigns summary retrieved",
		ApprovedCount: approved,
		RunningCount:  running,
		Total:         approved + running,
	}
	return resp, nil
}

// calculateParts calculates the number of SMS parts based on the effective
// character count after link substitution rules are applied.
// Non-SMS platforms always use a single part.
func (s *CampaignFlowImpl) calculateParts(content *string, adLink *string, shortLinkDomain *string, platform string) uint64 {
	if platform != models.CampaignPlatformSMS {
		return 1
	}

	if content == nil || *content == "" {
		return 1
	}

	// Count characters after link substitution.
	charCount := s.countCharacters(*content, adLink, shortLinkDomain, platform)

	// A single message fits in 70 characters. Concatenated messages use 66
	// characters per part. Do not cap the result: long valid campaign content
	// must reserve and charge for every part it will send.
	if charCount <= 70 {
		return 1
	}
	return (charCount + 65) / 66
}

// countCharacters counts characters after applying campaign link expansion rules.
func (s *CampaignFlowImpl) countCharacters(text string, adLink *string, shortLinkDomain *string, platform string) uint64 {
	if text == "" {
		if platform == models.CampaignPlatformSMS {
			return 6
		}
		return 0
	}

	textToCount := text

	hasAdLink := adLink != nil && strings.TrimSpace(*adLink) != ""
	hasShortLinkDomain := shortLinkDomain != nil && strings.TrimSpace(*shortLinkDomain) != ""

	switch {
	case hasAdLink && hasShortLinkDomain:
		shortLinkText := strings.TrimSpace(*shortLinkDomain)
		shortLinkText = shortLinkText + "/123456"
		textToCount = strings.ReplaceAll(textToCount, "{YOUR_LINK}", shortLinkText)
	case hasAdLink:
		resolvedAdLink := strings.TrimSpace(*adLink)
		if strings.Contains(resolvedAdLink, "{uid}") {
			resolvedAdLink = strings.ReplaceAll(resolvedAdLink, "{uid}", "123456")
		}
		textToCount = strings.ReplaceAll(textToCount, "{YOUR_LINK}", resolvedAdLink)
	}

	var count uint64
	for _, char := range textToCount {
		// Check if character is English (ASCII range 32-126)
		if char >= 32 && char <= 126 {
			count += 1 // English character
		} else {
			count += 1
		}
	}

	if platform == models.CampaignPlatformSMS {
		count += 6
	}

	return count
}

func sanitizeShortLinkDomain(domain *string) (*string, error) {
	if domain == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*domain)
	if trimmed == "" {
		return nil, ErrInvalidShortLinkDomain
	}
	if !slices.Contains(allowedShortLinkDomains, trimmed) {
		return nil, ErrInvalidShortLinkDomain
	}
	return &trimmed, nil
}

func sanitizeCategoryAndJob(accountType string, category, job *string, isMandatoryForAgency bool) (*string, *string, error) {
	var sanitizedCategory, sanitizedJob *string
	if category != nil {
		cat := strings.TrimSpace(*category)
		if cat == "" {
			return nil, nil, ErrAgencyCategoryJobRequired
		}
		sanitizedCategory = &cat
	}
	if job != nil {
		j := strings.TrimSpace(*job)
		if j == "" {
			return nil, nil, ErrAgencyCategoryJobRequired
		}
		sanitizedJob = &j
	}

	if accountType == models.AccountTypeMarketingAgency && isMandatoryForAgency {
		if sanitizedCategory == nil || sanitizedJob == nil {
			return nil, nil, ErrAgencyCategoryJobRequired
		}
	}

	return sanitizedCategory, sanitizedJob, nil
}

func sanitizeCampaignPlatform(platform *string) (string, error) {
	if platform == nil {
		return models.CampaignPlatformSMS, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(*platform))
	if normalized == "" {
		return "", ErrCampaignPlatformRequired
	}
	if !models.IsValidCampaignPlatform(normalized) {
		return "", ErrCampaignPlatformInvalid
	}
	return normalized, nil
}

func ensureCampaignSpecDefaults(spec *models.CampaignSpec) {
	if spec == nil {
		return
	}
	if strings.TrimSpace(spec.Platform) == "" {
		spec.Platform = models.CampaignPlatformSMS
	}
	if spec.AudienceGrades == nil {
		spec.AudienceGrades = []string{"A", "B", "C"}
	}
}

func sanitizeAudienceGrades(grades []string) ([]string, error) {
	if grades == nil {
		return nil, nil
	}

	valid := []string{"A", "B", "C"}
	normalized := make([]string, 0, len(grades))
	seen := make(map[string]struct{}, len(grades))
	for _, grade := range grades {
		trimmed := strings.ToUpper(strings.TrimSpace(grade))
		if !slices.Contains(valid, trimmed) {
			return nil, ErrCampaignAudienceGradesInvalid
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}

	return normalized, nil
}

func campaignAudienceGradesOrDefault(grades []string) []string {
	if grades == nil {
		return []string{"A", "B", "C"}
	}
	return grades
}

// createAuditLog creates an audit log entry for the campaign operation
func (s *CampaignFlowImpl) createAuditLog(ctx context.Context, customer *models.Customer, action, description string, success bool, errorMsg *string, metadata *ClientMetadata) error {
	var customerID *uint
	if customer != nil {
		customerID = &customer.ID
	}

	ipAddress := ""
	userAgent := ""
	if metadata != nil {
		ipAddress = metadata.IPAddress
		userAgent = metadata.UserAgent
	}

	audit := &models.AuditLog{
		CustomerID:   customerID,
		Action:       action,
		Description:  &description,
		Success:      utils.ToPtr(success),
		IPAddress:    &ipAddress,
		UserAgent:    &userAgent,
		ErrorMessage: errorMsg,
	}

	// Extract request ID from context if available
	requestID := ctx.Value(utils.RequestIDKey)
	if requestID != nil {
		requestIDStr, ok := requestID.(string)
		if ok {
			audit.RequestID = &requestIDStr
		}
	}

	if err := s.auditRepo.Save(ctx, audit); err != nil {
		return err
	}

	return nil
}

func (s *CampaignFlowImpl) CountTargetAudienceFromExcelFile(ctx context.Context, customerID uint, targetAudienceExcelFileUUID string) (uint64, error) {
	asset, err := s.multimediaRepo.ByUUID(ctx, targetAudienceExcelFileUUID)
	if err != nil {
		return 0, err
	}
	if asset == nil || asset.CustomerID != customerID {
		return 0, os.ErrNotExist
	}

	cleanPath, err := sanitizeStoredMultimediaPath(asset.StoredPath)
	if err != nil {
		return 0, err
	}

	rowCount, err := countExcelRows(cleanPath, asset)
	if err != nil {
		return 0, err
	}
	if rowCount == 0 {
		return 0, ErrCampaignTargetAudienceExcelFileInvalid
	}
	return rowCount, nil
}

func countExcelRows(path string, asset *models.MultimediaAsset) (uint64, error) {
	f, err := excelize.OpenFile(path, excelize.Options{
		UnzipSizeLimit:    2 << 30, // 2GB
		UnzipXMLSizeLimit: 1 << 30, // 1GB
	})
	if err != nil {
		return 0, fmt.Errorf("%w: cannot open excel file: %v", ErrCampaignTargetAudienceExcelFileInvalid, err)
	}
	defer func() {
		_ = f.Close()
	}()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return 0, fmt.Errorf("%w: excel file has no sheets", ErrCampaignTargetAudienceExcelFileInvalid)
	}

	rows, err := f.Rows(sheets[0])
	if err != nil {
		return 0, fmt.Errorf("%w: cannot iterate rows: %v", ErrCampaignTargetAudienceExcelFileInvalid, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var count uint64
	for rows.Next() {
		count++
	}
	if err := rows.Error(); err != nil {
		return 0, fmt.Errorf("%w: failed while reading rows: %v", ErrCampaignTargetAudienceExcelFileInvalid, err)
	}

	if isExcelAsset(asset) && count > 0 {
		// Treat first row as header when a dedicated Excel audience file is uploaded.
		count--
	}

	return count, nil
}

func isExcelAsset(asset *models.MultimediaAsset) bool {
	if asset == nil {
		return false
	}
	ext := strings.ToLower(strings.TrimSpace(asset.Extension))
	if ext == ".xlsx" || ext == ".xlsm" || ext == ".xls" {
		return true
	}
	mime := strings.ToLower(strings.TrimSpace(asset.MimeType))
	return strings.Contains(mime, "spreadsheetml") || strings.Contains(mime, "ms-excel")
}

func sanitizeStoredMultimediaPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("invalid empty path")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	base := filepath.ToSlash(filepath.Clean(filepath.Join("data", "uploads", "multimedia")))
	if !strings.HasPrefix(cleaned, base) {
		return "", fmt.Errorf("path outside multimedia root")
	}
	return filepath.FromSlash(cleaned), nil
}

func buildCampaignReportExcel(rows []campaignReportRow) ([]byte, error) {
	xl := excelize.NewFile()
	defer func() { _ = xl.Close() }()

	sheetName := "Report"
	defaultSheet := xl.GetSheetName(0)
	if defaultSheet != sheetName {
		xl.SetSheetName(defaultSheet, sheetName)
	}

	if err := xl.SetSheetRow(sheetName, "A1", &campaignReportHeaders); err != nil {
		return nil, err
	}

	for i, row := range rows {
		record := []string{row.AudienceProfileUID, row.Status, row.Clicked}
		cellRef, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			return nil, err
		}
		if err := xl.SetSheetRow(sheetName, cellRef, &record); err != nil {
			return nil, err
		}
	}

	if err := xl.SetColWidth(sheetName, "A", "C", 24); err != nil {
		return nil, err
	}

	buf, err := xl.WriteToBuffer()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ExportCampaignClickReport builds a CSV with two columns - uid and clicked (true/false) -
// for all audience members targeted by the campaign. UIDs are fetched from the campaign's
// file-backed audience store, and click status is derived from short_link_clicks.
func (s *CampaignFlowImpl) ExportCampaignClickReport(ctx context.Context, campaignUUID string) ([]byte, error) {
	campaignUUID = strings.TrimSpace(campaignUUID)
	if campaignUUID == "" {
		return nil, NewBusinessError("CAMPAIGN_UUID_REQUIRED", "campaign uuid is required", ErrCampaignUUIDRequired)
	}
	parsed, err := uuid.Parse(campaignUUID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_UUID_INVALID", "campaign uuid is invalid", ErrCampaignUUIDRequired)
	}

	customerID, ok := ctx.Value(utils.CustomerIDKey).(uint)
	if !ok || customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "customer id is required", ErrCustomerNotFound)
	}

	campaign, err := getCampaign(ctx, s.campaignRepo, parsed.String(), customerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "failed to lookup campaign", err)
	}

	allUIDs, uidToCode, err := readCampaignAudienceUIDs(campaign.ID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, NewBusinessError("AUDIENCE_REPORT_NOT_AVAILABLE", "audience report data is not available (may have expired or not yet pushed)", nil)
		}
		return nil, NewBusinessError("AUDIENCE_UIDS_FETCH_FAILED", "failed to fetch audience UIDs", err)
	}
	if len(allUIDs) == 0 {
		return nil, NewBusinessError("AUDIENCE_REPORT_NOT_AVAILABLE", "audience report data is not available (may have expired or not yet pushed)", nil)
	}

	clickedCodes := make(map[string]struct{})
	if len(uidToCode) > 0 {
		codes, err := s.shortLinkClickRepo.DistinctShortLinkUIDsByCampaignID(ctx, campaign.ID)
		if err != nil {
			return nil, NewBusinessError("CLICKED_CODES_FETCH_FAILED", "failed to fetch clicked short-link codes", err)
		}
		for _, code := range codes {
			clickedCodes[code] = struct{}{}
		}
	}

	sort.Strings(allUIDs)

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"uid", "clicked"}); err != nil {
		return nil, NewBusinessError("CSV_WRITE_FAILED", "failed to write csv header", err)
	}
	for _, uid := range allUIDs {
		clicked := "false"
		if code, ok := uidToCode[uid]; ok && code != "" {
			if _, wasClicked := clickedCodes[code]; wasClicked {
				clicked = "true"
			}
		}
		if err := w.Write([]string{uid, clicked}); err != nil {
			return nil, NewBusinessError("CSV_WRITE_FAILED", "failed to write csv row", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, NewBusinessError("CSV_FLUSH_FAILED", "failed to flush csv", err)
	}

	return buf.Bytes(), nil
}
