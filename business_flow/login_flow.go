// Package businessflow contains the core business logic and use cases for authentication workflows
package businessflow

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// LoginFlow handles user authentication and password reset operations
type LoginFlow interface {
	Login(ctx context.Context, request *dto.LoginRequest, metadata *ClientMetadata) (*dto.LoginResponse, error)
	RequestLoginOTP(ctx context.Context, request *dto.LoginOTPRequest, metadata *ClientMetadata) (*dto.LoginOTPResponse, error)
	ForgotPassword(ctx context.Context, request *dto.ForgotPasswordRequest, metadata *ClientMetadata) (*dto.ForgetPasswordResponse, error)
	ResetPassword(ctx context.Context, request *dto.ResetPasswordRequest, metadata *ClientMetadata) (*dto.ResetPasswordResponse, error)
}

// LoginFlowImpl implements the login business flow
type LoginFlowImpl struct {
	customerRepo    repository.CustomerRepository
	sessionRepo     repository.CustomerSessionRepository
	auditRepo       repository.AuditLogRepository
	accountTypeRepo repository.AccountTypeRepository
	tokenService    services.TokenService
	otpSMSSvc       services.SMSService
	notificationSvc services.NotificationService
	messageConfig   config.MessageConfig
	adminConfig     config.AdminConfig
	db              *gorm.DB
	rc              *redis.Client
}

// NewLoginFlow creates a new login flow instance
func NewLoginFlow(
	customerRepo repository.CustomerRepository,
	sessionRepo repository.CustomerSessionRepository,
	auditRepo repository.AuditLogRepository,
	accountTypeRepo repository.AccountTypeRepository,
	tokenService services.TokenService,
	otpSMSSvc services.SMSService,
	notificationSvc services.NotificationService,
	messageConfig config.MessageConfig,
	adminConfig config.AdminConfig,
	db *gorm.DB,
	rc *redis.Client,
) LoginFlow {
	return &LoginFlowImpl{
		customerRepo:    customerRepo,
		sessionRepo:     sessionRepo,
		auditRepo:       auditRepo,
		accountTypeRepo: accountTypeRepo,
		tokenService:    tokenService,
		otpSMSSvc:       otpSMSSvc,
		notificationSvc: notificationSvc,
		messageConfig:   messageConfig,
		adminConfig:     adminConfig,
		db:              db,
		rc:              rc,
	}
}

