package scheduler

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"strconv"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
)

func maybeMockSMSProvider(provider SMSProvider, enabled bool) SMSProvider {
	if !enabled {
		return provider
	}
	return &mockSMSProvider{SMSProvider: provider}
}

// Mock constructors are shared by campaign test sends and schedulers.
func NewMockCandooSMSProvider(provider SMSProvider) SMSProvider {
	return maybeMockSMSProvider(provider, true)
}
func NewMockPayamSMSClient() PayamSMSClient { return maybeMockPayamSMSClient(nil, true) }
func NewMockBaleClient() BaleClient         { return maybeMockBaleClient(nil, true) }
func NewMockRubikaClient() RubikaClient     { return maybeMockRubikaClient(nil, true) }
func NewMockSplusClient() SplusClient       { return maybeMockSplusClient(nil, true) }

type mockPayamBalanceClient struct{}

func (mockPayamBalanceClient) FetchBalance(context.Context) (int64, error) {
	log.Print("mock provider=payamsms action=balance")
	return 9223372036854775800, nil
}
func NewMockPayamBalanceClient() PayamBalanceClient { return mockPayamBalanceClient{} }

// mockSMSProvider keeps provider selection and durable scheduler paths intact
// while avoiding both Candoo send and status HTTP calls in mock mode.
type mockSMSProvider struct {
	SMSProvider
}

// Validate intentionally succeeds in mock mode so test/staging campaign runs
// do not require live Candoo credentials or a production status-code map.
func (c *mockSMSProvider) Validate() error { return nil }

func (c *mockSMSProvider) SendBatch(_ context.Context, _ string, items []SMSProviderMessage) (SMSProviderSendResult, error) {
	log.Printf("mock provider=%s action=send count=%d", c.Name(), len(items))
	observability.RecordMockOperation(string(c.Name()), "send", "accepted", len(items))
	responses := make([]SMSProviderSendItem, 0, len(items))
	for _, item := range items {
		messageID := nextMockMessageID(string(c.Name()))
		status := "ACCEPTED"
		code := "200"
		responses = append(responses, SMSProviderSendItem{
			TrackingID:          item.TrackingID,
			ProviderCustomerID:  item.ProviderCustomerID,
			ProviderMessageID:   &messageID,
			ProviderStatusCode:  &code,
			ProviderStatusText:  &status,
			InternalStatus:      models.SMSSendStatusPending,
			TrackDeliveryStatus: true,
		})
	}
	return SMSProviderSendResult{Items: responses}, nil
}

func (c *mockSMSProvider) FetchStatus(_ context.Context, messageIDs []string) (SMSProviderStatusResult, error) {
	log.Printf("mock provider=%s action=status count=%d", c.Name(), len(messageIDs))
	items := make([]SMSProviderStatusItem, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		customerID, err := strconv.ParseInt(messageID, 10, 64)
		if err != nil || customerID <= 0 {
			return SMSProviderStatusResult{}, fmt.Errorf("mock %s status: invalid customer ID", c.Name())
		}
		status := "DELIVERED"
		code := "200"
		internal := models.SMSSendStatusSuccessful
		delivered, undelivered := int64(1), int64(0)
		if mockDeliveryFailed(string(c.Name()), messageID) {
			status, code, internal = "FAILED", "500", models.SMSSendStatusUnsuccessful
			delivered, undelivered = 0, 1
		}
		observability.RecordMockOperation(string(c.Name()), "status", string(internal), 1)
		items = append(items, SMSProviderStatusItem{
			ProviderCustomerID: &customerID,
			ProviderMessageID:  messageID,
			ProviderStatusCode: &code,
			ProviderStatusText: &status,
			InternalStatus:     internal,
			TotalParts:         1,
			DeliveredParts:     delivered,
			UndeliveredParts:   undelivered,
		})
	}
	return SMSProviderStatusResult{Items: items}, nil
}

const mockSendDescription = "mocked successful send"

func nextMockMessageID(provider string) string {
	return fmt.Sprintf("mock-%s-%s", provider, uuid.NewString())
}

func mockDeliveryFailed(provider, id string) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(provider + "\x00" + id))
	return h.Sum64()%5 == 0
}

