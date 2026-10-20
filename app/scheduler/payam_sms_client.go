package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

const (
	payamRetryBaseDelay   = 1 * time.Second
	payamRetryMaxDelay    = 2 * time.Minute
	payamRetryMaxAttempts = 5 // 0 means unlimited retries until success or context cancellation.

	// A status request that is rejected with 401 gets at most three total
	// attempts: the original request and two requests with refreshed tokens.
	payamStatusUnauthorizedMaxAttempts = 3
)

func payamRetryBackoffDelay(attempt int) time.Duration {
	return retryBackoffDelay(attempt, payamRetryBaseDelay, payamRetryMaxDelay)
}

func isPayamRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "http status: 500") ||
		strings.Contains(msg, "http status 500") ||
		strings.Contains(msg, "http status: 502") ||
		strings.Contains(msg, "http status 502") ||
		strings.Contains(msg, "http status: 503") ||
		strings.Contains(msg, "http status 503") ||
		strings.Contains(msg, "http status: 504") ||
		strings.Contains(msg, "http status 504") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "eof")
}

type PayamSMSItem struct {
	Recipient  string
	Body       string
	TrackingID string
}

type PayamSMSResponseItem struct {
	TrackingID string  `json:"customerId"`
	Mobile     string  `json:"mobile"`
	ServerID   *string `json:"serverId"`
	ErrorCode  *string `json:"errorCode"`
	Desc       *string `json:"description"`
}

// PayamSMSSendResult retains the provider's immediate HTTP response alongside
// the decoded per-recipient acknowledgements. RawResponse is non-nil whenever
// an HTTP response body was received, including empty, non-2xx, and malformed
// bodies. HTTPStatusCode and ResponseHeaders remain nil when the request failed
// before the provider returned a response.
type PayamSMSSendResult struct {
	Items           []PayamSMSResponseItem
	RawResponse     *string
	ResponseHeaders http.Header
	HTTPStatusCode  *int
	AttemptCount    int
}

type PayamStatusResponse struct {
	TrackingID            string  `json:"customerId"`
	ServerID              *string `json:"serverId"`
	TotalParts            int64   `json:"totalParts"`
	TotalDeliveredParts   int64   `json:"totalDeliveredParts"`
	TotalUndeliveredParts int64   `json:"totalUnDeliveredParts"`
	TotalUnknownParts     int64   `json:"totalUnKnownParts"`
	Status                string  `json:"status"`
}

// PayamStatusFetchResult keeps the provider payload alongside its parsed items.
// RawResponse is set whenever an HTTP response body was received, even if the
// response is empty, partial, non-2xx, or cannot be decoded.
type PayamStatusFetchResult struct {
	Items       []PayamStatusResponse
	RawResponse *string
}

type payamHTTPStatusError struct {
	operation  string
	statusCode int
	body       string
}

func (e *payamHTTPStatusError) Error() string {
	return fmt.Sprintf("payamsms %s http status: %d, body: %s", e.operation, e.statusCode, strings.TrimSpace(e.body))
}

func isPayamUnauthorizedError(err error) bool {
	var statusErr *payamHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.statusCode == http.StatusUnauthorized
}

type PayamSMSClient interface {
	SendBatch(ctx context.Context, sender string, items []PayamSMSItem) (PayamSMSSendResult, error)
	GetToken(ctx context.Context) (string, error)
	FetchStatus(ctx context.Context, token string, ids []string) (PayamStatusFetchResult, error)
}

// PayamBalanceClient is the minimal provider API needed by the balance monitor.
// Keeping it separate from campaign delivery avoids coupling that worker to SMS
// submission and delivery-status behavior.
type PayamBalanceClient interface {
	FetchBalance(ctx context.Context) (int64, error)
}

type httpPayamSMSClient struct {
	cfg    config.PayamSMSConfig
	client *http.Client

	refreshMu   sync.Mutex
	tokenMu     sync.RWMutex
	accessToken string

	statusUnauthorizedRetryDelay func(attempt int) time.Duration
	balanceRetryDelay            func(attempt int) time.Duration
}

func newHTTPPayamSMSClient(cfg config.PayamSMSConfig) *httpPayamSMSClient {
	return newHTTPPayamSMSClientWithClient(cfg, newHTTPClient(60*time.Second))
}

