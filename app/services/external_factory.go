package services

import (
	"fmt"
	"os"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

// ExternalFactory is the environment-aware entry point for outbound services.
// Callers must not use the live constructors directly when wiring application flows.
type ExternalFactory struct{ Mock bool }

func (f ExternalFactory) NotificationSMS(cfg *config.ProductionConfig) SMSService {
	if f.Mock || cfg.SMS.ProviderDomain == "mock" {
		return NewMockSMSService(f.Mock)
	}
	if cfg.SMS.ProviderDomain == "payamsms" {
		return NewPayamSMSService(&cfg.SMS, &cfg.PayamSMS)
	}
	return NewSMSService(&cfg.SMS)
}

func (f ExternalFactory) OTPSMS(cfg *config.ProductionConfig) (SMSService, error) {
	if f.Mock {
		path := os.Getenv("MOCK_OTP_OUTBOX")
		if path == "" {
			path = "data/mock-otp.jsonl"
		}
		return NewMockOTPService(path), nil
	}
	if cfg.SMS.ProviderDomain == "mock" {
		return NewMockSMSService(false), nil
	}
	return NewPayamSMSServiceWithHTTPSProxy(&cfg.SMS, &cfg.PayamSMS, cfg.IRHTTPSProxy)
}

func (f ExternalFactory) OpenAI(cfg config.SmartTagOpenAIConfig) (SmartTagOpenAIClient, error) {
	if f.Mock {
		cfg.Mock = true
	}
	return NewSmartTagOpenAIClient(cfg)
}

func (f ExternalFactory) Crypto(name, baseURL, apiKey string, timeout time.Duration) (CryptoPaymentProvider, error) {
	switch name {
	case "oxapay":
		if f.Mock {
			return &mockOxapayClient{provider: name, invoices: make(map[string]*mockOxapayInvoice)}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported crypto provider %q", name)
	}
	return NewOxapayClient(baseURL, apiKey, timeout), nil
}
