package handlers

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

// CampaignBotHandlerInterface defines the contract for bot campaign handlers
type CampaignBotHandlerInterface interface {
	ListReadyCampaigns(c fiber.Ctx) error
	MoveCampaignToExecuted(c fiber.Ctx) error
	MoveCampaignToRunning(c fiber.Ctx) error
	DownloadTargetAudienceExcelFile(c fiber.Ctx) error
	UpdateCampaignStatistics(c fiber.Ctx) error
	PushAudienceUIDs(c fiber.Ctx) error
}

// CampaignBotHandler handles bot campaign-related HTTP requests
type CampaignBotHandler struct {
	campaignFlow businessflow.BotCampaignFlow
	validator    *validator.Validate
}

func NewCampaignBotHandler(flow businessflow.BotCampaignFlow) CampaignBotHandlerInterface {
	h := &CampaignBotHandler{
		campaignFlow: flow,
		validator:    validator.New(),
	}
	return h
}

func (h *CampaignBotHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *CampaignBotHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// ListReadyCampaigns lists ready campaigns and marks them running
// @Summary Bot List Ready Campaigns
// @Tags Bot Campaigns
// @Produce json
// @Param platform query string false "Filter by platform (sms|rubika|bale|splus)"
// @Success 200 {object} dto.APIResponse{data=dto.BotListCampaignsResponse}
// @Router /api/v1/bot/campaigns/ready [get]
func (h *CampaignBotHandler) ListReadyCampaigns(c fiber.Ctx) error {
	var platformPtr *string
	if p := strings.TrimSpace(c.Query("platform")); p != "" {
		platformPtr = &p
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/ready", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.ListReadyCampaigns(ctx, platformPtr)
	if err != nil {
		if businessflow.IsCampaignPlatformInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid platform", "INVALID_PLATFORM", nil)
		}
		log.Println("List ready campaigns failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to list ready campaigns", "LIST_READY_CAMPAIGNS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Ready campaigns retrieved", res)
}

// MoveCampaignToExecuted updates status to executed
// @Summary Bot Move Campaign to Executed
// @Tags Bot Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} dto.APIResponse
// @Router /api/v1/bot/campaigns/{id}/executed [post]
func (h *CampaignBotHandler) MoveCampaignToExecuted(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", "INVALID_CAMPAIGN_ID", nil)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	_ = metadata
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/"+idStr+"/executed", 30*time.Second)
	defer cancel()
	if err := h.campaignFlow.MoveCampaignToExecuted(ctx, uint(id)); err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok && be.Code == "BOT_MOVE_CAMPAIGN_TO_EXECUTED_FAILED" {
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to move campaign to executed", be.Code, nil)
		}
		log.Println("Move to executed failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to move campaign to executed", "MOVE_TO_EXECUTED_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign moved to executed", fiber.Map{"ok": true})
}

// MoveCampaignToRunning updates status to running
// @Summary Bot Move Campaign to Running
// @Tags Bot Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} dto.APIResponse
// @Router /api/v1/bot/campaigns/{id}/running [post]
func (h *CampaignBotHandler) MoveCampaignToRunning(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", "INVALID_CAMPAIGN_ID", nil)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	_ = metadata
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/"+idStr+"/running", 30*time.Second)
	defer cancel()
	if err := h.campaignFlow.MoveCampaignToRunning(ctx, uint(id)); err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok && be.Code == "BOT_MOVE_CAMPAIGN_TO_RUNNING_FAILED" {
			return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to move campaign to running", be.Code, nil)
		}
		log.Println("Move to running failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to move campaign to running", "MOVE_TO_RUNNING_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign moved to running", fiber.Map{"ok": true})
}

// DownloadTargetAudienceExcelFile downloads campaign target-audience Excel file for bots.
// @Summary Bot Download Campaign Target Audience Excel File
// @Tags Bot Campaigns
// @Security BearerAuth
// @Produce application/octet-stream
// @Param id path int true "Campaign ID"
// @Success 200 {string} string "Binary file"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bot/campaigns/{id}/target-audience-excel-file [get]
func (h *CampaignBotHandler) DownloadTargetAudienceExcelFile(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", "INVALID_CAMPAIGN_ID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/"+idStr+"/target-audience-excel-file", 30*time.Second)
	defer cancel()
	filename, contentType, data, err := h.campaignFlow.DownloadTargetAudienceExcelFile(
		ctx,
		uint(id),
	)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) || businessflow.IsCampaignTargetAudienceExcelMediaNotFound(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusNotFound,
				"Target audience excel file not found",
				"TARGET_AUDIENCE_EXCEL_FILE_NOT_FOUND",
				nil,
			)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_CAMPAIGN_ID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", be.Code, nil)
			case "TARGET_AUDIENCE_EXCEL_FILE_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Target audience excel file not found", be.Code, nil)
			}
		}
		log.Println("Download target audience excel file failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to download target audience excel file",
			"DOWNLOAD_TARGET_AUDIENCE_EXCEL_FILE_FAILED",
			nil,
		)
	}

	if contentType != "" {
		c.Set("Content-Type", contentType)
	}
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

