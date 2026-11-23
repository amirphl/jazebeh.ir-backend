package handlers

import (
	"context"
	"log"
	"mime/multipart"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

const (
	shortLinkUploadTimeout   = 30 * time.Minute
	shortLinkDownloadTimeout = 5 * time.Minute
)

// ShortLinkAdminHandlerInterface defines admin endpoints for short links (CSV upload and downloads)
type ShortLinkAdminHandlerInterface interface {
	UploadCSV(c fiber.Ctx) error
	UploadStatus(c fiber.Ctx) error
	DownloadByScenario(c fiber.Ctx) error
	DownloadWithClicksByScenario(c fiber.Ctx) error
	DownloadWithClicksByScenarioRange(c fiber.Ctx) error
	DownloadWithClicksByScenarioNameExcel(c fiber.Ctx) error
}

// ShortLinkAdminHandler implements the admin short link endpoints
type ShortLinkAdminHandler struct {
	uploadFlow  businessflow.AdminShortLinkFlow
	downloadAll businessflow.AdminShortLinkFlow
	downloadHit businessflow.AdminShortLinkFlow
	validator   *validator.Validate
}

func NewShortLinkAdminHandler(uploadFlow businessflow.AdminShortLinkFlow, downloadAll businessflow.AdminShortLinkFlow, downloadHit businessflow.AdminShortLinkFlow) ShortLinkAdminHandlerInterface {
	return &ShortLinkAdminHandler{
		uploadFlow:  uploadFlow,
		downloadAll: downloadAll,
		downloadHit: downloadHit,
		validator:   validator.New(),
	}
}

func (h *ShortLinkAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, code string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: code, Details: details}})
}

// UploadCSV accepts a multipart/form-data with file (CSV), short_link_domain, and scenario_name fields
// @Summary Admin Upload Short Links CSV
// @Tags Admin ShortLinks
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file with long_link column"
// @Param short_link_domain formData string true "Domain for short links (e.g., https://j0in.ir)"
// @Param scenario_name formData string true "Scenario name for grouping (stored in short_links.scenario_name)"
// @Success 201 {object} dto.APIResponse{data=dto.AdminCreateShortLinksResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/upload-csv [post]
func (h *ShortLinkAdminHandler) UploadCSV(c fiber.Ctx) error {
	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader == nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "file is required", "INVALID_REQUEST", nil)
	}
	domain := c.FormValue("short_link_domain")
	scenarioName := strings.TrimSpace(c.FormValue("scenario_name"))
	if scenarioName == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "scenario_name is required", "VALIDATION_ERROR", nil)
	}
	fh, err := openFormFile(fileHeader)
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "invalid file", "INVALID_FILE", err.Error())
	}
	defer fh.Close()
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/short-links/upload-csv", shortLinkUploadTimeout)
	defer cancel()
	res, flowErr := h.uploadFlow.CreateShortLinksFromCSV(ctx, fh, domain, scenarioName)
	if flowErr != nil {
		log.Println("Admin upload short links failed:", flowErr)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create short links", "CREATE_SHORT_LINKS_FAILED", nil)
	}
	_ = metadata // reserved for audit if needed later
	status := fiber.StatusAccepted
	if res.Job.Status == "completed" {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(dto.APIResponse{Success: true, Message: res.Message, Data: res})
}

// UploadStatus returns the current status and progress of a short-link CSV upload job.
// @Summary Get Short Links CSV Upload Status
// @Tags Admin ShortLinks
// @Produce json
// @Param id path string true "Upload job ID"
// @Success 200 {object} dto.APIResponse{data=dto.AdminShortLinkUploadJobDTO}
// @Failure 404 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/upload-csv/{id} [get]
func (h *ShortLinkAdminHandler) UploadStatus(c fiber.Ctx) error {
	res, err := h.uploadFlow.UploadJob(c.Context(), c.Params("id"))
	if err != nil {
		return h.ErrorResponse(c, fiber.StatusNotFound, "Upload job not found", "UPLOAD_JOB_NOT_FOUND", nil)
	}
	return c.JSON(dto.APIResponse{Success: true, Message: "Upload job status", Data: res})
}

