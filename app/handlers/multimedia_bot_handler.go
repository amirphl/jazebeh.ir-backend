package handlers

import (
	"context"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
)

// MultimediaBotHandlerInterface defines bot multimedia handlers.
type MultimediaBotHandlerInterface interface {
	Download(c fiber.Ctx) error
}

// MultimediaBotHandler handles bot multimedia requests.
type MultimediaBotHandler struct {
	flow businessflow.MultimediaBotFlow
}

// NewMultimediaBotHandler creates a new bot multimedia handler.
func NewMultimediaBotHandler(flow businessflow.MultimediaBotFlow) MultimediaBotHandlerInterface {
	return &MultimediaBotHandler{flow: flow}
}

func (h *MultimediaBotHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

// Download handles multimedia download for bots.
// @Summary Bot download multimedia
// @Description Download an image or video by uuid (bot access)
// @Tags Bot Multimedia
// @Security BearerAuth
// @Produce application/octet-stream
// @Param uuid path string true "Multimedia UUID"
// @Success 200 {string} string "Binary file"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 401 {object} dto.APIResponse "Unauthorized"
// @Failure 404 {object} dto.APIResponse "Not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/bot/media/{uuid} [get]
func (h *MultimediaBotHandler) Download(c fiber.Ctx) error {
	mediaUUID := c.Params("uuid")
	if mediaUUID == "" {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", "INVALID_UUID", nil)
	}

	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/media/{uuid}", 30*time.Second)
	defer cancel()
	filename, contentType, data, err := h.flow.DownloadMultimediaByBot(ctx, mediaUUID)
	if err != nil {
		if be, ok := err.(*businessflow.BusinessError); ok {
			switch be.Code {
			case "INVALID_UUID":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid uuid", be.Code, be.Error())
			case "MULTIMEDIA_NOT_FOUND":
				return h.ErrorResponse(c, fiber.StatusNotFound, "Multimedia not found", be.Code, be.Error())
			case "INVALID_PATH":
				return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid file path", be.Code, be.Error())
			}
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to download multimedia", "DOWNLOAD_FAILED", nil)
	}

	if contentType != "" {
		c.Set("Content-Type", contentType)
	}
	c.Set("Content-Disposition", "attachment; filename="+filename)
	return c.Send(data)
}

func (h *MultimediaBotHandler) createRequestContextWithTimeout(
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
