package scheduler

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

const maxExternalMappingBodyBytes = 12 * 1024 * 1024
const maxExternalMappingBatchSize = 500

type ExternalShortLinkClickPage struct {
	Clicks      []models.ExternalShortLinkClick `json:"clicks"`
	NextAfterID int64                           `json:"next_after_id"`
	HasMore     bool                            `json:"has_more"`
}

// ExternalShortLinkAPI is shared by the campaign publication gate and the two background workers.
type ExternalShortLinkAPI interface {
	UploadMappings(ctx context.Context, links []*models.ShortLink) error
	FetchClicks(ctx context.Context, afterID int64, limit int) (*ExternalShortLinkClickPage, error)
	AcknowledgeClicks(ctx context.Context, throughClickID int64) error
}

type HTTPExternalShortLinkClient struct {
	baseURL            string
	token              string
	mappingBatchSize   int
	mappingParallelism int
	client             *http.Client
}

func NewExternalShortLinkClient(cfg config.ExternalShortLinkConfig) (*HTTPExternalShortLinkClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	parsedBaseURL, err := url.Parse(baseURL)
	validScheme := parsedBaseURL != nil && (parsedBaseURL.Scheme == "https" || (cfg.AllowInsecureHTTP && parsedBaseURL.Scheme == "http"))
	if err != nil || !validScheme || parsedBaseURL.Hostname() == "" || parsedBaseURL.User != nil ||
		(parsedBaseURL.Path != "" && parsedBaseURL.Path != "/") || parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
		return nil, fmt.Errorf("external short-link base URL is invalid or insecure")
	}
	if !isURLSafeExternalShortLinkToken(cfg.APIToken) {
		return nil, fmt.Errorf("external short-link API token must be a URL-safe secret with at least 32 characters")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, fmt.Errorf("external short-link request timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read external short-link CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("external short-link CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load external short-link client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	transport.TLSClientConfig = tlsConfig
	return &HTTPExternalShortLinkClient{
		baseURL:            baseURL,
		token:              cfg.APIToken,
		mappingBatchSize:   cfg.MappingBatchSize,
		mappingParallelism: cfg.MappingParallelism,
		client:             &http.Client{Timeout: cfg.RequestTimeout, Transport: transport},
	}, nil
}

func isURLSafeExternalShortLinkToken(token string) bool {
	if len(token) < 32 {
		return false
	}
	for _, character := range token {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '~' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func (c *HTTPExternalShortLinkClient) endpoint(path string) string {
	return c.baseURL + path
}

func (c *HTTPExternalShortLinkClient) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal external short-link request: %w", err)
		}
		return c.requestPayload(ctx, method, path, payload, out)
	}
	return c.requestReader(ctx, method, path, reader, false, out)
}

func (c *HTTPExternalShortLinkClient) requestPayload(ctx context.Context, method, path string, payload []byte, out any) error {
	return c.requestReader(ctx, method, path, bytes.NewReader(payload), true, out)
}

func (c *HTTPExternalShortLinkClient) requestReader(ctx context.Context, method, path string, reader io.Reader, hasJSONBody bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if hasJSONBody {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("external short-link %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64*1024*1024))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode external short-link response: %w", err)
	}
	return nil
}

type externalMappingUpload struct {
	Links []externalMapping `json:"links"`
}

type externalMapping struct {
	Code            string  `json:"code"`
	LongURL         string  `json:"long_url"`
	ShortURL        string  `json:"short_url"`
	SourceLinkID    uint    `json:"source_link_id"`
	CampaignID      *uint   `json:"campaign_id,omitempty"`
	ClientID        *uint   `json:"client_id,omitempty"`
	ScenarioID      *uint   `json:"scenario_id,omitempty"`
	ScenarioName    *string `json:"scenario_name,omitempty"`
	PhoneNumber     *string `json:"phone_number,omitempty"`
	IsTest          bool    `json:"is_test"`
	SourceCreatedAt string  `json:"source_created_at,omitempty"`
	SourceUpdatedAt string  `json:"source_updated_at,omitempty"`
}