// Login authenticates a user with a mobile number, password, and OTP.
func (lf *LoginFlowImpl) Login(ctx context.Context, req *dto.LoginRequest, metadata *ClientMetadata) (*dto.LoginResponse, error) {
	// Validate business rules
	if err := lf.validateLoginRequest(req); err != nil {
		return nil, NewBusinessError("LOGIN_VALIDATION_FAILED", "Login validation failed", err)
	}
	req.Identifier = normalizeLoginIdentifier(req.Identifier)

	if err := lf.enforceLoginRateLimit(ctx, req.Identifier, metadata); err != nil {
		return nil, NewBusinessError("LOGIN_RATE_LIMITED", "Login failed", err)
	}

	var customer *models.Customer
	var resp *dto.LoginResponse
	var newlyVerified bool

	err := repository.WithTransaction(ctx, lf.db, func(txCtx context.Context) error {
		// Login is mobile-only because the OTP is sent to that mobile number.
		var err error
		customer, err = lf.customerRepo.ByMobile(txCtx, req.Identifier)
		if err != nil {
			return err
		}
		if customer == nil {
			return ErrAuthenticationFailed
		}
		if !utils.IsTrue(customer.IsActive) {
			return ErrAuthenticationFailed
		}

		// Verify password
		if err := bcrypt.CompareHashAndPassword([]byte(customer.PasswordHash), []byte(req.Password)); err != nil {
			return ErrAuthenticationFailed
		}

		// Get account type information
		accountType, err := lf.accountTypeRepo.ByID(txCtx, customer.AccountTypeID)
		if err != nil {
			return err
		}
		if accountType == nil {
			return ErrAccountTypeNotFound
		}

		// Verify and consume the OTP only after the password has been accepted.
		if err := lf.verifyLoginOTP(txCtx, customer.ID, req.OTPCode); err != nil {
			return err
		}

		// A valid login OTP also verifies the customer's mobile number.
		if !utils.IsTrue(customer.IsMobileVerified) {
			if err := lf.completeSignupAfterLogin(txCtx, customer); err != nil {
				return err
			}
			newlyVerified = true
			updated, err := lf.customerRepo.ByID(txCtx, customer.ID)
			if err != nil {
				return err
			}
			if updated != nil {
				customer = updated
			}
		}

		// Create new session
		session, err := lf.createSession(txCtx, customer.ID, metadata)
		if err != nil {
			return err
		}

		resp = &dto.LoginResponse{
			Customer: ToAuthCustomerDTO(*customer),
			Session:  ToCustomerSessionDTO(*session),
		}

		return nil
	})

	if err != nil {
		if IsAuthenticationFailed(err) || IsNoValidOTPFound(err) || IsInvalidOTPCode(err) {
			_ = lf.recordFailedLoginAttempt(ctx, req.Identifier, metadata)
		}
		errMsg := fmt.Sprintf("Login failed for identifier %s: %s", req.Identifier, err.Error())
		_ = lf.createAuditLog(ctx, customer, models.AuditActionLoginFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("LOGIN_FAILED", "Login failed", err)
	}
	_ = lf.clearFailedLoginAttempts(ctx, req.Identifier, metadata)

	if newlyVerified {
		msg := fmt.Sprintf("Signup completed successfully for customer %d", customer.ID)
		_ = lf.createAuditLog(ctx, customer, models.AuditActionSignupCompleted, msg, true, nil, metadata)
	}

	msg := fmt.Sprintf("User logged in successfully for identifier %s", req.Identifier)
	_ = lf.createAuditLog(ctx, customer, models.AuditActionLoginSuccess, msg, true, nil, metadata)

	return resp, nil
}

// RequestLoginOTP generates an OTP for login and sends it via SMS.
func (lf *LoginFlowImpl) RequestLoginOTP(
	ctx context.Context,
	req *dto.LoginOTPRequest,
	metadata *ClientMetadata,
) (*dto.LoginOTPResponse, error) {
	if err := lf.validateLoginOTPRequest(req); err != nil {
		return nil, NewBusinessError("LOGIN_OTP_VALIDATION_FAILED", "Login OTP validation failed", err)
	}
	if lf.rc == nil {
		return nil, NewBusinessError("LOGIN_OTP_CACHE_UNAVAILABLE", "Cache not available", ErrCacheNotAvailable)
	}
	req.Identifier = normalizeLoginIdentifier(req.Identifier)

	customer, err := lf.customerRepo.ByMobile(ctx, req.Identifier)
	if err != nil {
		return nil, err
	}
	if customer == nil || !utils.IsTrue(customer.IsActive) {
		return nil, ErrCustomerNotFound
	}

	key := lf.loginOTPKey(customer.ID)
	if _, ttl, err := lf.getOTPState(ctx, key); err == nil {
		return &dto.LoginOTPResponse{
			Message:     "OTP already generated and sent",
			CustomerID:  customer.ID,
			MaskedPhone: dto.MaskPhoneNumber(customer.RepresentativeMobile),
			OTPSent:     true,
			AlreadySent: true,
			OTPExpiry:   utils.UTCNowAdd(ttl),
		}, nil
	} else if err != nil && err != ErrNoValidOTPFound {
		return nil, err
	}

	otpCode, err := generateOTP()
	if err != nil {
		return nil, err
	}

	expiresAt := utils.UTCNowAdd(utils.OTPExpiry)
	if err := lf.saveOTPState(ctx, key, otpCode, utils.OTPExpiry); err != nil {
		return nil, err
	}

	message := fmt.Sprintf(lf.messageConfig.SigninVerificationCodeTemplate, otpCode)
	customerID := int64(customer.ID)
	recipient, err := normalizeOTPMobile(customer.RepresentativeMobile)
	if err != nil {
		_ = lf.deleteOTPState(ctx, key)
		return nil, err
	}
	if req.LogOTPToConsole {
		adminRecipient, err := normalizeOTPMobile(lf.adminConfig.ActiveLoginOTPForwardMobile())
		if err != nil {
			_ = lf.deleteOTPState(ctx, key)
			return nil, err
		}
		runAsyncOTPTask(ctx, "RequestLoginOTP send OTP to admin", func(asyncCtx context.Context) error {
			if err := lf.otpSMSSvc.SendOTP(asyncCtx, adminRecipient, message, &customerID); err != nil {
				_ = lf.deleteOTPState(asyncCtx, key)
				return err
			}
			return nil
		})
	} else {
		runAsyncOTPTask(ctx, "RequestLoginOTP send OTP", func(asyncCtx context.Context) error {
			if err := lf.otpSMSSvc.SendOTP(asyncCtx, recipient, message, &customerID); err != nil {
				_ = lf.deleteOTPState(asyncCtx, key)
				return err
			}
			return nil
		})
	}

	return &dto.LoginOTPResponse{
		Message:     "OTP sent successfully",
		CustomerID:  customer.ID,
		MaskedPhone: dto.MaskPhoneNumber(customer.RepresentativeMobile),
		OTPSent:     true,
		AlreadySent: false,
		OTPExpiry:   expiresAt,
	}, nil
}

// ForgotPassword initiates the password reset process
func (lf *LoginFlowImpl) ForgotPassword(
	ctx context.Context,
	req *dto.ForgotPasswordRequest,
	metadata *ClientMetadata,
) (*dto.ForgetPasswordResponse, error) {
	// Validate business rules
	if err := lf.validateForgotPasswordRequest(req); err != nil {
		return nil, NewBusinessError("FORGOT_PASSWORD_VALIDATION_FAILED", "Forgot password validation failed", err)
	}
	req.Identifier = normalizeLoginIdentifier(req.Identifier)

	var customer *models.Customer
	var resp *dto.ForgetPasswordResponse
	var otpRecipient string
	var otpMessage string
	var otpCustomerID int64
	var otpKey string

	err := repository.WithTransaction(ctx, lf.db, func(txCtx context.Context) error {
		var err error
		customer, err = lf.findCustomerByIdentifier(txCtx, req.Identifier)
		if err != nil {
			return err
		}
		if customer == nil {
			return ErrCustomerNotFound
		}
		if !utils.IsTrue(customer.IsActive) {
			return ErrAccountInactive
		}

		// Generate new OTP (Redis)
		otpCode, expiresAt, err := lf.generateAndSavePasswordResetOTP(txCtx, customer)
		if err != nil {
			return err
		}

		otpMessage = fmt.Sprintf(lf.messageConfig.PasswordResetVerificationCodeTemplate, otpCode)
		otpCustomerID = int64(customer.ID)
		otpRecipient, err = normalizeOTPMobile(customer.RepresentativeMobile)
		if err != nil {
			_ = lf.deleteOTPState(txCtx, lf.passwordResetOTPKey(customer.ID))
			return err
		}
		otpKey = lf.passwordResetOTPKey(customer.ID)

		resp = &dto.ForgetPasswordResponse{
			CustomerID:  customer.ID,
			MaskedPhone: dto.MaskPhoneNumber(customer.RepresentativeMobile),
			OTPExpiry:   expiresAt,
		}

		return nil
	})

	if err != nil {
		errMsg := fmt.Sprintf("Forgot password failed for identifier %s: %s", req.Identifier, err.Error())
		_ = lf.createAuditLog(ctx, customer, models.AuditActionPasswordResetFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("FORGOT_PASSWORD_FAILED", "Forgot password failed", err)
	}

	runAsyncOTPTask(ctx, "ForgotPassword send OTP", func(asyncCtx context.Context) error {
		if err := lf.otpSMSSvc.SendOTP(asyncCtx, otpRecipient, otpMessage, &otpCustomerID); err != nil {
			if lf.rc != nil {
				_ = lf.rc.Del(asyncCtx, otpKey).Err()
			}
			errMsg := fmt.Sprintf("OTP generated but SMS failed: %v", err)
			_ = lf.createAuditLog(asyncCtx, customer, models.AuditActionPasswordResetFailed, errMsg, false, &errMsg, metadata)
			return err
		}
		return nil
	})

	msg := fmt.Sprintf("Password reset OTP sent successfully for identifier %s", req.Identifier)
	_ = lf.createAuditLog(ctx, customer, models.AuditActionPasswordResetRequested, msg, true, nil, metadata)

	return resp, nil
}

// ResetPassword completes the password reset process with OTP verification
func (lf *LoginFlowImpl) ResetPassword(
	ctx context.Context,
	req *dto.ResetPasswordRequest,
	metadata *ClientMetadata,
) (*dto.ResetPasswordResponse, error) {
	// Validate business rules
	if err := lf.validateResetPasswordRequest(req); err != nil {
		return nil, NewBusinessError("RESET_PASSWORD_VALIDATION_FAILED", "Reset password validation failed", err)
	}

	var customer models.Customer
	var resp *dto.ResetPasswordResponse

	// Start transaction for password reset completion
	err := repository.WithTransaction(ctx, lf.db, func(txCtx context.Context) error {
		// Find the customer
		var err error
		customer, err = getCustomer(txCtx, lf.customerRepo, req.CustomerID)
		if err != nil {
			return err
		}

		// Verify OTP
		if err := lf.verifyPasswordResetOTP(txCtx, customer.ID, req.OTPCode); err != nil {
			return err
		}

		// Hash the new password
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		// Update customer password (maintain referential integrity)
		err = lf.customerRepo.UpdatePassword(txCtx, customer.ID, string(hashedPassword))
		if err != nil {
			return err
		}

		// Invalidate all existing sessions for this customer
		if err := lf.invalidateAllSessions(txCtx, customer.ID); err != nil {
			return err
		}

		// Create new session for the user
		session, err := lf.createSession(txCtx, customer.ID, metadata)
		if err != nil {
			return err
		}

		resp = &dto.ResetPasswordResponse{
			Customer: ToAuthCustomerDTO(customer),
			Session:  ToCustomerSessionDTO(*session),
		}

		return nil
	})

	if err != nil {
		errMsg := fmt.Sprintf("Password reset failed for customer %d: %s", req.CustomerID, err.Error())
		_ = lf.createAuditLog(ctx, &customer, models.AuditActionPasswordResetFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("PASSWORD_RESET_FAILED", "Password reset failed", err)
	}

	msg := fmt.Sprintf("Password reset completed successfully for customer %d", req.CustomerID)
	_ = lf.createAuditLog(ctx, &customer, models.AuditActionPasswordResetCompleted, msg, true, nil, metadata)

	return resp, nil
}

// Private helper methods

func (lf *LoginFlowImpl) findCustomerByIdentifier(ctx context.Context, identifier string) (*models.Customer, error) {
	identifier = normalizeLoginIdentifier(identifier)

	// Try to find by email first
	if strings.Contains(identifier, "@") {
		filter := models.CustomerFilter{
			Email:    &identifier,
			IsActive: utils.ToPtr(true),
		}
		customers, err := lf.customerRepo.ByFilter(ctx, filter, "", 0, 0)
		if err != nil {
			return nil, err
		}
		if len(customers) > 0 {
			// Return the most recent record (latest ID)
			return customers[0], nil
		}
	} else {
		// Try to find by mobile
		filter := models.CustomerFilter{
			RepresentativeMobile: &identifier,
			IsActive:             utils.ToPtr(true),
		}
		customers, err := lf.customerRepo.ByFilter(ctx, filter, "", 0, 0)
		if err != nil {
			return nil, err
		}
		if len(customers) > 0 {
			// Return the most recent record (latest ID)
			return customers[0], nil
		}
	}

	return nil, nil
}

func (lf *LoginFlowImpl) createSession(ctx context.Context, customerID uint, metadata *ClientMetadata) (*models.CustomerSession, error) {
	// Generate tokens
	accessToken, refreshToken, err := lf.tokenService.GenerateTokens(customerID)
	if err != nil {
		return nil, err
	}

	ipAddress := ""
	userAgent := ""
	if metadata != nil {
		ipAddress = metadata.IPAddress
		userAgent = metadata.UserAgent
	}

	// Create session record
	session := &models.CustomerSession{
		CorrelationID: uuid.New(),
		CustomerID:    customerID,
		SessionToken:  accessToken,
		RefreshToken:  &refreshToken,
		// DeviceInfo:    json.RawMessage, // TODO: Add device info
		IPAddress:      &ipAddress,
		UserAgent:      &userAgent,
		IsActive:       utils.ToPtr(true),
		ExpiresAt:      utils.UTCNowAdd(utils.SessionTimeout),
		LastAccessedAt: utils.UTCNow(),
	}

	err = lf.sessionRepo.Save(ctx, session)
	if err != nil {
		return nil, err
	}

	return session, nil
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// generateAndSavePasswordResetOTP creates a new OTP and stores it in Redis with expiry
func (lf *LoginFlowImpl) generateAndSavePasswordResetOTP(ctx context.Context, customer *models.Customer) (string, time.Time, error) {
	if lf.rc == nil {
		return "", time.Time{}, ErrCacheNotAvailable
	}
	key := lf.passwordResetOTPKey(customer.ID)
	if existing, ttl, err := lf.getOTPState(ctx, key); err == nil {
		if ttl > 0 && utils.UTCNow().Sub(existing.LastSentAt) < authOTPResendCooldown {
			return "", time.Time{}, ErrRateLimitExceeded
		}
	} else if err != ErrNoValidOTPFound {
		return "", time.Time{}, err
	}

	otpCode, err := generateOTP()
	if err != nil {
		return "", time.Time{}, err
	}

	expiresAt := utils.UTCNowAdd(utils.OTPExpiry)
	if err := lf.saveOTPState(ctx, key, otpCode, utils.OTPExpiry); err != nil {
		return "", time.Time{}, err
	}

	return otpCode, expiresAt, nil
}

func (lf *LoginFlowImpl) verifyLoginOTP(ctx context.Context, customerID uint, otpCode string) error {
	if lf.rc == nil {
		return ErrCacheNotAvailable
	}
	key := lf.loginOTPKey(customerID)
	return lf.verifyOTPState(ctx, key, otpCode, true)
}

// verifyPasswordResetOTP checks the OTP from Redis and consumes it on success
func (lf *LoginFlowImpl) verifyPasswordResetOTP(ctx context.Context, customerID uint, otpCode string) error {
	if lf.rc == nil {
		return ErrCacheNotAvailable
	}
	key := lf.passwordResetOTPKey(customerID)
	return lf.verifyOTPState(ctx, key, otpCode, true)
}

func (lf *LoginFlowImpl) invalidateAllSessions(ctx context.Context, customerID uint) error {
	// Find all active sessions for this customer
	filter := models.CustomerSessionFilter{
		CustomerID: &customerID,
		IsActive:   utils.ToPtr(true),
	}

	sessions, err := lf.sessionRepo.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return err
	}

	// Mark all as inactive (create new inactive records)
	for _, session := range sessions {
		session.IsActive = utils.ToPtr(false)
		session.ExpiresAt = utils.UTCNow()

		if err := lf.sessionRepo.Update(ctx, session); err != nil {
			return err
		}
	}

	return nil
}

func (lf *LoginFlowImpl) createAuditLog(
	ctx context.Context,
	customer *models.Customer,
	action string,
	description string,
	success bool,
	errMsg *string,
	metadata *ClientMetadata,
) error {
	var customerID *uint
	if customer != nil {
		customerID = &customer.ID
	}

	ipAddress := ""
	userAgent := ""
	if metadata != nil {
		ipAddress = metadata.IPAddress
		userAgent = metadata.UserAgent
	}

	audit := &models.AuditLog{
		CustomerID:   customerID,
		Action:       action,
		Description:  &description,
		Success:      utils.ToPtr(success),
		IPAddress:    &ipAddress,
		UserAgent:    &userAgent,
		ErrorMessage: errMsg,
	}

	requestID := ctx.Value(utils.RequestIDKey)
	if requestID != nil {
		requestIDStr, ok := requestID.(string)
		if ok {
			audit.RequestID = &requestIDStr
		}
	}

	if err := lf.auditRepo.Save(ctx, audit); err != nil {
		return err
	}

	return nil
}

func (lf *LoginFlowImpl) validateLoginRequest(request *dto.LoginRequest) error {
	// Login is intentionally mobile-only; email identifiers cannot receive the OTP.
	if strings.TrimSpace(request.Identifier) == "" || strings.Contains(request.Identifier, "@") {
		return ErrAuthenticationFailed
	}

	// Validate password is not empty
	if request.Password == "" {
		return ErrAuthenticationFailed
	}

	if !isSixDigitCode(request.OTPCode) {
		return ErrInvalidOTPCode
	}

	return nil
}

func (lf *LoginFlowImpl) validateLoginOTPRequest(request *dto.LoginOTPRequest) error {
	if strings.TrimSpace(request.Identifier) == "" || strings.Contains(request.Identifier, "@") {
		return ErrCustomerNotFound
	}
	return nil
}

func (lf *LoginFlowImpl) validateForgotPasswordRequest(request *dto.ForgotPasswordRequest) error {
	// Validate identifier is not empty
	if strings.TrimSpace(request.Identifier) == "" {
		return ErrCustomerNotFound
	}

	return nil
}

func (lf *LoginFlowImpl) completeSignupAfterLogin(ctx context.Context, customer *models.Customer) error {
	isMobileVerified := utils.ToPtr(true)
	mobileVerifiedAt := utils.UTCNowPtr()
	if err := lf.customerRepo.UpdateVerificationStatus(ctx, customer.ID, isMobileVerified, nil, mobileVerifiedAt, nil); err != nil {
		return err
	}

	if lf.notificationSvc != nil {
		adminMsg := fmt.Sprintf("New user verified: %s %s", customer.RepresentativeFirstName, customer.RepresentativeLastName)
		for _, mobile := range lf.adminConfig.ActiveMobiles() {
			_ = lf.notificationSvc.SendSMS(ctx, mobile, adminMsg, utils.ToPtr(int64(customer.ID)))
		}
	}

	return nil
}

func (lf *LoginFlowImpl) validateResetPasswordRequest(request *dto.ResetPasswordRequest) error {
	// Validate OTP code format (6 digits)
	if !isSixDigitCode(request.OTPCode) {
		return ErrInvalidOTPCode
	}

	// Validate password is not empty
	if request.NewPassword == "" {
		return ErrIncorrectPassword
	}

	// Validate password confirmation matches
	if request.NewPassword != request.ConfirmPassword {
		return ErrIncorrectPassword
	}

	return nil
}

func (lf *LoginFlowImpl) loginOTPKey(customerID uint) string {
	return fmt.Sprintf("login:otp:%d", customerID)
}

func (lf *LoginFlowImpl) passwordResetOTPKey(customerID uint) string {
	return fmt.Sprintf("password_reset:otp:%d", customerID)
}

func (lf *LoginFlowImpl) saveOTPState(ctx context.Context, key, code string, ttl time.Duration) error {
	state := otpChallengeState{
		OTPHash:    hashOTPCode(code),
		Attempts:   0,
		CreatedAt:  utils.UTCNow(),
		LastSentAt: utils.UTCNow(),
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return lf.rc.Set(ctx, key, payload, ttl).Err()
}

func (lf *LoginFlowImpl) getOTPState(ctx context.Context, key string) (*otpChallengeState, time.Duration, error) {
	raw, err := lf.rc.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, 0, ErrNoValidOTPFound
		}
		return nil, 0, err
	}
	var state otpChallengeState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		if isSixDigitCode(raw) {
			state = otpChallengeState{
				OTPHash:   hashOTPCode(raw),
				Attempts:  0,
				CreatedAt: utils.UTCNow(),
			}
		} else {
			return nil, 0, err
		}
	}
	ttl := lf.rc.TTL(ctx, key).Val()
	if ttl <= 0 {
		return nil, 0, ErrNoValidOTPFound
	}
	return &state, ttl, nil
}

func (lf *LoginFlowImpl) updateOTPState(ctx context.Context, key string, state *otpChallengeState, ttl time.Duration) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return lf.rc.Set(ctx, key, payload, ttl).Err()
}

