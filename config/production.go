// Package config provides configuration management and environment variable handling for the application
package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

// ProductionConfig holds all configuration for production environment
type ProductionConfig struct {
	Database           DatabaseConfig           `json:"database"`
	Server             ServerConfig             `json:"server"`
	Security           SecurityConfig           `json:"security"`
	JWT                JWTConfig                `json:"jwt"`
	Sentry             SentryConfig             `json:"sentry"`
	SMS                SMSConfig                `json:"sms"`
	Email              EmailConfig              `json:"email"`
	Logging            LoggingConfig            `json:"logging"`
	Metrics            MetricsConfig            `json:"metrics"`
	Cache              CacheConfig              `json:"cache"`
	Deployment         DeploymentConfig         `json:"deployment"`
	Atipay             AtipayConfig             `json:"atipay"`
	Admin              AdminConfig              `json:"admin"`
	System             SystemConfig             `json:"system"`
	PayamSMS           PayamSMSConfig           `json:"payam_sms"`
	CandooSMS          CandooSMSConfig          `json:"candoo_sms"`
	Bale               BaleConfig               `json:"bale"`
	Rubika             RubikaConfig             `json:"rubika"`
	Splus              SplusConfig              `json:"splus"`
	Bot                BotConfig                `json:"bot"`
	Scheduler          SchedulerConfig          `json:"scheduler"`
	ExternalShortLink  ExternalShortLinkConfig  `json:"external_short_link"`
	Crypto             CryptoConfig             `json:"crypto"`
	Message            MessageConfig            `json:"message"`
	SmartTagEvaluation SmartTagEvaluationConfig `json:"smart_tag_evaluation"`
	IRHTTPSProxy       string                   `json:"ir_https_proxy"`
}

type DatabaseConfig struct {
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	Name            string        `json:"name"`
	User            string        `json:"user"`
	Password        string        `json:"password"`
	SSLMode         string        `json:"ssl_mode"`
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `json:"conn_max_idle_time"`
	SlowQueryLog    bool          `json:"slow_query_log"`
	SlowQueryTime   time.Duration `json:"slow_query_time"`
}

type ServerConfig struct {
	Host              string        `json:"host"`
	Port              int           `json:"port"`
	ReadTimeout       time.Duration `json:"read_timeout"`
	WriteTimeout      time.Duration `json:"write_timeout"`
	IdleTimeout       time.Duration `json:"idle_timeout"`
	ShutdownTimeout   time.Duration `json:"shutdown_timeout"`
	BodyLimit         int           `json:"body_limit"`
	EnablePprof       bool          `json:"enable_pprof"`
	EnableMetrics     bool          `json:"enable_metrics"`
	TrustedProxies    []string      `json:"trusted_proxies"`
	ProxyHeader       string        `json:"proxy_header"`
	EnableCompression bool          `json:"enable_compression"`
	CompressionLevel  int           `json:"compression_level"`
}

type SecurityConfig struct {
	// TLS/HTTPS
	TLSEnabled         bool   `json:"tls_enabled"`
	TLSCertFile        string `json:"tls_cert_file"`
	TLSKeyFile         string `json:"tls_key_file"`
	TLSMinVersion      string `json:"tls_min_version"`
	HSTSMaxAge         int    `json:"hsts_max_age"`
	HSTSIncludeSubDoms bool   `json:"hsts_include_subdomains"`
	HSTSPreload        bool   `json:"hsts_preload"`

	// CORS
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowedMethods   []string `json:"allowed_methods"`
	AllowedHeaders   []string `json:"allowed_headers"`
	AllowCredentials bool     `json:"allow_credentials"`
	CORSMaxAge       int      `json:"cors_max_age"`

	// Rate Limiting
	AuthRateLimit   int           `json:"auth_rate_limit"`   // requests per minute
	GlobalRateLimit int           `json:"global_rate_limit"` // requests per minute
	RateLimitWindow time.Duration `json:"rate_limit_window"`
	RateLimitMemory int           `json:"rate_limit_memory"` // MB

	// Content Security
	CSPPolicy           string `json:"csp_policy"`
	XFrameOptions       string `json:"x_frame_options"`
	XContentTypeOptions string `json:"x_content_type_options"`
	XSSProtection       string `json:"xss_protection"`
	ReferrerPolicy      string `json:"referrer_policy"`

	// API Security
	RequireAPIKey  bool     `json:"require_api_key"`
	APIKeyHeader   string   `json:"api_key_header"`
	AllowedAPIKeys []string `json:"allowed_api_keys"`
	IPWhitelist    []string `json:"ip_whitelist"`
	IPBlacklist    []string `json:"ip_blacklist"`

	// Password & Auth
	PasswordMinLength     int  `json:"password_min_length"`
	PasswordRequireUpper  bool `json:"password_require_upper"`
	PasswordRequireLower  bool `json:"password_require_lower"`
	PasswordRequireNum    bool `json:"password_require_number"`
	PasswordRequireSymbol bool `json:"password_require_symbol"`
	BcryptCost            int  `json:"bcrypt_cost"`

	// Session Security
	SessionCookieSecure    bool          `json:"session_cookie_secure"`
	SessionCookieHTTPOnly  bool          `json:"session_cookie_httponly"`
	SessionCookieSameSite  string        `json:"session_cookie_samesite"`
	SessionTimeout         time.Duration `json:"session_timeout"`
	SessionCleanupInterval time.Duration `json:"session_cleanup_interval"`
}

type JWTConfig struct {
	SecretKey       string        `json:"secret_key"`
	PrivateKey      string        `json:"private_key"`  // RSA private key in PEM format
	PublicKey       string        `json:"public_key"`   // RSA public key in PEM format
	UseRSAKeys      bool          `json:"use_rsa_keys"` // Whether to use RSA keys instead of secret key
	AccessTokenTTL  time.Duration `json:"access_token_ttl"`
	RefreshTokenTTL time.Duration `json:"refresh_token_ttl"`
	Issuer          string        `json:"issuer"`
	Audience        string        `json:"audience"`
	Algorithm       string        `json:"algorithm"`
}

type SentryConfig struct {
	DSN         string        `json:"dsn"`
	Environment string        `json:"environment"`
	Release     string        `json:"release"`
	ServerName  string        `json:"server_name"`
	Timeout     time.Duration `json:"timeout"`
	Capture4xx  bool          `json:"capture_4xx"`
	Capture5xx  bool          `json:"capture_5xx"`
}

type SMSConfig struct {
	ProviderDomain string        `json:"provider_domain"`
	APIKey         string        `json:"api_key"`
	SourceNumber   string        `json:"source_number"`
	RetryCount     int           `json:"retry_count"`
	ValidityPeriod int           `json:"validity_period"`
	Timeout        time.Duration `json:"timeout"`
}

type EmailConfig struct {
	Host          string        `json:"host"`
	Port          int           `json:"port"`
	Username      string        `json:"username"`
	Password      string        `json:"password"`
	FromEmail     string        `json:"from_email"`
	FromName      string        `json:"from_name"`
	UseTLS        bool          `json:"use_tls"`
	UseSTARTTLS   bool          `json:"use_starttls"`
	RateLimit     int           `json:"rate_limit"` // Emails per minute
	RetryAttempts int           `json:"retry_attempts"`
	Timeout       time.Duration `json:"timeout"`
}

type LoggingConfig struct {
	Level            string `json:"level"`  // debug, info, warn, error
	Format           string `json:"format"` // json, text
	Output           string `json:"output"` // stdout, file, both
	FilePath         string `json:"file_path"`
	MaxSize          int    `json:"max_size"` // MB
	MaxBackups       int    `json:"max_backups"`
	MaxAge           int    `json:"max_age"` // days
	Compress         bool   `json:"compress"`
	EnableCaller     bool   `json:"enable_caller"`
	EnableStacktrace bool   `json:"enable_stacktrace"`

	// Access Logs
	EnableAccessLog bool   `json:"enable_access_log"`
	AccessLogPath   string `json:"access_log_path"`
	AccessLogFormat string `json:"access_log_format"`

	// Audit Logs
	EnableAuditLog bool   `json:"enable_audit_log"`
	AuditLogPath   string `json:"audit_log_path"`

	// Security Logs
	EnableSecurityLog bool   `json:"enable_security_log"`
	SecurityLogPath   string `json:"security_log_path"`
}

