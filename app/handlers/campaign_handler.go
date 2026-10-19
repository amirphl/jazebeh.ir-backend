// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"errors"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// CampaignHandlerInterface defines the contract for campaign handlers
type CampaignHandlerInterface interface {
	CreateCampaign(c fiber.Ctx) error
	UpdateCampaign(c fiber.Ctx) error
	CalculateCampaignCapacity(c fiber.Ctx) error
	CalculateCampaignCost(c fiber.Ctx) error
	CalculateCampaignCostV2(c fiber.Ctx) error
	ListCampaigns(c fiber.Ctx) error
	GetLastInitiatedCampaign(c fiber.Ctx) error
	GetPagePrices(c fiber.Ctx) error
	ListAudienceSpec(c fiber.Ctx) error
	GetApprovedRunningSummary(c fiber.Ctx) error
	CancelCampaign(c fiber.Ctx) error
	CloneCampaign(c fiber.Ctx) error
	ExportCampaignReport(c fiber.Ctx) error
	ExportCampaignClickReport(c fiber.Ctx) error
	SendCampaignTestMessage(c fiber.Ctx) error
	HideCampaigns(c fiber.Ctx) error
	UnhideCampaigns(c fiber.Ctx) error
	ListSmartTargetingTags(c fiber.Ctx) error
	ListBundleSmartTargetingTags(c fiber.Ctx) error
	GetSmartTargetingSelection(c fiber.Ctx) error
	ReplaceSmartTargetingSelection(c fiber.Ctx) error
	AutoSelectSmartTargetingTags(c fiber.Ctx) error
	StartSmartTargetingCapacityCalculation(c fiber.Ctx) error
	GetSmartTargetingCapacityCalculation(c fiber.Ctx) error
	GetSmartTargetingCapacityCalculationByID(c fiber.Ctx) error
	PreviewSmartTargetingTestSampling(c fiber.Ctx) error
	GetSmartTargetingTestSampling(c fiber.Ctx) error
	GetSmartTargetingTestSamplingByID(c fiber.Ctx) error
}

// PreviewSmartTargetingTestSampling queues ordered, all-or-nothing per-tag
// sampling. Completed jobs persist an immutable private audience snapshot;
// scheduler preparation materializes that exact snapshot for delivery.
// @Summary Request Smart Targeting Test sampling
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned Smart Targeting Test campaign UUID" format(uuid)
// @Success 202 {object} dto.APIResponse{data=dto.SmartTargetingTestSamplingCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/test-sampling-preview [post]
func (h *CampaignHandler) PreviewSmartTargetingTestSampling(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/test-sampling-preview", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.StartSmartTargetingTestSampling(ctx, &dto.SmartTargetingTestSamplingPreviewRequest{
		CustomerID: customerID, CampaignUUID: c.Params("uuid"),
	}, businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent")))
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to request Smart Targeting Test sampling", "SMART_TARGETING_TEST_SAMPLING_REQUEST_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusAccepted, "Smart Targeting Test sampling requested successfully", result)
}

// GetSmartTargetingTestSampling returns the latest job for the current input.
// @Summary Get current Smart Targeting Test sampling
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned Smart Targeting Test campaign UUID" format(uuid)
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingTestSamplingCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/test-sampling-preview [get]
func (h *CampaignHandler) GetSmartTargetingTestSampling(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/test-sampling-preview", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.GetCurrentSmartTargetingTestSampling(ctx, customerID, c.Params("uuid"))
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to load Smart Targeting Test sampling", "SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting Test sampling retrieved successfully", result)
}

// GetSmartTargetingTestSamplingByID returns one historical sampling job.
// @Summary Get a Smart Targeting Test sampling job
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned Smart Targeting Test campaign UUID" format(uuid)
// @Param calculation_id path int true "Sampling calculation ID" minimum(1)
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingTestSamplingCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/test-sampling-preview/{calculation_id} [get]
func (h *CampaignHandler) GetSmartTargetingTestSamplingByID(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	calculationID, err := strconv.ParseInt(c.Params("calculation_id"), 10, 64)
	if err != nil || calculationID < 1 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Calculation ID is invalid", "INVALID_CALCULATION_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/test-sampling-preview/:calculation_id", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.GetSmartTargetingTestSamplingByID(ctx, customerID, c.Params("uuid"), calculationID)
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to load Smart Targeting Test sampling", "SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting Test sampling retrieved successfully", result)
}

// CampaignHandler handles campaign-related HTTP requests
type CampaignHandler struct {
	campaignFlow       businessflow.CampaignFlow
	smartTargetingFlow businessflow.SmartTargetingFlow
	capacityFlow       businessflow.SmartTargetingCapacityFlow
	validator          *validator.Validate
}

func (h *CampaignHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *CampaignHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// NewCampaignHandler creates a new campaign handler
func NewCampaignHandler(campaignFlow businessflow.CampaignFlow, smartTargetingFlow businessflow.SmartTargetingFlow, capacityFlow businessflow.SmartTargetingCapacityFlow) *CampaignHandler {
	handler := &CampaignHandler{
		campaignFlow:       campaignFlow,
		smartTargetingFlow: smartTargetingFlow,
		capacityFlow:       capacityFlow,
		validator:          validator.New(),
	}

	// Setup custom validations
	handler.setupCustomValidations()

	return handler
}

// StartSmartTargetingCapacityCalculation queues an exact capacity generation.
// @Summary Calculate exact Smart Targeting capacity
// @Tags Campaigns
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Param request body dto.StartSmartTargetingCapacityCalculationRequest false "Score-class selection; omitted reuses the campaign selection, with an empty campaign selection meaning A, B, and C"
// @Success 202 {object} dto.APIResponse{data=dto.SmartTargetingCapacityCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/capacity-calculations [post]
func (h *CampaignHandler) StartSmartTargetingCapacityCalculation(c fiber.Ctx) error {
	var req dto.StartSmartTargetingCapacityCalculationRequest
	if len(c.Body()) > 0 {
		if err := c.Bind().JSON(&req); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", nil)
		}
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	if h.capacityFlow == nil {
		return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Exact capacity calculation is unavailable", "SMART_TARGETING_CAPACITY_UNAVAILABLE", nil)
	}
	req.CustomerID, req.CampaignUUID = customerID, c.Params("uuid")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/capacity-calculations", 30*time.Second)
	defer cancel()
	result, err := h.capacityFlow.Start(ctx, &req, businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent")))
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to request exact capacity calculation", "SMART_TARGETING_CAPACITY_REQUEST_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusAccepted, "Exact capacity calculation requested successfully", result)
}

// GetSmartTargetingCapacityCalculation returns the latest generation and
// derives recalculation_required when tags, allocation, or expiry changed.
// @Summary Get current exact Smart Targeting capacity
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingCapacityCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/capacity-calculations [get]
func (h *CampaignHandler) GetSmartTargetingCapacityCalculation(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	if h.capacityFlow == nil {
		return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Exact capacity calculation is unavailable", "SMART_TARGETING_CAPACITY_UNAVAILABLE", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/capacity-calculations", 30*time.Second)
	defer cancel()
	result, err := h.capacityFlow.GetCurrent(ctx, customerID, c.Params("uuid"))
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to load exact capacity calculation", "SMART_TARGETING_CAPACITY_LOOKUP_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Exact capacity calculation retrieved successfully", result)
}

// GetSmartTargetingCapacityCalculationByID returns one historical generation.
// @Summary Get an exact Smart Targeting capacity generation
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Param calculation_id path int true "Calculation generation ID" minimum(1)
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingCapacityCalculationResponse}
// @Router /api/v1/campaigns/{uuid}/smart-targeting/capacity-calculations/{calculation_id} [get]
func (h *CampaignHandler) GetSmartTargetingCapacityCalculationByID(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	calculationID, err := strconv.ParseInt(c.Params("calculation_id"), 10, 64)
	if err != nil || calculationID < 1 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Calculation ID is invalid", "INVALID_CALCULATION_ID", nil)
	}
	if h.capacityFlow == nil {
		return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Exact capacity calculation is unavailable", "SMART_TARGETING_CAPACITY_UNAVAILABLE", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/capacity-calculations/:calculation_id", 30*time.Second)
	defer cancel()
	result, err := h.capacityFlow.GetByID(ctx, customerID, c.Params("uuid"), calculationID)
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to load exact capacity calculation", "SMART_TARGETING_CAPACITY_LOOKUP_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Exact capacity calculation retrieved successfully", result)
}