func newHTTPPayamSMSClientWithClient(cfg config.PayamSMSConfig, client *http.Client) *httpPayamSMSClient {
	if client == nil {
		client = newHTTPClient(60 * time.Second)
	}
	return &httpPayamSMSClient{
		cfg:                          cfg,
		client:                       client,
		statusUnauthorizedRetryDelay: payamRetryBackoffDelay,
		balanceRetryDelay:            payamRetryBackoffDelay,
	}
}

// SendBatch sends a batch of SMS messages with exponential backoff retries.
func (c *httpPayamSMSClient) SendBatch(ctx context.Context, sender string, items []PayamSMSItem) (PayamSMSSendResult, error) {
	if len(items) == 0 {
		return PayamSMSSendResult{}, nil
	}
	// GetToken already retries internally; no need to re-fetch on each send retry.
	token, err := c.GetToken(ctx)
	if err != nil {
		return PayamSMSSendResult{}, err
	}

	var out PayamSMSSendResult
	for attempt := 0; ; attempt++ {
		out, err = c.sendBatchOnce(ctx, sender, items, token)
		out.AttemptCount = attempt + 1
		if !isPayamRetryableError(err) {
			return out, err
		}
		if payamRetryMaxAttempts > 0 && attempt+1 >= payamRetryMaxAttempts {
			break
		}
		if sleepErr := sleepWithContext(ctx, payamRetryBackoffDelay(attempt)); sleepErr != nil {
			return out, ctx.Err()
		}
	}
	return out, err
}

