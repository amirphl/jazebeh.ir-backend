package handlers

import (
	"context"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

type AuthBotHandlerInterface interface {
	Login(c fiber.Ctx) error
}

type AuthBotHandler struct {
	flow      businessflow.BotAuthFlow
	validator *validator.Validate
}

func (h *AuthBotHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).
		JSON(dto.APIResponse{Success: false, Message: message, Error: dto.ErrorDetail{Code: errorCode, Details: details}})
}

func (h *AuthBotHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

func NewAuthBotHandler(flow businessflow.BotAuthFlow) AuthBotHandlerInterface {
	return &AuthBotHandler{flow: flow, validator: validator.New()}
}

// Login authenticates a bot and returns tokens
// @Summary Bot Login
// @Tags Bot Authentication
// @Accept json
// @Produce json
// @Param request body dto.BotLoginRequest true "Bot login"
// @Success 200 {object} dto.APIResponse{data=dto.BotLoginResponse}
// @Failure 400 {object} dto.APIResponse
// @Failure 401 {object} dto.APIResponse
// @Router /api/v1/bot/auth/login [post]
func (h *AuthBotHandler) Login(c fiber.Ctx) error {
	var req dto.BotLoginRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}
	if err := h.validator.Struct(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Validation failed", "VALIDATION_ERROR", nil)
	}
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/bot/auth/login", 30*time.Second)
	defer cancel()
	res, err := h.flow.Verify(ctx, &req, metadata)
	if err != nil {
		log.Println("Bot login failed", err)
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Login failed", "BOT_LOGIN_FAILED", nil)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Login successful", res)
}

func (h *AuthBotHandler) createRequestContextWithTimeout(
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
