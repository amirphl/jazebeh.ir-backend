// Package businessflow contains the core business logic and use cases for authentication workflows
package businessflow

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
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
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// AdminAuthFlow represents the admin authentication flow used by handlers
type AdminAuthFlow interface {
	InitCaptcha(ctx context.Context) (*dto.AdminCaptchaInitResponse, error)
	Verify(ctx context.Context, req *dto.AdminCaptchaVerifyRequest, metadata *ClientMetadata) (*dto.AdminLoginInitResponse, error)
	VerifyOTP(ctx context.Context, req *dto.AdminLoginVerifyOTPRequest, metadata *ClientMetadata) (*dto.AdminLoginResponse, error)
}

// AdminAuthFlowImpl provides captcha-init and admin credential verification
type AdminAuthFlowImpl struct {
	adminRepo    repository.AdminRepository
	tokenService services.TokenService
	captchaSvc   services.CaptchaService
	otpSMSSvc    services.SMSService
	adminConfig  config.AdminConfig
	messageCfg   config.MessageConfig
	rc           *redis.Client
}

// adminLoginOTPMaxAttempts is intentionally separate from authOTPMaxAttempts so
// the admin and user limits can be tuned independently without silent coupling.
const adminLoginOTPMaxAttempts = 5

type adminLoginChallenge struct {
	ChallengeID string    `json:"challenge_id"`
	AdminID     uint      `json:"admin_id"`
	Username    string    `json:"username"`
	Phone       string    `json:"phone"`
	OTPHash     string    `json:"otp_hash"`
	CreatedAt   time.Time `json:"created_at"`
	LastSentAt  time.Time `json:"last_sent_at"`
}

func NewAdminAuthFlow(
	adminRepo repository.AdminRepository,
	tokenService services.TokenService,
	captchaSvc services.CaptchaService,
	otpSMSSvc services.SMSService,
	adminConfig config.AdminConfig,
	messageCfg config.MessageConfig,
	rc *redis.Client,
) AdminAuthFlow {
	return &AdminAuthFlowImpl{
		adminRepo:    adminRepo,
		tokenService: tokenService,
		captchaSvc:   captchaSvc,
		otpSMSSvc:    otpSMSSvc,
		adminConfig:  adminConfig,
		messageCfg:   messageCfg,
		rc:           rc,
	}
}

func (af *AdminAuthFlowImpl) InitCaptcha(ctx context.Context) (*dto.AdminCaptchaInitResponse, error) {
	if af.captchaSvc == nil {
		return nil, NewBusinessError("CAPTCHA_NOT_AVAILABLE", "Captcha service not available", ErrCacheNotAvailable)
	}
	ch, err := af.captchaSvc.GenerateRotate(ctx)
	if err != nil {
		return nil, NewBusinessError("CAPTCHA_INIT_FAILED", "Failed to initialize captcha", err)
	}
	return &dto.AdminCaptchaInitResponse{
		ChallengeID:       ch.ID,
		MasterImageBase64: ch.MasterImageBase64,
		ThumbImageBase64:  ch.ThumbImageBase64,
	}, nil
}

