package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

func testCandooStatusCodeMap() map[string]string {
	return map[string]string{
		"-1":  "pending",
		"100": "successful",
		"200": "unsuccessful",
	}
}

func TestCandooSendBatchUsesDocumentedRequestShapeAndCorrelatesByCustomerID(t *testing.T) {
	t.Parallel()

	var payload []candooSendRequest
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled:              true,
		APIKey:               "test-key",
		MessageType:          2,
		RetryCount:           4,
		ValidityPeriod:       300,
		MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts:      1,
		StatusCodeMap:        testCandooStatusCodeMap(),
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != candooSendPath {
			t.Fatalf("request = %s %s, want POST %s", req.Method, req.URL.Path, candooSendPath)
		}
		if got := req.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// Deliberately reverse the response to prove matching does not depend on
		// response order.
		return payamTestResponse(req, http.StatusOK, `[
			{"messageId":9002,"recipient":"989120000002","customerId":22,"status":"REJECTED","statusCode":-4},
			{"messageId":9001,"recipient":"989120000001","customerId":11,"status":"ACCEPTED","statusCode":200}
		]`), nil
	})})

	first, second := int64(11), int64(22)
	result, err := client.SendBatch(context.Background(), "02170007177", []SMSProviderMessage{
		{TrackingID: "trk-1", Recipient: "+989120000001", Body: "one", ProviderCustomerID: &first},
		{TrackingID: "trk-2", Recipient: "09120000002", Body: "two", ProviderCustomerID: &second},
	})
	if err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("payload length = %d, want 2", len(payload))
	}
	if payload[0].SrcNum != "02170007177" || payload[0].Recipient != "989120000001" {
		t.Fatalf("first normalized payload = %+v", payload[0])
	}
	if payload[0].Type != 2 || payload[0].RetryCount != 4 || payload[0].ValidityPeriod != 300 {
		t.Fatalf("first Candoo config payload = %+v", payload[0])
	}

	byTrackingID := make(map[string]SMSProviderSendItem, len(result.Items))
	for _, item := range result.Items {
		byTrackingID[item.TrackingID] = item
	}
	accepted := byTrackingID["trk-1"]
	if !accepted.TrackDeliveryStatus || accepted.ProviderMessageID == nil || *accepted.ProviderMessageID != "9001" || accepted.InternalStatus != models.SMSSendStatusPending {
		t.Fatalf("accepted result = %+v", accepted)
	}
	rejected := byTrackingID["trk-2"]
	if rejected.TrackDeliveryStatus || rejected.InternalStatus != models.SMSSendStatusUnsuccessful || rejected.ErrorCode == nil || *rejected.ErrorCode != "-4" {
		t.Fatalf("rejected result = %+v", rejected)
	}
}

func TestCandooSendBatchAcceptsArbitraryProvisionedSenderLine(t *testing.T) {
	t.Parallel()

	var payload []candooSendRequest
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled: true, APIKey: "test-key", MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts: 1, StatusCodeMap: testCandooStatusCodeMap(),
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return payamTestResponse(req, http.StatusOK, `[{"messageId":1,"customerId":1,"status":"ACCEPTED","statusCode":100}]`), nil
	})})

	customerID := int64(1)
	if _, err := client.SendBatch(context.Background(), "  50001234  ", []SMSProviderMessage{{
		TrackingID: "trk-short-code", Recipient: "989120000001", ProviderCustomerID: &customerID,
	}}); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	if len(payload) != 1 || payload[0].SrcNum != "50001234" {
		t.Fatalf("sender payload = %+v, want provisioned short code", payload)
	}
}

func TestCandooSendBatchRejectsOversizedBatchAndInvalidRecipientLocally(t *testing.T) {
	t.Parallel()
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled:              true,
		APIKey:               "test-key",
		MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts:      1,
		StatusCodeMap:        testCandooStatusCodeMap(),
	}, &http.Client{})

	overflow := make([]SMSProviderMessage, candooMaxBatchSize+1)
	if _, err := client.SendBatch(context.Background(), "982170007177", overflow); err == nil {
		t.Fatal("oversized batch unexpectedly succeeded")
	}

	customerID := int64(10)
	result, err := client.SendBatch(context.Background(), "982170007177", []SMSProviderMessage{{
		TrackingID:         "trk-invalid",
		Recipient:          "not-a-number",
		ProviderCustomerID: &customerID,
	}})
	if err != nil {
		t.Fatalf("invalid recipient should become a per-message failure: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].InternalStatus != models.SMSSendStatusUnsuccessful || result.Items[0].ErrorCode == nil || *result.Items[0].ErrorCode != "INVALID_RECIPIENT" {
		t.Fatalf("invalid recipient result = %+v", result.Items)
	}
}

