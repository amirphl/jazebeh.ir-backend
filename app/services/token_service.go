// Package services provides external service integrations and technical concerns like notifications and tokens
package services

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/golang-jwt/jwt/v5"
)

// Token service error constants
var (
	ErrTokenExpired = errors.New("token has expired")
	ErrTokenInvalid = errors.New("invalid token")
	ErrTokenRevoked = errors.New("token has been revoked")
)

// TokenService handles JWT token generation and validation
type TokenService interface {
	GenerateTokens(customerID uint) (accessToken, refreshToken string, err error)
	ValidateToken(token string) (*TokenClaims, error)
	RefreshToken(refreshToken string) (newAccessToken, newRefreshToken string, err error)
	RevokeToken(token string) error
	GetTokenClaims(token string) (*TokenClaims, error)
	IsTokenRevoked(token string) bool
	GenerateAdminTokens(adminID uint) (accessToken, refreshToken string, err error)
	ValidateAdminToken(token string) (*AdminTokenClaims, error)
	// Bot tokens
	GenerateBotTokens(botID uint) (accessToken, refreshToken string, err error)
	ValidateBotToken(token string) (*BotTokenClaims, error)
}

// TokenClaims represents the claims in a JWT token
type TokenClaims struct {
	CustomerID uint      `json:"customer_id"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	TokenType  string    `json:"token_type"` // "access" or "refresh"
	TokenID    string    `json:"jti"`        // JWT ID for token revocation
}

// AdminTokenClaims represents claims for admin JWTs
type AdminTokenClaims struct {
	AdminID   uint      `json:"admin_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
	TokenID   string    `json:"jti"`
}

