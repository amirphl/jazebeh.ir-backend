package services

import (
	"context"
	"testing"
	"time"
)

func TestExternalFactoryMocksEveryCryptoProvider(t *testing.T) {
	factory := ExternalFactory{Mock: true}
	for _, name := range []string{"oxapay"} {
		t.Run(name, func(t *testing.T) {
			provider, err := factory.Crypto(name, "https://example.invalid", "", time.Second)
			if err != nil || provider.Name() != name {
				t.Fatalf("provider selection: %v", err)
			}
			quote, err := provider.GetQuote(context.Background(), QuoteInput{FiatAmountToman: 123})
			if err != nil || quote.RateSource != "mock" {
				t.Fatalf("mock quote: %+v, %v", quote, err)
			}
		})
	}
}
