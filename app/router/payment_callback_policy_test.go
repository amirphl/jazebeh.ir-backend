package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/handlers"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/gofiber/fiber/v3"
)

type callbackTokenService struct{ services.TokenService }

func (callbackTokenService) ValidateToken(token string) (*services.TokenClaims, error) {
	if token != "valid" {
		return nil, services.ErrTokenInvalid
	}
	return &services.TokenClaims{CustomerID: 42}, nil
}

type callbackHandler struct {
	handlers.PaymentHandlerInterface
}

func (callbackHandler) PaymentCallback(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) }

func TestPaymentCallbackAuthenticationPolicy(t *testing.T) {
	for _, tc := range []struct {
		mock  bool
		token string
		want  int
	}{
		{true, "", http.StatusUnauthorized},
		{true, "invalid", http.StatusUnauthorized},
		{true, "valid", http.StatusNoContent},
		{false, "", http.StatusNoContent},
	} {
		app := fiber.New()
		r := &FiberRouter{mockPayments: tc.mock, paymentHandler: callbackHandler{}, authMiddleware: middleware.NewAuthMiddleware(callbackTokenService{})}
		r.registerPaymentCallback(app.Group("/payments"))
		req := httptest.NewRequest(http.MethodPost, "/payments/callback/invoice", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("mock=%v token=%q: got %d want %d", tc.mock, tc.token, resp.StatusCode, tc.want)
		}
	}
}