func (lf *LoginFlowImpl) deleteOTPState(ctx context.Context, key string) error {
	return lf.rc.Del(ctx, key).Err()
}

func (lf *LoginFlowImpl) verifyOTPState(ctx context.Context, key, otpCode string, consumeOnSuccess bool) error {
	state, ttl, err := lf.getOTPState(ctx, key)
	if err != nil {
		return err
	}
	if state.Attempts >= authOTPMaxAttempts {
		_ = lf.deleteOTPState(ctx, key)
		return ErrRateLimitExceeded
	}
	if !verifyOTPCodeHash(otpCode, state.OTPHash) {
		state.Attempts++
		if state.Attempts >= authOTPMaxAttempts {
			_ = lf.deleteOTPState(ctx, key)
			return ErrRateLimitExceeded
		}
		if err := lf.updateOTPState(ctx, key, state, ttl); err != nil {
			return err
		}
		return ErrInvalidOTPCode
	}
	if consumeOnSuccess {
		return lf.deleteOTPState(ctx, key)
	}
	return nil
}

func (lf *LoginFlowImpl) enforceLoginRateLimit(ctx context.Context, identifier string, metadata *ClientMetadata) error {
	if lf.rc == nil {
		return nil
	}
	key := loginFailureKey(identifier, clientIPAddress(metadata))
	attempts, err := lf.rc.Get(ctx, key).Int()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	if attempts >= authLoginMaxFailures {
		return ErrRateLimitExceeded
	}
	return nil
}

func (lf *LoginFlowImpl) recordFailedLoginAttempt(ctx context.Context, identifier string, metadata *ClientMetadata) error {
	if lf.rc == nil {
		return nil
	}
	key := loginFailureKey(identifier, clientIPAddress(metadata))
	pipe := lf.rc.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, authLoginFailureWindow)
	_, err := pipe.Exec(ctx)
	return err
}

func (lf *LoginFlowImpl) clearFailedLoginAttempts(ctx context.Context, identifier string, metadata *ClientMetadata) error {
	if lf.rc == nil {
		return nil
	}
	key := loginFailureKey(identifier, clientIPAddress(metadata))
	return lf.rc.Del(ctx, key).Err()
}

func clientIPAddress(metadata *ClientMetadata) string {
	if metadata == nil || strings.TrimSpace(metadata.IPAddress) == "" {
		return "unknown"
	}
	return strings.TrimSpace(metadata.IPAddress)
}
