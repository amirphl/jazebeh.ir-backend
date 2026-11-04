package router

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

func TestErrorHandlerLogsCompleteRequestTimeoutURL(t *testing.T) {
	var output bytes.Buffer
	restoreLogOutput(t, &output)

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Get("/api/v1/campaigns", func(fiber.Ctx) error {
		return fiber.ErrRequestTimeout
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns?access_token=must-not-log", nil)
	req.Host = "api.example.test"
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app test: %v", err)
	}
	if resp.StatusCode != fiber.StatusRequestTimeout {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusRequestTimeout)
	}

	fields := decodeLogFields(t, output.Bytes())
	if fields["request_complete"] != true {
		t.Fatalf("request_complete = %v, want true", fields["request_complete"])
	}
	if fields["method"] != http.MethodGet {
		t.Fatalf("method = %v, want %s", fields["method"], http.MethodGet)
	}
	if fields["path"] != "/api/v1/campaigns" {
		t.Fatalf("path = %v", fields["path"])
	}
	if fields["url"] != "http://api.example.test/api/v1/campaigns" {
		t.Fatalf("url = %v", fields["url"])
	}
	if _, exists := fields["query"]; exists {
		t.Fatal("timeout log must not include query values")
	}
}

func TestLogRequestTimeoutMarksIncompleteRequestUnavailable(t *testing.T) {
	var output bytes.Buffer
	restoreLogOutput(t, &output)

	app := fiber.New()
	ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
	t.Cleanup(func() { app.ReleaseCtx(ctx) })

	logRequestTimeout(ctx, fiber.ErrRequestTimeout)

	fields := decodeLogFields(t, output.Bytes())
	if fields["request_complete"] != false {
		t.Fatalf("request_complete = %v, want false", fields["request_complete"])
	}
	for _, key := range []string{"method", "path", "host", "url"} {
		if fields[key] != "<unavailable>" {
			t.Fatalf("%s = %v, want <unavailable>", key, fields[key])
		}
	}
}

func restoreLogOutput(t *testing.T, output *bytes.Buffer) {
	t.Helper()
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
}

func decodeLogFields(t *testing.T, output []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &fields); err != nil {
		t.Fatalf("decode timeout log %q: %v", output, err)
	}
	return fields
}
