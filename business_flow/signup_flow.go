// Package businessflow contains the core business logic and use cases for authentication workflows
package businessflow

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	OTPTypeMobile = "mobile"
	OTPTypeEmail  = "email"
)

// SignupFlow handles the complete signup business logic
type SignupFlow interface {
	Signup(ctx context.Context, req *dto.SignupRequest, metadata *ClientMetadata) (*dto.SignupResponse, error)
	VerifyOTP(ctx context.Context, req *dto.OTPVerificationRequest, metadata *ClientMetadata) (*dto.OTPVerificationResponse, error)
	ResendOTP(ctx context.Context, req *dto.OTPResendRequest, metadata *ClientMetadata) (*dto.OTPResendResponse, error)
}

// SignupFlowImpl implements the signup business flow
type SignupFlowImpl struct {
	customerRepo       repository.CustomerRepository
	accountTypeRepo    repository.AccountTypeRepository
	sessionRepo        repository.CustomerSessionRepository
	auditRepo          repository.AuditLogRepository
	agencyDiscountRepo repository.AgencyDiscountRepository
	walletRepo         repository.WalletRepository
	tokenService       services.TokenService
	otpSMSSvc          services.SMSService
	notificationSvc    services.NotificationService
	adminConfig        config.AdminConfig
	messageConfig      config.MessageConfig
	db                 *gorm.DB
	rc                 *redis.Client
}

type pendingSignupData struct {
	Request      dto.SignupRequest `json:"request"`
	PasswordHash string            `json:"password_hash"`
	CreatedAt    time.Time         `json:"created_at"`
}

// NewSignupFlow creates a new signup flow instance
func NewSignupFlow(
	customerRepo repository.CustomerRepository,
	accountTypeRepo repository.AccountTypeRepository,
	sessionRepo repository.CustomerSessionRepository,
	auditRepo repository.AuditLogRepository,
	agencyDiscountRepo repository.AgencyDiscountRepository,
	walletRepo repository.WalletRepository,
	tokenService services.TokenService,
	otpSMSSvc services.SMSService,
	notificationSvc services.NotificationService,
	adminConfig config.AdminConfig,
	messageConfig config.MessageConfig,
	db *gorm.DB,
	rc *redis.Client,
) SignupFlow {
	return &SignupFlowImpl{
		customerRepo:       customerRepo,
		accountTypeRepo:    accountTypeRepo,
		sessionRepo:        sessionRepo,
		auditRepo:          auditRepo,
		agencyDiscountRepo: agencyDiscountRepo,
		walletRepo:         walletRepo,
		tokenService:       tokenService,
		otpSMSSvc:          otpSMSSvc,
		notificationSvc:    notificationSvc,
		adminConfig:        adminConfig,
		messageConfig:      messageConfig,
		db:                 db,
		rc:                 rc,
	}
}

// Signup handles the complete signup process
func (s *SignupFlowImpl) Signup(ctx context.Context, req *dto.SignupRequest, metadata *ClientMetadata) (*dto.SignupResponse, error) {
	s.sanitizeSignupRequest(req)

	// Validate business rules
	if err := s.validateSignupRequest(ctx, req); err != nil {
		return nil, NewBusinessError("SIGNUP_VALIDATION_FAILED", "Signup validation failed", err)
	}
	if s.rc == nil {
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", ErrCacheNotAvailable)
	}

	// Default referrer code if not provided
	if req.ReferrerAgencyCode == nil || len(strings.TrimSpace(*req.ReferrerAgencyCode)) == 0 {
		defaultCode := utils.DefaultReferrerAgencyCode
		req.ReferrerAgencyCode = &defaultCode
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", err)
	}
	req.Password = ""
	req.ConfirmPassword = ""

	pendingID, err := s.createPendingSignup(ctx, req, string(hashedPassword))
	if err != nil {
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", err)
	}
	otpCode, err := s.generateAndSaveOTP(ctx, pendingID, OTPTypeMobile)
	if err != nil {
		_ = s.deletePendingSignup(ctx, pendingID)
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", err)
	}

	customerID := int64(pendingID)
	message := fmt.Sprintf(s.messageConfig.SignupVerificationCodeTemplate, otpCode)
	recipient, err := normalizeOTPMobile(req.RepresentativeMobile)
	if err != nil {
		_ = s.deletePendingSignup(ctx, pendingID)
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", err)
	}
	if s.otpSMSSvc == nil {
		_ = s.deletePendingSignup(ctx, pendingID)
		return nil, NewBusinessError("SIGNUP_FAILED", "Signup failed", fmt.Errorf("sms service not configured"))
	}

	runAsyncOTPTask(ctx, "Signup send OTP", func(asyncCtx context.Context) error {
		if err := s.otpSMSSvc.SendOTP(asyncCtx, recipient, message, &customerID); err != nil {
			_ = s.deletePendingSignup(asyncCtx, pendingID)
			return err
		}
		return nil
	})

	return &dto.SignupResponse{
		Message:    "Signup initiated successfully. OTP sent to your mobile number.",
		CustomerID: pendingID,
		OTPSent:    true,
		OTPTarget:  s.maskMobileNumber(req.RepresentativeMobile),
	}, nil
}

