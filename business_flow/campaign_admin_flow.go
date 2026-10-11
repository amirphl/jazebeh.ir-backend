// Package businessflow contains the core business logic and use cases for campaign workflows
package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"gorm.io/gorm"
)

// AdminCampaignFlow handles the campaign business logic
type AdminCampaignFlow interface {
	ListCampaigns(ctx context.Context, filter dto.AdminListCampaignsFilter) (*dto.AdminListCampaignsResponse, error)
	GetCampaign(ctx context.Context, id uint) (*dto.AdminGetCampaignResponse, error)
	ApproveCampaign(ctx context.Context, req *dto.AdminApproveCampaignRequest) (*dto.AdminApproveCampaignResponse, error)
	RejectCampaign(ctx context.Context, req *dto.AdminRejectCampaignRequest) (*dto.AdminRejectCampaignResponse, error)
	CancelCampaign(ctx context.Context, req *dto.AdminCancelCampaignRequest) (*dto.AdminCancelCampaignResponse, error)
	RescheduleCampaign(ctx context.Context, req *dto.AdminRescheduleCampaignRequest) (*dto.AdminRescheduleCampaignResponse, error)
	UpdatePagePrice(ctx context.Context, req *dto.AdminUpdatePagePriceRequest) (*dto.AdminUpdatePagePriceResponse, error)
	GetPagePrices(ctx context.Context) (*dto.AdminGetPagePricesResponse, error)
}

// AdminCampaignFlowImpl implements the campaign business flow
type AdminCampaignFlowImpl struct {
	campaignRepo            repository.CampaignRepository
	customerRepo            repository.CustomerRepository
	walletRepo              repository.WalletRepository
	balanceSnapshotRepo     repository.BalanceSnapshotRepository
	transactionRepo         repository.TransactionRepository
	auditRepo               repository.AuditLogRepository
	platformSettingsRepo    repository.PlatformSettingsRepository
	platformBaseRepo        repository.PlatformBasePriceRepository
	lineNumberRepo          repository.LineNumberRepository
	segmentPriceRepo        repository.SegmentPriceFactorRepository
	pagePriceRepo           repository.PagePriceRepository
	selectedTagRepo         repository.CampaignSelectedTagRepository
	capacityCalculationRepo repository.CampaignTargetingCapacityRepository
	notifier                services.NotificationService
	adminConfig             config.AdminConfig
	messageConfig           config.MessageConfig
	cacheConfig             config.CacheConfig
	rc                      *redis.Client
	db                      *gorm.DB
}

const (
	adminRescheduleMinLeadTime = 2 * time.Minute
	adminCancelMinLeadTime     = 2 * time.Minute
)

var adminTehranLoc *time.Location = time.FixedZone("Asia/Tehran", 3*3600+1800)

// NewAdminCampaignFlow creates a new campaign flow instance
func NewAdminCampaignFlow(
	campaignRepo repository.CampaignRepository,
	customerRepo repository.CustomerRepository,
	walletRepo repository.WalletRepository,
	balanceSnapshotRepo repository.BalanceSnapshotRepository,
	transactionRepo repository.TransactionRepository,
	auditRepo repository.AuditLogRepository,
	platformSettingsRepo repository.PlatformSettingsRepository,
	platformBaseRepo repository.PlatformBasePriceRepository,
	lineNumberRepo repository.LineNumberRepository,
	segmentPriceRepo repository.SegmentPriceFactorRepository,
	pagePriceRepo repository.PagePriceRepository,
	selectedTagRepo repository.CampaignSelectedTagRepository,
	capacityCalculationRepo repository.CampaignTargetingCapacityRepository,
	db *gorm.DB,
	rc *redis.Client,
	notifier services.NotificationService,
	adminConfig config.AdminConfig,
	messageConfig config.MessageConfig,
	cacheConfig config.CacheConfig,
) AdminCampaignFlow {
	return &AdminCampaignFlowImpl{
		campaignRepo:            campaignRepo,
		customerRepo:            customerRepo,
		walletRepo:              walletRepo,
		balanceSnapshotRepo:     balanceSnapshotRepo,
		transactionRepo:         transactionRepo,
		auditRepo:               auditRepo,
		platformSettingsRepo:    platformSettingsRepo,
		platformBaseRepo:        platformBaseRepo,
		lineNumberRepo:          lineNumberRepo,
		segmentPriceRepo:        segmentPriceRepo,
		pagePriceRepo:           pagePriceRepo,
		selectedTagRepo:         selectedTagRepo,
		capacityCalculationRepo: capacityCalculationRepo,
		notifier:                notifier,
		adminConfig:             adminConfig,
		messageConfig:           messageConfig,
		cacheConfig:             cacheConfig,
		rc:                      rc,
		db:                      db,
	}
}

