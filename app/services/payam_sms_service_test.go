package services

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/utils"
)

type payamSMSTestRoundTripper func(*http.Request) (*http.Response, error)

func (f payamSMSTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func payamSMSTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestPayamSMSSendRefreshesRejectedBearerToken(t *testing.T) {
	var tokenCalls, sendCalls int
	var sendAuthorizations []string
	service := &PayamSMSSMSService{
		smsConfig:   &config.SMSConfig{SourceNumber: "3000", Timeout: time.Second},
		payamConfig: &config.PayamSMSConfig{RootAccessToken: "root-token"},
		client: &http.Client{Transport: payamSMSTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/auth/oauth/token":
				tokenCalls++
				if got := req.Header.Get("Authorization"); got != "Basic root-token" {
					t.Fatalf("token authorization = %q, want root token", got)
				}
				if tokenCalls == 1 {
					return payamSMSTestResponse(http.StatusOK, `{"access_token":"rejected-token","expires_in":3600}`), nil
				}
				return payamSMSTestResponse(http.StatusOK, `{"access_token":"fresh-token","expires_in":3600}`), nil
			case "/panel/webservice/sendMultipleWithSrc":
				sendCalls++
				sendAuthorizations = append(sendAuthorizations, req.Header.Get("Authorization"))
				if req.Header.Get("Authorization") == "Bearer rejected-token" {
					return payamSMSTestResponse(http.StatusUnauthorized, "expired"), nil
				}
				return payamSMSTestResponse(http.StatusOK, `[]`), nil
			default:
				t.Fatalf("unexpected request URL %s", req.URL)
				return nil, nil
			}
		})},
	}

	if err := service.SendOTP(context.Background(), "989121234567", "code", nil); err != nil {
		t.Fatalf("SendOTP returned an error after bearer refresh: %v", err)
	}
	if tokenCalls != 2 {
		t.Fatalf("token calls = %d, want 2", tokenCalls)
	}
	if sendCalls != 2 {
		t.Fatalf("send calls = %d, want 2", sendCalls)
	}
	if got, want := strings.Join(sendAuthorizations, ","), "Bearer rejected-token,Bearer fresh-token"; got != want {
		t.Fatalf("send authorizations = %q, want %q", got, want)
	}
}

func TestPayamSMSTokenUsesProviderExpiry(t *testing.T) {
	service := &PayamSMSSMSService{
		smsConfig:   &config.SMSConfig{Timeout: time.Second},
		payamConfig: &config.PayamSMSConfig{},
		client: &http.Client{Transport: payamSMSTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			return payamSMSTestResponse(http.StatusOK, `{"access_token":"short-lived","expires_in":10}`), nil
		})},
	}

	if _, err := service.getToken(context.Background()); err != nil {
		t.Fatalf("getToken returned an error: %v", err)
	}
	remaining := service.tokenExpiry.Sub(utils.UTCNow())
	if remaining <= 8*time.Second || remaining > 9*time.Second {
		t.Fatalf("cached token lifetime = %s, want approximately 9s", remaining)
	}
}
