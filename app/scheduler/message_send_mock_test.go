package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
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

func TestMockStatusClientsAreOfflineAndStable(t *testing.T) {
	ctx := context.Background()
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("message-%d", i)
	}
	payam := maybeMockPayamSMSClient(nil, true)
	if _, err := payam.GetToken(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := payam.FetchStatus(ctx, "", ids)
	if err != nil {
		t.Fatal(err)
	}
	again, err := payam.FetchStatus(ctx, "", ids)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("status must be stable")
	}
	failed := 0
	for _, item := range first.Items {
		if item.TotalUndeliveredParts == 1 {
			failed++
		}
	}
	if failed < 150 || failed > 250 {
		t.Fatalf("failure count %d outside expected range", failed)
	}
	bale := maybeMockBaleClient(nil, true)
	if !bale.SupportsStatusTracking() {
		t.Fatal("Bale mock does not support status")
	}
	if _, err := bale.UploadFile(ctx, "unused"); err != nil {
		t.Fatal(err)
	}
	if got, err := bale.FetchStatus(ctx, ids); err != nil || len(got.Items) != len(ids) {
		t.Fatal("Bale status failed", err)
	}
	rubika := maybeMockRubikaClient(nil, true)
	if !rubika.SupportsStatusTracking() {
		t.Fatal("Rubika mock does not support status")
	}
	if _, err := rubika.UploadFile(ctx, "unused"); err != nil {
		t.Fatal(err)
	}
	if got, err := rubika.FetchStatus(ctx, ids); err != nil || len(got) != len(ids) {
		t.Fatal("Rubika status failed", err)
	}
	splus := maybeMockSplusClient(nil, true)
	if !splus.SupportsStatusTracking() {
		t.Fatal("Splus mock does not support status")
	}
	if _, err := splus.UploadFile(ctx, "", "unused"); err != nil {
		t.Fatal(err)
	}
	if got, err := splus.FetchStatus(ctx, ids); err != nil || len(got) != len(ids) {
		t.Fatal("Splus status failed", err)
	}
}

func TestMockCandooStatusCorrelatesCustomerIDs(t *testing.T) {
	provider := maybeMockSMSProvider(NewCandooSMSProvider(config.CandooSMSConfig{}), true)
	ids := []string{"101", "102", "103"}
	result, err := provider.FetchStatus(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingExternalSMSStatusLookupIDs(models.SMSProviderCandoo, ids, result.Items); len(missing) != 0 {
		t.Fatalf("mock statuses missing customer IDs: %v", missing)
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