// ListCampaigns retrieves campaigns for admin using optional filters: title (name), status, start/end dates
func (s *AdminCampaignFlowImpl) ListCampaigns(ctx context.Context, filter dto.AdminListCampaignsFilter) (*dto.AdminListCampaignsResponse, error) {
	page := max(1, filter.Page)
	limit := filter.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	cf := models.CampaignFilter{}
	if filter.CampaignTitle != nil && *filter.CampaignTitle != "" {
		cf.CampaignTitle = filter.CampaignTitle
	}
	if filter.BundleTitle != nil && *filter.BundleTitle != "" {
		cf.BundleTitle = filter.BundleTitle
	}
	if filter.CustomerName != nil && *filter.CustomerName != "" {
		cf.CustomerName = filter.CustomerName
	}
	if filter.Status != nil && *filter.Status != "" {
		st := models.CampaignStatus(*filter.Status)
		if st.Valid() {
			cf.Status = &st
		}
	}
	if filter.StartDate != nil {
		cf.CreatedAfter = filter.StartDate
	}
	if filter.EndDate != nil {
		cf.CreatedBefore = filter.EndDate
	}

	if filter.StartDate != nil && filter.EndDate != nil {
		if filter.EndDate.Before(*filter.StartDate) {
			return nil, NewBusinessError("ADMIN_LIST_CAMPAIGNS_FAILED", "End date must be after start date", ErrStartDateAfterEndDate)
		}
	}

	total64, err := s.campaignRepo.Count(ctx, cf)
	if err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignList, "Admin listed campaigns", false, nil, map[string]any{
			"status": filter.Status,
		}, err)
		return nil, NewBusinessError("ADMIN_LIST_CAMPAIGNS_FAILED", "Failed to count campaigns", err)
	}

	// Sort order mirrors the in-memory logic that was previously applied after
	// fetching, but keeps initiated and in-progress campaigns at the end:
	// null/empty schedule_at first, then upcoming (>= NOW()) before past,
	// upcoming sorted soonest-first, past sorted most-recent-first.
	// The regex guard (RFC3339 prefix) prevents a ::timestamptz cast error on
	// malformed values; PostgreSQL short-circuits AND before evaluating the cast.
	const scheduleAtOrder = `
		CASE WHEN status IN ('initiated', 'in-progress') THEN 1 ELSE 0 END ASC,
		CASE WHEN spec->>'schedule_at' IS NULL OR spec->>'schedule_at' = '' THEN 0 ELSE 1 END ASC,
		CASE WHEN spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (spec->>'schedule_at')::timestamptz >= NOW() THEN 0 ELSE 1 END ASC,
		CASE WHEN spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (spec->>'schedule_at')::timestamptz >= NOW() THEN (spec->>'schedule_at')::timestamptz END ASC NULLS LAST,
		CASE WHEN spec->>'schedule_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}' AND (spec->>'schedule_at')::timestamptz < NOW() THEN (spec->>'schedule_at')::timestamptz END DESC NULLS LAST`
	rows, err := s.campaignRepo.ByFilter(ctx, cf, scheduleAtOrder, limit, offset)
	if err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignList, "Admin listed campaigns", false, nil, map[string]any{
			"status": filter.Status,
		}, err)
		return nil, NewBusinessError("ADMIN_LIST_CAMPAIGNS_FAILED", "Failed to list campaigns", err)
	}
	// Click counts and stats
	campaignIDs := make([]uint, 0, len(rows))
	for _, c := range rows {
		campaignIDs = append(campaignIDs, c.ID)
	}
	clickCounts, err := s.campaignRepo.AggregateClickCountsByCampaignIDs(ctx, campaignIDs)
	if err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignList, "Admin listed campaigns", false, nil, map[string]any{
			"status": filter.Status,
		}, err)
		return nil, NewBusinessError("ADMIN_LIST_CAMPAIGNS_FAILED", "Failed to aggregate click counts", err)
	}

	items := make([]dto.AdminGetCampaignResponse, 0, len(rows))
	platformBasePrices := make(map[string]*uint64)
	for _, c := range rows {
		ensureCampaignSpecDefaults(&c.Spec)

		var stats map[string]any
		if len(c.Statistics) > 0 {
			_ = json.Unmarshal(c.Statistics, &stats)
		}
		var bundleTitle *string
		if c.Bundle != nil {
			bundleTitle = &c.Bundle.Title
		}
		clicks := clickCounts[c.ID]
		totalClicks := clicks
		clickRate := computeClickRate(clicks, parseAggregatedTotalSentFromMap(stats))
		platformBasePrice, err := s.resolvePlatformBasePriceForAdminList(ctx, c.Spec.Platform, platformBasePrices)
		if err != nil {
			logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignList, "Admin listed campaigns", false, nil, map[string]any{
				"status": filter.Status,
			}, err)
			return nil, NewBusinessError("ADMIN_LIST_CAMPAIGNS_FAILED", "Failed to fetch platform base price", err)
		}

		items = append(items, dto.AdminGetCampaignResponse{
			ID:                 c.ID,
			UUID:               c.UUID.String(),
			Hidden:             c.Hidden,
			Status:             c.Status.String(),
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
			Title:              c.Spec.Title,
			Level1:             c.Spec.Level1,
			Level2s:            c.Spec.Level2s,
			Level3s:            c.Spec.Level3s,
			Tags:               c.Spec.Tags,
			TargetingMethod:    campaignAudienceTargetingMethod(c.Spec),
			Sex:                c.Spec.Sex,
			City:               c.Spec.City,
			AdLink:             c.Spec.AdLink,
			Content:            c.Spec.Content,
			ShortLinkDomain:    c.Spec.ShortLinkDomain,
			Category:           c.Spec.Category,
			Job:                c.Spec.Job,
			ScheduleAt:         c.Spec.ScheduleAt,
			LineNumber:         c.Spec.LineNumber,
			MediaUUID:          c.Spec.MediaUUID,
			PlatformSettingsID: c.Spec.PlatformSettingsID,
			Platform:           c.Spec.Platform,
			PlatformBasePrice:  platformBasePrice,
			Budget:             c.Spec.Budget,
			Comment:            c.Comment,
			Statistics:         stats,
			TotalClicks:        &totalClicks,
			ClickRate:          clickRate,
			NumAudience:        c.NumAudience,
			SampleSizePerTag:   c.SampleSizePerTag,
			CustomerFullName:   formatCampaignPartyFullName(c.Customer),
			AgencyFullName:     formatCampaignAgencyFullName(c.Customer),

			BundleID:    c.BundleID,
			BundleTitle: bundleTitle,
			Phase:       campaignPhasePtr(c.Phase),

			AudienceGrades: campaignAudienceGradesOrDefault(c.Spec.AudienceGrades),

			TargetAudienceExcelFileUUID: c.Spec.TargetAudienceExcelFileUUID,
		})
	}
	totalPages := int((total64 + int64(limit) - 1) / int64(limit))

	resp := &dto.AdminListCampaignsResponse{
		Message: "Campaigns retrieved successfully",
		Items:   items,
		Pagination: dto.PaginationInfo{
			Total:      total64,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}
	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignList, "Admin listed campaigns", true, nil, map[string]any{
		"items":       len(items),
		"total":       total64,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
	}, nil)
	return resp, nil
}

