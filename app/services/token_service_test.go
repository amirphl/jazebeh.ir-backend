// Package services provides external service integrations and technical concerns like notifications and tokens
package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestTokenService creates a token service for testing with symmetric key
func createTestTokenService() (TokenService, error) {
	return NewTokenService(
		15*time.Minute,
		7*24*time.Hour,
		"test-issuer",
		"test-audience",
		false, // useRSAKeys
		"",    // privateKeyPEM
		"",    // publicKeyPEM
		"test-secret-key-for-jwt-signing-32-chars", // secretKey
	)
}

func TestNewTokenService(t *testing.T) {
	tests := []struct {
		name            string
		accessTokenTTL  time.Duration
		refreshTokenTTL time.Duration
		issuer          string
		audience        string
		useRSAKeys      bool
		privateKeyPEM   string
		publicKeyPEM    string
		secretKey       string
		expectError     bool
	}{
		{
			name:            "valid symmetric key configuration",
			accessTokenTTL:  15 * time.Minute,
			refreshTokenTTL: 7 * 24 * time.Hour,
			issuer:          "test-issuer",
			audience:        "test-audience",
			useRSAKeys:      false,
			privateKeyPEM:   "",
			publicKeyPEM:    "",
			secretKey:       "test-secret-key-for-jwt-signing-32-chars",
			expectError:     false,
		},
		{
			name:            "missing secret key",
			accessTokenTTL:  15 * time.Minute,
			refreshTokenTTL: 7 * 24 * time.Hour,
			issuer:          "test-issuer",
			audience:        "test-audience",
			useRSAKeys:      false,
			privateKeyPEM:   "",
			publicKeyPEM:    "",
			secretKey:       "",
			expectError:     true,
		},
		{
			name:            "empty issuer and audience",
			accessTokenTTL:  15 * time.Minute,
			refreshTokenTTL: 7 * 24 * time.Hour,
			issuer:          "",
			audience:        "",
			useRSAKeys:      false,
			privateKeyPEM:   "",
			publicKeyPEM:    "",
			secretKey:       "test-secret-key-for-jwt-signing-32-chars",
			expectError:     false, // Should not error, just use empty strings
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewTokenService(
				tt.accessTokenTTL,
				tt.refreshTokenTTL,
				tt.issuer,
				tt.audience,
				tt.useRSAKeys,
				tt.privateKeyPEM,
				tt.publicKeyPEM,
				tt.secretKey,
			)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, service)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, service)
			}
		})
	}
}

func TestGenerateTokens(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	tests := []struct {
		name        string
		customerID  uint
		expectError bool
	}{
		{
			name:        "valid customer ID",
			customerID:  123,
			expectError: false,
		},
		{
			name:        "zero customer ID",
			customerID:  0,
			expectError: false,
		},
		{
			name:        "large customer ID",
			customerID:  999999999,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accessToken, refreshToken, err := service.GenerateTokens(tt.customerID)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, accessToken)
				assert.Empty(t, refreshToken)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, accessToken)
				assert.NotEmpty(t, refreshToken)
				assert.NotEqual(t, accessToken, refreshToken)

				// Verify tokens are different
				assert.NotEqual(t, accessToken, refreshToken)

				// Verify tokens are valid JWT format (should start with "eyJ")
				assert.Contains(t, accessToken, "eyJ")
				assert.Contains(t, refreshToken, "eyJ")
			}
		})
	}
}

func TestValidateToken(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	// Generate valid tokens for testing
	accessToken, refreshToken, err := service.GenerateTokens(123)
	require.NoError(t, err)

	tests := []struct {
		name         string
		token        string
		expectError  bool
		expectClaims *TokenClaims
	}{
		{
			name:        "valid access token",
			token:       accessToken,
			expectError: false,
			expectClaims: &TokenClaims{
				CustomerID: 123,
				TokenType:  "access",
			},
		},
		{
			name:        "valid refresh token",
			token:       refreshToken,
			expectError: false,
			expectClaims: &TokenClaims{
				CustomerID: 123,
				TokenType:  "refresh",
			},
		},
		{
			name:         "empty token",
			token:        "",
			expectError:  true,
			expectClaims: nil,
		},
		{
			name:         "invalid token format",
			token:        "invalid.token.format",
			expectError:  true,
			expectClaims: nil,
		},
		{
			name:         "malformed token",
			token:        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid.signature",
			expectError:  true,
			expectClaims: nil,
		},
		{
			name:         "token with wrong signature",
			token:        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJjdXN0b21lcl9pZCI6MTIzLCJ0b2tlbl90eXBlIjoiYWNjZXNzIn0.wrong_signature",
			expectError:  true,
			expectClaims: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := service.ValidateToken(tt.token)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)

				if tt.expectClaims != nil {
					assert.Equal(t, tt.expectClaims.CustomerID, claims.CustomerID)
					assert.Equal(t, tt.expectClaims.TokenType, claims.TokenType)
					assert.NotEmpty(t, claims.TokenID)
					assert.False(t, claims.IssuedAt.IsZero())
					assert.False(t, claims.ExpiresAt.IsZero())
					assert.True(t, claims.ExpiresAt.After(claims.IssuedAt))
				}
			}
		})
	}
}

