package businessflow

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const maxSmartTargetingSelections = 10000

// SmartTargetingFlow owns customer-facing Smart Targeting reads and selection
// mutations. Implementations must enforce campaign/bundle ownership and treat
// every selection replacement as a complete, atomic set replacement.
type SmartTargetingFlow interface {
	ListTags(ctx context.Context, req *dto.ListSmartTargetingTagsRequest) (*dto.ListSmartTargetingTagsResponse, error)
	ListBundleTags(ctx context.Context, req *dto.ListSmartTargetingTagsRequest) (*dto.ListSmartTargetingTagsResponse, error)
	GetSelection(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingSelectionResponse, error)
	ReplaceSelection(ctx context.Context, req *dto.ReplaceSmartTargetingSelectionRequest) (*dto.SmartTargetingSelectionResponse, error)
	AutoSelect(ctx context.Context, req *dto.AutoSelectSmartTargetingTagsRequest) (*dto.SmartTargetingSelectionResponse, error)
}

// SmartTargetingFlowImpl coordinates ownership checks, evaluation-aware
// sorting, and transactional selection persistence.
type SmartTargetingFlowImpl struct {
	campaignRepo   repository.CampaignRepository
	bundleRepo     repository.BundleRepository
	selectionRepo  repository.CampaignSelectedTagRepository
	evaluationRepo repository.BundleTagEvaluationReadRepository
	db             *gorm.DB
}

// NewSmartTargetingFlow constructs the Smart Targeting business flow.
func NewSmartTargetingFlow(campaignRepo repository.CampaignRepository, bundleRepo repository.BundleRepository, selectionRepo repository.CampaignSelectedTagRepository, evaluationRepo repository.BundleTagEvaluationReadRepository, db *gorm.DB) SmartTargetingFlow {
	return &SmartTargetingFlowImpl{campaignRepo: campaignRepo, bundleRepo: bundleRepo, selectionRepo: selectionRepo, evaluationRepo: evaluationRepo, db: db}
}

// ownedCampaign resolves a campaign UUID and enforces that it belongs to the
// authenticated customer and has a bundle. Keeping this check in one place
// prevents selection endpoints from leaking cross-customer campaign data.
func (s *SmartTargetingFlowImpl) ownedCampaign(ctx context.Context, customerID uint, campaignUUID string) (*models.Campaign, error) {
	if customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "Customer ID is required", ErrCustomerNotFound)
	}
	campaignUUID = strings.TrimSpace(campaignUUID)
	if campaignUUID == "" {
		return nil, NewBusinessError("INVALID_CAMPAIGN_UUID", "Campaign UUID is required", ErrCampaignUUIDRequired)
	}
	if _, err := uuid.Parse(campaignUUID); err != nil {
		return nil, NewBusinessError("INVALID_CAMPAIGN_UUID", "Campaign UUID is invalid", ErrCampaignUUIDInvalid)
	}
	campaign, err := s.campaignRepo.ByUUID(ctx, campaignUUID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	if campaign == nil {
		return nil, NewBusinessError("CAMPAIGN_NOT_FOUND", "Campaign not found", ErrCampaignNotFound)
	}
	if campaign.CustomerID != customerID {
		return nil, NewBusinessError("CAMPAIGN_ACCESS_DENIED", "Campaign access denied", ErrCampaignAccessDenied)
	}
	if campaign.BundleID == nil || *campaign.BundleID == 0 {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Campaign bundle not found", ErrBundleNotFound)
	}
	return campaign, nil
}

