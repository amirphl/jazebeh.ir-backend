package businessflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

type BundleFlow interface {
	CreateBundle(ctx context.Context, req *dto.CreateBundleRequest, metadata *ClientMetadata) (*dto.CreateBundleResponse, error)
	UpdateBundle(ctx context.Context, req *dto.UpdateBundleRequest, metadata *ClientMetadata) (*dto.UpdateBundleResponse, error)
	GetBundle(ctx context.Context, req *dto.GetBundleRequest, metadata *ClientMetadata) (*dto.GetBundleResponse, error)
	ListBundles(ctx context.Context, req *dto.ListBundlesRequest, metadata *ClientMetadata) (*dto.ListBundlesResponse, error)
}

type BundleFlowImpl struct {
	bundleRepo         repository.BundleRepository
	campaignRepo       repository.CampaignRepository
	customerRepo       repository.CustomerRepository
	auditRepo          repository.AuditLogRepository
	evaluationReadRepo repository.BundleTagEvaluationReadRepository
	db                 *gorm.DB
}

func NewBundleFlow(
	bundleRepo repository.BundleRepository,
	campaignRepo repository.CampaignRepository,
	customerRepo repository.CustomerRepository,
	auditRepo repository.AuditLogRepository,
	evaluationReadRepo repository.BundleTagEvaluationReadRepository,
	db *gorm.DB,
) BundleFlow {
	return &BundleFlowImpl{
		bundleRepo:         bundleRepo,
		campaignRepo:       campaignRepo,
		customerRepo:       customerRepo,
		auditRepo:          auditRepo,
		evaluationReadRepo: evaluationReadRepo,
		db:                 db,
	}
}

func (f *BundleFlowImpl) CreateBundle(ctx context.Context, req *dto.CreateBundleRequest, metadata *ClientMetadata) (*dto.CreateBundleResponse, error) {
	if req == nil {
		return nil, NewBusinessError("CREATE_BUNDLE_FAILED", "Failed to create bundle", ErrInvalidState)
	}

	customer, err := getCustomer(ctx, f.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_FAILED", "Failed to create bundle", err)
	}

	bundleInput, err := validateBundleInput(customer, req.Title, req.Objective, req.TargetAudiencePersona, req.ShortLinkDomain, req.Description, req.AdLink, req.TargetCustomerName, req.Category, req.Job)
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	now := utils.UTCNow()
	row := bundleInput.newBundle(customer.ID, now)

	if err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		if err := f.bundleRepo.Save(txCtx, row); err != nil {
			return err
		}

		msg := fmt.Sprintf("Bundle created successfully: id=%d title=%q", row.ID, row.Title)
		if err := createAuditLog(txCtx, f.auditRepo, &customer, models.AuditActionBundleCreated, msg, true, nil, metadata); err != nil {
			return err
		}

		return nil
	}); err != nil {
		errMsg := fmt.Sprintf("Bundle creation failed for customer %d title=%q: %s", customer.ID, bundleInput.title, err.Error())
		_ = createAuditLog(ctx, f.auditRepo, &customer, models.AuditActionBundleCreationFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("CREATE_BUNDLE_FAILED", "Failed to create bundle", err)
	}

	return &dto.CreateBundleResponse{
		Message:   "Bundle created successfully",
		ID:        row.ID,
		CreatedAt: row.CreatedAt,
	}, nil
}

