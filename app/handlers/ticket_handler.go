// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
package handlers

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
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

// TicketHandlerInterface defines the contract for ticket handlers
type TicketHandlerInterface interface {
	Create(c fiber.Ctx) error
	CreateResponse(c fiber.Ctx) error
	List(c fiber.Ctx) error
	DownloadAttachment(c fiber.Ctx) error
	AdminCreateResponse(c fiber.Ctx) error
	AdminList(c fiber.Ctx) error
}

// TicketHandler handles ticket-related HTTP requests
type TicketHandler struct {
	flow      businessflow.TicketFlow
	validator *validator.Validate
}

func (h *TicketHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *TicketHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// NewTicketHandler creates a new ticket handler
func NewTicketHandler(flow businessflow.TicketFlow) *TicketHandler {
	h := &TicketHandler{
		flow:      flow,
		validator: validator.New(),
	}
	return h
}

func getFirstFile(files []*multipart.FileHeader) *multipart.FileHeader {
	if len(files) == 0 {
		return nil
	}
	return files[0]
}

// Create Ticket
// @Description Create a new support ticket for the authenticated customer. Supports multipart form upload or JSON.
// @Tags Tickets
// @Accept mpfd
// @Accept json
// @Produce json
// @Param title formData string false "Ticket title (<=80 chars)"
// @Param content formData string false "Ticket content (<=1000 chars)"
// @Param file formData file false "Attachment (jpg/png/pdf/docx/xlsx/zip, <=10MB)"
// @Param request body dto.CreateTicketRequest false "JSON alternative for creating ticket"
// @Success 201 {object} dto.APIResponse{data=dto.CreateTicketResponse} "Ticket created successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/tickets [post]
func (h *TicketHandler) Create(c fiber.Ctx) error {
	contentType := c.Get("Content-Type")
	var req dto.CreateTicketRequest
	var savedPath *string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart
		if form, err := c.MultipartForm(); err == nil && form != nil {
			req.Title = c.FormValue("title")
			req.Content = c.FormValue("content")
			if fileHeader := getFirstFile(form.File["file"]); fileHeader != nil {
				p, err := h.saveUploadedFile(fileHeader)
				if err != nil {
					return h.ErrorResponse(c, fiber.StatusBadRequest, "File upload failed", "FILE_UPLOAD_FAILED", err.Error())
				}
				savedPath = &p
				req.AttachedFileName = &fileHeader.Filename
			}
		}
	} else {
		// JSON
		if err := c.Bind().JSON(&req); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
		}
	}

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID
	req.SavedFilePath = savedPath

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/tickets", 30*time.Second)
	defer cancel()
	result, err := h.flow.CreateTicket(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_TITLE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid title", be.Code, be.Error())
			case "INVALID_CONTENT":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid content", be.Code, be.Error())
			case "INVALID_FILE_TYPE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file type", be.Code, be.Error())
			case "FILE_TOO_LARGE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large", be.Code, be.Error())
			case "CREATE_TICKET_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create ticket", be.Code, nil)
			case "FILE_DOWNLOAD_FAILED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File download failed", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create ticket", "CREATE_TICKET_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Ticket created successfully", result)
}

