package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

// SplusClient is the interface for the Splus messaging provider.
type SplusClient interface {
	SendMessage(ctx context.Context, botID string, req *SplusSendMessageRequest) (*SplusResponse, error)
	UploadFile(ctx context.Context, botID string, path string) (*SplusUploadResponse, error)
	// FetchStatus returns delivery statuses for the given server-side message IDs.
	FetchStatus(ctx context.Context, messageIDs []string) ([]SplusStatusResponse, error)
	// SupportsStatusTracking reports whether the provider exposes a status API.
	SupportsStatusTracking() bool
}

// SplusSendMessageRequest is the payload for a single Splus send call.
type SplusSendMessageRequest struct {
	PhoneNumber string  `json:"phone_number,omitempty"`
	UserID      string  `json:"user_id,omitempty"`
	Text        string  `json:"text,omitempty"`
	FileID      *string `json:"file_id,omitempty"`
}

// SplusResponse is the provider's response to a send request.
type SplusResponse struct {
	ResultCode    int     `json:"result_code"`
	ResultMessage string  `json:"result_message"`
	MessageID     *string `json:"message_id,omitempty"`
	RequestID     *int64  `json:"request_id,omitempty"`
	UserID        *string `json:"user_id,omitempty"`
	HTTPStatus    int     `json:"-"`
}

// SplusUploadResponse is the provider's response to a file upload.
type SplusUploadResponse struct {
	ResultCode    int    `json:"result_code"`
	ResultMessage string `json:"result_message"`
	FileID        string `json:"file_id"`
	HTTPStatus    int    `json:"-"`
}

// SplusStatusResponse is the provider's status entry for a previously sent message.
type SplusStatusResponse struct {
	MessageID  string // server-assigned message ID used for correlation
	Status     int    // provider status code
	StatusText string // human-readable status description
}

const (
	splusUploadRetryBaseDelay = 1 * time.Second
	splusUploadRetryMaxDelay  = 30 * time.Second
	splusUploadMaxRetries     = 3 // 0 means unlimited retries until success or context cancellation.
)

func splusUploadRetryBackoffDelay(attempt int) time.Duration {
	return retryBackoffDelay(attempt, splusUploadRetryBaseDelay, splusUploadRetryMaxDelay)
}

// isRetryableSplusUploadError reports whether an upload error is transient and
// worth retrying (network failures and HTTP 429/5xx responses).
func isRetryableSplusUploadError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "http status: 500") ||
		strings.Contains(msg, "http status: 502") ||
		strings.Contains(msg, "http status: 503") ||
		strings.Contains(msg, "http status: 504") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "eof")
}

// httpSplusClient is the production HTTP implementation of SplusClient.
type httpSplusClient struct {
	cfg    config.SplusConfig
	client *http.Client
}

func newHTTPSplusClient(cfg config.SplusConfig) *httpSplusClient {
	return newHTTPSplusClientWithClient(cfg, newHTTPClient(60*time.Second))
}

func newHTTPSplusClientWithClient(cfg config.SplusConfig, client *http.Client) *httpSplusClient {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultSplusBaseURL
	}
	cfg.BaseURL = strings.TrimRight(baseURL, "/")
	if client == nil {
		client = newHTTPClient(60 * time.Second)
	}

	return &httpSplusClient{
		cfg:    cfg,
		client: client,
	}
}

func (c *httpSplusClient) SendMessage(ctx context.Context, botID string, reqBody *SplusSendMessageRequest) (*SplusResponse, error) {
	if strings.TrimSpace(botID) == "" {
		return nil, fmt.Errorf("splus bot id is not configured")
	}
	if reqBody == nil {
		return nil, fmt.Errorf("splus request body is nil")
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/messages/send", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", botID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var out SplusResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode splus send message response: %w", err)
		}
	}
	out.HTTPStatus = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.ResultCode != 0 || strings.TrimSpace(out.ResultMessage) != "" {
			return &out, fmt.Errorf(
				"splus send http status: %d result_code=%d result_message=%s",
				resp.StatusCode,
				out.ResultCode,
				strings.TrimSpace(out.ResultMessage),
			)
		}
		return nil, fmt.Errorf("splus send http status: %d body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return &out, nil
}

// UploadFile uploads a media file to Splus with exponential backoff retries on
// transient errors (network failures and HTTP 429/5xx). Each retry re-opens the
// file from disk so the multipart body is always fresh.
func (c *httpSplusClient) UploadFile(ctx context.Context, botID string, path string) (*SplusUploadResponse, error) {
	// Validate inputs once — these errors are permanent, no point retrying.
	if strings.TrimSpace(botID) == "" {
		return nil, fmt.Errorf("splus bot id is not configured")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("upload path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > splusMediaMaxSize {
		return nil, fmt.Errorf("file size exceeds splus upload limit (8MB)")
	}

	var resp *SplusUploadResponse
	for attempt := 0; ; attempt++ {
		resp, err = c.uploadFileOnce(ctx, botID, path)
		if !isRetryableSplusUploadError(err) {
			return resp, err
		}
		if splusUploadMaxRetries > 0 && attempt+1 >= splusUploadMaxRetries {
			break
		}
		if sleepErr := sleepWithContext(ctx, splusUploadRetryBackoffDelay(attempt)); sleepErr != nil {
			return resp, ctx.Err()
		}
	}
	return resp, err
}

// uploadFileOnce performs a single (non-retried) file upload attempt. It opens
// the file on every call so the multipart body is always fresh.
func (c *httpSplusClient) uploadFileOnce(ctx context.Context, botID string, path string) (*SplusUploadResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fileName := filepath.Base(path)
	fileContentType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))))
	if fileContentType == "" {
		fileContentType = "application/octet-stream"
	}
	if fileContentType == "image/jpg" {
		fileContentType = "image/jpeg"
	}

	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	partHeaders := textproto.MIMEHeader{}
	partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	partHeaders.Set("Content-Type", fileContentType)
	part, err := writer.CreatePart(partHeaders)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	formContentType := writer.FormDataContentType()
	if !strings.HasPrefix(strings.ToLower(formContentType), "multipart/form-data;") {
		return nil, fmt.Errorf("invalid multipart content type: %s", formContentType)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/file/upload", buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", botID)
	req.Header.Set("Content-Type", formContentType)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var out SplusUploadResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode splus upload response: %w", err)
		}
	}
	out.HTTPStatus = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.ResultCode != 0 || strings.TrimSpace(out.ResultMessage) != "" {
			return &out, fmt.Errorf(
				"splus upload http status: %d result_code=%d result_message=%s",
				resp.StatusCode,
				out.ResultCode,
				strings.TrimSpace(out.ResultMessage),
			)
		}
		return nil, fmt.Errorf("splus upload http status: %d body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if strings.TrimSpace(out.FileID) == "" {
		return nil, fmt.Errorf("splus upload returned empty file_id")
	}

	return &out, nil
}

// FetchStatus is a mock implementation. Replace with a real HTTP call once Splus exposes a status API.
func (c *httpSplusClient) FetchStatus(_ context.Context, _ []string) ([]SplusStatusResponse, error) {
	return nil, nil
}

// SupportsStatusTracking returns false until Splus provides a real status API.
func (c *httpSplusClient) SupportsStatusTracking() bool {
	return false
}
