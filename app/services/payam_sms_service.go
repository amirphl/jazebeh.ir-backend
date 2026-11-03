// Package services provides external service integrations and technical concerns like notifications and tokens
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

type PayamSMSSMSService struct {
	smsConfig   *config.SMSConfig
	payamConfig *config.PayamSMSConfig
	client      *http.Client
	mu          sync.RWMutex
	token       string
	tokenExpiry time.Time
}

const (
	payamSMSTokenFallbackTTL = time.Hour
	payamSMSTokenRefreshSkew = 30 * time.Second
)

type payamSMSTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type payamSMSHTTPStatusError struct {
	operation  string
	statusCode int
	body       string
}

func (e *payamSMSHTTPStatusError) Error() string {
	return fmt.Sprintf("PayamSMS %s http status %d: %s", e.operation, e.statusCode, strings.TrimSpace(e.body))
}

func isPayamSMSUnauthorizedError(err error) bool {
	statusErr, ok := err.(*payamSMSHTTPStatusError)
	return ok && statusErr.statusCode == http.StatusUnauthorized
}

type payamSMSBulkPayload struct {
	Sender   string                 `json:"sender"`
	SMSItems []payamSMSBulkItemBody `json:"smsItems"`
}

type payamSMSBulkItemBody struct {
	Recipient  string `json:"recipient"`
	Body       string `json:"body"`
	CustomerID string `json:"customerId"`
}

type payamSMSBulkResponseItem struct {
	TrackingID string  `json:"customerId"`
	Mobile     string  `json:"mobile"`
	ServerID   *string `json:"serverId"`
	ErrorCode  *string `json:"errorCode"`
	Desc       *string `json:"description"`
}

func NewPayamSMSService(smsCfg *config.SMSConfig, payamCfg *config.PayamSMSConfig) SMSService {
	return &PayamSMSSMSService{
		smsConfig:   smsCfg,
		payamConfig: payamCfg,
		client: &http.Client{
			Timeout: smsCfg.Timeout,
		},
	}
}

func NewPayamSMSServiceWithHTTPSProxy(smsCfg *config.SMSConfig, payamCfg *config.PayamSMSConfig, proxyURL string) (SMSService, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return NewPayamSMSService(smsCfg, payamCfg), nil
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)

	return &PayamSMSSMSService{
		smsConfig:   smsCfg,
		payamConfig: payamCfg,
		client: &http.Client{
			Timeout:   smsCfg.Timeout,
			Transport: transport,
		},
	}, nil
}

func (s *PayamSMSSMSService) SendOTP(ctx context.Context, recipient, message string, customerID *int64) error {
	return s.SendSMS(ctx, recipient, message, customerID)
}

func (s *PayamSMSSMSService) SendSMS(ctx context.Context, recipient, message string, customerID *int64) error {
	return s.SendBulk(ctx, []string{recipient}, message, customerID)
}

func (s *PayamSMSSMSService) SendBulk(ctx context.Context, recipients []string, message string, customerID *int64) error {
	if len(recipients) == 0 {
		return nil
	}

	payload := payamSMSBulkPayload{
		Sender:   s.smsConfig.SourceNumber,
		SMSItems: make([]payamSMSBulkItemBody, 0, len(recipients)),
	}
	for idx, recipient := range recipients {
		payload.SMSItems = append(payload.SMSItems, payamSMSBulkItemBody{
			Recipient:  recipient,
			Body:       message,
			CustomerID: buildPayamSMSCustomerID(nil, idx),
		})
	}

	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal PayamSMS request: %w", err)
	}

	token, err := s.getToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get PayamSMS access token: %w", err)
	}

	err = s.sendBulkWithToken(ctx, token, requestBody)
	if !isPayamSMSUnauthorizedError(err) {
		return err
	}

	// PayamSMS can revoke a bearer token before its advertised expiry.  Clear
	// only the rejected value, obtain a replacement with the root token, and
	// retry the submission once. A 401 is an authentication failure, so this
	// retry cannot duplicate an accepted submission.
	token, refreshErr := s.refreshTokenAfterUnauthorized(ctx, token)
	if refreshErr != nil {
		return fmt.Errorf("failed to refresh PayamSMS access token after 401: %w", refreshErr)
	}
	return s.sendBulkWithToken(ctx, token, requestBody)
}