// VerifyOTP handles OTP verification and completes signup
func (s *SignupFlowImpl) VerifyOTP(
	ctx context.Context,
	req *dto.OTPVerificationRequest,
	metadata *ClientMetadata,
) (*dto.OTPVerificationResponse, error) {
	// Validate business rules
	if err := s.validateOTPVerificationRequest(ctx, req); err != nil {
		return nil, NewBusinessError("OTP_VERIFICATION_VALIDATION_FAILED", "OTP verification validation failed", err)
	}
	pending, err := s.getPendingSignup(ctx, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("OTP_VERIFICATION_FAILED", "OTP verification failed", err)
	}

	var customer models.Customer
	var tokens struct {
		access  string
		refresh string
	}

	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		var err error

		// Verify OTP
		if err := s.verifyOTPCode(txCtx, req.CustomerID, req.OTPCode, req.OTPType); err != nil {
			return err
		}

		if err := s.validateSignupRequest(txCtx, &pending.Request); err != nil {
			return err
		}

		createdCustomer, err := s.createCustomer(txCtx, &pending.Request, pending.PasswordHash)
		if err != nil {
			return err
		}
		customer = *createdCustomer

		if err := s.createDefaultDiscount(txCtx, &customer); err != nil {
			return err
		}

		if _, err := createWalletWithInitialSnapshot(txCtx, s.walletRepo, customer.ID, "signup"); err != nil {
			return err
		}

		// Mark customer as verified and complete signup
		if err := s.completeSignup(txCtx, &customer, req.OTPType); err != nil {
			return err
		}

		// Generate tokens
		tokens.access, tokens.refresh, err = s.tokenService.GenerateTokens(customer.ID)
		if err != nil {
			return err
		}

		// Create session
		if err := s.createSession(txCtx, customer.ID, tokens.access, tokens.refresh, metadata); err != nil {
			return err
		}

		// Get customer again to get the updated customer
		customer, err = getCustomer(txCtx, s.customerRepo, customer.ID)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, NewBusinessError("OTP_VERIFICATION_FAILED", "OTP verification failed", err)
	}
	_ = s.deletePendingSignup(ctx, req.CustomerID)

	msg := fmt.Sprintf("Signup completed successfully for customer %d", customer.ID)
	_ = s.createAuditLog(ctx, &customer, models.AuditActionSignupCompleted, msg, true, nil, metadata)

	// Notify admins
	if s.notificationSvc != nil {
		adminMsg := fmt.Sprintf("New user verified: %s %s", customer.RepresentativeFirstName, customer.RepresentativeLastName)
		for _, mobile := range s.adminConfig.ActiveMobiles() {
			_ = s.notificationSvc.SendSMS(ctx, mobile, adminMsg, utils.ToPtr(int64(customer.ID)))
		}
	}

	return &dto.OTPVerificationResponse{
		Message:      "Signup completed successfully!",
		Token:        tokens.access,
		RefreshToken: tokens.refresh,
		Customer:     ToAuthCustomerDTO(customer),
	}, nil
}