func (af *AdminAuthFlowImpl) Verify(
	ctx context.Context,
	req *dto.AdminCaptchaVerifyRequest,
	metadata *ClientMetadata,
) (*dto.AdminLoginInitResponse, error) {
	// Validate request
	if req == nil {
		return nil, NewBusinessError("ADMIN_LOGIN_VALIDATION_FAILED", "Admin login validation failed", ErrAuthenticationFailed)
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) == 0 || len(req.Password) == 0 {
		return nil, NewBusinessError("ADMIN_LOGIN_VALIDATION_FAILED", "Admin login validation failed", ErrAuthenticationFailed)
	}
	if len(req.ChallengeID) == 0 {
		return nil, NewBusinessError("CAPTCHA_INVALID", "Captcha challenge missing", ErrInvalidCaptcha)
	}

	// Verify captcha first
	if af.captchaSvc == nil || !af.captchaSvc.VerifyRotate(ctx, req.ChallengeID, req.UserAngle) {
		return nil, NewBusinessError("CAPTCHA_INVALID", "Captcha validation failed", ErrInvalidCaptcha)
	}
	if err := af.enforceAdminLoginRateLimit(ctx, req.Username, metadata); err != nil {
		return nil, NewBusinessError("ADMIN_LOGIN_RATE_LIMITED", "Admin login failed", err)
	}

	// Lookup admin
	admin, err := af.adminRepo.ByUsername(ctx, req.Username)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOOKUP_FAILED", "Failed to lookup admin", err)
	}
	if admin == nil {
		_ = af.recordAdminLoginFailure(ctx, req.Username, metadata)
		return nil, NewBusinessError("ADMIN_LOGIN_FAILED", "Admin login failed", ErrAuthenticationFailed)
	}
	if !utils.IsTrue(admin.IsActive) {
		_ = af.recordAdminLoginFailure(ctx, req.Username, metadata)
		return nil, NewBusinessError("ADMIN_LOGIN_FAILED", "Admin login failed", ErrAuthenticationFailed)
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		_ = af.recordAdminLoginFailure(ctx, req.Username, metadata)
		return nil, NewBusinessError("ADMIN_LOGIN_FAILED", "Admin login failed", ErrAuthenticationFailed)
	}

	if af.adminConfig.AllowsOTPBypass(af.adminConfig.TwoFAMobile(admin.Username)) {
		resp, err := af.issueAdminSession(admin)
		if err != nil {
			return nil, NewBusinessError("TOKEN_GENERATION_FAILED", "Failed to generate tokens", err)
		}
		_ = af.clearAdminLoginFailures(ctx, req.Username, metadata)
		return &dto.AdminLoginInitResponse{
			Message:           "Login successful",
			OTPSent:           false,
			AlreadySent:       false,
			RequiresTwoFactor: false,
			Admin:             &resp.Admin,
			Session:           &resp.Session,
		}, nil
	}

	resp, err := af.issueOrReuseOTP(ctx, admin.ID, admin.Username)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_FAILED", "Failed to start admin login verification", err)
	}
	// Clear only after OTP is confirmed sent so a transient SMS failure does not
	// reset the brute-force counter for a valid-password probe.
	_ = af.clearAdminLoginFailures(ctx, req.Username, metadata)

	return resp, nil
}

func (af *AdminAuthFlowImpl) VerifyOTP(
	ctx context.Context,
	req *dto.AdminLoginVerifyOTPRequest,
	metadata *ClientMetadata,
) (*dto.AdminLoginResponse, error) {
	if req == nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_VALIDATION_FAILED", "Admin OTP validation failed", ErrInvalidOTPCode)
	}
	if strings.TrimSpace(req.ChallengeID) == "" || !isSixDigitCode(req.OTPCode) {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_VALIDATION_FAILED", "Admin OTP validation failed", ErrInvalidOTPCode)
	}
	if af.rc == nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_CACHE_UNAVAILABLE", "Cache not available", ErrCacheNotAvailable)
	}

	challenge, ttl, err := af.getAdminLoginChallenge(ctx, req.ChallengeID)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_VERIFY_FAILED", "Admin OTP verification failed", err)
	}

	// Atomically increment the attempt counter before any OTP check so concurrent
	// requests each consume a distinct slot and cannot race past the limit.
	newAttempts, err := af.incrementAdminOTPAttempts(ctx, req.ChallengeID, ttl)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_VERIFY_FAILED", "Admin OTP verification failed", err)
	}
	if newAttempts > adminLoginOTPMaxAttempts {
		_ = af.deleteAdminLoginChallenge(ctx, req.ChallengeID, challenge.AdminID)
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_ATTEMPTS_EXCEEDED", "Admin OTP verification failed", ErrInvalidOTPCode)
	}

	if !verifyOTPCodeHash(req.OTPCode, challenge.OTPHash) {
		if newAttempts >= adminLoginOTPMaxAttempts {
			_ = af.deleteAdminLoginChallenge(ctx, req.ChallengeID, challenge.AdminID)
		}
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_INVALID", "Admin OTP verification failed", ErrInvalidOTPCode)
	}

	admin, err := af.adminRepo.ByID(ctx, challenge.AdminID)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOOKUP_FAILED", "Failed to lookup admin", err)
	}
	if admin == nil {
		_ = af.deleteAdminLoginChallenge(ctx, req.ChallengeID, challenge.AdminID)
		return nil, NewBusinessError("ADMIN_NOT_FOUND", "Admin not found", ErrAdminNotFound)
	}
	if !utils.IsTrue(admin.IsActive) {
		_ = af.deleteAdminLoginChallenge(ctx, req.ChallengeID, challenge.AdminID)
		return nil, NewBusinessError("ADMIN_INACTIVE", "Admin account is inactive", ErrAdminInactive)
	}

	// consumeAdminLoginChallenge atomically deletes the challenge and checks it
	// was still present. A concurrent request that already deleted it returns
	// false here, preventing a second token pair from being issued for a single OTP.
	consumed, err := af.consumeAdminLoginChallenge(ctx, req.ChallengeID, challenge.AdminID)
	if err != nil {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_VERIFY_FAILED", "Admin OTP verification failed", err)
	}
	if !consumed {
		return nil, NewBusinessError("ADMIN_LOGIN_OTP_INVALID", "Admin OTP verification failed", ErrInvalidOTPCode)
	}
	resp, err := af.issueAdminSession(admin)
	if err != nil {
		return nil, NewBusinessError("TOKEN_GENERATION_FAILED", "Failed to generate tokens", err)
	}
	return resp, nil
}