func TestRefreshToken(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	// Generate valid tokens for testing
	_, refreshToken, err := service.GenerateTokens(123)
	require.NoError(t, err)

	tests := []struct {
		name            string
		refreshToken    string
		expectError     bool
		expectNewTokens bool
	}{
		{
			name:            "valid refresh token",
			refreshToken:    refreshToken,
			expectError:     false,
			expectNewTokens: true,
		},
		{
			name:            "empty refresh token",
			refreshToken:    "",
			expectError:     true,
			expectNewTokens: false,
		},
		{
			name:            "invalid refresh token",
			refreshToken:    "invalid.token",
			expectError:     true,
			expectNewTokens: false,
		},
		{
			name:            "access token instead of refresh token",
			refreshToken:    func() string { token, _, _ := service.GenerateTokens(123); return token }(),
			expectError:     true,
			expectNewTokens: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newAccessToken, newRefreshToken, err := service.RefreshToken(tt.refreshToken)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, newAccessToken)
				assert.Empty(t, newRefreshToken)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, newAccessToken)
				assert.NotEmpty(t, newRefreshToken)
				assert.NotEqual(t, newAccessToken, newRefreshToken)
				assert.NotEqual(t, newAccessToken, tt.refreshToken)
				assert.NotEqual(t, newRefreshToken, tt.refreshToken)
			}
		})
	}
}

func TestRevokeToken(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	// Generate valid token for testing
	accessToken, _, err := service.GenerateTokens(123)
	require.NoError(t, err)

	tests := []struct {
		name        string
		token       string
		expectError bool
	}{
		{
			name:        "valid token",
			token:       accessToken,
			expectError: false,
		},
		{
			name:        "empty token",
			token:       "",
			expectError: true,
		},
		{
			name:        "invalid token",
			token:       "invalid.token",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.RevokeToken(tt.token)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}

	claims, err := service.ValidateToken(accessToken)
	assert.ErrorIs(t, err, ErrTokenRevoked)
	assert.Nil(t, claims)
}

func TestGetTokenClaims(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	// Generate valid token for testing
	accessToken, _, err := service.GenerateTokens(123)
	require.NoError(t, err)

	tests := []struct {
		name         string
		token        string
		expectError  bool
		expectClaims *TokenClaims
	}{
		{
			name:        "valid token",
			token:       accessToken,
			expectError: false,
			expectClaims: &TokenClaims{
				CustomerID: 123,
				TokenType:  "access",
			},
		},
		{
			name:         "empty token",
			token:        "",
			expectError:  true,
			expectClaims: nil,
		},
		{
			name:         "invalid token",
			token:        "invalid.token",
			expectError:  true,
			expectClaims: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := service.GetTokenClaims(tt.token)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)

				if tt.expectClaims != nil {
					assert.Equal(t, tt.expectClaims.CustomerID, claims.CustomerID)
					assert.Equal(t, tt.expectClaims.TokenType, claims.TokenType)
					assert.NotEmpty(t, claims.TokenID)
					assert.False(t, claims.IssuedAt.IsZero())
					assert.False(t, claims.ExpiresAt.IsZero())
				}
			}
		})
	}
}

func TestTokenExpiration(t *testing.T) {
	// Create service with very short TTL for testing expiration
	service, err := NewTokenService(
		1*time.Second,
		2*time.Second,
		"test-issuer",
		"test-audience",
		false,
		"",
		"",
		"test-secret-key-for-jwt-signing-32-chars",
	)
	require.NoError(t, err)

	// Generate tokens
	accessToken, refreshToken, err := service.GenerateTokens(123)
	require.NoError(t, err)

	// Initially, tokens should be valid
	claims, err := service.ValidateToken(accessToken)
	assert.NoError(t, err)
	assert.NotNil(t, claims)
	assert.Equal(t, uint(123), claims.CustomerID)

	// Wait for tokens to expire
	time.Sleep(3 * time.Second)

	// After expiration, tokens should be invalid
	claims, err = service.ValidateToken(accessToken)
	assert.Error(t, err)
	assert.Nil(t, claims)

	// Refresh token should also be expired
	_, _, err = service.RefreshToken(refreshToken)
	assert.Error(t, err)
}