// ListSmartTargetingTags returns the campaign's searchable, sortable tag table.
// @Summary List Smart Targeting tags
// @Description Returns a page of tags for an owned campaign, each row's selected state, materialized Bundle/tag Test CTR, global overall CTR, nullable per-Campaign Test totals and CTR, the complete selected_tag_ids set across all pages, and the complete selection capacity summary. Execution Campaigns default to Test CTR descending, then persona-fit score descending, then database order. When the bundle has current score rows, tag names, display titles, personas, capacities, fit scores, and score explanations come from that completed evaluation snapshot. Otherwise active live tags are returned and evaluation fields are null.
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Param search query string false "Case-insensitive tag name or display-title search (maximum 200 characters)"
// @Param capacity query integer false "Return tags with capacity strictly greater than this value" minimum(0)
// @Param sort_by query string false "Sort field. bundle_persona_fit_score requires a completed bundle evaluation." Enums(tag_capacity,bundle_persona_fit_score,test_phase_avg_ctr,overall_avg_ctr)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Param page query int false "Page number" default(1) minimum(1)
// @Param page_size query int false "Items per page; limit is accepted as a backward-compatible alias" default(20) minimum(1) maximum(100)
// @Param limit query int false "Backward-compatible alias for page_size; ignored when page_size is supplied" minimum(1) maximum(100)
// @Success 200 {object} dto.APIResponse{data=dto.ListSmartTargetingTagsResponse} "Tag page and complete selection state"
// @Failure 400 {object} dto.APIResponse "Invalid UUID, capacity, pagination, search, or sort; persona score unavailable"
// @Failure 401 {object} dto.APIResponse "Authentication required"
// @Failure 403 {object} dto.APIResponse "Campaign belongs to another customer"
// @Failure 404 {object} dto.APIResponse "Campaign or bundle not found"
// @Failure 500 {object} dto.APIResponse "Tag, evaluation, or selection lookup failed"
// @Router /api/v1/campaigns/{uuid}/smart-targeting/tags [get]
func (h *CampaignHandler) ListSmartTargetingTags(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	pageSizeValue := c.Query("page_size")
	if pageSizeValue == "" {
		pageSizeValue = c.Query("limit")
	}
	pageSize, err := parseBoundedPositiveQuery(pageSizeValue, 20, 100)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page size", "INVALID_PAGE_SIZE", nil)
	}
	page, err := parseBoundedPositiveQuery(c.Query("page"), 1, maxPageForSmartTargetingOffset(pageSize))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
	}
	capacity, err := parseOptionalNonNegativeInt64Query(c.Query("capacity"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid capacity", "INVALID_CAPACITY", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/tags", 30*time.Second)
	defer cancel()
	res, err := h.smartTargetingFlow.ListTags(ctx, &dto.ListSmartTargetingTagsRequest{
		CustomerID: customerID, CampaignUUID: c.Params("uuid"), Search: c.Query("search"),
		Capacity: capacity, SortBy: c.Query("sort_by"), SortDirection: c.Query("sort_direction"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to list Smart Targeting tags", "SMART_TARGETING_TAG_LIST_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting tags retrieved successfully", res)
}

// ListBundleSmartTargetingTags returns available tags before campaign creation.
// @Summary List bundle Smart Targeting tags
// @Description Returns a page of tags for an owned bundle before a campaign exists, including materialized Bundle/tag Test totals and CTR plus global overall CTR. When current score rows exist, their tag and score snapshots are authoritative; otherwise the endpoint falls back to active live tags with null evaluation fields. selected_tag_ids and the selection summary are empty; submit chosen IDs with campaign creation.
// @Tags Bundles
// @Produce json
// @Security BearerAuth
// @Param id path int true "Owned bundle ID" minimum(1)
// @Param search query string false "Case-insensitive tag name or display-title search (maximum 200 characters)"
// @Param capacity query integer false "Return tags with capacity strictly greater than this value" minimum(0)
// @Param sort_by query string false "Sort field. bundle_persona_fit_score requires a completed bundle evaluation." Enums(tag_capacity,bundle_persona_fit_score,test_phase_avg_ctr,overall_avg_ctr)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Param page query int false "Page number" default(1) minimum(1)
// @Param page_size query int false "Items per page; limit is accepted as a backward-compatible alias" default(20) minimum(1) maximum(100)
// @Param limit query int false "Backward-compatible alias for page_size; ignored when page_size is supplied" minimum(1) maximum(100)
// @Success 200 {object} dto.APIResponse{data=dto.ListSmartTargetingTagsResponse} "Available tag page"
// @Failure 400 {object} dto.APIResponse "Invalid bundle ID, capacity, pagination, search, or sort; persona score unavailable"
// @Failure 401 {object} dto.APIResponse "Authentication required"
// @Failure 403 {object} dto.APIResponse "Bundle belongs to another customer"
// @Failure 404 {object} dto.APIResponse "Bundle not found"
// @Failure 500 {object} dto.APIResponse "Tag or evaluation lookup failed"
// @Router /api/v1/bundles/{id}/smart-targeting/tags [get]
func (h *CampaignHandler) ListBundleSmartTargetingTags(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	bundleID, err := strconv.ParseUint(c.Params("id"), 10, strconv.IntSize)
	if err != nil || bundleID == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}
	pageSizeValue := c.Query("page_size")
	if pageSizeValue == "" {
		pageSizeValue = c.Query("limit")
	}
	pageSize, err := parseBoundedPositiveQuery(pageSizeValue, 20, 100)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page size", "INVALID_PAGE_SIZE", nil)
	}
	page, err := parseBoundedPositiveQuery(c.Query("page"), 1, maxPageForSmartTargetingOffset(pageSize))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
	}
	capacity, err := parseOptionalNonNegativeInt64Query(c.Query("capacity"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid capacity", "INVALID_CAPACITY", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/smart-targeting/tags", 30*time.Second)
	defer cancel()
	res, err := h.smartTargetingFlow.ListBundleTags(ctx, &dto.ListSmartTargetingTagsRequest{
		CustomerID: customerID, BundleID: uint(bundleID), Search: c.Query("search"),
		Capacity: capacity, SortBy: c.Query("sort_by"), SortDirection: c.Query("sort_direction"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to list Smart Targeting tags", "SMART_TARGETING_TAG_LIST_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting tags retrieved successfully", res)
}

func parseBoundedPositiveQuery(value string, defaultValue, max int) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || (max > 0 && parsed > max) {
		return 0, errors.New("invalid positive integer")
	}
	return parsed, nil
}

func parseOptionalNonNegativeInt64Query(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return nil, errors.New("invalid non-negative integer")
	}
	return &parsed, nil
}

// maxPageForSmartTargetingOffset returns the greatest page whose zero-based
// offset is representable as an int for the supplied (already validated) page
// size. Page size one is special: MaxInt + 1 would itself overflow.
func maxPageForSmartTargetingOffset(pageSize int) int {
	maxPage := math.MaxInt / pageSize
	if maxPage == math.MaxInt {
		return math.MaxInt
	}
	return maxPage + 1
}

// GetSmartTargetingSelection returns the complete persisted selection, independent of table pagination.
// @Summary Get Smart Targeting selection
// @Description Returns every selected tag ID and the raw-capacity summary for an owned campaign. The result is independent of tag-table pagination.
// @Tags Campaigns
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingSelectionResponse} "Complete selection"
// @Failure 400 {object} dto.APIResponse "Invalid campaign UUID"
// @Failure 401 {object} dto.APIResponse "Authentication required"
// @Failure 403 {object} dto.APIResponse "Campaign belongs to another customer"
// @Failure 404 {object} dto.APIResponse "Campaign or bundle not found"
// @Failure 500 {object} dto.APIResponse "Selection lookup failed"
// @Router /api/v1/campaigns/{uuid}/smart-targeting/selection [get]
func (h *CampaignHandler) GetSmartTargetingSelection(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/selection", 30*time.Second)
	defer cancel()
	res, err := h.smartTargetingFlow.GetSelection(ctx, customerID, c.Params("uuid"))
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to retrieve Smart Targeting selection", "SMART_TARGETING_SELECTION_LOOKUP_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting selection retrieved successfully", res)
}

// ReplaceSmartTargetingSelection atomically replaces the complete selected-tag set.
// @Summary Replace Smart Targeting selection
// @Description Atomically replaces the complete selected-tag set for an editable Smart Targeting campaign. All IDs must be unique and available in the bundle's current score snapshot, or in active live tags when no score rows exist; at least one ID is required.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Param request body dto.ReplaceSmartTargetingSelectionRequest true "Complete replacement tag-ID set"
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingSelectionResponse} "Selection replaced"
// @Failure 400 {object} dto.APIResponse "Invalid UUID, body, targeting mode, or tag IDs"
// @Failure 401 {object} dto.APIResponse "Authentication required"
// @Failure 403 {object} dto.APIResponse "Campaign access denied or campaign is not editable"
// @Failure 404 {object} dto.APIResponse "Campaign or bundle not found"
// @Failure 500 {object} dto.APIResponse "Selection persistence failed"
// @Router /api/v1/campaigns/{uuid}/smart-targeting/selection [put]
func (h *CampaignHandler) ReplaceSmartTargetingSelection(c fiber.Ctx) error {
	var req dto.ReplaceSmartTargetingSelectionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "At least one tag must be selected for Smart Targeting.", "SMART_TARGETING_SELECTION_INVALID", nil)
	}
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID, req.CampaignUUID = customerID, c.Params("uuid")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/selection", 30*time.Second)
	defer cancel()
	res, err := h.smartTargetingFlow.ReplaceSelection(ctx, &req)
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to save Smart Targeting selection", "SMART_TARGETING_SELECTION_SAVE_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting selection saved successfully", res)
}

