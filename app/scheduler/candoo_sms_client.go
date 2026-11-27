package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

const (
	candooSendPath       = "/api/v3.0.1/send"
	candooStatusPath     = "/api/v3.0.1/get-status-customer-id"
	candooMaxBatchSize   = 100
	candooRetryBaseDelay = time.Second
	candooRetryMaxDelay  = time.Minute
)

type candooHTTPStatusError struct {
	operation  string
	statusCode int
	body       string
}

func (e *candooHTTPStatusError) Error() string {
	// The raw body is retained in the durable audit record. Do not include it
	// in an error because scheduler logs are broadly visible and Candoo bodies
	// can contain recipient data.
	return fmt.Sprintf("candoo %s http status: %d", e.operation, e.statusCode)
}

type candooSMSProvider struct {
	cfg     config.CandooSMSConfig
	client  *http.Client
	limiter *smsRateLimiter
}

func NewCandooSMSProvider(cfg config.CandooSMSConfig) SMSProvider {
	return newCandooSMSProviderWithClient(cfg, newHTTPClient(candooTimeout(cfg)))
}

func NewCandooSMSProviderWithHTTPSProxy(cfg config.CandooSMSConfig, proxyURL string) (SMSProvider, error) {
	client, err := newHTTPClientWithHTTPSProxy(candooTimeout(cfg), proxyURL)
	if err != nil {
		return nil, err
	}
	return newCandooSMSProviderWithClient(cfg, client), nil
}

func newCandooSMSProviderWithClient(cfg config.CandooSMSConfig, client *http.Client) *candooSMSProvider {
	if client == nil {
		client = newHTTPClient(candooTimeout(cfg))
	}
	return &candooSMSProvider{
		cfg:     cfg,
		client:  client,
		limiter: newSMSRateLimiter(cfg.MaxRequestsPerSecond),
	}
}

func candooTimeout(cfg config.CandooSMSConfig) time.Duration {
	if cfg.Timeout <= 0 {
		return 30 * time.Second
	}
	return cfg.Timeout
}

func (c *candooSMSProvider) Name() models.SMSProvider { return models.SMSProviderCandoo }
func (c *candooSMSProvider) MaxBatchSize() int        { return candooMaxBatchSize }

type candooSendRequest struct {
	SrcNum         string `json:"srcNum"`
	Recipient      string `json:"recipient"`
	Body           string `json:"body"`
	CustomerID     int64  `json:"customerId"`
	Type           int    `json:"type"`
	RetryCount     int    `json:"retryCount"`
	ValidityPeriod int    `json:"validityPeriod"`
}