// GetCampaign retrieves a single campaign by ID for admin
func (s *AdminCampaignFlowImpl) GetCampaign(ctx context.Context, id uint) (*dto.AdminGetCampaignResponse, error) {
	c, err := s.campaignRepo.ByID(ctx, id)
	if err != nil {
		return nil, NewBusinessError("ADMIN_GET_CAMPAIGN_FAILED", "Failed to get campaign", err)
	}
	if c == nil {
		return nil, ErrCampaignNotFound
	}
	ensureCampaignSpecDefaults(&c.Spec)

	var stats map[string]any
	if len(c.Statistics) > 0 {
		_ = json.Unmarshal(c.Statistics, &stats)
	}
	clickCounts, err := s.campaignRepo.AggregateClickCountsByCampaignIDs(ctx, []uint{id})
	if err != nil {
		return nil, NewBusinessError("ADMIN_GET_CAMPAIGN_FAILED", "Failed to aggregate click counts", err)
	}
	clicks := clickCounts[id]
	totalClicks := clicks
	clickRate := computeClickRate(clicks, parseAggregatedTotalSentFromMap(stats))
	metaPlatform, metaSegment, metaLine, err := s.readCampaignPriceFactorsFromMetadata(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	platformBasePrice := uint64(0) // TODO: Should return -1 to indicate an issue.
	if metaPlatform != nil {
		platformBasePrice = *metaPlatform
	}
	segmentPriceFactor := float64(-1)
	if metaSegment != nil {
		segmentPriceFactor = *metaSegment
	}
	// else if len(c.Spec.Level3s) > 0 {
	// 	factors, err := s.segmentPriceRepo.LatestByLevel3s(ctx, c.Spec.Level3s)
	// 	if err != nil {
	// 		return nil, NewBusinessError("ADMIN_GET_CAMPAIGN_FAILED", "Failed to get segment price factor", err)
	// 	}
	// 	maxFactor := float64(0)
	// 	for _, l3 := range c.Spec.Level3s {
	// 		if f, ok := factors[l3]; ok && f > maxFactor {
	// 			maxFactor = f
	// 		}
	// 	}
	// 	if maxFactor > 0 {
	// 		segmentPriceFactor = maxFactor
	// 	}
	// }
	lineNumberPriceFactor := float64(-1)
	if metaLine != nil {
		lineNumberPriceFactor = *metaLine
	}
	// else if c.Spec.LineNumber != nil {
	// 	lineNumber, err := s.lineNumberRepo.ByValue(ctx, *c.Spec.LineNumber)
	// 	if err != nil {
	// 		return nil, NewBusinessError("ADMIN_GET_CAMPAIGN_FAILED", "Failed to get line number price factor", err)
	// 	}
	// 	if lineNumber != nil {
	// 		lineNumberPriceFactor = lineNumber.PriceFactor
	// 	}
	// }
	var bundleTitle *string
	if c.Bundle != nil {
		bundleTitle = &c.Bundle.Title
	}

	resp := &dto.AdminGetCampaignResponse{
		ID:                    c.ID,
		UUID:                  c.UUID.String(),
		Hidden:                c.Hidden,
		Status:                c.Status.String(),
		CreatedAt:             c.CreatedAt,
		UpdatedAt:             c.UpdatedAt,
		Title:                 c.Spec.Title,
		Level1:                c.Spec.Level1,
		Level2s:               c.Spec.Level2s,
		Level3s:               c.Spec.Level3s,
		Tags:                  c.Spec.Tags,
		TargetingMethod:       campaignAudienceTargetingMethod(c.Spec),
		Sex:                   c.Spec.Sex,
		City:                  c.Spec.City,
		AdLink:                c.Spec.AdLink,
		Content:               c.Spec.Content,
		ShortLinkDomain:       c.Spec.ShortLinkDomain,
		Category:              c.Spec.Category,
		Job:                   c.Spec.Job,
		ScheduleAt:            c.Spec.ScheduleAt,
		LineNumber:            c.Spec.LineNumber,
		MediaUUID:             c.Spec.MediaUUID,
		PlatformSettingsID:    c.Spec.PlatformSettingsID,
		Platform:              c.Spec.Platform,
		PlatformBasePrice:     &platformBasePrice,
		Budget:                c.Spec.Budget,
		Comment:               c.Comment,
		SegmentPriceFactor:    segmentPriceFactor,
		LineNumberPriceFactor: lineNumberPriceFactor,
		Statistics:            stats,
		TotalClicks:           &totalClicks,
		ClickRate:             clickRate,
		NumAudience:           c.NumAudience,
		SampleSizePerTag:      c.SampleSizePerTag,
		CustomerFullName:      formatCampaignPartyFullName(c.Customer),
		AgencyFullName:        formatCampaignAgencyFullName(c.Customer),

		BundleID:    c.BundleID,
		BundleTitle: bundleTitle,
		Phase:       campaignPhasePtr(c.Phase),

		AudienceGrades: campaignAudienceGradesOrDefault(c.Spec.AudienceGrades),

		TargetAudienceExcelFileUUID: c.Spec.TargetAudienceExcelFileUUID,
	}
	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignGet, "Admin fetched campaign", true, &c.CustomerID, map[string]any{
		"campaign_id": c.ID,
	}, nil)
	return resp, nil
}

func (s *AdminCampaignFlowImpl) resolvePlatformBasePriceForAdminList(ctx context.Context, platform string, cache map[string]*uint64) (*uint64, error) {
	if value, ok := cache[platform]; ok {
		return value, nil
	}

	pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	if pbp == nil {
		cache[platform] = nil
		return nil, nil
	}

	cache[platform] = &pbp.Price
	return cache[platform], nil
}

func formatCampaignPartyFullName(customer *models.Customer) *string {
	if customer == nil {
		return nil
	}

	fullName := strings.TrimSpace(customer.RepresentativeFirstName + " " + customer.RepresentativeLastName)
	companyName := ""
	if customer.CompanyName != nil {
		companyName = strings.TrimSpace(*customer.CompanyName)
	}

	switch {
	case fullName != "" && companyName != "":
		value := fullName + " - " + companyName
		return &value
	case fullName != "":
		return &fullName
	case companyName != "":
		return &companyName
	default:
		return nil
	}
}

