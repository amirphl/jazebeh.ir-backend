package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

const (
	defaultBaleLegacyAPIDomain = "https://safir.bale.ai"
	defaultBaleNajvaAPIDomain  = "https://sms.najva.com"
)

const (
	baleProviderAuto    = "auto"
	baleProviderLegacy  = "safir_v3"
	baleProviderNajvaV2 = "najva_v2"
)

const (
	baleRetryBaseDelay   = 1 * time.Second
	baleRetryMaxDelay    = 15 * time.Minute
	baleRetryMaxAttempts = 0                // 0 means unlimited retries until success or context cancellation.
	najvaMaxRecipients   = 9000             // < 10000
	najvaMaxStatusIDs    = 900              // < 1000
	najvaMaxFileBytes    = 10 * 1024 * 1024 // < 15
)

var najvaAllowedUploadExt = map[string]struct{}{
	".jpeg": {},
	".jpg":  {},
	".png":  {},
	".gif":  {},
	".opus": {},
	".ogg":  {},
	".mp4":  {},
}

// Najva documented delivery statuses.
const (
	najvaStatusInQueue            = 1
	najvaStatusScheduled          = 2
	najvaStatusSentToOperator     = 4
	najvaStatusSendFailed         = 6
	najvaStatusDelivered          = 10
	najvaStatusDeliveryProblem    = 11
	najvaStatusCanceled           = 13
	najvaStatusRecipientBlocked   = 14
	najvaStatusInvalidMessageID   = 100
	najvaErrorInvalidInput        = 400
	najvaErrorPayloadTooLarge     = 413
	najvaErrorTooManyItems        = 414
	najvaErrorInsufficientBalance = 418
	najvaErrorTooManyRequests     = 429
	najvaErrorInternalServerError = 500
	najvaErrorBadGateway          = 502
	najvaErrorServiceUnavailable  = 503
	najvaErrorGatewayTimeout      = 504
)

var najvaStatusTextByCode = map[int]string{
	najvaStatusInQueue:          "in queue",
	najvaStatusScheduled:        "scheduled",
	najvaStatusSentToOperator:   "sent to operator",
	najvaStatusSendFailed:       "failed to send to recipient",
	najvaStatusDelivered:        "delivered",
	najvaStatusDeliveryProblem:  "delivery problem",
	najvaStatusCanceled:         "canceled",
	najvaStatusRecipientBlocked: "recipient blocked",
	najvaStatusInvalidMessageID: "invalid message id",
}

// BaleSendMessageRequest is kept stable to preserve scheduler compatibility.
type BaleSendMessageRequest struct {
	RequestID   string              `json:"request_id,omitempty"`
	BotID       int64               `json:"bot_id"`
	PhoneNumber string              `json:"phone_number"`
	MessageData BaleSendMessageData `json:"message_data"`
}

type BaleSendMessageData struct {
	Message  *BaleMessage `json:"message,omitempty"`
	OTP      *BaleOTP     `json:"otp_message,omitempty"`
	IsSecure *bool        `json:"is_secure,omitempty"`
}

type BaleMessage struct {
	Text     string  `json:"text,omitempty"`
	FileID   *string `json:"file_id,omitempty"`
	CopyText *string `json:"copy_text,omitempty"`
}

type BaleOTP struct {
	OTP string `json:"otp"`
}

