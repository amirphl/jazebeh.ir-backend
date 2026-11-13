// Package router provides HTTP routing, middleware configuration, and server setup for the web application
package router

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/handlers"
	"github.com/amirphl/Yamata-no-Orochi/app/middleware"
	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/config"
	_ "github.com/amirphl/Yamata-no-Orochi/docs"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cache"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/helmet"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// Router interface for HTTP routing
type Router interface {
	SetupRoutes()
	Start(address string) error
	GetApp() *fiber.App
}

// FiberRouter implements Router using Fiber v3
type FiberRouter struct {
	app                            *fiber.App
	authHandler                    handlers.AuthHandlerInterface
	bundleHandler                  handlers.BundleHandlerInterface
	campaignHandler                handlers.CampaignHandlerInterface
	paymentHandler                 handlers.PaymentHandlerInterface
	paymentAdminHandler            handlers.PaymentAdminHandlerInterface
	agencyHandler                  handlers.AgencyHandlerInterface
	authMiddleware                 *middleware.AuthMiddleware
	authzMiddleware                *middleware.AuthorizationMiddleware
	authAdminHandler               handlers.AuthAdminHandlerInterface
	authBotHandler                 handlers.AuthBotHandlerInterface
	campaignAdminHandler           handlers.CampaignAdminHandlerInterface
	lineNumberHandler              handlers.LineNumberHandlerInterface
	lineNumberAdminHandler         handlers.LineNumberAdminHandlerInterface
	segmentPriceFactorAdminHandler handlers.SegmentPriceFactorAdminHandlerInterface
	platformBasePriceAdminHandler  handlers.PlatformBasePriceAdminHandlerInterface
	platformBasePriceHandler       handlers.PlatformBasePriceHandlerInterface
	segmentPriceFactorHandler      handlers.SegmentPriceFactorHandlerInterface
	adminCustomerManagementHandler handlers.AdminCustomerManagementHandlerInterface
	campaignBotHandler             handlers.CampaignBotHandlerInterface
	ticketHandler                  handlers.TicketHandlerInterface
	shortLinkBotHandler            handlers.ShortLinkBotHandlerInterface
	shortLinkHandler               handlers.ShortLinkHandlerInterface
	shortLinkAdminHandler          handlers.ShortLinkAdminHandlerInterface
	cryptoPaymentHandler           handlers.CryptoPaymentHandlerInterface
	profileHandler                 handlers.ProfileHandlerInterface
	multimediaHandler              handlers.MultimediaHandlerInterface
	multimediaAdminHandler         handlers.MultimediaAdminHandlerInterface
	multimediaBotHandler           handlers.MultimediaBotHandlerInterface
	platformSettingsHandler        handlers.PlatformSettingsHandlerInterface
	platformSettingsAdminHandler   handlers.PlatformSettingsAdminHandlerInterface
	accessControlHandler           handlers.AccessControlHandlerInterface
}

// NewFiberRouter creates a new Fiber router
func NewFiberRouter(
	authHandler handlers.AuthHandlerInterface,
	bundleHandler handlers.BundleHandlerInterface,
	campaignHandler handlers.CampaignHandlerInterface,
	paymentHandler handlers.PaymentHandlerInterface,
	paymentAdminHandler handlers.PaymentAdminHandlerInterface,
	agencyHandler handlers.AgencyHandlerInterface,
	authMiddleware *middleware.AuthMiddleware,
	authzMiddleware *middleware.AuthorizationMiddleware,
	authAdminHandler handlers.AuthAdminHandlerInterface,
	authBotHandler handlers.AuthBotHandlerInterface,
	campaignAdminHandler handlers.CampaignAdminHandlerInterface,
	lineNumberHandler handlers.LineNumberHandlerInterface,
	lineNumberAdminHandler handlers.LineNumberAdminHandlerInterface,
	segmentPriceFactorAdminHandler handlers.SegmentPriceFactorAdminHandlerInterface,
	platformBasePriceAdminHandler handlers.PlatformBasePriceAdminHandlerInterface,
	platformBasePriceHandler handlers.PlatformBasePriceHandlerInterface,
	segmentPriceFactorHandler handlers.SegmentPriceFactorHandlerInterface,
	adminCustomerManagemetHandler handlers.AdminCustomerManagementHandlerInterface,
	campaignBotHandler handlers.CampaignBotHandlerInterface,
	ticketHandler handlers.TicketHandlerInterface,
	shortLinkBotHandler handlers.ShortLinkBotHandlerInterface,
	shortLinkHandler handlers.ShortLinkHandlerInterface,
	shortLinkAdminHandler handlers.ShortLinkAdminHandlerInterface,
	cryptoPaymentHandler handlers.CryptoPaymentHandlerInterface,
	profileHandler handlers.ProfileHandlerInterface,
	multimediaHandler handlers.MultimediaHandlerInterface,
	multimediaAdminHandler handlers.MultimediaAdminHandlerInterface,
	multimediaBotHandler handlers.MultimediaBotHandlerInterface,
	platformSettingsHandler handlers.PlatformSettingsHandlerInterface,
	platformSettingsAdminHandler handlers.PlatformSettingsAdminHandlerInterface,
	accessControlHandler handlers.AccessControlHandlerInterface,
	serverCfg config.ServerConfig,
) Router {
	// Configure Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "Yamata no Orochi API",
		ServerHeader: "Yamata-no-Orochi",
		ErrorHandler: errorHandler,
		BodyLimit:    serverCfg.BodyLimit,
		ReadTimeout:  serverCfg.ReadTimeout,
		WriteTimeout: serverCfg.WriteTimeout,
		IdleTimeout:  serverCfg.IdleTimeout,
		JSONEncoder:  json.Marshal,
		JSONDecoder:  json.Unmarshal,
		// Trust proxy headers from nginx to get real client IP
		ProxyHeader: serverCfg.ProxyHeader,
	})

	return &FiberRouter{
		app:                            app,
		authHandler:                    authHandler,
		bundleHandler:                  bundleHandler,
		campaignHandler:                campaignHandler,
		paymentHandler:                 paymentHandler,
		paymentAdminHandler:            paymentAdminHandler,
		agencyHandler:                  agencyHandler,
		authMiddleware:                 authMiddleware,
		authzMiddleware:                authzMiddleware,
		authAdminHandler:               authAdminHandler,
		authBotHandler:                 authBotHandler,
		campaignAdminHandler:           campaignAdminHandler,
		lineNumberHandler:              lineNumberHandler,
		lineNumberAdminHandler:         lineNumberAdminHandler,
		segmentPriceFactorAdminHandler: segmentPriceFactorAdminHandler,
		platformBasePriceAdminHandler:  platformBasePriceAdminHandler,
		platformBasePriceHandler:       platformBasePriceHandler,
		segmentPriceFactorHandler:      segmentPriceFactorHandler,
		adminCustomerManagementHandler: adminCustomerManagemetHandler,
		campaignBotHandler:             campaignBotHandler,
		ticketHandler:                  ticketHandler,
		shortLinkBotHandler:            shortLinkBotHandler,
		shortLinkHandler:               shortLinkHandler,
		shortLinkAdminHandler:          shortLinkAdminHandler,
		cryptoPaymentHandler:           cryptoPaymentHandler,
		profileHandler:                 profileHandler,
		multimediaHandler:              multimediaHandler,
		multimediaAdminHandler:         multimediaAdminHandler,
		multimediaBotHandler:           multimediaBotHandler,
		platformSettingsHandler:        platformSettingsHandler,
		platformSettingsAdminHandler:   platformSettingsAdminHandler,
		accessControlHandler:           accessControlHandler,
	}
}