func formatCampaignAgencyFullName(customer *models.Customer) *string {
	if customer == nil {
		return nil
	}
	return formatCampaignPartyFullName(customer.ReferrerAgency)
}

func (s *AdminCampaignFlowImpl) readCampaignPriceFactorsFromMetadata(ctx context.Context, campaignID uint) (*uint64, *float64, *float64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, nil, nil, NewBusinessError("ADMIN_GET_CAMPAIGN_FAILED", "Failed to get campaign metadata", err)
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil, nil, nil
	}

	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil, nil, nil
	}

	basePrice, _ := parseMetadataUint64(meta["base_price"])
	segmentPriceFactor := parseMetadataFloat(meta["segment_price_factor"])
	lineNumberPriceFactor := parseMetadataFloat(meta["line_number_price_factor"])
	if basePrice == 0 {
		return nil, segmentPriceFactor, lineNumberPriceFactor, nil
	}
	return &basePrice, segmentPriceFactor, lineNumberPriceFactor, nil
}

func parseMetadataFloat(value any) *float64 {
	switch v := value.(type) {
	case float64:
		return &v
	case int64:
		f := float64(v)
		return &f
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return &f
		}
	}
	return nil
}

// ApproveCampaign approves a campaign: ensure schedule_at > now, change status to approved, and reduce frozen to locked spend
func (s *AdminCampaignFlowImpl) ApproveCampaign(ctx context.Context, req *dto.AdminApproveCampaignRequest) (*dto.AdminApproveCampaignResponse, error) {
	if req == nil || req.CampaignID == 0 {
		return nil, NewBusinessError("ADMIN_APPROVE_CAMPAIGN_FAILED", "campaign_id is required", nil)
	}

	var campaign *models.Campaign
	var customer models.Customer

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
		if campaign.Status != models.CampaignStatusWaitingForApproval {
			return ErrCampaignNotWaitingForApproval
		}
		if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.Before(utils.UTCNow()) {
			return ErrScheduleTimeTooSoon
		}
		// Every non-Excel campaign consumes the shared Bundle audience ledger.
		// Capacity workers take a SHARE lock on the same Bundle row, so both
		// standard and Smart Targeting approvals must take the UPDATE lock before
		// establishing a new unmaterialized reservation.
		if !campaign.Spec.UsesExcelTargeting() {
			if campaign.BundleID == nil || *campaign.BundleID == 0 {
				return ErrBundleNotFound
			}
			if err := repository.LockBundleForUpdate(txCtx, *campaign.BundleID); err != nil {
				return err
			}
		}
		if campaign.Spec.UsesSmartTargeting() {
			if s.capacityCalculationRepo == nil {
				return ErrSmartTargetingExactCapacityRequired
			}
			exact, err := CurrentSmartTargetingCapacity(txCtx, s.db, s.selectedTagRepo, s.capacityCalculationRepo, campaign)
			if err != nil {
				return err
			}
			var requiredAudience uint64
			if campaign.Phase == models.CampaignPhaseTest {
				intent, intentErr := currentSmartTargetingTestSamplingIntent(txCtx, s.selectedTagRepo, campaign, true)
				if intentErr != nil {
					return intentErr
				}
				if campaign.ActiveSmartTargetingTestSelectionID == nil {
					return ErrSmartTargetingTestPreviewRequired
				}
				snapshot, selectionErr := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).
					ActiveReservedForCampaign(txCtx, campaign.ID, *campaign.ActiveSmartTargetingTestSelectionID)
				if selectionErr != nil || snapshot == nil || int64(len(snapshot.Members)) != int64(intent.effective) {
					return ErrSmartTargetingTestPreviewRequired
				}
				requiredAudience = intent.effective
				campaign.NumAudience = utils.ToPtr(requiredAudience)
			} else if campaign.NumAudience != nil {
				requiredAudience = *campaign.NumAudience
			} else {
				return ErrSmartTargetingExactCapacityRequired
			}
			if exact.UsableUniqueAudienceCount < 0 || requiredAudience > uint64(exact.UsableUniqueAudienceCount) {
				return ErrSmartTargetingExactCapacityRequired
			}
		}
		if err := s.validateApprovalPlatformSettings(txCtx, campaign); err != nil {
			return err
		}

		customer, err = getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
		if err != nil {
			return err
		}

		// Find the frozen reservation transaction created during finalize
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

		// Load wallet and current balance
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

		amount := freezeTx.Amount
		if latestBalance.FrozenBalance < amount {
			return ErrInsufficientFunds
		}

		meta := map[string]any{
			"source":      "admin_campaign_approve",
			"operation":   "approve_campaign_budget_consume",
			"campaign_id": campaign.ID,
		}
		if req.Comment != nil {
			meta["comment"] = *req.Comment
		}
		metaBytes, _ := json.Marshal(meta)

		newFrozen := latestBalance.FrozenBalance - amount
		newSpentOnCampaign := latestBalance.SpentOnCampaign + amount

		newSnap := &models.BalanceSnapshot{
			UUID:               uuid.New(),
			CorrelationID:      freezeTx.CorrelationID,
			WalletID:           wallet.ID,
			CustomerID:         customer.ID,
			FreeBalance:        latestBalance.FreeBalance,
			FrozenBalance:      newFrozen,
			LockedBalance:      latestBalance.LockedBalance,
			CreditBalance:      latestBalance.CreditBalance,
			SpentOnCampaign:    newSpentOnCampaign,
			AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			TotalBalance:       latestBalance.FreeBalance + newFrozen + latestBalance.LockedBalance + latestBalance.CreditBalance + newSpentOnCampaign + latestBalance.AgencyShareWithTax,
			Reason:             "campaign_approved_budget_spent_on_campaign",
			Description:        fmt.Sprintf("Budget spent on approved campaign %d", campaign.ID),
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

		feeTx := &models.Transaction{
			UUID:          uuid.New(),
			CorrelationID: freezeTx.CorrelationID,
			Type:          models.TransactionTypeFee,
			Status:        models.TransactionStatusCompleted,
			Amount:        amount,
			Currency:      utils.TomanCurrency,
			WalletID:      wallet.ID,
			CustomerID:    customer.ID,
			BalanceBefore: beforeMap,
			BalanceAfter:  afterMap,
			Description:   fmt.Sprintf("Budget locked for approved campaign %d", campaign.ID),
			Metadata:      metaBytes,
		}
		if err := s.transactionRepo.Save(txCtx, feeTx); err != nil {
			return err
		}

		campaign.Status = models.CampaignStatusApproved
		campaign.Comment = req.Comment
		campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
		if err := s.campaignRepo.Update(txCtx, *campaign); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrSmartTargetingExactCapacityRequired) && campaign != nil && s.capacityCalculationRepo != nil {
			_, ensureErr := EnsureCurrentSmartTargetingCapacity(ctx, s.db, s.campaignRepo, s.selectedTagRepo, s.capacityCalculationRepo, campaign)
			if ensureErr != nil {
				err = ensureErr
			}
		}
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignApproved, "Admin approved campaign", false, nil, map[string]any{
			"campaign_id": req.CampaignID,
		}, err)
		if errors.Is(err, ErrSmartTargetingCapacityPending) {
			return nil, NewBusinessError("SMART_TARGETING_CAPACITY_PENDING", "Exact Smart Targeting capacity calculation was submitted; please wait and retry approval", err)
		}
		if errors.Is(err, ErrSmartTargetingExactCapacityRequired) {
			return nil, NewBusinessError("SMART_TARGETING_EXACT_CAPACITY_REQUIRED", "A current exact Smart Targeting capacity calculation is required before approval", err)
		}
		return nil, NewBusinessError("ADMIN_APPROVE_CAMPAIGN_FAILED", "Failed to approve campaign", err)
	}

	// Notify customer (best-effort, outside transaction)
	if s.notifier != nil {
		title := campaign.UUID.String()
		if campaign.Spec.Title != nil && *campaign.Spec.Title != "" {
			title = *campaign.Spec.Title
		}
		customerMobile := normalizeIranMobile(customer.RepresentativeMobile)
		msgCustomer := fmt.Sprintf("Your campaign '%s' has been approved.", title)
		id64 := int64(customer.ID)
		smsCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.notifier.SendSMS(smsCtx, customerMobile, msgCustomer, &id64)
	}

	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignApproved, "Admin approved campaign", true, &customer.ID, map[string]any{
		"campaign_id": campaign.ID,
		"comment":     req.Comment,
	}, nil)
	return &dto.AdminApproveCampaignResponse{Message: "Campaign approved successfully"}, nil
}