// evaluationAvailable reports whether current_bundle_tag_scores contains at
// least one row for the bundle. A successful run with no score rows is treated
// as unevaluated by Smart Targeting, allowing repository reads to fall back to
// the active live tag catalog.
func (s *SmartTargetingFlowImpl) evaluationAvailable(ctx context.Context, bundleID uint) (bool, error) {
	count, err := s.evaluationRepo.CountCurrentScoresByBundleID(ctx, bundleID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// normalizeSmartTargetingQuery validates user-controlled search/sort input and
// resolves deterministic defaults:
//   - Execution Campaigns default to Test CTR, then persona-fit score;
//   - other evaluated bundles default to persona-fit score descending;
//   - unevaluated bundles default to database/tag ID order ascending.
//
// database_order is internal-only, while persona-fit sorting is rejected when
// the bundle has no current score rows.
func normalizeSmartTargetingQuery(search, sortBy, direction string, evaluationAvailable, executionPhase bool) (string, string, string, error) {
	search = strings.TrimSpace(search)
	if utf8.RuneCountInString(search) > 200 {
		return "", "", "", ErrSmartTargetingSearchTooLong
	}
	sortBy = strings.TrimSpace(strings.ToLower(sortBy))
	direction = strings.TrimSpace(strings.ToLower(direction))
	defaultSort := sortBy == ""
	if sortBy == "" {
		if executionPhase {
			sortBy = "execution_default"
			direction = "desc"
		} else if evaluationAvailable {
			sortBy = "bundle_persona_fit_score"
			direction = "desc"
		} else {
			sortBy = "database_order"
			direction = "asc"
		}
	}
	switch sortBy {
	case "tag_capacity", "test_phase_avg_ctr", "overall_avg_ctr":
	case "execution_default":
		if !defaultSort || !executionPhase {
			return "", "", "", ErrSmartTargetingSortInvalid
		}
	case "bundle_persona_fit_score":
		if !evaluationAvailable {
			return "", "", "", ErrSmartTargetingScoreUnavailable
		}
	case "database_order":
		if !defaultSort {
			return "", "", "", ErrSmartTargetingSortInvalid
		}
	default:
		return "", "", "", ErrSmartTargetingSortInvalid
	}
	if direction == "" {
		direction = "desc"
	}
	if direction != "asc" && direction != "desc" {
		return "", "", "", ErrSmartTargetingSortInvalid
	}
	return search, sortBy, direction, nil
}

// smartTargetingPageOffset calculates a repository offset without allowing a
// large, otherwise-valid page number to wrap into a negative value. Keeping
// this check in the flow protects non-HTTP callers as well as handlers.
func smartTargetingPageOffset(page, pageSize int) (int, error) {
	if page < 1 || pageSize < 1 || pageSize > 100 || page-1 > math.MaxInt/pageSize {
		return 0, ErrInvalidPage
	}
	return (page - 1) * pageSize, nil
}

// ListTags returns one page of tags for an owned campaign plus the complete
// persisted selection and summary. SelectedTagIDs intentionally spans all
// pages so clients can paginate without discarding off-page selections.
func (s *SmartTargetingFlowImpl) ListTags(ctx context.Context, req *dto.ListSmartTargetingTagsRequest) (*dto.ListSmartTargetingTagsResponse, error) {
	if req == nil || req.Page < 1 || req.PageSize < 1 || req.PageSize > 100 {
		return nil, NewBusinessError("INVALID_PAGINATION", "Page must be at least 1 and page_size must be between 1 and 100", ErrInvalidPage)
	}
	offset, err := smartTargetingPageOffset(req.Page, req.PageSize)
	if err != nil {
		return nil, NewBusinessError("INVALID_PAGINATION", "Page must be at least 1 and page_size must be between 1 and 100", err)
	}
	campaign, err := s.ownedCampaign(ctx, req.CustomerID, req.CampaignUUID)
	if err != nil {
		return nil, err
	}
	evaluated, err := s.evaluationAvailable(ctx, *campaign.BundleID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_EVALUATION_LOOKUP_FAILED", "Failed to lookup bundle evaluation", err)
	}
	search, sortBy, direction, err := normalizeSmartTargetingQuery(
		req.Search,
		req.SortBy,
		req.SortDirection,
		evaluated,
		campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseExecution,
	)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_QUERY_INVALID", err.Error(), err)
	}
	rows, total, err := s.selectionRepo.ListAvailable(ctx, *campaign.BundleID, campaign.ID, search, sortBy, direction, req.PageSize, offset)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_TAG_LIST_FAILED", "Failed to list smart targeting tags", err)
	}
	selected, err := s.selectionRepo.ListSelected(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_LOOKUP_FAILED", "Failed to load selected tags", err)
	}
	summary, err := s.selectionRepo.Summary(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_LOOKUP_FAILED", "Failed to load selection summary", err)
	}

	items := make([]dto.SmartTargetingTagItem, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			items = append(items, smartTargetingTagItem(row))
		}
	}
	selectedIDs := make([]uint, 0, len(selected))
	for _, item := range selected {
		selectedIDs = append(selectedIDs, item.TagID)
	}
	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(req.PageSize)))
	}
	return &dto.ListSmartTargetingTagsResponse{
		Items: items, SelectedTagIDs: selectedIDs, EvaluationAvailable: evaluated,
		EffectiveSortBy: sortBy, EffectiveSortDirection: direction,
		Pagination: dto.PaginationInfo{Total: total, Page: req.Page, Limit: req.PageSize, TotalPages: totalPages},
		Summary:    dto.SmartTargetingSelectionSummary{SelectedTagCount: summary.SelectedTagCount, SelectedRawCapacity: summary.SelectedRawCapacity},
	}, nil
}