func (af *AdminAuthFlowImpl) issueOrReuseOTP(ctx context.Context, adminID uint, username string) (*dto.AdminLoginInitResponse, error) {
	if af.rc == nil {
		return nil, ErrCacheNotAvailable
	}
	if af.otpSMSSvc == nil {
		return nil, ErrAdminTwoFactorNotConfigured
	}

	mobile := af.adminConfig.TwoFAMobile(username)
	if mobile == "" {
		return nil, ErrAdminTwoFactorNotConfigured
	}
	recipient, err := normalizeOTPMobile(mobile)
	if err != nil {
		return nil, err
	}

	if existing, ttl, err := af.getExistingChallengeByAdminID(ctx, adminID); err == nil && existing != nil {
		return &dto.AdminLoginInitResponse{
			Message:           "OTP already generated and sent",
			ChallengeID:       existing.ChallengeID,
			MaskedPhone:       dto.MaskPhoneNumber(mobile),
			OTPSent:           true,
			AlreadySent:       true,
			OTPExpiresAt:      utils.UTCNowAdd(ttl),
			RequiresTwoFactor: true,
		}, nil
	} else if err != nil && err != redis.Nil && err != ErrNoValidOTPFound {
		return nil, err
	}

	otpCode, err := generateAdminOTP()
	if err != nil {
		return nil, err
	}
	challengeID, err := generateAdminLoginChallengeID()
	if err != nil {
		return nil, err
	}

	now := utils.UTCNow()
	challenge := &adminLoginChallenge{
		ChallengeID: challengeID,
		AdminID:     adminID,
		Username:    username,
		Phone:       recipient,
		OTPHash:     hashOTPCode(otpCode),
		CreatedAt:   now,
		LastSentAt:  now,
	}

	message := fmt.Sprintf(af.messageCfg.SigninVerificationCodeTemplate, otpCode)
	adminID64 := int64(adminID)
	if err := af.saveAdminLoginChallenge(ctx, challenge, utils.OTPExpiry); err != nil {
		return nil, err
	}
	if err := af.otpSMSSvc.SendOTP(ctx, recipient, message, &adminID64); err != nil {
		_ = af.deleteAdminLoginChallenge(ctx, challengeID, adminID)
		return nil, err
	}

	return &dto.AdminLoginInitResponse{
		Message:           "OTP sent successfully",
		ChallengeID:       challengeID,
		MaskedPhone:       dto.MaskPhoneNumber(mobile),
		OTPSent:           true,
		AlreadySent:       false,
		OTPExpiresAt:      now.Add(utils.OTPExpiry),
		RequiresTwoFactor: true,
	}, nil
}

func (af *AdminAuthFlowImpl) getExistingChallengeByAdminID(ctx context.Context, adminID uint) (*adminLoginChallenge, time.Duration, error) {
	challengeID, err := af.rc.Get(ctx, af.adminLoginIndexKey(adminID)).Result()
	if err != nil {
		return nil, 0, err
	}
	return af.getAdminLoginChallenge(ctx, challengeID)
}

func (af *AdminAuthFlowImpl) getAdminLoginChallenge(ctx context.Context, challengeID string) (*adminLoginChallenge, time.Duration, error) {
	key := af.adminLoginChallengeKey(challengeID)
	payload, err := af.rc.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, 0, ErrNoValidOTPFound
		}
		return nil, 0, err
	}
	var challenge adminLoginChallenge
	if err := json.Unmarshal([]byte(payload), &challenge); err != nil {
		return nil, 0, err
	}
	ttl := af.rc.TTL(ctx, key).Val()
	if ttl <= 0 {
		return nil, 0, ErrNoValidOTPFound
	}
	return &challenge, ttl, nil
}

