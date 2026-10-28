package handlers

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

const (
	shortLinkCreateTimeout = 2 * time.Minute
	// A 200K-recipient allocation performs a bulk insert and, when enabled,
	// waits for all redirect mappings to be acknowledged. Leave time for the
	// reverse proxy to return a timeout response and for the scheduler client
	// to receive it; equal deadlines make a completed allocation look failed.
	shortLinkAllocateTimeout = 50 * time.Minute
)

// ShortLinkBotHandlerInterface defines contract for bot short link endpoints
type ShortLinkBotHandlerInterface interface {
	CreateShortLink(c fiber.Ctx) error
	CreateShortLinks(c fiber.Ctx) error
	AllocateShortLinks(c fiber.Ctx) error
}

// ShortLinkBotHandler handles bot short link creation
type ShortLinkBotHandler struct {
	flow      businessflow.BotShortLinkFlow
	validator *validator.Validate
}

func NewShortLinkBotHandler(flow businessflow.BotShortLinkFlow) ShortLinkBotHandlerInterface {
	return &ShortLinkBotHandler{flow: flow, validator: validator.New()}
}

func (h *ShortLinkBotHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: errorCode, Details: details}})
}

func (h *ShortLinkBotHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// CreateShortLink creates a single short link (bot)
// @Summary Bot Create Short Link
// @Tags Bot ShortLinks
// @Accept json
// @Produce json
// @Param request body dto.BotCreateShortLinkRequest true "Short link creation"
// @Success 201 {object} dto.APIResponse{data=dto.BotCreateShortLinkResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/bot/short-links/one [post]
func (h *ShortLinkBotHandler) CreateShortLink(c fiber.Ctx) error {
	var req dto.BotCreateShortLinkRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/short-links/one", shortLinkCreateTimeout)
	defer cancel()
	res, err := h.flow.CreateShortLink(ctx, &req)
	if err != nil {
		log.Println("Bot create short link failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create short link", "CREATE_SHORT_LINK_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusCreated, "Short link created", res)
}

// CreateShortLinks creates multiple short links (bot)
// @Summary Bot Create Short Links (Batch)
// @Tags Bot ShortLinks
// @Accept json
// @Produce json
// @Param request body dto.BotCreateShortLinksRequest true "Short links creation"
// @Success 201 {object} dto.APIResponse{data=dto.BotCreateShortLinksResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/bot/short-links [post]
func (h *ShortLinkBotHandler) CreateShortLinks(c fiber.Ctx) error {
	var req dto.BotCreateShortLinksRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/short-links", shortLinkCreateTimeout)
	defer cancel()
	res, err := h.flow.CreateShortLinks(ctx, &req)
	if err != nil {
		log.Println("Bot create short links failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create short links", "CREATE_SHORT_LINKS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusCreated, "Short links created", res)
}

// AllocateShortLinks generates sequential UIDs and creates per-phone short links for a campaign.
// @Summary Bot Allocate Short Links
// @Tags Bot ShortLinks
// @Accept json
// @Produce json
// @Param request body dto.BotAllocateShortLinksRequest true "Allocate per-phone short links"
// @Success 200 {object} dto.APIResponse{data=dto.BotAllocateShortLinksResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 500 {object} dto.APIResponse
// @Router /api/v1/bot/short-links/allocate [post]
func (h *ShortLinkBotHandler) AllocateShortLinks(c fiber.Ctx) error {
	var req dto.BotAllocateShortLinksRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", err.Error())
	}
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/short-links/allocate", shortLinkAllocateTimeout)
	defer cancel()
	codes, err := h.flow.GenerateAndCreateShortLinks(ctx, &req)
	if err != nil {
		log.Println("Bot allocate short links failed", err)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return h.ErrorResponse(c, fiber.StatusGatewayTimeout, "Short-link allocation timed out", "ALLOCATE_SHORT_LINKS_TIMEOUT", nil)
		}
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to allocate short links", "ALLOCATE_SHORT_LINKS_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Short links allocated", dto.BotAllocateShortLinksResponse{Message: "Short links allocated", Codes: codes})
}

func (h *ShortLinkBotHandler) createRequestContextWithTimeout(c fiber.Ctx, endpoint string, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel)
	return ctx, cancel
}