// AutoSelectSmartTargetingTags replaces the selection with the first count tags from the complete filtered order.
// @Summary Auto-select Smart Targeting tags
// @Description Selects up to count available tags from the complete normalized snapshot-first search and sort order—not only the visible page—and atomically replaces the campaign's complete selection.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Owned campaign UUID" format(uuid)
// @Param request body dto.AutoSelectSmartTargetingTagsRequest true "Automatic selection criteria"
// @Success 200 {object} dto.APIResponse{data=dto.SmartTargetingSelectionResponse} "Tags selected"
// @Failure 400 {object} dto.APIResponse "Invalid UUID, count, search, sort, targeting mode, or no matching tags"
// @Failure 401 {object} dto.APIResponse "Authentication required"
// @Failure 403 {object} dto.APIResponse "Campaign access denied or campaign is not editable"
// @Failure 404 {object} dto.APIResponse "Campaign or bundle not found"
// @Failure 500 {object} dto.APIResponse "Evaluation, tag lookup, or selection persistence failed"
// @Router /api/v1/campaigns/{uuid}/smart-targeting/selection/auto [post]
func (h *CampaignHandler) AutoSelectSmartTargetingTags(c fiber.Ctx) error {
	var req dto.AutoSelectSmartTargetingTagsRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid automatic-selection request", "SMART_TARGETING_AUTO_SELECT_INVALID", nil)
	}
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID, req.CampaignUUID = customerID, c.Params("uuid")
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/:uuid/smart-targeting/selection/auto", 30*time.Second)
	defer cancel()
	res, err := h.smartTargetingFlow.AutoSelect(ctx, &req)
	if err != nil {
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to automatically select Smart Targeting tags", "SMART_TARGETING_AUTO_SELECT_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Smart Targeting tags selected successfully", res)
}

