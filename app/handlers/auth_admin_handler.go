// Package handlers contains HTTP request handlers and presentation layer logic for the API endpoints
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

// AuthAdminHandlerInterface defines the contract for admin auth handlers
type AuthAdminHandlerInterface interface {
	InitCaptcha(cCtx fiber.Ctx) error
	VerifyLogin(cCtx fiber.Ctx) error
	VerifyLoginOTP(cCtx fiber.Ctx) error
}

// AuthAdminHandler implements AuthAdminHandlerInterface
type AuthAdminHandler struct {
	flow      businessflow.AdminAuthFlow
	validator *validator.Validate
}

// ErrorResponse standard JSON error
func (h *AuthAdminHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

// SuccessResponse standard JSON success
func (h *AuthAdminHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func NewAuthAdminHandler(flow businessflow.AdminAuthFlow) AuthAdminHandlerInterface {
	return &AuthAdminHandler{
		flow:      flow,
		validator: validator.New(),
	}
}

// InitCaptcha starts the admin login by returning a rotate captcha challenge
// @Summary Admin captcha init
// @Description Initialize rotate captcha for admin login (returns base64 images and challenge ID)
// @Tags Admin Authentication
// @Produce json
// @Success 200 {object} dto.APIResponse{data=dto.AdminCaptchaInitResponse} "Captcha initialized"
// @Failure 500 {object} dto.APIResponse "Failed to initialize captcha"
// @Router /api/v1/admin/auth/captcha/init [get]
func (h *AuthAdminHandler) InitCaptcha(c fiber.Ctx) error {
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/auth/captcha/init", 30*time.Second)
	defer cancel()
	resp, err := h.flow.InitCaptcha(ctx)
	if err != nil {
		log.Println("Admin captcha init failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Admin captcha init failed", "ADMIN_CAPTCHA_INIT_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Captcha initialized", resp)
}

// VerifyLogin validates captcha and credentials, then starts the OTP step.
// @Summary Admin login
// @Description Verify captcha and admin credentials, then send/reuse an OTP for two-factor login
// @Tags Admin Authentication
// @Accept json
// @Produce json
// @Param request body dto.AdminCaptchaVerifyRequest true "Admin login data"
// @Success 200 {object} dto.APIResponse{data=dto.AdminLoginInitResponse} "OTP challenge created"
// @Failure 400 {object} dto.APIResponse "Invalid request or captcha"
// @Failure 401 {object} dto.APIResponse "Incorrect credentials or admin not found"
// @Failure 403 {object} dto.APIResponse "Admin inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/auth/login [post]
func (h *AuthAdminHandler) VerifyLogin(c fiber.Ctx) error {
	var req dto.AdminCaptchaVerifyRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/auth/login", 30*time.Second)
	defer cancel()
	result, err := h.flow.Verify(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Too many login attempts", "RATE_LIMITED", nil)
		}
		if businessflow.IsInvalidCaptcha(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid captcha", "INVALID_CAPTCHA", nil)
		}
		if businessflow.IsAuthenticationFailed(err) || businessflow.IsAdminNotFound(err) || businessflow.IsAdminInactive(err) ||
			businessflow.IsIncorrectPassword(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Login failed", "LOGIN_FAILED", nil)
		}
		log.Println("Admin login failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Login failed", "INTERNAL_ERROR", nil)
	}

	if !result.RequiresTwoFactor && result.Session != nil && result.Admin != nil {
		return h.SuccessResponse(c, fiber.StatusOK, "Login successful", fiber.Map{
			"access_token":  result.Session.AccessToken,
			"refresh_token": result.Session.RefreshToken,
			"token_type":    result.Session.TokenType,
			"expires_in":    result.Session.ExpiresIn,
			"admin":         result.Admin,
		})
	}

	return h.SuccessResponse(c, fiber.StatusOK, "OTP sent", result)
}

// VerifyLoginOTP completes the second factor step for admin login.
// @Summary Admin login OTP verification
// @Description Verify admin OTP and issue access/refresh tokens
// @Tags Admin Authentication
// @Accept json
// @Produce json
// @Param request body dto.AdminLoginVerifyOTPRequest true "Admin login OTP verification data"
// @Success 200 {object}
// dto.APIResponse{data=object{access_token=string,refresh_token=string,token_type=string,expires_in=int,admin=dto.AdminDTO}} "Login
// successful"
// @Failure 400 {object} dto.APIResponse "Invalid request body"
// @Failure 401 {object} dto.APIResponse "Invalid or expired OTP"
// @Failure 403 {object} dto.APIResponse "Admin inactive"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/admin/auth/login/verify-otp [post]
func (h *AuthAdminHandler) VerifyLoginOTP(c fiber.Ctx) error {
	var req dto.AdminLoginVerifyOTPRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/admin/auth/login/verify-otp", 30*time.Second)
	defer cancel()
	result, err := h.flow.VerifyOTP(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Too many attempts", "RATE_LIMITED", nil)
		}
		if businessflow.IsAdminNotFound(err) || businessflow.IsIncorrectPassword(err) || businessflow.IsNoValidOTPFound(err) ||
			businessflow.IsInvalidOTPCode(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Invalid or expired OTP", "INVALID_OTP", nil)
		}
		if businessflow.IsAdminInactive(err) {
			return h.ErrorResponse(c, fiber.StatusForbidden, "Admin inactive", "ADMIN_INACTIVE", nil)
		}
		log.Println("Admin OTP verification failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "OTP verification failed", "INTERNAL_ERROR", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, "Login successful", fiber.Map{
		"access_token":  result.Session.AccessToken,
		"refresh_token": result.Session.RefreshToken,
		"token_type":    result.Session.TokenType,
		"expires_in":    result.Session.ExpiresIn,
		"admin":         result.Admin,
	})
}

func (h *AuthAdminHandler) createRequestContextWithTimeout(
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
	return ctx, cancel
}