// CreateResponse Ticket
// @Description Create a response to an existing ticket. Customer can reply to their own tickets. Supports multipart form upload or JSON.
// @Tags Tickets
// @Accept mpfd
// @Accept json
// @Produce json
// @Param ticket_id formData integer false "Original ticket ID to respond to"
// @Param content formData string false "Response content (<=1000 chars)"
// @Param file formData file false "Attachment (jpg/png/pdf/docx/xlsx/zip, <=10MB)"
// @Param request body dto.CreateResponseTicketRequest false "JSON alternative for creating response"
// @Success 201 {object} dto.APIResponse{data=dto.CreateResponseTicketResponse} "Response ticket created successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 403 {object} dto.APIResponse "Forbidden - ticket does not belong to customer"
// @Failure 404 {object} dto.APIResponse "Ticket not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/tickets/reply [post]
func (h *TicketHandler) CreateResponse(c fiber.Ctx) error {
	contentType := c.Get("Content-Type")
	var req dto.CreateResponseTicketRequest
	var savedPath *string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart
		if form, err := c.MultipartForm(); err == nil && form != nil {
			ticketIDStr := c.FormValue("ticket_id")
			if ticketIDStr != "" {
				if tid, err := strconv.ParseUint(ticketIDStr, 10, 64); err == nil {
					req.TicketID = uint(tid)
				}
			}
			req.Content = c.FormValue("content")
			if fileHeader := getFirstFile(form.File["file"]); fileHeader != nil {
				p, err := h.saveUploadedFile(fileHeader)
				if err != nil {
					return h.ErrorResponse(c, fiber.StatusBadRequest, "File upload failed", "FILE_UPLOAD_FAILED", err.Error())
				}
				savedPath = &p
				req.AttachedFileName = &fileHeader.Filename
			}
		}
	} else {
		// JSON
		if err := c.Bind().JSON(&req); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
		}
	}

	// Get authenticated customer ID from context
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}
	req.CustomerID = customerID
	req.SavedFilePath = savedPath

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/tickets/reply", 30*time.Second)
	defer cancel()
	result, err := h.flow.CreateResponseTicket(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsTicketNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Ticket not found", "TICKET_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "FORBIDDEN":
				return h.ErrorResponse(c, fiber.StatusForbidden, "You can only respond to your own tickets", be.Code, be.Error())
			case "INVALID_CONTENT":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid content", be.Code, be.Error())
			case "INVALID_FILE_TYPE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file type", be.Code, be.Error())
			case "FILE_TOO_LARGE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large", be.Code, be.Error())
			case "FILE_DOWNLOAD_FAILED":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File download failed", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create response ticket", "CREATE_RESPONSE_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Response ticket created successfully", result)
}