type MetricsConfig struct {
	Enabled     bool   `json:"enabled"`
	Port        int    `json:"port"`
	Path        string `json:"path"`
	EnablePprof bool   `json:"enable_pprof"`
	PprofPort   int    `json:"pprof_port"`

	// Prometheus
	EnablePrometheus bool   `json:"enable_prometheus"`
	PrometheusPath   string `json:"prometheus_path"`

	// Custom Metrics
	CollectDBMetrics    bool `json:"collect_db_metrics"`
	CollectCacheMetrics bool `json:"collect_cache_metrics"`
	CollectAppMetrics   bool `json:"collect_app_metrics"`
}

type CacheConfig struct {
	Enabled         bool          `json:"enabled"`
	Provider        string        `json:"provider"` // redis, memory
	RedisURL        string        `json:"redis_url"`
	RedisDB         int           `json:"redis_db"`
	RedisPrefix     string        `json:"redis_prefix"`
	DefaultTTL      time.Duration `json:"default_ttl"`
	MaxMemory       int           `json:"max_memory"` // MB
	CleanupInterval time.Duration `json:"cleanup_interval"`
}

type DeploymentConfig struct {
	// Domain Configuration
	Domain             string   `json:"domain"`
	APIDomain          string   `json:"api_domain"`
	MonitoringDomain   string   `json:"monitoring_domain"`
	SentryUIDomain     string   `json:"sentry_ui_domain"`
	SentryURLPrefix    string   `json:"sentry_url_prefix"`
	SentryAllowedHosts []string `json:"sentry_allowed_hosts"`

	// SSL/TLS Configuration
	CertbotEmail string `json:"certbot_email"`

	// Monitoring Configuration
	GrafanaAdminPassword string `json:"grafana_admin_password"`

	// Additional Security
	RedisPassword string `json:"redis_password"`

	// Backup Configuration
	BackupS3Bucket    string `json:"backup_s3_bucket"`
	BackupS3AccessKey string `json:"backup_s3_access_key"`
	BackupS3SecretKey string `json:"backup_s3_secret_key"`

	// Build Information
	Environment string `json:"environment"`
	Version     string `json:"version"`
	CommitHash  string `json:"commit_hash"`
	BuildTime   string `json:"build_time"`
}

type AtipayConfig struct {
	APIKey   string `json:"api_key"`
	Terminal string `json:"terminal"`
}

type AdminConfig struct {
	Mobiles               []string          `json:"admin_mobile"`
	DepositReviewers      []string          `json:"admin_deposit_reviewer"`
	TwoFAMobiles          map[string]string `json:"admin_2fa_mobiles"`
	OTPBypassMobiles      []string          `json:"admin_otp_bypass_mobiles"`
	LoginOTPForwardMobile string            `json:"admin_login_otp_forward_mobile"`
}

func (c AdminConfig) ActiveMobiles() []string {
	return normalizeMobileList(c.Mobiles)
}

func (c AdminConfig) ActiveDepositReviewers() []string {
	return normalizeMobileList(c.DepositReviewers)
}

func (c AdminConfig) TwoFAMobile(username string) string {
	trimmedUsername := strings.TrimSpace(username)
	if trimmedUsername == "" || len(c.TwoFAMobiles) == 0 {
		return ""
	}
	return strings.TrimSpace(c.TwoFAMobiles[trimmedUsername])
}

func (c AdminConfig) ActiveOTPBypassMobiles() []string {
	return normalizeMobileList(c.OTPBypassMobiles)
}

func (c AdminConfig) ActiveLoginOTPForwardMobile() string {
	return strings.TrimSpace(c.LoginOTPForwardMobile)
}

func (c AdminConfig) AllowsOTPBypass(mobile string) bool {
	trimmed := strings.TrimSpace(mobile)
	if trimmed == "" {
		return false
	}
	for _, allowedMobile := range c.ActiveOTPBypassMobiles() {
		if allowedMobile == trimmed {
			return true
		}
	}
	return false
}

func (c AdminConfig) HasMobile(mobile string) bool {
	trimmed := strings.TrimSpace(mobile)
	if trimmed == "" {
		return false
	}
	for _, adminMobile := range c.ActiveMobiles() {
		if adminMobile == trimmed {
			return true
		}
	}
	return false
}

