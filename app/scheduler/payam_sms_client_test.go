package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

func TestPayamSendBatchRetainsImmediateResponse(t *testing.T) {
	t.Parallel()

	const rawResponse = `[{"customerId":"tracking-1","mobile":"09120000001","serverId":"server-1"}]`
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL:        "https://www.payamsms.com/auth/oauth/token",
		RootAccessToken: "root-token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				return payamTestResponse(req, http.StatusOK, `{"access_token":"send-token"}`), nil
			}
			response := payamTestResponse(req, http.StatusAccepted, rawResponse)
			response.Header.Set("X-Request-ID", "payam-request-1")
			return response, nil
		}),
	})

	result, err := client.SendBatch(context.Background(), "sender", []PayamSMSItem{{
		Recipient:  "09120000001",
		Body:       "message",
		TrackingID: "tracking-1",
	}})
	if err != nil {
		t.Fatalf("SendBatch returned an error: %v", err)
	}
	if result.RawResponse == nil || *result.RawResponse != rawResponse {
		t.Fatalf("raw response mismatch: got=%v want=%q", result.RawResponse, rawResponse)
	}
	if result.HTTPStatusCode == nil || *result.HTTPStatusCode != http.StatusAccepted {
		t.Fatalf("status mismatch: got=%v want=%d", result.HTTPStatusCode, http.StatusAccepted)
	}
	if got := result.ResponseHeaders.Get("X-Request-ID"); got != "payam-request-1" {
		t.Fatalf("response header mismatch: got=%q", got)
	}
	if result.AttemptCount != 1 {
		t.Fatalf("attempt count mismatch: got=%d want=1", result.AttemptCount)
	}
	if len(result.Items) != 1 || result.Items[0].TrackingID != "tracking-1" {
		t.Fatalf("decoded items mismatch: %+v", result.Items)
	}
}

func TestPayamSendBatchRetainsNonSuccessResponse(t *testing.T) {
	t.Parallel()

	const rawResponse = `{"error":"invalid sender"}`
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL: "https://www.payamsms.com/auth/oauth/token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				return payamTestResponse(req, http.StatusOK, `{"access_token":"send-token"}`), nil
			}
			response := payamTestResponse(req, http.StatusBadRequest, rawResponse)
			response.Header.Set("X-Request-ID", "payam-request-error")
			return response, nil
		}),
	})

	result, err := client.SendBatch(context.Background(), "sender", []PayamSMSItem{{TrackingID: "tracking-1"}})
	if err == nil {
		t.Fatal("SendBatch unexpectedly succeeded")
	}
	if result.RawResponse == nil || *result.RawResponse != rawResponse {
		t.Fatalf("raw error response mismatch: got=%v want=%q", result.RawResponse, rawResponse)
	}
	if result.HTTPStatusCode == nil || *result.HTTPStatusCode != http.StatusBadRequest {
		t.Fatalf("status mismatch: got=%v want=%d", result.HTTPStatusCode, http.StatusBadRequest)
	}
	if got := result.ResponseHeaders.Get("X-Request-ID"); got != "payam-request-error" {
		t.Fatalf("response header mismatch: got=%q", got)
	}
}

func TestPayamSendBatchRetainsMalformedSuccessResponse(t *testing.T) {
	t.Parallel()

	const rawResponse = `{"customerId":`
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL: "https://www.payamsms.com/auth/oauth/token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				return payamTestResponse(req, http.StatusOK, `{"access_token":"send-token"}`), nil
			}
			response := payamTestResponse(req, http.StatusOK, rawResponse)
			response.Header.Set("X-Request-ID", "payam-malformed")
			return response, nil
		}),
	})

	result, err := client.SendBatch(context.Background(), "sender", []PayamSMSItem{{TrackingID: "tracking-1"}})
	if err == nil {
		t.Fatal("SendBatch unexpectedly decoded a malformed response")
	}
	if result.RawResponse == nil || *result.RawResponse != rawResponse {
		t.Fatalf("raw malformed response mismatch: got=%v want=%q", result.RawResponse, rawResponse)
	}
	if result.HTTPStatusCode == nil || *result.HTTPStatusCode != http.StatusOK {
		t.Fatalf("status mismatch: got=%v want=%d", result.HTTPStatusCode, http.StatusOK)
	}
	if got := result.ResponseHeaders.Get("X-Request-ID"); got != "payam-malformed" {
		t.Fatalf("response header mismatch: got=%q", got)
	}
}