func (s *PayamSMSSMSService) sendBulkWithToken(ctx context.Context, token string, requestBody []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.payamsms.com/panel/webservice/sendMultipleWithSrc", bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create PayamSMS request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PayamSMS request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &payamSMSHTTPStatusError{
			operation:  "send",
			statusCode: resp.StatusCode,
			body:       string(body),
		}
	}

	var results []payamSMSBulkResponseItem
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("failed to decode PayamSMS response: %w", err)
	}

	for _, result := range results {
		if result.ErrorCode != nil && strings.TrimSpace(*result.ErrorCode) != "" {
			description := ""
			if result.Desc != nil {
				description = strings.TrimSpace(*result.Desc)
			}
			return fmt.Errorf("PayamSMS delivery failed for %s: %s (%s)", result.Mobile, description, strings.TrimSpace(*result.ErrorCode))
		}
	}

	return nil
}

func (s *PayamSMSSMSService) getToken(ctx context.Context) (string, error) {
	s.mu.RLock()
	if s.hasUsableTokenLocked(utils.UTCNow()) {
		token := s.token
		s.mu.RUnlock()
		return token, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasUsableTokenLocked(utils.UTCNow()) {
		return s.token, nil
	}
	return s.requestTokenLocked(ctx)
}

// refreshTokenAfterUnauthorized ensures concurrent requests that receive a
// 401 share one replacement token instead of all hitting the token endpoint.
func (s *PayamSMSSMSService) refreshTokenAfterUnauthorized(ctx context.Context, rejectedToken string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasUsableTokenLocked(utils.UTCNow()) && s.token != rejectedToken {
		return s.token, nil
	}
	s.token = ""
	s.tokenExpiry = time.Time{}
	return s.requestTokenLocked(ctx)
}

func (s *PayamSMSSMSService) hasUsableTokenLocked(now time.Time) bool {
	return s.token != "" && now.Before(s.tokenExpiry)
}

func (s *PayamSMSSMSService) requestTokenLocked(ctx context.Context) (string, error) {

	tokenURL := strings.TrimSpace(s.payamConfig.TokenURL)
	if tokenURL == "" {
		tokenURL = "https://www.payamsms.com/auth/oauth/token"
	}

	query := url.Values{}
	query.Set("systemName", s.payamConfig.SystemName)
	query.Set("username", s.payamConfig.Username)
	query.Set("password", s.payamConfig.Password)
	query.Set("scope", defaultString(s.payamConfig.Scope, "webservice"))
	query.Set("grant_type", defaultString(s.payamConfig.GrantType, "password"))

	requestURL := tokenURL
	if strings.Contains(tokenURL, "?") {
		requestURL += "&" + query.Encode()
	} else {
		requestURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create PayamSMS token request: %w", err)
	}
	if strings.TrimSpace(s.payamConfig.RootAccessToken) != "" {
		req.Header.Set("Authorization", "Basic "+s.payamConfig.RootAccessToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request PayamSMS token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PayamSMS token response body: %w", err)
	}
	trimmedBody := strings.TrimSpace(string(body))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("PayamSMS token http status %d: %s", resp.StatusCode, trimmedBody)
	}

	var tokenResp payamSMSTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode PayamSMS token response: %w, body: %s", err, trimmedBody)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", fmt.Errorf("PayamSMS token response did not contain access_token, body: %s", trimmedBody)
	}

	ttl := payamSMSTokenFallbackTTL
	if tokenResp.ExpiresIn > 0 {
		ttl = time.Duration(tokenResp.ExpiresIn) * time.Second
	}
	refreshSkew := payamSMSTokenRefreshSkew
	if tenth := ttl / 10; tenth < refreshSkew {
		refreshSkew = tenth
	}
	if refreshSkew <= 0 {
		refreshSkew = time.Nanosecond
	}
	s.token = tokenResp.AccessToken
	// Ensure expiry is always safely in the future. If skew calculation results in
	// a zero or negative expiry (e.g., from malformed API response), use full TTL.
	expiry := utils.UTCNow().Add(ttl - refreshSkew)
	if expiry.Before(utils.UTCNow().Add(100 * time.Millisecond)) {
		// Token lifetime too short for safe refresh; use full TTL instead
		expiry = utils.UTCNow().Add(ttl)
	}
	s.tokenExpiry = expiry

	return s.token, nil
}

func buildPayamSMSCustomerID(customerID *int64, idx int) string {
	if customerID != nil {
		if idx == 0 {
			return strconv.FormatInt(*customerID, 10)
		}
		return fmt.Sprintf("%d-%d", *customerID, idx)
	}
	return fmt.Sprintf("otp-%d-%d", utils.UTCNowUnixNano(), idx)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