func normalizeMobileList(mobiles []string) []string {
	if len(mobiles) == 0 {
		return nil
	}
	result := make([]string, 0, len(mobiles))
	seen := make(map[string]struct{}, len(mobiles))
	for _, mobile := range mobiles {
		trimmed := strings.TrimSpace(mobile)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

type BotConfig struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	APIDomain string `json:"api_domain"`
}

type CryptoConfig struct {
	Enabled         bool         `json:"enabled"`
	DefaultPlatform string       `json:"default_platform"`
	SupportedCoins  []string     `json:"supported_coins"`
	Oxapay          OxapayConfig `json:"oxapay"`
}

type OxapayConfig struct {
	BaseURL string        `json:"base_url"`
	APIKey  string        `json:"api_key"`
	Timeout time.Duration `json:"timeout"`
}

// SystemConfig holds system/tax actors and wallets UUIDs configured by admin
type SystemConfig struct {
	SystemUserUUID    string `json:"system_user_uuid"`
	TaxUserUUID       string `json:"tax_user_uuid"`
	SystemUserMobile  string `json:"system_user_mobile"`
	TaxUserMobile     string `json:"tax_user_mobile"`
	SystemUserEmail   string `json:"system_user_email"`
	TaxUserEmail      string `json:"tax_user_email"`
	SystemWalletUUID  string `json:"system_wallet_uuid"`
	TaxWalletUUID     string `json:"tax_wallet_uuid"`
	SystemShebaNumber string `json:"system_sheba_number"`
}

// PayamSMSConfig holds credentials and endpoints for PayamSMS OAuth
type PayamSMSConfig struct {
	TokenURL        string `json:"token_url"`
	SystemName      string `json:"system_name"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	Scope           string `json:"scope"`
	GrantType       string `json:"grant_type"`
	RootAccessToken string `json:"root_access_token"`
}

// CandooSMSConfig holds the campaign-scheduler settings for Candoo. It is
// separate from SMSConfig because a sender line chooses its campaign provider
// while OTP/notification routing remains globally configured.
type CandooSMSConfig struct {
	Enabled              bool          `json:"enabled"`
	BaseURL              string        `json:"base_url"`
	APIKey               string        `json:"api_key"`
	MessageType          int           `json:"message_type"`
	RetryCount           int           `json:"retry_count"`
	ValidityPeriod       int           `json:"validity_period"`
	Timeout              time.Duration `json:"timeout"`
	MaxRequestsPerSecond int           `json:"max_requests_per_second"`
	HTTPMaxAttempts      int           `json:"http_max_attempts"`
	// StatusCodeMap maps Candoo's integer delivery states to the internal SMS
	// model. The vendor's status-code definitions must be supplied by the
	// deployment, for example: "-1:pending,100:successful,200:unsuccessful".
	StatusCodeMap map[string]string `json:"status_code_map"`
}

func loadCandooSMSConfig() CandooSMSConfig {
	return CandooSMSConfig{
		Enabled:              getEnvBool("CANDOO_SMS_ENABLED", false),
		BaseURL:              getEnvString("CANDOO_SMS_BASE_URL", "https://api.candoosms.com"),
		APIKey:               getEnvString("CANDOO_SMS_API_KEY", ""),
		MessageType:          getEnvInt("CANDOO_SMS_MESSAGE_TYPE", 0),
		RetryCount:           getEnvInt("CANDOO_SMS_RETRY_COUNT", 0),
		ValidityPeriod:       getEnvInt("CANDOO_SMS_VALIDITY_PERIOD", 0),
		Timeout:              getEnvDuration("CANDOO_SMS_TIMEOUT", 30*time.Second),
		MaxRequestsPerSecond: getEnvInt("CANDOO_SMS_MAX_REQUESTS_PER_SECOND", 1),
		HTTPMaxAttempts:      getEnvInt("CANDOO_SMS_HTTP_MAX_ATTEMPTS", 3),
		StatusCodeMap:        getEnvStringMap("CANDOO_SMS_STATUS_MAP", map[string]string{}),
	}
}

// BaleConfig holds credentials for Bale Safir messaging API.
type BaleConfig struct {
	APIAccessKey string `json:"api_access_key"`
	Provider     string `json:"provider"`
	LegacyDomain string `json:"legacy_domain"`
	NajvaDomain  string `json:"najva_domain"`
}

// RubikaConfig holds credentials for Rubika messaging API.
type RubikaConfig struct {
	Token     string `json:"token"`
	ServiceID string `json:"service_id"`
	BaseURL   string `json:"base_url"`
}

// SplusConfig holds credentials for Splus business messaging API.
type SplusConfig struct {
	BaseURL string `json:"base_url"`
}

type SchedulerConfig struct {
	CampaignExecutionEnabled                   bool          `json:"campaign_execution_enabled"`
	CampaignExecutionInterval                  time.Duration `json:"campaign_execution_interval"`
	MessageSendDelay                           time.Duration `json:"message_send_delay"`
	MessageSendMockEnabled                     bool          `json:"message_send_mock_enabled"`
	SmartTargetingCapacitySchedulerEnabled     bool          `json:"smart_targeting_capacity_scheduler_enabled"`
	SmartTargetingTestSamplingSchedulerEnabled bool          `json:"smart_targeting_test_sampling_scheduler_enabled"`
	TagTestPerformanceSchedulerEnabled         bool          `json:"tag_test_performance_scheduler_enabled"`
	TagTestPerformanceSchedulerInterval        time.Duration `json:"tag_test_performance_scheduler_interval"`
	TagTestPerformanceSchedulerBatchSize       int           `json:"tag_test_performance_scheduler_batch_size"`
}

// ExternalShortLinkConfig controls outbound mapping publication and inbound click synchronization.
type ExternalShortLinkConfig struct {
	Enabled             bool          `json:"enabled"`
	BaseURL             string        `json:"base_url"`
	APIToken            string        `json:"api_token"`
	ClientCertFile      string        `json:"client_cert_file"`
	ClientKeyFile       string        `json:"client_key_file"`
	CAFile              string        `json:"ca_file"`
	AllowInsecureHTTP   bool          `json:"allow_insecure_http"`
	RequestTimeout      time.Duration `json:"request_timeout"`
	MappingSyncInterval time.Duration `json:"mapping_sync_interval"`
	ClickSyncInterval   time.Duration `json:"click_sync_interval"`
	MappingBatchSize    int           `json:"mapping_batch_size"`
	ClickPageSize       int           `json:"click_page_size"`
	MaxClickPagesPerRun int           `json:"max_click_pages_per_run"`
}

func loadSchedulerConfig() SchedulerConfig {
	capacitySchedulerEnabled := getEnvBool("SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED", false)
	return SchedulerConfig{
		CampaignExecutionEnabled:               getEnvBool("CAMPAIGN_EXECUTION_ENABLED", true),
		CampaignExecutionInterval:              getEnvDuration("CAMPAIGN_EXECUTION_INTERVAL", 1*time.Minute),
		MessageSendDelay:                       getEnvDuration("CAMPAIGN_MESSAGE_SEND_DELAY", 23*time.Millisecond),
		MessageSendMockEnabled:                 getEnvBool("CAMPAIGN_MESSAGE_SEND_MOCK_ENABLED", false),
		SmartTargetingCapacitySchedulerEnabled: capacitySchedulerEnabled,
		// Fall back to the former shared switch for a backward-compatible rollout.
		// An explicit sampling value always wins, so either worker can be enabled alone.
		SmartTargetingTestSamplingSchedulerEnabled: getEnvBool("SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED", capacitySchedulerEnabled),
		TagTestPerformanceSchedulerEnabled:         getEnvBool("TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED", false),
		TagTestPerformanceSchedulerInterval:        getEnvDuration("TAG_TEST_PERFORMANCE_SCHEDULER_INTERVAL", time.Minute),
		TagTestPerformanceSchedulerBatchSize:       getEnvInt("TAG_TEST_PERFORMANCE_SCHEDULER_BATCH_SIZE", 25),
	}
}

type MessageConfig struct {
	SignupVerificationCodeTemplate        string `json:"signup_verification_code_template"`
	SigninVerificationCodeTemplate        string `json:"signin_verification_code_template"`
	OTPResendVerificationCodeTemplate     string `json:"otp_resend_verification_code_template"`
	PasswordResetVerificationCodeTemplate string `json:"password_reset_verification_code_template"`
	CampaignRejectedTemplate              string `json:"campaign_rejected_template"`
	DepositReceiptSubmittedTemplate       string `json:"deposit_receipt_submitted_template"`
	InvoiceIssueRequestTemplate           string `json:"invoice_issue_request_template"`
}

type SmartTagEvaluationConfig struct {
	Enabled               bool                              `json:"enabled"`
	DailyLimitPerCustomer int                               `json:"daily_limit_per_customer"`
	Scheduler             SmartTagEvaluationSchedulerConfig `json:"scheduler"`
	PersonaAnalysis       SmartTagPromptConfig              `json:"persona_analysis"`
	TagScoring            SmartTagPromptConfig              `json:"tag_scoring"`
	OpenAI                SmartTagOpenAIConfig              `json:"openai"`
	Batching              SmartTagBatchingConfig            `json:"batching"`
	Validation            SmartTagValidationConfig          `json:"validation"`
}

type SmartTagEvaluationSchedulerConfig struct {
	Enabled         bool          `json:"enabled"`
	PollInterval    time.Duration `json:"poll_interval"`
	MaxParallelRuns int           `json:"max_parallel_runs"`
}

type SmartTagPromptConfig struct {
	SystemPrompt string `json:"system_prompt"`
}

type SmartTagOpenAIConfig struct {
	APIKeyEnv            string        `json:"api_key_env"`
	BaseURL              string        `json:"base_url"`
	Model                string        `json:"model"`
	ReasoningEffort      *string       `json:"reasoning_effort,omitempty"`
	MaxOutputTokens      int           `json:"max_output_tokens"`
	Temperature          *float64      `json:"temperature,omitempty"`
	Timeout              time.Duration `json:"timeout"`
	MaxRetries           int           `json:"max_retries"`
	MaxRequestsPerMinute int           `json:"max_requests_per_minute"`
	HTTPProxy            *string       `json:"http_proxy,omitempty"`
}

type SmartTagBatchingConfig struct {
	TagBatchSize       int `json:"tag_batch_size"`
	MaxParallelBatches int `json:"max_parallel_batches"`
}

type SmartTagValidationConfig struct {
	RequireExactTagCount bool `json:"require_exact_tag_count"`
	RequireExactTagIDs   bool `json:"require_exact_tag_ids"`
	MaxMissingTagCount   int  `json:"max_missing_tag_count"`
}

const (
	smartTagPersonaAnalysisSystemPromptFile = "SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT"
	smartTagTagScoringSystemPromptFile      = "SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT"
)

// LoadProductionConfig loads and validates configuration from environment variables
func LoadProductionConfig() (*ProductionConfig, error) {
	// Load environment variables from .env file
	if err := loadEnvFile(); err != nil {
		return nil, fmt.Errorf("failed to load .env file: %w", err)
	}

	smartTagEvaluationEnabled := getEnvBool("SMART_TAG_EVALUATION_ENABLED", false)
	personaAnalysisSystemPrompt, err := readConfigTextFile(
		smartTagPersonaAnalysisSystemPromptFile,
		smartTagEvaluationEnabled,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load smart tag persona analysis system prompt: %w", err)
	}
	tagScoringSystemPrompt, err := readConfigTextFile(
		smartTagTagScoringSystemPromptFile,
		smartTagEvaluationEnabled,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load smart tag scoring system prompt: %w", err)
	}

	cfg := &ProductionConfig{
		Database: DatabaseConfig{
			Host:            getEnvString("DB_HOST", "localhost"),
			Port:            getEnvInt("DB_PORT", 5432),
			Name:            getEnvString("DB_NAME", "postgres"),
			User:            getEnvString("DB_USER", "postgres"),
			Password:        getEnvString("DB_PASSWORD", ""),
			SSLMode:         getEnvString("DB_SSL_MODE", "require"),
			MaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 20),
			MaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
			ConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", 15*time.Minute),
			SlowQueryLog:    getEnvBool("DB_SLOW_QUERY_LOG", true),
			SlowQueryTime:   getEnvDuration("DB_SLOW_QUERY_TIME", 1*time.Second),
		},
		Server: ServerConfig{
			Host:              getEnvString("SERVER_HOST", "0.0.0.0"),
			Port:              getEnvInt("SERVER_PORT", 8080),
			ReadTimeout:       getEnvDuration("SERVER_READ_TIMEOUT", 5*time.Minute),
			WriteTimeout:      getEnvDuration("SERVER_WRITE_TIMEOUT", 5*time.Minute),
			IdleTimeout:       getEnvDuration("SERVER_IDLE_TIMEOUT", 120*time.Second),
			ShutdownTimeout:   getEnvDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second),
			BodyLimit:         getEnvInt("SERVER_BODY_LIMIT", 100*1024*1024), // 100MB
			EnablePprof:       getEnvBool("SERVER_ENABLE_PPROF", false),
			EnableMetrics:     getEnvBool("SERVER_ENABLE_METRICS", true),
			TrustedProxies:    getEnvStringSlice("SERVER_TRUSTED_PROXIES", []string{"127.0.0.1"}),
			ProxyHeader:       getEnvString("SERVER_PROXY_HEADER", "X-Forwarded-For"),
			EnableCompression: getEnvBool("SERVER_ENABLE_COMPRESSION", true),
			CompressionLevel:  getEnvInt("SERVER_COMPRESSION_LEVEL", 6),
		},
		Security: SecurityConfig{
			TLSEnabled:             getEnvBool("TLS_ENABLED", true),
			TLSCertFile:            getEnvString("TLS_CERT_FILE", "/etc/ssl/certs/yamata.crt"),
			TLSKeyFile:             getEnvString("TLS_KEY_FILE", "/etc/ssl/private/yamata.key"),
			TLSMinVersion:          getEnvString("TLS_MIN_VERSION", "1.3"),
			HSTSMaxAge:             getEnvInt("HSTS_MAX_AGE", 31536000), // 1 year
			HSTSIncludeSubDoms:     getEnvBool("HSTS_INCLUDE_SUBDOMAINS", true),
			HSTSPreload:            getEnvBool("HSTS_PRELOAD", true),
			AllowedOrigins:         getEnvStringSlice("CORS_ALLOWED_ORIGINS", []string{"https://yamata-no-orochi.com", "https://api.yamata-no-orochi.com", "https://wwww.yamata-no-orochi.com", "https://monitoring.yamata-no-orochi.com", "https://admin.yamata-no-orochi.com"}),
			AllowedMethods:         getEnvStringSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}),
			AllowedHeaders:         getEnvStringSlice("CORS_ALLOWED_HEADERS", []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-API-Key"}),
			AllowCredentials:       getEnvBool("CORS_ALLOW_CREDENTIALS", true),
			CORSMaxAge:             getEnvInt("CORS_MAX_AGE", 86400),
			AuthRateLimit:          getEnvInt("AUTH_RATE_LIMIT", 20),
			GlobalRateLimit:        getEnvInt("GLOBAL_RATE_LIMIT", 2000),
			RateLimitWindow:        getEnvDuration("RATE_LIMIT_WINDOW", 1*time.Minute),
			RateLimitMemory:        getEnvInt("RATE_LIMIT_MEMORY", 64), // MB
			CSPPolicy:              getEnvString("CSP_POLICY", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https: blob:; font-src 'self' https:; connect-src 'self' https:; frame-ancestors 'none';"),
			XFrameOptions:          getEnvString("X_FRAME_OPTIONS", "DENY"),
			XContentTypeOptions:    getEnvString("X_CONTENT_TYPE_OPTIONS", "nosniff"),
			XSSProtection:          getEnvString("XSS_PROTECTION", "1; mode=block"),
			ReferrerPolicy:         getEnvString("REFERRER_POLICY", "strict-origin-when-cross-origin"),
			RequireAPIKey:          getEnvBool("REQUIRE_API_KEY", false),
			APIKeyHeader:           getEnvString("API_KEY_HEADER", "X-API-Key"),
			AllowedAPIKeys:         getEnvStringSlice("ALLOWED_API_KEYS", []string{}),
			IPWhitelist:            getEnvStringSlice("IP_WHITELIST", []string{}),
			IPBlacklist:            getEnvStringSlice("IP_BLACKLIST", []string{}),
			PasswordMinLength:      getEnvInt("PASSWORD_MIN_LENGTH", 8),
			PasswordRequireUpper:   getEnvBool("PASSWORD_REQUIRE_UPPER", true),
			PasswordRequireLower:   getEnvBool("PASSWORD_REQUIRE_LOWER", true),
			PasswordRequireNum:     getEnvBool("PASSWORD_REQUIRE_NUMBER", true),
			PasswordRequireSymbol:  getEnvBool("PASSWORD_REQUIRE_SYMBOL", true),
			BcryptCost:             getEnvInt("BCRYPT_COST", 12),
			SessionCookieSecure:    getEnvBool("SESSION_COOKIE_SECURE", true),
			SessionCookieHTTPOnly:  getEnvBool("SESSION_COOKIE_HTTPONLY", true),
			SessionCookieSameSite:  getEnvString("SESSION_COOKIE_SAMESITE", "Strict"),
			SessionTimeout:         getEnvDuration("SESSION_TIMEOUT", 24*time.Hour),
			SessionCleanupInterval: getEnvDuration("SESSION_CLEANUP_INTERVAL", 1*time.Hour),
		},
		JWT: JWTConfig{
			SecretKey:       getEnvString("JWT_SECRET_KEY", ""),
			PrivateKey:      getEnvString("JWT_PRIVATE_KEY", ""),
			PublicKey:       getEnvString("JWT_PUBLIC_KEY", ""),
			UseRSAKeys:      getEnvBool("JWT_USE_RSA_KEYS", false),
			AccessTokenTTL:  getEnvDuration("JWT_ACCESS_TOKEN_TTL", 24*time.Hour),
			RefreshTokenTTL: getEnvDuration("JWT_REFRESH_TOKEN_TTL", 7*24*time.Hour),
			Issuer:          getEnvString("JWT_ISSUER", "yamata-no-orochi"),
			Audience:        getEnvString("JWT_AUDIENCE", "yamata-no-orochi-api"),
			Algorithm:       getEnvString("JWT_ALGORITHM", "HS256"),
		},
		Sentry: SentryConfig{
			DSN:         getEnvString("SENTRY_DSN", ""),
			Environment: getEnvString("SENTRY_ENVIRONMENT", getEnvString("APP_ENV", "production")),
			Release:     getEnvString("SENTRY_RELEASE", getEnvString("VERSION", "1.0.0")),
			ServerName:  getEnvString("SENTRY_SERVER_NAME", ""),
			Timeout:     getEnvDuration("SENTRY_TIMEOUT", 5*time.Second),
			Capture4xx:  getEnvBool("SENTRY_CAPTURE_4XX", true),
			Capture5xx:  getEnvBool("SENTRY_CAPTURE_5XX", true),
		},
		SMS: SMSConfig{
			ProviderDomain: getEnvString("SMS_PROVIDER_DOMAIN", "mock"),
			APIKey:         getEnvString("SMS_API_KEY", ""),
			SourceNumber:   getEnvString("SMS_SOURCE_NUMBER", ""),
			RetryCount:     getEnvInt("SMS_RETRY_COUNT", 3),
			ValidityPeriod: getEnvInt("SMS_VALIDITY_PERIOD", 300),
			Timeout:        getEnvDuration("SMS_TIMEOUT", 30*time.Second),
		},
		Email: EmailConfig{
			Host:          getEnvString("EMAIL_HOST", "smtp.gmail.com"),
			Port:          getEnvInt("EMAIL_PORT", 587),
			Username:      getEnvString("EMAIL_USERNAME", ""),
			Password:      getEnvString("EMAIL_PASSWORD", ""),
			FromEmail:     getEnvString("EMAIL_FROM_EMAIL", "noreply@yamata-no-orochi.com"),
			FromName:      getEnvString("EMAIL_FROM_NAME", "Yamata no Orochi"),
			UseTLS:        getEnvBool("EMAIL_USE_TLS", true),
			UseSTARTTLS:   getEnvBool("EMAIL_USE_STARTTLS", true),
			RateLimit:     getEnvInt("EMAIL_RATE_LIMIT", 100),
			RetryAttempts: getEnvInt("EMAIL_RETRY_ATTEMPTS", 3),
			Timeout:       getEnvDuration("EMAIL_TIMEOUT", 30*time.Second),
		},
		Logging: LoggingConfig{
			Level:             getEnvString("LOG_LEVEL", "info"),
			Format:            getEnvString("LOG_FORMAT", "json"),
			Output:            getEnvString("LOG_OUTPUT", "file"),
			FilePath:          getEnvString("LOG_FILE_PATH", "/var/log/yamata/app.log"),
			MaxSize:           getEnvInt("LOG_MAX_SIZE", 100),
			MaxBackups:        getEnvInt("LOG_MAX_BACKUPS", 10),
			MaxAge:            getEnvInt("LOG_MAX_AGE", 30),
			Compress:          getEnvBool("LOG_COMPRESS", true),
			EnableCaller:      getEnvBool("LOG_ENABLE_CALLER", true),
			EnableStacktrace:  getEnvBool("LOG_ENABLE_STACKTRACE", true),
			EnableAccessLog:   getEnvBool("LOG_ENABLE_ACCESS", true),
			AccessLogPath:     getEnvString("LOG_ACCESS_PATH", "/var/log/yamata/access.log"),
			AccessLogFormat:   getEnvString("LOG_ACCESS_FORMAT", "combined"),
			EnableAuditLog:    getEnvBool("LOG_ENABLE_AUDIT", true),
			AuditLogPath:      getEnvString("LOG_AUDIT_PATH", "/var/log/yamata/audit.log"),
			EnableSecurityLog: getEnvBool("LOG_ENABLE_SECURITY", true),
			SecurityLogPath:   getEnvString("LOG_SECURITY_PATH", "/var/log/yamata/security.log"),
		},
		Metrics: MetricsConfig{
			Enabled:             getEnvBool("METRICS_ENABLED", true),
			Port:                getEnvInt("METRICS_PORT", 9090),
			Path:                getEnvString("METRICS_PATH", "/metrics"),
			EnablePprof:         getEnvBool("METRICS_ENABLE_PPROF", false),
			PprofPort:           getEnvInt("METRICS_PPROF_PORT", 6060),
			EnablePrometheus:    getEnvBool("METRICS_ENABLE_PROMETHEUS", true),
			PrometheusPath:      getEnvString("METRICS_PROMETHEUS_PATH", "/prometheus"),
			CollectDBMetrics:    getEnvBool("METRICS_COLLECT_DB", true),
			CollectCacheMetrics: getEnvBool("METRICS_COLLECT_CACHE", true),
			CollectAppMetrics:   getEnvBool("METRICS_COLLECT_APP", true),
		},
		Cache: CacheConfig{
			Enabled:         getEnvBool("CACHE_ENABLED", true),
			Provider:        getEnvString("CACHE_PROVIDER", "redis"),
			RedisURL:        getEnvString("CACHE_REDIS_URL", "redis://localhost:6379"),
			RedisDB:         getEnvInt("CACHE_REDIS_DB", 0),
			RedisPrefix:     getEnvString("CACHE_REDIS_PREFIX", "yamata:"),
			DefaultTTL:      getEnvDuration("CACHE_DEFAULT_TTL", 1*time.Hour),
			MaxMemory:       getEnvInt("CACHE_MAX_MEMORY", 256),
			CleanupInterval: getEnvDuration("CACHE_CLEANUP_INTERVAL", 10*time.Minute),
		},
		Deployment: DeploymentConfig{
			Domain:               getEnvString("DOMAIN", "your-domain.com"),
			APIDomain:            getEnvString("API_DOMAIN", "api.your-domain.com"),
			MonitoringDomain:     getEnvString("MONITORING_DOMAIN", "monitoring.your-domain.com"),
			SentryUIDomain:       getEnvString("SENTRY_UI_DOMAIN", "sentry.your-domain.com"),
			SentryURLPrefix:      getEnvString("SENTRY_URL_PREFIX", "https://sentry.your-domain.com"),
			SentryAllowedHosts:   getEnvStringSlice("SENTRY_ALLOWED_HOSTS", []string{"sentry.your-domain.com"}),
			CertbotEmail:         getEnvString("CERTBOT_EMAIL", "admin@your-domain.com"),
			GrafanaAdminPassword: getEnvString("GRAFANA_ADMIN_PASSWORD", ""),
			RedisPassword:        getEnvString("REDIS_PASSWORD", ""),
			BackupS3Bucket:       getEnvString("BACKUP_S3_BUCKET", ""),
			BackupS3AccessKey:    getEnvString("BACKUP_S3_ACCESS_KEY", ""),
			BackupS3SecretKey:    getEnvString("BACKUP_S3_SECRET_KEY", ""),
			Environment:          getEnvString("APP_ENV", "production"),
			Version:              getEnvString("VERSION", "1.0.0"),
			CommitHash:           getEnvString("COMMIT_HASH", "unknown"),
			BuildTime:            getEnvString("BUILD_TIME", "unknown"),
		},
		Atipay: AtipayConfig{
			APIKey:   getEnvString("ATIPAY_API_KEY", ""),
			Terminal: getEnvString("ATIPAY_TERMINAL", ""),
		},
		Admin: AdminConfig{
			Mobiles:               getEnvStringSlice("ADMIN_MOBILE", []string{}),
			DepositReviewers:      getEnvStringSlice("ADMIN_DEPOSIT_REVIEWER", []string{}),
			TwoFAMobiles:          getEnvStringMap("ADMIN_2FA_MOBILES", map[string]string{}),
			OTPBypassMobiles:      getEnvStringSlice("ADMIN_OTP_BYPASS_MOBILES", []string{}),
			LoginOTPForwardMobile: getEnvString("ADMIN_LOGIN_OTP_FORWARD_MOBILE", ""),
		},
		System: SystemConfig{
			SystemUserUUID:    getEnvString("SYSTEM_USER_UUID", ""),
			TaxUserUUID:       getEnvString("TAX_USER_UUID", ""),
			SystemUserMobile:  getEnvString("SYSTEM_USER_MOBILE", ""),
			TaxUserMobile:     getEnvString("TAX_USER_MOBILE", ""),
			SystemUserEmail:   getEnvString("SYSTEM_USER_EMAIL", ""),
			TaxUserEmail:      getEnvString("TAX_USER_EMAIL", ""),
			SystemWalletUUID:  getEnvString("SYSTEM_WALLET_UUID", ""),
			TaxWalletUUID:     getEnvString("TAX_WALLET_UUID", ""),
			SystemShebaNumber: getEnvString("SYSTEM_SHEBA_NUMBER", ""),
		},
		PayamSMS: PayamSMSConfig{
			TokenURL:        getEnvString("PAYAM_SMS_TOKEN_URL", "https://www.payamsms.com/auth/oauth/token/"),
			SystemName:      getEnvString("PAYAM_SMS_SYSTEM_NAME", "jaazebeh.ir"),
			Username:        getEnvString("PAYAM_SMS_USERNAME", ""),
			Password:        getEnvString("PAYAM_SMS_PASSWORD", ""),
			Scope:           getEnvString("PAYAM_SMS_SCOPE", "webservice"),
			GrantType:       getEnvString("PAYAM_SMS_GRANT_TYPE", "password"),
			RootAccessToken: getEnvString("PAYAM_SMS_ROOT_ACCESS_TOKEN", ""),
		},
		CandooSMS: loadCandooSMSConfig(),
		Bale: BaleConfig{
			APIAccessKey: getEnvString("BALE_API_ACCESS_KEY", ""),
			Provider:     getEnvString("BALE_PROVIDER", "najva"),
			LegacyDomain: getEnvString("BALE_LEGACY_DOMAIN", "https://safir.bale.ai"),
			NajvaDomain:  getEnvString("BALE_NAJVA_DOMAIN", "https://sms.najva.com"),
		},
		Rubika: RubikaConfig{
			Token:     getEnvString("RUBIKA_TOKEN", ""),
			ServiceID: getEnvString("RUBIKA_SERVICE_ID", ""),
			BaseURL:   getEnvString("RUBIKA_BASE_URL", "https://messaging.rubika.ir"),
		},
		Splus: SplusConfig{
			BaseURL: getEnvString("SPLUS_BASE_URL", "https://bui.splus.ir"),
		},
		Bot: BotConfig{
			Username:  getEnvString("BOT_USERNAME", ""),
			Password:  getEnvString("BOT_PASSWORD", ""),
			APIDomain: getEnvString("BOT_API_DOMAIN", ""),
		},
		Scheduler: loadSchedulerConfig(),
		ExternalShortLink: ExternalShortLinkConfig{
			Enabled:             getEnvBool("EXTERNAL_SHORTLINK_ENABLED", false),
			BaseURL:             getEnvString("EXTERNAL_SHORTLINK_BASE_URL", ""),
			APIToken:            getEnvString("EXTERNAL_SHORTLINK_API_TOKEN", ""),
			ClientCertFile:      getEnvString("EXTERNAL_SHORTLINK_CLIENT_CERT_FILE", ""),
			ClientKeyFile:       getEnvString("EXTERNAL_SHORTLINK_CLIENT_KEY_FILE", ""),
			CAFile:              getEnvString("EXTERNAL_SHORTLINK_CA_FILE", ""),
			AllowInsecureHTTP:   getEnvBool("EXTERNAL_SHORTLINK_ALLOW_INSECURE_HTTP", false),
			RequestTimeout:      getEnvDuration("EXTERNAL_SHORTLINK_REQUEST_TIMEOUT", 30*time.Second),
			MappingSyncInterval: getEnvDuration("EXTERNAL_SHORTLINK_MAPPING_SYNC_INTERVAL", time.Minute),
			ClickSyncInterval:   getEnvDuration("EXTERNAL_SHORTLINK_CLICK_SYNC_INTERVAL", 5*time.Minute),
			MappingBatchSize:    getEnvInt("EXTERNAL_SHORTLINK_MAPPING_BATCH_SIZE", 500),
			ClickPageSize:       getEnvInt("EXTERNAL_SHORTLINK_CLICK_PAGE_SIZE", 1000),
			MaxClickPagesPerRun: getEnvInt("EXTERNAL_SHORTLINK_MAX_CLICK_PAGES_PER_RUN", 1000),
		},
		Crypto: CryptoConfig{
			Enabled:         getEnvBool("CRYPTO_ENABLED", true),
			DefaultPlatform: getEnvString("CRYPTO_DEFAULT_PLATFORM", "oxapay"),
			SupportedCoins:  getEnvStringSlice("CRYPTO_SUPPORTED_COINS", []string{"ETH", "DOGE", "XRP", "BNB"}),
			Oxapay: OxapayConfig{
				BaseURL: getEnvString("OXA_BASE_URL", "https://api.oxapay.com"),
				APIKey:  getEnvString("OXA_API_KEY", ""),
				Timeout: getEnvDuration("OXA_TIMEOUT", 10*time.Second),
			},
		},
		Message: MessageConfig{
			SignupVerificationCodeTemplate:        getEnvString("MESSAGE_SIGNUP_VERIFICATION_CODE_TEMPLATE", "Your verification code is %s"),
			SigninVerificationCodeTemplate:        getEnvString("MESSAGE_SIGNIN_VERIFICATION_CODE_TEMPLATE", "Your verification code is %s"),
			OTPResendVerificationCodeTemplate:     getEnvString("MESSAGE_OTP_RESEND_VERIFICATION_CODE_TEMPLATE", "Your new verification code is: %s. Valid for %v minutes."),
			PasswordResetVerificationCodeTemplate: getEnvString("MESSAGE_PASSWORD_RESET_VERIFICATION_CODE_TEMPLATE", "Your password reset code is: %s. This code will expire in %v minutes."),
			CampaignRejectedTemplate:              getEnvString("MESSAGE_CAMPAIGN_REJECTED_TEMPLATE", "Your campaign has been rejected."),
			DepositReceiptSubmittedTemplate:       getEnvString("MESSAGE_DEPOSIT_RECEIPT_SUBMITTED_TEMPLATE", "سلام شارژی در سامانه جاذبه انجام شده است. لطفا از پنل ادمین فاکتور مربوطه را صادر و آپلود نمایید"),
			InvoiceIssueRequestTemplate:           getEnvString("MESSAGE_INVOICE_ISSUE_REQUEST_TEMPLATE", "درخواست صدور فاکتور ثبت شد. مشتری: %s، شرکت: %s"),
		},
		SmartTagEvaluation: SmartTagEvaluationConfig{
			Enabled:               smartTagEvaluationEnabled,
			DailyLimitPerCustomer: getEnvInt("SMART_TAG_EVALUATION_DAILY_LIMIT_PER_CUSTOMER", 2),
			Scheduler: SmartTagEvaluationSchedulerConfig{
				Enabled:         getEnvBool("SMART_TAG_EVALUATION_SCHEDULER_ENABLED", false),
				PollInterval:    getEnvDuration("SMART_TAG_EVALUATION_SCHEDULER_POLL_INTERVAL", 30*time.Second),
				MaxParallelRuns: getEnvInt("SMART_TAG_EVALUATION_SCHEDULER_MAX_PARALLEL_RUNS", 2),
			},
			PersonaAnalysis: SmartTagPromptConfig{
				SystemPrompt: personaAnalysisSystemPrompt,
			},
			TagScoring: SmartTagPromptConfig{
				SystemPrompt: tagScoringSystemPrompt,
			},
			OpenAI: SmartTagOpenAIConfig{
				APIKeyEnv:            getEnvString("SMART_TAG_EVALUATION_OPENAI_API_KEY_ENV", "OPENAI_API_KEY"),
				BaseURL:              getEnvString("SMART_TAG_EVALUATION_OPENAI_BASE_URL", "https://api.openai.com/v1"),
				Model:                getEnvString("SMART_TAG_EVALUATION_OPENAI_MODEL", ""),
				ReasoningEffort:      getOptionalEnvString("SMART_TAG_EVALUATION_OPENAI_REASONING_EFFORT"),
				MaxOutputTokens:      getEnvInt("SMART_TAG_EVALUATION_OPENAI_MAX_OUTPUT_TOKENS", 8000),
				Temperature:          getOptionalEnvFloat64("SMART_TAG_EVALUATION_OPENAI_TEMPERATURE"),
				Timeout:              getEnvDuration("SMART_TAG_EVALUATION_OPENAI_TIMEOUT", 120*time.Second),
				MaxRetries:           getEnvInt("SMART_TAG_EVALUATION_OPENAI_MAX_RETRIES", 3),
				MaxRequestsPerMinute: getEnvInt("SMART_TAG_EVALUATION_OPENAI_MAX_REQUESTS_PER_MINUTE", 60),
				HTTPProxy:            getOptionalEnvString("SMART_TAG_EVALUATION_OPENAI_HTTP_PROXY"),
			},
			Batching: SmartTagBatchingConfig{
				TagBatchSize:       getEnvInt("SMART_TAG_EVALUATION_BATCHING_TAG_BATCH_SIZE", 50),
				MaxParallelBatches: getEnvInt("SMART_TAG_EVALUATION_BATCHING_MAX_PARALLEL_BATCHES", 4),
			},
			Validation: SmartTagValidationConfig{
				RequireExactTagCount: getEnvBool("SMART_TAG_EVALUATION_VALIDATION_REQUIRE_EXACT_TAG_COUNT", true),
				RequireExactTagIDs:   getEnvBool("SMART_TAG_EVALUATION_VALIDATION_REQUIRE_EXACT_TAG_IDS", true),
				MaxMissingTagCount:   getEnvInt("SMART_TAG_EVALUATION_VALIDATION_MAX_MISSING_TAG_COUNT", 1),
			},
		},
		IRHTTPSProxy: getEnvString("IR_HTTPS_PROXY", ""),
	}

	// Validate the loaded configuration
	if err := ValidateProductionConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadEnvFile loads environment variables from .env file if it exists
func loadEnvFile() error {
	envFile := ".env"

	// Check if .env file exists
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		// .env file doesn't exist, continue with environment variables
		return nil
	}

	// Open .env file
	file, err := os.Open(envFile)
	if err != nil {
		return fmt.Errorf("failed to open .env file: %w", err)
	}
	defer file.Close()

	// Read file line by line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value pairs
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Remove quotes if present
				if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
					(strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`)) {
					value = value[1 : len(value)-1]
				}

				// Set environment variable if not already set
				if os.Getenv(key) == "" {
					os.Setenv(key, value)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading .env file: %w", err)
	}

	return nil
}