// SetupRoutes configures all application routes
func (r *FiberRouter) SetupRoutes() {
	log.Println("Setting up routes...")

	// Global middleware
	r.setupMiddleware()

	// API routes
	api := r.app.Group("/api/v1")

	// Health check route (no rate limiting)
	api.Get("/health", r.healthCheck)

	// API documentation route (development only)
	if os.Getenv("APP_ENV") == "development" || os.Getenv("APP_ENV") == "local" {
		api.Get("/docs", r.getAPIDocumentation)
		api.Get("/swagger.json", r.serveSwaggerJSON)
		// Serve Swagger UI
		r.app.Get("/swagger", r.serveSwaggerUI)
		// Serve standalone Swagger UI
		r.app.Get("/swagger-standalone", r.serveStandaloneSwaggerUI)
		// Serve Swagger UI static assets
		r.app.Get("/swagger-ui-assets/*", func(c fiber.Ctx) error {
			filePath := c.Params("*")
			return c.SendFile("./docs/swagger-ui-assets/" + filePath)
		})
		log.Println("API documentation enabled for development")
	}

	// Apply general rate limiting to all API routes (aligned with nginx)
	api.Use(limiter.New(limiter.Config{
		Max:        2000,            // Maximum 2000 requests (matches nginx api zone)
		Expiration: 1 * time.Minute, // Per minute
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() // Rate limit by IP
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(dto.APIResponse{
				Success: false,
				Message: "Too many requests. Please try again later.",
				Error: dto.ErrorDetail{
					Code: "RATE_LIMIT_EXCEEDED",
				},
			})
		},
		Next: func(c fiber.Ctx) bool {
			// Skip rate limiting for health checks
			return c.Path() == "/api/v1/health"
		},
	}))

	// Auth routes with stricter rate limiting
	auth := api.Group("/auth")

	// Apply stricter rate limiting to auth endpoints (aligned with nginx)
	auth.Use(limiter.New(limiter.Config{
		Max:        20,              // Maximum 20 requests (matches nginx auth zone)
		Expiration: 1 * time.Minute, // Per minute
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() // Rate limit by IP
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(dto.APIResponse{
				Success: false,
				Message: "Too many requests. Please try again later.",
				Error: dto.ErrorDetail{
					Code: "RATE_LIMIT_EXCEEDED",
				},
			})
		},
	}))

	// Auth endpoints
	auth.Post("/signup", r.authHandler.Signup)
	auth.Post("/verify", r.authHandler.VerifyOTP)
	auth.Post("/resend-otp", r.authHandler.ResendOTP)
	auth.Post("/login", r.authHandler.Login)
	auth.Post("/login/otp", r.authHandler.RequestLoginOTP)
	auth.Post("/forgot-password", r.authHandler.ForgotPassword)
	auth.Post("/reset", r.authHandler.ResetPassword)

	// Admin auth routes (separate group; can have separate rate limit if needed)
	adminAuth := api.Group("/admin/auth")
	adminAuth.Use(limiter.New(limiter.Config{
		Max:          20,
		Expiration:   1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(dto.APIResponse{
				Success: false,
				Message: "Too many requests. Please try again later.",
				Error:   dto.ErrorDetail{Code: "RATE_LIMIT_EXCEEDED"},
			})
		},
	}))
	adminAuth.Get("/captcha/init", r.authAdminHandler.InitCaptcha)
	adminAuth.Post("/login", r.authAdminHandler.VerifyLogin)
	adminAuth.Post("/login/verify-otp", r.authAdminHandler.VerifyLoginOTP)

	// Bot auth
	botAuth := api.Group("/bot/auth")
	botAuth.Post("/login", r.authBotHandler.Login)

	// Campaign routes (protected with authentication)
	campaigns := api.Group("/campaigns")
	campaigns.Use(r.authMiddleware.Authenticate()) // Require authentication
	campaigns.Post("/", r.campaignHandler.CreateCampaign)
	campaigns.Put("/:uuid", r.campaignHandler.UpdateCampaign)
	campaigns.Get("/", r.campaignHandler.ListCampaigns)
	campaigns.Get("/:uuid/smart-targeting/tags", r.campaignHandler.ListSmartTargetingTags)
	campaigns.Get("/:uuid/smart-targeting/selection", r.campaignHandler.GetSmartTargetingSelection)
	campaigns.Put("/:uuid/smart-targeting/selection", r.campaignHandler.ReplaceSmartTargetingSelection)
	campaigns.Post("/:uuid/smart-targeting/selection/auto", r.campaignHandler.AutoSelectSmartTargetingTags)
	campaigns.Post("/:uuid/smart-targeting/capacity-calculations", r.campaignHandler.StartSmartTargetingCapacityCalculation)
	campaigns.Get("/:uuid/smart-targeting/capacity-calculations", r.campaignHandler.GetSmartTargetingCapacityCalculation)
	campaigns.Get("/:uuid/smart-targeting/capacity-calculations/:calculation_id", r.campaignHandler.GetSmartTargetingCapacityCalculationByID)
	campaigns.Post("/:uuid/smart-targeting/test-sampling-preview", r.campaignHandler.PreviewSmartTargetingTestSampling)
	campaigns.Get("/:uuid/smart-targeting/test-sampling-preview", r.campaignHandler.GetSmartTargetingTestSampling)
	campaigns.Get("/:uuid/smart-targeting/test-sampling-preview/:calculation_id", r.campaignHandler.GetSmartTargetingTestSamplingByID)
	campaigns.Post("/:uuid/smart-targeting/execution-audience-calculations", r.campaignHandler.StartSmartTargetingExecutionCalculation)
	campaigns.Get("/:uuid/smart-targeting/execution-audience-calculations/:calculation_id", r.campaignHandler.GetSmartTargetingExecutionCalculation)
	campaigns.Post("/:uuid/clone", r.campaignHandler.CloneCampaign)
	campaigns.Post("/:uuid/test-send", r.campaignHandler.SendCampaignTestMessage)
	campaigns.Post("/calculate-capacity", r.campaignHandler.CalculateCampaignCapacity)
	campaigns.Post("/calculate-cost", r.campaignHandler.CalculateCampaignCost)
	campaigns.Post("/calculate-cost-v2", r.campaignHandler.CalculateCampaignCostV2)
	campaigns.Get("/page-prices", r.campaignHandler.GetPagePrices)
	campaigns.Get("/audience-spec", r.campaignHandler.ListAudienceSpec)
	campaigns.Get("/summary", r.campaignHandler.GetApprovedRunningSummary)
	campaigns.Get("/initiated/last", r.campaignHandler.GetLastInitiatedCampaign)
	campaigns.Post("/audience-click-report", r.campaignHandler.ExportCampaignAudienceClickReport)
	campaigns.Get("/:id/export", r.campaignHandler.ExportCampaignReport)
	campaigns.Get("/:uuid/click-report", r.campaignHandler.ExportCampaignClickReport)
	campaigns.Post("/:id/cancel", r.campaignHandler.CancelCampaign)
	campaigns.Post("/hide", r.campaignHandler.HideCampaigns)
	campaigns.Post("/unhide", r.campaignHandler.UnhideCampaigns)

	// Bundle routes (protected with authentication)
	bundles := api.Group("/bundles")
	bundles.Use(r.authMiddleware.Authenticate())
	bundles.Post("/", r.bundleHandler.Create)
	bundles.Get("/", r.bundleHandler.List)
	bundles.Get("/:id", r.bundleHandler.Get)
	bundles.Get("/:id/smart-targeting/tags", r.campaignHandler.ListBundleSmartTargetingTags)
	bundles.Put("/:id", r.bundleHandler.Update)
	bundles.Post("/:id/tag-evaluations", r.bundleHandler.RequestTagEvaluation)
	bundles.Get("/:id/tag-evaluation", r.bundleHandler.GetTagEvaluationStatus)
	bundles.Get("/:id/tag-scores", r.bundleHandler.ListTagScores)

	// Admin campaigns listing and actions
	adminCampaigns := api.Group("/admin/campaigns")
	adminCampaigns.Use(r.authMiddleware.AdminAuthenticate())
	adminCampaigns.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminCampaigns.Use(r.authzMiddleware.AdminAuthorize())
	adminCampaigns.Get("/", r.campaignAdminHandler.ListCampaigns)
	adminCampaigns.Get("/page-prices", r.campaignAdminHandler.GetPagePrices)
	adminCampaigns.Get("/:id", r.campaignAdminHandler.GetCampaign)
	adminCampaigns.Post("/approve", r.campaignAdminHandler.ApproveCampaign)
	adminCampaigns.Post("/reject", r.campaignAdminHandler.RejectCampaign)
	adminCampaigns.Post("/reschedule", r.campaignAdminHandler.RescheduleCampaign)
	adminCampaigns.Post("/cancel", r.campaignAdminHandler.CancelCampaign)
	adminCampaigns.Put("/page-prices", r.campaignAdminHandler.UpdatePagePrice)

	// Admin segment price factors
	adminSegmentPF := api.Group("/admin/segment-price-factors")
	adminSegmentPF.Use(r.authMiddleware.AdminAuthenticate())
	adminSegmentPF.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminSegmentPF.Use(r.authzMiddleware.AdminAuthorize())
	adminSegmentPF.Post("/", r.segmentPriceFactorAdminHandler.CreateSegmentPriceFactor)
	adminSegmentPF.Get("/", r.segmentPriceFactorAdminHandler.ListSegmentPriceFactors)
	adminSegmentPF.Get("/level3-options", r.segmentPriceFactorAdminHandler.ListLevel3Options)

	// Admin platform base prices
	adminPlatformBasePrice := api.Group("/admin/platform-base-prices")
	adminPlatformBasePrice.Use(r.authMiddleware.AdminAuthenticate())
	adminPlatformBasePrice.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminPlatformBasePrice.Use(r.authzMiddleware.AdminAuthorize())
	adminPlatformBasePrice.Get("/", r.platformBasePriceAdminHandler.List)
	adminPlatformBasePrice.Put("/", r.platformBasePriceAdminHandler.Update)

	// Platform base prices (authenticated)
	platformBasePrice := api.Group("/platform-base-prices")
	platformBasePrice.Use(r.authMiddleware.Authenticate())
	platformBasePrice.Get("/", r.platformBasePriceHandler.List)

	// Bot campaigns routes (protected)
	botCampaigns := api.Group("/bot/campaigns")
	botCampaigns.Use(r.authMiddleware.BotAuthenticate())
	botCampaigns.Use(func(c fiber.Ctx) error { return middleware.RequireBotAuth(c) })
	botCampaigns.Get("/ready", r.campaignBotHandler.ListReadyCampaigns)
	botCampaigns.Post("/:id/executed", r.campaignBotHandler.MoveCampaignToExecuted)
	botCampaigns.Post("/:id/running", r.campaignBotHandler.MoveCampaignToRunning)
	botCampaigns.Post("/:id/statistics", r.campaignBotHandler.UpdateCampaignStatistics)
	botCampaigns.Get("/:id/target-audience-excel-file", r.campaignBotHandler.DownloadTargetAudienceExcelFile)
	botCampaigns.Post("/:id/audience-uids", r.campaignBotHandler.PushAudienceUIDs)

	// Bot short-links routes (protected)
	botShortLinks := api.Group("/bot/short-links")
	botShortLinks.Use(r.authMiddleware.BotAuthenticate())
	botShortLinks.Use(func(c fiber.Ctx) error { return middleware.RequireBotAuth(c) })
	botShortLinks.Post("/", r.shortLinkBotHandler.CreateShortLinks)
	botShortLinks.Post("/one", r.shortLinkBotHandler.CreateShortLink)
	botShortLinks.Post("/allocate", r.shortLinkBotHandler.AllocateShortLinks)

	// Bot multimedia routes (protected)
	botMedia := api.Group("/bot/media")
	botMedia.Use(r.authMiddleware.BotAuthenticate())
	botMedia.Use(func(c fiber.Ctx) error { return middleware.RequireBotAuth(c) })
	botMedia.Get("/:uuid", r.multimediaBotHandler.Download)

	// Admin short-links routes (protected)
	adminShortLinks := api.Group("/admin/short-links")
	adminShortLinks.Use(r.authMiddleware.AdminAuthenticate())
	adminShortLinks.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminShortLinks.Use(r.authzMiddleware.AdminAuthorize())
	adminShortLinks.Post("/upload-csv", r.shortLinkAdminHandler.UploadCSV)
	adminShortLinks.Post("/download", r.shortLinkAdminHandler.DownloadByScenario)
	adminShortLinks.Post("/download-with-clicks", r.shortLinkAdminHandler.DownloadWithClicksByScenario)
	adminShortLinks.Post("/download-with-clicks-range", r.shortLinkAdminHandler.DownloadWithClicksByScenarioRange)
	adminShortLinks.Post("/download-with-clicks-by-scenario-name", r.shortLinkAdminHandler.DownloadWithClicksByScenarioNameExcel)

	// Admin customer reports
	adminCustomers := api.Group("/admin/customer-management")
	adminCustomers.Use(r.authMiddleware.AdminAuthenticate())
	adminCustomers.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminCustomers.Use(r.authzMiddleware.AdminAuthorize())
	adminCustomers.Get("/", r.adminCustomerManagementHandler.ListCustomers)
	adminCustomers.Get("/shares", r.adminCustomerManagementHandler.GetCustomersShares)
	adminCustomers.Get("/:customer_id", r.adminCustomerManagementHandler.GetCustomerWithCampaigns)
	adminCustomers.Post("/active-status", r.adminCustomerManagementHandler.SetCustomerActiveStatus)
	adminCustomers.Get("/:customer_id/discounts", r.adminCustomerManagementHandler.GetCustomerDiscountsHistory)

	// Line numbers
	lineNumbers := api.Group("/line-numbers")
	lineNumbers.Use(r.authMiddleware.Authenticate()) // Require authentication
	lineNumbers.Get("/active", r.lineNumberHandler.ListActive)

	// Segment price factors (authenticated)
	segmentPriceFactors := api.Group("/segment-price-factors")
	segmentPriceFactors.Use(r.authMiddleware.Authenticate())
	segmentPriceFactors.Get("/", r.segmentPriceFactorHandler.ListLatest)

	// Admin line numbers protected routes
	adminLineNumbers := api.Group("/admin/line-numbers")
	adminLineNumbers.Use(r.authMiddleware.AdminAuthenticate())
	adminLineNumbers.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminLineNumbers.Use(r.authzMiddleware.AdminAuthorize())
	adminLineNumbers.Get("/", r.lineNumberAdminHandler.ListLineNumbers)
	adminLineNumbers.Post("/", r.lineNumberAdminHandler.CreateLineNumber)
	adminLineNumbers.Put("/", r.lineNumberAdminHandler.UpdateLineNumbersBatch)
	adminLineNumbers.Get("/report", r.lineNumberAdminHandler.GetLineNumbersReport)

	// Tickets
	tickets := api.Group("/tickets")
	tickets.Use(r.authMiddleware.Authenticate())
	tickets.Post("/", r.ticketHandler.Create)
	tickets.Post("/reply", r.ticketHandler.CreateResponse)
	tickets.Get("/", r.ticketHandler.List)
	tickets.Get("/:ticket_id/attachments/:file_index", r.ticketHandler.DownloadAttachment)

	// Admin tickets (reply)
	adminTickets := api.Group("/admin/tickets")
	adminTickets.Use(r.authMiddleware.AdminAuthenticate())
	adminTickets.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminTickets.Use(r.authzMiddleware.AdminAuthorize())
	adminTickets.Post("/reply", r.ticketHandler.AdminCreateResponse)
	adminTickets.Get("/", r.ticketHandler.AdminList)

	// Wallet routes (protected with authentication)
	wallet := api.Group("/wallet")
	wallet.Use(r.authMiddleware.Authenticate()) // Require authentication
	wallet.Get("/balance", r.paymentHandler.GetWalletBalance)

	// Payment routes
	payments := api.Group("/payments")
	// Charge wallet endpoint (protected with authentication)
	payments.Post("/charge-wallet", r.authMiddleware.Authenticate(), r.paymentHandler.ChargeWallet)
	// Payment callback endpoint (unprotected - called by Atipay)
	payments.Post("/callback/:invoice_number", r.paymentHandler.PaymentCallback)
	// Transaction history endpoint (protected with authentication)
	payments.Get("/history", r.authMiddleware.Authenticate(), r.paymentHandler.GetTransactionHistory)
	// Deposit receipt submission & listing
	payments.Post("/deposit-receipts", r.authMiddleware.Authenticate(), r.paymentHandler.SubmitDepositReceipt)
	payments.Post("/transactions/invoice-issue-request", r.authMiddleware.Authenticate(), r.paymentHandler.NotifyInvoiceIssueRequest)
	payments.Get("/deposit-receipts", r.authMiddleware.Authenticate(), r.paymentHandler.ListDepositReceipts)
	// Proforma invoice preview
	payments.Get("/proforma/preview", r.authMiddleware.Authenticate(), r.paymentHandler.PreviewProformaInvoice)
	payments.Get("/proforma/preview-by-amount", r.authMiddleware.Authenticate(), r.paymentHandler.PreviewProformaInvoiceByAmount)
	// Receipt file download
	payments.Get("/deposit-receipts/:receipt_uuid/file", r.authMiddleware.Authenticate(), r.paymentHandler.DownloadDepositReceiptFile)
	// Receipt file update/delete
	payments.Put("/deposit-receipts/:receipt_uuid/file", r.authMiddleware.Authenticate(), r.paymentHandler.UpdateDepositReceiptFile)
	payments.Delete("/deposit-receipts/:receipt_uuid/file", r.authMiddleware.Authenticate(), r.paymentHandler.DeleteDepositReceiptFile)

	// Admin payment routes (protected)
	adminPayments := api.Group("/admin/payments")
	adminPayments.Use(r.authMiddleware.AdminAuthenticate())
	adminPayments.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminPayments.Use(r.authzMiddleware.AdminAuthorize())
	adminPayments.Post("/charge-wallet", r.paymentAdminHandler.ChargeWallet)
	adminPayments.Post("/charge-wallet/preview", r.paymentAdminHandler.PreviewWalletChargeImpact)
	adminPayments.Get("/transactions", r.paymentAdminHandler.ListTransactions)
	adminPayments.Get("/deposit-receipts", r.paymentAdminHandler.ListDepositReceipts)
	adminPayments.Get("/deposit-receipts/:uuid/file", r.paymentAdminHandler.GetDepositReceiptFile)
	adminPayments.Post("/deposit-receipts/status", r.paymentAdminHandler.UpdateDepositReceiptStatus)
	adminPayments.Post("/transactions/invoice", r.paymentAdminHandler.AddInvoiceToTransaction)

	// Crypto payment routes
	crypto := api.Group("/crypto")
	// public provider callbacks
	api.Post("/crypto/providers/:platform/callback", r.cryptoPaymentHandler.Webhook)
	// protected crypto APIs
	crypto.Use(r.authMiddleware.Authenticate())
	crypto.Post("/payments/request", r.cryptoPaymentHandler.CreateRequest)
	crypto.Get("/payments/:uuid/status", r.cryptoPaymentHandler.GetStatus)
	crypto.Post("/payments/verify", r.cryptoPaymentHandler.ManualVerify)

	// Agency routes (protected)
	agency := api.Group("/reports")
	agency.Use(r.authMiddleware.Authenticate())
	agency.Get("/agency/customers", r.agencyHandler.GetAgencyCustomerReport)
	agency.Get("/agency/customers/list", r.agencyHandler.ListAgencyCustomers)
	agency.Get("/agency/discounts/active", r.agencyHandler.ListAgencyActiveDiscounts)
	agency.Get("/agency/customers/:customer_id/discounts", r.agencyHandler.ListAgencyCustomerDiscounts)
	agency.Post("/agency/discounts", r.agencyHandler.CreateAgencyDiscount)

	// Profile route (protected)
	api.Get("/profile", r.authMiddleware.Authenticate(), r.profileHandler.GetProfile)

	// Multimedia upload route (protected)
	media := api.Group("/media")
	media.Use(r.authMiddleware.Authenticate())
	media.Post("/upload", r.multimediaHandler.Upload)
	media.Get("/:uuid", r.multimediaHandler.Download)
	media.Get("/:uuid/preview", r.multimediaHandler.Preview)

	// Admin multimedia routes (protected)
	adminMedia := api.Group("/admin/media")
	adminMedia.Use(r.authMiddleware.AdminAuthenticate())
	adminMedia.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminMedia.Use(r.authzMiddleware.AdminAuthorize())
	adminMedia.Post("/upload", r.multimediaAdminHandler.Upload)
	adminMedia.Get("/:uuid", r.multimediaAdminHandler.Download)
	adminMedia.Get("/:uuid/preview", r.multimediaAdminHandler.Preview)

	// Platform settings routes (protected)
	platformSettings := api.Group("/platform-settings")
	platformSettings.Use(r.authMiddleware.Authenticate())
	platformSettings.Post("/", r.platformSettingsHandler.Create)
	platformSettings.Get("/", r.platformSettingsHandler.List)

	// Admin platform settings routes (protected)
	adminPlatformSettings := api.Group("/admin/platform-settings")
	adminPlatformSettings.Use(r.authMiddleware.AdminAuthenticate())
	adminPlatformSettings.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	adminPlatformSettings.Use(r.authzMiddleware.AdminAuthorize())
	adminPlatformSettings.Get("/", r.platformSettingsAdminHandler.List)
	adminPlatformSettings.Put("/status", r.platformSettingsAdminHandler.ChangeStatus)
	adminPlatformSettings.Put("/metadata", r.platformSettingsAdminHandler.AddMetadata)

	// Admin access-control (maker-checker)
	acl := api.Group("/admin/access-control")
	acl.Use(r.authMiddleware.AdminAuthenticate())
	acl.Use(func(c fiber.Ctx) error { return middleware.RequireAdminAuth(c) })
	acl.Use(r.authzMiddleware.AdminAuthorize())
	acl.Post("/requests", r.accessControlHandler.CreateRequest)
	acl.Post("/requests/:uuid/decision", r.accessControlHandler.ApproveRequest)

	// Public short-link redirect (no auth)
	r.app.Get("/s/:uid", r.shortLinkHandler.Visit)
	r.app.Get("/:uid", r.shortLinkHandler.Visit)

	// Public tst short-link redirect (no auth)
	// Handles /s/tst7df343 and /tst7df343
	r.app.Get("/s/tst:uid", r.shortLinkHandler.Visit)
	r.app.Get("/tst:uid", r.shortLinkHandler.Visit)

	// Not found handler
	r.app.Use(r.notFoundHandler)

	log.Println("Routes configured successfully")
}

