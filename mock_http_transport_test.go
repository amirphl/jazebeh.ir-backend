package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestMockHTTPTransportBlocksExternalAndAllowsLoopback(t *testing.T) {
	localDialed := false
	transport := mockHTTPTransportWithDial(http.DefaultTransport.(*http.Transport), func(_ context.Context, _, _ string) (net.Conn, error) {
		localDialed = true
		return nil, errors.New("test dial")
	}, false)
	_, _ = transport.DialContext(context.Background(), "tcp", "127.0.0.1:8080")
	if !localDialed {
		t.Fatal("local address should reach dialer")
	}
	localDialed = false
	_, err := transport.DialContext(context.Background(), "tcp", "example.com:80")
	if err == nil || !strings.Contains(err.Error(), "blocked in mock mode") {
		t.Fatalf("external request should be blocked, got %v", err)
	}
	if localDialed {
		t.Fatal("external address reached dialer")
	}
	_, err = transport.DialContext(context.Background(), "tcp", "staging.jazebeh.ir:443")
	if err == nil || !strings.Contains(err.Error(), "blocked in mock mode") {
		t.Fatalf("development must not contact staging: %v", err)
	}
}

func TestMockHTTPTransportDoesNotInheritCustomTLSDialers(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unguarded TLS dial")
	}
	transport := mockHTTPTransport(base, true)
	if transport.DialTLSContext != nil || transport.DialTLS != nil {
		t.Fatal("custom TLS dialers must not bypass the mock egress guard")
	}
}
