// Package middleware contains HTTP middleware functions for request processing
package middleware

import (
	"errors"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/gofiber/fiber/v3"
)

// AuthMiddleware handles JWT token validation for protected endpoints
type AuthMiddleware struct {
	tokenService services.TokenService
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(tokenService services.TokenService) *AuthMiddleware {
	return &AuthMiddleware{
		tokenService: tokenService,
	}
}

// Authenticate is the middleware function that validates JWT tokens
func (m *AuthMiddleware) Authenticate() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get the Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Authorization header is required",
				Error: dto.ErrorDetail{
					Code: "MISSING_AUTHORIZATION_HEADER",
				},
			})
		}

		// Check if the header starts with "Bearer "
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Invalid authorization header format. Expected 'Bearer <token>'",
				Error: dto.ErrorDetail{
					Code: "INVALID_AUTHORIZATION_FORMAT",
				},
			})
		}

		// Extract the token (remove "Bearer " prefix)
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Access token is required",
				Error: dto.ErrorDetail{
					Code: "MISSING_ACCESS_TOKEN",
				},
			})
		}

		// Validate the token (this already checks for revocation)
		claims, err := m.tokenService.ValidateToken(token)
		if err != nil {
			var errorCode string
			var message string

			// Determine the specific error type
			if errors.Is(err, services.ErrTokenExpired) {
				errorCode = "TOKEN_EXPIRED"
				message = "Access token has expired"
			} else if errors.Is(err, services.ErrTokenInvalid) {
				errorCode = "TOKEN_INVALID"
				message = "Invalid access token"
			} else if errors.Is(err, services.ErrTokenRevoked) {
				errorCode = "TOKEN_REVOKED"
				message = "Access token has been revoked"
			} else {
				errorCode = "TOKEN_VALIDATION_FAILED"
				message = "Token validation failed"
			}

			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: message,
				Error: dto.ErrorDetail{
					Code: errorCode,
				},
			})
		}

		// Store user information in context for downstream handlers
		c.Locals("customer_id", claims.CustomerID)
		c.Locals("token_id", claims.TokenID)
		c.Locals("token_claims", claims)

		// Store RequestID for audit logging
		if requestID := c.Get("X-Request-ID"); requestID != "" {
			c.Locals("request_id", requestID)
		}

		// Continue to the next handler
		return c.Next()
	}
}

// AdminAuthenticate validates JWT tokens and sets admin-specific context values
func (m *AuthMiddleware) AdminAuthenticate() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get the Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Authorization header is required",
				Error:   dto.ErrorDetail{Code: "MISSING_AUTHORIZATION_HEADER"},
			})
		}

		// Check Bearer format
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Invalid authorization header format. Expected 'Bearer <token>'",
				Error:   dto.ErrorDetail{Code: "INVALID_AUTHORIZATION_FORMAT"},
			})
		}

		// Extract token
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Access token is required",
				Error:   dto.ErrorDetail{Code: "MISSING_ACCESS_TOKEN"},
			})
		}

		// Validate token (admin)
		adminClaims, err := m.tokenService.ValidateAdminToken(token)
		if err != nil {
			var code, msg string
			if errors.Is(err, services.ErrTokenExpired) {
				code = "TOKEN_EXPIRED"
				msg = "Access token has expired"
			} else if errors.Is(err, services.ErrTokenInvalid) {
				code = "TOKEN_INVALID"
				msg = "Invalid access token"
			} else if errors.Is(err, services.ErrTokenRevoked) {
				code = "TOKEN_REVOKED"
				msg = "Access token has been revoked"
			} else {
				code = "TOKEN_VALIDATION_FAILED"
				msg = "Token validation failed"
			}
			return c.Status(fiber.StatusUnauthorized).
				JSON(dto.APIResponse{Success: false, Message: msg, Error: dto.ErrorDetail{Code: code}})
		}

		// For admin tokens, use admin-specific claims
		c.Locals("admin_id", adminClaims.AdminID)
		c.Locals("token_id", adminClaims.TokenID)
		c.Locals("token_claims", adminClaims)

		if requestID := c.Get("X-Request-ID"); requestID != "" {
			c.Locals("request_id", requestID)
		}

		return c.Next()
	}
}

