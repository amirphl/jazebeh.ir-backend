package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
)

const defaultBotAPIDomain = "https://jazebeh.ir"

// defaultShortLinkAllocationTimeout is deliberately much longer than normal
// bot calls: allocation persists a large batch and may synchronously publish
// redirect mappings before returning codes to a scheduler.
// This must outlive the API handler (50m) and proxy (55m), so a valid
// response has time to reach the scheduler instead of becoming an ambiguous
// client-side timeout.
const defaultShortLinkAllocationTimeout = 60 * time.Minute

type BotClient interface {
	Login(ctx context.Context) (string, error)
	ListReadyCampaigns(ctx context.Context, token string, platform string) ([]dto.BotGetCampaignResponse, error)
	MoveCampaignToRunning(ctx context.Context, token string, id uint) error
	MoveCampaignToExecuted(ctx context.Context, token string, id uint) error
	DownloadTargetAudienceExcelFile(ctx context.Context, token string, campaignID uint) ([]byte, error)
	AllocateShortLinks(ctx context.Context, token string, req *dto.BotAllocateShortLinksRequest) ([]string, error)
	PushCampaignStatistics(ctx context.Context, processedCampaignID uint, stats map[string]any) error
	PushCampaignAudienceUIDs(ctx context.Context, campaignID uint, uids, codes []string) error
	CreateShortLinks(ctx context.Context, token string, reqBody *dto.BotCreateShortLinksRequest) error
	DownloadCampaignMedia(ctx context.Context, token, mediaUUID string) (string, error)
}

type httpBotClient struct {
	cfg                  config.BotConfig
	client               *http.Client
	shortLinkAllocClient *http.Client
}

func newHTTPBotClient(cfg config.BotConfig) *httpBotClient {
	cfg.APIDomain = strings.TrimRight(cfg.APIDomain, "/")
	if cfg.APIDomain == "" {
		cfg.APIDomain = defaultBotAPIDomain
	}

	allocationTimeout := cfg.ShortLinkAllocationTimeout
	if allocationTimeout <= 0 {
		allocationTimeout = defaultShortLinkAllocationTimeout
	}
	return &httpBotClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		shortLinkAllocClient: &http.Client{Timeout: allocationTimeout},
	}
}

func (c *httpBotClient) endpoint(path string) string {
	return c.cfg.APIDomain + path
}

func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal json payload: %w", err)
	}
	return b, nil
}

func statusErr(operation string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("%s http status: %d", operation, resp.StatusCode)
	}
	return fmt.Errorf("%s http status: %d body: %s", operation, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *httpBotClient) Login(ctx context.Context) (string, error) {
	if c.cfg.Username == "" || c.cfg.Password == "" {
		return "", fmt.Errorf("bot credentials not configured")
	}
	endpoint := c.endpoint("/api/v1/bot/auth/login")
	reqBody := dto.BotLoginRequest{
		Username: c.cfg.Username,
		Password: c.cfg.Password,
	}
	payload, err := marshalJSON(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusErr("bot login", resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp dto.APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("failed to decode JSON into APIResponse: %w", err)
	}

	if !apiResp.Success {
		return "", fmt.Errorf("bot login failed: %v", apiResp.Message)
	}

	dataBytes, err := json.Marshal(apiResp.Data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal APIResponse data: %w", err)
	}

	var botLoginResp dto.BotLoginResponse
	if err := json.Unmarshal(dataBytes, &botLoginResp); err != nil {
		return "", fmt.Errorf("failed to decode JSON into BotLoginResponse: %w", err)
	}

	if botLoginResp.Session.AccessToken == "" {
		return "", fmt.Errorf("empty bot access token")
	}

	return botLoginResp.Session.AccessToken, nil
}

func (c *httpBotClient) ListReadyCampaigns(ctx context.Context, token string, platform string) ([]dto.BotGetCampaignResponse, error) {
	endpoint := c.endpoint("/api/v1/bot/campaigns/ready")
	if platform != "" {
		q := url.Values{}
		q.Set("platform", platform)
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusErr("ready campaigns", resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp dto.APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode JSON into APIResponse: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("list ready campaigns failed: %v", apiResp.Message)
	}

	dataBytes, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal APIResponse data: %w", err)
	}

	var listCampaignsResp dto.BotListCampaignsResponse
	if err := json.Unmarshal(dataBytes, &listCampaignsResp); err != nil {
		return nil, fmt.Errorf("failed to decode JSON into BotListCampaignsResponse: %w", err)
	}

	return listCampaignsResp.Items, nil
}

func (c *httpBotClient) MoveCampaignToRunning(ctx context.Context, token string, id uint) error {
	endpoint := c.endpoint("/api/v1/bot/campaigns/" + strconv.FormatUint(uint64(id), 10) + "/running")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr("move to running", resp)
	}
	return nil
}