func TestCandooSendBatchMarksDocumentedClientErrorAsDefiniteFailure(t *testing.T) {
	t.Parallel()
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled:              true,
		APIKey:               "test-key",
		MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts:      1,
		StatusCodeMap:        testCandooStatusCodeMap(),
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return payamTestResponse(req, http.StatusBadRequest, `{"message":"insufficient credit"}`), nil
	})})

	customerID := int64(10)
	result, err := client.SendBatch(context.Background(), "982170007177", []SMSProviderMessage{{
		TrackingID:         "trk-credit",
		Recipient:          "989120000001",
		Body:               "message",
		ProviderCustomerID: &customerID,
	}})
	if err == nil {
		t.Fatal("HTTP 400 unexpectedly succeeded")
	}
	if len(result.Items) != 1 {
		t.Fatalf("result item count = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.InternalStatus != models.SMSSendStatusUnsuccessful || item.ErrorCode == nil || *item.ErrorCode != "HTTP_400" {
		t.Fatalf("definite failure result = %+v", item)
	}
}

func TestCandooFetchStatusUsesCustomerIDsAndPreservesPendingStatus(t *testing.T) {
	t.Parallel()
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled:              true,
		APIKey:               "test-key",
		MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts:      1,
		StatusCodeMap:        testCandooStatusCodeMap(),
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != candooStatusPath {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		var ids []int64
		if err := json.NewDecoder(req.Body).Decode(&ids); err != nil {
			t.Fatalf("decode status request: %v", err)
		}
		if got := strings.TrimSpace(req.Header.Get("x-api-key")); got != "test-key" {
			t.Fatalf("status x-api-key = %q", got)
		}
		if len(ids) != 1 || ids[0] != 123243446 {
			t.Fatalf("status ids = %v", ids)
		}
		return payamTestResponse(req, http.StatusOK, `[{"messageId":0,"customerId":123243446,"status":-1}]`), nil
	})})

	result, err := client.FetchStatus(context.Background(), []string{"123243446"})
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("status item count = %d", len(result.Items))
	}
	item := result.Items[0]
	if item.ProviderCustomerID == nil || *item.ProviderCustomerID != 123243446 || item.ProviderMessageID != "0" || item.ProviderStatusCode == nil || *item.ProviderStatusCode != "-1" || item.InternalStatus != models.SMSSendStatusPending || item.UnknownParts != 1 {
		t.Fatalf("status item = %+v", item)
	}
}

func TestCandooFetchStatusMapsConfiguredTerminalStatuses(t *testing.T) {
	t.Parallel()
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{
		Enabled:              true,
		APIKey:               "test-key",
		MaxRequestsPerSecond: 1000,
		HTTPMaxAttempts:      1,
		StatusCodeMap:        testCandooStatusCodeMap(),
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return payamTestResponse(req, http.StatusOK, `[
			{"messageId":101,"customerId":1,"status":100},
			{"messageId":202,"customerId":2,"status":200}
		]`), nil
	})})

	result, err := client.FetchStatus(context.Background(), []string{"101", "202"})
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("status item count = %d, want 2", len(result.Items))
	}
	if got := result.Items[0]; got.InternalStatus != models.SMSSendStatusSuccessful || got.DeliveredParts != 1 || got.UnknownParts != 0 {
		t.Fatalf("successful Candoo mapping = %+v", got)
	}
	if got := result.Items[1]; got.InternalStatus != models.SMSSendStatusUnsuccessful || got.UndeliveredParts != 1 || got.UnknownParts != 0 {
		t.Fatalf("unsuccessful Candoo mapping = %+v", got)
	}
}

func TestCandooValidateRequiresStatusCodeMap(t *testing.T) {
	t.Parallel()
	client := newCandooSMSProviderWithClient(config.CandooSMSConfig{Enabled: true, APIKey: "test-key"}, &http.Client{})
	if err := client.Validate(); err == nil || !strings.Contains(err.Error(), "STATUS_MAP") {
		t.Fatalf("Validate() error = %v, want missing status-map error", err)
	}
}

func TestMissingExternalSMSStatusLookupIDsUsesCandooCustomerIDs(t *testing.T) {
	t.Parallel()
	first, third, extra := int64(1), int64(3), int64(99)
	missing := missingExternalSMSStatusLookupIDs(models.SMSProviderCandoo, []string{"1", "2", "3"}, []SMSProviderStatusItem{
		{ProviderCustomerID: &first, ProviderMessageID: "101"},
		{ProviderCustomerID: &third, ProviderMessageID: "303"},
		{ProviderCustomerID: &extra, ProviderMessageID: "2"},
	})
	if len(missing) != 1 || missing[0] != "2" {
		t.Fatalf("missing status IDs = %v, want [2]", missing)
	}
}

func TestExternalSMSStatusRequestIDUsesCandooCustomerID(t *testing.T) {
	t.Parallel()
	customerID := int64(123243445)
	row := &models.SentSMS{ProviderCustomerID: &customerID}

	got, ok := externalSMSStatusRequestID(models.SMSProviderCandoo, row)
	if !ok || got != "123243445" {
		t.Fatalf("Candoo status lookup ID = %q, %v; want customer ID", got, ok)
	}
}

func TestCandooRetryPolicyOnlyRetriesExplicitThrottle(t *testing.T) {
	t.Parallel()
	tooManyRequests := &candooHTTPStatusError{statusCode: http.StatusTooManyRequests}
	if !isCandooRetryableError(tooManyRequests) {
		t.Fatal("429 must be retryable")
	}
	if isCandooRetryableError(&candooHTTPStatusError{statusCode: http.StatusBadGateway}) {
		t.Fatal("ambiguous 5xx response must not be retried automatically")
	}
}