func (c *HTTPExternalShortLinkClient) UploadMappings(ctx context.Context, links []*models.ShortLink) error {
	if len(links) == 0 {
		return nil
	}
	batchSize := c.mappingBatchSize
	if batchSize <= 0 || batchSize > maxExternalMappingBatchSize {
		batchSize = maxExternalMappingBatchSize
	}
	type uploadChunk struct {
		start    int
		end      int
		expected int
		encoded  []byte
	}
	buildChunk := func(start int) (uploadChunk, error) {
		end := min(start+batchSize, len(links))
		payload, encoded, err := externalMappingPayload(links[start:end])
		if err != nil {
			return uploadChunk{}, fmt.Errorf("build external mapping chunk [%d,%d): %w", start, end, err)
		}
		for len(encoded) > maxExternalMappingBodyBytes && len(payload.Links) > 1 {
			end = start + (end-start+1)/2
			payload, encoded, err = externalMappingPayload(links[start:end])
			if err != nil {
				return uploadChunk{}, fmt.Errorf("build external mapping chunk [%d,%d): %w", start, end, err)
			}
		}
		if len(encoded) > maxExternalMappingBodyBytes {
			return uploadChunk{}, fmt.Errorf("external mapping at offset %d exceeds the %d-byte request limit", start, maxExternalMappingBodyBytes)
		}
		return uploadChunk{start: start, end: end, expected: len(payload.Links), encoded: encoded}, nil
	}

	upload := func(ctx context.Context, chunk uploadChunk) error {
		var response struct {
			Persisted int `json:"persisted"`
		}
		if err := c.requestPayload(ctx, http.MethodPost, "/api/v1/links/batch", chunk.encoded, &response); err != nil {
			return fmt.Errorf("upload mapping chunk [%d,%d): %w", chunk.start, chunk.end, err)
		}
		if response.Persisted != chunk.expected {
			return fmt.Errorf("upload mapping chunk [%d,%d): acknowledged %d of %d mappings", chunk.start, chunk.end, response.Persisted, chunk.expected)
		}
		return nil
	}

	parallelism := c.mappingParallelism
	if parallelism <= 1 || len(links) <= batchSize {
		for start := 0; start < len(links); {
			chunk, err := buildChunk(start)
			if err != nil {
				return err
			}
			if err := upload(ctx, chunk); err != nil {
				return err
			}
			start = chunk.end
		}
		return nil
	}
	chunkCount := (len(links) + batchSize - 1) / batchSize
	if parallelism > chunkCount {
		parallelism = chunkCount
	}

	// The redirect service treats mapping batches as idempotent and has an
	// eight-connection pool. A bounded fan-out dramatically shortens a large
	// allocation without monopolizing that pool or reordering any local state.
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan uploadChunk)
	var (
		workers  sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for range parallelism {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for chunk := range jobs {
				if err := upload(uploadCtx, chunk); err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
			}
		}()
	}
	for start := 0; start < len(links); {
		chunk, err := buildChunk(start)
		if err != nil {
			close(jobs)
			workers.Wait()
			return err
		}
		select {
		case jobs <- chunk:
			start = chunk.end
		case <-uploadCtx.Done():
			close(jobs)
			workers.Wait()
			if firstErr != nil {
				return firstErr
			}
			return uploadCtx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return firstErr
}

func externalMappingPayload(links []*models.ShortLink) (externalMappingUpload, []byte, error) {
	payload := externalMappingUpload{Links: make([]externalMapping, 0, len(links))}
	for offset, link := range links {
		if link == nil {
			return externalMappingUpload{}, nil, fmt.Errorf("external mapping at offset %d is nil", offset)
		}
		payload.Links = append(payload.Links, externalMappingFromShortLink(link))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return externalMappingUpload{}, nil, fmt.Errorf("marshal external short-link request: %w", err)
	}
	return payload, encoded, nil
}

func externalMappingFromShortLink(link *models.ShortLink) externalMapping {
	mapping := externalMapping{
		Code:         link.UID,
		LongURL:      link.LongLink,
		ShortURL:     link.ShortLink,
		SourceLinkID: link.ID,
		CampaignID:   link.CampaignID,
		ClientID:     link.ClientID,
		ScenarioID:   link.ScenarioID,
		ScenarioName: link.ScenarioName,
		PhoneNumber:  link.PhoneNumber,
		IsTest:       link.IsTest,
	}
	if !link.CreatedAt.IsZero() {
		mapping.SourceCreatedAt = link.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if !link.UpdatedAt.IsZero() {
		mapping.SourceUpdatedAt = link.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return mapping
}

func (c *HTTPExternalShortLinkClient) FetchClicks(ctx context.Context, afterID int64, limit int) (*ExternalShortLinkClickPage, error) {
	query := url.Values{}
	query.Set("after_id", strconv.FormatInt(afterID, 10))
	query.Set("limit", strconv.Itoa(limit))
	var page ExternalShortLinkClickPage
	if err := c.request(ctx, http.MethodGet, "/api/v1/clicks?"+query.Encode(), nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (c *HTTPExternalShortLinkClient) AcknowledgeClicks(ctx context.Context, throughClickID int64) error {
	var response struct {
		ThroughClickID int64 `json:"through_click_id"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/v1/clicks/ack", map[string]int64{"through_click_id": throughClickID}, &response); err != nil {
		return err
	}
	if response.ThroughClickID < throughClickID {
		return fmt.Errorf("external acknowledgement stopped at %d, expected at least %d", response.ThroughClickID, throughClickID)
	}
	return nil
}