func maybeMockPayamSMSClient(client PayamSMSClient, enabled bool) PayamSMSClient {
	if !enabled {
		return client
	}
	return &mockPayamSMSClient{PayamSMSClient: client}
}

// mockPayamSMSClient handles all provider operations without network access.
type mockPayamSMSClient struct {
	PayamSMSClient
}

func (c *mockPayamSMSClient) SendBatch(_ context.Context, _ string, items []PayamSMSItem) (PayamSMSSendResult, error) {
	log.Printf("mock provider=payamsms action=send count=%d", len(items))
	observability.RecordMockOperation("payamsms", "send", "accepted", len(items))
	responses := make([]PayamSMSResponseItem, 0, len(items))
	for _, item := range items {
		serverID := nextMockMessageID("sms")
		description := mockSendDescription
		responses = append(responses, PayamSMSResponseItem{
			TrackingID: item.TrackingID,
			Mobile:     item.Recipient,
			ServerID:   &serverID,
			Desc:       &description,
		})
	}
	return PayamSMSSendResult{Items: responses}, nil
}

func (c *mockPayamSMSClient) GetToken(context.Context) (string, error) { return "mock-token", nil }

func (c *mockPayamSMSClient) FetchStatus(_ context.Context, _ string, ids []string) (PayamStatusFetchResult, error) {
	items := make([]PayamStatusResponse, 0, len(ids))
	for _, id := range ids {
		item := PayamStatusResponse{TrackingID: id, TotalParts: 1, TotalDeliveredParts: 1, Status: "DELIVERED"}
		if mockDeliveryFailed("payamsms", id) {
			item.TotalDeliveredParts, item.TotalUndeliveredParts, item.Status = 0, 1, "FAILED"
		}
		observability.RecordMockOperation("payamsms", "status", item.Status, 1)
		items = append(items, item)
	}
	log.Printf("mock provider=payamsms action=status count=%d", len(ids))
	return PayamStatusFetchResult{Items: items}, nil
}

func maybeMockBaleClient(client BaleClient, enabled bool) BaleClient {
	if !enabled {
		return client
	}
	return &mockBaleClient{BaleClient: client}
}

// mockBaleClient handles sends, uploads and status requests locally.
type mockBaleClient struct {
	BaleClient
}

func (c *mockBaleClient) SendMessage(_ context.Context, req *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	log.Printf("mock provider=bale action=send count=1")
	observability.RecordMockOperation("bale", "send", "accepted", 1)
	requestID := ""
	if req != nil {
		requestID = req.RequestID
	}
	return &BaleSendMessageResponse{
		RequestID: requestID,
		MessageID: nextMockMessageID("bale"),
		Provider:  "mock",
	}, nil
}

func (c *mockBaleClient) UploadFile(context.Context, string) (*BaleUploadFileResponse, error) {
	log.Printf("mock provider=bale action=upload")
	return &BaleUploadFileResponse{FileID: nextMockMessageID("bale-file"), Provider: "mock"}, nil
}
func (c *mockBaleClient) SupportsStatusTracking() bool { return true }
func (c *mockBaleClient) FetchStatus(_ context.Context, ids []string) (BaleStatusFetchResult, error) {
	items := make([]BaleStatusResponse, 0, len(ids))
	for _, id := range ids {
		status := najvaStatusDelivered
		if mockDeliveryFailed("bale", id) {
			status = najvaStatusDeliveryProblem
		}
		observability.RecordMockOperation("bale", "status", najvaStatusTextByCode[status], 1)
		items = append(items, BaleStatusResponse{MessageID: id, Status: status, StatusText: najvaStatusTextByCode[status], Provider: "mock"})
	}
	log.Printf("mock provider=bale action=status count=%d", len(ids))
	return BaleStatusFetchResult{Items: items}, nil
}

func (c *mockBaleClient) SendBatch(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	responses := make([]BaleSendMessageResponse, 0, len(reqs))
	for i := range reqs {
		response, err := c.SendMessage(ctx, &reqs[i])
		if err != nil {
			return responses, err
		}
		responses = append(responses, *response)
	}
	return responses, nil
}

