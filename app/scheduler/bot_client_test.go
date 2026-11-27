package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

func TestNewHTTPBotClientUsesDedicatedAllocationTimeout(t *testing.T) {
	const allocationTimeout = 47 * time.Minute
	client := newHTTPBotClient(config.BotConfig{ShortLinkAllocationTimeout: allocationTimeout})
	if got := client.shortLinkAllocClient.Timeout; got != allocationTimeout {
		t.Fatalf("allocation timeout = %s, want %s", got, allocationTimeout)
	}
	if got := client.client.Timeout; got != 30*time.Second {
		t.Fatalf("ordinary bot timeout = %s, want 30s", got)
	}
}

func TestBotClientLoginSharesValidTokenAcrossConcurrentCalls(t *testing.T) {
	var mu sync.Mutex
	loginCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bot/auth/login" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		mu.Lock()
		loginCalls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"session":{"access_token":"cached-token","expires_in":3600}}}`))
	}))
	defer srv.Close()

	client := newHTTPBotClient(config.BotConfig{APIDomain: srv.URL, Username: "scheduler", Password: "password"})
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := client.Login(context.Background())
			if err == nil && token != "cached-token" {
				err = fmt.Errorf("token = %q, want cached-token", token)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Login returned an error: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 1 {
		t.Fatalf("login requests = %d, want 1", loginCalls)
	}
}

func TestDownloadCampaignMediaKeepsHeaderExtension(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bot/media/m1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="media.png"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer srv.Close()

	client := newHTTPBotClient(config.BotConfig{APIDomain: srv.URL})
	path, err := client.DownloadCampaignMedia(context.Background(), "t", "m1")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer os.Remove(path)

	if ext := strings.ToLower(filepath.Ext(path)); ext != ".png" {
		t.Fatalf("expected .png extension, got=%q path=%q", ext, path)
	}
}

func TestDownloadCampaignMediaInfersExtensionFromContent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bot/media/m2" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		// PNG signature should infer image/png.
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	}))
	defer srv.Close()

	client := newHTTPBotClient(config.BotConfig{APIDomain: srv.URL})
	path, err := client.DownloadCampaignMedia(context.Background(), "t", "m2")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer os.Remove(path)

	if ext := strings.ToLower(filepath.Ext(path)); ext != ".png" {
		t.Fatalf("expected inferred .png extension, got=%q path=%q", ext, path)
	}
}
