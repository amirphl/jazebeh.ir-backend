package router

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSwaggerUIUsesLocalAssetsAndOpenAPISpec(t *testing.T) {
	r := &FiberRouter{app: fiber.New()}
	r.setupMiddleware()
	r.app.Get("/swagger", r.serveSwaggerUI)
	resp, err := r.app.Test(httptest.NewRequest("GET", "/swagger", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, asset := range []string{"swagger-ui.css", "swagger-ui-bundle.js", "swagger-ui-standalone-preset.js"} {
		if !strings.Contains(html, "/swagger-ui-assets/"+asset) {
			t.Errorf("missing local asset %s", asset)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "docs", "swagger-ui-assets", asset)); err != nil {
			t.Errorf("missing checked-in asset %s: %v", asset, err)
		}
	}
	if strings.Contains(html, "unpkg.com") || !strings.Contains(html, "url: '/api/v1/swagger.json'") || !strings.Contains(html, "validatorUrl: null") {
		t.Fatal("Swagger should use the local specification without a remote validator or CDN")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("unexpected CSP: %q", csp)
	}
}