func (c *httpBotClient) MoveCampaignToExecuted(ctx context.Context, token string, id uint) error {
	endpoint := c.endpoint("/api/v1/bot/campaigns/" + strconv.FormatUint(uint64(id), 10) + "/executed")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr("move to executed", resp)
	}
	return nil
}

func (c *httpBotClient) DownloadTargetAudienceExcelFile(ctx context.Context, token string, campaignID uint) ([]byte, error) {
	endpoint := c.endpoint(fmt.Sprintf("/api/v1/bot/campaigns/%d/target-audience-excel-file", campaignID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusErr("download target audience excel file", resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (c *httpBotClient) AllocateShortLinks(ctx context.Context, token string, req *dto.BotAllocateShortLinksRequest) ([]string, error) {
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/v1/bot/short-links/allocate"), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.shortLinkAllocClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusErr("allocate short-links", resp)
	}
	var apiResp dto.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("allocate short-links failed: %v", apiResp.Message)
	}
	dataBytes, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, err
	}
	var out dto.BotAllocateShortLinksResponse
	if err := json.Unmarshal(dataBytes, &out); err != nil {
		return nil, err
	}
	return out.Codes, nil
}

func (c *httpBotClient) PushCampaignStatistics(ctx context.Context, campaignID uint, stats map[string]any) error {
	token, err := c.Login(ctx)
	if err != nil {
		return err
	}
	endpoint := c.endpoint(fmt.Sprintf("/api/v1/bot/campaigns/%d/statistics", campaignID))
	payload, err := marshalJSON(dto.BotUpdateCampaignStatisticsRequest{Statistics: stats})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr("push statistics", resp)
	}
	return nil
}

const audienceUIDChunkSize = 5000

// PushCampaignAudienceUIDs sends all audience UIDs (and their short-link codes) to the
// server in chunks of audienceUIDChunkSize. It logs in once and reuses the token for all
// chunks so that a 250K-UID campaign only incurs one auth round-trip.
func (c *httpBotClient) PushCampaignAudienceUIDs(ctx context.Context, campaignID uint, uids, codes []string) error {
	if len(uids) == 0 {
		return nil
	}
	token, err := c.Login(ctx)
	if err != nil {
		return fmt.Errorf("push audience UIDs login: %w", err)
	}
	endpoint := c.endpoint(fmt.Sprintf("/api/v1/bot/campaigns/%d/audience-uids", campaignID))
	for start := 0; start < len(uids); start += audienceUIDChunkSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("push audience UIDs context cancelled at offset %d: %w", start, err)
		}
		end := min(start+audienceUIDChunkSize, len(uids))
		items := make([]dto.BotAudienceUIDItem, end-start)
		for i, uid := range uids[start:end] {
			code := ""
			if start+i < len(codes) {
				code = codes[start+i]
			}
			items[i] = dto.BotAudienceUIDItem{UID: uid, Code: code}
		}
		payload, err := marshalJSON(dto.BotPushAudienceUIDsRequest{Items: items})
		if err != nil {
			return fmt.Errorf("push audience UIDs marshal chunk [%d,%d): %w", start, end, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("push audience UIDs build request chunk [%d,%d): %w", start, end, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("push audience UIDs chunk [%d,%d): %w", start, end, err)
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("push audience UIDs chunk [%d,%d) http status: %d", start, end, resp.StatusCode)
		}
	}
	return nil
}

