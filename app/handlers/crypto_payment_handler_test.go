package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/gofiber/fiber/v3"
)

// cryptoWebhookFlow embeds the full flow interface so this focused route test
// only needs to implement the operation it invokes.
type cryptoWebhookFlow struct{ businessflow.CryptoPaymentFlow }

func (cryptoWebhookFlow) HandleOxapayWebhook(context.Context, []byte, string, string, *businessflow.ClientMetadata) error {
	return nil
}

func TestCryptoWebhookIsNotExposedInMockMode(t *testing.T) {
	app := fiber.New()
	handler := NewCryptoPaymentHandler(cryptoWebhookFlow{}, &config.ProductionConfig{
		MockExternal: true,
		Crypto:       config.CryptoConfig{Enabled: true},
	})
	app.Post("/callback", handler.Webhook)

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/callback", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