// ResendOTP generates and sends a new OTP
func (s *SignupFlowImpl) ResendOTP(
	ctx context.Context,
	req *dto.OTPResendRequest,
	metadata *ClientMetadata,
) (*dto.OTPResendResponse, error) {
	// Validate business rules
	if err := s.validateOTPResendRequest(ctx, req); err != nil {
		return nil, NewBusinessError("OTP_RESEND_VALIDATION_FAILED", "OTP resend validation failed", err)
	}
	pending, err := s.getPendingSignup(ctx, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("RESEND_OTP_FAILED", "Resend OTP failed", err)
	}

	var target string
	target = pending.Request.RepresentativeMobile
	if req.OTPType == OTPTypeEmail {
		target = pending.Request.Email
	}

	otpCode, err := s.generateAndSaveOTP(ctx, req.CustomerID, req.OTPType)
	if err != nil {
		return nil, NewBusinessError("RESEND_OTP_FAILED", "Resend OTP failed", err)
	}

	message := fmt.Sprintf(s.messageConfig.OTPResendVerificationCodeTemplate, otpCode, utils.OTPExpiry.Minutes())
	if req.OTPType == OTPTypeMobile {
		customerID := int64(req.CustomerID)
		recipient, mobileErr := normalizeOTPMobile(target)
		if mobileErr != nil {
			return nil, NewBusinessError("RESEND_OTP_FAILED", "Resend OTP failed", mobileErr)
		}
		if s.otpSMSSvc == nil {
			_ = s.deleteSignupOTPState(ctx, req.CustomerID, req.OTPType)
			return nil, NewBusinessError("RESEND_OTP_FAILED", "Resend OTP failed", fmt.Errorf("sms service not configured"))
		}
		runAsyncOTPTask(ctx, "ResendOTP send mobile OTP", func(asyncCtx context.Context) error {
			if err := s.otpSMSSvc.SendOTP(asyncCtx, recipient, message, &customerID); err != nil {
				_ = s.deleteSignupOTPState(asyncCtx, req.CustomerID, req.OTPType)
				return err
			}
			return nil
		})
	} else {
		if s.notificationSvc == nil {
			_ = s.deleteSignupOTPState(ctx, req.CustomerID, req.OTPType)
			return nil, NewBusinessError("RESEND_OTP_FAILED", "Resend OTP failed", fmt.Errorf("notification service not configured"))
		}
		runAsyncOTPTask(ctx, "ResendOTP send email OTP", func(asyncCtx context.Context) error {
			_ = asyncCtx
			if err := s.notificationSvc.SendEmail(target, "Verification Code", message); err != nil {
				_ = s.deleteSignupOTPState(asyncCtx, req.CustomerID, req.OTPType)
				return err
			}
			return nil
		})
	}

	return &dto.OTPResendResponse{
		Message:         "OTP resent successfully",
		OTPSent:         true,
		MaskedOTPTarget: maskOTPTarget(target),
	}, nil
}

func (s *SignupFlowImpl) validateSignupRequest(ctx context.Context, req *dto.SignupRequest) error {
	if req == nil {
		return ErrCustomerNotFound
	}

	// Check if email already exists
	existingCustomer, err := s.customerRepo.ByEmail(ctx, req.Email)
	if err != nil {
		return err
	}
	if existingCustomer != nil {
		return ErrEmailAlreadyExists
	}

	// Check if mobile already exists
	existingCustomer, err = s.customerRepo.ByMobile(ctx, req.RepresentativeMobile)
	if err != nil {
		return err
	}
	if existingCustomer != nil {
		return ErrMobileAlreadyExists
	}

	// Validate company fields for business accounts
	if req.AccountType == models.AccountTypeIndependentCompany || req.AccountType == models.AccountTypeMarketingAgency {
		if req.CompanyName == nil || req.NationalID == nil || req.CompanyPhone == nil ||
			req.CompanyAddress == nil || req.PostalCode == nil {
			return ErrCompanyFieldsRequired
		}

		// Check if national ID already exists
		if existingCustomer, err := s.customerRepo.ByNationalID(ctx, *req.NationalID); err != nil {
			return err
		} else if existingCustomer != nil {
			return ErrNationalIDAlreadyExists
		}
	}

	// National ID requirement for individual accounts
	if req.AccountType == models.AccountTypeIndividual {
		if req.NationalID == nil || *req.NationalID == "" {
			return ErrNationalIDRequired
		}

		if existingCustomer, err := s.customerRepo.ByNationalID(ctx, *req.NationalID); err != nil {
			return err
		} else if existingCustomer != nil {
			return ErrNationalIDAlreadyExists
		}
	}

	// Sheba requirement for marketing_agency
	if req.AccountType == models.AccountTypeMarketingAgency {
		shebaNumber, err := ValidateShebaNumber(req.ShebaNumber)
		if err != nil {
			return err
		}
		req.ShebaNumber = &shebaNumber
	}

	// Check if agency exists (for marketing_agency account type)
	if req.ReferrerAgencyCode != nil {
		agencyFilter := models.CustomerFilter{
			AgencyRefererCode: req.ReferrerAgencyCode,
			IsActive:          utils.ToPtr(true),
			IsMobileVerified:  utils.ToPtr(true),
		}
		agencies, err := s.customerRepo.ByFilter(ctx, agencyFilter, "", 0, 0)
		if err != nil {
			return err
		}
		if len(agencies) == 0 {
			return ErrReferrerAgencyNotFound
		}
	}

	// Require job and category for non-agency customers
	if req.AccountType != models.AccountTypeMarketingAgency {
		if req.Job == nil || *req.Job == "" || req.Category == nil || *req.Category == "" {
			return ErrJobCategoryRequired
		}
	}

	return nil
}