func (s *AdminCampaignFlowImpl) validateApprovalPlatformSettings(ctx context.Context, campaign *models.Campaign) error {
	if campaign == nil {
		return ErrCampaignNotFound
	}

	platform := strings.ToLower(strings.TrimSpace(campaign.Spec.Platform))
	switch platform {
	case models.CampaignPlatformBale, models.CampaignPlatformRubika, models.CampaignPlatformSPlus:
	default:
		return nil
	}

	if campaign.Spec.PlatformSettingsID == nil || *campaign.Spec.PlatformSettingsID == 0 {
		return ErrCampaignPlatformSettingRequired
	}

	settings, err := s.platformSettingsRepo.ByID(ctx, *campaign.Spec.PlatformSettingsID)
	if err != nil {
		return err
	}
	if settings == nil {
		return ErrCampaignPlatformSettingNotFound
	}
	if settings.CustomerID != campaign.CustomerID || strings.ToLower(strings.TrimSpace(settings.Platform)) != platform {
		return ErrCampaignPlatformSettingNotFound
	}

	switch platform {
	case models.CampaignPlatformBale:
		if _, err := parsePositiveIntMetadata(settings.Metadata, "bale_bot_id"); err != nil {
			return fmt.Errorf("campaign platform_settings.metadata.bale_bot_id is required for bale campaigns: %w", err)
		}
	case models.CampaignPlatformRubika:
		if _, err := parseStringMetadata(settings.Metadata, "rubika_service_id"); err != nil {
			return fmt.Errorf("campaign platform_settings.metadata.rubika_service_id is required for rubika campaigns: %w", err)
		}
	case models.CampaignPlatformSPlus:
		if _, err := parseStringMetadata(settings.Metadata, "splus_bot_id"); err != nil {
			return fmt.Errorf("campaign platform_settings.metadata.splus_bot_id is required for splus campaigns: %w", err)
		}
	}

	return nil
}

func parsePositiveIntMetadata(metadata map[string]any, key string) (int64, error) {
	if metadata == nil {
		return 0, fmt.Errorf("metadata is missing")
	}
	raw, ok := metadata[key]
	if !ok {
		return 0, fmt.Errorf("%s is missing", key)
	}

	switch v := raw.(type) {
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("%s must be positive", key)
		}
		return int64(v), nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("%s must be positive", key)
		}
		return v, nil
	case float64:
		if v <= 0 || v != float64(int64(v)) {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return int64(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, fmt.Errorf("%s must not be empty", key)
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return id, nil
	case json.Number:
		id, err := v.Int64()
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("%s has unsupported type %T", key, raw)
	}
}

func parseStringMetadata(metadata map[string]any, key string) (string, error) {
	if metadata == nil {
		return "", fmt.Errorf("metadata is missing")
	}
	raw, ok := metadata[key]
	if !ok {
		return "", fmt.Errorf("%s is missing", key)
	}

	switch v := raw.(type) {
	case string:
		out := strings.TrimSpace(v)
		if out == "" {
			return "", fmt.Errorf("%s must not be empty", key)
		}
		return out, nil
	case int:
		if v <= 0 {
			return "", fmt.Errorf("%s must be positive", key)
		}
		return strconv.Itoa(v), nil
	case int64:
		if v <= 0 {
			return "", fmt.Errorf("%s must be positive", key)
		}
		return strconv.FormatInt(v, 10), nil
	case float64:
		if v <= 0 {
			return "", fmt.Errorf("%s must be positive", key)
		}
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), nil
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case json.Number:
		out := strings.TrimSpace(v.String())
		if out == "" {
			return "", fmt.Errorf("%s must not be empty", key)
		}
		return out, nil
	default:
		return "", fmt.Errorf("%s has unsupported type %T", key, raw)
	}
}

