package scheduler

import (
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

// NewBotClient creates a new bot API client instance.
func NewBotClient(cfg config.BotConfig) BotClient {
	return newHTTPBotClient(cfg)
}

// NewPayamSMSClient creates a new PayamSMS client instance.
func NewPayamSMSClient(cfg config.PayamSMSConfig) PayamSMSClient {
	return newHTTPPayamSMSClient(cfg)
}

// NewPayamBalanceClient creates a direct Payam balance client.
func NewPayamBalanceClient(cfg config.PayamSMSConfig) PayamBalanceClient {
	return newHTTPPayamSMSClient(cfg)
}

// NewPayamSMSClientWithHTTPSProxy creates a new PayamSMS client that routes
// requests through the provided HTTPS proxy URL.
func NewPayamSMSClientWithHTTPSProxy(cfg config.PayamSMSConfig, proxyURL string) (PayamSMSClient, error) {
	client, err := newHTTPClientWithHTTPSProxy(60*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return newHTTPPayamSMSClientWithClient(cfg, client), nil
}

// NewPayamBalanceClientWithHTTPSProxy creates a Payam balance client using the
// same optional outbound proxy configuration as the other provider clients.
func NewPayamBalanceClientWithHTTPSProxy(cfg config.PayamSMSConfig, proxyURL string) (PayamBalanceClient, error) {
	client, err := newHTTPClientWithHTTPSProxy(60*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return newHTTPPayamSMSClientWithClient(cfg, client), nil
}

// NewBaleClient creates a new Bale client instance.
func NewBaleClient(cfg config.BaleConfig) BaleClient {
	return newHTTPBaleClient(cfg)
}

// NewBaleClientWithHTTPSProxy creates a new Bale client that routes requests
// through the provided HTTPS proxy URL.
func NewBaleClientWithHTTPSProxy(cfg config.BaleConfig, proxyURL string) (BaleClient, error) {
	client, err := newHTTPClientWithHTTPSProxy(60*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return newHTTPBaleClientWithClient(cfg, client), nil
}

// NewRubikaClient creates a new Rubika client instance.
func NewRubikaClient(cfg config.RubikaConfig) RubikaClient {
	return newHTTPRubikaClient(cfg)
}

// NewRubikaClientWithHTTPSProxy creates a new Rubika client that routes
// requests through the provided HTTPS proxy URL.
func NewRubikaClientWithHTTPSProxy(cfg config.RubikaConfig, proxyURL string) (RubikaClient, error) {
	client, err := newHTTPClientWithHTTPSProxy(60*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return newHTTPRubikaClientWithClient(cfg, client), nil
}

// NewSplusClient creates a new Splus client instance.
func NewSplusClient(cfg config.SplusConfig) SplusClient {
	return newHTTPSplusClient(cfg)
}

// NewSplusClientWithHTTPSProxy creates a new Splus client that routes requests
// through the provided HTTPS proxy URL.
func NewSplusClientWithHTTPSProxy(cfg config.SplusConfig, proxyURL string) (SplusClient, error) {
	client, err := newHTTPClientWithHTTPSProxy(60*time.Second, proxyURL)
	if err != nil {
		return nil, err
	}
	return newHTTPSplusClientWithClient(cfg, client), nil
}