// ListBundleTags returns the selectable tag table before a campaign exists.
// Ownership is checked against the bundle. Selection state and summary are
// explicitly empty because selected IDs are submitted atomically when the
// campaign is created.
func (s *SmartTargetingFlowImpl) ListBundleTags(ctx context.Context, req *dto.ListSmartTargetingTagsRequest) (*dto.ListSmartTargetingTagsResponse, error) {
	if req == nil || req.Page < 1 || req.PageSize < 1 || req.PageSize > 100 {
		return nil, NewBusinessError("INVALID_PAGINATION", "Page must be at least 1 and page_size must be between 1 and 100", ErrInvalidPage)
	}
	offset, err := smartTargetingPageOffset(req.Page, req.PageSize)
	if err != nil {
		return nil, NewBusinessError("INVALID_PAGINATION", "Page must be at least 1 and page_size must be between 1 and 100", err)
	}
	if req.CustomerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "Customer ID is required", ErrCustomerNotFound)
	}
	bundle, err := s.bundleRepo.ByID(ctx, req.BundleID)
	if err != nil {
		return nil, NewBusinessError("BUNDLE_LOOKUP_FAILED", "Failed to lookup bundle", err)
	}
	if bundle == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if bundle.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}
	evaluated, err := s.evaluationAvailable(ctx, bundle.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_EVALUATION_LOOKUP_FAILED", "Failed to lookup bundle evaluation", err)
	}
	search, sortBy, direction, err := normalizeSmartTargetingQuery(req.Search, req.SortBy, req.SortDirection, evaluated, false)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_QUERY_INVALID", err.Error(), err)
	}
	rows, total, err := s.selectionRepo.ListAvailable(ctx, bundle.ID, 0, search, sortBy, direction, req.PageSize, offset)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_TAG_LIST_FAILED", "Failed to list smart targeting tags", err)
	}
	items := make([]dto.SmartTargetingTagItem, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			items = append(items, smartTargetingTagItem(row))
		}
	}
	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(req.PageSize)))
	}
	return &dto.ListSmartTargetingTagsResponse{
		Items: items, SelectedTagIDs: []uint{}, EvaluationAvailable: evaluated,
		EffectiveSortBy: sortBy, EffectiveSortDirection: direction,
		Pagination: dto.PaginationInfo{Total: total, Page: req.Page, Limit: req.PageSize, TotalPages: totalPages},
		Summary: dto.SmartTargetingSelectionSummary{
			SelectedTagCount:    0,
			SelectedRawCapacity: 0,
		},
	}, nil
}

// smartTargetingTagItem maps every read-model field into its API counterpart.
// CTR pointers remain nil when the repository reports that metrics are not
// available; this is distinct from a measured zero CTR.
func smartTargetingTagItem(row *models.SmartTargetingTagRow) dto.SmartTargetingTagItem {
	return dto.SmartTargetingTagItem{
		TagID: row.TagID,
		// TagName:               row.TagName,
		TagDisplayTitle: row.TagDisplayTitle,
		// TagAudiencePersona:    row.TagAudiencePersona,
		TagCapacity:           row.TagAudienceCount,
		BundlePersonaFitScore: row.BundlePersonaFitScore,
		EvaluationRunID:       row.EvaluationRunID,
		FitLevel:              row.FitLevel,
		RelationType:          row.RelationType,
		// Reason:                row.Reason,
		TestPhaseAvgCTR:         row.TestPhaseAvgCTR,
		TotalTestSelectedCount:  row.TotalTestSelectedCount,
		TotalTestSentCount:      row.TotalTestSentCount,
		TotalTestDeliveredCount: row.TotalTestDeliveredCount,
		TotalTestClickCount:     row.TotalTestClickCount,
		SelectedCount:           row.SelectedCount,
		SentCount:               row.SentCount,
		DeliveredCount:          row.DeliveredCount,
		ClickCount:              row.ClickCount,
		TestCampaignCTR:         row.TestCampaignCTR,
		OverallAvgCTR:           row.OverallAvgCTR,
		Selected:                row.Selected,
	}
}