// UpdateCampaignStatistics updates statistics json for a campaign
// @Summary Bot Update Campaign Statistics
// @Tags Bot Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param request body dto.BotUpdateCampaignStatisticsRequest true "Statistics payload"
// @Success 200 {object} dto.APIResponse{data=dto.BotUpdateCampaignStatisticsResponse}
// @Failure 400 {object} dto.APIResponse
// @Router /api/v1/bot/campaigns/{id}/statistics [post]
func (h *CampaignBotHandler) UpdateCampaignStatistics(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", "INVALID_CAMPAIGN_ID", nil)
	}
	var req dto.BotUpdateCampaignStatisticsRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/"+idStr+"/statistics", 30*time.Second)
	defer cancel()
	res, err := h.campaignFlow.UpdateCampaignStatistics(ctx, uint(id), req.Statistics)
	if err != nil {
		if businessflow.IsCampaignNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Campaign not found", "CAMPAIGN_NOT_FOUND", nil)
		}
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "VALIDATION_ERROR":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", be.Code, nil)
			case "CAMPAIGN_FETCH_FAILED", "STATISTICS_MARSHAL_FAILED", "CAMPAIGN_STATISTICS_UPDATE_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update campaign statistics", be.Code, nil)
			}
		}
		log.Println("Update campaign statistics failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to update campaign statistics",
			"UPDATE_CAMPAIGN_STATISTICS_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Campaign statistics updated", res)
}

// PushAudienceUIDs stores a batch of audience uid/code pairs for a campaign.
// @Summary Bot Push Campaign Audience UIDs
// @Tags Bot Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param request body dto.BotPushAudienceUIDsRequest true "Audience UIDs batch"
// @Success 200 {object} dto.APIResponse{data=dto.BotPushAudienceUIDsResponse}
// @Failure 400 {object} dto.APIResponse
// @Router /api/v1/bot/campaigns/{id}/audience-uids [post]
func (h *CampaignBotHandler) PushAudienceUIDs(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid campaign id", "INVALID_CAMPAIGN_ID", nil)
	}
	var req dto.BotPushAudienceUIDsRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if len(req.Items) == 0 {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "items must not be empty", "VALIDATION_ERROR", nil)
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/campaigns/"+idStr+"/audience-uids", 60*time.Second)
	defer cancel()
	if err := h.campaignFlow.PushCampaignAudienceUIDs(ctx, uint(id), req.Items); err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "VALIDATION_ERROR":
				return h.ErrorResponse(c, fiber.StatusBadRequest, be.Message, be.Code, nil)
			case "AUDIENCE_UIDS_STORE_FAILED":
				return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to store audience UIDs", be.Code, nil)
			}
		}
		log.Println("Push audience UIDs failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to push audience UIDs", "PUSH_AUDIENCE_UIDS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Audience UIDs stored", dto.BotPushAudienceUIDsResponse{Message: "Audience UIDs stored"})
}

func (h *CampaignBotHandler) createRequestContextWithTimeout(
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