func (s *SignupFlowImpl) createCustomer(ctx context.Context, req *dto.SignupRequest, passwordHash string) (*models.Customer, error) {
	// Get account type
	accountType, err := s.accountTypeRepo.ByTypeName(ctx, req.AccountType)
	if err != nil {
		return nil, err
	}
	if accountType == nil {
		return nil, ErrAccountTypeNotFound
	}

	// Find agency if provided
	var agencyID *uint
	if req.ReferrerAgencyCode != nil {
		// Find agency by agency_referer_code
		agency, err := s.customerRepo.ByAgencyRefererCode(ctx, *req.ReferrerAgencyCode)
		if err != nil {
			return nil, err
		}
		if agency == nil {
			return nil, ErrReferrerAgencyNotFound
		}
		if agency.AccountType.TypeName != models.AccountTypeMarketingAgency {
			return nil, ErrReferrerMustBeAgency
		}
		if !utils.IsTrue(agency.IsActive) {
			return nil, ErrReferrerAgencyInactive
		}
		agencyID = &agency.ID
	}

	// Create customer
	customer := &models.Customer{
		UUID:                    uuid.New(),
		AgencyRefererCode:       utils.GenerateRandomAgencyRefererCode(),
		AccountTypeID:           accountType.ID,
		AccountType:             *accountType,
		CompanyName:             req.CompanyName,
		NationalID:              req.NationalID,
		CompanyPhone:            req.CompanyPhone,
		CompanyAddress:          req.CompanyAddress,
		PostalCode:              req.PostalCode,
		ShebaNumber:             req.ShebaNumber,
		Job:                     req.Job,
		Category:                req.Category,
		RepresentativeFirstName: req.RepresentativeFirstName,
		RepresentativeLastName:  req.RepresentativeLastName,
		RepresentativeMobile:    req.RepresentativeMobile,
		Email:                   req.Email,
		PasswordHash:            passwordHash,
		ReferrerAgencyID:        agencyID,
		IsEmailVerified:         utils.ToPtr(false),
		IsMobileVerified:        utils.ToPtr(false),
		IsActive:                utils.ToPtr(true),
	}

	err = s.customerRepo.Save(ctx, customer)
	if err != nil {
		return nil, err
	}

	return customer, nil
}

func (s *SignupFlowImpl) generateAndSaveOTP(ctx context.Context, customerID uint, otpType string) (string, error) {
	// Generate 6-digit OTP
	otpCode, err := generateOTP()
	if err != nil {
		return "", err
	}

	if s.rc != nil {
		key := s.signupOTPKey(customerID, otpType)
		if existing, ttl, err := s.getSignupOTPState(ctx, customerID, otpType); err == nil {
			if ttl > 0 && utils.UTCNow().Sub(existing.LastSentAt) < authOTPResendCooldown {
				return "", ErrRateLimitExceeded
			}
		} else if err != ErrNoValidOTPFound {
			return "", err
		}

		state := otpChallengeState{
			OTPHash:    hashOTPCode(otpCode),
			Attempts:   0,
			CreatedAt:  utils.UTCNow(),
			LastSentAt: utils.UTCNow(),
		}
		payload, err := json.Marshal(state)
		if err != nil {
			return "", err
		}
		if err := s.rc.Set(ctx, key, payload, utils.OTPExpiry).Err(); err != nil {
			return "", err
		}
		return otpCode, nil
	}

	return "", ErrCacheNotAvailable
}