func (f *BundleFlowImpl) UpdateBundle(ctx context.Context, req *dto.UpdateBundleRequest, metadata *ClientMetadata) (*dto.UpdateBundleResponse, error) {
	if req == nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_FAILED", "Failed to update bundle", ErrInvalidState)
	}

	customer, err := getCustomer(ctx, f.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_FAILED", "Failed to update bundle", err)
	}

	bundleInput, err := validateBundleInput(customer, req.Title, req.Objective, req.TargetAudiencePersona, req.ShortLinkDomain, req.Description, req.AdLink, req.TargetCustomerName, req.Category, req.Job)
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	row, err := f.bundleRepo.ByID(ctx, req.ID)
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_FAILED", "Failed to update bundle", err)
	}
	if row == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if row.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}

	bundleInput.applyTo(row)

	if err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		if err := f.bundleRepo.Update(txCtx, row); err != nil {
			return err
		}

		msg := fmt.Sprintf("Bundle updated successfully: id=%d title=%q", row.ID, row.Title)
		if err := createAuditLog(txCtx, f.auditRepo, &customer, models.AuditActionBundleUpdated, msg, true, nil, metadata); err != nil {
			return err
		}

		return nil
	}); err != nil {
		errMsg := fmt.Sprintf("Bundle update failed for customer %d bundle %d title=%q: %s", customer.ID, req.ID, bundleInput.title, err.Error())
		_ = createAuditLog(ctx, f.auditRepo, &customer, models.AuditActionBundleUpdateFailed, errMsg, false, &errMsg, metadata)
		return nil, NewBusinessError("UPDATE_BUNDLE_FAILED", "Failed to update bundle", err)
	}

	return &dto.UpdateBundleResponse{
		Message:   "Bundle updated successfully",
		ID:        row.ID,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (f *BundleFlowImpl) GetBundle(ctx context.Context, req *dto.GetBundleRequest, metadata *ClientMetadata) (*dto.GetBundleResponse, error) {
	if req == nil {
		return nil, NewBusinessError("GET_BUNDLE_FAILED", "Failed to get bundle", ErrInvalidState)
	}

	if _, err := getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("GET_BUNDLE_FAILED", "Failed to get bundle", err)
	}

	row, err := f.bundleRepo.ByID(ctx, req.ID)
	if err != nil {
		return nil, NewBusinessError("GET_BUNDLE_FAILED", "Failed to get bundle", err)
	}
	if row == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if row.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}

	item := bundleItemFromModel(row)
	if err := f.injectBundleStatistics(ctx, req.CustomerID, []*dto.BundleItem{item}); err != nil {
		return nil, NewBusinessError("GET_BUNDLE_FAILED", "Failed to get bundle", err)
	}
	if err := f.injectBundleEvaluationStatus(ctx, []*dto.BundleItem{item}); err != nil {
		return nil, NewBusinessError("GET_BUNDLE_FAILED", "Failed to get bundle", err)
	}

	return &dto.GetBundleResponse{
		Message: "Bundle retrieved successfully",
		Item:    item,
	}, nil
}

func (f *BundleFlowImpl) ListBundles(ctx context.Context, req *dto.ListBundlesRequest, metadata *ClientMetadata) (*dto.ListBundlesResponse, error) {
	if req == nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", ErrInvalidState)
	}

	if _, err := getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", err)
	}

	page := max(1, req.Page)
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	filter := models.BundleFilter{
		CustomerID: &req.CustomerID,
	}
	if req.Filter != nil {
		if req.Filter.Title != nil {
			trimmed := strings.TrimSpace(*req.Filter.Title)
			if trimmed != "" {
				filter.Title = &trimmed
			}
		}
		if req.Filter.TargetCustomerName != nil {
			trimmed := strings.TrimSpace(*req.Filter.TargetCustomerName)
			if trimmed != "" {
				filter.TargetCustomerName = &trimmed
			}
		}
	}

	total64, err := f.bundleRepo.Count(ctx, filter)
	if err != nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", err)
	}

	rows, err := f.bundleRepo.ByFilter(ctx, filter, "updated_at DESC, id DESC", limit, offset)
	if err != nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", err)
	}

	items := make([]dto.BundleItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, *bundleItemFromModel(row))
	}
	if err := f.injectBundleStatistics(ctx, req.CustomerID, bundleItemsToPointers(items)); err != nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", err)
	}
	if err := f.injectBundleEvaluationStatus(ctx, bundleItemsToPointers(items)); err != nil {
		return nil, NewBusinessError("LIST_BUNDLES_FAILED", "Failed to list bundles", err)
	}

	totalPages := int((total64 + int64(limit) - 1) / int64(limit))

	return &dto.ListBundlesResponse{
		Message: "Bundles retrieved successfully",
		Items:   items,
		Pagination: dto.PaginationInfo{
			Total:      total64,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}, nil
}

func bundleItemFromModel(bundle *models.Bundle) *dto.BundleItem {
	item := &dto.BundleItem{
		ID:                    bundle.ID,
		Title:                 bundle.Title,
		Objective:             bundle.Objective,
		TargetAudiencePersona: bundle.TargetAudiencePersona,
		AdLink:                bundle.Adlink,
		Description:           bundle.Description,
		ShortLinkDomain:       bundle.ShortLinkDomain,
		TargetCustomerName:    bundle.TargetCustomerName,
		Category:              bundle.Category,
		Job:                   bundle.Job,
		CustomerID:            bundle.CustomerID,
		CreatedAt:             bundle.CreatedAt,
		UpdatedAt:             bundle.UpdatedAt,
	}
	if len(bundle.Statistics) > 0 {
		_ = json.Unmarshal(bundle.Statistics, &item.Statistics)
	}
	return item
}