// List Tickets
// @Description List customer's tickets grouped by correlation ID (each group sorted by id DESC)
// @Tags Tickets
// @Accept json
// @Produce json
// @Param title query string false "Exact title filter"
// @Param start_date query string false "Start date (RFC3339)"
// @Param end_date query string false "End date (RFC3339)"
// @Param page query integer false "Page number (default: 1)"
// @Param page_size query integer false "Items per page (default: 20, max: 100)"
// @Success 200 {object} dto.APIResponse{data=dto.ListTicketsResponse} "Tickets retrieved successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/tickets [get]
func (h *TicketHandler) List(c fiber.Ctx) error {
	// Auth
	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	// Parse filters
	var (
		title     *string
		startDate *time.Time
		endDate   *time.Time
	)
	if t := c.Query("title"); t != "" {
		title = &t
	}
	if s := c.Query("start_date"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			startDate = &parsed
		}
	}
	if e := c.Query("end_date"); e != "" {
		if parsed, err := time.Parse(time.RFC3339, e); err == nil {
			endDate = &parsed
		}
	}

	var page, pageSize uint
	if v := c.Query("page"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			page = uint(n)
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			pageSize = uint(n)
		}
	}

	req := &dto.ListTicketsRequest{
		CustomerID: customerID,
		Title:      title,
		StartDate:  startDate,
		EndDate:    endDate,
		Page:       page,
		PageSize:   pageSize,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/tickets", 30*time.Second)
	defer cancel()
	result, err := h.flow.ListTickets(ctx, req, metadata)
	if err != nil {
		if businessflow.IsStartDateAfterEndDate(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Start date must be before end date", "START_DATE_AFTER_END_DATE", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "LIST_TICKETS_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list tickets", be.Code, nil)
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list tickets", "LIST_TICKETS_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Tickets retrieved successfully", result)
}

// DownloadAttachment Ticket
// @Description Download a specific attachment from a customer's ticket.
// @Tags Tickets
// @Produce application/octet-stream
// @Param ticket_id path integer true "Ticket ID"
// @Param file_index path integer true "Attachment index"
// @Success 200 {string} string "Attachment file"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized - customer not found or inactive"
// @Failure 403 {object} dto.APIResponse "Forbidden - ticket does not belong to customer"
// @Failure 404 {object} dto.APIResponse "Ticket or attachment not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/tickets/{ticket_id}/attachments/{file_index} [get]
func (h *TicketHandler) DownloadAttachment(c fiber.Ctx) error {
	ticketID, err := strconv.ParseUint(c.Params("ticket_id"), 10, 64)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ticket ID", "INVALID_TICKET_ID", err.Error())
	}
	fileIndex, err := strconv.Atoi(c.Params("file_index"))
	if err != nil || fileIndex < 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file index", "INVALID_FILE_INDEX", nil)
	}

	customerID, ok := c.Locals("customer_id").(uint)
	if !ok {
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer ID not found in context", "MISSING_CUSTOMER_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/tickets/{ticket_id}/attachments/{file_index}", 30*time.Second)
	defer cancel()
	filename, contentType, data, err := h.flow.DownloadTicketAttachment(
		ctx,
		customerID,
		uint(ticketID),
		fileIndex,
	)
	if err != nil {
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Customer account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsTicketNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Ticket not found", "TICKET_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "FORBIDDEN":
				return h.ErrorResponse(c, fiber.StatusForbidden, "You can only download your own ticket attachments", be.Code, be.Error())
			case "ATTACHMENT_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Attachment not found", be.Code, nil)
			case "INVALID_ATTACHMENT_PATH":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid attachment path", be.Code, nil)
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to download attachment", "DOWNLOAD_ATTACHMENT_FAILED", nil)
	}

	c.Set("Content-Disposition", "attachment; filename="+filename)
	c.Set("Content-Type", contentType)
	return c.Send(data)
}

// AdminCreateResponse Create Admin Response Ticket
// @Description Admin creates a response to an existing ticket; new ticket shares correlation ID with the original. Supports multipart or
// JSON.
// @Tags Tickets
// @Accept mpfd
// @Accept json
// @Produce json
// @Param ticket_id formData integer false "Original ticket ID"
// @Param content formData string false "Response content (<=1000 chars)"
// @Param file formData file false "Attachment (jpg/png/pdf/docx/xlsx/zip, <=10MB)"
// @Param request body dto.AdminCreateResponseTicketRequest false "JSON alternative payload"
// @Success 201 {object} dto.APIResponse{data=dto.AdminCreateResponseTicketResponse} "Admin response created successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Ticket not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/tickets/reply [post]
func (h *TicketHandler) AdminCreateResponse(c fiber.Ctx) error {
	contentType := c.Get("Content-Type")
	var req dto.AdminCreateResponseTicketRequest
	var savedPath *string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if form, err := c.MultipartForm(); err == nil && form != nil {
			if idStr := c.FormValue("ticket_id"); idStr != "" {
				id, err := strconv.ParseUint(idStr, 10, 64)
				if err != nil {
					return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ticket ID", "INVALID_TICKET_ID", err.Error())
				}
				req.TicketID = uint(id)
			}
			req.Content = c.FormValue("content")
			if fileHeader := getFirstFile(form.File["file"]); fileHeader != nil {
				p, err := h.saveUploadedFile(fileHeader)
				if err != nil {
					return h.ErrorResponse(c, fiber.StatusBadRequest, "File upload failed", "FILE_UPLOAD_FAILED", err.Error())
				}
				savedPath = &p
				req.AttachedFileName = &fileHeader.Filename
			}
		}
	} else {
		if err := c.Bind().JSON(&req); err != nil {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
		}
	}

	req.SavedFilePath = savedPath
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/tickets/reply", 30*time.Second)
	defer cancel()
	result, err := h.flow.AdminCreateResponseTicket(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsTicketNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Ticket not found", "TICKET_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_CONTENT":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid content", be.Code, be.Error())
			case "INVALID_FILE_TYPE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file type", be.Code, be.Error())
			case "FILE_TOO_LARGE":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "File too large", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create admin response", "CREATE_ADMIN_RESPONSE_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusCreated, "Admin response created successfully", result)
}

