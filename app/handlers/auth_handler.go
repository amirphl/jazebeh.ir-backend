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

// AuthHandlerInterface defines the contract for authentication handlers
type AuthHandlerInterface interface {
	Signup(c fiber.Ctx) error
	VerifyOTP(c fiber.Ctx) error
	ResendOTP(c fiber.Ctx) error
	Login(c fiber.Ctx) error
	RequestLoginOTP(c fiber.Ctx) error
	ForgotPassword(c fiber.Ctx) error
	ResetPassword(c fiber.Ctx) error
}

// AuthHandler handles authentication-related HTTP requests
type AuthHandler struct {
	signupFlow businessflow.SignupFlow
	loginFlow  businessflow.LoginFlow
	validator  *validator.Validate
}

func (h *AuthHandler) ErrorResponse(c fiber.Ctx, statusCode int, message, errorCode string, details any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code:    errorCode,
			Details: details,
		},
	})
}

func (h *AuthHandler) SuccessResponse(c fiber.Ctx, statusCode int, message string, data any) error {
	return c.Status(statusCode).JSON(dto.APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// NewAuthHandler creates a new authentication handler
func NewAuthHandler(signupFlow businessflow.SignupFlow, loginFlow businessflow.LoginFlow) *AuthHandler {
	handler := &AuthHandler{
		signupFlow: signupFlow,
		loginFlow:  loginFlow,
		validator:  validator.New(),
	}

	// Setup custom validations
	handler.setupCustomValidations()

	return handler
}

// Signup handles the user registration process
// @Summary User Registration
// @Description Register a new user account with mobile verification. National ID is required for individual, independent_company, and
// marketing_agency account types.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.SignupRequest true "User registration data"
// @Success 200 {object} dto.APIResponse{data=dto.SignupResponse} "Registration initiated successfully"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 409 {object} dto.APIResponse "User already exists"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/signup [post]
func (h *AuthHandler) Signup(c fiber.Ctx) error {
	var req dto.SignupRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/signup", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.signupFlow.Signup(ctx, &req, metadata)
	if err != nil {
		// Handle specific business errors
		if businessflow.IsEmailAlreadyExists(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Email already exists", "EMAIL_EXISTS", nil)
		}
		if businessflow.IsMobileAlreadyExists(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "Mobile number already exists", "MOBILE_EXISTS", nil)
		}
		if businessflow.IsNationalIDAlreadyExists(err) {
			return h.ErrorResponse(c, fiber.StatusConflict, "National ID already exists", "NATIONAL_ID_EXISTS", nil)
		}
		if businessflow.IsNationalIDRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "National ID is required", "NATIONAL_ID_REQUIRED", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsCompanyFieldsRequired(err) {
			return h.ErrorResponse(
				c,
				fiber.StatusBadRequest,
				"Company fields are required for business accounts",
				"COMPANY_FIELDS_REQUIRED",
				nil,
			)
		}
		if businessflow.IsReferrerAgencyNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Referrer agency not found", "REFERRER_AGENCY_NOT_FOUND", nil)
		}
		if businessflow.IsReferrerMustBeAgency(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Referrer must be a marketing agency", "REFERRER_MUST_BE_AGENCY", nil)
		}
		if businessflow.IsReferrerAgencyInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Referrer agency is inactive", "REFERRER_AGENCY_INACTIVE", nil)
		}
		if businessflow.IsShebaNumberRequired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Sheba number is required", "SHEBA_NUMBER_REQUIRED", nil)
		}
		if businessflow.IsShebaNumberInvalid(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Sheba number is invalid", "SHEBA_NUMBER_INVALID", nil)
		}
		if businessflow.IsCacheNotAvailable(err) {
			return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Cache not available", "CACHE_NOT_AVAILABLE", nil)
		}

		log.Println("Signup failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Signup failed", "SIGNUP_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, result.Message, fiber.Map{
		"customer_id": result.CustomerID,
	})
}

