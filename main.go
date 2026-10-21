// Package main provides the main entry point for the Yamata no Orochi authentication system
//
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description JWT Bearer token: Bearer <access_token>
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/handlers"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/app/router"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/config"
	_ "github.com/amirphl/Yamata-no-Orochi/docs"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"

	"github.com/amirphl/Yamata-no-Orochi/app/scheduler"
)

// Application represents the main application structure
type Application struct {
	router    *router.FiberRouter
	config    *config.ProductionConfig
	server    *fiber.App
	stopFuncs []func()
}

// dbStatsCollector exposes sql.DB Stats to Prometheus
type dbStatsCollector struct {
	db    *sql.DB
	name  string
	open  *prometheus.Desc
	inUse *prometheus.Desc
	idle  *prometheus.Desc
	waitC *prometheus.Desc
	waitD *prometheus.Desc
	maxOC *prometheus.Desc
	maxIC *prometheus.Desc
	maxLC *prometheus.Desc
}

var metricsRegisterOnce sync.Once

func newDBStatsCollector(db *sql.DB, name string) *dbStatsCollector {
	const ns = "db"
	labels := []string{"name"}
	return &dbStatsCollector{
		db:    db,
		name:  name,
		open:  prometheus.NewDesc(ns+"_open_connections", "The number of established connections both in use and idle.", labels, nil),
		inUse: prometheus.NewDesc(ns+"_in_use_connections", "The number of connections currently in use.", labels, nil),
		idle:  prometheus.NewDesc(ns+"_idle_connections", "The number of idle connections.", labels, nil),
		waitC: prometheus.NewDesc(ns+"_wait_count_total", "The total number of connections waited for.", labels, nil),
		waitD: prometheus.NewDesc(ns+"_wait_duration_seconds_total", "The total time blocked waiting for a new connection.", labels, nil),
		maxOC: prometheus.NewDesc(ns+"_max_open_connections", "Maximum number of open connections to the database.", labels, nil),
		maxIC: prometheus.NewDesc(ns+"_max_idle_closed_total", "The total number of connections closed due to SetMaxIdleConns.", labels, nil),
		maxLC: prometheus.NewDesc(ns+"_max_lifetime_closed_total", "The total number of connections closed due to SetConnMaxLifetime.", labels, nil),
	}
}

func (c *dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.open
	ch <- c.inUse
	ch <- c.idle
	ch <- c.waitC
	ch <- c.waitD
	ch <- c.maxOC
	ch <- c.maxIC
	ch <- c.maxLC
}

func (c *dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.db.Stats()
	labels := []string{c.name}
	ch <- prometheus.MustNewConstMetric(c.open, prometheus.GaugeValue, float64(s.OpenConnections), labels...)
	ch <- prometheus.MustNewConstMetric(c.inUse, prometheus.GaugeValue, float64(s.InUse), labels...)
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(s.Idle), labels...)
	ch <- prometheus.MustNewConstMetric(c.waitC, prometheus.CounterValue, float64(s.WaitCount), labels...)
	ch <- prometheus.MustNewConstMetric(c.waitD, prometheus.CounterValue, s.WaitDuration.Seconds(), labels...)
	ch <- prometheus.MustNewConstMetric(c.maxOC, prometheus.GaugeValue, float64(s.MaxOpenConnections), labels...)
	ch <- prometheus.MustNewConstMetric(c.maxIC, prometheus.CounterValue, float64(s.MaxIdleClosed), labels...)
	ch <- prometheus.MustNewConstMetric(c.maxLC, prometheus.CounterValue, float64(s.MaxLifetimeClosed), labels...)
}

func startMetricsServer(cfg config.MetricsConfig, db *gorm.DB) (func(), error) {
	if !cfg.Enabled || !cfg.EnablePrometheus {
		return func() {}, nil
	}

	metricsRegisterOnce.Do(func() {
		register := func(col prometheus.Collector) {
			if err := prometheus.DefaultRegisterer.Register(col); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
					return
				}
				log.Printf("metrics register error: %v", err)
			}
		}
		register(collectors.NewGoCollector())
		register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

		if db != nil {
			if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
				register(newDBStatsCollector(sqlDB, "primary"))
			}
		}
	})

	mux := http.NewServeMux()
	path := cfg.Path
	if path == "" {
		path = "/metrics"
	}
	mux.Handle(path, promhttp.Handler())

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()
	log.Printf("Metrics server started on %s%s", addr, path)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}, nil
}