// CreateCampaign handles the campaign creation process
// @Summary Create Campaign
// @Description Create a new campaign with the specified parameters
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.CreateCampaignRequest true "Campaign creation data"
// @Success 201 {object} dto.APIResponse{data=dto.CreateCampaignResponse} "Campaign created successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns [post]
func (h *CampaignHandler) CreateCampaign(c fiber.Ctx) error {
	var req dto.CreateCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Validate request
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Set the authenticated customer ID in the request
	req.CustomerID = customerID

	// Call business logic with proper context
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.CreateCampaign(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign creation failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign creation failed", "CAMPAIGN_CREATION_FAILED")
	}

	// Successful campaign creation
	return h.SuccessResponse(c, fiber.StatusCreated, "Campaign created successfully", fiber.Map{
		"message":    result.Message,
		"id":         result.ID,
		"uuid":       result.UUID,
		"status":     result.Status,
		"created_at": result.CreatedAt,
	})
}

// UpdateCampaign handles the campaign update process
// @Summary Update Campaign
// @Description Update an existing campaign with the specified parameters
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param uuid path string true "Campaign UUID"
// @Param request body dto.UpdateCampaignRequest true "Campaign update data"
// @Success 200 {object} dto.APIResponse{data=dto.UpdateCampaignResponse} "Campaign updated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 403 {object} dto.APIResponse "Forbidden - campaign access denied or update not allowed"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/{uuid} [put]
func (h *CampaignHandler) UpdateCampaign(c fiber.Ctx) error {
	// Get campaign UUID from path parameter
	campaignUUID := c.Params("uuid")
	if campaignUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is required", "MISSING_CAMPAIGN_UUID", nil)
	}

	var req dto.UpdateCampaignRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Set the campaign UUID and customer ID in the request
	req.UUID = campaignUUID

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	// Validate request
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Call business logic with proper context
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+campaignUUID, 60*time.Second)
	defer cancel()
	result, err := h.campaignFlow.UpdateCampaign(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign update failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign update failed", "CAMPAIGN_UPDATE_FAILED")
	}

	// Successful campaign update
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign updated successfully", fiber.Map{
		"message": result.Message,
	})
}

// CancelCampaign handles customer-initiated campaign cancellation
// @Summary Cancel Campaign
// @Description Cancel a campaign that is waiting for approval, or approved but not yet scheduled to start
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param request body dto.CancelCampaignRequest false "Optional comment"
// @Success 200 {object} dto.APIResponse{data=dto.CancelCampaignResponse} "Campaign cancelled successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden - campaign access denied or status not cancellable"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/{id}/cancel [post]
func (h *CampaignHandler) CancelCampaign(c fiber.Ctx) error {
	idStr := c.Params("id")
	if idStr == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign ID is required", "MISSING_CAMPAIGN_ID", nil)
	}
	id64, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign ID", "INVALID_CAMPAIGN_ID", nil)
	}

	var req dto.CancelCampaignRequest
	req.CampaignID = uint(id64)

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+idStr+"/cancel", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.CancelCampaign(ctx, &req, metadata)
	if err != nil {
		log.Println("Cancel campaign failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Cancel campaign failed", "CANCEL_CAMPAIGN_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaign cancelled successfully", result)
}

// CloneCampaign clones an existing campaign for the authenticated customer.
// @Summary Clone Campaign
// @Description Clone an existing campaign belonging to the current customer.
// @Tags Campaigns
// @Produce json
// @Param uuid path string true "Campaign UUID to clone"
// @Success 201 {object} dto.APIResponse{data=dto.CloneCampaignResponse} "Campaign cloned successfully"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden - access denied"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 409 {object} dto.APIResponse "Clone not allowed"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/{uuid}/clone [post]
func (h *CampaignHandler) CloneCampaign(c fiber.Ctx) error {
	campaignUUID := c.Params("uuid")
	if strings.TrimSpace(campaignUUID) == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is required", "MISSING_CAMPAIGN_UUID", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	req := dto.CloneCampaignRequest{
		UUID:       campaignUUID,
		CustomerID: customerID,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+campaignUUID+"/clone", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.CloneCampaign(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if businessflow.IsCampaignAccessDenied(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Access denied", "CAMPAIGN_ACCESS_DENIED", nil)
		}
		if businessflow.IsCampaignUpdateNotAllowed(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Clone not allowed", "CLONE_NOT_ALLOWED", nil)
		}
		log.Println("Clone campaign failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to clone campaign", "CAMPAIGN_CLONE_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Campaign cloned successfully", res)
}

// ExportCampaignReport exports campaign tracking results as an Excel report.
// @Summary Export Campaign Report
// @Description Export an SMS campaign report as an Excel file for the authenticated customer
// @Tags Campaigns
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param uuid path string true "Campaign UUID"
// @Success 200 {string} string "Excel file"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/{uuid}/export [get]
func (h *CampaignHandler) ExportCampaignReport(c fiber.Ctx) error {
	campaignUUID := strings.TrimSpace(c.Params("uuid"))
	if campaignUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is required", "MISSING_CAMPAIGN_UUID", nil)
	}
	parsedCampaignUUID, err := uuid.Parse(campaignUUID)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is invalid", "INVALID_CAMPAIGN_UUID", nil)
	}
	campaignUUID = parsedCampaignUUID.String()

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+campaignUUID+"/export", 30*time.Second)
	defer cancel()
	data, err := h.campaignFlow.ExportCampaignReport(ctx, campaignUUID)
	if err != nil {
		log.Println("Export campaign report failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to export campaign report", "CAMPAIGN_REPORT_EXPORT_FAILED")
	}

	filename := "campaign_report_" + campaignUUID + ".xlsx"
	c.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	return c.Send(data)
}