type BaleErrorData struct {
	PhoneNumber string `json:"phone_number,omitempty"`
	CodeRaw     any    `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
}

func (e BaleErrorData) CodeString() string {
	switch v := e.CodeRaw.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// BaleSendMessageResponse is normalized across Safir and Najva.
type BaleSendMessageResponse struct {
	RequestID string          `json:"request_id,omitempty"`
	MessageID string          `json:"message_id"`
	ErrorData []BaleErrorData `json:"error_data"`
	RawBody   json.RawMessage `json:"-"`
	Provider  string          `json:"-"`
}

type BaleUploadFileResponse struct {
	FileID   string          `json:"file_id"`
	Error    any             `json:"error"`
	RawBody  json.RawMessage `json:"-"`
	Provider string          `json:"-"`
}

type BaleStatusResponse struct {
	MessageID  string          `json:"message_id"`
	Status     int             `json:"status"`
	StatusText string          `json:"status_text"`
	RawBody    json.RawMessage `json:"-"`
	Provider   string          `json:"-"`
}

// BaleStatusFetchResult keeps the provider payload alongside its parsed items.
// RawResponse is set whenever one or more HTTP response bodies were received,
// including non-2xx and undecodable responses.
type BaleStatusFetchResult struct {
	Items       []BaleStatusResponse
	RawResponse *string
}

type BaleClient interface {
	SendMessage(ctx context.Context, req *BaleSendMessageRequest) (*BaleSendMessageResponse, error)
	SendBatch(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error)
	UploadFile(ctx context.Context, path string) (*BaleUploadFileResponse, error)
	FetchStatus(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error)
	SupportsStatusTracking() bool
}

type httpBaleClient struct {
	cfg       config.BaleConfig
	client    *http.Client
	provider  string
	legacyURL string
	najvaURL  string
}

func newHTTPBaleClient(cfg config.BaleConfig) *httpBaleClient {
	return newHTTPBaleClientWithClient(cfg, newHTTPClient(60*time.Second))
}

func newHTTPBaleClientWithClient(cfg config.BaleConfig, client *http.Client) *httpBaleClient {
	if client == nil {
		client = newHTTPClient(60 * time.Second)
	}
	provider := normalizeBaleProvider(cfg.Provider)
	legacyURL := strings.TrimSpace(cfg.LegacyDomain)
	if legacyURL == "" {
		legacyURL = defaultBaleLegacyAPIDomain
	}
	najvaURL := strings.TrimSpace(cfg.NajvaDomain)
	if najvaURL == "" {
		najvaURL = defaultBaleNajvaAPIDomain
	}

	return &httpBaleClient{
		cfg:       cfg,
		provider:  provider,
		legacyURL: strings.TrimRight(legacyURL, "/"),
		najvaURL:  strings.TrimRight(najvaURL, "/"),
		client:    client,
	}
}

func normalizeBaleProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", baleProviderAuto:
		return baleProviderAuto
	case "legacy", "safir", "safir_v3", "v1":
		return baleProviderLegacy
	case "najva", "najva_v2", "v2":
		return baleProviderNajvaV2
	default:
		return baleProviderAuto
	}
}

func (c *httpBaleClient) SupportsStatusTracking() bool {
	return c.provider == baleProviderNajvaV2 || c.provider == baleProviderAuto
}

func (c *httpBaleClient) SendMessage(ctx context.Context, reqBody *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	if strings.TrimSpace(c.cfg.APIAccessKey) == "" {
		return nil, fmt.Errorf("bale api access key is not configured")
	}
	if reqBody == nil {
		return nil, fmt.Errorf("request body is nil")
	}

	resp, err := c.sendMessageWithRetry(ctx, reqBody)
	if resp != nil {
		resp.RequestID = strings.TrimSpace(reqBody.RequestID)
	}
	return resp, err
}

func (c *httpBaleClient) SendBatch(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	if strings.TrimSpace(c.cfg.APIAccessKey) == "" {
		return nil, fmt.Errorf("bale api access key is not configured")
	}
	if len(reqs) == 0 {
		return []BaleSendMessageResponse{}, nil
	}

	trimmedItems := make([]BaleSendMessageRequest, 0, len(reqs))
	for _, req := range reqs {
		if strings.TrimSpace(req.PhoneNumber) == "" {
			continue
		}
		trimmedItems = append(trimmedItems, req)
	}
	if len(trimmedItems) == 0 {
		return []BaleSendMessageResponse{}, nil
	}
	if c.provider != baleProviderLegacy && len(trimmedItems) > najvaMaxRecipients {
		return nil, fmt.Errorf("najva send supports at most %d recipients per request, got %d", najvaMaxRecipients, len(trimmedItems))
	}

	// Najva bulk endpoint supports sending one payload to many recipients.
	// When messages differ, fall back to one-by-one sends with shared retry logic.
	if c.provider != baleProviderLegacy && canSendNajvaBatch(trimmedItems) {
		responses, err := c.sendNajvaBatchWithRetry(ctx, trimmedItems)
		if err == nil {
			return responses, nil
		}
		if !isEndpointNotSupported(err) {
			return nil, err
		}
	}

	// Najva P2P endpoint supports different message text per recipient.
	if c.provider != baleProviderLegacy && canSendNajvaP2PBatch(trimmedItems) {
		responses, err := c.sendNajvaP2PBatchWithRetry(ctx, trimmedItems)
		if err == nil {
			return responses, nil
		}
		if !isEndpointNotSupported(err) {
			return nil, err
		}
	}

	out := make([]BaleSendMessageResponse, 0, len(trimmedItems))
	var firstErr error
	for i := range trimmedItems {
		resp, err := c.sendMessageWithRetry(ctx, &trimmedItems[i])
		if resp != nil {
			resp.RequestID = strings.TrimSpace(trimmedItems[i].RequestID)
			out = append(out, *resp)
			if firstErr == nil && err != nil {
				firstErr = err
			}
			continue
		}

		out = append(out, BaleSendMessageResponse{
			RequestID: strings.TrimSpace(trimmedItems[i].RequestID),
			Provider:  c.provider,
			ErrorData: []BaleErrorData{
				{
					PhoneNumber: strings.TrimSpace(trimmedItems[i].PhoneNumber),
					CodeRaw:     "SEND_FAILED",
					Description: firstNonEmpty(errString(err), "send failed"),
				},
			},
		})
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return out, firstErr
}

func (c *httpBaleClient) sendMessageWithRetry(ctx context.Context, reqBody *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	var (
		resp *BaleSendMessageResponse
		err  error
	)
	for attempt := 0; ; attempt++ {
		resp, err = c.sendMessageOnce(ctx, reqBody)
		if !isBaleRetryable(err, resp) {
			return resp, err
		}
		if baleRetryMaxAttempts > 0 && attempt+1 >= baleRetryMaxAttempts {
			break
		}
		if err := sleepWithContext(ctx, baleRetryBackoffDelay(attempt)); err != nil {
			return resp, ctx.Err()
		}
	}
	return resp, err
}

func (c *httpBaleClient) sendMessageOnce(ctx context.Context, reqBody *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	switch c.provider {
	case baleProviderLegacy:
		return c.sendSafir(ctx, reqBody)
	case baleProviderNajvaV2:
		return c.sendNajva(ctx, reqBody)
	default:
		resp, err := c.sendNajva(ctx, reqBody)
		if err == nil {
			return resp, nil
		}
		if isEndpointNotSupported(err) {
			return c.sendSafir(ctx, reqBody)
		}
		return nil, err
	}
}

func canSendNajvaBatch(reqs []BaleSendMessageRequest) bool {
	if len(reqs) < 2 {
		return false
	}
	if !isPositiveSenderID(reqs[0].BotID) {
		return false
	}
	firstText, firstFileID := extractBaleMessagePayload(&reqs[0])
	firstBotID := reqs[0].BotID
	firstText = strings.TrimSpace(firstText)
	if firstText == "" {
		return false
	}
	firstFileIDValue := ""
	if firstFileID != nil {
		firstFileIDValue = strings.TrimSpace(*firstFileID)
	}

	for i := 1; i < len(reqs); i++ {
		text, fileID := extractBaleMessagePayload(&reqs[i])
		if strings.TrimSpace(text) != firstText {
			return false
		}
		if reqs[i].BotID != firstBotID {
			return false
		}

		fileIDValue := ""
		if fileID != nil {
			fileIDValue = strings.TrimSpace(*fileID)
		}
		if fileIDValue != firstFileIDValue {
			return false
		}
	}
	return true
}

func canSendNajvaP2PBatch(reqs []BaleSendMessageRequest) bool {
	if len(reqs) < 2 || len(reqs) > najvaMaxRecipients {
		return false
	}

	firstBotID := reqs[0].BotID
	if !isPositiveSenderID(firstBotID) {
		return false
	}
	_, firstFileID := extractBaleMessagePayload(&reqs[0])
	firstFileIDValue := normalizeOptionalStringPtr(firstFileID)

	for i := 0; i < len(reqs); i++ {
		if strings.TrimSpace(reqs[i].PhoneNumber) == "" {
			return false
		}
		text, fileID := extractBaleMessagePayload(&reqs[i])
		if strings.TrimSpace(text) == "" {
			return false
		}
		if reqs[i].BotID != firstBotID {
			return false
		}
		if normalizeOptionalStringPtr(fileID) != firstFileIDValue {
			return false
		}
	}

	return true
}

func (c *httpBaleClient) sendNajvaBatchWithRetry(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	var (
		out []BaleSendMessageResponse
		err error
	)
	for attempt := 0; ; attempt++ {
		out, err = c.sendNajvaBatchOnce(ctx, reqs)
		if !isBaleBatchRetryable(err, out) {
			return out, err
		}
		if baleRetryMaxAttempts > 0 && attempt+1 >= baleRetryMaxAttempts {
			break
		}
		if err := sleepWithContext(ctx, baleRetryBackoffDelay(attempt)); err != nil {
			return out, ctx.Err()
		}
	}
	return out, err
}

func (c *httpBaleClient) sendNajvaP2PBatchWithRetry(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	var (
		out []BaleSendMessageResponse
		err error
	)
	for attempt := 0; ; attempt++ {
		out, err = c.sendNajvaP2PBatchOnce(ctx, reqs)
		if !isBaleBatchRetryable(err, out) {
			return out, err
		}
		if baleRetryMaxAttempts > 0 && attempt+1 >= baleRetryMaxAttempts {
			break
		}
		if err := sleepWithContext(ctx, baleRetryBackoffDelay(attempt)); err != nil {
			return out, ctx.Err()
		}
	}
	return out, err
}

func (c *httpBaleClient) sendNajvaBatchOnce(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	type najvaSendRequest struct {
		Receivers []string `json:"receivers"`
		Message   string   `json:"message"`
		Sender    string   `json:"sender,omitempty"`
		FileID    *string  `json:"file_id,omitempty"`
	}

	firstText, firstFileID := extractBaleMessagePayload(&reqs[0])
	payload := najvaSendRequest{
		Receivers: make([]string, 0, len(reqs)),
		Message:   strings.TrimSpace(firstText),
		Sender:    strconv.FormatInt(reqs[0].BotID, 10),
		FileID:    normalizeOptionalStringPtrRef(firstFileID),
	}
	for _, req := range reqs {
		payload.Receivers = append(payload.Receivers, normalizeNajvaPhoneNumber(req.PhoneNumber))
	}
	if err := validateNajvaBulkSendRequest(payload.Receivers, payload.Message, payload.Sender); err != nil {
		return nil, err
	}

	body, err := c.doJSONPost(ctx, c.najvaURL+"/v2/sms/send", payload, "najva send")
	if err != nil {
		return nil, err
	}

	items, err := decodeNajvaSendItems(body)
	if err != nil {
		return nil, fmt.Errorf("decode najva batch send response: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("najva batch send response is empty")
	}

	byReceptor := buildNajvaSendItemsByReceptor(items)

	out := make([]BaleSendMessageResponse, 0, len(reqs))
	for i := range reqs {
		resp := BaleSendMessageResponse{
			RequestID: strings.TrimSpace(reqs[i].RequestID),
			Provider:  baleProviderNajvaV2,
			RawBody:   body,
		}

		normalizedPhone := normalizeNajvaPhoneNumber(reqs[i].PhoneNumber)
		item, ok := popNajvaSendItemForReceptor(byReceptor, normalizedPhone)
		if !ok {
			item, ok = popNajvaSendItemForReceptor(byReceptor, strings.TrimSpace(reqs[i].PhoneNumber))
		}
		if !ok && i < len(items) {
			item = items[i]
			ok = true
		}
		if !ok {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     "INCOMPLETE_RESPONSE",
				Description: "najva response has fewer items than request",
			})
			out = append(out, resp)
			continue
		}

		messageID := normalizeAnyToString(item.MessageID)
		statusCode := normalizeAnyToInt(item.Status)
		resp.MessageID = messageID

		if messageID == "" {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     statusCode,
				Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode), "empty messageid in najva response"),
			})
			out = append(out, resp)
			continue
		}

		if isNajvaSendImmediateFailure(statusCode) {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     statusCode,
				Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode)),
			})
		}

		out = append(out, resp)
	}
	return out, nil
}

func (c *httpBaleClient) sendNajvaP2PBatchOnce(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	type najvaP2PMessage struct {
		Message  string `json:"message"`
		Receiver string `json:"receiver"`
	}

	type najvaSendP2PRequest struct {
		Messages []najvaP2PMessage `json:"messages"`
		Sender   string            `json:"sender,omitempty"`
		FileID   *string           `json:"file_id,omitempty"`
	}

	_, firstFileID := extractBaleMessagePayload(&reqs[0])
	payload := najvaSendP2PRequest{
		Messages: make([]najvaP2PMessage, 0, len(reqs)),
		Sender:   strconv.FormatInt(reqs[0].BotID, 10),
		FileID:   normalizeOptionalStringPtrRef(firstFileID),
	}
	receptors := make([]string, 0, len(reqs))
	messages := make([]string, 0, len(reqs))
	for _, req := range reqs {
		text, _ := extractBaleMessagePayload(&req)
		receiver := normalizeNajvaPhoneNumber(req.PhoneNumber)
		message := strings.TrimSpace(text)
		payload.Messages = append(payload.Messages, najvaP2PMessage{
			Message:  message,
			Receiver: receiver,
		})
		receptors = append(receptors, receiver)
		messages = append(messages, message)
	}
	if err := validateNajvaP2PSendRequest(receptors, messages, payload.Sender); err != nil {
		return nil, err
	}

	body, err := c.doJSONPost(ctx, c.najvaURL+"/v2/sms/send-p2p", payload, "najva send-p2p")
	if err != nil {
		return nil, err
	}

	items, err := decodeNajvaSendItems(body)
	if err != nil {
		return nil, fmt.Errorf("decode najva p2p send response: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("najva p2p send response is empty")
	}

	byReceptor := buildNajvaSendItemsByReceptor(items)

	out := make([]BaleSendMessageResponse, 0, len(reqs))
	for i := range reqs {
		resp := BaleSendMessageResponse{
			RequestID: strings.TrimSpace(reqs[i].RequestID),
			Provider:  baleProviderNajvaV2,
			RawBody:   body,
		}

		normalizedPhone := normalizeNajvaPhoneNumber(reqs[i].PhoneNumber)
		item, ok := popNajvaSendItemForReceptor(byReceptor, normalizedPhone)
		if !ok {
			item, ok = popNajvaSendItemForReceptor(byReceptor, strings.TrimSpace(reqs[i].PhoneNumber))
		}
		if !ok && i < len(items) {
			item = items[i]
			ok = true
		}
		if !ok {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     "INCOMPLETE_RESPONSE",
				Description: "najva response has fewer items than request",
			})
			out = append(out, resp)
			continue
		}
		messageID := normalizeAnyToString(item.MessageID)
		statusCode := normalizeAnyToInt(item.Status)
		resp.MessageID = messageID

		if messageID == "" {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     statusCode,
				Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode), "empty messageid in najva response"),
			})
			out = append(out, resp)
			continue
		}

		if isNajvaSendImmediateFailure(statusCode) {
			resp.ErrorData = append(resp.ErrorData, BaleErrorData{
				PhoneNumber: reqs[i].PhoneNumber,
				CodeRaw:     statusCode,
				Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode)),
			})
		}

		out = append(out, resp)
	}

	return out, nil
}

func (c *httpBaleClient) UploadFile(ctx context.Context, path string) (*BaleUploadFileResponse, error) {
	if strings.TrimSpace(c.cfg.APIAccessKey) == "" {
		return nil, fmt.Errorf("bale api access key is not configured")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("upload path is empty")
	}

	switch c.provider {
	case baleProviderLegacy:
		return c.uploadFileSafir(ctx, path)
	case baleProviderNajvaV2:
		return c.uploadFileNajva(ctx, path)
	default:
		resp, err := c.uploadFileNajva(ctx, path)
		if err == nil {
			return resp, nil
		}
		if isEndpointNotSupported(err) {
			return c.uploadFileSafir(ctx, path)
		}
		return nil, err
	}
}

func (c *httpBaleClient) FetchStatus(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error) {
	if strings.TrimSpace(c.cfg.APIAccessKey) == "" {
		return BaleStatusFetchResult{}, fmt.Errorf("bale api access key is not configured")
	}
	if len(messageIDs) == 0 {
		return BaleStatusFetchResult{}, nil
	}

	// Najva is currently the only provider with the documented status API in this codebase.
	if c.provider == baleProviderLegacy {
		return BaleStatusFetchResult{}, fmt.Errorf("status tracking is not supported for provider %s", baleProviderLegacy)
	}

	cleanIDs := make([]int64, 0, len(messageIDs))
	for _, id := range messageIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return BaleStatusFetchResult{}, fmt.Errorf("invalid message id %q: %w", trimmed, err)
		}
		cleanIDs = append(cleanIDs, parsed)
	}
	if len(cleanIDs) == 0 {
		return BaleStatusFetchResult{}, nil
	}

	out := BaleStatusFetchResult{Items: make([]BaleStatusResponse, 0, len(cleanIDs))}
	for start := 0; start < len(cleanIDs); start += najvaMaxStatusIDs {
		end := min(start+najvaMaxStatusIDs, len(cleanIDs))
		chunk := cleanIDs[start:end]

		chunkOut, err := c.fetchStatusChunkWithRetry(ctx, chunk)
		out.RawResponse = appendBaleRawResponse(out.RawResponse, chunkOut.RawResponse)
		if err != nil {
			if c.provider == baleProviderAuto && isEndpointNotSupported(err) {
				return out, fmt.Errorf("status tracking is not supported for provider %s", baleProviderLegacy)
			}
			return out, err
		}
		out.Items = append(out.Items, chunkOut.Items...)
	}

	return out, nil
}

func (c *httpBaleClient) fetchStatusChunkWithRetry(ctx context.Context, ids []int64) (BaleStatusFetchResult, error) {
	var (
		out BaleStatusFetchResult
		err error
	)
	for attempt := 0; ; attempt++ {
		out, err = c.fetchStatusChunkOnce(ctx, ids)
		if !isBaleStatusRetryable(err) {
			return out, err
		}
		if baleRetryMaxAttempts > 0 && attempt+1 >= baleRetryMaxAttempts {
			break
		}
		if err := sleepWithContext(ctx, baleRetryBackoffDelay(attempt)); err != nil {
			return out, ctx.Err()
		}
	}
	return out, err
}

func (c *httpBaleClient) fetchStatusChunkOnce(ctx context.Context, ids []int64) (BaleStatusFetchResult, error) {

	type najvaStatusRequest struct {
		MessageIDs []int64 `json:"messageids"`
	}
	payload := najvaStatusRequest{MessageIDs: ids}
	body, requestErr := c.doJSONPost(ctx, c.najvaURL+"/v2/sms/status", payload, "najva status")
	result := BaleStatusFetchResult{}
	if body != nil {
		rawResponse := string(body)
		result.RawResponse = &rawResponse
	}
	if requestErr != nil {
		return result, requestErr
	}

	items, err := decodeNajvaStatusItems(body)
	if err != nil {
		return result, fmt.Errorf("decode najva status response: %w", err)
	}

	out := make([]BaleStatusResponse, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		messageID := normalizeAnyToString(item.MessageID)
		if messageID == "" {
			continue
		}
		seen[messageID] = struct{}{}
		out = append(out, BaleStatusResponse{
			MessageID:  messageID,
			Status:     normalizeAnyToInt(item.Status),
			StatusText: firstNonEmpty(item.StatusText, najvaStatusText(normalizeAnyToInt(item.Status))),
			RawBody:    body,
			Provider:   baleProviderNajvaV2,
		})
	}
	for _, id := range ids {
		idStr := strconv.FormatInt(id, 10)
		if _, ok := seen[idStr]; ok {
			continue
		}
		// Defensive fallback for incomplete provider responses.
		out = append(out, BaleStatusResponse{
			MessageID:  idStr,
			Status:     100,
			StatusText: "message id not found in provider response",
			RawBody:    body,
			Provider:   baleProviderNajvaV2,
		})
	}
	result.Items = out
	return result, nil
}

func appendBaleRawResponse(current, next *string) *string {
	if next == nil {
		return current
	}
	if current == nil {
		value := *next
		return &value
	}
	value := *current + "\n" + *next
	return &value
}

func (c *httpBaleClient) sendSafir(ctx context.Context, reqBody *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	payload, _ := json.Marshal(reqBody)
	url := c.legacyURL + "/api/v3/send_message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	setBaleAuthHeaders(req, c.cfg.APIAccessKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newBaleHTTPError("safir send_message", resp.StatusCode, body)
	}

	var out BaleSendMessageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode safir send response: %w", err)
	}
	out.RawBody = body
	out.Provider = baleProviderLegacy
	return &out, nil
}

func (c *httpBaleClient) sendNajva(ctx context.Context, reqBody *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	type najvaSendRequest struct {
		Receivers []string `json:"receivers"`
		Message   string   `json:"message"`
		Sender    string   `json:"sender,omitempty"`
		FileID    *string  `json:"file_id,omitempty"`
	}

	text, fileID := extractBaleMessagePayload(reqBody)
	payload := najvaSendRequest{
		Receivers: []string{normalizeNajvaPhoneNumber(reqBody.PhoneNumber)},
		Message:   strings.TrimSpace(text),
		Sender:    strconv.FormatInt(reqBody.BotID, 10),
		FileID:    normalizeOptionalStringPtrRef(fileID),
	}
	if err := validateNajvaBulkSendRequest(payload.Receivers, payload.Message, payload.Sender); err != nil {
		return nil, err
	}

	body, err := c.doJSONPost(ctx, c.najvaURL+"/v2/sms/send", payload, "najva send")
	if err != nil {
		return nil, err
	}

	items, err := decodeNajvaSendItems(body)
	if err != nil {
		return nil, fmt.Errorf("decode najva send response: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("najva send response is empty")
	}

	item := items[0]
	messageID := normalizeAnyToString(item.MessageID)
	statusCode := normalizeAnyToInt(item.Status)
	out := &BaleSendMessageResponse{
		MessageID: messageID,
		RawBody:   body,
		Provider:  baleProviderNajvaV2,
	}

	if messageID == "" {
		out.ErrorData = append(out.ErrorData, BaleErrorData{
			PhoneNumber: reqBody.PhoneNumber,
			CodeRaw:     statusCode,
			Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode), "empty messageid in najva response"),
		})
		return out, nil
	}

	if isNajvaSendImmediateFailure(statusCode) {
		out.ErrorData = append(out.ErrorData, BaleErrorData{
			PhoneNumber: reqBody.PhoneNumber,
			CodeRaw:     statusCode,
			Description: firstNonEmpty(item.StatusText, najvaStatusText(statusCode)),
		})
	}

	return out, nil
}

func (c *httpBaleClient) uploadFileSafir(ctx context.Context, path string) (*BaleUploadFileResponse, error) {
	buf, contentType, err := createMultipartFilePayload(path)
	if err != nil {
		return nil, err
	}

	url := c.legacyURL + "/api/v3/upload_file"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, buf)
	if err != nil {
		return nil, err
	}
	setBaleAuthHeaders(req, c.cfg.APIAccessKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newBaleHTTPError("safir upload_file", resp.StatusCode, body)
	}

	var out BaleUploadFileResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode safir upload response: %w", err)
	}
	if strings.TrimSpace(out.FileID) == "" {
		return nil, fmt.Errorf("safir upload returned empty file_id")
	}
	out.RawBody = body
	out.Provider = baleProviderLegacy
	return &out, nil
}

func (c *httpBaleClient) uploadFileNajva(ctx context.Context, path string) (*BaleUploadFileResponse, error) {
	preparedPath, cleanup, err := prepareNajvaUploadPath(path)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := validateNajvaUploadFile(preparedPath); err != nil {
		return nil, err
	}

	buf, contentType, err := createMultipartFilePayload(preparedPath)
	if err != nil {
		return nil, err
	}

	url := c.najvaURL + "/upload-file/bale"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, buf)
	if err != nil {
		return nil, err
	}
	setBaleAuthHeaders(req, c.cfg.APIAccessKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newBaleHTTPError("najva upload_file", resp.StatusCode, body)
	}

	fileID, err := decodeNajvaUploadFileID(body)
	if err != nil {
		return nil, fmt.Errorf("decode najva upload response: %w", err)
	}
	if strings.TrimSpace(fileID) == "" {
		return nil, fmt.Errorf("najva upload returned empty file_id")
	}

	return &BaleUploadFileResponse{
		FileID:   strings.TrimSpace(fileID),
		RawBody:  body,
		Provider: baleProviderNajvaV2,
	}, nil
}

func prepareNajvaUploadPath(path string) (string, func(), error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", func() {}, fmt.Errorf("upload path is empty")
	}
	if strings.TrimSpace(filepath.Ext(trimmed)) != "" {
		return trimmed, func() {}, nil
	}

	inferredExt, err := inferNajvaUploadExtension(trimmed)
	if err != nil {
		return "", func() {}, err
	}

	src, err := os.Open(trimmed)
	if err != nil {
		return "", func() {}, err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp("", "najva-upload-*"+inferredExt)
	if err != nil {
		return "", func() {}, err
	}
	tmpPath := tmpFile.Name()
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, src); err != nil {
		_ = os.Remove(tmpPath)
		return "", func() {}, err
	}

	return tmpPath, func() { _ = os.Remove(tmpPath) }, nil
}

func inferNajvaUploadExtension(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	contentType := strings.ToLower(strings.TrimSpace(http.DetectContentType(header[:n])))
	switch {
	case strings.HasPrefix(contentType, "image/jpeg"):
		return ".jpg", nil
	case strings.HasPrefix(contentType, "image/png"):
		return ".png", nil
	case strings.HasPrefix(contentType, "image/gif"):
		return ".gif", nil
	case strings.HasPrefix(contentType, "video/mp4"):
		return ".mp4", nil
	case strings.HasPrefix(contentType, "audio/ogg"), strings.HasPrefix(contentType, "application/ogg"):
		return ".ogg", nil
	default:
		return "", fmt.Errorf("unsupported file extension %q; allowed extensions: jpeg, jpg, png, gif, opus, ogg, mp4", "")
	}
}

func (c *httpBaleClient) doJSONPost(ctx context.Context, url string, payload any, op string) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	setBaleAuthHeaders(req, c.cfg.APIAccessKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, newBaleHTTPError(op, resp.StatusCode, body)
	}
	return body, nil
}

func createMultipartFilePayload(path string) (*bytes.Buffer, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf, writer.FormDataContentType(), nil
}

func validateNajvaUploadFile(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("upload path is empty")
	}

	ext := strings.ToLower(filepath.Ext(trimmed))
	if _, ok := najvaAllowedUploadExt[ext]; !ok {
		return fmt.Errorf(
			"unsupported file extension %q; allowed extensions: jpeg, jpg, png, gif, opus, ogg, mp4",
			strings.TrimPrefix(ext, "."),
		)
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		return err
	}
	if info.Size() > najvaMaxFileBytes {
		return fmt.Errorf("file size exceeds %d MB", najvaMaxFileBytes/(1024*1024))
	}

	return nil
}

func setBaleAuthHeaders(req *http.Request, apiKey string) {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" || req == nil {
		return
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		trimmed = "Bearer " + trimmed
	}
	// Keep legacy key header and add common alternatives for proxy providers.
	req.Header.Set("api-access-key", trimmed)
	req.Header.Set("x-api-key", trimmed)
	req.Header.Set("apikey", trimmed)
	req.Header.Set("Authorization", trimmed)
}

func extractBaleMessagePayload(req *BaleSendMessageRequest) (string, *string) {
	if req == nil {
		return "", nil
	}
	if req.MessageData.Message != nil {
		text := strings.TrimSpace(req.MessageData.Message.Text)
		return text, req.MessageData.Message.FileID
	}
	if req.MessageData.OTP != nil {
		return strings.TrimSpace(req.MessageData.OTP.OTP), nil
	}
	return "", nil
}

func normalizeOptionalStringPtr(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func normalizeOptionalStringPtrRef(v *string) *string {
	trimmed := normalizeOptionalStringPtr(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isPositiveSenderID(id int64) bool {
	return id > 0
}

func validateNajvaBulkSendRequest(receptors []string, message string, sender string) error {
	if len(receptors) == 0 {
		return fmt.Errorf("at least one receptor is required")
	}
	if len(receptors) > najvaMaxRecipients {
		return fmt.Errorf("najva send supports at most %d recipients per request", najvaMaxRecipients)
	}
	for _, receptor := range receptors {
		if strings.TrimSpace(receptor) == "" {
			return fmt.Errorf("receptor contains an empty phone number")
		}
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is required")
	}
	if strings.TrimSpace(sender) == "" || strings.TrimSpace(sender) == "0" {
		return fmt.Errorf("sender is required and must be a positive Bale bot id")
	}
	return nil
}

func validateNajvaP2PSendRequest(receptors []string, messages []string, sender string) error {
	if len(receptors) != len(messages) {
		return fmt.Errorf("receptor/message length mismatch: %d/%d", len(receptors), len(messages))
	}
	if len(receptors) == 0 {
		return fmt.Errorf("at least one receptor is required")
	}
	if len(receptors) > najvaMaxRecipients {
		return fmt.Errorf("najva send-p2p supports at most %d recipients per request", najvaMaxRecipients)
	}
	if strings.TrimSpace(sender) == "" || strings.TrimSpace(sender) == "0" {
		return fmt.Errorf("sender is required and must be a positive Bale bot id")
	}
	for i := range receptors {
		if strings.TrimSpace(receptors[i]) == "" {
			return fmt.Errorf("receptor contains an empty phone number")
		}
		if strings.TrimSpace(messages[i]) == "" {
			return fmt.Errorf("message is required for receptor %s", strings.TrimSpace(receptors[i]))
		}
	}
	return nil
}

func normalizeNajvaPhoneNumber(raw string) string {
	phone := strings.TrimSpace(raw)
	if phone == "" {
		return ""
	}

	switch {
	case strings.HasPrefix(phone, "+98"):
		phone = strings.TrimPrefix(phone, "+98")
	case strings.HasPrefix(phone, "0098"):
		phone = strings.TrimPrefix(phone, "0098")
	case strings.HasPrefix(phone, "98"):
		phone = strings.TrimPrefix(phone, "98")
	}

	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}
	if !strings.HasPrefix(phone, "0") {
		phone = "0" + strings.TrimLeft(phone, "0")
	}
	return phone
}

func buildNajvaSendItemsByReceptor(items []najvaSendItem) map[string][]najvaSendItem {
	out := make(map[string][]najvaSendItem, len(items))
	for _, item := range items {
		receptor := firstNonEmpty(
			strings.TrimSpace(item.Receptor),
			strings.TrimSpace(item.Receiver),
		)
		if receptor == "" {
			continue
		}
		out[receptor] = append(out[receptor], item)
	}
	return out
}

func popNajvaSendItemForReceptor(byReceptor map[string][]najvaSendItem, receptor string) (najvaSendItem, bool) {
	if len(byReceptor) == 0 {
		return najvaSendItem{}, false
	}
	items, ok := byReceptor[receptor]
	if !ok || len(items) == 0 {
		return najvaSendItem{}, false
	}
	item := items[0]
	if len(items) == 1 {
		delete(byReceptor, receptor)
		return item, true
	}
	byReceptor[receptor] = items[1:]
	return item, true
}

type najvaSendItem struct {
	MessageID  any    `json:"messageid"`
	Message    string `json:"message"`
	Status     any    `json:"status"`
	StatusText string `json:"statustext"`
	Sender     string `json:"sender"`
	Receptor   string `json:"receptor"`
	Receiver   string `json:"receiver"`
	Date       any    `json:"date"`
	Cost       any    `json:"cost"`
}

type najvaStatusItem struct {
	MessageID  any    `json:"messageid"`
	Status     any    `json:"status"`
	StatusText string `json:"statustext"`
}

func decodeNajvaSendItems(body []byte) ([]najvaSendItem, error) {
	var many []najvaSendItem
	if err := json.Unmarshal(body, &many); err == nil {
		return many, nil
	}

	var envelope struct {
		Return  json.RawMessage `json:"return"`
		Entries json.RawMessage `json:"entries"`
		Items   []najvaSendItem `json:"items"`
		Data    []najvaSendItem `json:"data"`
		Result  []najvaSendItem `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		switch {
		case len(envelope.Entries) > 0:
			var entriesMany []najvaSendItem
			if err := json.Unmarshal(envelope.Entries, &entriesMany); err == nil && len(entriesMany) > 0 {
				return entriesMany, nil
			}
			var entryOne najvaSendItem
			if err := json.Unmarshal(envelope.Entries, &entryOne); err == nil {
				return []najvaSendItem{entryOne}, nil
			}
		case len(envelope.Items) > 0:
			return envelope.Items, nil
		case len(envelope.Data) > 0:
			return envelope.Data, nil
		case len(envelope.Result) > 0:
			return envelope.Result, nil
		}
	}

	var one najvaSendItem
	if err := json.Unmarshal(body, &one); err == nil {
		return []najvaSendItem{one}, nil
	}
	return nil, fmt.Errorf("unsupported najva send response format")
}