// DownloadByScenario posts scenario_id and returns CSV of all short links with that scenario
// @Summary Admin Download Short Links by Scenario
// @Tags Admin ShortLinks
// @Accept json
// @Produce text/csv
// @Param request body dto.AdminDownloadShortLinksRequest true "Scenario ID"
// @Success 200 {string} string "CSV file"
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/download [post]
func (h *ShortLinkAdminHandler) DownloadByScenario(c fiber.Ctx) error {
	var req dto.AdminDownloadShortLinksRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/short-links/download", shortLinkDownloadTimeout)
	defer cancel()
	filename, data, err := h.downloadAll.DownloadShortLinksCSV(ctx, req.ScenarioID)
	if err != nil {
		log.Println("Admin download short links failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to generate CSV", "DOWNLOAD_FAILED", nil)
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

// DownloadWithClicksByScenario posts scenario_id and returns CSV of short links that have at least one click
// @Summary Admin Download Short Links With Clicks by Scenario
// @Tags Admin ShortLinks
// @Accept json
// @Produce text/csv
// @Param request body dto.AdminDownloadShortLinksRequest true "Scenario ID"
// @Success 200 {string} string "CSV file"
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/download-with-clicks [post]
func (h *ShortLinkAdminHandler) DownloadWithClicksByScenario(c fiber.Ctx) error {
	var req dto.AdminDownloadShortLinksRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/short-links/download-with-clicks", shortLinkDownloadTimeout)
	defer cancel()
	filename, data, err := h.downloadHit.DownloadShortLinksWithClicksCSV(ctx, req.ScenarioID)
	if err != nil {
		log.Println("Admin download short links with clicks failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to generate CSV", "DOWNLOAD_FAILED", nil)
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

// DownloadWithClicksByScenarioRange posts scenario_from and scenario_to and returns CSV of short links with clicks in [from, to)
// @Summary Admin Download Short Links With Clicks by Scenario Range
// @Tags Admin ShortLinks
// @Accept json
// @Produce text/csv
// @Param request body dto.AdminDownloadShortLinksRangeRequest true "Scenario range [from, to)"
// @Success 200 {string} string "CSV file"
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/download-with-clicks-range [post]
func (h *ShortLinkAdminHandler) DownloadWithClicksByScenarioRange(c fiber.Ctx) error {
	var req dto.AdminDownloadShortLinksRangeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/short-links/download-with-clicks-range", shortLinkDownloadTimeout)
	defer cancel()
	filename, data, err := h.downloadHit.DownloadShortLinksWithClicksCSVRange(ctx, req.ScenarioFrom, req.ScenarioTo)
	if err != nil {
		log.Println("Admin download short links with clicks range failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to generate CSV", "DOWNLOAD_FAILED", nil)
	}
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

// DownloadWithClicksByScenarioNameExcel posts a regex for scenario_name and returns an Excel file with each sheet per scenario
// @Summary Admin Download Short Links With Clicks by Scenario Name Regex (Excel)
// @Tags Admin ShortLinks
// @Accept json
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param request body dto.AdminDownloadShortLinksByScenarioNameRegexRequest true "Scenario name regex"
// @Success 200 {string} string "Excel file"
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/admin/short-links/download-with-clicks-by-scenario-name [post]
func (h *ShortLinkAdminHandler) DownloadWithClicksByScenarioNameExcel(c fiber.Ctx) error {
	var req dto.AdminDownloadShortLinksByScenarioNameRegexRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/short-links/download-with-clicks-by-scenario-name", shortLinkDownloadTimeout)
	defer cancel()
	filename, data, err := h.downloadHit.DownloadShortLinksWithClicksExcelByScenarioNameRegex(ctx, req.ScenarioNameRegex)
	if err != nil {
		log.Println("Admin download short links with clicks by scenario_name failed:", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to generate Excel", "DOWNLOAD_FAILED", nil)
	}
	c.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

func openFormFile(fh *multipart.FileHeader) (multipart.File, error) {
	return fh.Open()
}

func (h *ShortLinkAdminHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	if adminID, ok := middleware.GetAdminIDFromContext(c); ok {
		ctx = context.WithValue(ctx, utils.AdminIDKey, adminID)
	}
	return ctx, cancel
}