// VerifyOTP handles OTP verification for user registration
// @Summary Verify OTP
// @Description Verify OTP code sent to user's mobile number
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.OTPVerificationRequest true "OTP verification data"
// @Success 200 {object}
// dto.APIResponse{data=object{access_token=string,refresh_token=string,token_type=string,expires_in=int,customer=dto.AuthCustomerDTO}} "OTP
// verified successfully"
// @Failure 400 {object} dto.APIResponse "Invalid OTP or request"
// @Failure 404 {object} dto.APIResponse "User not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/verify [post]
func (h *AuthHandler) VerifyOTP(c fiber.Ctx) error {
	var req dto.OTPVerificationRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/verify", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.signupFlow.VerifyOTP(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Too many attempts", "RATE_LIMITED", nil)
		}
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsNoValidOTPFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "No valid OTP found", "NO_VALID_OTP", nil)
		}
		if businessflow.IsInvalidOTPCode(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid OTP code", "INVALID_OTP_CODE", nil)
		}
		if businessflow.IsInvalidOTPType(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid OTP type", "INVALID_OTP_TYPE", nil)
		}
		if businessflow.IsOTPExpired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "OTP expired", "OTP_EXPIRED", nil)
		}
		if businessflow.IsAlreadyVerified(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is already verified", "ACCOUNT_ALREADY_VERIFIED", nil)
		}
		if businessflow.IsCacheNotAvailable(err) {
			return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Cache not available", "CACHE_NOT_AVAILABLE", nil)
		}

		log.Println("OTP verification failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusBadRequest, "OTP verification failed", "OTP_VERIFICATION_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, result.Message, fiber.Map{
		"access_token":  result.Token,
		"refresh_token": result.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    utils.AccessTokenTTLSeconds,
		"customer":      result.Customer,
	})
}

// ResendOTP handles resending OTP to user's mobile number
// @Summary Resend OTP
// @Description Resend OTP code to user's mobile number
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.OTPResendRequest true "OTP resend request"
// @Success 200 {object} dto.APIResponse{data=object{otp_sent=bool,masked_otp_target=string}} "OTP resent successfully"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 404 {object} dto.APIResponse "User not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/resend-otp [post]
func (h *AuthHandler) ResendOTP(c fiber.Ctx) error {
	var req dto.OTPResendRequest
	if err := c.Bind().JSON(&req); err != nil {
		return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body", "INVALID_REQUEST", err.Error())
	}

	// Get client information
	metadata := businessflow.NewClientMetadata(c.IP(), c.Get("User-Agent"))
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/resend-otp", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.signupFlow.ResendOTP(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Please wait before requesting another OTP", "RATE_LIMITED", nil)
		}
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsCacheNotAvailable(err) {
			return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Cache not available", "CACHE_NOT_AVAILABLE", nil)
		}
		if businessflow.IsAlreadyVerified(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is already verified", "ACCOUNT_ALREADY_VERIFIED", nil)
		}

		log.Println("Resend OTP failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to resend OTP", "RESEND_OTP_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, result.Message, fiber.Map{
		"otp_sent":   result.OTPSent,
		"otp_target": result.MaskedOTPTarget,
	})
}

// Login handles user authentication
// @Summary User Login
// @Description Authenticate user with a mobile number, password, and OTP. Both credentials must be valid.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.LoginRequest true "Login credentials"
// @Success 200 {object}
// dto.APIResponse{data=object{access_token=string,refresh_token=string,token_type=string,expires_in=int,customer=dto.AuthCustomerDTO}}
// "Login successful with tokens"
// @Failure 400 {object} dto.APIResponse "Invalid credentials"
// @Failure 401 {object} dto.APIResponse "Authentication failed"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/login [post]
func (h *AuthHandler) Login(c fiber.Ctx) error {
	var req dto.LoginRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/login", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.loginFlow.Login(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Too many login attempts", "RATE_LIMITED", nil)
		}
		if businessflow.IsAuthenticationFailed(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Invalid credentials", "AUTHENTICATION_FAILED", nil)
		}
		if businessflow.IsNoValidOTPFound(err) || businessflow.IsInvalidOTPCode(err) {
			return h.ErrorResponse(c, fiber.StatusUnauthorized, "Invalid credentials", "AUTHENTICATION_FAILED", nil)
		}
		if businessflow.IsCacheNotAvailable(err) {
			return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Cache not available", "CACHE_NOT_AVAILABLE", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}

		log.Println("Login failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusUnauthorized, "Login failed", "LOGIN_FAILED", nil)
	}

	// Successful login - return tokens and user info
	return h.SuccessResponse(c, fiber.StatusOK, "Login successful", fiber.Map{
		"access_token":  result.Session.SessionToken,
		"refresh_token": result.Session.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    utils.AccessTokenTTLSeconds,
		"customer":      result.Customer,
	})
}