// ExportCampaignClickReport exports a CSV with uid,clicked columns for all audience members.
// @Summary Export Campaign Click Report
// @Description Export a CSV showing which targeted audience members clicked the campaign short link
// @Tags Campaigns
// @Produce text/csv
// @Param uuid path string true "Campaign UUID"
// @Success 200 {string} string "CSV file"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Campaign not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/{uuid}/click-report [get]
func (h *CampaignHandler) ExportCampaignClickReport(c fiber.Ctx) error {
	campaignUUID := strings.TrimSpace(c.Params("uuid"))
	if campaignUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is required", "MISSING_CAMPAIGN_UUID", nil)
	}
	parsed, err := uuid.Parse(campaignUUID)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is invalid", "INVALID_CAMPAIGN_UUID", nil)
	}
	campaignUUID = parsed.String()

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+campaignUUID+"/click-report", 60*time.Second)
	defer cancel()
	data, err := h.campaignFlow.ExportCampaignClickReport(ctx, campaignUUID)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "AUDIENCE_REPORT_NOT_AVAILABLE":
				return h.ErrorResponse(c, fiber.StatusNotFound, be.Message, be.Code, nil)
			case "CAMPAIGN_UUID_REQUIRED", "CAMPAIGN_UUID_INVALID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, be.Message, be.Code, nil)
			case "MISSING_CUSTOMER_ID":
				return h.ErrorResponse(c, fiber.StatusUnauthorized, be.Message, be.Code, nil)
			}
		}
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		log.Println("Export campaign click report failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to export campaign click report", "CAMPAIGN_CLICK_REPORT_EXPORT_FAILED", nil)
	}

	filename := "campaign_click_report_" + campaignUUID + ".csv"
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	return c.Send(data)
}

// CalculateCampaignCapacity handles the campaign capacity calculation process
// @Summary Calculate Campaign Capacity
// @Description Calculate the potential reach and capacity of an campaign based on parameters
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.CalculateCampaignCapacityRequest true "Campaign parameters for capacity calculation"
// @Success 200 {object} dto.APIResponse{data=dto.CalculateCampaignCapacityResponse} "Capacity calculated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/calculate-capacity [post]
func (h *CampaignHandler) CalculateCampaignCapacity(c fiber.Ctx) error {
	var req dto.CalculateCampaignCapacityRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Validate request
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	// Call business logic with proper context
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/calculate-capacity", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.CalculateCampaignCapacity(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign capacity calculation failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign capacity calculation failed", "CAPACITY_CALCULATION_FAILED")
	}

	// Successful capacity calculation
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign capacity calculated successfully", fiber.Map{
		"message":  result.Message,
		"capacity": result.Capacity,
	})
}

// CalculateCampaignCost handles the campaign cost calculation process
// @Summary Calculate Campaign Cost
// @Description Calculate the total cost of an campaign based on content, target audience, and pricing factors
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.CalculateCampaignCostRequest true "Campaign parameters for cost calculation"
// @Success 200 {object} dto.APIResponse{data=dto.CalculateCampaignCostResponse} "Cost calculated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/calculate-cost [post]
func (h *CampaignHandler) CalculateCampaignCost(c fiber.Ctx) error {
	var req dto.CalculateCampaignCostRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Validate request
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	// Call business logic with proper context
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/calculate-cost", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.CalculateCampaignCost(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign cost calculation failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign cost calculation failed", "COST_CALCULATION_FAILED")
	}

	// Successful cost calculation
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign cost calculated successfully", fiber.Map{
		"message":        result.Message,
		"total_cost":     result.TotalCost,
		"msg_target":     result.NumTargetAudience,
		"max_msg_target": result.MaxTargetAudience,
	})
}

// CalculateCampaignCostV2 handles reverse cost calculation process
// @Summary Calculate Campaign Cost V2
// @Description Calculate required credit based on desired num_messages, capped by audience capacity
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.CalculateCampaignCostV2Request true "Campaign parameters for reverse cost calculation"
// @Success 200 {object} dto.APIResponse{data=dto.CalculateCampaignCostResponse} "Cost calculated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/calculate-cost-v2 [post]
func (h *CampaignHandler) CalculateCampaignCostV2(c fiber.Ctx) error {
	var req dto.CalculateCampaignCostV2Request
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/calculate-cost-v2", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.CalculateCampaignCostV2(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign cost calculation v2 failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign cost calculation failed", "COST_CALCULATION_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaign cost calculated successfully", fiber.Map{
		"message":        result.Message,
		"total_cost":     result.TotalCost,
		"msg_target":     result.NumTargetAudience,
		"max_msg_target": result.MaxTargetAudience,
	})
}

// ListCampaigns returns user's campaigns with filters and pagination
// @Summary List Campaigns
// @Description Retrieve the authenticated user's campaigns with pagination, ordering, and filters
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param page query int true "Page number"
// @Param limit query int true "Items per page (max 100)"
// @Param orderby query string false "Order by (newest|oldest)" default(newest)
// @Param title query string false "Filter by title (contains)"
// @Param status query string false "Filter by status (initiated|in-progress|waiting-for-approval|approved|rejected|running|executed|expired|cancelled|cancelled-by-admin)"
// @Success 200 {object} dto.APIResponse{data=dto.ListCampaignsResponse}
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns [get]
func (h *CampaignHandler) ListCampaigns(c fiber.Ctx) error {
	// Parse query params
	pageStr := c.Query("page", "1")
	limitStr := c.Query("limit", "10")
	page := 1
	if v, err := strconv.Atoi(pageStr); err == nil && v > 0 {
		page = v
	}
	limit := 10
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
		limit = v
	}
	if limit > 100 {
		limit = 100
	}
	orderby := c.Query("orderby", "newest")
	campaignTitle := strings.TrimSpace(c.Query("title"))
	if campaignTitle == "" {
		campaignTitle = strings.TrimSpace(c.Query("campaign_title"))
	}
	bundleTitle := c.Query("bundle_title")
	customerName := c.Query("customer_name")
	status := c.Query("status")
	bundleIDStr := c.Query("bundle_id")
	platform := c.Query("platform")
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")
	phase := c.Query("phase")

	// Get authenticated customer ID
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Build request DTO
	var filter *dto.ListCampaignsFilter
	if campaignTitle != "" || bundleTitle != "" || customerName != "" || status != "" || bundleIDStr != "" || platform != "" || startDateStr != "" || endDateStr != "" || phase != "" {
		filter = &dto.ListCampaignsFilter{}
		if campaignTitle != "" {
			filter.CampaignTitle = &campaignTitle
		}
		if bundleTitle != "" {
			filter.BundleTitle = &bundleTitle
		}
		if customerName != "" {
			filter.CustomerName = &customerName
		}
		if status != "" {
			filter.Status = &status
		}
		if bundleIDStr != "" {
			bundleID, err := strconv.ParseUint(bundleIDStr, 10, 64)
			if err != nil || bundleID == 0 {
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
			}
			parsedBundleID := uint(bundleID)
			filter.BundleID = &parsedBundleID
		}
		if platform != "" {
			filter.Platform = &platform
		}
		if startDateStr != "" {
			t, err := time.Parse(time.RFC3339, startDateStr)
			if err != nil {
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid start_date format", "INVALID_DATE", nil)
			}
			startUTC := t.UTC()
			filter.StartDate = &startUTC
		}
		if endDateStr != "" {
			t, err := time.Parse(time.RFC3339, endDateStr)
			if err != nil {
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid end_date format", "INVALID_DATE", nil)
			}
			endUTC := t.UTC()
			filter.EndDate = &endUTC
		}
		if phase != "" {
			filter.Phase = &phase
		}
	}
	req := &dto.ListCampaignsRequest{
		CustomerID: customerID,
		Page:       page,
		Limit:      limit,
		OrderBy:    orderby,
		Filter:     filter,
	}

	// Client metadata
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	// Call business logic
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.ListCampaigns(ctx, req, metadata)
	if err != nil {
		log.Println("List campaigns failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to list campaigns", "LIST_CAMPAIGNS_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaigns retrieved successfully", fiber.Map{
		"message":    result.Message,
		"items":      result.Items,
		"pagination": result.Pagination,
	})
}

