package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
)

// mockHTTPTransport prevents accidental HTTP calls to external services when
// running with mocks. Database and Redis connections do not use this transport.
// Container-network enforcement remains necessary for non-Go processes and
// clients that install their own transport.
func mockHTTPTransport(base *http.Transport, staging bool) *http.Transport {
	dialer := &net.Dialer{}
	return mockHTTPTransportWithDial(base, dialer.DialContext, staging)
}

func mockHTTPTransportWithDial(base *http.Transport, dial func(context.Context, string, string) (net.Conn, error), staging bool) *http.Transport {
	transport := base.Clone()
	transport.Proxy = nil
	// A custom TLS dialer bypasses DialContext, so never inherit one.
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		host = strings.ToLower(host)
		if host == "localhost" || host == "127.0.0.1" || host == "::1" ||
			(staging && (host == "app-beta" || host == "sentry-beta" || host == "staging.jazebeh.ir")) {
			return dial(ctx, network, address)
		}
		observability.RecordMockOperation("http", "egress", "blocked", 1)
		return nil, fmt.Errorf("external HTTP connection blocked in mock mode: %s", host)
	}
	return transport
}
