package scheduler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

func TestAsiaTechSendUsesV4P2PUDHAndPersistsProviderMetadata(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	paths := make([]string, 0, 3)
	provider := newAsiaTechSMSProviderWithClient(config.AsiaTechSMSConfig{Enabled: true, Username: "user", Password: "pass", Scope: "BulkApiAccess", TokenURL: "https://token.example/connect/token", BaseURL: "https://api.example", MaxBatchSize: 10, SendRequestsPerSecond: 1000, DLRRequestsPerSecond: 10, FallbackMPS: 1000}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		body := ""
		switch req.URL.Path {
		case "/connect/token":
			body = `{"access_token":"token","expires_in":300}`
		case "/api/user/userinfo":
			body = `{"succeeded":true,"resultCode":100,"data":{"mps":1000,"senderIds":["9890001234"]}}`
		case "/api/4/message/P2PBulk":
			payload, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(payload), `"udh":"trk-1"`) || strings.Contains(string(payload), "null") {
				t.Errorf("unexpected P2P payload: %s", payload)
			}
			body = `{"succeeded":true,"resultCode":100,"data":[{"id":"provider-1","part":"1","upstreamGateway":"SMTN"}]}`
		default:
			t.Errorf("unexpected endpoint %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	result, err := provider.SendBatch(context.Background(), "9890001234", []SMSProviderMessage{{TrackingID: "trk-1", Recipient: "09121234567", Body: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ProviderMessageID == nil || *result.Items[0].ProviderMessageID != "provider-1" {
		t.Fatalf("send result = %#v", result.Items)
	}
	if !strings.Contains(string(result.Items[0].Metadata), `"upstream_gateway":"SMTN"`) {
		t.Fatalf("metadata = %s", result.Items[0].Metadata)
	}
	if len(paths) != 3 {
		t.Fatalf("paths = %v", paths)
	}
}

func TestAsiaTechFinalizationAndChargebackRules(t *testing.T) {
	now := time.Now()
	if asiaTechDeliveryFinal(1, now) {
		t.Fatal("Delivered must be rechecked for 20 minutes")
	}
	if !asiaTechDeliveryFinal(1, now.Add(-21*time.Minute)) {
		t.Fatal("Delivered should be final after confirmation window")
	}
	if asiaTechDeliveryFinal(34, now.Add(-6*time.Hour)) || !asiaTechDeliveryFinal(34, now.Add(-7*time.Hour-time.Second)) {
		t.Fatal("Undeliverable seven-hour rule broken")
	}
	if !asiaTechChargebackEligible("mci", 34) || asiaTechChargebackEligible("other", 34) || !asiaTechChargebackEligible("other", 7) {
		t.Fatal("chargeback mapping broken")
	}
}
