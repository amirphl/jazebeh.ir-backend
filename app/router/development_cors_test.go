package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestDevelopmentCORSPreflight(t *testing.T) {
	r := &FiberRouter{app: fiber.New(), developmentOrigins: developmentCORSOrigins("development", []string{"http://localhost:8081", "http://127.0.0.1:8081"})}
	r.setupMiddleware()
	r.app.Post("/api/v1/example", func(c fiber.Ctx) error { return c.SendStatus(200) })
	for _, tc := range []struct {
		origin  string
		allowed bool
	}{
		{"http://localhost:8081", true}, {"http://127.0.0.1:8081", true}, {"http://localhost:3000", false}, {"https://example.com", false},
	} {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/example", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		resp, err := r.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); (got == tc.origin) != tc.allowed {
			t.Errorf("origin %s: allow origin %q", tc.origin, got)
		}
	}
	if developmentCORSOrigins("production", []string{"http://localhost:8081"}) != nil {
		t.Fatal("production CORS must remain unchanged")
	}
}