func main() {
	// Load production configuration
	cfg, err := config.LoadProductionConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if err := observability.InitSentry(observability.SentryConfig{
		DSN:         cfg.Sentry.DSN,
		Environment: cfg.Sentry.Environment,
		Release:     cfg.Sentry.Release,
		ServerName:  cfg.Sentry.ServerName,
		Timeout:     cfg.Sentry.Timeout,
		Capture4xx:  cfg.Sentry.Capture4xx,
		Capture5xx:  cfg.Sentry.Capture5xx,
	}); err != nil {
		log.Fatalf("Failed to initialize sentry transport: %v", err)
	}

	// Configure logging to persist across container restarts with rotation
	logCloser, err := setupLogging(cfg.Logging)
	if err != nil {
		log.Printf("Failed to initialize file logger, falling back to default: %v", err)
	} else if logCloser != nil {
		defer logCloser.Close()
	}

	log.Println("Starting Yamata no Orochi application...")

	// Initialize application
	app, err := initializeApplication(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	// Setup routes
	app.router.SetupRoutes()

	// Setup graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		address := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		log.Printf("Server starting on %s", address)

		if err := app.server.Listen(address); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down gracefully...")

	// Stop background workers
	for _, fn := range app.stopFuncs {
		fn()
	}

	// Graceful  shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := app.server.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	observability.ShutdownSentry(shutdownCtx)

	log.Println("Server stopped")
}

// initializeDatabase initializes the database connection with connection pooling
func initializeDatabase(cfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode)

	logLevel := gormlogger.Error
	if cfg.SlowQueryLog {
		logLevel = gormlogger.Warn
	}
	gormLog := gormlogger.New(log.New(log.Writer(), "", log.Flags()), gormlogger.Config{
		SlowThreshold:             cfg.SlowQueryTime,
		LogLevel:                  logLevel,
		IgnoreRecordNotFoundError: false,
		// Large UID/audience arrays must never be interpolated into logs. Besides
		// producing enormous log lines, interpolation itself adds latency and can
		// expose customer targeting data.
		ParameterizedQueries: true,
		Colorful:             true,
	})

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormLog})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get underlying sql.DB for connection pooling configuration
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Configure connection pooling
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Test the connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Database connection established with %d max open connections, %d max idle connections",
		cfg.MaxOpenConns, cfg.MaxIdleConns)

	return db, nil
}

// initializeCache initializes the Cache client and verifies connectivity
func initializeCache(cfg config.CacheConfig) (*redis.Client, error) {
	if !cfg.Enabled || cfg.Provider != "redis" {
		return nil, nil
	}

	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis url: %w", err)
	}
	// Override DB if provided in config
	opt.DB = cfg.RedisDB

	rc := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rc.Ping(ctx).Err(); err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	log.Printf("Redis connection established to %s (db=%d)", cfg.RedisURL, cfg.RedisDB)
	return rc, nil
}

// startCacheHealthMonitor starts a background goroutine that periodically pings Redis
// to detect connectivity issues. The returned cancel function stops the monitor.
func startCacheHealthMonitor(parent context.Context, client *redis.Client, interval time.Duration) func() {
	monitorCtx, cancel := context.WithCancel(parent)
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
				if err := client.Ping(ctx).Err(); err != nil {
					log.Printf("Redis healthcheck failed: %v", err)
				}
				c()
			}
		}
	}()
	return cancel
}

// initializeNotificationService initializes the notification service
func initializeNotificationService(cfg *config.ProductionConfig) services.NotificationService {
	// Create SMS service based on configuration
	var smsService services.SMSService
	var emailProvider services.EmailProvider

	switch cfg.SMS.ProviderDomain {
	case "mock":
		smsService = services.NewMockSMSService()
	case "payamsms":
		smsService = services.NewPayamSMSService(&cfg.SMS, &cfg.PayamSMS)
	default:
		smsService = services.NewSMSService(&cfg.SMS)
	}

	// Create email provider (mock for now)
	emailProvider = services.NewMockEmailProvider()

	return services.NewNotificationService(smsService, emailProvider)
}

func initializeOTPSMSService(cfg *config.ProductionConfig) services.SMSService {
	if cfg.SMS.ProviderDomain == "mock" {
		return services.NewMockSMSService()
	}

	svc, err := services.NewPayamSMSServiceWithHTTPSProxy(&cfg.SMS, &cfg.PayamSMS, cfg.IRHTTPSProxy)
	if err != nil {
		log.Printf("Failed to initialize proxy-enabled OTP SMS service, falling back to direct connection: %v", err)
		return services.NewPayamSMSService(&cfg.SMS, &cfg.PayamSMS)
	}

	return svc
}

