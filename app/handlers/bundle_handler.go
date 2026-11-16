package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/xuri/excelize/v2"
)

type BundleHandlerInterface interface {
	Create(c fiber.Ctx) error
	Update(c fiber.Ctx) error
	Get(c fiber.Ctx) error
	List(c fiber.Ctx) error
	RequestTagEvaluation(c fiber.Ctx) error
	GetTagEvaluationStatus(c fiber.Ctx) error
	ListTagScores(c fiber.Ctx) error
	DownloadActionFileTemplate(c fiber.Ctx) error
	UploadActionFile(c fiber.Ctx) error
	ListActionFiles(c fiber.Ctx) error
	GetActionFile(c fiber.Ctx) error
	DeleteActionFile(c fiber.Ctx) error
	GetActionSummary(c fiber.Ctx) error
	GetActionTagMetrics(c fiber.Ctx) error
}

type BundleHandler struct {
	flow           businessflow.BundleFlow
	evaluationFlow businessflow.BundleTagEvaluationFlow
	actionFlow     businessflow.BundleActionFlow
	validator      *validator.Validate
}

func (h *BundleHandler) actionBundleRequest(c fiber.Ctx) (uint, uint, error) {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok || customerID == 0 {
		return 0, 0, fmt.Errorf("customer id missing")
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return 0, 0, fmt.Errorf("invalid bundle id")
	}
	return customerID, uint(id), nil
}

func actionFileDTO(row *models.BundleActionFile) dto.BundleActionFileItem {
	return dto.BundleActionFileItem{ID: row.ID, OriginalFileName: row.OriginalFileName, Status: string(row.Status), TotalRowCount: row.TotalRowCount, UniqueUIDCount: row.UniqueUIDCount, NewActionUIDCount: row.NewActionUIDCount, DuplicateInFileCount: row.DuplicateInFileCount, DuplicateInOtherFilesCount: row.DuplicateInOtherFilesCount, InvalidUIDCount: row.InvalidUIDCount, OutsideBundleCount: row.OutsideBundleCount, UnassignedTagCount: row.UnassignedTagCount, MissingDeliveryCount: row.MissingDeliveryCount, EligibleActionUIDCount: row.EligibleActionUIDCount, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt, ProcessedAt: row.ProcessedAt}
}