// AdminList Tickets
// @Description Admin lists tickets with optional filters
// @Tags Tickets
// @Accept json
// @Produce json
// @Param customer_id query integer false "Filter by customer id"
// @Param title query string false "Exact title filter"
// @Param start_date query string false "Start date (RFC3339)"
// @Param end_date query string false "End date (RFC3339)"
// @Param replied_by_admin query boolean false "Filter by replied flag"
// @Param page query integer false "Page number (default: 1)"
// @Param page_size query integer false "Items per page (default: 20, max: 100)"
// @Success 200 {object} dto.APIResponse{data=dto.AdminListTicketsResponse} "Admin tickets retrieved successfully"
// @Failure 400 {object} dto.APIResponse "Validation error or invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/tickets [get]
func (h *TicketHandler) AdminList(c fiber.Ctx) error {
	var (
		customerID *uint
		title      *string
		startDate  *time.Time
		endDate    *time.Time
		replied    *bool
		page       uint
		pageSize   uint
	)
	if v := c.Query("customer_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			u := uint(n)
			customerID = &u
		}
	}
	if v := c.Query("title"); v != "" {
		title = &v
	}
	if v := c.Query("start_date"); v != "" {
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			startDate = &parsed
		}
	}
	if v := c.Query("end_date"); v != "" {
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			endDate = &parsed
		}
	}
	if v := c.Query("replied_by_admin"); v != "" {
		lv := strings.ToLower(v)
		if lv == "true" || lv == "1" {
			b := true
			replied = &b
		} else if lv == "false" || lv == "0" {
			b := false
			replied = &b
		}
	}
	if v := c.Query("page"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			page = uint(n)
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			pageSize = uint(n)
		}
	}

	req := &dto.AdminListTicketsRequest{
		CustomerID:     customerID,
		Title:          title,
		StartDate:      startDate,
		EndDate:        endDate,
		RepliedByAdmin: replied,
		Page:           page,
		PageSize:       pageSize,
	}

	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/tickets", 30*time.Second)
	defer cancel()
	result, err := h.flow.AdminListTickets(ctx, req, metadata)
	if err != nil {
		if businessflow.IsStartDateAfterEndDate(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Start date must be before end date", "START_DATE_AFTER_END_DATE", nil)
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list tickets", "ADMIN_LIST_TICKETS_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Admin tickets retrieved successfully", result)
}

func (h *TicketHandler) createRequestContextWithTimeout(
	c fiber.Ctx,
	endpoint string,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	return ctx, cancel
}

// saveUploadedFile writes multipart upload to disk under data/uploads/tickets/YYYY-MM-DD/
func (h *TicketHandler) saveUploadedFile(fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", fmt.Errorf("no file provided")
	}
	// Extension validation (basic)
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	switch ext {
	case ".jpg", ".png", ".pdf", ".docx", ".xlsx", ".zip":
	default:
		return "", fmt.Errorf("invalid file type")
	}
	// 10MB limit
	if fileHeader.Size > 10*1024*1024 {
		return "", fmt.Errorf("file too large")
	}

	dateDir := utils.UTCNow().Format("2006-01-02")
	baseDir := filepath.Join("data", "uploads", "tickets", dateDir)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	fname := uuid.New().String() + ext
	fullPath := filepath.Join(baseDir, fname)

	src, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(fullPath)
		return "", err
	}

	return filepath.ToSlash(filepath.Join("data", "uploads", "tickets", dateDir, fname)), nil
}