// initializeApplication initializes the main application components
func initializeApplication(cfg *config.ProductionConfig) (*Application, error) {
	var stopFuncs []func()

	// Initialize database
	db, err := initializeDatabase(cfg.Database)
	if err != nil {
		return nil, err
	}

	rc, err := initializeCache(cfg.Cache)
	if err != nil {
		return nil, err
	}

	cancel := startCacheHealthMonitor(context.Background(), rc, cfg.Cache.CleanupInterval)
	stopFuncs = append(stopFuncs, cancel)

	// Init system/tax entities dynamically using config
	if err := ensureSystemAndTaxEntities(db, cfg); err != nil {
		return nil, err
	}

	// Initialize repositories
	accountTypeRepo := repository.NewAccountTypeRepository(db)
	customerRepo := repository.NewCustomerRepository(db)
	sessionRepo := repository.NewCustomerSessionRepository(db)
	auditRepo := repository.NewAuditLogRepository(db)
	campaignRepo := repository.NewCampaignRepository(db)
	walletRepo := repository.NewWalletRepository(db)
	paymentRequestRepo := repository.NewPaymentRequestRepository(db)
	balanceSnapshotRepo := repository.NewBalanceSnapshotRepository(db)
	transactionRepo := repository.NewTransactionRepository(db)
	agencyDiscountRepo := repository.NewAgencyDiscountRepository(db)
	depositReceiptRepo := repository.NewDepositReceiptRepository(db)
	adminRepo := repository.NewAdminRepository(db)
	lineNumberRepo := repository.NewLineNumberRepository(db)
	botRepo := repository.NewBotRepository(db)
	audienceProfileRepo := repository.NewAudienceProfileRepository(db)
	tagRepo := repository.NewTagRepository(db)
	campaignSelectedTagRepo := repository.NewCampaignSelectedTagRepository(db)
	capacityCalculationRepo := repository.NewCampaignTargetingCapacityRepository(db)
	testSamplingCalculationRepo := repository.NewCampaignTargetingTestSamplingRepository(db)
	srcLayerAllStatsRepo := repository.NewSrcLayerAllStatsRepository(db)
	audienceSpecRepo := repository.NewAudienceSpecRepository(db)
	sentSMSRepo := repository.NewSentSMSRepository(db)
	sentBaleMessageRepo := repository.NewSentBaleMessageRepository(db)
	sentRubikaMessageRepo := repository.NewSentRubikaMessageRepository(db)
	sentSplusMessageRepo := repository.NewSentSplusMessageRepository(db)
	processedCampaignRepo := repository.NewProcessedCampaignRepository(db)
	campaignStatusJobRepo := repository.NewCampaignStatusJobRepository(db)
	smsStatusResultRepo := repository.NewSMSStatusResultRepository(db)
	baleStatusResultRepo := repository.NewBaleStatusResultRepository(db)
	rubikaStatusResultRepo := repository.NewRubikaStatusResultRepository(db)
	splusStatusResultRepo := repository.NewSplusStatusResultRepository(db)
	ticketRepo := repository.NewTicketRepository(db)
	multimediaRepo := repository.NewMultimediaAssetRepository(db)
	platformSettingsRepo := repository.NewPlatformSettingsRepository(db)
	bundleRepo := repository.NewBundleRepository(db)
	shortLinkRepo := repository.NewShortLinkRepository(db)
	shortLinkClickRepo := repository.NewShortLinkClickRepository(db)
	externalShortLinkSyncRepo := repository.NewExternalShortLinkSyncRepository(db)
	tagTestPerformanceRepo := repository.NewTagTestPerformanceRepository(db)
	segmentPriceFactorRepo := repository.NewSegmentPriceFactorRepository(db)
	platformBasePriceRepo := repository.NewPlatformBasePriceRepository(db)
	pagePriceRepo := repository.NewPagePriceRepository(db)
	bundleTagEvaluationRunRepo := repository.NewBundleTagEvaluationRunRepository(db)
	bundleTagEvaluationEventRepo := repository.NewBundleTagEvaluationEventRepository(db)
	bundleTagPersonaAttemptRepo := repository.NewBundleTagPersonaAnalysisAttemptRepository(db)
	bundleTagEvaluationBatchRepo := repository.NewBundleTagEvaluationBatchRepository(db)
	bundleTagEvaluationBatchAttemptRepo := repository.NewBundleTagEvaluationBatchAttemptRepository(db)
	bundleTagScoreRepo := repository.NewBundleTagScoreRepository(db)
	bundleTagEvaluationReadRepo := repository.NewBundleTagEvaluationReadRepository(db)
	// Crypto payment repositories
	cryptoPaymentRequestRepo := repository.NewCryptoPaymentRequestRepository(db)
	cryptoDepositRepo := repository.NewCryptoDepositRepository(db)

	// Initialize services
	notificationService := initializeNotificationService(cfg)

	var externalShortLinkClient *scheduler.HTTPExternalShortLinkClient
	var shortLinkPublishers []businessflow.ShortLinkMappingPublisher
	if cfg.ExternalShortLink.Enabled {
		externalShortLinkClient, err = scheduler.NewExternalShortLinkClient(cfg.ExternalShortLink)
		if err != nil {
			return nil, fmt.Errorf("initialize external short-link client: %w", err)
		}
		shortLinkPublishers = append(shortLinkPublishers, externalShortLinkClient)
	}

	// Captcha service for admin
	captchaSvc, err := services.NewCaptchaServiceRotate(2*time.Minute, 15, 300)
	if err != nil {
		return nil, err
	}

	// Initialize token service
	tokenService, err := services.NewTokenService(
		cfg.JWT.AccessTokenTTL,
		cfg.JWT.RefreshTokenTTL,
		cfg.JWT.Issuer,
		cfg.JWT.Audience,
		cfg.JWT.UseRSAKeys,
		cfg.JWT.PrivateKey,
		cfg.JWT.PublicKey,
		cfg.JWT.SecretKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize token service: %w", err)
	}

	// Log that services are initialized
	log.Printf("Token service initialized with issuer: %s, audience: %s", cfg.JWT.Issuer, cfg.JWT.Audience)

	// Initialize flows
	otpSMSService := initializeOTPSMSService(cfg)

	signupFlow := businessflow.NewSignupFlow(
		customerRepo,
		accountTypeRepo,
		sessionRepo,
		auditRepo,
		agencyDiscountRepo,
		walletRepo,
		tokenService,
		otpSMSService,
		notificationService,
		cfg.Admin,
		cfg.Message,
		db,
		rc,
	)

	loginFlow := businessflow.NewLoginFlow(
		customerRepo,
		sessionRepo,
		auditRepo,
		accountTypeRepo,
		tokenService,
		otpSMSService,
		notificationService,
		cfg.Message,
		cfg.Admin,
		db,
		rc,
	)

	campaignFlow := businessflow.NewCampaignFlow(
		campaignRepo,
		bundleRepo,
		shortLinkRepo,
		customerRepo,
		multimediaRepo,
		platformSettingsRepo,
		walletRepo,
		balanceSnapshotRepo,
		transactionRepo,
		auditRepo,
		lineNumberRepo,
		segmentPriceFactorRepo,
		platformBasePriceRepo,
		pagePriceRepo,
		processedCampaignRepo,
		smsStatusResultRepo,
		shortLinkClickRepo,
		campaignSelectedTagRepo,
		capacityCalculationRepo,
		testSamplingCalculationRepo,
		audienceSpecRepo,
		db,
		rc,
		notificationService,
		cfg.Admin,
		cfg.Cache,
		cfg.Bot,
		cfg.PayamSMS,
		cfg.CandooSMS,
		cfg.Bale,
		cfg.Rubika,
		cfg.Splus,
		cfg.IRHTTPSProxy,
		shortLinkPublishers...,
	)
	smartTargetingFlow := businessflow.NewSmartTargetingFlow(
		campaignRepo,
		bundleRepo,
		campaignSelectedTagRepo,
		bundleTagEvaluationReadRepo,
		db,
	)
	smartTargetingCapacityFlow := businessflow.NewSmartTargetingCapacityFlow(
		campaignRepo,
		campaignSelectedTagRepo,
		capacityCalculationRepo,
		lineNumberRepo,
		db,
	)

	// Initialize PaymentFlow
	paymentFlow := businessflow.NewPaymentFlow(
		paymentRequestRepo,
		walletRepo,
		customerRepo,
		campaignRepo,
		auditRepo,
		balanceSnapshotRepo,
		transactionRepo,
		agencyDiscountRepo,
		depositReceiptRepo,
		multimediaRepo,
		otpSMSService,
		cfg.Admin,
		cfg.Message,
		cfg.Cache,
		rc,
		db,
		cfg.Atipay,
		cfg.System,
		cfg.Deployment,
	)
	paymentAdminFlow := businessflow.NewPaymentAdminFlow(
		paymentRequestRepo,
		walletRepo,
		customerRepo,
		auditRepo,
		balanceSnapshotRepo,
		transactionRepo,
		agencyDiscountRepo,
		depositReceiptRepo,
		multimediaRepo,
		db,
		cfg.Atipay,
		cfg.System,
		cfg.Deployment,
	)

	// Initialize CryptoPaymentFlow (providers registry)
	providers := map[string]services.CryptoPaymentProvider{}
	if cfg.Crypto.Enabled && cfg.Crypto.Oxapay.BaseURL != "" && cfg.Crypto.Oxapay.APIKey != "" {
		providers["oxapay"] = services.NewOxapayClient(
			cfg.Crypto.Oxapay.BaseURL,
			cfg.Crypto.Oxapay.APIKey,
			cfg.Crypto.Oxapay.Timeout,
		)
	}
	cryptoPaymentFlow := businessflow.NewCryptoPaymentFlow(
		cryptoPaymentRequestRepo,
		cryptoDepositRepo,
		walletRepo,
		customerRepo,
		balanceSnapshotRepo,
		transactionRepo,
		auditRepo,
		agencyDiscountRepo,
		providers,
		db,
		cfg.System,
		cfg.Deployment,
	)

	// Initialize AgencyFlow
	agencyFlow := businessflow.NewAgencyFlow(
		customerRepo,
		campaignRepo,
		agencyDiscountRepo,
		transactionRepo,
		auditRepo,
		db,
	)

	adminAuthFlow := businessflow.NewAdminAuthFlow(
		adminRepo,
		tokenService,
		captchaSvc,
		otpSMSService,
		cfg.Admin,
		cfg.Message,
		rc,
	)

	botAuthFlow := businessflow.NewBotAuthFlow(
		botRepo,
		tokenService,
	)

	adminCampaignFlow := businessflow.NewAdminCampaignFlow(
		campaignRepo,
		customerRepo,
		walletRepo,
		balanceSnapshotRepo,
		transactionRepo,
		auditRepo,
		platformSettingsRepo,
		platformBasePriceRepo,
		lineNumberRepo,
		segmentPriceFactorRepo,
		pagePriceRepo,
		campaignSelectedTagRepo,
		capacityCalculationRepo,
		db,
		rc,
		notificationService,
		cfg.Admin,
		cfg.Message,
		cfg.Cache,
	)

	lineNumberFlow := businessflow.NewLineNumberFlow(lineNumberRepo)

	adminLineNumberFlow := businessflow.NewAdminLineNumberFlow(lineNumberRepo, db, auditRepo)

	adminCustomerManagementFlow := businessflow.NewAdminCustomerManagementFlow(
		customerRepo,
		campaignRepo,
		transactionRepo,
		auditRepo,
		lineNumberRepo,
		segmentPriceFactorRepo,
	)

	botCampaignFlow := businessflow.NewBotCampaignFlow(
		campaignRepo,
		multimediaRepo,
		platformSettingsRepo,
		transactionRepo,
		platformBasePriceRepo,
		campaignSelectedTagRepo,
		lineNumberRepo,
		cfg.Cache,
		db,
		rc,
	)
	botShortLinkFlow := businessflow.NewBotShortLinkFlow(shortLinkRepo, db, shortLinkPublishers...)

	ticketFlow := businessflow.NewTicketFlow(customerRepo, ticketRepo, notificationService, cfg.Admin)
	multimediaFlow := businessflow.NewMultimediaFlow(customerRepo, multimediaRepo)
	multimediaAdminFlow := businessflow.NewMultimediaAdminFlow(customerRepo, multimediaRepo)
	multimediaBotFlow := businessflow.NewMultimediaBotFlow(multimediaRepo)
	platformSettingsFlow := businessflow.NewPlatformSettingsFlow(platformSettingsRepo, multimediaRepo, notificationService, cfg.Admin)
	bundleFlow := businessflow.NewBundleFlow(bundleRepo, campaignRepo, customerRepo, auditRepo, bundleTagEvaluationReadRepo, db)
	bundleTagEvaluationFlow := businessflow.NewBundleTagEvaluationFlow(
		bundleRepo,
		customerRepo,
		tagRepo,
		bundleTagEvaluationRunRepo,
		bundleTagEvaluationEventRepo,
		bundleTagPersonaAttemptRepo,
		bundleTagEvaluationBatchRepo,
		bundleTagEvaluationBatchAttemptRepo,
		bundleTagScoreRepo,
		bundleTagEvaluationReadRepo,
		db,
		cfg.SmartTagEvaluation,
	)
	platformSettingsAdminFlow := businessflow.NewPlatformSettingsAdminFlow(platformSettingsRepo, multimediaRepo)
	platformBasePriceFlow := businessflow.NewPlatformBasePriceFlow(platformBasePriceRepo)
	platformBasePriceAdminFlow := businessflow.NewPlatformBasePriceAdminFlow(platformBasePriceRepo, auditRepo)

	shortLinkVisitFlow := businessflow.NewShortLinkVisitFlow(shortLinkRepo, shortLinkClickRepo)

	// Admin short-links flows and handler
	adminShortLinkFlow := businessflow.NewAdminShortLinkFlow(shortLinkRepo, shortLinkClickRepo, auditRepo)
	adminShortLinkDownloadFlow := businessflow.NewAdminShortLinkFlow(shortLinkRepo, shortLinkClickRepo, auditRepo)
	adminShortLinkClicksDownloadFlow := businessflow.NewAdminShortLinkFlow(shortLinkRepo, shortLinkClickRepo, auditRepo)

	// Profile flow
	profileFlow := businessflow.NewProfileFlow(customerRepo)

	segmentPriceFactorFlow := businessflow.NewSegmentPriceFactorFlow(segmentPriceFactorRepo, audienceSpecRepo)

	accessControlFlow := businessflow.NewAccessControlFlow(adminRepo, repository.NewACLChangeRequestRepository(db), auditRepo)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(signupFlow, loginFlow)
	bundleHandler := handlers.NewBundleHandler(bundleFlow, bundleTagEvaluationFlow)
	campaignHandler := handlers.NewCampaignHandler(campaignFlow, smartTargetingFlow, smartTargetingCapacityFlow)
	paymentHandler := handlers.NewPaymentHandler(paymentFlow)
	paymentAdminHandler := handlers.NewPaymentAdminHandler(paymentAdminFlow)
	cryptoPaymentHandler := handlers.NewCryptoPaymentHandler(cryptoPaymentFlow, cfg)
	agencyHandler := handlers.NewAgencyHandler(agencyFlow)
	authAdminHandler := handlers.NewAuthAdminHandler(adminAuthFlow)
	authBotHandler := handlers.NewAuthBotHandler(botAuthFlow)
	campaignAdminHandler := handlers.NewCampaignAdminHandler(adminCampaignFlow)
	lineNumberHandler := handlers.NewLineNumberHandler(lineNumberFlow)
	lineNumberAdminHandler := handlers.NewLineNumberAdminHandler(adminLineNumberFlow)
	adminCustomerManagementHandler := handlers.NewAdminCustomerManagementHandler(adminCustomerManagementFlow)
	campaignBotHandler := handlers.NewCampaignBotHandler(botCampaignFlow)
	shortLinkBotHandler := handlers.NewShortLinkBotHandler(botShortLinkFlow)
	shortLinkHandler := handlers.NewShortLinkHandler(shortLinkVisitFlow)
	shortLinkAdminHandler := handlers.NewShortLinkAdminHandler(adminShortLinkFlow, adminShortLinkDownloadFlow, adminShortLinkClicksDownloadFlow)

	ticketHandler := handlers.NewTicketHandler(ticketFlow)
	multimediaHandler := handlers.NewMultimediaHandler(multimediaFlow)
	multimediaAdminHandler := handlers.NewMultimediaAdminHandler(multimediaAdminFlow)
	multimediaBotHandler := handlers.NewMultimediaBotHandler(multimediaBotFlow)
	platformSettingsHandler := handlers.NewPlatformSettingsHandler(platformSettingsFlow)
	platformSettingsAdminHandler := handlers.NewPlatformSettingsAdminHandler(platformSettingsAdminFlow)
	platformBasePriceHandler := handlers.NewPlatformBasePriceHandler(platformBasePriceFlow)
	accessControlHandler := handlers.NewAccessControlHandler(accessControlFlow)

	profileHandler := handlers.NewProfileHandler(profileFlow)

	segmentPriceFactorAdminHandler := handlers.NewSegmentPriceFactorAdminHandler(segmentPriceFactorFlow)
	segmentPriceFactorHandler := handlers.NewSegmentPriceFactorHandler(segmentPriceFactorFlow)
	platformBasePriceAdminHandler := handlers.NewPlatformBasePriceAdminHandler(platformBasePriceAdminFlow)

	// Initialize auth middleware
	authMiddleware := middleware.NewAuthMiddleware(tokenService)
	authzMiddleware := middleware.NewAuthorizationMiddleware(adminRepo)

	// Initialize router
	appRouter := router.NewFiberRouter(
		authHandler,
		bundleHandler,
		campaignHandler,
		paymentHandler,
		paymentAdminHandler,
		agencyHandler,
		authMiddleware,
		authzMiddleware,
		authAdminHandler,
		authBotHandler,
		campaignAdminHandler,
		lineNumberHandler,
		lineNumberAdminHandler,
		segmentPriceFactorAdminHandler,
		platformBasePriceAdminHandler,
		platformBasePriceHandler,
		segmentPriceFactorHandler,
		adminCustomerManagementHandler,
		campaignBotHandler,
		ticketHandler,
		shortLinkBotHandler,
		shortLinkHandler,
		shortLinkAdminHandler,
		cryptoPaymentHandler,
		profileHandler,
		multimediaHandler,
		multimediaAdminHandler,
		multimediaBotHandler,
		platformSettingsHandler,
		platformSettingsAdminHandler,
		accessControlHandler,
		cfg.Server,
	)

	if cfg.ExternalShortLink.Enabled {
		mappingScheduler := scheduler.NewExternalShortLinkMappingScheduler(
			shortLinkRepo,
			externalShortLinkClient,
			log.Default(),
			cfg.ExternalShortLink.MappingSyncInterval,
			cfg.ExternalShortLink.MappingBatchSize,
		)
		stopFuncs = append(stopFuncs, mappingScheduler.Start(context.Background()))

		clickScheduler := scheduler.NewExternalShortLinkClickScheduler(
			externalShortLinkSyncRepo,
			externalShortLinkClient,
			log.Default(),
			cfg.ExternalShortLink.ClickSyncInterval,
			cfg.ExternalShortLink.ClickPageSize,
			cfg.ExternalShortLink.MaxClickPagesPerRun,
		)
		stopFuncs = append(stopFuncs, clickScheduler.Start(context.Background()))
	}

	if cfg.Scheduler.CampaignExecutionEnabled {
		if cfg.Scheduler.PayamBalanceMonitorEnabled {
			balanceClient, err := scheduler.NewPayamBalanceClientWithHTTPSProxy(cfg.PayamSMS, cfg.IRHTTPSProxy)
			if err != nil {
				log.Printf("payamsms balance monitor: proxy client setup failed, using direct connection: %v", err)
				balanceClient = scheduler.NewPayamBalanceClient(cfg.PayamSMS)
			}
			balanceScheduler := scheduler.NewPayamBalanceScheduler(
				balanceClient,
				notificationService,
				cfg.Admin,
				log.Default(),
				cfg.Scheduler.PayamBalanceMonitorInterval,
				cfg.Scheduler.PayamBalanceMonitorThresholdTomans,
				cfg.Scheduler.PayamBalanceMonitorMaxAlertInterval,
			)
			stopFuncs = append(stopFuncs, balanceScheduler.Start(context.Background()))
		}

		// Start SMS campaign scheduler.
		smsSched := scheduler.NewCampaignScheduler(
			audienceProfileRepo,
			tagRepo,
			lineNumberRepo,
			sentSMSRepo,
			processedCampaignRepo,
			campaignStatusJobRepo,
			smsStatusResultRepo,
			srcLayerAllStatsRepo,
			notificationService,
			db,
			log.Default(),
			cfg.Scheduler.CampaignExecutionInterval,
			cfg.PayamSMS,
			cfg.CandooSMS,
			cfg.Bot,
			cfg.Admin,
			cfg.Scheduler.MessageSendMockEnabled,
		)
		stopSMSScheduler := smsSched.Start(context.Background())
		stopFuncs = append(stopFuncs, stopSMSScheduler)

		// Start Bale campaign scheduler.
		baleSched := scheduler.NewBaleCampaignScheduler(
			audienceProfileRepo,
			tagRepo,
			sentBaleMessageRepo,
			processedCampaignRepo,
			campaignStatusJobRepo,
			baleStatusResultRepo,
			srcLayerAllStatsRepo,
			notificationService,
			db,
			log.Default(),
			cfg.Scheduler.CampaignExecutionInterval,
			cfg.Scheduler.MessageSendDelay,
			cfg.Bale,
			cfg.Bot,
			cfg.Admin,
			cfg.Scheduler.MessageSendMockEnabled,
		)
		stopBaleScheduler := baleSched.Start(context.Background())
		stopFuncs = append(stopFuncs, stopBaleScheduler)

		// Start Rubika campaign scheduler.
		rubikaSched := scheduler.NewRubikaCampaignScheduler(
			audienceProfileRepo,
			tagRepo,
			sentRubikaMessageRepo,
			processedCampaignRepo,
			campaignStatusJobRepo,
			rubikaStatusResultRepo,
			srcLayerAllStatsRepo,
			notificationService,
			db,
			log.Default(),
			cfg.Scheduler.CampaignExecutionInterval,
			cfg.Scheduler.MessageSendDelay,
			cfg.Rubika,
			cfg.Bot,
			cfg.Admin,
			cfg.Scheduler.MessageSendMockEnabled,
		)
		stopRubikaScheduler := rubikaSched.Start(context.Background())
		stopFuncs = append(stopFuncs, stopRubikaScheduler)

		// Start Splus campaign scheduler.
		splusSched := scheduler.NewSplusCampaignScheduler(
			audienceProfileRepo,
			tagRepo,
			sentSplusMessageRepo,
			processedCampaignRepo,
			campaignStatusJobRepo,
			splusStatusResultRepo,
			srcLayerAllStatsRepo,
			notificationService,
			db,
			log.Default(),
			cfg.Scheduler.CampaignExecutionInterval,
			cfg.Scheduler.MessageSendDelay,
			cfg.Splus,
			cfg.Bot,
			cfg.Admin,
			cfg.Scheduler.MessageSendMockEnabled,
		)
		stopSplusScheduler := splusSched.Start(context.Background())
		stopFuncs = append(stopFuncs, stopSplusScheduler)
	}

	if cfg.SmartTagEvaluation.Enabled && cfg.SmartTagEvaluation.Scheduler.Enabled {
		smartTagScheduler := scheduler.NewBundleTagEvaluationScheduler(
			bundleTagEvaluationFlow,
			bundleTagEvaluationReadRepo,
			log.Default(),
			cfg.SmartTagEvaluation.Scheduler.PollInterval,
			cfg.SmartTagEvaluation.Scheduler.MaxParallelRuns,
		)
		stopSmartTagScheduler := smartTagScheduler.Start(context.Background())
		stopFuncs = append(stopFuncs, stopSmartTagScheduler)
	}

	if cfg.Scheduler.SmartTargetingCapacitySchedulerEnabled {
		// Exact Smart Targeting capacity requests are durable database jobs. This
		// worker is intentionally independent of AI tag evaluation configuration.
		capacityScheduler := scheduler.NewSmartTargetingCapacityScheduler(
			smartTargetingCapacityFlow,
			capacityCalculationRepo,
			log.Default(),
			5*time.Second,
			2,
		)
		stopCapacityScheduler := capacityScheduler.Start(context.Background())
		stopFuncs = append(stopFuncs, stopCapacityScheduler)
	}

	if cfg.Scheduler.SmartTargetingTestSamplingSchedulerEnabled {
		// Test sampling owns a separate durable worker pool and can run before an
		// exact-capacity generation exists.
		testSamplingScheduler := scheduler.NewSmartTargetingTestSamplingScheduler(
			campaignFlow,
			testSamplingCalculationRepo,
			log.Default(),
			5*time.Second,
			1,
		)
		stopTestSamplingScheduler := testSamplingScheduler.Start(context.Background())
		stopFuncs = append(stopFuncs, stopTestSamplingScheduler)
	}

	if cfg.Scheduler.TagTestPerformanceSchedulerEnabled {
		tagTestPerformanceScheduler := scheduler.NewTagTestPerformanceScheduler(
			tagTestPerformanceRepo,
			log.Default(),
			cfg.Scheduler.TagTestPerformanceSchedulerInterval,
			cfg.Scheduler.TagTestPerformanceSchedulerBatchSize,
		)
		stopTagTestPerformanceScheduler := tagTestPerformanceScheduler.Start(context.Background())
		stopFuncs = append(stopFuncs, stopTagTestPerformanceScheduler)
	}

	// Create application struct from FiberRouter
	fiberRouter := appRouter.(*router.FiberRouter)
	// Start metrics server (Prometheus) if enabled
	if stop, err := startMetricsServer(cfg.Metrics, db); err == nil && stop != nil {
		stopFuncs = append(stopFuncs, stop)
	} else if err != nil {
		log.Printf("failed to start metrics server: %v", err)
	}

	application := &Application{
		router:    fiberRouter,
		config:    cfg,
		server:    fiberRouter.GetApp(),
		stopFuncs: stopFuncs,
	}

	return application, nil
}