// RejectCampaign rejects a campaign: change status to rejected and refund frozen to free
func (s *AdminCampaignFlowImpl) RejectCampaign(ctx context.Context, req *dto.AdminRejectCampaignRequest) (*dto.AdminRejectCampaignResponse, error) {
	if req == nil || req.CampaignID == 0 || strings.TrimSpace(req.Comment) == "" {
		return nil, NewBusinessError("ADMIN_REJECT_CAMPAIGN_FAILED", "campaign_id and comment are required", nil)
	}

	var campaign *models.Campaign
	var customer models.Customer

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
		if campaign.Status != models.CampaignStatusWaitingForApproval {
			return ErrCampaignNotWaitingForApproval
		}

		customer, err = getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
		if err != nil {
			return err
		}

		// Find the frozen reservation transaction created during finalize
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

		amount := freezeTx.Amount
		if latestBalance.FrozenBalance < amount {
			return ErrInsufficientFunds
		}

		meta := map[string]any{
			"source":      "admin_campaign_reject",
			"operation":   "reject_campaign_refund_frozen",
			"campaign_id": campaign.ID,
			"comment":     req.Comment,
		}
		metaBytes, _ := json.Marshal(meta)

		// TODO:
		// // Restore free/credit split exactly as it was taken during the freeze.
		// newFrozen := latestBalance.FrozenBalance - amount
		// freeRefund, creditRefund := computeFreezeRefundSplit(freezeTx, amount)
		// newFree := latestBalance.FreeBalance + freeRefund
		// newCredit := latestBalance.CreditBalance + creditRefund

		// Move from frozen back to free
		newFrozen := latestBalance.FrozenBalance - amount
		newCredit := latestBalance.CreditBalance + amount

		newSnap := &models.BalanceSnapshot{
			UUID:          uuid.New(),
			CorrelationID: freezeTx.CorrelationID,
			WalletID:      wallet.ID,
			CustomerID:    customer.ID,
			// FreeBalance:        newFree,
			FreeBalance:        latestBalance.FreeBalance,
			FrozenBalance:      newFrozen,
			LockedBalance:      latestBalance.LockedBalance,
			CreditBalance:      newCredit,
			SpentOnCampaign:    latestBalance.SpentOnCampaign,
			AgencyShareWithTax: latestBalance.AgencyShareWithTax,
			// TotalBalance:       newFree + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
			TotalBalance: latestBalance.FreeBalance + newFrozen + latestBalance.LockedBalance + newCredit + latestBalance.SpentOnCampaign + latestBalance.AgencyShareWithTax,
			Reason:       "campaign_rejected_budget_refund",
			Description:  fmt.Sprintf("Refund reserved budget for rejected campaign %d", campaign.ID),
			Metadata:     metaBytes,
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
			Description:   fmt.Sprintf("Refund reserved budget for rejected campaign %d", campaign.ID),
			Metadata:      metaBytes,
		}
		if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
			return err
		}

		campaign.Status = models.CampaignStatusRejected
		campaign.Comment = &req.Comment
		campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
		if err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ReleaseForCampaign(txCtx, campaign.ID); err != nil {
			return err
		}
		if err := s.campaignRepo.Update(txCtx, *campaign); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignRejected, "Admin rejected campaign", false, nil, map[string]any{
			"campaign_id": req.CampaignID,
			"comment":     req.Comment,
		}, err)
		return nil, NewBusinessError("ADMIN_REJECT_CAMPAIGN_FAILED", "Failed to reject campaign", err)
	}

	// Notify customer and admin (best-effort, outside transaction)
	if s.notifier != nil {
		title := campaign.UUID.String()
		if campaign.Spec.Title != nil && *campaign.Spec.Title != "" {
			title = *campaign.Spec.Title
		}
		customerMobile := normalizeIranMobile(customer.RepresentativeMobile)
		msgCustomer := strings.TrimSpace(s.messageConfig.CampaignRejectedTemplate)
		if msgCustomer == "" {
			msgCustomer = "Your campaign '%s' has been rejected."
		}
		if strings.Contains(msgCustomer, "%") {
			msgCustomer = fmt.Sprintf(msgCustomer, title)
		}
		id64 := int64(customer.ID)
		smsCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.notifier.SendSMS(smsCtx, customerMobile, msgCustomer, &id64)
		adminMsg := fmt.Sprintf("Campaign rejected:\n%s", title)
		for _, mobile := range s.adminConfig.ActiveMobiles() {
			_ = s.notifier.SendSMS(smsCtx, mobile, adminMsg, nil)
		}
	}

	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignRejected, "Admin rejected campaign", true, &customer.ID, map[string]any{
		"campaign_id": campaign.ID,
		"comment":     req.Comment,
	}, nil)
	return &dto.AdminRejectCampaignResponse{
		Message: "Campaign rejected and budget refunded successfully",
	}, nil
}