// RequestLoginOTP handles login OTP generation
// @Summary Request Login OTP
// @Description Generate and send a one-time password for login via SMS
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.LoginOTPRequest true "Login OTP request"
// @Success 200 {object} dto.APIResponse{data=dto.LoginOTPResponse} "OTP sent"
// @Failure 400 {object} dto.APIResponse "Validation error"
// @Failure 404 {object} dto.APIResponse "User not found"
// @Failure 503 {object} dto.APIResponse "Cache not available"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/login/otp [post]
func (h *AuthHandler) RequestLoginOTP(c fiber.Ctx) error {
	var req dto.LoginOTPRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/login/otp", 30*time.Second)
	defer cancel()

	res, err := h.loginFlow.RequestLoginOTP(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Please wait before requesting another OTP", "RATE_LIMITED", nil)
		}
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "User not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsCacheNotAvailable(err) {
			return h.ErrorResponse(c, fiber.StatusServiceUnavailable, "Cache not available", "CACHE_NOT_AVAILABLE", nil)
		}

		log.Println("Login OTP request failed", err)
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Login OTP request failed", "LOGIN_OTP_REQUEST_FAILED", nil)
	}

	return h.SuccessResponse(c, fiber.StatusOK, res.Message, res)
}

// ForgotPassword handles password reset initiation
// @Summary Forgot Password
// @Description Initiate password reset by sending OTP to registered mobile
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.ForgotPasswordRequest true "Password reset request"
// @Success 200 {object} dto.APIResponse{data=object{customer_id=uint,masked_phone=string,expires_in=int}} "OTP sent successfully"
// @Failure 400 {object} dto.APIResponse "Invalid request"
// @Failure 404 {object} dto.APIResponse "User not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/forgot-password [post]
func (h *AuthHandler) ForgotPassword(c fiber.Ctx) error {
	var req dto.ForgotPasswordRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/forgot-password", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.loginFlow.ForgotPassword(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Please wait before requesting another OTP", "RATE_LIMITED", nil)
		}
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}

		log.Println("Forgot password failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Password reset failed", "PASSWORD_RESET_FAILED", nil)
	}

	// Successful response
	return h.SuccessResponse(c, fiber.StatusOK, "Password reset OTP sent to your mobile number", fiber.Map{
		"customer_id":  result.CustomerID,
		"masked_phone": result.MaskedPhone,
		"expires_in":   utils.OTPExpiry.Seconds(),
	})
}