func ensureSystemAndTaxEntities(db *gorm.DB, cfg *config.ProductionConfig) error {
	customerRepo := repository.NewCustomerRepository(db)
	accountTypeRepo := repository.NewAccountTypeRepository(db)
	walletRepo := repository.NewWalletRepository(db)

	// Ensure system user
	if cfg.System.SystemUserUUID != "" {
		if err := ensureCustomerByUUID(
			customerRepo,
			accountTypeRepo,
			cfg.System.SystemUserUUID,
			models.AccountTypeMarketingAgency,
			"System",
			"Account",
			cfg.System.SystemUserEmail,
			cfg.System.SystemUserMobile,
			"jazebeh.ir",
			cfg.System.SystemShebaNumber,
		); err != nil {
			return err
		}
	}
	// Ensure tax user
	if cfg.System.TaxUserUUID != "" {
		if err := ensureCustomerByUUID(
			customerRepo,
			accountTypeRepo,
			cfg.System.TaxUserUUID,
			models.AccountTypeIndividual,
			"Tax",
			"Collector",
			cfg.System.TaxUserEmail,
			cfg.System.TaxUserMobile,
			"tax.jazebeh.ir",
			"",
		); err != nil {
			return err
		}
	}

	// Ensure system wallet
	if cfg.System.SystemWalletUUID != "" && cfg.System.SystemUserUUID != "" {
		if err := ensureWalletByUUID(
			customerRepo,
			walletRepo,
			cfg.System.SystemWalletUUID,
			cfg.System.SystemUserUUID,
			map[string]any{"type": "system_wallet", "owner": "system", "source": "ensure_system_wallet"},
		); err != nil {
			return err
		}
	}

	// Ensure tax wallet
	if cfg.System.TaxWalletUUID != "" && cfg.System.TaxUserUUID != "" {
		if err := ensureWalletByUUID(
			customerRepo,
			walletRepo,
			cfg.System.TaxWalletUUID,
			cfg.System.TaxUserUUID,
			map[string]any{"type": "tax_wallet", "owner": "system", "source": "ensure_tax_wallet"},
		); err != nil {
			return err
		}
	}

	return nil
}