type candooSendResponse struct {
	MessageID  int64  `json:"messageId"`
	SrcNum     string `json:"srcNum"`
	Recipient  string `json:"recipient"`
	CustomerID int64  `json:"customerId"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
}

type candooStatusResponse struct {
	MessageID  int64 `json:"messageId"`
	CustomerID int64 `json:"customerId"`
	Status     int   `json:"status"`
}

func (c *candooSMSProvider) SendBatch(ctx context.Context, sender string, items []SMSProviderMessage) (SMSProviderSendResult, error) {
	if err := c.Validate(); err != nil {
		return SMSProviderSendResult{}, err
	}
	if len(items) == 0 {
		return SMSProviderSendResult{}, nil
	}
	if len(items) > candooMaxBatchSize {
		return SMSProviderSendResult{}, fmt.Errorf("candoo send batch exceeds %d messages", candooMaxBatchSize)
	}

	sender, err := normalizeCandooSender(sender)
	if err != nil {
		return SMSProviderSendResult{}, fmt.Errorf("normalize Candoo sender: %w", err)
	}

	requests := make([]candooSendRequest, 0, len(items))
	itemByCustomerID := make(map[int64]SMSProviderMessage, len(items))
	preflight := make([]SMSProviderSendItem, 0)
	for _, item := range items {
		if item.ProviderCustomerID == nil || *item.ProviderCustomerID <= 0 {
			return SMSProviderSendResult{}, fmt.Errorf("Candoo message %q has no positive provider customer id", item.TrackingID)
		}
		if _, exists := itemByCustomerID[*item.ProviderCustomerID]; exists {
			return SMSProviderSendResult{}, fmt.Errorf("duplicate Candoo provider customer id %d", *item.ProviderCustomerID)
		}
		recipient, normalizeErr := normalizeCandooNumber(item.Recipient)
		if normalizeErr != nil {
			code := "INVALID_RECIPIENT"
			description := normalizeErr.Error()
			preflight = append(preflight, SMSProviderSendItem{
				TrackingID:         item.TrackingID,
				ProviderCustomerID: item.ProviderCustomerID,
				InternalStatus:     models.SMSSendStatusUnsuccessful,
				ErrorCode:          &code,
				Description:        &description,
			})
			continue
		}
		itemByCustomerID[*item.ProviderCustomerID] = item
		requests = append(requests, candooSendRequest{
			SrcNum:         sender,
			Recipient:      recipient,
			Body:           item.Body,
			CustomerID:     *item.ProviderCustomerID,
			Type:           c.cfg.MessageType,
			RetryCount:     c.cfg.RetryCount,
			ValidityPeriod: c.cfg.ValidityPeriod,
		})
	}
	if len(requests) == 0 {
		return SMSProviderSendResult{Items: preflight}, nil
	}

	result, err := c.sendBatchWithRetry(ctx, requests, itemByCustomerID)
	if err != nil && isCandooDefinitiveSendError(err) {
		result.Items = candooDefinitiveSendFailures(itemByCustomerID, err)
	}
	result.Items = append(preflight, result.Items...)
	return result, err
}

func isCandooDefinitiveSendError(err error) bool {
	var statusErr *candooHTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.statusCode >= http.StatusBadRequest && statusErr.statusCode < http.StatusInternalServerError
}

func candooDefinitiveSendFailures(items map[int64]SMSProviderMessage, err error) []SMSProviderSendItem {
	var statusErr *candooHTTPStatusError
	_ = errors.As(err, &statusErr)
	code := "SEND_REJECTED"
	description := "Candoo rejected the batch"
	if statusErr != nil {
		code = fmt.Sprintf("HTTP_%d", statusErr.statusCode)
		description = fmt.Sprintf("Candoo rejected the batch with HTTP %d", statusErr.statusCode)
	}
	result := make([]SMSProviderSendItem, 0, len(items))
	for _, item := range items {
		result = append(result, SMSProviderSendItem{
			TrackingID:         item.TrackingID,
			ProviderCustomerID: item.ProviderCustomerID,
			InternalStatus:     models.SMSSendStatusUnsuccessful,
			ErrorCode:          &code,
			Description:        &description,
		})
	}
	return result
}

func (c *candooSMSProvider) sendBatchWithRetry(ctx context.Context, requests []candooSendRequest, itemByCustomerID map[int64]SMSProviderMessage) (SMSProviderSendResult, error) {
	maxAttempts := c.cfg.HTTPMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	var last SMSProviderSendResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return last, err
		}
		result, err := c.sendBatchOnce(ctx, requests, itemByCustomerID)
		result.AttemptCount = attempt + 1
		last = result
		if err == nil || !isCandooRetryableError(err) || attempt+1 >= maxAttempts {
			return result, err
		}
		if sleepErr := sleepWithContext(ctx, retryBackoffDelay(attempt, candooRetryBaseDelay, candooRetryMaxDelay)); sleepErr != nil {
			return result, sleepErr
		}
	}
	return last, nil
}

func (c *candooSMSProvider) sendBatchOnce(ctx context.Context, requests []candooSendRequest, itemByCustomerID map[int64]SMSProviderMessage) (SMSProviderSendResult, error) {
	body, err := json.Marshal(requests)
	if err != nil {
		return SMSProviderSendResult{}, fmt.Errorf("marshal Candoo send body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, candooURL(c.cfg.BaseURL, candooSendPath), bytes.NewReader(body))
	if err != nil {
		return SMSProviderSendResult{}, err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return SMSProviderSendResult{}, err
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(resp.Body)
	raw := string(responseBody)
	statusCode := resp.StatusCode
	result := SMSProviderSendResult{
		RawResponse:     &raw,
		ResponseHeaders: resp.Header.Clone(),
		HTTPStatusCode:  &statusCode,
	}
	if readErr != nil {
		return result, fmt.Errorf("read Candoo send response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &candooHTTPStatusError{operation: "send", statusCode: resp.StatusCode, body: raw}
	}

	var responses []candooSendResponse
	if err := json.Unmarshal(responseBody, &responses); err != nil {
		return result, fmt.Errorf("decode Candoo send response: %w", err)
	}
	responseByCustomerID := make(map[int64]candooSendResponse, len(responses))
	for _, item := range responses {
		if _, duplicate := responseByCustomerID[item.CustomerID]; duplicate {
			return result, fmt.Errorf("Candoo send response repeated customerId %d", item.CustomerID)
		}
		responseByCustomerID[item.CustomerID] = item
	}

	result.Items = make([]SMSProviderSendItem, 0, len(itemByCustomerID))
	for customerID, outbound := range itemByCustomerID {
		response, ok := responseByCustomerID[customerID]
		if !ok {
			code := "MISSING_SEND_RESPONSE"
			description := fmt.Sprintf("Candoo omitted a response for customerId=%d", customerID)
			result.Items = append(result.Items, SMSProviderSendItem{
				TrackingID:         outbound.TrackingID,
				ProviderCustomerID: outbound.ProviderCustomerID,
				InternalStatus:     models.SMSSendStatusPending,
				ErrorCode:          &code,
				Description:        &description,
			})
			continue
		}
		result.Items = append(result.Items, candooSendItem(outbound, response))
	}
	return result, nil
}

func candooSendItem(outbound SMSProviderMessage, response candooSendResponse) SMSProviderSendItem {
	statusText := strings.TrimSpace(response.Status)
	statusCode := strconv.Itoa(response.StatusCode)
	item := SMSProviderSendItem{
		TrackingID:         outbound.TrackingID,
		ProviderCustomerID: outbound.ProviderCustomerID,
		ProviderStatusCode: &statusCode,
		ProviderStatusText: &statusText,
		InternalStatus:     models.SMSSendStatusPending,
	}
	if strings.EqualFold(statusText, "ACCEPTED") && response.MessageID > 0 {
		messageID := strconv.FormatInt(response.MessageID, 10)
		item.ProviderMessageID = &messageID
		item.TrackDeliveryStatus = true
		return item
	}
	if strings.EqualFold(statusText, "REJECTED") {
		item.InternalStatus = models.SMSSendStatusUnsuccessful
		item.ErrorCode = &statusCode
		item.Description = &statusText
		return item
	}

	code := "UNKNOWN_SEND_OUTCOME"
	description := fmt.Sprintf("Candoo status=%q statusCode=%d messageId=%d", statusText, response.StatusCode, response.MessageID)
	item.ErrorCode = &code
	item.Description = &description
	return item
}

func (c *candooSMSProvider) FetchStatus(ctx context.Context, customerIDs []string) (SMSProviderStatusResult, error) {
	if err := c.Validate(); err != nil {
		return SMSProviderStatusResult{}, err
	}
	ids := make([]int64, 0, len(customerIDs))
	for _, id := range customerIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		parsed, err := strconv.ParseInt(id, 10, 64)
		if err != nil || parsed <= 0 {
			return SMSProviderStatusResult{}, fmt.Errorf("invalid Candoo customer id %q", id)
		}
		ids = append(ids, parsed)
	}
	if len(ids) == 0 {
		return SMSProviderStatusResult{}, nil
	}

	maxAttempts := c.cfg.HTTPMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	var last SMSProviderStatusResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return last, err
		}
		result, err := c.fetchStatusOnce(ctx, ids)
		last = result
		if err == nil || !isCandooRetryableError(err) || attempt+1 >= maxAttempts {
			return result, err
		}
		if sleepErr := sleepWithContext(ctx, retryBackoffDelay(attempt, candooRetryBaseDelay, candooRetryMaxDelay)); sleepErr != nil {
			return result, sleepErr
		}
	}
	return last, nil
}

func (c *candooSMSProvider) fetchStatusOnce(ctx context.Context, ids []int64) (SMSProviderStatusResult, error) {
	body, err := json.Marshal(ids)
	if err != nil {
		return SMSProviderStatusResult{}, fmt.Errorf("marshal Candoo status body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, candooURL(c.cfg.BaseURL, candooStatusPath), bytes.NewReader(body))
	if err != nil {
		return SMSProviderStatusResult{}, err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return SMSProviderStatusResult{}, err
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(resp.Body)
	raw := string(responseBody)
	result := SMSProviderStatusResult{RawResponse: &raw}
	if readErr != nil {
		return result, fmt.Errorf("read Candoo status response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &candooHTTPStatusError{operation: "status", statusCode: resp.StatusCode, body: raw}
	}
	var responses []candooStatusResponse
	if err := json.Unmarshal(responseBody, &responses); err != nil {
		return result, fmt.Errorf("decode Candoo status response: %w", err)
	}
	result.Items = make([]SMSProviderStatusItem, 0, len(responses))
	for _, response := range responses {
		result.Items = append(result.Items, c.mapStatus(response))
	}
	return result, nil
}

func (c *candooSMSProvider) mapStatus(response candooStatusResponse) SMSProviderStatusItem {
	customerID := response.CustomerID
	messageID := strconv.FormatInt(response.MessageID, 10)
	code := strconv.Itoa(response.Status)
	metadata, err := json.Marshal(map[string]any{
		"messageId":  response.MessageID,
		"customerId": response.CustomerID,
		"status":     response.Status,
	})
	if err != nil {
		metadata = []byte(`{}`)
	}

	item := SMSProviderStatusItem{
		ProviderCustomerID: &customerID,
		ProviderMessageID:  messageID,
		ProviderStatusCode: &code,
		InternalStatus:     models.SMSSendStatusPending,
		TotalParts:         1,
		UnknownParts:       1,
		Metadata:           metadata,
	}
	statusMappings, err := candooStatusCodeMappings(c.cfg.StatusCodeMap)
	if err != nil {
		// Validate is called before every API request. Keep unexpected in-memory
		// configuration changes safe rather than guessing a terminal outcome.
		return item
	}
	status, ok := statusMappings[response.Status]
	if !ok {
		// Unknown provider values must remain separate from the normalized model
		// and be counted as pending/unknown by the scheduler.
		return item
	}

	item.InternalStatus = status
	item.UnknownParts = 0
	switch status {
	case models.SMSSendStatusSuccessful:
		item.DeliveredParts = 1
	case models.SMSSendStatusUnsuccessful:
		item.UndeliveredParts = 1
	default:
		item.UnknownParts = 1
	}
	return item
}

// Validate confirms Candoo can be used before the scheduler claims a campaign
// for execution.
func (c *candooSMSProvider) Validate() error {
	if c == nil {
		return errors.New("Candoo provider is unavailable")
	}
	if !c.cfg.Enabled {
		return errors.New("Candoo provider is disabled (set CANDOO_SMS_ENABLED=true)")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return errors.New("Candoo API key is not configured")
	}
	if c.cfg.MessageType < 0 || c.cfg.MessageType > 4 {
		return fmt.Errorf("Candoo message type %d is outside 0-4", c.cfg.MessageType)
	}
	if c.cfg.RetryCount < 0 || c.cfg.RetryCount > 10 {
		return fmt.Errorf("Candoo retry count %d is outside 0-10", c.cfg.RetryCount)
	}
	if c.cfg.ValidityPeriod < 0 || c.cfg.ValidityPeriod > 172800 {
		return fmt.Errorf("Candoo validity period %d is outside 0-172800", c.cfg.ValidityPeriod)
	}
	if _, err := candooStatusCodeMappings(c.cfg.StatusCodeMap); err != nil {
		return err
	}
	return nil
}

func candooStatusCodeMappings(raw map[string]string) (map[int]models.SMSSendStatus, error) {
	if len(raw) == 0 {
		return nil, errors.New("Candoo status-code mapping is not configured (set CANDOO_SMS_STATUS_MAP)")
	}
	mappings := make(map[int]models.SMSSendStatus, len(raw))
	hasTerminalStatus := false
	for codeText, statusText := range raw {
		code, err := strconv.Atoi(strings.TrimSpace(codeText))
		if err != nil {
			return nil, fmt.Errorf("Candoo status-code mapping has non-integer code %q", codeText)
		}
		status := models.SMSSendStatus(strings.ToLower(strings.TrimSpace(statusText)))
		switch status {
		case models.SMSSendStatusPending:
		case models.SMSSendStatusSuccessful, models.SMSSendStatusUnsuccessful:
			hasTerminalStatus = true
		default:
			return nil, fmt.Errorf("Candoo status-code mapping maps code %d to invalid internal status %q", code, statusText)
		}
		mappings[code] = status
	}
	if !hasTerminalStatus {
		return nil, errors.New("Candoo status-code mapping must include a successful or unsuccessful terminal status")
	}
	return mappings, nil
}

func normalizeCandooNumber(value string) (string, error) {
	v := strings.TrimSpace(value)
	v = strings.ReplaceAll(v, " ", "")
	v = strings.ReplaceAll(v, "-", "")
	switch {
	case strings.HasPrefix(v, "+98"):
		v = strings.TrimPrefix(v, "+")
	case strings.HasPrefix(v, "0098"):
		v = "98" + strings.TrimPrefix(v, "0098")
	case strings.HasPrefix(v, "0"):
		v = "98" + strings.TrimPrefix(v, "0")
	}
	if !strings.HasPrefix(v, "98") || len(v) < 11 {
		return "", fmt.Errorf("number must be in Iranian 98 format")
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("number contains non-digit characters")
		}
	}
	return v, nil
}

// normalizeCandooSender deliberately does not apply the recipient phone-number
// rules. Candoo accepts provisioned sender lines such as short codes, which do
// not necessarily use Iran's 98 mobile-number format.
func normalizeCandooSender(value string) (string, error) {
	sender := strings.TrimSpace(value)
	if sender == "" {
		return "", fmt.Errorf("sender is required")
	}
	return sender, nil
}

func candooURL(baseURL, path string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + path
}

func isCandooRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *candooHTTPStatusError
	if errors.As(err, &statusErr) {
		// A 429 is an explicit no-accept response. Retrying a 5xx can duplicate
		// messages because the vendor does not document idempotency semantics.
		return statusErr.statusCode == http.StatusTooManyRequests
	}
	return false
}