type bundleInput struct {
	title                 string
	objective             string
	targetAudiencePersona string
	adLink                *string
	description           *string
	shortLinkDomain       *string
	targetCustomerName    *string
	category              *string
	job                   *string
}

func validateBundleInput(
	customer models.Customer,
	title, objective, targetAudiencePersona string,
	shortLinkDomain, description, adLink, targetCustomerName, category, job *string,
) (bundleInput, error) {
	validatedTitle, err := validateRequiredBundleField(title, 255, "title")
	if err != nil {
		return bundleInput{}, err
	}
	validatedObjective, err := validateRequiredBundleField(objective, 1023, "objective")
	if err != nil {
		return bundleInput{}, err
	}
	validatedPersona, err := validateRequiredBundleField(targetAudiencePersona, 1023, "target audience persona")
	if err != nil {
		return bundleInput{}, err
	}
	validatedShortLinkDomain, err := sanitizeShortLinkDomain(shortLinkDomain)
	if err != nil {
		return bundleInput{}, err
	}
	validatedDescription, err := sanitizeOptionalBundleField(description, 2047, "description")
	if err != nil {
		return bundleInput{}, err
	}
	validatedAdLink, err := sanitizeOptionalBundleField(adLink, 2047, "adlink")
	if err != nil {
		return bundleInput{}, err
	}
	validatedTargetCustomerName, err := sanitizeOptionalBundleField(targetCustomerName, 255, "target customer name")
	if err != nil {
		return bundleInput{}, err
	}
	validatedCategory, validatedJob, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, category, job, false)
	if err != nil {
		return bundleInput{}, err
	}

	return bundleInput{
		title:                 validatedTitle,
		objective:             validatedObjective,
		targetAudiencePersona: validatedPersona,
		adLink:                validatedAdLink,
		description:           validatedDescription,
		shortLinkDomain:       validatedShortLinkDomain,
		targetCustomerName:    validatedTargetCustomerName,
		category:              validatedCategory,
		job:                   validatedJob,
	}, nil
}

func (input bundleInput) newBundle(customerID uint, now time.Time) *models.Bundle {
	bundle := &models.Bundle{
		Metadata:   json.RawMessage(`{}`),
		Statistics: json.RawMessage(`{}`),
		CustomerID: customerID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	input.applyTo(bundle)
	return bundle
}

func (input bundleInput) applyTo(bundle *models.Bundle) {
	bundle.Title = input.title
	bundle.Objective = input.objective
	bundle.TargetAudiencePersona = input.targetAudiencePersona
	bundle.Adlink = input.adLink
	bundle.Description = input.description
	bundle.ShortLinkDomain = input.shortLinkDomain
	bundle.TargetCustomerName = input.targetCustomerName
	bundle.Category = input.category
	bundle.Job = input.job
}

func validateRequiredBundleField(value string, maxLen int, fieldName string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	if len([]rune(trimmed)) > maxLen {
		return "", fmt.Errorf("%s must be <= %d characters", fieldName, maxLen)
	}
	return trimmed, nil
}

func sanitizeOptionalBundleField(value *string, maxLen int, fieldName string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if len([]rune(trimmed)) > maxLen {
		return nil, fmt.Errorf("%s must be <= %d characters", fieldName, maxLen)
	}
	return utils.ToPtr(trimmed), nil
}

type bundleStatisticsAggregate struct {
	aggregatedTotalRecords       uint64
	aggregatedTotalSent          uint64
	aggregatedTotalClicks        int64
	totalCampaignsPhaseTest      int
	totalCampaignsPhaseExecution int
	totalCampaigns               int
}

func bundleItemsToPointers(items []dto.BundleItem) []*dto.BundleItem {
	out := make([]*dto.BundleItem, 0, len(items))
	for i := range items {
		out = append(out, &items[i])
	}
	return out
}

func (f *BundleFlowImpl) injectBundleStatistics(ctx context.Context, customerID uint, items []*dto.BundleItem) error {
	if len(items) == 0 {
		return nil
	}

	bundleIDs := make([]uint, 0, len(items))
	for _, item := range items {
		if item != nil {
			bundleIDs = append(bundleIDs, item.ID)
		}
	}
	if len(bundleIDs) == 0 {
		return nil
	}

	campaigns, err := f.campaignRepo.ByCustomerIDAndBundleIDs(ctx, customerID, bundleIDs)
	if err != nil {
		return err
	}

	aggregates, err := f.aggregateBundleStatistics(ctx, campaigns)
	if err != nil {
		return err
	}

	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Statistics == nil {
			item.Statistics = map[string]any{}
		}

		agg := aggregates[item.ID]
		item.Statistics["aggregatedTotalRecords"] = agg.aggregatedTotalRecords
		item.Statistics["aggregatedTotalSent"] = agg.aggregatedTotalSent
		// Live click totals are intentionally not calculated while listing
		// bundles. Calculating them here requires an unbounded aggregate over
		// short_link_clicks for every historical campaign in the page and can
		// exceed the request deadline. Leave an existing persisted value intact
		// rather than replacing it with an inaccurate zero.
		// item.Statistics["aggregatedTotalClicks"] = agg.aggregatedTotalClicks
		item.Statistics["totalCampaignsPhaseTest"] = agg.totalCampaignsPhaseTest
		item.Statistics["totalCampaignsPhaseExecution"] = agg.totalCampaignsPhaseExecution
		item.Statistics["totalCampaigns"] = agg.totalCampaigns
	}

	return nil
}