func ensureCustomerByUUID(
	customerRepo repository.CustomerRepository,
	accountTypeRepo repository.AccountTypeRepository,
	uuidStr, accountTypeName, firstName, lastName, email, mobile, agencyRefererCode, shebaNumber string,
) error {
	parsed, err := uuid.Parse(uuidStr)
	if err != nil {
		return err
	}
	customers, err := customerRepo.ByFilter(context.Background(), models.CustomerFilter{UUID: &parsed}, "", 1, 0)
	if err != nil {
		return err
	}
	if len(customers) > 0 {
		return nil
	}

	accountType, err := accountTypeRepo.ByTypeName(context.Background(), accountTypeName)
	if err != nil {
		return err
	}

	// Create minimal customer
	a := models.Customer{
		UUID:                    parsed,
		AgencyRefererCode:       agencyRefererCode,
		ShebaNumber:             utils.ToPtr(shebaNumber),
		AccountTypeID:           accountType.ID,
		RepresentativeFirstName: firstName,
		RepresentativeLastName:  lastName,
		RepresentativeMobile:    mobile,
		Email:                   email,
		PasswordHash:            "", // not used
		IsActive:                utils.ToPtr(true),
		IsEmailVerified:         utils.ToPtr(false),
		IsMobileVerified:        utils.ToPtr(false),
		CreatedAt:               utils.UTCNow(),
		UpdatedAt:               utils.UTCNow(),
	}
	err = customerRepo.Save(context.Background(), &a)
	if err != nil {
		return err
	}
	return nil
}

func ensureWalletByUUID(
	customerRepo repository.CustomerRepository,
	walletRepo repository.WalletRepository,
	walletUUID, ownerUUID string,
	metadata map[string]any) error {
	// Lookup owner by UUID
	ownerParsed, err := uuid.Parse(ownerUUID)
	if err != nil {
		return err
	}
	customers, err := customerRepo.ByFilter(context.Background(), models.CustomerFilter{UUID: &ownerParsed}, "", 1, 0)
	if err != nil {
		return err
	}
	if len(customers) == 0 {
		return fmt.Errorf("owner not found")
	}
	owner := customers[0]
	// Check wallet exists
	w, err := walletRepo.ByUUID(context.Background(), walletUUID)
	if err != nil {
		return err
	}
	if w != nil {
		return nil
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	// Create wallet with initial snapshot
	err = walletRepo.SaveWithInitialSnapshot(context.Background(), &models.Wallet{
		UUID:       uuid.MustParse(walletUUID),
		CustomerID: owner.ID,
		Metadata:   metadataJSON,
	})
	if err != nil {
		return err
	}
	return nil
}