// SetupMiddleware configures global middleware
func (r *FiberRouter) setupMiddleware() {
	// Request ID middleware - must be first
	r.app.Use(requestid.New(requestid.Config{
		Header: "X-Request-ID",
		Generator: func() string {
			return generateRequestID()
		},
	}))

	// Capture every completed 4xx/5xx response, even when the handler does not return an error.
	r.app.Use(observability.HTTPStatusCaptureMiddleware())

	// Prometheus HTTP metrics (concise)
	r.app.Use(middleware.Metrics())

	// Security headers middleware
	r.app.Use(helmet.New(helmet.Config{
		XSSProtection:             "1; mode=block",
		ContentTypeNosniff:        "nosniff",
		XFrameOptions:             "DENY",
		HSTSMaxAge:                31536000, // 1 year
		HSTSExcludeSubdomains:     false,
		ContentSecurityPolicy:     "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https: blob:; font-src 'self' https:; connect-src 'self' https:; frame-ancestors 'none';",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "cross-origin",
		OriginAgentCluster:        "?1",
		XDNSPrefetchControl:       "off",
		XDownloadOptions:          "noopen",
		XPermittedCrossDomain:     "none",
	}))

	// CORS middleware with production settings
	r.app.Use(cors.New(cors.Config{
		AllowOrigins: []string{
			"https://yamata-no-orochi.com",
			"https://api.yamata-no-orochi.com",
			"https://admin.yamata-no-orochi.com",
			"https://monitoring.yamata-no-orochi.com",
			"https://app.yamata-no-orochi.com",
			"https://*.j0in.ir",
		},
		AllowMethods: []string{
			"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS",
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Accept",
			"Authorization",
			"X-Requested-With",
			"X-Request-ID",
			"X-API-Key",
			"Cache-Control",
		},
		ExposeHeaders: []string{
			"X-Request-ID",
			"X-Response-Time",
		},
		AllowCredentials: true,
		MaxAge:           utils.CORSMaxAge,
	}))

	// Compression middleware for performance
	r.app.Use(compress.New(compress.Config{
		Level: compress.LevelBestSpeed,
		Next: func(c fiber.Ctx) bool {
			// Skip compression for certain content types
			contentType := c.Get("Content-Type")
			return contains(contentType, "image/") ||
				contains(contentType, "video/") ||
				contains(contentType, "audio/")
		},
	}))

	// Cache middleware for static content
	r.app.Use(cache.New(cache.Config{
		Next: func(c fiber.Ctx) bool {
			// Only cache GET requests to specific endpoints
			return c.Method() != "GET" ||
				!contains(c.Path(), "/health") &&
					!contains(c.Path(), "/docs")
		},
		Expiration: 30 * time.Minute,
	}))

	// Advanced logging middleware
	r.app.Use(logger.New(logger.Config{
		Format:     `{"time":"${time}","pid":"${pid}","request_id":"${request_id}","level":"info","method":"${method}","path":"${path}","protocol":"${protocol}","ip":"${ip}","user_agent":"${ua}","status":${status},"latency":"${latency}","bytes_in":${bytesReceived},"bytes_out":${bytesSent},"referer":"${referer}"}` + "\n",
		TimeFormat: time.RFC3339,
		TimeZone:   "UTC",
		CustomTags: map[string]logger.LogFunc{
			"request_id": func(output logger.Buffer, c fiber.Ctx, _ *logger.Data, _ string) (int, error) {
				return output.WriteString(jsonLogString(requestid.FromContext(c)))
			},
		},
		Next: func(c fiber.Ctx) bool {
			// Skip logging for health checks in production
			return c.Path() == "/api/v1/health"
		},
	}))

	// Custom security middleware
	r.app.Use(r.securityMiddleware)

	// API key validation middleware (optional)
	r.app.Use(r.apiKeyMiddleware)

	// Recovery middleware with custom error handling
	r.app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			// Log panic with request context
			log.Printf(`{"time":"%s","level":"error","request_id":"%s","event":"panic","error":"%v","path":"%s","method":"%s","ip":"%s"}`,
				utils.UTCNow().Format(time.RFC3339),
				requestid.FromContext(c),
				e,
				c.Path(),
				c.Method(),
				c.IP(),
			)
			observability.CapturePanic(c, e)
		},
	}))
}