func TestPayamSendBatchReportsTransportFailureWithoutHTTPResponse(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("broken transport")
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL: "https://www.payamsms.com/auth/oauth/token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				return payamTestResponse(req, http.StatusOK, `{"access_token":"send-token"}`), nil
			}
			return nil, transportErr
		}),
	})

	result, err := client.SendBatch(context.Background(), "sender", []PayamSMSItem{{TrackingID: "tracking-1"}})
	if err == nil {
		t.Fatal("SendBatch unexpectedly succeeded")
	}
	if result.RawResponse != nil || result.ResponseHeaders != nil || result.HTTPStatusCode != nil {
		t.Fatalf("transport failure unexpectedly had an HTTP response: %+v", result)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPayamFetchStatusRetainsRawEmptyResponse(t *testing.T) {
	t.Parallel()

	const rawResponse = "[\n]"
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(rawResponse)),
				Request:    req,
			}, nil
		}),
	})

	result, err := client.FetchStatus(context.Background(), "token", []string{"tracking-1"})
	if err != nil {
		t.Fatalf("FetchStatus returned an error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no parsed status items, got=%d", len(result.Items))
	}
	if result.RawResponse == nil || *result.RawResponse != rawResponse {
		t.Fatalf("raw response mismatch: got=%v want=%q", result.RawResponse, rawResponse)
	}
}

func TestPayamFetchStatusRefreshesUnauthorizedTokenAndRetainsIt(t *testing.T) {
	t.Parallel()

	var statusAuthorization []string
	tokenCalls := 0
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL:        "https://www.payamsms.com/auth/oauth/token",
		RootAccessToken: "root-token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				tokenCalls++
				if got := req.Header.Get("Authorization"); got != "Basic root-token" {
					t.Fatalf("token authorization mismatch: got=%q", got)
				}
				return payamTestResponse(req, http.StatusOK, `{"access_token":"refreshed-token","expires_in":3600}`), nil
			}

			statusAuthorization = append(statusAuthorization, req.Header.Get("Authorization"))
			if len(statusAuthorization) == 1 {
				return payamTestResponse(req, http.StatusUnauthorized, "expired"), nil
			}
			return payamTestResponse(req, http.StatusOK, `[{"customerId":"tracking-1","status":"Delivered"}]`), nil
		}),
	})
	client.statusUnauthorizedRetryDelay = func(int) time.Duration { return 0 }

	result, err := client.FetchStatus(context.Background(), "expired-token", []string{"tracking-1"})
	if err != nil {
		t.Fatalf("FetchStatus returned an error after token refresh: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].TrackingID != "tracking-1" {
		t.Fatalf("unexpected status result: %+v", result.Items)
	}

	// Even though the caller still has the rejected token, the refreshed token
	// is retained by the client and used by the next status job.
	if _, err := client.FetchStatus(context.Background(), "expired-token", []string{"tracking-2"}); err != nil {
		t.Fatalf("second FetchStatus returned an error: %v", err)
	}

	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls mismatch: got=%d want=1", tokenCalls)
	}
	wantAuthorization := []string{
		"Bearer expired-token",
		"Bearer refreshed-token",
		"Bearer refreshed-token",
	}
	if !reflect.DeepEqual(statusAuthorization, wantAuthorization) {
		t.Fatalf("status authorization mismatch: got=%v want=%v", statusAuthorization, wantAuthorization)
	}
}