// selectionResponse converts the complete persisted selection and aggregate
// capacity into the stable API response shape.
func selectionResponse(selected []*models.CampaignSelectedTag, summary *models.CampaignSelectedTagSummary) *dto.SmartTargetingSelectionResponse {
	ids := make([]uint, 0, len(selected))
	for _, item := range selected {
		ids = append(ids, item.TagID)
	}
	return &dto.SmartTargetingSelectionResponse{SelectedTagIDs: ids, Summary: dto.SmartTargetingSelectionSummary{SelectedTagCount: summary.SelectedTagCount, SelectedRawCapacity: summary.SelectedRawCapacity}}
}

// GetSelection returns the complete selection for an owned campaign. It is
// independent of list pagination and is safe to use when restoring UI state.
func (s *SmartTargetingFlowImpl) GetSelection(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingSelectionResponse, error) {
	campaign, err := s.ownedCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	selected, err := s.selectionRepo.ListSelected(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_LOOKUP_FAILED", "Failed to load selected tags", err)
	}
	summary, err := s.selectionRepo.Summary(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_LOOKUP_FAILED", "Failed to load selection summary", err)
	}
	return selectionResponse(selected, summary), nil
}

// normalizeSelectedTagIDs enforces the selection cardinality and ID invariants,
// rejects duplicates, and returns a copy in the exact caller-supplied order.
func normalizeSelectedTagIDs(ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return nil, ErrSmartTargetingTagsRequired
	}
	if len(ids) > maxSmartTargetingSelections {
		return nil, ErrSmartTargetingCountInvalid
	}
	normalized := append([]uint(nil), ids...)
	seen := make(map[uint]struct{}, len(normalized))
	for _, id := range normalized {
		if id == 0 {
			return nil, ErrSmartTargetingTagInvalid
		}
		if _, exists := seen[id]; exists {
			return nil, ErrSmartTargetingTagInvalid
		}
		seen[id] = struct{}{}
	}
	return normalized, nil
}

// replace validates campaign editability and Smart Targeting mode, then
// replaces the complete selected-tag set in one database transaction. The
// repository revalidates bundle membership and tag availability against the
// same snapshot-first source while holding the campaign row lock, closing the
// race between validation and persistence.
func (s *SmartTargetingFlowImpl) replace(ctx context.Context, campaign *models.Campaign, customerID uint, ids []uint) (*dto.SmartTargetingSelectionResponse, error) {
	if !campaign.IsEditable() {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Campaign cannot be updated in current status", ErrCampaignUpdateNotAllowed)
	}
	if !campaign.Spec.UsesSmartTargeting() {
		return nil, NewBusinessError("SMART_TARGETING_NOT_ENABLED", "Campaign does not use Smart Targeting", ErrCampaignAudienceTargetingMethodInvalid)
	}
	normalized, err := normalizeSelectedTagIDs(ids)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", err.Error(), err)
	}
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		// Capacity calculation requests take the same lock before snapshotting
		// selections. This gives tag replacement and exact-capacity generations a
		// single serialization point and prevents a mixed selection snapshot.
		if err := smartTargetingDB(txCtx, s.db).Exec("SELECT id FROM campaigns WHERE id = ? FOR UPDATE", campaign.ID).Error; err != nil {
			return err
		}
		return s.selectionRepo.Replace(txCtx, campaign.ID, *campaign.BundleID, customerID, normalized)
	})
	if err != nil {
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", ErrSmartTargetingTagInvalid.Error(), ErrSmartTargetingTagInvalid)
		}
		if errors.Is(err, repository.ErrCampaignSelectedTagsNotEditable) {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Campaign cannot be updated in current status", ErrCampaignUpdateNotAllowed)
		}
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_SAVE_FAILED", "Failed to save selected tags", err)
	}
	return s.GetSelection(ctx, customerID, campaign.UUID.String())
}