func decodeNajvaStatusItems(body []byte) ([]najvaStatusItem, error) {
	var many []najvaStatusItem
	if err := json.Unmarshal(body, &many); err == nil {
		return many, nil
	}

	var envelope struct {
		Return  json.RawMessage   `json:"return"`
		Entries json.RawMessage   `json:"entries"`
		Items   []najvaStatusItem `json:"items"`
		Data    []najvaStatusItem `json:"data"`
		Result  []najvaStatusItem `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		switch {
		case len(envelope.Entries) > 0:
			var entriesMany []najvaStatusItem
			if err := json.Unmarshal(envelope.Entries, &entriesMany); err == nil && len(entriesMany) > 0 {
				return entriesMany, nil
			}
			var entryOne najvaStatusItem
			if err := json.Unmarshal(envelope.Entries, &entryOne); err == nil {
				return []najvaStatusItem{entryOne}, nil
			}
		case len(envelope.Items) > 0:
			return envelope.Items, nil
		case len(envelope.Data) > 0:
			return envelope.Data, nil
		case len(envelope.Result) > 0:
			return envelope.Result, nil
		}
	}

	var one najvaStatusItem
	if err := json.Unmarshal(body, &one); err == nil {
		return []najvaStatusItem{one}, nil
	}
	return nil, fmt.Errorf("unsupported najva status response format")
}

func decodeNajvaUploadFileID(body []byte) (string, error) {
	var flat struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(body, &flat); err == nil {
		if trimmed := strings.TrimSpace(flat.FileID); trimmed != "" {
			return trimmed, nil
		}
	}

	var envelope struct {
		Return  json.RawMessage `json:"return"`
		Entries json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Entries) > 0 {
		var entry struct {
			FileID string `json:"file_id"`
			FildID string `json:"fild_id"`
		}
		if err := json.Unmarshal(envelope.Entries, &entry); err == nil {
			if id := strings.TrimSpace(firstNonEmpty(entry.FileID, entry.FildID)); id != "" {
				return id, nil
			}
		}

		var entries []struct {
			FileID string `json:"file_id"`
			FildID string `json:"fild_id"`
		}
		if err := json.Unmarshal(envelope.Entries, &entries); err == nil {
			for _, e := range entries {
				if id := strings.TrimSpace(firstNonEmpty(e.FileID, e.FildID)); id != "" {
					return id, nil
				}
			}
		}
	}

	return "", fmt.Errorf("unsupported najva upload response format")
}

func normalizeAnyToString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return strings.TrimSpace(t.String())
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		if t == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func normalizeAnyToInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return i
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	default:
		return 0
	}
}

func isNajvaSendImmediateFailure(code int) bool {
	switch code {
	case najvaStatusSendFailed, najvaStatusDeliveryProblem, najvaStatusCanceled, najvaStatusRecipientBlocked, najvaStatusInvalidMessageID:
		return true
	default:
		return false
	}
}

func najvaStatusText(code int) string {
	return strings.TrimSpace(najvaStatusTextByCode[code])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func baleRetryBackoffDelay(attempt int) time.Duration {
	return retryBackoffDelay(attempt, baleRetryBaseDelay, baleRetryMaxDelay)
}

func isBaleRetryable(err error, resp *BaleSendMessageResponse) bool {
	if isBaleRetryableError(err) {
		return true
	}
	return isBaleRetryableResponse(resp)
}

func isBaleBatchRetryable(err error, responses []BaleSendMessageResponse) bool {
	_ = responses
	return isBaleRetryableError(err)
}

func isBaleStatusRetryable(err error) bool {
	return isBaleRetryableError(err)
}

func isBaleRetryableError(err error) bool {
	var httpErr *baleHTTPError
	if errors.As(err, &httpErr) {
		if isRetryableHTTPStatus(httpErr.status) {
			return true
		}
	}

	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				return true
			}
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "429") ||
			strings.Contains(msg, "ratelimit") ||
			strings.Contains(msg, "rate limit") ||
			strings.Contains(msg, "timed out") ||
			strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "connection aborted") ||
			strings.Contains(msg, "temporary failure") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "eof") ||
			strings.Contains(msg, "502") ||
			strings.Contains(msg, "503") ||
			strings.Contains(msg, "504") {
			return true
		}
	}
	return false
}

func isBaleRetryableResponse(resp *BaleSendMessageResponse) bool {
	if resp == nil {
		return false
	}
	for _, e := range resp.ErrorData {
		code := strings.ToLower(strings.TrimSpace(e.CodeString()))
		desc := strings.ToLower(strings.TrimSpace(e.Description))
		if strings.Contains(code, "ratelimit") ||
			strings.Contains(code, "rate_limit") ||
			strings.Contains(code, "rate limit") ||
			strings.Contains(code, "timeout") ||
			strings.Contains(code, "tempor") ||
			strings.Contains(code, "unavailable") ||
			strings.Contains(code, "throttle") {
			return true
		}
		if strings.Contains(desc, "ratelimit") ||
			strings.Contains(desc, "rate limit") ||
			strings.Contains(desc, "timeout") ||
			strings.Contains(desc, "tempor") ||
			strings.Contains(desc, "unavailable") ||
			strings.Contains(desc, "connection") ||
			strings.Contains(desc, "throttle") {
			return true
		}
	}
	return false
}

type baleHTTPError struct {
	op     string
	status int
	body   string
}

func (e *baleHTTPError) Error() string {
	return fmt.Sprintf("%s http status: %d body: %s", e.op, e.status, e.body)
}

func newBaleHTTPError(op string, status int, body []byte) error {
	trimmedBody := strings.TrimSpace(string(body))
	if trimmedBody == "" {
		trimmedBody = najvaHTTPErrorDescription(op, status)
	}
	if len(trimmedBody) > 2048 {
		trimmedBody = trimmedBody[:2048]
	}
	return &baleHTTPError{op: op, status: status, body: trimmedBody}
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case najvaErrorTooManyRequests,
		najvaErrorInternalServerError,
		najvaErrorBadGateway,
		najvaErrorServiceUnavailable,
		najvaErrorGatewayTimeout:
		return true
	default:
		return false
	}
}

func najvaHTTPErrorDescription(op string, status int) string {
	op = strings.ToLower(strings.TrimSpace(op))
	switch {
	case strings.Contains(op, "najva send-p2p"), strings.Contains(op, "najva send"):
		switch status {
		case najvaErrorInvalidInput:
			return "invalid input parameters"
		case najvaErrorTooManyItems:
			return "recipient count exceeds maximum allowed (10000)"
		case najvaErrorInsufficientBalance:
			return "insufficient account balance"
		}
	case strings.Contains(op, "najva upload_file"):
		switch status {
		case najvaErrorInvalidInput:
			return "invalid file extension; allowed: jpeg, jpg, png, gif, opus, ogg, mp4"
		case najvaErrorPayloadTooLarge:
			return "file size exceeds 15MB"
		}
	case strings.Contains(op, "najva status"):
		switch status {
		case najvaErrorInvalidInput:
			return "invalid input parameters"
		case najvaErrorTooManyItems:
			return "message id count exceeds maximum allowed (1000)"
		}
	}
	return ""
}

func isEndpointNotSupported(err error) bool {
	var httpErr *baleHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	switch httpErr.status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}