// BotAuthenticate validates JWT tokens and sets bot-specific context values
func (m *AuthMiddleware) BotAuthenticate() fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).
				JSON(dto.APIResponse{Success: false, Message: "Authorization header is required", Error: dto.ErrorDetail{Code: "MISSING_AUTHORIZATION_HEADER"}})
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).
				JSON(dto.APIResponse{Success: false, Message: "Invalid authorization header format. Expected 'Bearer <token>'", Error: dto.ErrorDetail{Code: "INVALID_AUTHORIZATION_FORMAT"}})
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).
				JSON(dto.APIResponse{Success: false, Message: "Access token is required", Error: dto.ErrorDetail{Code: "MISSING_ACCESS_TOKEN"}})
		}

		botClaims, err := m.tokenService.ValidateBotToken(token)
		if err != nil {
			var code, msg string
			if errors.Is(err, services.ErrTokenExpired) {
				code = "TOKEN_EXPIRED"
				msg = "Access token has expired"
			} else if errors.Is(err, services.ErrTokenInvalid) {
				code = "TOKEN_INVALID"
				msg = "Invalid access token"
			} else if errors.Is(err, services.ErrTokenRevoked) {
				code = "TOKEN_REVOKED"
				msg = "Access token has been revoked"
			} else {
				code = "TOKEN_VALIDATION_FAILED"
				msg = "Token validation failed"
			}
			return c.Status(fiber.StatusUnauthorized).
				JSON(dto.APIResponse{Success: false, Message: msg, Error: dto.ErrorDetail{Code: code}})
		}

		c.Locals("bot_id", botClaims.BotID)
		c.Locals("token_id", botClaims.TokenID)
		c.Locals("token_claims", botClaims)
		if requestID := c.Get("X-Request-ID"); requestID != "" {
			c.Locals("request_id", requestID)
		}
		return c.Next()
	}
}

// OptionalAuth is a middleware that validates JWT tokens if present, but doesn't require them
func (m *AuthMiddleware) OptionalAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get the Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			// No authorization header, continue without authentication
			return c.Next()
		}

		// Check if the header starts with "Bearer "
		if !strings.HasPrefix(authHeader, "Bearer ") {
			// Invalid format, continue without authentication
			return c.Next()
		}

		// Extract the token (remove "Bearer " prefix)
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			// Empty token, continue without authentication
			return c.Next()
		}

		// Try to validate the token (this already checks for revocation)
		claims, err := m.tokenService.ValidateToken(token)
		if err != nil {
			// Token is invalid, but this is optional auth, so continue
			return c.Next()
		}

		// Store user information in context for downstream handlers
		c.Locals("customer_id", claims.CustomerID)
		c.Locals("token_id", claims.TokenID)
		c.Locals("token_claims", claims)

		// Store RequestID for audit logging
		if requestID := c.Get("X-Request-ID"); requestID != "" {
			c.Locals("request_id", requestID)
		}

		// Continue to the next handler
		return c.Next()
	}
}

// GetCustomerIDFromContext extracts customer ID from the request context
func GetCustomerIDFromContext(c fiber.Ctx) (uint, bool) {
	customerID, ok := c.Locals("customer_id").(uint)
	return customerID, ok
}

// GetAdminIDFromContext extracts admin ID from the request context
func GetAdminIDFromContext(c fiber.Ctx) (uint, bool) {
	adminID, ok := c.Locals("admin_id").(uint)
	return adminID, ok
}

// GetBotIDFromContext extracts bot ID from the request context
func GetBotIDFromContext(c fiber.Ctx) (uint, bool) {
	botID, ok := c.Locals("bot_id").(uint)
	return botID, ok
}

// GetTokenClaimsFromContext extracts token claims from the request context
func GetTokenClaimsFromContext(c fiber.Ctx) (*services.TokenClaims, bool) {
	claims, ok := c.Locals("token_claims").(*services.TokenClaims)
	return claims, ok
}

// RequireAuth is a helper function that ensures authentication is required
func RequireAuth(c fiber.Ctx) error {
	customerID, exists := GetCustomerIDFromContext(c)
	if !exists {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Authentication required",
			Error: dto.ErrorDetail{
				Code: "AUTHENTICATION_REQUIRED",
			},
		})
	}

	// Check if customer ID is valid
	if customerID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Invalid customer ID",
			Error: dto.ErrorDetail{
				Code: "INVALID_CUSTOMER_ID",
			},
		})
	}

	return nil
}

// RequireAdminAuth ensures admin authentication is present
func RequireAdminAuth(c fiber.Ctx) error {
	adminID, exists := GetAdminIDFromContext(c)
	if !exists {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Admin authentication required",
			Error:   dto.ErrorDetail{Code: "ADMIN_AUTHENTICATION_REQUIRED"},
		})
	}
	if adminID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Invalid admin ID",
			Error:   dto.ErrorDetail{Code: "INVALID_ADMIN_ID"},
		})
	}
	return c.Next()
}

// RequireBotAuth ensures bot authentication is present
func RequireBotAuth(c fiber.Ctx) error {
	botID, exists := GetBotIDFromContext(c)
	if !exists {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Bot authentication required",
			Error:   dto.ErrorDetail{Code: "BOT_AUTHENTICATION_REQUIRED"},
		})
	}
	if botID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
			Success: false,
			Message: "Invalid bot ID",
			Error:   dto.ErrorDetail{Code: "INVALID_BOT_ID"},
		})
	}
	return c.Next()
}