// ResetPassword handles password reset completion
// @Summary Reset Password
// @Description Complete password reset with OTP verification
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body dto.ResetPasswordRequest true "Password reset data"
// @Success 200 {object}
// dto.APIResponse{data=object{access_token=string,refresh_token=string,token_type=string,expires_in=int,customer=dto.AuthCustomerDTO,password_changed_at=string}}
// "Password reset successful with tokens"
// @Failure 400 {object} dto.APIResponse "Invalid request or OTP"
// @Failure 404 {object} dto.APIResponse "User not found"
// @Failure 500 {object} dto.APIResponse "Internal server error"
// @Router /api/v1/auth/reset [post]
func (h *AuthHandler) ResetPassword(c fiber.Ctx) error {
	var req dto.ResetPasswordRequest
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
	ctx, cancel := h.createRequestContextWithTimeout(c, "/api/v1/auth/reset", 30*time.Second)
	defer cancel()

	// Call business logic with proper context
	result, err := h.loginFlow.ResetPassword(ctx, &req, metadata)
	if err != nil {
		if businessflow.IsRateLimitExceeded(err) {
			return h.ErrorResponse(c, fiber.StatusTooManyRequests, "Too many attempts", "RATE_LIMITED", nil)
		}
		// Handle specific business errors
		if businessflow.IsCustomerNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusNotFound, "Customer not found", "CUSTOMER_NOT_FOUND", nil)
		}
		if businessflow.IsAccountInactive(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account is inactive", "ACCOUNT_INACTIVE", nil)
		}
		if businessflow.IsAccountTypeNotFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Account type not found", "ACCOUNT_TYPE_NOT_FOUND", nil)
		}
		if businessflow.IsNoValidOTPFound(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "No valid OTP found", "NO_VALID_OTP", nil)
		}
		if businessflow.IsInvalidOTPCode(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid OTP code", "INVALID_OTP_CODE", nil)
		}
		if businessflow.IsInvalidOTPType(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "Invalid OTP type", "INVALID_OTP_TYPE", nil)
		}
		if businessflow.IsOTPExpired(err) {
			return h.ErrorResponse(c, fiber.StatusBadRequest, "OTP expired", "OTP_EXPIRED", nil)
		}

		log.Println("Reset password failed", err)
		// Handle generic business errors
		return h.ErrorResponse(c, fiber.StatusInternalServerError, "Password reset failed", "PASSWORD_RESET_FAILED", nil)
	}

	// Successful password reset - return tokens and user info
	return h.SuccessResponse(c, fiber.StatusOK, "Password reset successful", fiber.Map{
		"access_token":        result.Session.SessionToken,
		"refresh_token":       result.Session.RefreshToken,
		"token_type":          "Bearer",
		"expires_in":          utils.AccessTokenTTLSeconds,
		"customer":            result.Customer,
		"password_changed_at": utils.UTCNow().Format(time.RFC3339),
	})
}

// Health handles health check requests
// @Summary Health Check
// @Description Check the health status of the API
// @Tags Health
// @Accept json
// @Produce json
// @Success 200 {object} dto.APIResponse "Service is healthy"
// @Router /api/v1/health [get]
func (h *AuthHandler) Health(c fiber.Ctx) error {
	return h.SuccessResponse(c, fiber.StatusOK, "Auth service is healthy", fiber.Map{
		"status":    "healthy",
		"timestamp": utils.UTCNow().Format(time.RFC3339),
		"service":   "auth-handler",
	})
}

// createRequestContextWithTimeout creates a context with custom timeout and request-scoped values
func (h *AuthHandler) createRequestContextWithTimeout(
	c fiber.Ctx,
	endpoint string,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	// Create context with custom timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Add request-scoped values for observability
	ctx = context.WithValue(ctx, utils.RequestIDKey, c.Get("X-Request-ID"))
	ctx = context.WithValue(ctx, utils.UserAgentKey, c.Get("User-Agent"))
	ctx = context.WithValue(ctx, utils.IPAddressKey, c.IP())
	ctx = context.WithValue(ctx, utils.EndpointKey, endpoint)
	ctx = context.WithValue(ctx, utils.TimeoutKey, timeout)
	ctx = context.WithValue(ctx, utils.CancelFuncKey, cancel) // Store cancel function for cleanup

	return ctx, cancel
}

// Custom validation setup
func (h *AuthHandler) setupCustomValidations() {
	// Register custom validation for alpha characters with spaces
	h.validator.RegisterValidation("alpha_space", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		for _, char := range value {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == ' ') {
				return false
			}
		}
		return true
	})

	// Register custom validation for mobile format
	h.validator.RegisterValidation("mobile_format", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		// Iranian mobile format: +989xxxxxxxxx
		if len(value) != 13 {
			return false
		}
		if value[:4] != "+989" {
			return false
		}
		// Check if remaining characters are digits
		for _, char := range value[4:] {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	})

	// Register custom validation for password strength
	h.validator.RegisterValidation("password_strength", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()

		hasUpper := false
		hasNumber := false

		for _, char := range value {
			if char >= 'A' && char <= 'Z' {
				hasUpper = true
			}
			if char >= '0' && char <= '9' {
				hasNumber = true
			}
		}

		return hasUpper && hasNumber
	})

	h.validator.RegisterValidation("numeric", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		for _, char := range value {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	})
}
