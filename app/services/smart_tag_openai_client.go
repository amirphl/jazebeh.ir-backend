package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

type SmartTagOpenAIClient interface {
	CallResponsesAPI(ctx context.Context, payload map[string]any) (*SmartTagOpenAIResult, error)
}

type SmartTagOpenAIResult struct {
	RequestPayload     json.RawMessage
	RawResponse        string
	HTTPStatusCode     int
	RetryAfter         time.Duration
	ProviderResponseID *string
	ModelName          string
	UsageMetadata      json.RawMessage
	RequestedAt        time.Time
	RespondedAt        time.Time
}

type SmartTagOpenAIHTTPError struct {
	StatusCode int
	Message    string
}

func (e *SmartTagOpenAIHTTPError) Error() string {
	return fmt.Sprintf("OPENAI_HTTP_ERROR: status=%d message=%s", e.StatusCode, e.Message)
}

func (e *SmartTagOpenAIHTTPError) Retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusConflict ||
		e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= http.StatusInternalServerError
}

type smartTagOpenAIClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

func NewSmartTagOpenAIClient(cfg config.SmartTagOpenAIConfig) (SmartTagOpenAIClient, error) {
	if cfg.Mock {
		return mockSmartTagOpenAIClient{model: cfg.Model}, nil
	}
	var parsedProxy *url.URL
	if cfg.HTTPProxy != nil {
		proxyURL := strings.TrimSpace(*cfg.HTTPProxy)
		if proxyURL != "" {
			var err error
			parsedProxy, err = url.Parse(proxyURL)
			if err != nil {
				return nil, fmt.Errorf("OPENAI_PROXY_INVALID: %w", err)
			}
		}
	}

	apiKeyEnv := strings.TrimSpace(cfg.APIKeyEnv)
	if apiKeyEnv == "" {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY_NOT_CONFIGURED")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if parsedProxy != nil {
		transport.Proxy = http.ProxyURL(parsedProxy)
	}

	return &smartTagOpenAIClient{
		baseURL: baseURL,
		model:   cfg.Model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}, nil
}

func (c *smartTagOpenAIClient) CallResponsesAPI(ctx context.Context, payload map[string]any) (*SmartTagOpenAIResult, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	if strings.TrimSpace(c.model) != "" {
		if _, ok := payload["model"]; !ok {
			payload["model"] = c.model
		}
	}

	requestedAt := time.Now().UTC()
	result := &SmartTagOpenAIResult{
		ModelName:   c.model,
		RequestedAt: requestedAt,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		result.RespondedAt = time.Now().UTC()
		return result, err
	}
	result.RequestPayload = reqBody

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(reqBody))
	if err != nil {
		result.RespondedAt = time.Now().UTC()
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.RespondedAt = time.Now().UTC()
		return result, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	respondedAt := time.Now().UTC()
	if readErr != nil {
		result.HTTPStatusCode = resp.StatusCode
		result.RespondedAt = respondedAt
		return result, readErr
	}

	result.RawResponse = string(body)
	result.HTTPStatusCode = resp.StatusCode
	result.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), respondedAt)
	result.RespondedAt = respondedAt

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err == nil {
		if id, ok := decoded["id"].(string); ok && strings.TrimSpace(id) != "" {
			result.ProviderResponseID = &id
		}
		if modelName, ok := decoded["model"].(string); ok && strings.TrimSpace(modelName) != "" {
			result.ModelName = modelName
		}
		if usage, ok := decoded["usage"]; ok {
			if usageJSON, marshalErr := json.Marshal(usage); marshalErr == nil {
				result.UsageMetadata = usageJSON
			}
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return result, &SmartTagOpenAIHTTPError{
			StatusCode: resp.StatusCode,
			Message:    openAIErrorMessage(resp.StatusCode, body),
		}
	}

	return result, nil
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
		return seconds
	}
	if retryAt, err := http.ParseTime(raw); err == nil {
		if delay := retryAt.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func openAIErrorMessage(statusCode int, body []byte) string {
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		if message := strings.TrimSpace(decoded.Error.Message); message != "" {
			return message
		}
	}

	const maxErrorLength = 512
	message := strings.TrimSpace(string(body))
	if len(message) > maxErrorLength {
		message = message[:maxErrorLength] + "..."
	}
	if message == "" {
		return http.StatusText(statusCode)
	}
	return message
}
