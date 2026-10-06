package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

const (
	defaultSMSProviderName = models.SMSProviderPayamSMS
	payamSMSBatchSize      = 200
)

// SMSProvider decouples the scheduler from an individual SMS gateway. The
// scheduler owns durable tracking IDs; providers own protocol-specific IDs and
// raw status mappings.
type SMSProvider interface {
	Name() models.SMSProvider
	MaxBatchSize() int
	SendBatch(ctx context.Context, sender string, items []SMSProviderMessage) (SMSProviderSendResult, error)
	FetchStatus(ctx context.Context, lookupIDs []string) (SMSProviderStatusResult, error)
}

// SMSProviderReadinessChecker is implemented by providers that require
// configuration validation before a campaign is allowed to enter running.
// Keeping it optional avoids changing the established PayamSMS startup and
// send behavior.
type SMSProviderReadinessChecker interface {
	Validate() error
}

type SMSProviderMessage struct {
	TrackingID         string
	Recipient          string
	Body               string
	ProviderCustomerID *int64
}

type SMSProviderSendItem struct {
	TrackingID          string
	ProviderCustomerID  *int64
	ProviderMessageID   *string
	ProviderStatusCode  *string
	ProviderStatusText  *string
	InternalStatus      models.SMSSendStatus
	TrackDeliveryStatus bool
	ErrorCode           *string
	Description         *string
	// Metadata is provider-owned, valid JSON data used by durable integrations.
	// It is never written to scheduler logs.
	Metadata json.RawMessage
}

type SMSProviderSendResult struct {
	Items           []SMSProviderSendItem
	RawResponse     *string
	ResponseHeaders http.Header
	HTTPStatusCode  *int
	AttemptCount    int
}

type SMSProviderStatusItem struct {
	ProviderCustomerID *int64
	ProviderMessageID  string
	ProviderStatusCode *string
	ProviderStatusText *string
	InternalStatus     models.SMSSendStatus
	TotalParts         int64
	DeliveredParts     int64
	UndeliveredParts   int64
	UnknownParts       int64
	Metadata           json.RawMessage
}

type SMSProviderStatusResult struct {
	Items       []SMSProviderStatusItem
	RawResponse *string
}

type SMSProviderRegistry struct {
	providers map[models.SMSProvider]SMSProvider
}

func NewSMSProviderRegistry(providers ...SMSProvider) *SMSProviderRegistry {
	r := &SMSProviderRegistry{providers: make(map[models.SMSProvider]SMSProvider, len(providers))}
	for _, provider := range providers {
		if provider == nil || provider.Name() == "" {
			continue
		}
		r.providers[provider.Name()] = provider
	}
	return r
}

func (r *SMSProviderRegistry) Provider(provider models.SMSProvider) (SMSProvider, error) {
	if provider == "" {
		provider = defaultSMSProviderName
	}
	if r == nil {
		return nil, fmt.Errorf("SMS provider registry is unavailable")
	}
	p, ok := r.providers[provider]
	if !ok || p == nil {
		return nil, fmt.Errorf("SMS provider %q is not configured", provider)
	}
	return p, nil
}

func normalizeSMSProvider(provider models.SMSProvider) (models.SMSProvider, error) {
	provider = models.SMSProvider(strings.ToLower(strings.TrimSpace(string(provider))))
	if provider == "" {
		return defaultSMSProviderName, nil
	}
	if !models.IsValidSMSProvider(provider) {
		return "", fmt.Errorf("unsupported SMS provider %q", provider)
	}
	return provider, nil
}

type payamSMSProvider struct {
	client PayamSMSClient
}

func newPayamSMSProvider(client PayamSMSClient) SMSProvider {
	return &payamSMSProvider{client: client}
}

func (p *payamSMSProvider) Name() models.SMSProvider { return models.SMSProviderPayamSMS }
func (p *payamSMSProvider) MaxBatchSize() int        { return payamSMSBatchSize }