// RescheduleCampaign updates the scheduled time for eligible campaigns (admin action).
func (s *AdminCampaignFlowImpl) RescheduleCampaign(ctx context.Context, req *dto.AdminRescheduleCampaignRequest) (*dto.AdminRescheduleCampaignResponse, error) {
	if req == nil || req.CampaignID == 0 {
		return nil, NewBusinessError("ADMIN_RESCHEDULE_CAMPAIGN_FAILED", "campaign_id is required", ErrCampaignNotFound)
	}
	if req.ScheduleAt.IsZero() {
		return nil, NewBusinessError("SCHEDULE_TIME_REQUIRED", "schedule_at is required", ErrScheduleTimeNotPresent)
	}
	if !isUTCInstant(req.ScheduleAt) {
		return nil, NewBusinessError("SCHEDULE_TIME_MUST_BE_UTC", "schedule_at must be a UTC timestamp", ErrScheduleTimeMustBeUTC)
	}

	campaign, err := s.campaignRepo.ByID(ctx, req.CampaignID)
	if err != nil {
		return nil, NewBusinessError("ADMIN_RESCHEDULE_CAMPAIGN_FAILED", "Failed to fetch campaign", err)
	}
	if campaign == nil {
		return nil, ErrCampaignNotFound
	}
	if !isAdminReschedulable(campaign.Status) {
		return nil, ErrCampaignRescheduleNotAllowed
	}

	scheduleUTC := req.ScheduleAt.UTC()
	nowUTC := utils.UTCNow()
	minAllowedUTC := nowUTC.Add(adminRescheduleMinLeadTime)
	isMissedPendingApproval := isAdminMissedPendingApprovalCampaign(campaign, nowUTC)

	if campaign.Spec.ScheduleAt != nil && campaign.Spec.ScheduleAt.IsZero() {
		return nil, NewBusinessError("SCHEDULE_TIME_NOT_PRESENT", "current campaign schedule is missing", ErrScheduleTimeNotPresent)
	}
	// Preserve the lead-time guard for active schedules, but allow recovery of
	// missed waiting-for-approval campaigns whose schedule has already passed.
	if campaign.Spec.ScheduleAt != nil && !isMissedPendingApproval && campaign.Spec.ScheduleAt.UTC().Sub(nowUTC) < adminRescheduleMinLeadTime {
		return nil, NewBusinessError("SCHEDULE_TIME_TOO_CLOSE_TO_CURRENT", "current schedule is too close to reschedule", ErrScheduleTimeTooCloseToCurrent)
	}
	if scheduleUTC.Before(minAllowedUTC) {
		return nil, ErrScheduleTimeTooSoon
	}
	tehranTime := scheduleUTC.In(tehranLocation())
	if !isWithinRescheduleWindow(tehranTime) {
		return nil, NewBusinessError("SCHEDULE_TIME_OUTSIDE_WINDOW", "Schedule time must be between 08:00 and 21:00 Asia/Tehran", ErrScheduleTimeOutsideWindow)
	}

	campaign.Spec.ScheduleAt = utils.ToPtr(scheduleUTC)
	campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())

	if err := s.campaignRepo.Update(ctx, *campaign); err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignRescheduled, "Admin rescheduled campaign", false, &campaign.CustomerID, map[string]any{
			"campaign_id": req.CampaignID,
			"schedule_at": scheduleUTC,
		}, err)
		return nil, NewBusinessError("ADMIN_RESCHEDULE_CAMPAIGN_FAILED", "Failed to reschedule campaign", err)
	}

	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignRescheduled, "Admin rescheduled campaign", true, &campaign.CustomerID, map[string]any{
		"campaign_id": req.CampaignID,
		"schedule_at": scheduleUTC,
	}, nil)
	return &dto.AdminRescheduleCampaignResponse{
		Message: "Campaign rescheduled successfully",
	}, nil
}

// CancelCampaign cancels an approved campaign or a missed waiting-for-approval campaign.
func (s *AdminCampaignFlowImpl) CancelCampaign(ctx context.Context, req *dto.AdminCancelCampaignRequest) (*dto.AdminCancelCampaignResponse, error) {
	if req == nil || req.CampaignID == 0 || strings.TrimSpace(req.Comment) == "" {
		return nil, NewBusinessError("ADMIN_CANCEL_CAMPAIGN_FAILED", "campaign_id and comment are required", nil)
	}

	var campaign *models.Campaign
	var customer models.Customer

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
		nowUTC := utils.UTCNow()
		isMissedPendingApproval := isAdminMissedPendingApprovalCampaign(campaign, nowUTC)
		if campaign.Status != models.CampaignStatusApproved && !isMissedPendingApproval {
			return ErrCampaignNotApproved
		}
		if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() {
			return ErrScheduleTimeNotPresent
		}
		if campaign.Status == models.CampaignStatusApproved && campaign.Spec.ScheduleAt.UTC().Sub(nowUTC) < adminCancelMinLeadTime {
			return NewBusinessError("SCHEDULE_TIME_TOO_CLOSE_TO_CANCEL", "current schedule is too close to cancel", ErrScheduleTimeTooCloseToCancel)
		}

		customer, err = getCustomer(txCtx, s.customerRepo, campaign.CustomerID)
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

		if isMissedPendingApproval {
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
				"source":        "admin_campaign_cancel",
				"operation":     "cancel_campaign_refund_frozen_missed_approval_deadline",
				"campaign_id":   campaign.ID,
				"comment":       req.Comment,
				"refund_amount": amount,
			}
			metaBytes, _ := json.Marshal(meta)

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
				Reason:             "campaign_cancelled_by_admin_budget_refund_before_approval",
				Description:        fmt.Sprintf("Refund reserved budget for admin-cancelled campaign %d before approval", campaign.ID),
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
				Description:   fmt.Sprintf("Refund reserved budget for admin-cancelled campaign %d before approval", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}
		} else {
			// Find the debit (fee) transaction created when campaign was approved.
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

			meta := map[string]any{
				"source":        "admin_campaign_cancel",
				"operation":     "cancel_campaign_refund_spent",
				"campaign_id":   campaign.ID,
				"comment":       req.Comment,
				"refund_amount": amount,
			}
			metaBytes, _ := json.Marshal(meta)

			newCredit := latestBalance.CreditBalance + amount
			newSpentOnCampaign := latestBalance.SpentOnCampaign - amount

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
				Reason:             "campaign_cancelled_by_admin_budget_refund",
				Description:        fmt.Sprintf("Refund spent budget for admin-cancelled campaign %d", campaign.ID),
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
				Description:   fmt.Sprintf("Refund spent budget for admin-cancelled campaign %d", campaign.ID),
				Metadata:      metaBytes,
			}
			if err := s.transactionRepo.Save(txCtx, refundTx); err != nil {
				return err
			}
		}

		campaign.Status = models.CampaignStatusCancelledByAdmin
		campaign.Comment = &req.Comment
		campaign.UpdatedAt = utils.ToPtr(utils.UTCNow())
		if err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ReleaseForCampaign(txCtx, campaign.ID); err != nil {
			return err
		}
		if err := s.campaignRepo.Update(txCtx, *campaign); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignCancelled, "Admin cancelled campaign", false, nil, map[string]any{
			"campaign_id": req.CampaignID,
			"comment":     req.Comment,
		}, err)
		return nil, NewBusinessError("ADMIN_CANCEL_CAMPAIGN_FAILED", "Failed to cancel campaign", err)
	}

	// Notify customer/admin (best effort)
	if s.notifier != nil {
		title := campaign.UUID.String()
		if campaign.Spec.Title != nil && *campaign.Spec.Title != "" {
			title = *campaign.Spec.Title
		}
		customerMobile := normalizeIranMobile(customer.RepresentativeMobile)
		msgCustomer := fmt.Sprintf("Your campaign '%s' has been cancelled by admin.", title)
		id64 := int64(customer.ID)
		smsCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.notifier.SendSMS(smsCtx, customerMobile, msgCustomer, &id64)
		adminMsg := fmt.Sprintf("Campaign cancelled by admin:\n%s", title)
		for _, mobile := range s.adminConfig.ActiveMobiles() {
			_ = s.notifier.SendSMS(smsCtx, mobile, adminMsg, nil)
		}
	}

	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminCampaignCancelled, "Admin cancelled campaign", true, &customer.ID, map[string]any{
		"campaign_id": campaign.ID,
		"comment":     req.Comment,
	}, nil)
	return &dto.AdminCancelCampaignResponse{
		Message: "Campaign cancelled and budget refunded successfully",
	}, nil
}