func (c *httpBotClient) CreateShortLinks(ctx context.Context, token string, reqBody *dto.BotCreateShortLinksRequest) error {
	if reqBody == nil {
		return fmt.Errorf("create short-links request body is nil")
	}

	endpoint := c.endpoint("/api/v1/bot/short-links")
	payload, err := marshalJSON(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr("create short-links", resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	var apiResp dto.APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("failed to decode JSON into APIResponse: %w", err)
	}
	if !apiResp.Success {
		return fmt.Errorf("create short-links failed: %v", apiResp.Message)
	}
	return nil
}

func (c *httpBotClient) DownloadCampaignMedia(ctx context.Context, token, mediaUUID string) (string, error) {
	if strings.TrimSpace(mediaUUID) == "" {
		return "", fmt.Errorf("media uuid is required")
	}

	endpoint := c.endpoint("/api/v1/bot/media/" + mediaUUID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "*/*")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusErr("bot media download", resp)
	}

	head := make([]byte, 512)
	n, readErr := io.ReadFull(resp.Body, head)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", readErr
	}
	head = head[:n]

	pattern := "bale-media-*"
	if ext := resolveBotDownloadedMediaExtension(resp, head); ext != "" {
		pattern += ext
	}

	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if len(head) > 0 {
		if _, err := tmpFile.Write(head); err != nil {
			_ = os.Remove(tmpFile.Name())
			return "", err
		}
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

func resolveBotDownloadedMediaExtension(resp *http.Response, bodyHead []byte) string {
	if resp != nil {
		contentDisposition := strings.TrimSpace(resp.Header.Get("Content-Disposition"))
		if contentDisposition != "" {
			_, params, err := mime.ParseMediaType(contentDisposition)
			if err == nil {
				if filename := strings.TrimSpace(params["filename"]); filename != "" {
					if ext := normalizeBotDownloadedMediaExt(filepath.Ext(filename)); ext != "" {
						return ext
					}
				}
				if filenameStar := strings.TrimSpace(params["filename*"]); filenameStar != "" {
					if idx := strings.Index(filenameStar, "''"); idx >= 0 {
						filenameStar = filenameStar[idx+2:]
					}
					if decoded, err := url.QueryUnescape(filenameStar); err == nil {
						filenameStar = decoded
					}
					if ext := normalizeBotDownloadedMediaExt(filepath.Ext(filenameStar)); ext != "" {
						return ext
					}
				}
			}
		}

		contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
		if ext := botMediaTypeToExtension(contentType); ext != "" {
			return ext
		}
	}

	if len(bodyHead) > 0 {
		contentType := http.DetectContentType(bodyHead)
		if ext := botMediaTypeToExtension(contentType); ext != "" {
			return ext
		}
	}

	return ""
}

func botMediaTypeToExtension(contentType string) string {
	trimmed := strings.TrimSpace(contentType)
	if trimmed == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(trimmed)
	if err == nil {
		trimmed = mediaType
	}
	trimmed = strings.ToLower(trimmed)

	switch trimmed {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/opus":
		return ".opus"
	}

	if exts, err := mime.ExtensionsByType(trimmed); err == nil {
		for _, ext := range exts {
			if normalized := normalizeBotDownloadedMediaExt(ext); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func normalizeBotDownloadedMediaExt(ext string) string {
	trimmed := strings.ToLower(strings.TrimSpace(ext))
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, ".") {
		trimmed = "." + trimmed
	}
	switch trimmed {
	case ".jpeg":
		return ".jpg"
	case ".oga", ".ogx":
		return ".ogg"
	}
	switch trimmed {
	case ".jpg", ".png", ".gif", ".mp4", ".ogg", ".opus":
		return trimmed
	default:
		return ""
	}
}
