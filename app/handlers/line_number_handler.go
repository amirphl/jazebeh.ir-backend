package handlers

import (
	"context"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
)

type LineNumberHandlerInterface interface {
	ListActive(c fiber.Ctx) error
}

type LineNumberHandler struct {
	flow businessflow.LineNumberFlow
}

func NewLineNumberHandler(flow businessflow.LineNumberFlow) LineNumberHandlerInterface {
	return &LineNumberHandler{flow: flow}
}

func (h *LineNumberHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error:   dto.ErrorDetail{Code: errorCode, Details: details},
	})
}

func (h *LineNumberHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{Success: true, Message: message, Data: data})
}

// ListActive returns active line numbers for customers
// @Summary List Active Line Numbers
// @Tags Line Numbers
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.ListActiveLineNumbersResponse}
// @Router /api/v1/line-numbers/active [get]
func (h *LineNumberHandler) ListActive(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/line-numbers/active", 30*time.Second)
	defer cancel()
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	res, err := h.flow.ListActiveLineNumbers(ctx, metadata)
	if err != nil {
		log.Println("List active line numbers failed", err)
		return h.ErrorResponse(
			c,
			fiber.StatusInternalServerError,
			"Failed to list active line numbers",
			"LIST_ACTIVE_LINE_NUMBERS_FAILED",
			nil,
		)
	}
	return h.SuccessResponse(c, fiber.StatusOK, "Active line numbers retrieved", res)
}

func (h *LineNumberHandler) createRequestContextWithTimeout(
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