func (p *payamSMSProvider) SendBatch(ctx context.Context, sender string, items []SMSProviderMessage) (SMSProviderSendResult, error) {
	if p == nil || p.client == nil {
		return SMSProviderSendResult{}, fmt.Errorf("PayamSMS provider is unavailable")
	}
	payamItems := make([]PayamSMSItem, 0, len(items))
	for _, item := range items {
		payamItems = append(payamItems, PayamSMSItem{
			Recipient:  item.Recipient,
			Body:       item.Body,
			TrackingID: item.TrackingID,
		})
	}
	result, err := p.client.SendBatch(ctx, sender, payamItems)
	out := SMSProviderSendResult{
		RawResponse:     result.RawResponse,
		ResponseHeaders: result.ResponseHeaders,
		HTTPStatusCode:  result.HTTPStatusCode,
		AttemptCount:    result.AttemptCount,
		Items:           make([]SMSProviderSendItem, 0, len(result.Items)),
	}
	for _, response := range result.Items {
		status := models.SMSSendStatusPending
		if response.ErrorCode != nil && strings.TrimSpace(*response.ErrorCode) != "" {
			status = models.SMSSendStatusUnsuccessful
		}
		out.Items = append(out.Items, SMSProviderSendItem{
			TrackingID:          strings.TrimSpace(response.TrackingID),
			ProviderMessageID:   response.ServerID,
			ProviderStatusCode:  response.ErrorCode,
			ProviderStatusText:  response.Desc,
			InternalStatus:      status,
			TrackDeliveryStatus: true, // Preserve existing PayamSMS polling behavior.
			ErrorCode:           response.ErrorCode,
			Description:         response.Desc,
		})
	}
	return out, err
}

func (p *payamSMSProvider) FetchStatus(ctx context.Context, trackingIDs []string) (SMSProviderStatusResult, error) {
	if p == nil || p.client == nil {
		return SMSProviderStatusResult{}, fmt.Errorf("PayamSMS provider is unavailable")
	}
	token, err := p.client.GetToken(ctx)
	if err != nil {
		return SMSProviderStatusResult{}, err
	}
	result, err := p.client.FetchStatus(ctx, token, trackingIDs)
	out := SMSProviderStatusResult{RawResponse: result.RawResponse, Items: make([]SMSProviderStatusItem, 0, len(result.Items))}
	for _, item := range result.Items {
		status := models.SMSSendStatusPending
		if item.TotalParts > 0 && item.TotalParts == item.TotalDeliveredParts {
			status = models.SMSSendStatusSuccessful
		} else if item.TotalUndeliveredParts > 0 {
			status = models.SMSSendStatusUnsuccessful
		}
		providerStatus := strings.TrimSpace(item.Status)
		var providerStatusPtr *string
		if providerStatus != "" {
			providerStatusPtr = &providerStatus
		}
		out.Items = append(out.Items, SMSProviderStatusItem{
			ProviderMessageID:  strings.TrimSpace(item.TrackingID),
			ProviderStatusText: providerStatusPtr,
			InternalStatus:     status,
			TotalParts:         item.TotalParts,
			DeliveredParts:     item.TotalDeliveredParts,
			UndeliveredParts:   item.TotalUndeliveredParts,
			UnknownParts:       item.TotalUnknownParts,
			Metadata:           json.RawMessage(`{}`),
		})
	}
	return out, err
}

// smsRateLimiter is a small context-aware shared limiter. Unlike a ticker,
// it has no background goroutine and therefore shuts down naturally with its
// provider.
type smsRateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newSMSRateLimiter(requestsPerSecond int) *smsRateLimiter {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 1
	}
	return &smsRateLimiter{interval: time.Second / time.Duration(requestsPerSecond)}
}

func (l *smsRateLimiter) Wait(ctx context.Context) error {
	if l == nil || l.interval <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()

	if delay := time.Until(at); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
