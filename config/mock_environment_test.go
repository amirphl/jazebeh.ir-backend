package config

import (
	"os"
	"strings"
	"testing"
)

func TestDevelopmentTemplateLoadsWithMissingProviderCredentials(t *testing.T) {
	data, err := os.ReadFile("development.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			t.Setenv(key, value)
		}
	}
	t.Setenv("DB_PASSWORD", "test-password")
	for _, key := range []string{"SMS_API_KEY", "ATIPAY_API_KEY", "PAYAM_SMS_ROOT_ACCESS_TOKEN", "BALE_API_ACCESS_KEY", "OPENAI_API_KEY", "EXTERNAL_SHORTLINK_API_TOKEN", "CANDOO_SMS_API_KEY"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadProductionConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MockExternal || !cfg.Scheduler.MessageSendMockEnabled || !cfg.ExternalShortLink.Mock || !cfg.SmartTagEvaluation.OpenAI.Mock || !cfg.Atipay.Mock {
		t.Fatal("mock integrations not selected")
	}
	t.Setenv("EXTERNAL_SERVICES_MODE", "live")
	if _, err := LoadProductionConfig(); err == nil {
		t.Fatal("development must reject live external services")
	}
}

func TestProductionRejectsMockExternalServices(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("EXTERNAL_SERVICES_MODE", "mock")

	_, err := LoadProductionConfig()
	if err == nil || !strings.Contains(err.Error(), "production requires EXTERNAL_SERVICES_MODE=live") {
		t.Fatalf("LoadProductionConfig() error = %v, want production mock-mode rejection", err)
	}
}

func TestStagingRequiresInternalGlitchTipDSN(t *testing.T) {
	data, err := os.ReadFile("staging.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			t.Setenv(key, value)
		}
	}
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("JWT_SECRET_KEY", "staging-test-secret-0123456789abcdef")
	t.Setenv("SMART_TAG_EVALUATION_OPENAI_MODEL", "mock")
	t.Setenv("SENTRY_DSN", "https://key@staging.jazebeh.ir/sentry/1")
	if _, err := LoadProductionConfig(); err == nil || !strings.Contains(err.Error(), "internal GlitchTip") {
		t.Fatalf("public staging DSN error = %v, want internal endpoint rejection", err)
	}
	t.Setenv("SENTRY_DSN", "http://key@sentry-beta:8000/sentry/1")
	if _, err := LoadProductionConfig(); err != nil {
		t.Fatalf("internal staging DSN error = %v", err)
	}
}
