package businessflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	title, err := validateRequiredBundleField(req.Title, 255, "title")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	objective, err := validateRequiredBundleField(req.Objective, 1023, "objective")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	targetAudiencePersona, err := validateRequiredBundleField(req.TargetAudiencePersona, 1023, "target audience persona")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	shortLinkDomain, err := sanitizeShortLinkDomain(req.ShortLinkDomain)
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	description, err := sanitizeOptionalBundleField(req.Description, 2047, "description")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	adLink, err := sanitizeOptionalBundleField(req.AdLink, 2047, "adlink")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	targetCustomerName, err := sanitizeOptionalBundleField(req.TargetCustomerName, 255, "target customer name")
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	category, job, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, req.Category, req.Job, false)
	if err != nil {
		return nil, NewBusinessError("CREATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}

	now := utils.UTCNow()
	row := &models.Bundle{
		Title:                 title,
		Objective:             objective,
		TargetAudiencePersona: targetAudiencePersona,
		Adlink:                adLink,
		Description:           description,
		ShortLinkDomain:       shortLinkDomain,
		TargetCustomerName:    targetCustomerName,
		Category:              category,
		Job:                   job,
		Metadata:              json.RawMessage(`{}`),
		Statistics:            json.RawMessage(`{}`),
		CustomerID:            customer.ID,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

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
		errMsg := fmt.Sprintf("Bundle creation failed for customer %d title=%q: %s", customer.ID, title, err.Error())
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

	title, err := validateRequiredBundleField(req.Title, 255, "title")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	objective, err := validateRequiredBundleField(req.Objective, 1023, "objective")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	targetAudiencePersona, err := validateRequiredBundleField(req.TargetAudiencePersona, 1023, "target audience persona")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	shortLinkDomain, err := sanitizeShortLinkDomain(req.ShortLinkDomain)
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	description, err := sanitizeOptionalBundleField(req.Description, 2047, "description")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	adLink, err := sanitizeOptionalBundleField(req.AdLink, 2047, "adlink")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	targetCustomerName, err := sanitizeOptionalBundleField(req.TargetCustomerName, 255, "target customer name")
	if err != nil {
		return nil, NewBusinessError("UPDATE_BUNDLE_VALIDATION_FAILED", "Bundle validation failed", err)
	}
	category, job, err := sanitizeCategoryAndJob(customer.AccountType.TypeName, req.Category, req.Job, false)
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

	row.Title = title
	row.Objective = objective
	row.TargetAudiencePersona = targetAudiencePersona
	row.Adlink = adLink
	row.Description = description
	row.ShortLinkDomain = shortLinkDomain
	row.TargetCustomerName = targetCustomerName
	row.Category = category
	row.Job = job

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
		errMsg := fmt.Sprintf("Bundle update failed for customer %d bundle %d title=%q: %s", customer.ID, req.ID, title, err.Error())
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

	item := &dto.BundleItem{
		ID:                    row.ID,
		Title:                 row.Title,
		Objective:             row.Objective,
		TargetAudiencePersona: row.TargetAudiencePersona,
		AdLink:                row.Adlink,
		Description:           row.Description,
		ShortLinkDomain:       row.ShortLinkDomain,
		TargetCustomerName:    row.TargetCustomerName,
		Category:              row.Category,
		Job:                   row.Job,
		CustomerID:            row.CustomerID,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}

	// if len(row.Metadata) > 0 {
	// 	_ = json.Unmarshal(row.Metadata, &item.Metadata)
	// }
	if len(row.Statistics) > 0 {
		_ = json.Unmarshal(row.Statistics, &item.Statistics)
	}
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
		item := dto.BundleItem{
			ID:                    row.ID,
			Title:                 row.Title,
			Objective:             row.Objective,
			TargetAudiencePersona: row.TargetAudiencePersona,
			AdLink:                row.Adlink,
			Description:           row.Description,
			ShortLinkDomain:       row.ShortLinkDomain,
			TargetCustomerName:    row.TargetCustomerName,
			Category:              row.Category,
			Job:                   row.Job,
			CustomerID:            row.CustomerID,
			CreatedAt:             row.CreatedAt,
			UpdatedAt:             row.UpdatedAt,
		}

		// if len(row.Metadata) > 0 {
		// 	_ = json.Unmarshal(row.Metadata, &item.Metadata)
		// }
		if len(row.Statistics) > 0 {
			_ = json.Unmarshal(row.Statistics, &item.Statistics)
		}

		items = append(items, item)
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