func (s *AdminCampaignFlowImpl) UpdatePagePrice(ctx context.Context, req *dto.AdminUpdatePagePriceRequest) (*dto.AdminUpdatePagePriceResponse, error) {
	if req == nil {
		return nil, NewBusinessError("INVALID_REQUEST", "request is required", nil)
	}

	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform == "" {
		return nil, NewBusinessError("PAGE_PRICE_PLATFORM_REQUIRED", "platform is required", ErrCampaignPlatformRequired)
	}
	if !models.IsValidCampaignPlatform(platform) {
		return nil, NewBusinessError("PAGE_PRICE_PLATFORM_INVALID", "invalid platform", ErrCampaignPlatformInvalid)
	}
	if req.Price == 0 {
		return nil, NewBusinessError("PAGE_PRICE_INVALID", "price must be greater than zero", ErrPriceFactorInvalid)
	}

	row := &models.PagePrice{
		Platform: platform,
		Price:    req.Price,
	}
	if adminID, ok := ctx.Value(utils.AdminIDKey).(uint); ok && adminID > 0 {
		row.CreatedByAdminID = &adminID
	}
	if err := s.pagePriceRepo.Insert(ctx, row); err != nil {
		return nil, NewBusinessError("PAGE_PRICE_INSERT_FAILED", "failed to insert page price", err)
	}

	_ = createAuditLog(ctx, s.auditRepo, nil, models.AuditActionAdminUpdatePagePrice, "Admin updated page price", true, nil, nil)
	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminUpdatePagePrice, "Admin updated page price", true, nil, map[string]any{
		"platform": platform,
		"price":    req.Price,
	}, nil)

	return &dto.AdminUpdatePagePriceResponse{
		Message:   "Page price updated successfully",
		Platform:  row.Platform,
		Price:     row.Price,
		CreatedAt: row.CreatedAt,
	}, nil
}

func (s *AdminCampaignFlowImpl) GetPagePrices(ctx context.Context) (*dto.AdminGetPagePricesResponse, error) {
	rows, err := s.pagePriceRepo.ListLatest(ctx)
	if err != nil {
		return nil, NewBusinessError("PAGE_PRICE_LIST_FAILED", "failed to list page prices", err)
	}

	items := make([]dto.AdminPagePriceItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, dto.AdminPagePriceItem{
			Platform:  row.Platform,
			Price:     row.Price,
			CreatedAt: row.CreatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Platform < items[j].Platform
	})

	_ = createAuditLog(ctx, s.auditRepo, nil, models.AuditActionAdminGetPagePrices, "Admin listed page prices", true, nil, nil)
	logAdminAction(ctx, s.auditRepo, models.AuditActionAdminGetPagePrices, "Admin listed page prices", true, nil, map[string]any{
		"items": len(items),
	}, nil)

	return &dto.AdminGetPagePricesResponse{
		Message: "Page prices retrieved successfully",
		Items:   items,
	}, nil
}

func isAdminReschedulable(status models.CampaignStatus) bool {
	switch status {
	case models.CampaignStatusInitiated, models.CampaignStatusInProgress, models.CampaignStatusWaitingForApproval, models.CampaignStatusApproved:
		return true
	default:
		return false
	}
}

func isAdminMissedPendingApprovalCampaign(campaign *models.Campaign, nowUTC time.Time) bool {
	if campaign == nil || campaign.Status != models.CampaignStatusWaitingForApproval {
		return false
	}
	if campaign.Spec.ScheduleAt == nil || campaign.Spec.ScheduleAt.IsZero() {
		return false
	}
	return !campaign.Spec.ScheduleAt.UTC().After(nowUTC)
}

func isUTCInstant(t time.Time) bool {
	_, offset := t.Zone()
	return offset == 0
}

func tehranLocation() *time.Location {
	if adminTehranLoc == nil || adminTehranLoc.String() != "Asia/Tehran" {
		if loaded, err := time.LoadLocation("Asia/Tehran"); err == nil {
			adminTehranLoc = loaded
		} else {
			adminTehranLoc = time.FixedZone("Asia/Tehran", 3*3600+1800)
		}
	}
	return adminTehranLoc
}

func isWithinRescheduleWindow(tehranTime time.Time) bool {
	hour := tehranTime.Hour()
	if hour < 8 {
		return false
	}
	if hour > 21 {
		return false
	}
	if hour == 21 && (tehranTime.Minute() > 0 || tehranTime.Second() > 0 || tehranTime.Nanosecond() > 0) {
		return false
	}
	return true
}

func normalizeIranMobile(m string) string {
	if m == "" {
		return m
	}
	if strings.HasPrefix(m, "+") {
		return m[1:]
	}
	if strings.HasPrefix(m, "0") && len(m) == 11 {
		return "98" + m[1:]
	}
	return m
}
