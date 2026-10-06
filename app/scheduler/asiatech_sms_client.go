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
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/redis/go-redis/v9"
)

const (
	asiaTechP2PBulkPath  = "/api/4/message/P2PBulk"
	asiaTechDLRPath      = "/api/message/getdlr?returnUDH=true&returnSentDate=true"
	asiaTechUserInfoPath = "/api/user/userinfo"
	asiaTechPingPath     = "/api/Tools/Ping"
	asiaTechTokenSkew    = 30 * time.Second
)

type asiaTechHTTPError struct {
	operation  string
	status     int
	resultCode *int
	message    string
}

func (e *asiaTechHTTPError) Error() string {
	return fmt.Sprintf("asiatech %s failed (http=%d)", e.operation, e.status)
}
func (e *asiaTechHTTPError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status == http.StatusUnauthorized
}

type asiaTechEnvelope[T any] struct {
	Message    string `json:"message"`
	Succeeded  bool   `json:"succeeded"`
	Data       T      `json:"data"`
	ResultCode int    `json:"resultCode"`
}
type asiaTechTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	ExpiresAt   string `json:"expires_at"`
}
type asiaTechP2PRequest struct {
	SourceAddress      string `json:"SourceAddress"`
	DestinationAddress string `json:"DestinationAddress"`
	MessageText        string `json:"MessageText"`
	UDH                string `json:"udh,omitempty"`
}
type asiaTechP2PResponse struct {
	ID              string          `json:"id"`
	Part            json.RawMessage `json:"part"`
	UpstreamGateway string          `json:"upstreamGateway"`
	ErrorCode       *int            `json:"errorCode,omitempty"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
}
type asiaTechDLRPart struct {
	Number int    `json:"item1"`
	Status int    `json:"item2"`
	At     string `json:"item3"`
}
type asiaTechDLRResponse struct {
	ID             string            `json:"id"`
	UDH            string            `json:"udh"`
	PartStatus     []asiaTechDLRPart `json:"partStatus"`
	DeliveryStatus int               `json:"deliveryStatus"`
}
type AsiaTechUserInfo struct {
	Credit    float64  `json:"credit"`
	MPS       int      `json:"mps"`
	SenderIDs []string `json:"senderIds"`
}

// AsiaTechProviderOperations is intentionally small so the monitor and the
// durable DLR worker can share one authenticated, rate-limited client.
type AsiaTechProviderOperations interface {
	SMSProvider
	UserInfo(context.Context) (AsiaTechUserInfo, error)
	Ping(context.Context) error
}

type asiaTechSMSProvider struct {
	cfg         config.AsiaTechSMSConfig
	client      *http.Client
	refreshMu   sync.Mutex
	tokenMu     sync.RWMutex
	token       string
	expiresAt   time.Time
	sendLimiter *smsRateLimiter
	dlrLimiter  *smsRateLimiter
	mpsMu       sync.Mutex
	mps         int
	nextPart    time.Time
	cache       *redis.Client
}

func NewAsiaTechSMSProvider(cfg config.AsiaTechSMSConfig) SMSProvider {
	return newAsiaTechSMSProviderWithClient(cfg, newHTTPClient(asiaTechTimeout(cfg)))
}
func NewAsiaTechSMSProviderWithRedis(cfg config.AsiaTechSMSConfig, cache *redis.Client) SMSProvider {
	p := newAsiaTechSMSProviderWithClient(cfg, newHTTPClient(asiaTechTimeout(cfg)))
	p.cache = cache
	return p
}
func NewAsiaTechSMSProviderWithHTTPSProxy(cfg config.AsiaTechSMSConfig, proxyURL string) (SMSProvider, error) {
	c, err := newHTTPClientWithHTTPSProxy(asiaTechTimeout(cfg), proxyURL)
	if err != nil {
		return nil, err
	}
	return newAsiaTechSMSProviderWithClient(cfg, c), nil
}
func newAsiaTechSMSProviderWithClient(cfg config.AsiaTechSMSConfig, client *http.Client) *asiaTechSMSProvider {
	if client == nil {
		client = newHTTPClient(asiaTechTimeout(cfg))
	}
	mps := cfg.FallbackMPS
	if mps < 1 {
		mps = 1
	}
	return &asiaTechSMSProvider{cfg: cfg, client: client, sendLimiter: newSMSRateLimiter(cfg.SendRequestsPerSecond), dlrLimiter: newSMSRateLimiter(cfg.DLRRequestsPerSecond), mps: mps}
}
func asiaTechTimeout(cfg config.AsiaTechSMSConfig) time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return 30 * time.Second
}
func (p *asiaTechSMSProvider) Name() models.SMSProvider { return models.SMSProviderAsiaTech }
func (p *asiaTechSMSProvider) MaxBatchSize() int {
	if p.cfg.MaxBatchSize > 0 {
		return p.cfg.MaxBatchSize
	}
	return 100
}
func (p *asiaTechSMSProvider) Validate() error {
	if p == nil {
		return errors.New("AsiaTech provider is unavailable")
	}
	if !p.cfg.Enabled {
		return errors.New("AsiaTech provider is disabled (set ASIATECH_SMS_ENABLED=true)")
	}
	if strings.TrimSpace(p.cfg.Username) == "" || strings.TrimSpace(p.cfg.Password) == "" {
		return errors.New("AsiaTech credentials are not configured")
	}
	if strings.TrimSpace(p.cfg.Scope) != "BulkApiAccess" {
		return errors.New("AsiaTech scope must be BulkApiAccess")
	}
	return nil
}

func (p *asiaTechSMSProvider) tokenFor(ctx context.Context, force bool) (string, error) {
	if p.cache != nil && !force {
		if token, err := p.cache.Get(ctx, asiaTechTokenCacheKey()).Result(); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}
	p.tokenMu.RLock()
	token, expiry := p.token, p.expiresAt
	p.tokenMu.RUnlock()
	if !force && token != "" && time.Until(expiry) > asiaTechTokenSkew {
		return token, nil
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if p.cache != nil {
		locked, cacheErr := p.cache.SetNX(ctx, asiaTechTokenLockKey(), "1", 15*time.Second).Result()
		if cacheErr == nil && !locked {
			for range 50 { // another scheduler is refreshing the account-wide token
				if err := sleepWithContext(ctx, 100*time.Millisecond); err != nil {
					return "", err
				}
				if shared, err := p.cache.Get(ctx, asiaTechTokenCacheKey()).Result(); err == nil && strings.TrimSpace(shared) != "" {
					return shared, nil
				}
			}
			return "", errors.New("timed out waiting for shared AsiaTech token refresh")
		}
		if cacheErr == nil && locked {
			defer p.cache.Del(context.Background(), asiaTechTokenLockKey())
		}
		if force {
			_ = p.cache.Del(ctx, asiaTechTokenCacheKey()).Err()
		}
	}
	if p.cache != nil && !force {
		if token, err := p.cache.Get(ctx, asiaTechTokenCacheKey()).Result(); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}
	p.tokenMu.RLock()
	token, expiry = p.token, p.expiresAt
	p.tokenMu.RUnlock()
	if !force && token != "" && time.Until(expiry) > asiaTechTokenSkew {
		return token, nil
	}
	form := url.Values{"username": {p.cfg.Username}, "password": {p.cfg.Password}, "scope": {p.cfg.Scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &asiaTechHTTPError{operation: "token", status: resp.StatusCode}
	}
	var out asiaTechTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode AsiaTech token: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return "", errors.New("AsiaTech token response contains no access_token")
	}
	expires := time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)
	if out.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, out.ExpiresAt); err == nil {
			expires = parsed
		}
	}
	p.tokenMu.Lock()
	p.token, p.expiresAt = out.AccessToken, expires
	p.tokenMu.Unlock()
	if p.cache != nil {
		ttl := time.Until(expires) - asiaTechTokenSkew
		if ttl > 0 {
			_ = p.cache.Set(ctx, asiaTechTokenCacheKey(), out.AccessToken, ttl).Err()
		}
	}
	return out.AccessToken, nil
}
func asiaTechTokenCacheKey() string { return "yamata:asiatech:bulk-api-access-token" }
func asiaTechTokenLockKey() string  { return "yamata:asiatech:bulk-api-access-token:refresh-lock" }

func (p *asiaTechSMSProvider) request(ctx context.Context, method, path string, body []byte, limiter *smsRateLimiter) ([]byte, int, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, 0, err
	}
	token, err := p.tokenFor(ctx, false)
	if err != nil {
		return nil, 0, err
	}
	call := func(token string) ([]byte, int, error) {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.cfg.BaseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		payload, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return payload, resp.StatusCode, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return payload, resp.StatusCode, &asiaTechHTTPError{operation: path, status: resp.StatusCode}
		}
		return payload, resp.StatusCode, nil
	}
	payload, status, err := call(token)
	var httpErr *asiaTechHTTPError
	if errors.As(err, &httpErr) && httpErr.status == http.StatusUnauthorized {
		if token, refreshErr := p.tokenFor(ctx, true); refreshErr == nil {
			return call(token)
		}
	}
	if errors.As(err, &httpErr) && httpErr.status == http.StatusTooManyRequests {
		// A 429 is an explicit rejection, unlike a transport/5xx failure whose
		// submission outcome cannot safely be replayed.
		if sleepErr := sleepWithContext(ctx, time.Second); sleepErr == nil {
			if waitErr := limiter.Wait(ctx); waitErr == nil {
				return call(token)
			}
		}
	}
	return payload, status, err
}

func normalizeAsiaTechNumber(value string) (string, error) {
	v := strings.NewReplacer(" ", "", "-", "", "+", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(v, "0098") {
		v = "98" + strings.TrimPrefix(v, "0098")
	} else if strings.HasPrefix(v, "0") {
		v = "98" + strings.TrimPrefix(v, "0")
	}
	if len(v) != 12 || !strings.HasPrefix(v, "989") {
		return "", fmt.Errorf("invalid Iranian mobile number")
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("invalid Iranian mobile number")
		}
	}
	return v, nil
}
func normalizeAsiaTechSender(value string) (string, error) {
	v := strings.NewReplacer(" ", "", "-", "", "+", "").Replace(strings.TrimSpace(value))
	if v == "" {
		return "", errors.New("sender is required")
	}
	if strings.HasPrefix(v, "0") {
		v = "98" + strings.TrimPrefix(v, "0")
	}
	return v, nil
}
func estimateAsiaTechParts(body string) int {
	if body == "" {
		return 1
	}
	n := len([]rune(body))
	if n <= 70 {
		return 1
	}
	return (n + 66) / 67
}
func (p *asiaTechSMSProvider) waitParts(ctx context.Context, parts int) error {
	if parts < 1 {
		parts = 1
	}
	p.mpsMu.Lock()
	mps := p.mps
	if mps < 1 {
		mps = 1
	}
	now := time.Now()
	at := p.nextPart
	if at.Before(now) {
		at = now
	}
	p.nextPart = at.Add(time.Second * time.Duration(parts) / time.Duration(mps))
	p.mpsMu.Unlock()
	if delay := time.Until(at); delay > 0 {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (p *asiaTechSMSProvider) SendBatch(ctx context.Context, sender string, items []SMSProviderMessage) (SMSProviderSendResult, error) {
	if err := p.Validate(); err != nil {
		return SMSProviderSendResult{}, err
	}
	if len(items) == 0 {
		return SMSProviderSendResult{}, nil
	}
	if len(items) > p.MaxBatchSize() {
		return SMSProviderSendResult{}, fmt.Errorf("AsiaTech batch exceeds %d messages", p.MaxBatchSize())
	}
	source, err := normalizeAsiaTechSender(sender)
	if err != nil {
		return SMSProviderSendResult{}, err
	}
	info, err := p.UserInfo(ctx)
	if err != nil {
		return SMSProviderSendResult{}, fmt.Errorf("read AsiaTech sender/MPS settings: %w", err)
	}
	if len(info.SenderIDs) > 0 {
		allowed := false
		for _, candidate := range info.SenderIDs {
			normalized, normalizeErr := normalizeAsiaTechSender(candidate)
			if normalizeErr == nil && normalized == source {
				allowed = true
				break
			}
		}
		if !allowed {
			return SMSProviderSendResult{}, fmt.Errorf("AsiaTech sender %q is not permitted for this account", source)
		}
	}
	reqs := make([]asiaTechP2PRequest, 0, len(items))
	valid := make([]SMSProviderMessage, 0, len(items))
	preflight := make([]SMSProviderSendItem, 0)
	parts := 0
	for _, item := range items {
		recipient, e := normalizeAsiaTechNumber(item.Recipient)
		if e != nil {
			code, desc := "INVALID_RECIPIENT", e.Error()
			preflight = append(preflight, SMSProviderSendItem{TrackingID: item.TrackingID, InternalStatus: models.SMSSendStatusUnsuccessful, ErrorCode: &code, Description: &desc})
			continue
		}
		if strings.TrimSpace(item.Body) == "" {
			code, desc := "INVALID_MESSAGE", "message body is empty"
			preflight = append(preflight, SMSProviderSendItem{TrackingID: item.TrackingID, InternalStatus: models.SMSSendStatusUnsuccessful, ErrorCode: &code, Description: &desc})
			continue
		}
		valid = append(valid, item)
		reqs = append(reqs, asiaTechP2PRequest{SourceAddress: source, DestinationAddress: recipient, MessageText: item.Body, UDH: item.TrackingID})
		parts += estimateAsiaTechParts(item.Body)
	}
	if len(reqs) == 0 {
		return SMSProviderSendResult{Items: preflight}, nil
	}
	if err := p.waitParts(ctx, parts); err != nil {
		return SMSProviderSendResult{Items: preflight}, err
	}
	body, _ := json.Marshal(reqs)
	raw, status, err := p.request(ctx, http.MethodPost, asiaTechP2PBulkPath, body, p.sendLimiter)
	result := SMSProviderSendResult{RawResponse: utilsStringPtr(string(raw)), HTTPStatusCode: utilsIntPtr(status), AttemptCount: 1, Items: preflight}
	if err != nil {
		return result, err
	}
	var envelope asiaTechEnvelope[[]asiaTechP2PResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return result, fmt.Errorf("decode AsiaTech P2PBulk response: %w", err)
	}
	if !envelope.Succeeded || envelope.ResultCode != 100 {
		return result, &asiaTechHTTPError{operation: "send", status: status, resultCode: &envelope.ResultCode, message: envelope.Message}
	}
	if len(envelope.Data) != len(valid) {
		return result, fmt.Errorf("AsiaTech P2PBulk response count=%d, want=%d; refusing unsafe association", len(envelope.Data), len(valid))
	}
	for i, response := range envelope.Data {
		item := valid[i]
		metadata, _ := json.Marshal(map[string]any{"part": response.Part, "upstream_gateway": response.UpstreamGateway, "udh": item.TrackingID, "source_address": source, "destination_address": reqs[i].DestinationAddress})
		if response.ErrorCode != nil && *response.ErrorCode != 0 {
			code := strconv.Itoa(*response.ErrorCode)
			desc := response.ErrorMessage
			result.Items = append(result.Items, SMSProviderSendItem{TrackingID: item.TrackingID, InternalStatus: models.SMSSendStatusUnsuccessful, ErrorCode: &code, Description: &desc, Metadata: metadata})
			continue
		}
		id := strings.TrimSpace(response.ID)
		if id == "" {
			code, desc := "MISSING_MESSAGE_ID", "AsiaTech accepted response without id"
			result.Items = append(result.Items, SMSProviderSendItem{TrackingID: item.TrackingID, InternalStatus: models.SMSSendStatusPending, ErrorCode: &code, Description: &desc, Metadata: metadata})
			continue
		}
		result.Items = append(result.Items, SMSProviderSendItem{TrackingID: item.TrackingID, ProviderMessageID: &id, InternalStatus: models.SMSSendStatusPending, TrackDeliveryStatus: true, Metadata: metadata})
	}
	return result, nil
}

func (p *asiaTechSMSProvider) FetchStatus(ctx context.Context, ids []string) (SMSProviderStatusResult, error) {
	if err := p.Validate(); err != nil {
		return SMSProviderStatusResult{}, err
	}
	if len(ids) > 1000 {
		return SMSProviderStatusResult{}, errors.New("AsiaTech DLR accepts at most 1000 ids")
	}
	if len(ids) == 0 {
		return SMSProviderStatusResult{}, nil
	}
	body, _ := json.Marshal(ids)
	raw, _, err := p.request(ctx, http.MethodPost, asiaTechDLRPath, body, p.dlrLimiter)
	result := SMSProviderStatusResult{RawResponse: utilsStringPtr(string(raw))}
	if err != nil {
		return result, err
	}
	var envelope asiaTechEnvelope[[]asiaTechDLRResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return result, fmt.Errorf("decode AsiaTech DLR response: %w", err)
	}
	if !envelope.Succeeded || envelope.ResultCode != 100 {
		return result, &asiaTechHTTPError{operation: "getdlr", status: 200, resultCode: &envelope.ResultCode, message: envelope.Message}
	}
	for _, dlr := range envelope.Data {
		code := strconv.Itoa(dlr.DeliveryStatus)
		text := asiaTechDeliveryStatusText(dlr.DeliveryStatus)
		meta, _ := json.Marshal(map[string]any{"udh": dlr.UDH, "part_status": dlr.PartStatus, "delivery_status": dlr.DeliveryStatus})
		total, delivered, undelivered, unknown := asiaTechParts(dlr.PartStatus)
		status := models.SMSSendStatusPending
		if asiaTechDeliveryFinal(dlr.DeliveryStatus, time.Time{}) {
			if delivered == total && total > 0 {
				status = models.SMSSendStatusSuccessful
			} else {
				status = models.SMSSendStatusUnsuccessful
			}
		}
		result.Items = append(result.Items, SMSProviderStatusItem{ProviderMessageID: dlr.ID, ProviderStatusCode: &code, ProviderStatusText: &text, InternalStatus: status, TotalParts: total, DeliveredParts: delivered, UndeliveredParts: undelivered, UnknownParts: unknown, Metadata: meta})
	}
	return result, nil
}
func (p *asiaTechSMSProvider) UserInfo(ctx context.Context) (AsiaTechUserInfo, error) {
	var zero AsiaTechUserInfo
	raw, _, err := p.request(ctx, http.MethodGet, asiaTechUserInfoPath, nil, p.sendLimiter)
	if err != nil {
		return zero, err
	}
	var env asiaTechEnvelope[AsiaTechUserInfo]
	if err := json.Unmarshal(raw, &env); err != nil {
		return zero, err
	}
	if !env.Succeeded || env.ResultCode != 100 {
		return zero, &asiaTechHTTPError{operation: "userinfo", status: 200, resultCode: &env.ResultCode, message: env.Message}
	}
	if env.Data.MPS > 0 {
		p.mpsMu.Lock()
		p.mps = env.Data.MPS
		p.mpsMu.Unlock()
	}
	return env.Data, nil
}
func (p *asiaTechSMSProvider) Ping(ctx context.Context) error {
	raw, _, err := p.request(ctx, http.MethodGet, asiaTechPingPath, nil, p.sendLimiter)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(string(raw)), "PONG") {
		return fmt.Errorf("AsiaTech ping returned unexpected response")
	}
	return nil
}
func asiaTechParts(parts []asiaTechDLRPart) (int64, int64, int64, int64) {
	if len(parts) == 0 {
		return 0, 0, 0, 1
	}
	total := int64(len(parts))
	var delivered, undelivered int64
	for _, p := range parts {
		if p.Status == 1 {
			delivered++
		} else if asiaTechDeliveryFinal(p.Status, time.Now().Add(-8*time.Hour)) {
			undelivered++
		}
	}
	return total, delivered, undelivered, total - delivered - undelivered
}
func asiaTechDeliveryFinal(code int, submitted time.Time) bool {
	if code == 1 {
		return submitted.IsZero() || time.Since(submitted) >= 20*time.Minute
	}
	if code == 9 || code == 34 {
		return !submitted.IsZero() && time.Since(submitted) >= 7*time.Hour
	}
	switch code {
	case 2, 3, 5, 7, 10, 11, 16, 32, 33, 36:
		return true
	}
	return false
}
func asiaTechDeliveryStatusText(code int) string {
	names := map[int]string{1: "Delivered", 2: "UnDelivered", 3: "Accepted", 5: "Rejected", 7: "ErrorInSending", 9: "Sent", 10: "NotSent", 11: "Expired", 16: "Deleted", 32: "Unknown", 33: "Enroute", 34: "Undeliverable", 36: "UnreachableNetwork"}
	if n := names[code]; n != "" {
		return n
	}
	return "Unknown"
}
func utilsStringPtr(v string) *string { return &v }
func utilsIntPtr(v int) *int          { return &v }