// DownloadActionFileTemplate returns a minimal XLSX template whose only
// required column is uid.
func (h *BundleHandler) DownloadActionFileTemplate(c fiber.Ctx) error {
	if _, _, err := h.actionBundleRequest(c); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	xl := excelize.NewFile()
	defer xl.Close()
	sheet := xl.GetSheetName(0)
	if err := xl.SetCellValue(sheet, "A1", "uid"); err != nil {
		return h.ErrorResponse(c, 500, "Failed to create action-file template", "ACTION_TEMPLATE_FAILED", nil)
	}
	buf, err := xl.WriteToBuffer()
	if err != nil {
		return h.ErrorResponse(c, 500, "Failed to create action-file template", "ACTION_TEMPLATE_FAILED", nil)
	}
	c.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Set("Content-Disposition", "attachment; filename=\"bundle_action_template.xlsx\"")
	return c.Send(buf.Bytes())
}
func (h *BundleHandler) UploadActionFile(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	file, err := c.FormFile("file")
	if err != nil {
		return h.ErrorResponse(c, 400, "Action file is required", "ACTION_FILE_REQUIRED", nil)
	}
	opened, err := file.Open()
	if err != nil {
		return h.ErrorResponse(c, 400, "Action file is invalid", "ACTION_FILE_INVALID", nil)
	}
	defer opened.Close()
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-files", 30*time.Second)
	defer cancel()
	row, err := h.actionFlow.Upload(ctx, customer, bundle, file.Filename, io.Reader(opened))
	if err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to upload action file", "ACTION_FILE_UPLOAD_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusAccepted, "Action file queued for processing", actionFileDTO(row))
}
func (h *BundleHandler) ListActionFiles(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	page, limit := 1, 50
	if q := c.Query("page"); q != "" {
		if page, err = strconv.Atoi(q); err != nil || page < 1 {
			return h.ErrorResponse(c, 400, "Invalid page", "INVALID_PAGE", nil)
		}
	}
	if q := c.Query("limit"); q != "" {
		if limit, err = strconv.Atoi(q); err != nil || limit < 1 || limit > 100 {
			return h.ErrorResponse(c, 400, "Invalid limit", "INVALID_LIMIT", nil)
		}
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-files", 30*time.Second)
	defer cancel()
	rows, total, err := h.actionFlow.List(ctx, customer, bundle, page, limit)
	if err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to list action files", "ACTION_FILE_LIST_FAILED")
	}
	items := make([]dto.BundleActionFileItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, actionFileDTO(row))
	}
	return h.SuccessResponse(c, 200, "Action files retrieved", dto.BundleActionFilesResponse{Items: items, Pagination: dto.PaginationInfo{Page: page, Limit: limit, Total: total, TotalPages: int((total + int64(limit) - 1) / int64(limit))}})
}
func (h *BundleHandler) GetActionFile(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	id, err := strconv.ParseInt(c.Params("file_id"), 10, 64)
	if err != nil || id < 1 {
		return h.ErrorResponse(c, 400, "Invalid action file ID", "INVALID_ACTION_FILE_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-files/:file_id", 30*time.Second)
	defer cancel()
	row, err := h.actionFlow.Get(ctx, customer, bundle, id)
	if err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to get action file", "ACTION_FILE_GET_FAILED")
	}
	return h.SuccessResponse(c, 200, "Action file retrieved", actionFileDTO(row))
}
func (h *BundleHandler) DeleteActionFile(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	id, err := strconv.ParseInt(c.Params("file_id"), 10, 64)
	if err != nil || id < 1 {
		return h.ErrorResponse(c, 400, "Invalid action file ID", "INVALID_ACTION_FILE_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-files/:file_id", 30*time.Second)
	defer cancel()
	if err := h.actionFlow.Delete(ctx, customer, bundle, id); err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to delete action file", "ACTION_FILE_DELETE_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusAccepted, "Action-file deletion queued", nil)
}
func (h *BundleHandler) GetActionSummary(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-summary", 30*time.Second)
	defer cancel()
	x, err := h.actionFlow.Summary(ctx, customer, bundle)
	if err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to get action summary", "ACTION_SUMMARY_GET_FAILED")
	}
	return h.SuccessResponse(c, 200, "Action summary retrieved", dto.BundleActionSummaryResponse{BundleID: x.BundleID, HasActiveActionFiles: x.HasActiveActionFiles, ActionCount: x.ActionCount, EligibleDeliveredCount: x.EligibleDeliveredCount, BundleAvgATR: x.BundleAvgATR, UpdatedAt: x.UpdatedAt})
}
func (h *BundleHandler) GetActionTagMetrics(c fiber.Ctx) error {
	customer, bundle, err := h.actionBundleRequest(c)
	if err != nil {
		return h.ErrorResponse(c, 400, "Invalid bundle request", "INVALID_BUNDLE_ID", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/action-tag-metrics", 30*time.Second)
	defer cancel()
	rows, err := h.actionFlow.TagMetrics(ctx, customer, bundle)
	if err != nil {
		return h.handleBundleFlowError(c, err, 500, "Failed to get tag action metrics", "ACTION_TAG_METRICS_GET_FAILED")
	}
	items := make([]dto.BundleActionTagMetricItem, 0, len(rows))
	for _, x := range rows {
		items = append(items, dto.BundleActionTagMetricItem{TagID: x.TagID, TestActionCount: x.TestActionCount, TestEligibleDeliveredCount: x.TestEligibleDeliveredCount, TestPhaseAvgATR: x.TestPhaseAvgATR, OverallActionCount: x.OverallActionCount, OverallEligibleDeliveredCount: x.OverallEligibleDeliveredCount, OverallAvgATR: x.OverallAvgATR})
	}
	return h.SuccessResponse(c, 200, "Tag action metrics retrieved", items)
}

func NewBundleHandler(flow businessflow.BundleFlow, evaluationFlow businessflow.BundleTagEvaluationFlow, actionFlow businessflow.BundleActionFlow) *BundleHandler {
	return &BundleHandler{
		flow:           flow,
		evaluationFlow: evaluationFlow,
		actionFlow:     actionFlow,
		validator:      validator.New(),
	}
}

func (h *BundleHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *BundleHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// Create creates a bundle for the authenticated customer.
// @Summary Create bundle
// @Description Create a new bundle for the authenticated customer
// @Tags Bundles
// @Accept json
// @Produce json
// @Param request body dto.CreateBundleRequest true "Bundle payload"
// @Success 201 {object} dto.APIResponse{data=dto.CreateBundleResponse} "Created"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles [post]
func (h *BundleHandler) Create(c fiber.Ctx) error {
	var req dto.CreateBundleRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, e := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(e))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles", 30*time.Second)
	defer cancel()

	res, err := h.flow.CreateBundle(ctx, &req, metadata)
	if err != nil {
		log.Println("Create bundle failed", err)
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to create bundle", "CREATE_BUNDLE_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Bundle created successfully", res)
}

// Update updates a bundle for the authenticated customer.
// @Summary Update bundle
// @Description Update an existing bundle for the authenticated customer
// @Tags Bundles
// @Accept json
// @Produce json
// @Param id path int true "Bundle ID"
// @Param request body dto.UpdateBundleRequest true "Bundle payload"
// @Success 200 {object} dto.APIResponse{data=dto.UpdateBundleResponse} "Updated"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles/{id} [put]
func (h *BundleHandler) Update(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}

	var req dto.UpdateBundleRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID
	req.ID = uint(id)

	if err := h.validator.Struct(&req); err != nil {
		var validationErrors []string
		for _, e := range err.(validator.ValidationErrors) {
			validationErrors = append(validationErrors, getValidationErrorMessage(e))
		}
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", validationErrors)
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id", 30*time.Second)
	defer cancel()

	res, err := h.flow.UpdateBundle(ctx, &req, metadata)
	if err != nil {
		log.Println("Update bundle failed", err)
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to update bundle", "UPDATE_BUNDLE_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Bundle updated successfully", res)
}

// Get retrieves one bundle for the authenticated customer.
// @Summary Get bundle
// @Description Get a single bundle by ID for the authenticated customer
// @Tags Bundles
// @Produce json
// @Param id path int true "Bundle ID"
// @Success 200 {object} dto.APIResponse{data=dto.GetBundleResponse} "Retrieved"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles/{id} [get]
func (h *BundleHandler) Get(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	req := dto.GetBundleRequest{
		CustomerID: customerID,
		ID:         uint(id),
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id", 30*time.Second)
	defer cancel()

	res, err := h.flow.GetBundle(ctx, &req, metadata)
	if err != nil {
		log.Println("Get bundle failed", err)
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to get bundle", "GET_BUNDLE_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Bundle retrieved successfully", res)
}

// List lists bundles for the authenticated customer.
// @Summary List bundles
// @Description List bundles for the authenticated customer with optional filters and pagination
// @Tags Bundles
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(10)
// @Param title query string false "Title filter"
// @Param target_customer_name query string false "Target customer name filter"
// @Success 200 {object} dto.APIResponse{data=dto.ListBundlesResponse} "Retrieved"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles [get]
func (h *BundleHandler) List(c fiber.Ctx) error {
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		parsed, err := strconv.Atoi(pageStr)
		if err != nil || parsed <= 0 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
		}
		page = parsed
	}

	limit := 10
	if limitStr := c.Query("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed <= 0 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid limit", "INVALID_LIMIT", nil)
		}
		limit = parsed
	}

	var filter *dto.ListBundlesFilter
	title := c.Query("title")
	targetCustomerName := c.Query("target_customer_name")
	if title != "" || targetCustomerName != "" {
		filter = &dto.ListBundlesFilter{}
		if title != "" {
			filter.Title = &title
		}
		if targetCustomerName != "" {
			filter.TargetCustomerName = &targetCustomerName
		}
	}

	req := dto.ListBundlesRequest{
		CustomerID: customerID,
		Page:       page,
		Limit:      limit,
		Filter:     filter,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles", 30*time.Second)
	defer cancel()

	res, err := h.flow.ListBundles(ctx, &req, metadata)
	if err != nil {
		log.Println("List bundles failed", err)
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to list bundles", "LIST_BUNDLES_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Bundles retrieved successfully", fiber.Map{
		"message":    res.Message,
		"items":      res.Items,
		"pagination": res.Pagination,
	})
}

// RequestTagEvaluation queues a smart tag evaluation for the authenticated customer's bundle.
// @Summary Request bundle tag evaluation
// @Description Start an async smart tag evaluation run for the authenticated customer's bundle
// @Tags Bundles
// @Produce json
// @Param id path int true "Bundle ID"
// @Success 202 {object} dto.APIResponse{data=dto.RequestBundleTagEvaluationResponse} "Accepted"
// @Failure 400 {object} dto.APIResponse "Invalid bundle ID"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 409 {object} dto.APIResponse "Evaluation already active or feature disabled"
// @Failure 429 {object} dto.APIResponse "Daily evaluation limit reached"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles/{id}/tag-evaluations [post]
func (h *BundleHandler) RequestTagEvaluation(c fiber.Ctx) error {
	id, err := parsePositiveUintParam(c.Params("id"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/tag-evaluations", 30*time.Second)
	defer cancel()

	res, err := h.evaluationFlow.RequestBundleTagEvaluation(ctx, &dto.RequestBundleTagEvaluationRequest{
		CustomerID: customerID,
		BundleID:   id,
	}, businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent")))
	if err != nil {
		var dailyLimitErr *businessflow.BundleTagEvaluationDailyLimitError
		if errors.As(err, &dailyLimitErr) {
			retryAfter := time.Until(dailyLimitErr.ResetsAt)
			if retryAfter > 0 {
				c.Set("Retry-After", strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
			}
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Daily bundle tag evaluation limit reached", "CUSTOMER_DAILY_EVALUATION_LIMIT_REACHED", dailyLimitErr)
		}
		var conflictErr *businessflow.BundleTagEvaluationConflictError
		if errors.As(err, &conflictErr) {
			return c.Status(fiber.StatusConflict).JSON(dto.APIResponse{
				Success: false,
				Message: "Bundle tag evaluation is already active",
				Error: dto.ErrorDetail{
					Code:    "BUNDLE_TAG_EVALUATION_ACTIVE",
					Details: conflictErr.Response,
				},
			})
		}
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to request bundle tag evaluation", "REQUEST_BUNDLE_TAG_EVALUATION_FAILED")
	}

	return h.SuccessResponse(c, fiber.StatusAccepted, "Bundle tag evaluation requested successfully", res)
}

// GetTagEvaluationStatus returns the current derived smart tag evaluation status for the bundle.
// @Summary Get bundle tag evaluation status
// @Description Retrieve the current smart tag evaluation status for the authenticated customer's bundle
// @Tags Bundles
// @Produce json
// @Param id path int true "Bundle ID"
// @Success 200 {object} dto.APIResponse{data=dto.GetBundleTagEvaluationStatusResponse} "Retrieved"
// @Failure 400 {object} dto.APIResponse "Invalid bundle ID"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles/{id}/tag-evaluation [get]
func (h *BundleHandler) GetTagEvaluationStatus(c fiber.Ctx) error {
	id, err := parsePositiveUintParam(c.Params("id"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/tag-evaluation", 30*time.Second)
	defer cancel()

	res, err := h.evaluationFlow.GetBundleTagEvaluationStatus(ctx, &dto.GetBundleTagEvaluationStatusRequest{
		CustomerID: customerID,
		BundleID:   id,
	}, businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent")))
	if err != nil {
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to get bundle tag evaluation status", "GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Bundle tag evaluation status retrieved successfully", res)
}

// ListTagScores returns the current smart tag scores for the authenticated customer's bundle.
// @Summary List bundle tag scores
// @Description Retrieve paginated current smart tag scores for the authenticated customer's bundle
// @Tags Bundles
// @Produce json
// @Param id path int true "Bundle ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} dto.APIResponse{data=dto.ListBundleTagScoresResponse} "Retrieved"
// @Failure 400 {object} dto.APIResponse "Invalid bundle ID or pagination"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 403 {object} dto.APIResponse "Forbidden"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bundles/{id}/tag-scores [get]
func (h *BundleHandler) ListTagScores(c fiber.Ctx) error {
	id, err := parsePositiveUintParam(c.Params("id"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid bundle ID", "INVALID_BUNDLE_ID", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		parsed, parseErr := strconv.Atoi(pageStr)
		if parseErr != nil || parsed <= 0 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid page", "INVALID_PAGE", nil)
		}
		page = parsed
	}
	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		parsed, parseErr := strconv.Atoi(limitStr)
		if parseErr != nil || parsed <= 0 {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid limit", "INVALID_LIMIT", nil)
		}
		limit = parsed
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bundles/:id/tag-scores", 30*time.Second)
	defer cancel()

	res, err := h.evaluationFlow.ListBundleTagScores(ctx, &dto.ListBundleTagScoresRequest{
		CustomerID: customerID,
		BundleID:   id,
		Page:       page,
		Limit:      limit,
	}, businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent")))
	if err != nil {
		return h.handleBundleFlowError(c, err, fiber.StatusInternalServerError, "Failed to list bundle tag scores", "LIST_BUNDLE_TAG_SCORES_FAILED")
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Bundle tag scores retrieved successfully", res)
}

func (h *BundleHandler) handleBundleFlowError(c fiber.Ctx, err error, defaultStatus int, defaultMessage, defaultCode string) error {
	if be, ok := err.(*businessflow.BusinessError); ok {
		switch be.Code {
		case "CREATE_BUNDLE_VALIDATION_FAILED", "UPDATE_BUNDLE_VALIDATION_FAILED":
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Bundle validation failed", be.Code, be.Error())
		case "BUNDLE_NOT_FOUND":
			return h.ErrorResponse(c, fiber.StatusNotFound, "Bundle not found", be.Code, nil)
		case "BUNDLE_ACCESS_DENIED":
			return h.ErrorResponse(c, fiber.StatusForbidden, "Bundle access denied", be.Code, nil)
		case "SMART_TAG_EVALUATION_DISABLED":
			return h.ErrorResponse(c, fiber.StatusConflict, "Smart tag evaluation is disabled", be.Code, nil)
		case "GET_BUNDLE_FAILED", "LIST_BUNDLES_FAILED", "CREATE_BUNDLE_FAILED", "UPDATE_BUNDLE_FAILED", "GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED", "LIST_BUNDLE_TAG_SCORES_FAILED", "REQUEST_BUNDLE_TAG_EVALUATION_FAILED":
			return h.ErrorResponse(c, defaultStatus, defaultMessage, be.Code, nil)
		case "MISSING_CUSTOMER_ID":
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found", be.Code, nil)
		case "CUSTOMER_NOT_FOUND":
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", be.Code, nil)
		}
	}

	return h.ErrorResponse(c, defaultStatus, defaultMessage, defaultCode, nil)
}

func (h *BundleHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	return ctx, cancel
}

func parsePositiveUintParam(raw string) (uint, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return uint(id), nil
}