// Custom security middleware
func (r *FiberRouter) securityMiddleware(c fiber.Ctx) error {
	// Add security headers
	c.Set("X-Response-Time", utils.UTCNow().Format(time.RFC3339))
	c.Set("Server", "Yamata-no-Orochi")

	// IP validation (if configured)
	clientIP := c.IP()

	// Simple IP blocking example
	blockedIPs := []string{
		"127.0.0.2", // Example blocked IP
	}

	for _, blockedIP := range blockedIPs {
		if clientIP == blockedIP {
			return c.Status(fiber.StatusForbidden).JSON(dto.APIResponse{
				Success: false,
				Message: "Access denied from this IP address",
				Error: dto.ErrorDetail{
					Code: "ACCESS_DENIED",
				},
			})
		}
	}

	// Continue to next middleware
	return c.Next()
}

// API key validation middleware
func (r *FiberRouter) apiKeyMiddleware(c fiber.Ctx) error {
	// Skip API key validation for certain endpoints
	if c.Path() == "/api/v1/health" || c.Path() == "/api/v1/docs" {
		return c.Next()
	}

	// Check if API key is required (this would come from config)
	requireAPIKey := false // Set from environment/config

	if requireAPIKey {
		apiKey := c.Get("X-API-Key")
		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "API key is required",
				Error: dto.ErrorDetail{
					Code: "MISSING_API_KEY",
				},
			})
		}

		// Validate API key (this would check against database/config)
		validAPIKeys := []string{
			"your-production-api-key", // Example - load from config
		}

		isValid := false
		for _, validKey := range validAPIKeys {
			if apiKey == validKey {
				isValid = true
				break
			}
		}

		if !isValid {
			return c.Status(fiber.StatusUnauthorized).JSON(dto.APIResponse{
				Success: false,
				Message: "Invalid API key",
				Error: dto.ErrorDetail{
					Code: "INVALID_API_KEY",
				},
			})
		}
	}

	return c.Next()
}

