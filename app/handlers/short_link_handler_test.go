package handlers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/gofiber/fiber/v3"
)

type trackedShortLinkFlow struct {
	calls       int
	destination string
	err         error
}

func (f *trackedShortLinkFlow) Visit(_ context.Context, _ string, _ *string, _ *string) (string, error) {
	f.calls++
	return f.destination, f.err
}

func TestShortLinkResponsesTrackBotsAndHumans(t *testing.T) {
	for _, route := range []string{"/:uid", "/s/:uid", "/tst:uid", "/s/tst:uid"} {
		t.Run(route, func(t *testing.T) {
			flow := &trackedShortLinkFlow{destination: "https://example.com/landing?campaign=1"}
			app := fiber.New()
			app.Get(route, NewShortLinkHandler(flow).Visit)
			path := "/abc1"
			if route == "/s/:uid" {
				path = "/s/abc1"
			}
			if route == "/tst:uid" {
				path = "/tstabc1"
			}
			if route == "/s/tst:uid" {
				path = "/s/tstabc1"
			}
			for _, ua := range []string{"GoogleMessages", "Mozilla/5.0 (X11; Linux x86_64) Chrome/120", "curl/8.0", "Mozilla/5.0 (iPhone) Safari/604"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("User-Agent", ua)
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				bot := ua == "GoogleMessages" || ua == "curl/8.0"
				if bot {
					if res.StatusCode != 200 || len(body) != 0 || res.Header.Get("Location") != "" || res.Header.Get("Refresh") != "" {
						t.Fatalf("bot response: %d %v %q", res.StatusCode, res.Header, body)
					}
				} else if res.StatusCode != 302 || res.Header.Get("Location") != flow.destination {
					t.Fatalf("human response: %d %v", res.StatusCode, res.Header)
				}
				if res.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("missing no-store")
				}
			}
			if flow.calls != 4 {
				t.Fatalf("flow called %d times", flow.calls)
			}
		})
	}
}

func TestShortLinkGoogleIPsSuppressRedirects(t *testing.T) {
	for _, ip := range []string{"66.249.1.2", "74.125.1.2"} {
		flow := &trackedShortLinkFlow{destination: "https://example.com/landing"}
		app := fiber.New()
		// Preserve the production handler's c.IP() extraction; simulate the peer IP.
		app.Use(func(c fiber.Ctx) error {
			c.RequestCtx().SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP(ip), Port: 12345})
			return c.Next()
		})
		app.Get("/:uid", NewShortLinkHandler(flow).Visit)
		req := httptest.NewRequest(http.MethodGet, "/abc1", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Location") != "" || flow.calls != 1 {
			t.Fatalf("IP %s: status %d, calls %d", ip, res.StatusCode, flow.calls)
		}
	}
}

func TestShortLinkBotErrorsArePreserved(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{businessflow.ErrShortLinkNotFound, 404}, {errors.New("tracking unavailable"), 500}} {
		flow := &trackedShortLinkFlow{err: tc.err}
		app := fiber.New()
		app.Get("/:uid", NewShortLinkHandler(flow).Visit)
		req := httptest.NewRequest(http.MethodGet, "/abc1", nil)
		req.Header.Set("User-Agent", "Googlebot")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status || res.Header.Get("Location") != "" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("error response: %d %v", res.StatusCode, res.Header)
		}
	}
}

func TestShortLinkFollowingClientDoesNotReachDestinationForBots(t *testing.T) {
	var destinationHits atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationHits.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	flow := &trackedShortLinkFlow{destination: destination.URL}
	app := fiber.New()
	app.Get("/:uid", NewShortLinkHandler(flow).Visit)
	// Adapt Fiber's test transport to a real HTTP server so http.Client follows redirects.
	shortlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := app.Test(r)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer res.Body.Close()
		for key, values := range res.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	defer shortlink.Close()
	for index, ua := range []string{"GoogleMessages", "Mozilla/5.0 Chrome/120", "Twitterbot", "Mozilla/5.0 Safari/604"} {
		req, _ := http.NewRequest(http.MethodGet, shortlink.URL+"/abc1", nil)
		req.Header.Set("User-Agent", ua)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got, want := destinationHits.Load(), int64((index+1)/2); got != want {
			t.Fatalf("destination hits %d, want %d", got, want)
		}
	}
	if flow.calls != 4 {
		t.Fatalf("flow calls %d", flow.calls)
	}
}