func (c *httpPayamSMSClient) sendBatchOnce(ctx context.Context, sender string, items []PayamSMSItem, token string) (PayamSMSSendResult, error) {
	payload := struct {
		Sender   string `json:"sender"`
		SMSItems []any  `json:"smsItems"`
	}{
		Sender:   sender,
		SMSItems: make([]any, 0, len(items)),
	}
	// sendDate, err := utils.TehranNow()
	// if err != nil {
	// 	return nil, err
	// }
	// sendDate = sendDate.Add(time.Minute)

	for _, it := range items {
		payload.SMSItems = append(payload.SMSItems, map[string]any{
			"recipient":  it.Recipient,
			"body":       it.Body,
			"customerId": it.TrackingID,
			// "sendDate":   sendDate.Format("2006-01-02 15:04:05"),
		})
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return PayamSMSSendResult{}, fmt.Errorf("payamsms sendBatch marshal payload: %w", err)
	}

	// BUG FIX 2: local variable renamed from `url` to `sendURL` to stop shadowing
	// the imported "net/url" package within this function.
	sendURL := "https://www.payamsms.com/panel/webservice/sendMultipleWithSrc"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(b))
	if err != nil {
		return PayamSMSSendResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.client.Do(req)
	if err != nil {
		return PayamSMSSendResult{}, err
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	bodyBytes, readErr := io.ReadAll(resp.Body)
	rawResponse := string(bodyBytes)
	result := PayamSMSSendResult{
		RawResponse:     &rawResponse,
		ResponseHeaders: resp.Header.Clone(),
		HTTPStatusCode:  &statusCode,
	}
	if readErr != nil {
		return result, fmt.Errorf("read payamsms sendMultiple response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &payamHTTPStatusError{
			operation:  "sendMultiple",
			statusCode: resp.StatusCode,
			body:       rawResponse,
		}
	}
	if err := json.Unmarshal(bodyBytes, &result.Items); err != nil {
		return result, err
	}
	return result, nil
}

// GetToken fetches a fresh OAuth2 bearer token from PayamSMS with exponential backoff retries.
func (c *httpPayamSMSClient) GetToken(ctx context.Context) (string, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	token, err := c.getTokenWithRetry(ctx)
	if err != nil {
		return "", err
	}
	c.storeToken(token)
	return token, nil
}

func (c *httpPayamSMSClient) getTokenWithRetry(ctx context.Context) (string, error) {
	var (
		token string
		err   error
	)
	for attempt := 0; ; attempt++ {
		token, err = c.getTokenOnce(ctx)
		if !isPayamRetryableError(err) {
			return token, err
		}
		if payamRetryMaxAttempts > 0 && attempt+1 >= payamRetryMaxAttempts {
			break
		}
		if sleepErr := sleepWithContext(ctx, payamRetryBackoffDelay(attempt)); sleepErr != nil {
			return "", ctx.Err()
		}
	}
	return token, err
}

func (c *httpPayamSMSClient) storeToken(token string) {
	c.tokenMu.Lock()
	c.accessToken = token
	c.tokenMu.Unlock()
}

func (c *httpPayamSMSClient) currentToken(fallback string) string {
	c.tokenMu.RLock()
	token := c.accessToken
	c.tokenMu.RUnlock()
	if token != "" {
		return token
	}
	return fallback
}

// refreshTokenAfterUnauthorized avoids duplicate refreshes when concurrent
// requests discover that the same token has expired. A token refreshed by one
// request is reused by the others and by subsequent status jobs.
func (c *httpPayamSMSClient) refreshTokenAfterUnauthorized(ctx context.Context, rejectedToken string) (string, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	currentToken := c.currentToken("")
	if currentToken != "" && currentToken != rejectedToken {
		return currentToken, nil
	}

	token, err := c.getTokenWithRetry(ctx)
	if err != nil {
		return "", err
	}
	c.storeToken(token)
	return token, nil
}

func (c *httpPayamSMSClient) getTokenOnce(ctx context.Context) (string, error) {
	tokenURL := c.cfg.TokenURL
	if tokenURL == "" {
		tokenURL = "https://www.payamsms.com/auth/oauth/token"
	}
	systemName := c.cfg.SystemName
	username := c.cfg.Username
	password := c.cfg.Password
	scope := c.cfg.Scope
	grantType := c.cfg.GrantType
	rootToken := c.cfg.RootAccessToken
	if scope == "" {
		scope = "webservice"
	}
	if grantType == "" {
		grantType = "password"
	}

	q := url.Values{}
	q.Set("systemName", systemName)
	q.Set("username", username)
	q.Set("password", password)
	q.Set("scope", scope)
	q.Set("grant_type", grantType)
	tokenReqURL := tokenURL + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenReqURL, nil)
	if err != nil {
		return "", err
	}
	if rootToken != "" {
		req.Header.Set("Authorization", "Basic "+rootToken)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("payamsms token http status: %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("empty access_token")
	}
	return out.AccessToken, nil
}

// FetchStatus retrieves delivery statuses for the given tracking IDs with
// exponential backoff retries. When the provider rejects an expired access
// token, it obtains a new one with the configured root token, retains it for
// subsequent jobs, and retries the status request at most twice (three total
// status attempts).
func (c *httpPayamSMSClient) FetchStatus(ctx context.Context, token string, trackingIDs []string) (PayamStatusFetchResult, error) {
	token = c.currentToken(token)
	var out PayamStatusFetchResult
	for attempt := 0; attempt < payamStatusUnauthorizedMaxAttempts; attempt++ {
		var err error
		out, err = c.fetchStatusWithRetry(ctx, token, trackingIDs)
		if !isPayamUnauthorizedError(err) {
			return out, err
		}
		if attempt+1 >= payamStatusUnauthorizedMaxAttempts {
			return out, err
		}

		delay := payamRetryBackoffDelay(attempt)
		if c.statusUnauthorizedRetryDelay != nil {
			delay = c.statusUnauthorizedRetryDelay(attempt)
		}
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return out, ctx.Err()
		}

		refreshedToken, refreshErr := c.refreshTokenAfterUnauthorized(ctx, token)
		if refreshErr != nil {
			return out, fmt.Errorf("payamsms status token refresh after 401: %w", refreshErr)
		}
		token = refreshedToken
	}
	return out, nil
}

