package scheduler

import (
	"context"
	"strings"
	"testing"
)

func TestMockPayamSMSClientReturnsSuccessfulResponsePerItem(t *testing.T) {
	client := maybeMockPayamSMSClient(nil, true)
	result, err := client.SendBatch(context.Background(), "sender", []PayamSMSItem{
		{Recipient: "09120000001", TrackingID: "101"},
		{Recipient: "09120000002", TrackingID: "102"},
	})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	responses := result.Items
	if len(responses) != 2 {
		t.Fatalf("len(responses) = %d, want 2", len(responses))
	}
	for i, response := range responses {
		if response.TrackingID == "" || response.ServerID == nil || !strings.HasPrefix(*response.ServerID, "mock-sms-") {
			t.Fatalf("response[%d] = %+v, want successful mock response", i, response)
		}
	}
}

func TestMockBaleClientReturnsSuccessfulSingleAndBatchResponses(t *testing.T) {
	client := maybeMockBaleClient(nil, true)
	single, err := client.SendMessage(context.Background(), &BaleSendMessageRequest{RequestID: "201"})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if single.RequestID != "201" || !strings.HasPrefix(single.MessageID, "mock-bale-") || len(single.ErrorData) != 0 {
		t.Fatalf("SendMessage() = %+v, want successful mock response", single)
	}

	batch, err := client.SendBatch(context.Background(), []BaleSendMessageRequest{{RequestID: "202"}, {RequestID: "203"}})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	if len(batch) != 2 || batch[0].RequestID != "202" || batch[1].RequestID != "203" {
		t.Fatalf("SendBatch() = %+v, want responses correlated by request ID", batch)
	}
}

func TestMockRubikaClientReturnsSuccessfulResponsePerMessage(t *testing.T) {
	client := maybeMockRubikaClient(nil, true)
	response, err := client.SendBulkMessages(context.Background(), "service", []RubikaMessagePayload{
		{Phone: "09120000001"},
		{Phone: "09120000002"},
	})
	if err != nil {
		t.Fatalf("SendBulkMessages() error = %v", err)
	}
	if response.HTTPStatus != 200 || len(response.Data.MessageStatusList) != 2 {
		t.Fatalf("SendBulkMessages() = %+v, want successful mock response", response)
	}
	for i, status := range response.Data.MessageStatusList {
		if !rubikaMessageStatusSuccessful(status) || !strings.HasPrefix(status.MessageID, "mock-rubika-") {
			t.Fatalf("status[%d] = %+v, want successful mock status", i, status)
		}
	}
}

func TestMockSplusClientReturnsSuccessfulResponse(t *testing.T) {
	client := maybeMockSplusClient(nil, true)
	response, err := client.SendMessage(context.Background(), "bot", &SplusSendMessageRequest{UserID: "user-1"})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if response.ResultCode != 200 || response.HTTPStatus != 200 || response.MessageID == nil ||
		!strings.HasPrefix(*response.MessageID, "mock-splus-") {
		t.Fatalf("SendMessage() = %+v, want successful mock response", response)
	}
	if response.UserID == nil || *response.UserID != "user-1" {
		t.Fatalf("SendMessage() user ID = %v, want user-1", response.UserID)
	}
}

func TestMessageSendMockDisabledKeepsOriginalClient(t *testing.T) {
	var original PayamSMSClient = &mockPayamSMSClient{}
	if got := maybeMockPayamSMSClient(original, false); got != original {
		t.Fatal("disabled mock replaced the original client")
	}
}