// GetLastInitiatedCampaign returns the authenticated user's latest initiated campaign.
// @Summary Get Last Initiated Campaign
// @Description Retrieve the most recent campaign with status initiated for the authenticated user
// @Tags Campaigns
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.GetLastInitiatedCampaignResponse}
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/initiated/last [get]
func (h *CampaignHandler) GetLastInitiatedCampaign(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/initiated/last", 30*time.Second)
	defer cancel()
	result, err := h.campaignFlow.GetLastInitiatedCampaign(ctx, customerID, metadata)
	if err != nil {
		log.Println("Get last initiated campaign failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to get last initiated campaign", "GET_LAST_INITIATED_CAMPAIGN_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, result.Message, result)
}

// GetPagePrices returns latest page price per platform.
// @Summary Get Page Prices
// @Description Return latest page price for all platforms
// @Tags Campaigns
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.GetPagePricesResponse}
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/campaigns/page-prices [get]
func (h *CampaignHandler) GetPagePrices(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/page-prices", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.GetPagePrices(ctx)
	if err != nil {
		log.Println("Get page prices failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to get page prices", "PAGE_PRICE_LIST_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Page prices retrieved successfully", res)
}

// ListAudienceSpec returns the current audience spec
// @Summary List Audience Spec
// @Tags Campaigns
// @Produce json
// @Param platform query string false "Platform (default: sms)"
// @Success 200 {object} dto.APIResponse{data=dto.ListAudienceSpecResponse}
// @Router /api/v1/campaigns/audience-spec [get]
func (h *CampaignHandler) ListAudienceSpec(c fiber.Ctx) error {
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	_ = metadata
	var platform *string
	platformRaw := c.Query("platform")
	if platformRaw != "" {
		platform = &platformRaw
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/audience-spec", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.ListAudienceSpec(ctx, platform)
	if err != nil {
		if businessflow.IsAudienceSpecPlatformInvalid(err) || businessflow.IsAudienceSpecPlatformRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid platform", "INVALID_PLATFORM", nil)
		}
		log.Println("List audience spec failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list audience spec", "LIST_AUDIENCE_SPEC_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Audience spec retrieved successfully", res)
}

// GetApprovedRunningSummary returns counts of approved and running campaigns for the authenticated customer
// @Summary Get campaigns summary
// @Tags Campaigns
// @Produce json
// @Success 200 {object} dto.APIResponse{data=map[string]any}
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/summary [get]
func (h *CampaignHandler) GetApprovedRunningSummary(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/summary", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.GetApprovedRunningSummary(ctx, customerID)
	if err != nil {
		log.Println("Get campaigns summary failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to get campaigns summary", "GET_CAMPAIGNS_SUMMARY_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, res.Message, fiber.Map{
		"approved_count": res.ApprovedCount,
		"running_count":  res.RunningCount,
		"total":          res.Total,
	})
}

// SendCampaignTestMessage attempts best-effort delivery of a single test message to the requested target phone.
// @Summary Send Campaign Test Message
// @Description Send a best-effort test message for a campaign to a target phone number
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param uuid path string true "Campaign UUID"
// @Param request body dto.SendCampaignTestMessageRequest true "Campaign test-send target"
// @Success 200 {object} dto.APIResponse{data=dto.SendCampaignTestMessageResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Failure 403 {object} dto.APIResponse
// @Failure 404 {object} dto.APIResponse
// @Failure 409 {object} dto.APIResponse
// @Failure 429 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/campaigns/{uuid}/test-send [post]
func (h *CampaignHandler) SendCampaignTestMessage(c fiber.Ctx) error {
	var req dto.SendCampaignTestMessageRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	campaignUUID := strings.TrimSpace(c.Params("uuid"))
	if campaignUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is required", "MISSING_CAMPAIGN_UUID", nil)
	}
	parsedCampaignUUID, err := uuid.Parse(campaignUUID)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is invalid", "INVALID_CAMPAIGN_UUID", nil)
	}
	campaignUUID = parsedCampaignUUID.String()

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	req.UUID = campaignUUID
	req.CustomerID = customerID
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/"+campaignUUID+"/test-send", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.SendCampaignTestMessage(ctx, &req, metadata)
	if err != nil {
		log.Println("Campaign test send failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Campaign test send failed", "CAMPAIGN_TEST_SEND_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaign test message attempted", res)
}

// HideCampaigns marks campaigns so they no longer appear in caller-facing list responses.
// @Summary Hide Campaigns
// @Description Mark campaigns as hidden from the authenticated caller's campaign list
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.HideCampaignsRequest true "Campaign IDs to hide"
// @Success 200 {object} dto.APIResponse{data=dto.HideCampaignsResponse} "Campaigns hidden successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "One or more campaigns not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/hide [post]
func (h *CampaignHandler) HideCampaigns(c fiber.Ctx) error {
	var req dto.HideCampaignsRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/hide", 30*time.Second)
	defer cancel()

	result, err := h.campaignFlow.HideCampaigns(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "One or more campaigns not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		log.Println("Hide campaigns failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to hide campaigns", "HIDE_CAMPAIGNS_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaigns hidden successfully", result)
}

// UnhideCampaigns marks campaigns so they appear again in caller-facing list responses.
// @Summary Unhide Campaigns
// @Description Mark campaigns as visible in the authenticated caller's campaign list
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param request body dto.UnhideCampaignsRequest true "Campaign IDs to unhide"
// @Success 200 {object} dto.APIResponse{data=dto.UnhideCampaignsResponse} "Campaigns unhidden successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "One or more campaigns not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/campaigns/unhide [post]
func (h *CampaignHandler) UnhideCampaigns(c fiber.Ctx) error {
	var req dto.UnhideCampaignsRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(err))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/campaigns/unhide", 30*time.Second)
	defer cancel()

	result, err := h.campaignFlow.UnhideCampaigns(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "One or more campaigns not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		log.Println("Unhide campaigns failed", err)
		return h.handleCampaignFlowError(c, err, fiber.StatusInternalServerError, "Failed to unhide campaigns", "UNHIDE_CAMPAIGNS_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Campaigns unhidden successfully", result)
}

// createRequestContextWithTimeout creates a context with custom timeout and request-scoped values
func (h *CampaignHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
	// Create context with custom timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Add request-scoped values for observability
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel) // Store cancel function for cleanup
	if customerID, ok := c.Locals("customer_id").(uint); ok && customerID != 0 {
		ctx = context.WithValue(ctx, utils.CustomerIDKey, customerID)
	}

	return ctx, cancel
}

// setupCustomValidations sets up custom validation rules
func (h *CampaignHandler) setupCustomValidations() {
	h.validator.RegisterValidation("mobile_format", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if len(value) != 13 || value[:4] != "+989" {
			return false
		}
		for _, char := range value[4:] {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	})
}

func (h *CampaignHandler) handleCampaignFlowError(c fiber.Ctx, err error, defaultStatus int, defaultMessage, defaultCode string) error {
	var businessErr *businessflow.BusinessError
	_ = errors.As(err, &businessErr)

	if errors.Is(err, businessflow.ErrCampaignUUIDRequired) || errors.Is(err, businessflow.ErrCampaignUUIDInvalid) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign UUID is invalid", "INVALID_CAMPAIGN_UUID", nil)
	}
	if errors.Is(err, businessflow.ErrBundleNotFound) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Bundle not found", "BUNDLE_NOT_FOUND", nil)
	}
	if errors.Is(err, businessflow.ErrBundleAccessDenied) {
		return h.ErrorResponse(c, fiber.StatusForbidden, "Bundle access denied", "BUNDLE_ACCESS_DENIED", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingSampleSizeRequired) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, businessflow.ErrSmartTargetingSampleSizeRequired.Error(), "SMART_TARGETING_SAMPLE_SIZE_REQUIRED", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingSampleSizeInvalid) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, businessflow.ErrSmartTargetingSampleSizeInvalid.Error(), "SMART_TARGETING_SAMPLE_SIZE_INVALID", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingTestPreviewRequired) {
		return h.ErrorResponse(c, fiber.StatusConflict, businessflow.ErrSmartTargetingTestPreviewRequired.Error(), "SMART_TARGETING_TEST_PREVIEW_REQUIRED", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingTestNoSatisfiedTags) {
		return h.ErrorResponse(c, fiber.StatusConflict, businessflow.ErrSmartTargetingTestNoSatisfiedTags.Error(), "SMART_TARGETING_TEST_NO_SATISFIED_TAGS", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingTestAudienceCountOverflow) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, businessflow.ErrSmartTargetingTestAudienceCountOverflow.Error(), "SMART_TARGETING_TEST_AUDIENCE_COUNT_OVERFLOW", nil)
	}
	if errors.Is(err, businessflow.ErrCampaignCostOverflow) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, businessflow.ErrCampaignCostOverflow.Error(), "CAMPAIGN_COST_OVERFLOW", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingTagsRequired) ||
		errors.Is(err, businessflow.ErrSmartTargetingTagInvalid) ||
		errors.Is(err, businessflow.ErrSmartTargetingScoreClassesInvalid) ||
		errors.Is(err, businessflow.ErrSmartTargetingSortInvalid) ||
		errors.Is(err, businessflow.ErrSmartTargetingScoreUnavailable) ||
		errors.Is(err, businessflow.ErrSmartTargetingSearchTooLong) ||
		errors.Is(err, businessflow.ErrSmartTargetingCountInvalid) ||
		errors.Is(err, businessflow.ErrCampaignAudienceTargetingMethodInvalid) {
		code := defaultCode
		if businessErr != nil && businessErr.Code != "" {
			code = businessErr.Code
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, err.Error(), code, nil)
	}
	if errors.Is(err, businessflow.ErrInvalidState) {
		message, code := "Another request is already in progress", "INVALID_STATE"
		if businessErr != nil {
			if businessErr.Message != "" {
				message = businessErr.Message
			}
			if businessErr.Code != "" {
				code = businessErr.Code
			}
		}
		return h.ErrorResponse(c, fiber.StatusConflict, message, code, nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingCapacityPending) {
		var pending *businessflow.SmartTargetingCapacityPendingError
		if errors.As(err, &pending) && pending != nil {
			return h.ErrorResponse(c, fiber.StatusConflict, "Exact Smart Targeting capacity calculation is pending; please wait and retry", "SMART_TARGETING_CAPACITY_PENDING", pending.Response)
		}
		return h.ErrorResponse(c, fiber.StatusConflict, "Exact Smart Targeting capacity calculation is pending; please wait and retry", "SMART_TARGETING_CAPACITY_PENDING", nil)
	}
	if errors.Is(err, businessflow.ErrSmartTargetingExactCapacityRequired) {
		return h.ErrorResponse(c, fiber.StatusConflict, "A current exact Smart Targeting capacity calculation is required", "SMART_TARGETING_EXACT_CAPACITY_REQUIRED", nil)
	}
	if businessflow.IsCampaignNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
	}
	if businessflow.IsCampaignAccessDenied(err) {
		return h.ErrorResponse(c, fiber.StatusForbidden, "Campaign access denied", "CAMPAIGN_ACCESS_DENIED", nil)
	}
	if businessflow.IsCampaignUpdateNotAllowed(err) {
		return h.ErrorResponse(c, fiber.StatusForbidden, "Campaign cannot be updated in current status", "CAMPAIGN_UPDATE_NOT_ALLOWED", nil)
	}
	if businessflow.IsCampaignNotWaitingForApproval(err) {
		return h.ErrorResponse(c, fiber.StatusForbidden, "Campaign cannot be cancelled in current status", "CAMPAIGN_CANCEL_NOT_ALLOWED", nil)
	}
	if businessflow.IsCampaignNotApproved(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Campaign is not approved", "CAMPAIGN_NOT_APPROVED", nil)
	}
	if businessflow.IsInsufficientCampaignCapacity(err) {
		message, code := "Insufficient campaign capacity", "INSUFFICIENT_CAPACITY"
		if businessErr != nil {
			if businessErr.Message != "" {
				message = businessErr.Message
			}
			if businessErr.Code != "" {
				code = businessErr.Code
			}
		}
		return h.ErrorResponse(c, fiber.StatusConflict, message, code, nil)
	}
	if businessflow.IsInsufficientFunds(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Insufficient funds", "INSUFFICIENT_FUNDS", nil)
	}

	if businessflow.IsCustomerNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
	}
	if businessflow.IsAccountInactive(err) {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Account is inactive", "ACCOUNT_INACTIVE", nil)
	}
	if businessflow.IsAccountTypeNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
	}
	if businessflow.IsWalletNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Wallet not found", "WALLET_NOT_FOUND", nil)
	}
	if businessflow.IsBalanceSnapshotNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Balance snapshot not found", "BALANCE_SNAPSHOT_NOT_FOUND", nil)
	}
	if businessflow.IsFreezeTransactionNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Freeze transaction not found", "FREEZE_TRANSACTION_NOT_FOUND", nil)
	}
	if businessflow.IsMultipleFreezeTransactionsFound(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Multiple freeze transactions found", "MULTIPLE_FREEZE_TRANSACTIONS_FOUND", nil)
	}
	if businessflow.IsCampaignDebitTransactionNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Campaign debit transaction not found", "CAMPAIGN_DEBIT_TRANSACTION_NOT_FOUND", nil)
	}
	if businessflow.IsMultipleCampaignDebitTransactionsFound(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Multiple campaign debit transactions found", "MULTIPLE_CAMPAIGN_DEBIT_TRANSACTIONS_FOUND", nil)
	}

	if businessflow.IsCampaignPlatformRequired(err) || businessflow.IsCampaignPlatformInvalid(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid platform", "INVALID_PLATFORM", nil)
	}
	if businessflow.IsCampaignPlatformSettingRequired(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Platform settings is required for non-sms campaigns", "PLATFORM_SETTINGS_REQUIRED", nil)
	}
	if businessflow.IsCampaignPlatformSettingNotApplicable(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Platform settings is only applicable for non-sms campaigns", "PLATFORM_SETTINGS_NOT_APPLICABLE", nil)
	}
	if businessflow.IsCampaignPlatformSettingNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Platform settings not found", "PLATFORM_SETTINGS_NOT_FOUND", nil)
	}
	if businessflow.IsCampaignTestPlatformSettingsInvalid(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Platform settings are invalid for test send", "PLATFORM_SETTINGS_INVALID", nil)
	}
	if businessflow.IsCampaignTestRecipientMissing(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Target phone number is missing", "TEST_RECIPIENT_MISSING", nil)
	}
	if businessflow.IsCampaignTestStateNotAllowed(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Campaign state does not allow test send", "TEST_SEND_STATE_NOT_ALLOWED", nil)
	}
	if businessflow.IsCampaignTestRateLimited(err) {
		return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Please wait before sending another test message", "TEST_SEND_RATE_LIMITED", nil)
	}
	if businessflow.IsCampaignTestCooldownUnavailable(err) {
		return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Test send cooldown is temporarily unavailable", "TEST_SEND_COOLDOWN_UNAVAILABLE", nil)
	}
	if businessflow.IsCampaignLineNumberNotApplicable(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is only applicable for sms campaigns", "LINE_NUMBER_NOT_APPLICABLE", nil)
	}
	if businessflow.IsCampaignLineNumberRequired(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is required for sms campaigns", "LINE_NUMBER_REQUIRED", nil)
	}
	if businessflow.IsLineNumberNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number not found", "LINE_NUMBER_NOT_FOUND", nil)
	}
	if businessflow.IsLineNumberNotActive(err) || businessflow.IsCampaignLineNumberNotActive(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Line number is not active", "LINE_NUMBER_NOT_ACTIVE", nil)
	}

	if businessflow.IsCampaignMediaNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Media not found", "MEDIA_NOT_FOUND", nil)
	}
	if businessflow.IsCampaignTargetAudienceExcelMediaNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Excel media not found", "EXCEL_MEDIA_NOT_FOUND", nil)
	}
	if businessflow.IsCampaignTargetAudienceExcelFileInvalid(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Excel file is invalid", "EXCEL_FILE_INVALID", nil)
	}
	if businessflow.IsSegmentPriceFactorNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Segment price factor not found", "SEGMENT_PRICE_FACTOR_NOT_FOUND", nil)
	}
	if businessflow.IsLevel3Required(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "At least one level3 option is required", "LEVEL3_REQUIRED", nil)
	}

	if businessflow.IsCampaignTitleRequired(err) ||
		businessflow.IsCampaignContentRequired(err) ||
		businessflow.IsCampaignLevel1Required(err) ||
		businessflow.IsCampaignLevel2sRequired(err) ||
		businessflow.IsCampaignLevel3sRequired(err) ||
		businessflow.IsCampaignBudgetRequired(err) ||
		businessflow.IsCampaignBudgetOutOfRange(err) ||
		businessflow.IsCampaignSexRequired(err) ||
		businessflow.IsCampaignAdLinkRequired(err) ||
		businessflow.IsCampaignCityRequired(err) ||
		businessflow.IsCampaignTagsRequired(err) ||
		businessflow.IsCampaignUUIDRequired(err) ||
		businessflow.IsCampaignUpdateRequired(err) ||
		businessflow.IsInvalidShortLinkDomain(err) ||
		businessflow.IsAgencyCategoryJobRequired(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Campaign validation failed", "CAMPAIGN_VALIDATION_FAILED", nil)
	}
	if businessflow.IsScheduleTimeNotPresent(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Schedule time is required", "SCHEDULE_TIME_REQUIRED", nil)
	}
	if businessflow.IsScheduleTimeTooSoon(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Schedule time must be at least 10 minutes in the future", "SCHEDULE_TIME_TOO_SOON", nil)
	}
	if businessflow.IsScheduleTimeOutsideWindow(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Schedule time must be between 08:00 and 21:00 Asia/Tehran", "SCHEDULE_TIME_OUTSIDE_WINDOW", nil)
	}
	if businessflow.IsCampaignRescheduleNotAllowed(err) {
		return h.ErrorResponse(c, fiber.StatusConflict, "Campaign cannot be rescheduled in current status", "CAMPAIGN_RESCHEDULE_NOT_ALLOWED", nil)
	}
	if businessflow.IsPlatformBasePriceNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Platform base price not found", "PLATFORM_BASE_PRICE_NOT_FOUND", nil)
	}
	if businessflow.IsPagePriceNotFound(err) {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Page price not found", "PAGE_PRICE_NOT_FOUND", nil)
	}

	if businessErr != nil && businessErr.Code != "" {
		message := defaultMessage
		if businessErr.Message != "" {
			message = businessErr.Message
		}
		return h.ErrorResponse(c, defaultStatus, message, businessErr.Code, nil)
	}
	return h.ErrorResponse(c, defaultStatus, defaultMessage, defaultCode, nil)
}