func TestPayamFetchStatusStopsAfterThreeUnauthorizedAttempts(t *testing.T) {
	t.Parallel()

	statusCalls := 0
	tokenCalls := 0
	var retryAttempts []int
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL:        "https://www.payamsms.com/auth/oauth/token",
		RootAccessToken: "root-token",
	}, &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/auth/oauth/token") {
				tokenCalls++
				return payamTestResponse(req, http.StatusOK, fmt.Sprintf(`{"access_token":"refreshed-token-%d"}`, tokenCalls)), nil
			}

			statusCalls++
			return payamTestResponse(req, http.StatusUnauthorized, fmt.Sprintf("unauthorized-%d", statusCalls)), nil
		}),
	})
	client.statusUnauthorizedRetryDelay = func(attempt int) time.Duration {
		retryAttempts = append(retryAttempts, attempt)
		return 0
	}

	result, err := client.FetchStatus(context.Background(), "expired-token", []string{"tracking-1"})
	if err == nil {
		t.Fatal("FetchStatus unexpectedly succeeded")
	}
	if !isPayamUnauthorizedError(err) {
		t.Fatalf("expected an unauthorized error, got=%v", err)
	}
	if statusCalls != payamStatusUnauthorizedMaxAttempts {
		t.Fatalf("status calls mismatch: got=%d want=%d", statusCalls, payamStatusUnauthorizedMaxAttempts)
	}
	if tokenCalls != payamStatusUnauthorizedMaxAttempts-1 {
		t.Fatalf("token calls mismatch: got=%d want=%d", tokenCalls, payamStatusUnauthorizedMaxAttempts-1)
	}
	if !reflect.DeepEqual(retryAttempts, []int{0, 1}) {
		t.Fatalf("retry attempts mismatch: got=%v want=[0 1]", retryAttempts)
	}
	if result.RawResponse == nil || *result.RawResponse != "unauthorized-3" {
		t.Fatalf("final raw response mismatch: got=%v", result.RawResponse)
	}
}

func TestPayamFetchBalanceUsesRootTokenAndBearerToken(t *testing.T) {
	t.Parallel()

	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL:        "https://payam.example/auth/oauth/token",
		BalanceURL:      "https://payam.example/accounting/webservice/balance",
		RootAccessToken: "root-token",
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/auth/oauth/token":
			if got := req.Header.Get("Authorization"); got != "Basic root-token" {
				t.Fatalf("token authorization = %q, want root token", got)
			}
			return payamTestResponse(req, http.StatusOK, `{"access_token":"bearer-token"}`), nil
		case "/accounting/webservice/balance":
			if got := req.Header.Get("Authorization"); got != "Bearer bearer-token" {
				t.Fatalf("balance authorization = %q, want bearer token", got)
			}
			return payamTestResponse(req, http.StatusOK, "4366117164"), nil
		default:
			t.Fatalf("unexpected request path %q", req.URL.Path)
			return nil, nil
		}
	})})

	balance, err := client.FetchBalance(context.Background())
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if balance != 4_366_117_164 {
		t.Fatalf("FetchBalance() = %d, want 4366117164", balance)
	}
}

func TestPayamFetchBalanceRefreshesUnauthorizedTokenAndRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	tokenCalls, balanceCalls := 0, 0
	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{
		TokenURL:        "https://payam.example/auth/oauth/token",
		BalanceURL:      "https://payam.example/accounting/webservice/balance",
		RootAccessToken: "root-token",
	}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/auth/oauth/token" {
			tokenCalls++
			return payamTestResponse(req, http.StatusOK, fmt.Sprintf(`{"access_token":"token-%d"}`, tokenCalls)), nil
		}
		balanceCalls++
		switch balanceCalls {
		case 1:
			return payamTestResponse(req, http.StatusUnauthorized, "expired"), nil
		case 2:
			return payamTestResponse(req, http.StatusServiceUnavailable, "temporary"), nil
		default:
			return payamTestResponse(req, http.StatusOK, "100"), nil
		}
	})})
	client.balanceRetryDelay = func(int) time.Duration { return 0 }

	balance, err := client.FetchBalance(context.Background())
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if balance != 100 || tokenCalls != 2 || balanceCalls != 3 {
		t.Fatalf("balance=%d tokenCalls=%d balanceCalls=%d, want 100/2/3", balance, tokenCalls, balanceCalls)
	}
}

func TestPayamFetchBalanceRejectsMalformedResponse(t *testing.T) {
	t.Parallel()

	client := newHTTPPayamSMSClientWithClient(config.PayamSMSConfig{TokenURL: "https://payam.example/token", BalanceURL: "https://payam.example/balance"}, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/token" {
			return payamTestResponse(req, http.StatusOK, `{"access_token":"token"}`), nil
		}
		return payamTestResponse(req, http.StatusOK, `{"balance":100}`), nil
	})})
	if _, err := client.FetchBalance(context.Background()); err == nil {
		t.Fatal("FetchBalance() unexpectedly accepted malformed balance")
	}
}

func payamTestResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