func (s *SignupFlowImpl) verifyOTPCode(ctx context.Context, customerID uint, code, otpType string) error {
	if s.rc != nil {
		state, ttl, err := s.getSignupOTPState(ctx, customerID, otpType)
		if err != nil {
			return err
		}
		key := s.signupOTPKey(customerID, otpType)
		if state.Attempts >= authOTPMaxAttempts {
			_ = s.rc.Del(ctx, key).Err()
			return ErrRateLimitExceeded
		}
		if !verifyOTPCodeHash(code, state.OTPHash) {
			state.Attempts++
			if state.Attempts >= authOTPMaxAttempts {
				_ = s.rc.Del(ctx, key).Err()
				return ErrRateLimitExceeded
			}
			payload, marshalErr := json.Marshal(state)
			if marshalErr != nil {
				return marshalErr
			}
			if err := s.rc.Set(ctx, key, payload, ttl).Err(); err != nil {
				return err
			}
			return ErrInvalidOTPCode
		}
		return nil
	}

	return ErrCacheNotAvailable
}

func (s *SignupFlowImpl) completeSignup(ctx context.Context, customer *models.Customer, otpType string) error {
	// Update verification status for existing customer (maintain referential integrity)
	var isMobileVerified, isEmailVerified *bool
	var mobileVerifiedAt, emailVerifiedAt *time.Time

	switch otpType {
	case OTPTypeMobile:
		isMobileVerified = utils.ToPtr(true)
		mobileVerifiedAt = utils.UTCNowPtr()
	case OTPTypeEmail:
		isEmailVerified = utils.ToPtr(true)
		emailVerifiedAt = utils.UTCNowPtr()
	default:
		return ErrInvalidOTPType
	}

	return s.customerRepo.UpdateVerificationStatus(ctx, customer.ID, isMobileVerified, isEmailVerified, mobileVerifiedAt, emailVerifiedAt)
}

func (s *SignupFlowImpl) createSession(
	ctx context.Context,
	customerID uint,
	accessToken, refreshToken string,
	metadata *ClientMetadata,
) error {
	ipAddress := ""
	userAgent := ""
	if metadata != nil {
		ipAddress = metadata.IPAddress
		userAgent = metadata.UserAgent
	}

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

	if err := s.sessionRepo.Save(ctx, session); err != nil {
		return err
	}

	return nil
}

func (s *SignupFlowImpl) createDefaultDiscount(ctx context.Context, customer *models.Customer) error {
	if customer.ReferrerAgencyID == nil {
		return nil
	}

	rate := 0.0
	if customer.AccountType.TypeName == models.AccountTypeMarketingAgency {
		rate = 0.5
	}

	// create agency discount for customer
	if err := s.agencyDiscountRepo.Save(ctx, &models.AgencyDiscount{
		UUID:         uuid.New(),
		AgencyID:     *customer.ReferrerAgencyID,
		CustomerID:   customer.ID,
		DiscountRate: rate,
		ExpiresAt:    nil,
		Reason:       utils.ToPtr("Created via Signup"),
	}); err != nil {
		return err
	}

	return nil
}

func (s *SignupFlowImpl) createAuditLog(
	ctx context.Context,
	customer *models.Customer,
	action, description string,
	success bool,
	errorMsg *string,
	metadata *ClientMetadata,
) error {
	var customerID *uint
	if customer != nil {
		customerID = &customer.ID
	}

	ipAddress := "127.0.0.1"
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
		ErrorMessage: errorMsg,
	}

	// Extract request ID from context if available
	requestID := ctx.Value(utils.RequestIDKey)
	if requestID != nil {
		requestIDStr, ok := requestID.(string)
		if ok {
			audit.RequestID = &requestIDStr
		}
	}

	if err := s.auditRepo.Save(ctx, audit); err != nil {
		return err
	}

	return nil
}

func (s *SignupFlowImpl) maskMobileNumber(mobile string) string {
	if len(mobile) < 8 {
		return mobile
	}
	// Show +989****1234 format
	return mobile[:4] + "****" + mobile[len(mobile)-4:]
}

func (s *SignupFlowImpl) validateOTPVerificationRequest(ctx context.Context, req *dto.OTPVerificationRequest) error {
	if req == nil {
		return ErrInvalidOTPCode
	}
	// Validate OTP type
	if req.OTPType != OTPTypeMobile && req.OTPType != OTPTypeEmail {
		return ErrInvalidOTPType
	}

	// Validate OTP code format (6 digits)
	if !isSixDigitCode(req.OTPCode) {
		return ErrInvalidOTPCode
	}

	if _, err := s.getPendingSignup(ctx, req.CustomerID); err != nil {
		return err
	}

	return nil
}