func (f *BundleFlowImpl) aggregateBundleStatistics(ctx context.Context, campaigns []*models.Campaign) (map[uint]bundleStatisticsAggregate, error) {
	out := make(map[uint]bundleStatisticsAggregate)
	if len(campaigns) == 0 {
		return out, nil
	}

	// Do not aggregate short_link_clicks during a bundle list/detail request.
	// A bundle can contain an unbounded campaign history, so this otherwise
	// produces a large COUNT(DISTINCT uid) query and blocks the endpoint.
	//
	// campaignIDs := make([]uint, 0, len(campaigns))
	// for _, campaign := range campaigns {
	// 	if campaign != nil {
	// 		campaignIDs = append(campaignIDs, campaign.ID)
	// 	}
	// }
	// clickCounts, err := f.campaignRepo.AggregateClickCountsByCampaignIDs(ctx, campaignIDs)
	// if err != nil {
	// 	return nil, err
	// }

	for _, campaign := range campaigns {
		if campaign == nil || campaign.BundleID == nil {
			continue
		}
		agg := out[*campaign.BundleID]
		agg.totalCampaigns++

		switch campaign.Phase {
		case models.CampaignPhaseTest:
			agg.totalCampaignsPhaseTest++
		case models.CampaignPhaseExecution:
			agg.totalCampaignsPhaseExecution++
		}

		statsMap := unmarshalStatisticsMap(campaign.Statistics)
		agg.aggregatedTotalRecords += parseUint64Stat(statsMap, "aggregatedTotalRecords")
		agg.aggregatedTotalSent += parseUint64Stat(statsMap, "aggregatedTotalSent")
		// See the disabled live aggregation above. Keep persisted bundle click
		// totals untouched until reporting is moved to a bounded/cached path.
		// agg.aggregatedTotalClicks += clickCounts[campaign.ID]

		out[*campaign.BundleID] = agg
	}

	return out, nil
}

func (f *BundleFlowImpl) injectBundleEvaluationStatus(ctx context.Context, items []*dto.BundleItem) error {
	if f.evaluationReadRepo == nil || len(items) == 0 {
		return nil
	}

	bundleIDs := make([]uint, 0, len(items))
	for _, item := range items {
		if item != nil {
			bundleIDs = append(bundleIDs, item.ID)
		}
	}
	if len(bundleIDs) == 0 {
		return nil
	}

	rows, err := f.evaluationReadRepo.ListByBundleIDs(ctx, bundleIDs)
	if err != nil {
		return err
	}
	byBundleID := make(map[uint]*models.CurrentBundleTagEvaluationStatus, len(rows))
	for _, row := range rows {
		if row != nil {
			byBundleID[row.BundleID] = row
		}
	}

	for _, item := range items {
		if item == nil {
			continue
		}
		if row, ok := byBundleID[item.ID]; ok {
			status := row.Status
			item.TagEvaluationStatus = &status
			item.TagEvaluatedAt = row.LatestCompletedAt
		} else {
			status := models.BundleTagEvaluationStatusNotEvaluated
			item.TagEvaluationStatus = &status
		}
	}

	return nil
}

func unmarshalStatisticsMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var statsMap map[string]any
	_ = json.Unmarshal(raw, &statsMap)
	return statsMap
}

func parseUint64Stat(statsMap map[string]any, key string) uint64 {
	if statsMap == nil {
		return 0
	}
	v, ok := statsMap[key]
	if !ok {
		return 0
	}

	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case float32:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case int:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case int64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case uint:
		return uint64(n)
	case uint64:
		return n
	case json.Number:
		f, err := n.Float64()
		if err != nil || f < 0 {
			return 0
		}
		return uint64(f)
	default:
		return 0
	}
}