// Start starts the HTTP server
func (r *FiberRouter) Start(address string) error {
	log.Printf("Starting server on %s", address)
	return r.app.Listen(address)
}

// GetApp returns the Fiber app instance
func (r *FiberRouter) GetApp() *fiber.App {
	return r.app
}

// Health check endpoint
func (r *FiberRouter) healthCheck(c fiber.Ctx) error {
	return c.JSON(dto.APIResponse{
		Success: true,
		Message: "Service is healthy",
		Data: fiber.Map{
			"status":    "ok",
			"timestamp": utils.UTCNow().Unix(),
			"version":   "1.0.0",
			"service":   "yamata-no-orochi-api",
		},
	})
}

// API documentation endpoint
func (r *FiberRouter) getAPIDocumentation(c fiber.Ctx) error {
	docs := GetRouteDocumentation()
	return c.JSON(dto.APIResponse{
		Success: true,
		Message: "API documentation retrieved successfully",
		Data: fiber.Map{
			"title":       "Yamata no Orochi API Documentation",
			"version":     "1.0.0",
			"description": "Customer signup and authentication API",
			"endpoints":   docs,
		},
	})
}

// Serve Swagger UI HTML page
func (r *FiberRouter) serveSwaggerUI(c fiber.Ctx) error {
	htmlContent := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Yamata no Orochi API - Swagger UI</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui.css" />
    <style>
        html {
            box-sizing: border-box;
            overflow: -moz-scrollbars-vertical;
            overflow-y: scroll;
        }
        *, *:before, *:after {
            box-sizing: inherit;
        }
        body {
            margin:0;
            background: #fafafa;
        }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                url: '/api/v1/swagger.json',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout",
                validatorUrl: null,
                onComplete: function() {
                    console.log("Swagger UI loaded successfully");
                }
            });
        };
    </script>