func (af *AdminAuthFlowImpl) saveAdminLoginChallenge(ctx context.Context, challenge *adminLoginChallenge, ttl time.Duration) error {
	if challenge == nil {
		return ErrNoValidOTPFound
	}
	payload, err := json.Marshal(challenge)
	if err != nil {
		return err
	}
	pipe := af.rc.TxPipeline()
	pipe.Set(ctx, af.adminLoginChallengeKey(challenge.ChallengeID), payload, ttl)
	pipe.Set(ctx, af.adminLoginIndexKey(challenge.AdminID), challenge.ChallengeID, ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (af *AdminAuthFlowImpl) deleteAdminLoginChallenge(ctx context.Context, challengeID string, adminID uint) error {
	if af.rc == nil {
		return nil
	}
	return af.rc.Del(ctx,
		af.adminLoginChallengeKey(challengeID),
		af.adminLoginIndexKey(adminID),
		af.adminLoginAttemptsKey(challengeID),
	).Err()
}

// consumeAdminLoginChallenge atomically deletes all three challenge keys in a
// single MULTI/EXEC and reports whether the challenge key itself was present.
// Returns (false, nil) when the challenge was already consumed by a concurrent
// request, which prevents issuing a second token pair from a single OTP code.
func (af *AdminAuthFlowImpl) consumeAdminLoginChallenge(ctx context.Context, challengeID string, adminID uint) (bool, error) {
	if af.rc == nil {
		return true, nil
	}
	pipe := af.rc.TxPipeline()
	challengeDel := pipe.Del(ctx, af.adminLoginChallengeKey(challengeID))
	pipe.Del(ctx, af.adminLoginIndexKey(adminID))
	pipe.Del(ctx, af.adminLoginAttemptsKey(challengeID))
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return challengeDel.Val() > 0, nil
}

func (af *AdminAuthFlowImpl) adminLoginChallengeKey(challengeID string) string {
	return fmt.Sprintf("admin:login:otp:challenge:%s", challengeID)
}

func (af *AdminAuthFlowImpl) adminLoginIndexKey(adminID uint) string {
	return fmt.Sprintf("admin:login:otp:index:%d", adminID)
}

func (af *AdminAuthFlowImpl) adminLoginAttemptsKey(challengeID string) string {
	return fmt.Sprintf("admin:login:otp:attempts:%s", challengeID)
}

func (af *AdminAuthFlowImpl) incrementAdminOTPAttempts(ctx context.Context, challengeID string, ttl time.Duration) (int, error) {
	key := af.adminLoginAttemptsKey(challengeID)
	pipe := af.rc.TxPipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(incrCmd.Val()), nil
}

func generateAdminOTP() (string, error) {
	n, err := crand.Int(crand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateAdminLoginChallengeID() (string, error) {
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (af *AdminAuthFlowImpl) issueAdminSession(admin *models.Admin) (*dto.AdminLoginResponse, error) {
	if admin == nil {
		return nil, ErrAdminNotFound
	}
	accessToken, refreshToken, err := af.tokenService.GenerateAdminTokens(admin.ID)
	if err != nil {
		return nil, err
	}
	return &dto.AdminLoginResponse{
		Admin:   ToAdminDTOModel(*admin),
		Session: ToAdminSessionDTO(accessToken, refreshToken),
	}, nil
}

func (af *AdminAuthFlowImpl) enforceAdminLoginRateLimit(ctx context.Context, username string, metadata *ClientMetadata) error {
	if af.rc == nil {
		return nil
	}
	key := loginFailureKey(strings.ToLower(username), clientIPAddress(metadata))
	attempts, err := af.rc.Get(ctx, key).Int()
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

func (af *AdminAuthFlowImpl) recordAdminLoginFailure(ctx context.Context, username string, metadata *ClientMetadata) error {
	if af.rc == nil {
		return nil
	}
	key := loginFailureKey(strings.ToLower(username), clientIPAddress(metadata))
	pipe := af.rc.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, authLoginFailureWindow)
	_, err := pipe.Exec(ctx)
	return err
}

func (af *AdminAuthFlowImpl) clearAdminLoginFailures(ctx context.Context, username string, metadata *ClientMetadata) error {
	if af.rc == nil {
		return nil
	}
	key := loginFailureKey(strings.ToLower(username), clientIPAddress(metadata))
	return af.rc.Del(ctx, key).Err()
}