// ReplaceSelection replaces the complete selection supplied by the customer.
// An empty selection is invalid because Smart Targeting campaigns must always
// have at least one selected tag.
func (s *SmartTargetingFlowImpl) ReplaceSelection(ctx context.Context, req *dto.ReplaceSmartTargetingSelectionRequest) (*dto.SmartTargetingSelectionResponse, error) {
	if req == nil {
		return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", ErrSmartTargetingTagsRequired.Error(), ErrSmartTargetingTagsRequired)
	}
	campaign, err := s.ownedCampaign(ctx, req.CustomerID, req.CampaignUUID)
	if err != nil {
		return nil, err
	}
	return s.replace(ctx, campaign, req.CustomerID, req.TagIDs)
}

// AutoSelect selects up to Count available tags from the complete filtered
// snapshot-first order, not merely from the currently visible page, and
// atomically replaces the campaign selection with those IDs.
func (s *SmartTargetingFlowImpl) AutoSelect(ctx context.Context, req *dto.AutoSelectSmartTargetingTagsRequest) (*dto.SmartTargetingSelectionResponse, error) {
	if req == nil || req.Count < 1 || req.Count > maxSmartTargetingSelections {
		return nil, NewBusinessError("SMART_TARGETING_COUNT_INVALID", ErrSmartTargetingCountInvalid.Error(), ErrSmartTargetingCountInvalid)
	}
	campaign, err := s.ownedCampaign(ctx, req.CustomerID, req.CampaignUUID)
	if err != nil {
		return nil, err
	}

	// Candidate order depends on current evaluation, tag-activation, and CTR
	// data. Read it only after taking the same campaign lock used by Replace,
	// then persist it before releasing that lock. Otherwise a candidate list
	// observed before the lock can become stale before it is saved.
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		db := smartTargetingDB(txCtx, s.db)
		if err := db.Exec("SELECT id FROM campaigns WHERE id = ? FOR UPDATE", campaign.ID).Error; err != nil {
			return err
		}

		var lockedCampaign models.Campaign
		if err := db.First(&lockedCampaign, campaign.ID).Error; err != nil {
			return err
		}
		if lockedCampaign.BundleID == nil || *lockedCampaign.BundleID == 0 {
			return NewBusinessError("BUNDLE_NOT_FOUND", "Campaign bundle not found", ErrBundleNotFound)
		}
		if !lockedCampaign.IsEditable() {
			return NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Campaign cannot be updated in current status", ErrCampaignUpdateNotAllowed)
		}
		if !lockedCampaign.Spec.UsesSmartTargeting() {
			return NewBusinessError("SMART_TARGETING_NOT_ENABLED", "Campaign does not use Smart Targeting", ErrCampaignAudienceTargetingMethodInvalid)
		}

		evaluated, err := s.evaluationAvailable(txCtx, *lockedCampaign.BundleID)
		if err != nil {
			return NewBusinessError("SMART_TARGETING_EVALUATION_LOOKUP_FAILED", "Failed to lookup bundle evaluation", err)
		}
		search, sortBy, direction, err := normalizeSmartTargetingQuery(
			req.Search,
			req.SortBy,
			req.SortDirection,
			evaluated,
			lockedCampaign.Phase == models.CampaignPhaseExecution,
		)
		if err != nil {
			return NewBusinessError("SMART_TARGETING_QUERY_INVALID", err.Error(), err)
		}
		ids, err := s.selectionRepo.ListAvailableTagIDs(txCtx, *lockedCampaign.BundleID, search, sortBy, direction, req.Count)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return NewBusinessError("SMART_TARGETING_SELECTION_INVALID", ErrSmartTargetingTagsRequired.Error(), ErrSmartTargetingTagsRequired)
		}
		return s.selectionRepo.Replace(txCtx, lockedCampaign.ID, *lockedCampaign.BundleID, req.CustomerID, ids)
	})
	if err != nil {
		var businessErr *BusinessError
		if errors.As(err, &businessErr) {
			return nil, businessErr
		}
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", ErrSmartTargetingTagInvalid.Error(), ErrSmartTargetingTagInvalid)
		}
		if errors.Is(err, repository.ErrCampaignSelectedTagsNotEditable) {
			return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Campaign cannot be updated in current status", ErrCampaignUpdateNotAllowed)
		}
		return nil, NewBusinessError("SMART_TARGETING_AUTO_SELECT_FAILED", "Failed to automatically select tags", err)
	}
	return s.GetSelection(ctx, req.CustomerID, campaign.UUID.String())
}