// BotTokenClaims represents claims for bot JWTs
type BotTokenClaims struct {
	BotID     uint      `json:"bot_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenType string    `json:"token_type"`
	TokenID   string    `json:"jti"`
}

// TokenServiceImpl implements TokenService
type TokenServiceImpl struct {
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	signingMethod   jwt.SigningMethod
	privateKey      *rsa.PrivateKey
	publicKey       *rsa.PublicKey
	secretKey       []byte
	useRSAKeys      bool
	issuer          string
	audience        string
	mu              sync.RWMutex
	revokedTokens   map[[sha256.Size]byte]time.Time
}

// NewTokenService creates a new token service
func NewTokenService(
	accessTokenTTL, refreshTokenTTL time.Duration,
	issuer, audience string,
	useRSAKeys bool,
	privateKeyPEM, publicKeyPEM, secretKey string,
) (TokenService, error) {
	var privateKey *rsa.PrivateKey
	var publicKey *rsa.PublicKey
	var secretKeyBytes []byte
	var signingMethod jwt.SigningMethod

	if useRSAKeys {
		// Use RSA keys
		var err error
		privateKey, publicKey, err = parseRSAKeys(privateKeyPEM, publicKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse RSA keys: %w", err)
		}
		signingMethod = jwt.SigningMethodRS256
	} else {
		// Use symmetric key
		if secretKey == "" {
			return nil, fmt.Errorf("secret key is required when not using RSA keys")
		}
		secretKeyBytes = []byte(secretKey)
		signingMethod = jwt.SigningMethodHS256
	}

	return &TokenServiceImpl{
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
		signingMethod:   signingMethod,
		privateKey:      privateKey,
		publicKey:       publicKey,
		secretKey:       secretKeyBytes,
		useRSAKeys:      useRSAKeys,
		issuer:          issuer,
		audience:        audience,
		revokedTokens:   make(map[[sha256.Size]byte]time.Time),
	}, nil
}

// parseRSAKeys parses RSA private and public keys from PEM format
func parseRSAKeys(privateKeyPEM, publicKeyPEM string) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	if privateKeyPEM == "" || publicKeyPEM == "" {
		return nil, nil, fmt.Errorf("both private and public keys are required")
	}

	// Parse private key
	privateKeyBlock, _ := pem.Decode([]byte(privateKeyPEM))
	if privateKeyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode private key")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(privateKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Parse public key
	publicKeyBlock, _ := pem.Decode([]byte(publicKeyPEM))
	if publicKeyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode public key")
	}

	publicKey, err := x509.ParsePKIXPublicKey(publicKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("public key is not RSA")
	}

	return privateKey, rsaPublicKey, nil
}

// GenerateTokens generates access and refresh tokens for a customer
func (s *TokenServiceImpl) GenerateTokens(customerID uint) (accessToken, refreshToken string, err error) {
	now := utils.UTCNow()

	// Generate unique token IDs
	accessTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	refreshTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	// Generate access token
	accessClaims := jwt.MapClaims{
		"customer_id": customerID,
		"token_type":  "access",
		"jti":         accessTokenID,
		"iat":         now.Unix(),
		"exp":         now.Add(s.accessTokenTTL).Unix(),
		"iss":         s.issuer,
		"aud":         s.audience,
	}

	accessToken, err = s.generateToken(accessClaims)
	if err != nil {
		return "", "", err
	}

	// Generate refresh token
	refreshClaims := jwt.MapClaims{
		"customer_id": customerID,
		"token_type":  "refresh",
		"jti":         refreshTokenID,
		"iat":         now.Unix(),
		"exp":         now.Add(s.refreshTokenTTL).Unix(),
		"iss":         s.issuer,
		"aud":         s.audience,
	}

	refreshToken, err = s.generateToken(refreshClaims)
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// GenerateAdminTokens generates access and refresh tokens for an admin (same TTLs, different claim key)
func (s *TokenServiceImpl) GenerateAdminTokens(adminID uint) (accessToken, refreshToken string, err error) {
	now := utils.UTCNow()

	accessTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	refreshTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	accessClaims := jwt.MapClaims{
		"admin_id":   adminID,
		"token_type": "access",
		"jti":        accessTokenID,
		"iat":        now.Unix(),
		"exp":        now.Add(s.accessTokenTTL).Unix(),
		"iss":        s.issuer,
		"aud":        s.audience,
	}

	accessToken, err = s.generateToken(accessClaims)
	if err != nil {
		return "", "", err
	}

	refreshClaims := jwt.MapClaims{
		"admin_id":   adminID,
		"token_type": "refresh",
		"jti":        refreshTokenID,
		"iat":        now.Unix(),
		"exp":        now.Add(s.refreshTokenTTL).Unix(),
		"iss":        s.issuer,
		"aud":        s.audience,
	}

	refreshToken, err = s.generateToken(refreshClaims)
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// GenerateBotTokens generates access and refresh tokens for a bot
func (s *TokenServiceImpl) GenerateBotTokens(botID uint) (accessToken, refreshToken string, err error) {
	now := utils.UTCNow()

	accessTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	refreshTokenID, err := generateTokenID()
	if err != nil {
		return "", "", err
	}

	accessClaims := jwt.MapClaims{
		"bot_id":     botID,
		"token_type": "access",
		"jti":        accessTokenID,
		"iat":        now.Unix(),
		"exp":        now.Add(s.accessTokenTTL).Unix(),
		"iss":        s.issuer,
		"aud":        s.audience,
	}

	accessToken, err = s.generateToken(accessClaims)
	if err != nil {
		return "", "", err
	}

	refreshClaims := jwt.MapClaims{
		"bot_id":     botID,
		"token_type": "refresh",
		"jti":        refreshTokenID,
		"iat":        now.Unix(),
		"exp":        now.Add(s.refreshTokenTTL).Unix(),
		"iss":        s.issuer,
		"aud":        s.audience,
	}

	refreshToken, err = s.generateToken(refreshClaims)
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// ValidateToken validates a JWT token and returns claims
func (s *TokenServiceImpl) ValidateToken(token string) (*TokenClaims, error) {
	var err error
	var parsedToken *jwt.Token

	if s.useRSAKeys {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			// Validate signing method

			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			return s.publicKey, nil
		})
	} else {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			// Validate signing method
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			return s.secretKey, nil
		})
	}

	if err != nil {
		// Check if the error is due to token expiration
		if strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "exp") {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	if !parsedToken.Valid {
		return nil, ErrTokenInvalid
	}

	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenInvalid
	}

	// Extract claims
	customerID, ok := claims["customer_id"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}

	tokenType, ok := claims["token_type"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}

	tokenID, ok := claims["jti"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}

	issuedAt, ok := claims["iat"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}

	expiresAt, ok := claims["exp"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}

	// Check if token has expired
	if utils.UTCNow().After(time.Unix(int64(expiresAt), 0)) {
		return nil, ErrTokenExpired
	}

	// Check if token has been revoked
	if s.IsTokenRevoked(token) {
		return nil, ErrTokenRevoked
	}

	return &TokenClaims{
		CustomerID: uint(customerID),
		TokenType:  tokenType,
		TokenID:    tokenID,
		IssuedAt:   time.Unix(int64(issuedAt), 0),
		ExpiresAt:  time.Unix(int64(expiresAt), 0),
	}, nil
}

// ValidateAdminToken validates an admin JWT and returns admin-specific claims
func (s *TokenServiceImpl) ValidateAdminToken(token string) (*AdminTokenClaims, error) {
	var err error
	var parsedToken *jwt.Token

	if s.useRSAKeys {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return s.publicKey, nil
		})
	} else {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return s.secretKey, nil
		})
	}
	if err != nil {
		if strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "exp") {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	if !parsedToken.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenInvalid
	}

	adminID, ok := claims["admin_id"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	tokenType, ok := claims["token_type"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}
	tokenID, ok := claims["jti"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}
	issuedAt, ok := claims["iat"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	expiresAt, ok := claims["exp"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	if utils.UTCNow().After(time.Unix(int64(expiresAt), 0)) {
		return nil, ErrTokenExpired
	}
	if s.IsTokenRevoked(token) {
		return nil, ErrTokenRevoked
	}
	return &AdminTokenClaims{
		AdminID:   uint(adminID),
		TokenType: tokenType,
		TokenID:   tokenID,
		IssuedAt:  time.Unix(int64(issuedAt), 0),
		ExpiresAt: time.Unix(int64(expiresAt), 0),
	}, nil
}

// ValidateBotToken validates a bot JWT and returns bot-specific claims
func (s *TokenServiceImpl) ValidateBotToken(token string) (*BotTokenClaims, error) {
	var err error
	var parsedToken *jwt.Token
	if s.useRSAKeys {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return s.publicKey, nil
		})
	} else {
		parsedToken, err = jwt.Parse(token, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return s.secretKey, nil
		})
	}
	if err != nil {
		if strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "exp") {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	if !parsedToken.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenInvalid
	}
	botID, ok := claims["bot_id"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	tokenType, ok := claims["token_type"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}
	tokenID, ok := claims["jti"].(string)
	if !ok {
		return nil, ErrTokenInvalid
	}
	issuedAt, ok := claims["iat"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	expiresAt, ok := claims["exp"].(float64)
	if !ok {
		return nil, ErrTokenInvalid
	}
	if utils.UTCNow().After(time.Unix(int64(expiresAt), 0)) {
		return nil, ErrTokenExpired
	}
	if s.IsTokenRevoked(token) {
		return nil, ErrTokenRevoked
	}
	return &BotTokenClaims{
		BotID:     uint(botID),
		TokenType: tokenType,
		TokenID:   tokenID,
		IssuedAt:  time.Unix(int64(issuedAt), 0),
		ExpiresAt: time.Unix(int64(expiresAt), 0),
	}, nil
}

// RefreshToken generates new tokens using a refresh token
func (s *TokenServiceImpl) RefreshToken(refreshToken string) (newAccessToken, newRefreshToken string, err error) {
	// Validate refresh token
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return "", "", fmt.Errorf("invalid refresh token: %w", err)
	}

	if claims.TokenType != "refresh" {
		return "", "", fmt.Errorf("token is not a refresh token")
	}

	if utils.UTCNow().After(claims.ExpiresAt) {
		return "", "", fmt.Errorf("refresh token has expired")
	}

	// Generate new tokens
	return s.GenerateTokens(claims.CustomerID)
}

// RevokeToken validates a token and keeps a hashed revocation entry until the
// token expires. Deployments that require revocations to survive restarts
// should replace this process-local store with Redis or durable persistence.
func (s *TokenServiceImpl) RevokeToken(token string) error {
	claims, err := s.ValidateToken(token)
	if err != nil {
		return fmt.Errorf("invalid token: %w", err)
	}
	digest := sha256.Sum256([]byte(token))
	now := utils.UTCNow()

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expiresAt := range s.revokedTokens {
		if !expiresAt.After(now) {
			delete(s.revokedTokens, key)
		}
	}
	s.revokedTokens[digest] = claims.ExpiresAt
	return nil
}

// GetTokenClaims returns fully validated customer-token claims.
func (s *TokenServiceImpl) GetTokenClaims(token string) (*TokenClaims, error) {
	return s.ValidateToken(token)
}

// IsTokenRevoked checks if a token has been revoked
func (s *TokenServiceImpl) IsTokenRevoked(token string) bool {
	digest := sha256.Sum256([]byte(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	expiresAt, exists := s.revokedTokens[digest]
	return exists && expiresAt.After(utils.UTCNow())
}

// generateToken creates a signed JWT token
func (s *TokenServiceImpl) generateToken(claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(s.signingMethod, claims)

	var signedString string
	var err error

	if s.useRSAKeys {
		signedString, err = token.SignedString(s.privateKey)
	} else {
		signedString, err = token.SignedString(s.secretKey)
	}

	if err != nil {
		return "", err
	}

	return signedString, nil
}

// generateTokenID generates a unique token ID
func generateTokenID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", bytes), nil
}