// FetchBalance returns the PayamSMS account balance in Rials. It obtains an
// OAuth bearer token through GetToken (and therefore the configured root token),
// retries transient failures, and refreshes a rejected bearer token.
func (c *httpPayamSMSClient) FetchBalance(ctx context.Context) (int64, error) {
	token, err := c.GetToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("payamsms balance token: %w", err)
	}

	for attempt := 0; attempt < payamStatusUnauthorizedMaxAttempts; attempt++ {
		balance, err := c.fetchBalanceWithRetry(ctx, token)
		if !isPayamUnauthorizedError(err) {
			return balance, err
		}
		if attempt+1 >= payamStatusUnauthorizedMaxAttempts {
			return 0, err
		}
		delay := payamRetryBackoffDelay(attempt)
		if c.balanceRetryDelay != nil {
			delay = c.balanceRetryDelay(attempt)
		}
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return 0, ctx.Err()
		}
		token, err = c.refreshTokenAfterUnauthorized(ctx, token)
		if err != nil {
			return 0, fmt.Errorf("payamsms balance token refresh after 401: %w", err)
		}
	}
	return 0, fmt.Errorf("payamsms balance authorization loop ended unexpectedly")
}

func (c *httpPayamSMSClient) fetchBalanceWithRetry(ctx context.Context, token string) (int64, error) {
	var (
		balance int64
		err     error
	)
	for attempt := 0; ; attempt++ {
		balance, err = c.fetchBalanceOnce(ctx, token)
		if !isPayamRetryableError(err) {
			return balance, err
		}
		if payamRetryMaxAttempts > 0 && attempt+1 >= payamRetryMaxAttempts {
			break
		}
		delay := payamRetryBackoffDelay(attempt)
		if c.balanceRetryDelay != nil {
			delay = c.balanceRetryDelay(attempt)
		}
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return 0, ctx.Err()
		}
	}
	return balance, err
}

func (c *httpPayamSMSClient) fetchBalanceOnce(ctx context.Context, token string) (int64, error) {
	balanceURL := strings.TrimSpace(c.cfg.BalanceURL)
	if balanceURL == "" {
		balanceURL = "https://www.payamsms.com/accounting/webservice/balance"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, balanceURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create payamsms balance request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, fmt.Errorf("read payamsms balance response: %w", readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, &payamHTTPStatusError{operation: "balance", statusCode: resp.StatusCode, body: string(body)}
	}

	balance, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil || balance < 0 {
		return 0, fmt.Errorf("invalid payamsms balance response %q", strings.TrimSpace(string(body)))
	}
	return balance, nil
}

func (c *httpPayamSMSClient) fetchStatusWithRetry(ctx context.Context, token string, trackingIDs []string) (PayamStatusFetchResult, error) {
	var (
		out PayamStatusFetchResult
		err error
	)
	for attempt := 0; ; attempt++ {
		out, err = c.fetchStatusOnce(ctx, token, trackingIDs)
		if !isPayamRetryableError(err) {
			return out, err
		}
		if payamRetryMaxAttempts > 0 && attempt+1 >= payamRetryMaxAttempts {
			break
		}
		if sleepErr := sleepWithContext(ctx, payamRetryBackoffDelay(attempt)); sleepErr != nil {
			return out, ctx.Err()
		}
	}
	return out, err
}

func (c *httpPayamSMSClient) fetchStatusOnce(ctx context.Context, token string, trackingIDs []string) (PayamStatusFetchResult, error) {
	if len(trackingIDs) == 0 {
		return PayamStatusFetchResult{}, fmt.Errorf("no tracking ids provided")
	}
	baseURL := "https://www.payamsms.com/report/webservice/status"
	u, err := url.Parse(baseURL)
	if err != nil {
		return PayamStatusFetchResult{}, err
	}
	q := u.Query()
	q.Set("byCustomer", "true")
	for _, trackingID := range trackingIDs {
		if strings.TrimSpace(trackingID) != "" {
			q.Add("ids", strings.TrimSpace(trackingID))
		}
	}
	u.RawQuery = q.Encode()

	// Log a curl command for manual retries when the API fails.
	// log.Printf("payamsms status curl: curl -X GET %q -H %q", u.String(), "Authorization: Bearer "+token)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return PayamStatusFetchResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return PayamStatusFetchResult{}, err
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	rawResponse := string(bodyBytes)
	result := PayamStatusFetchResult{RawResponse: &rawResponse}
	if readErr != nil {
		return result, fmt.Errorf("read payamsms status response: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &payamHTTPStatusError{
			operation:  "status",
			statusCode: resp.StatusCode,
			body:       rawResponse,
		}
	}

	if err := json.Unmarshal(bodyBytes, &result.Items); err != nil {
		return result, err
	}
	return result, nil
}