func (s *SignupFlowImpl) validateOTPResendRequest(ctx context.Context, req *dto.OTPResendRequest) error {
	if req == nil {
		return ErrInvalidOTPType
	}
	// Validate OTP type
	if req.OTPType != OTPTypeMobile && req.OTPType != OTPTypeEmail {
		return ErrInvalidOTPType
	}

	if _, err := s.getPendingSignup(ctx, req.CustomerID); err != nil {
		return err
	}

	return nil
}

func (s *SignupFlowImpl) pendingSignupKey(pendingID uint) string {
	return fmt.Sprintf("signup:pending:%d", pendingID)
}

func (s *SignupFlowImpl) createPendingSignup(ctx context.Context, req *dto.SignupRequest, passwordHash string) (uint, error) {
	if s.rc == nil {
		return 0, ErrCacheNotAvailable
	}

	payload := pendingSignupData{
		Request:      *req,
		PasswordHash: passwordHash,
		CreatedAt:    utils.UTCNow(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	for i := 0; i < 5; i++ {
		pendingID, err := generatePendingSignupID()
		if err != nil {
			return 0, err
		}
		ok, err := s.rc.SetNX(ctx, s.pendingSignupKey(pendingID), data, utils.OTPExpiry).Result()
		if err != nil {
			return 0, err
		}
		if ok {
			return pendingID, nil
		}
	}
	return 0, fmt.Errorf("failed to allocate pending signup id")
}

func (s *SignupFlowImpl) getPendingSignup(ctx context.Context, pendingID uint) (*pendingSignupData, error) {
	if s.rc == nil {
		return nil, ErrCacheNotAvailable
	}
	raw, err := s.rc.Get(ctx, s.pendingSignupKey(pendingID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCustomerNotFound
	}
	if err != nil {
		return nil, err
	}

	var out pendingSignupData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *SignupFlowImpl) deletePendingSignup(ctx context.Context, pendingID uint) error {
	if s.rc == nil {
		return ErrCacheNotAvailable
	}
	return s.rc.Del(
		ctx,
		s.pendingSignupKey(pendingID),
		s.signupOTPKey(pendingID, OTPTypeMobile),
		s.signupOTPKey(pendingID, OTPTypeEmail),
	).Err()
}

func generatePendingSignupID() (uint, error) {
	max := big.NewInt(math.MaxInt32)
	n, err := crand.Int(crand.Reader, max)
	if err != nil {
		return 0, err
	}
	id := uint(n.Int64())
	if id == 0 {
		return 1, nil
	}
	return id, nil
}

func (s *SignupFlowImpl) sanitizeSignupRequest(req *dto.SignupRequest) {
	if req == nil {
		return
	}

	req.Email = normalizeEmailIdentifier(req.Email)
	req.RepresentativeFirstName = strings.TrimSpace(req.RepresentativeFirstName)
	req.RepresentativeLastName = strings.TrimSpace(req.RepresentativeLastName)
	req.RepresentativeMobile = strings.TrimSpace(req.RepresentativeMobile)
	req.AccountType = strings.TrimSpace(req.AccountType)
	req.ReferrerAgencyCode = trimOptionalString(req.ReferrerAgencyCode)
	req.CompanyName = trimOptionalString(req.CompanyName)
	req.NationalID = trimOptionalString(req.NationalID)
	req.CompanyPhone = trimOptionalString(req.CompanyPhone)
	req.CompanyAddress = trimOptionalString(req.CompanyAddress)
	req.PostalCode = trimOptionalString(req.PostalCode)
	req.ShebaNumber = trimOptionalString(req.ShebaNumber)
	req.Job = trimOptionalString(req.Job)
	req.Category = trimOptionalString(req.Category)
}

func (s *SignupFlowImpl) signupOTPKey(customerID uint, otpType string) string {
	return fmt.Sprintf("signup:otp:%s:%d", otpType, customerID)
}

func (s *SignupFlowImpl) deleteSignupOTPState(ctx context.Context, customerID uint, otpType string) error {
	if s.rc == nil {
		return ErrCacheNotAvailable
	}
	return s.rc.Del(ctx, s.signupOTPKey(customerID, otpType)).Err()
}

func (s *SignupFlowImpl) getSignupOTPState(
	ctx context.Context,
	customerID uint,
	otpType string,
) (*otpChallengeState, time.Duration, error) {
	key := s.signupOTPKey(customerID, otpType)
	raw, err := s.rc.Get(ctx, key).Result()
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
	ttl := s.rc.TTL(ctx, key).Val()
	if ttl <= 0 {
		return nil, 0, ErrNoValidOTPFound
	}
	return &state, ttl, nil
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