func maybeMockRubikaClient(client RubikaClient, enabled bool) RubikaClient {
	if !enabled {
		return client
	}
	return &mockRubikaClient{RubikaClient: client}
}

// mockRubikaClient handles sends, uploads and status requests locally.
type mockRubikaClient struct {
	RubikaClient
}

func (c *mockRubikaClient) SendBulkMessages(
	_ context.Context,
	_ string,
	messages []RubikaMessagePayload,
) (*RubikaSendBulkMessagesResponse, error) {
	log.Printf("mock provider=rubika action=send count=%d", len(messages))
	observability.RecordMockOperation("rubika", "send", "accepted", len(messages))
	response := &RubikaSendBulkMessagesResponse{
		Status:     "OK",
		StatusDet:  mockSendDescription,
		HTTPStatus: 200,
	}
	response.Data.MessageStatusList = make([]RubikaMessageStatus, 0, len(messages))
	for _, message := range messages {
		response.Data.MessageStatusList = append(response.Data.MessageStatusList, RubikaMessageStatus{
			MessageID: nextMockMessageID("rubika"),
			Phone:     message.Phone,
			Status:    "sent",
			StatusDet: mockSendDescription,
		})
	}
	return response, nil
}

func (c *mockRubikaClient) UploadFile(context.Context, string) (*RubikaUploadFileResponse, error) {
	log.Printf("mock provider=rubika action=upload")
	result := &RubikaUploadFileResponse{Status: "OK", HTTPStatus: 200}
	result.Data.FileID = nextMockMessageID("rubika-file")
	return result, nil
}
func (c *mockRubikaClient) SupportsStatusTracking() bool { return true }
func (c *mockRubikaClient) FetchStatus(_ context.Context, ids []string) ([]RubikaStatusResponse, error) {
	items := make([]RubikaStatusResponse, 0, len(ids))
	for _, id := range ids {
		status := "delivered"
		if mockDeliveryFailed("rubika", id) {
			status = "failed"
		}
		observability.RecordMockOperation("rubika", "status", status, 1)
		items = append(items, RubikaStatusResponse{MessageID: id, Status: status})
	}
	log.Printf("mock provider=rubika action=status count=%d", len(ids))
	return items, nil
}

func maybeMockSplusClient(client SplusClient, enabled bool) SplusClient {
	if !enabled {
		return client
	}
	return &mockSplusClient{SplusClient: client}
}

// mockSplusClient handles sends, uploads and status requests locally.
type mockSplusClient struct {
	SplusClient
}

func (c *mockSplusClient) SendMessage(_ context.Context, _ string, req *SplusSendMessageRequest) (*SplusResponse, error) {
	log.Printf("mock provider=splus action=send count=1")
	observability.RecordMockOperation("splus", "send", "accepted", 1)
	messageID := nextMockMessageID("splus")
	response := &SplusResponse{
		ResultCode:    200,
		ResultMessage: mockSendDescription,
		MessageID:     &messageID,
		HTTPStatus:    200,
	}
	if req != nil && req.UserID != "" {
		userID := req.UserID
		response.UserID = &userID
	}
	return response, nil
}

func (c *mockSplusClient) UploadFile(context.Context, string, string) (*SplusUploadResponse, error) {
	log.Printf("mock provider=splus action=upload")
	return &SplusUploadResponse{ResultCode: 200, HTTPStatus: 200, FileID: nextMockMessageID("splus-file")}, nil
}
func (c *mockSplusClient) SupportsStatusTracking() bool { return true }
func (c *mockSplusClient) FetchStatus(_ context.Context, ids []string) ([]SplusStatusResponse, error) {
	items := make([]SplusStatusResponse, 0, len(ids))
	for _, id := range ids {
		code, status := 200, "delivered"
		if mockDeliveryFailed("splus", id) {
			code, status = 400, "failed"
		}
		observability.RecordMockOperation("splus", "status", status, 1)
		items = append(items, SplusStatusResponse{MessageID: id, Status: code, StatusText: status})
	}
	log.Printf("mock provider=splus action=status count=%d", len(ids))
	return items, nil
}