// Helper functions for environment variable parsing
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getOptionalEnvString(key string) *string {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func readConfigTextFile(path string, required bool) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if !required && os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %q: %w", path, err)
	}

	return string(content), nil
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getOptionalEnvFloat64(key string) *float64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func getEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		// Use standard library strings.Split and strings.TrimSpace
		var result []string
		for _, item := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return defaultValue
}

func getEnvStringMap(key string, defaultValue map[string]string) map[string]string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}

	result := make(map[string]string)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 {
			continue
		}

		mapKey := strings.TrimSpace(parts[0])
		mapValue := strings.TrimSpace(parts[1])
		if mapKey == "" || mapValue == "" {
			continue
		}
		result[mapKey] = mapValue
	}

	if len(result) == 0 {
		return defaultValue
	}
	return result
}

// ValidateProductionConfig validates the production configuration
func ValidateProductionConfig(cfg *ProductionConfig) error {
	var errors []string

	// Validate database configuration
	if cfg.Database.Host == "" {
		errors = append(errors, "DB_HOST is required")
	}
	if cfg.Database.Port <= 0 || cfg.Database.Port > 65535 {
		errors = append(errors, "DB_PORT must be between 1 and 65535")
	}
	if cfg.Database.Name == "" {
		errors = append(errors, "DB_NAME is required")
	}
	if cfg.Database.User == "" {
		errors = append(errors, "DB_USER is required")
	}
	if cfg.Database.Password == "" {
		errors = append(errors, "DB_PASSWORD is required")
	}

	// Validate JWT configuration
	if cfg.JWT.SecretKey == "" {
		errors = append(errors, "JWT_SECRET_KEY is required")
	}
	if len(cfg.JWT.SecretKey) < 32 {
		errors = append(errors, "JWT_SECRET_KEY must be at least 32 characters long")
	}
	if cfg.JWT.AccessTokenTTL <= 0 {
		errors = append(errors, "JWT_ACCESS_TOKEN_TTL must be positive")
	}
	if cfg.JWT.RefreshTokenTTL <= 0 {
		errors = append(errors, "JWT_REFRESH_TOKEN_TTL must be positive")
	}
	if cfg.JWT.Issuer == "" {
		errors = append(errors, "JWT_ISSUER is required")
	}
	if cfg.JWT.Audience == "" {
		errors = append(errors, "JWT_AUDIENCE is required")
	}

	// Validate server configuration
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		errors = append(errors, "SERVER_PORT must be between 1 and 65535")
	}
	if cfg.Server.ReadTimeout <= 0 {
		errors = append(errors, "SERVER_READ_TIMEOUT must be positive")
	}
	if cfg.Server.WriteTimeout <= 0 {
		errors = append(errors, "SERVER_WRITE_TIMEOUT must be positive")
	}
	if cfg.Server.IdleTimeout <= 0 {
		errors = append(errors, "SERVER_IDLE_TIMEOUT must be positive")
	}

	// Validate security configuration
	if cfg.Security.PasswordMinLength < 6 {
		errors = append(errors, "PASSWORD_MIN_LENGTH must be at least 6")
	}
	if cfg.Security.BcryptCost < 10 || cfg.Security.BcryptCost > 14 {
		errors = append(errors, "BCRYPT_COST must be between 10 and 14")
	}

	// Validate SMS configuration if enabled
	if cfg.SMS.ProviderDomain == "payamsms" {
		if cfg.SMS.SourceNumber == "" {
			errors = append(errors, "SMS_SOURCE_NUMBER is required for PayamSMS provider")
		}
		if cfg.PayamSMS.Username == "" {
			errors = append(errors, "PAYAM_SMS_USERNAME is required for PayamSMS provider")
		}
		if cfg.PayamSMS.Password == "" {
			errors = append(errors, "PAYAM_SMS_PASSWORD is required for PayamSMS provider")
		}
	} else if cfg.SMS.ProviderDomain != "mock" {
		if cfg.SMS.APIKey == "" {
			errors = append(errors, "SMS_API_KEY is required for SMS provider")
		}
		if cfg.SMS.SourceNumber == "" {
			errors = append(errors, "SMS_SOURCE_NUMBER is required for SMS provider")
		}
	}
	if cfg.CandooSMS.Enabled {
		if strings.TrimSpace(cfg.CandooSMS.APIKey) == "" {
			errors = append(errors, "CANDOO_SMS_API_KEY is required when CANDOO_SMS_ENABLED is true")
		}
		if cfg.CandooSMS.MessageType < 0 || cfg.CandooSMS.MessageType > 4 {
			errors = append(errors, "CANDOO_SMS_MESSAGE_TYPE must be between 0 and 4")
		}
		if cfg.CandooSMS.RetryCount < 0 || cfg.CandooSMS.RetryCount > 10 {
			errors = append(errors, "CANDOO_SMS_RETRY_COUNT must be between 0 and 10")
		}
		if cfg.CandooSMS.ValidityPeriod < 0 || cfg.CandooSMS.ValidityPeriod > 172800 {
			errors = append(errors, "CANDOO_SMS_VALIDITY_PERIOD must be between 0 and 172800")
		}
		if cfg.CandooSMS.Timeout <= 0 {
			errors = append(errors, "CANDOO_SMS_TIMEOUT must be positive")
		}
		if cfg.CandooSMS.MaxRequestsPerSecond <= 0 {
			errors = append(errors, "CANDOO_SMS_MAX_REQUESTS_PER_SECOND must be positive")
		}
		if cfg.CandooSMS.HTTPMaxAttempts <= 0 {
			errors = append(errors, "CANDOO_SMS_HTTP_MAX_ATTEMPTS must be positive")
		}
		if len(cfg.CandooSMS.StatusCodeMap) == 0 {
			errors = append(errors, "CANDOO_SMS_STATUS_MAP is required when CANDOO_SMS_ENABLED is true")
		} else {
			hasTerminalStatus := false
			for code, mappedStatus := range cfg.CandooSMS.StatusCodeMap {
				if _, err := strconv.Atoi(strings.TrimSpace(code)); err != nil {
					errors = append(errors, fmt.Sprintf("CANDOO_SMS_STATUS_MAP has a non-integer Candoo status code %q", code))
					continue
				}
				switch models.SMSSendStatus(strings.ToLower(strings.TrimSpace(mappedStatus))) {
				case models.SMSSendStatusPending:
				case models.SMSSendStatusSuccessful, models.SMSSendStatusUnsuccessful:
					hasTerminalStatus = true
				default:
					errors = append(errors, fmt.Sprintf("CANDOO_SMS_STATUS_MAP maps code %q to invalid internal status %q", code, mappedStatus))
				}
			}
			if !hasTerminalStatus {
				errors = append(errors, "CANDOO_SMS_STATUS_MAP must include at least one successful or unsuccessful terminal mapping")
			}
		}
	}

	// Validate email configuration if enabled
	if cfg.Email.Host != "" {
		if cfg.Email.Username == "" {
			errors = append(errors, "EMAIL_USERNAME is required for email configuration")
		}
		if cfg.Email.Password == "" {
			errors = append(errors, "EMAIL_PASSWORD is required for email configuration")
		}
		if cfg.Email.FromEmail == "" {
			errors = append(errors, "EMAIL_FROM_EMAIL is required for email configuration")
		}
	}

	// Validate TLS configuration if enabled
	if cfg.Security.TLSEnabled {
		if cfg.Security.TLSCertFile == "" {
			errors = append(errors, "TLS_CERT_FILE is required when TLS is enabled")
		}
		if cfg.Security.TLSKeyFile == "" {
			errors = append(errors, "TLS_KEY_FILE is required when TLS is enabled")
		}
	}

	// Validate logging configuration
	if cfg.Logging.Level != "" {
		validLevels := []string{"debug", "info", "warn", "error"}
		valid := false
		for _, level := range validLevels {
			if cfg.Logging.Level == level {
				valid = true
				break
			}
		}
		if !valid {
			errors = append(errors, fmt.Sprintf("LOG_LEVEL must be one of: %v", validLevels))
		}
	}

	// Validate cache configuration if enabled
	if cfg.Cache.Enabled {
		if cfg.Cache.Provider == "redis" && cfg.Cache.RedisURL == "" {
			errors = append(errors, "CACHE_REDIS_URL is required when cache is enabled with redis provider")
		}
	}

	if cfg.Scheduler.TagTestPerformanceSchedulerEnabled {
		if cfg.Scheduler.TagTestPerformanceSchedulerInterval <= 0 {
			errors = append(errors, "TAG_TEST_PERFORMANCE_SCHEDULER_INTERVAL must be positive")
		}
		if cfg.Scheduler.TagTestPerformanceSchedulerBatchSize <= 0 {
			errors = append(errors, "TAG_TEST_PERFORMANCE_SCHEDULER_BATCH_SIZE must be positive")
		}
	}

	errors = append(errors, validateExternalShortLinkConfig(cfg.ExternalShortLink)...)

	if cfg.SmartTagEvaluation.Enabled {
		if cfg.SmartTagEvaluation.DailyLimitPerCustomer <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_DAILY_LIMIT_PER_CUSTOMER must be positive")
		}
		if cfg.SmartTagEvaluation.OpenAI.Model == "" {
			errors = append(errors, "SMART_TAG_EVALUATION_OPENAI_MODEL is required when smart tag evaluation is enabled")
		}
		if cfg.SmartTagEvaluation.OpenAI.Timeout <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_OPENAI_TIMEOUT must be positive")
		}
		if cfg.SmartTagEvaluation.OpenAI.MaxRetries < 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_OPENAI_MAX_RETRIES must be zero or greater")
		}
		if cfg.SmartTagEvaluation.OpenAI.MaxRequestsPerMinute <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_OPENAI_MAX_REQUESTS_PER_MINUTE must be positive")
		}
		if cfg.SmartTagEvaluation.Batching.TagBatchSize <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_BATCHING_TAG_BATCH_SIZE must be positive")
		}
		if cfg.SmartTagEvaluation.Batching.MaxParallelBatches <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_BATCHING_MAX_PARALLEL_BATCHES must be positive")
		}
		if cfg.SmartTagEvaluation.Validation.MaxMissingTagCount < 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_VALIDATION_MAX_MISSING_TAG_COUNT must be zero or greater")
		}
		if cfg.SmartTagEvaluation.Scheduler.PollInterval <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_SCHEDULER_POLL_INTERVAL must be positive")
		}
		if cfg.SmartTagEvaluation.Scheduler.MaxParallelRuns <= 0 {
			errors = append(errors, "SMART_TAG_EVALUATION_SCHEDULER_MAX_PARALLEL_RUNS must be positive")
		}
	}

	// System config is not optional and all must be parsed
	if cfg.System.SystemUserUUID == "" {
		errors = append(errors, "SYSTEM_USER_UUID is required")
	}
	if cfg.System.TaxUserUUID == "" {
		errors = append(errors, "TAX_USER_UUID is required")
	}
	if cfg.System.SystemUserMobile == "" {
		errors = append(errors, "SYSTEM_USER_MOBILE is required")
	}
	if cfg.System.TaxUserMobile == "" {
		errors = append(errors, "TAX_USER_MOBILE is required")
	}
	if cfg.System.SystemUserEmail == "" {
		errors = append(errors, "SYSTEM_USER_EMAIL is required")
	}
	if cfg.System.TaxUserEmail == "" {
		errors = append(errors, "TAX_USER_EMAIL is required")
	}
	if cfg.System.SystemWalletUUID == "" {
		errors = append(errors, "SYSTEM_WALLET_UUID is required")
	}
	if cfg.System.TaxWalletUUID == "" {
		errors = append(errors, "TAX_WALLET_UUID is required")
	}
	_, err := uuid.Parse(cfg.System.SystemUserUUID)
	if err != nil {
		errors = append(errors, "SYSTEM_USER_UUID is invalid")
	}
	_, err = uuid.Parse(cfg.System.TaxUserUUID)
	if err != nil {
		errors = append(errors, "TAX_USER_UUID is invalid")
	}
	_, err = uuid.Parse(cfg.System.SystemWalletUUID)
	if err != nil {
		errors = append(errors, "SYSTEM_WALLET_UUID is invalid")
	}
	_, err = uuid.Parse(cfg.System.TaxWalletUUID)
	if err != nil {
		errors = append(errors, "TAX_WALLET_UUID is invalid")
	}
	if cfg.System.SystemShebaNumber == "" {
		errors = append(errors, "SYSTEM_SHEBA_NUMBER is required")
	}
	_, err = utils.ValidateShebaNumber(&cfg.System.SystemShebaNumber)
	if err != nil {
		errors = append(errors, "SYSTEM_SHEBA_NUMBER is invalid")
	}

	errors = append(errors, validateCryptoConfig(cfg.Crypto)...)

	// Return validation errors if any
	if len(errors) > 0 {
		return fmt.Errorf("configuration validation failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

func validateCryptoConfig(cfg CryptoConfig) []string {
	if !cfg.Enabled {
		return nil
	}

	var errors []string
	if cfg.DefaultPlatform != "oxapay" {
		errors = append(errors, "CRYPTO_DEFAULT_PLATFORM must be oxapay")
	}
	if cfg.DefaultPlatform == "oxapay" {
		if cfg.Oxapay.BaseURL == "" {
			errors = append(errors, "OXA_BASE_URL is required when oxapay is default platform")
		}
		if cfg.Oxapay.APIKey == "" {
			errors = append(errors, "OXA_API_KEY is required when oxapay is default platform")
		}
	}
	return errors
}

func validateExternalShortLinkConfig(cfg ExternalShortLinkConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	var errors []string
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		errors = append(errors, "EXTERNAL_SHORTLINK_BASE_URL is required when external short links are enabled")
	} else {
		parsed, err := url.Parse(baseURL)
		validScheme := err == nil && (parsed.Scheme == "https" || (cfg.AllowInsecureHTTP && parsed.Scheme == "http"))
		if !validScheme || parsed.Hostname() == "" || parsed.User != nil ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			errors = append(errors, "EXTERNAL_SHORTLINK_BASE_URL must be an origin URL using HTTPS")
		}
	}
	if !isURLSafeExternalShortLinkToken(cfg.APIToken) {
		errors = append(errors, "EXTERNAL_SHORTLINK_API_TOKEN must be a URL-safe secret with at least 32 characters")
	}
	if (cfg.ClientCertFile == "") != (cfg.ClientKeyFile == "") {
		errors = append(errors, "EXTERNAL_SHORTLINK_CLIENT_CERT_FILE and CLIENT_KEY_FILE must be configured together")
	}
	if cfg.RequestTimeout <= 0 || cfg.MappingSyncInterval <= 0 || cfg.ClickSyncInterval <= 0 {
		errors = append(errors, "external short-link timeouts and intervals must be positive")
	}
	if cfg.MappingBatchSize <= 0 || cfg.MappingBatchSize > 500 {
		errors = append(errors, "EXTERNAL_SHORTLINK_MAPPING_BATCH_SIZE must be between 1 and 500")
	}
	if cfg.ClickPageSize <= 0 || cfg.ClickPageSize > 2000 {
		errors = append(errors, "EXTERNAL_SHORTLINK_CLICK_PAGE_SIZE must be between 1 and 2000")
	}
	if cfg.MaxClickPagesPerRun <= 0 {
		errors = append(errors, "EXTERNAL_SHORTLINK_MAX_CLICK_PAGES_PER_RUN must be positive")
	}
	return errors
}

func isURLSafeExternalShortLinkToken(token string) bool {
	if len(token) < 32 {
		return false
	}
	for _, character := range token {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '~' || character == '-' {
			continue
		}
		return false
	}
	return true
}