func TestTokenSecurity(t *testing.T) {
	// Create services with different configurations to ensure different keys
	service1, err := NewTokenService(
		15*time.Minute,
		7*24*time.Hour,
		"issuer1",
		"audience1",
		false,
		"",
		"",
		"test-secret-key-1-for-jwt-signing-32-chars",
	)
	require.NoError(t, err)

	service2, err := NewTokenService(
		15*time.Minute,
		7*24*time.Hour,
		"issuer2",
		"audience2",
		false,
		"",
		"",
		"test-secret-key-2-for-jwt-signing-32-chars",
	)
	require.NoError(t, err)

	// Generate tokens with different services
	token1, _, err := service1.GenerateTokens(123)
	require.NoError(t, err)

	token2, _, err := service2.GenerateTokens(123)
	require.NoError(t, err)

	// Tokens should be different even with same customer ID
	assert.NotEqual(t, token1, token2)

	// Tokens from one service should not be valid in another service
	claims, err := service1.ValidateToken(token2)
	assert.Error(t, err)
	assert.Nil(t, claims)

	claims, err = service2.ValidateToken(token1)
	assert.Error(t, err)
	assert.Nil(t, claims)
}

func TestTokenClaimsStructure(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	accessToken, refreshToken, err := service.GenerateTokens(456)
	require.NoError(t, err)

	// Test access token claims
	accessClaims, err := service.ValidateToken(accessToken)
	require.NoError(t, err)
	assert.Equal(t, uint(456), accessClaims.CustomerID)
	assert.Equal(t, "access", accessClaims.TokenType)
	assert.NotEmpty(t, accessClaims.TokenID)
	assert.True(t, accessClaims.ExpiresAt.After(accessClaims.IssuedAt))

	// Test refresh token claims
	refreshClaims, err := service.ValidateToken(refreshToken)
	require.NoError(t, err)
	assert.Equal(t, uint(456), refreshClaims.CustomerID)
	assert.Equal(t, "refresh", refreshClaims.TokenType)
	assert.NotEmpty(t, refreshClaims.TokenID)
	assert.True(t, refreshClaims.ExpiresAt.After(refreshClaims.IssuedAt))

	// Token IDs should be different
	assert.NotEqual(t, accessClaims.TokenID, refreshClaims.TokenID)
}

func TestConcurrentTokenGeneration(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	// Test concurrent token generation
	const numGoroutines = 10
	tokens := make(chan string, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(customerID uint) {
			accessToken, _, err := service.GenerateTokens(customerID)
			if err != nil {
				errors <- err
				return
			}
			tokens <- accessToken
		}(uint(i + 1))
	}

	// Collect results
	generatedTokens := make(map[string]bool)
	for i := 0; i < numGoroutines; i++ {
		select {
		case token := <-tokens:
			assert.NotEmpty(t, token)
			assert.False(t, generatedTokens[token], "Duplicate token generated")
			generatedTokens[token] = true
		case err := <-errors:
			t.Errorf("Error generating token: %v", err)
		}
	}

	assert.Equal(t, numGoroutines, len(generatedTokens))
}

func TestTokenValidationEdgeCases(t *testing.T) {
	service, err := createTestTokenService()
	require.NoError(t, err)

	tests := []struct {
		name        string
		token       string
		expectError bool
	}{
		{
			name:        "nil token",
			token:       "",
			expectError: true,
		},
		{
			name:        "single character",
			token:       "a",
			expectError: true,
		},
		{
			name:        "non-JWT string",
			token:       "this is not a jwt token",
			expectError: true,
		},
		{
			name:        "JWT with wrong number of parts",
			token:       "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJjdXN0b21lcl9pZCI6MTIzfQ",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := service.ValidateToken(tt.token)
			assert.Error(t, err)
			assert.Nil(t, claims)
		})
	}
}

func BenchmarkGenerateTokens(b *testing.B) {
	service, err := createTestTokenService()
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := service.GenerateTokens(uint(i))
		require.NoError(b, err)
	}
}

func BenchmarkValidateToken(b *testing.B) {
	service, err := createTestTokenService()
	require.NoError(b, err)

	token, _, err := service.GenerateTokens(123)
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := service.ValidateToken(token)
		require.NoError(b, err)
	}
}