</body>
</html>`

	c.Set("Content-Type", "text/html")
	return c.SendString(htmlContent)
}

// Serve Swagger JSON specification
func (r *FiberRouter) serveSwaggerJSON(c fiber.Ctx) error {
	// Read the generated swagger.json file
	swaggerData, err := os.ReadFile("docs/swagger.json")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(dto.APIResponse{
			Success: false,
			Message: "Failed to load Swagger documentation",
			Error: dto.ErrorDetail{
				Code: "SWAGGER_LOAD_ERROR",
			},
		})
	}

	c.Set("Content-Type", "application/json")
	return c.Send(swaggerData)
}

// Serve standalone Swagger UI HTML page
func (r *FiberRouter) serveStandaloneSwaggerUI(c fiber.Ctx) error {
	// Read the standalone HTML file
	htmlData, err := os.ReadFile("docs/swagger-ui-standalone.html")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(dto.APIResponse{
			Success: false,
			Message: "Failed to load standalone Swagger UI",
			Error: dto.ErrorDetail{
				Code: "SWAGGER_UI_LOAD_ERROR",
			},
		})
	}

	c.Set("Content-Type", "text/html")
	return c.Send(htmlData)
}

// Not found handler
func (r *FiberRouter) notFoundHandler(c fiber.Ctx) error {
	requestID := requestid.FromContext(c)

	return c.Status(fiber.StatusNotFound).JSON(dto.APIResponse{
		Success: false,
		Message: "The requested resource was not found",
		Error: dto.ErrorDetail{
			Code: "NOT_FOUND",
			Details: fiber.Map{
				"path":       c.Path(),
				"method":     c.Method(),
				"request_id": requestID,
			},
		},
	})
}

// Global error handler
func errorHandler(c fiber.Ctx, err error) error {
	// Default error code
	code := fiber.StatusInternalServerError
	message := "An internal server error occurred"
	errorCode := "INTERNAL_ERROR"

	// Retrieve the custom status code if it's a fiber.*Error
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		if code >= fiber.StatusBadRequest && code < fiber.StatusInternalServerError {
			message = e.Message
			errorCode = strings.ToUpper(strings.ReplaceAll(e.Message, " ", "_"))
		}
	}

	// A 408 may be raised by fasthttp before it has parsed a complete HTTP
	// request. In that case it is not associated with an API route (and the
	// default GET / values are placeholders), so say that explicitly instead
	// of emitting a misleading path. When the request is complete, retain the
	// safe endpoint details needed to investigate it. Query values are not
	// logged here because they may contain credentials.
	if code == fiber.StatusRequestTimeout {
		logRequestTimeout(c, err)
	} else {
		log.Printf("Error %d: %v", code, err)
	}
	observability.CaptureError(c, code, err, "fiber_error_handler")

	// Get RequestID for tracing
	requestID := requestid.FromContext(c)

	// Return JSON error response
	return c.Status(code).JSON(dto.APIResponse{
		Success: false,
		Message: message,
		Error: dto.ErrorDetail{
			Code: errorCode,
			Details: fiber.Map{
				"timestamp":  utils.UTCNow().Unix(),
				"request_id": requestID,
			},
		},
	})
}

func logRequestTimeout(c fiber.Ctx, err error) {
	requestComplete := c.Host() != "" || c.OriginalURL() != "/"
	fields := map[string]any{
		"level":            "warning",
		"event":            "http_request_timeout",
		"status":           fiber.StatusRequestTimeout,
		"error":            err.Error(),
		"request_complete": requestComplete,
		"request_id":       requestid.FromContext(c),
		"client_ip":        c.IP(),
		"user_agent":       c.Get("User-Agent"),
	}
	if requestComplete {
		scheme := c.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
		}
		fields["method"] = c.Method()
		fields["path"] = c.Path()
		fields["host"] = c.Host()
		if c.Host() == "" {
			fields["url"] = c.Path()
		} else {
			fields["url"] = scheme + "://" + c.Host() + c.Path()
		}
	} else {
		fields["method"] = "<unavailable>"
		fields["path"] = "<unavailable>"
		fields["host"] = "<unavailable>"
		fields["url"] = "<unavailable>"
	}

	payload, marshalErr := json.Marshal(fields)
	if marshalErr != nil {
		log.Printf("Error %d: %v", fiber.StatusRequestTimeout, err)
		return
	}
	log.Print(string(payload))
}

// Helper functions

// generateRequestID creates a unique request ID
func generateRequestID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(bytes)
}

func jsonLogString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

// contains checks if a string contains a substring
func contains(str, substr string) bool {
	return strings.Contains(str, substr)
}

// GetRouteDocumentation returns API documentation
func GetRouteDocumentation() []map[string]any {
	return []map[string]any{}
}
