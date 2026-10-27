// Package scheduler
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TODO: Tx management in queries, especially around processed_campaign creation and audience fetching to ensure consistency

const (
	smsSendBatchSize     = 200 // NOTE: MUST BE LESS THAN 250
	smsStatusJobMaxRetry = 3
)

type SMSCampaignScheduler struct {
	audRepo   repository.AudienceProfileRepository
	tagRepo   repository.TagRepository
	lineRepo  repository.LineNumberRepository
	sentRepo  repository.SentSMSRepository
	pcRepo    repository.ProcessedCampaignRepository
	jobRepo   repository.CampaignStatusJobRepository
	resRepo   repository.SMSStatusResultRepository
	statsRepo repository.SrcLayerAllStatsRepository
	notifier  NotificationSender
	logger    *log.Logger
	interval  time.Duration

	db       *gorm.DB
	adminCfg config.AdminConfig
	botCfg   config.BotConfig

	botClient BotClient
	smsClient PayamSMSClient
	providers *SMSProviderRegistry

	logFile *os.File

	schedulerName string

	bundleAudienceCache *BundleAudienceCache
}

// NotificationSender is a minimal interface extracted from NotificationService for SMS
// This keeps the scheduler independent and easy to test
type NotificationSender interface {
	SendSMS(ctx context.Context, to string, message string, trackingID *int64) error
	SendSMSBulk(ctx context.Context, mobiles []string, message string, trackingID *int64) error
}

func NewCampaignScheduler(
	audRepo repository.AudienceProfileRepository,
	tagRepo repository.TagRepository,
	lineRepo repository.LineNumberRepository,
	sentRepo repository.SentSMSRepository,
	pcRepo repository.ProcessedCampaignRepository,
	jobRepo repository.CampaignStatusJobRepository,
	resRepo repository.SMSStatusResultRepository,
	statsRepo repository.SrcLayerAllStatsRepository,
	notifier NotificationSender,
	db *gorm.DB,
	logger *log.Logger,
	interval time.Duration,
	payamSMSCfg config.PayamSMSConfig,
	candooSMSCfg config.CandooSMSConfig,
	botCfg config.BotConfig,
	adminCfg config.AdminConfig,
	messageSendMockEnabled bool,
) *SMSCampaignScheduler {
	if interval <= 0 {
		interval = time.Minute
	}

	if botCfg.APIDomain == "" {
		botCfg.APIDomain = defaultBotAPIDomain
	}

	payamClient := maybeMockPayamSMSClient(newHTTPPayamSMSClient(payamSMSCfg), messageSendMockEnabled)
	candooProvider := NewCandooSMSProvider(candooSMSCfg)
	if messageSendMockEnabled {
		candooProvider = maybeMockSMSProvider(candooProvider, true)
	}
	s := &SMSCampaignScheduler{
		audRepo:             audRepo,
		tagRepo:             tagRepo,
		lineRepo:            lineRepo,
		sentRepo:            sentRepo,
		pcRepo:              pcRepo,
		jobRepo:             jobRepo,
		resRepo:             resRepo,
		statsRepo:           statsRepo,
		notifier:            notifier,
		logger:              logger,
		db:                  db,
		interval:            interval,
		adminCfg:            adminCfg,
		botCfg:              botCfg,
		botClient:           newHTTPBotClient(botCfg),
		smsClient:           payamClient,
		providers:           NewSMSProviderRegistry(newPayamSMSProvider(payamClient), candooProvider),
		bundleAudienceCache: NewBundleAudienceCache(repository.NewBundleAudienceSelectionRepository(db)),
		schedulerName:       "sms",
	}

	if err := s.initSchedulerLogger(); err != nil {
		s.logger = log.New(log.Default().Writer(), "sms_scheduler ", log.LstdFlags|log.Lmicroseconds|log.LUTC)
		s.logger.Printf("SMS scheduler: failed to initialize file logger: %v", err)
	}

	return s
}

func (s *SMSCampaignScheduler) initSchedulerLogger() error {
	l, f, err := initSchedulerLogger(s.schedulerName + "_scheduler")
	if err != nil {
		return err
	}
	s.logFile = f
	s.logger = l
	return nil
}

func (s *SMSCampaignScheduler) Start(parent context.Context) func() {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-parent.Done():
				return
			case <-ticker.C:
				func() {
					ctx, cancel := context.WithTimeout(parent, 20*time.Minute) // TODO:
					defer cancel()
					s.runOnce(ctx, parent)
				}()
			}
		}
	}()

	go s.startStatusJobWorker(parent)

	return func() {
		if s.logFile != nil {
			_ = s.logFile.Close()
		}
	}
}

func (s *SMSCampaignScheduler) runOnce(ctx context.Context, parent context.Context) {
	recoverStaleCampaignRuns(ctx, s.db, s.logger, "SMS")
	jazzAccessToken, err := s.botClient.Login(ctx)
	if err != nil {
		s.logger.Printf("SMS scheduler: bot login failed: %v", err)
		s.notifyAdmin(fmt.Sprintf("SMS Scheduler: bot login failed: %v", err))
		return
	}

	ready, err := s.botClient.ListReadyCampaigns(ctx, jazzAccessToken, models.CampaignPlatformSMS)
	if err != nil {
		s.logger.Printf("SMS scheduler: list ready campaigns failed: %v", err)
		s.notifyAdmin(fmt.Sprintf("SMS Scheduler: list ready campaigns failed: %v", err))
		return
	}
	if len(ready) == 0 {
		return
	}
	s.logger.Printf("SMS scheduler: listed %d ready campaigns", len(ready))

	pending := make([]dto.BotGetCampaignResponse, 0, len(ready))
	for _, c := range ready {
		if strings.ToLower(strings.TrimSpace(c.Platform)) != models.CampaignPlatformSMS {
			s.logger.Printf("SMS scheduler: campaign id=%d has unsupported platform %q, skipping", c.ID, c.Platform)
			s.notifyAdmin(fmt.Sprintf("SMS Scheduler: campaign id=%d has unsupported platform %q, skipping", c.ID, c.Platform))
			continue
		}
		if err := s.validateSMSCampaign(c); err != nil {
			s.logger.Printf("SMS scheduler: validate campaign failed for campaign id=%d (skipped): %v", c.ID, err)
			s.notifyAdmin(fmt.Sprintf("SMS Scheduler: validate campaign failed for id=%d: %v", c.ID, err))
			continue
		}
		pc, err := s.pcRepo.ByCampaignID(ctx, c.ID)
		if err != nil {
			s.logger.Printf("SMS scheduler: check processed failed for campaign id=%d (skipped): %v", c.ID, err)
			s.notifyAdmin(fmt.Sprintf("SMS Scheduler: check processed failed for id=%d: %v", c.ID, err))
			continue
		}
		if pc == nil {
			pending = append(pending, c)
		} else {
			s.logger.Printf("SMS scheduler: campaign id=%d already processed, skipping", c.ID)
		}
	}
	if len(pending) == 0 {
		return
	}
	s.logger.Printf("SMS scheduler: %d campaigns pending processing...", len(pending))

	s.dispatchPendingSMSCampaigns(parent, jazzAccessToken, pending, s.processSMSCampaign)
}

type smsCampaignDispatchGroup struct {
	campaigns []dto.BotGetCampaignResponse
}

func groupSMSCampaignsForDispatch(pending []dto.BotGetCampaignResponse) []smsCampaignDispatchGroup {
	groups := make([]smsCampaignDispatchGroup, 0, len(pending))
	groupByKey := make(map[string]int, len(pending))

	for _, camp := range pending {
		key := fmt.Sprintf("campaign:%d", camp.ID)
		if camp.BundleID != nil {
			key = fmt.Sprintf("bundle:%d:%d", camp.CustomerID, *camp.BundleID)
		}

		if idx, ok := groupByKey[key]; ok {
			groups[idx].campaigns = append(groups[idx].campaigns, camp)
			continue
		}

		groupByKey[key] = len(groups)
		groups = append(groups, smsCampaignDispatchGroup{
			campaigns: []dto.BotGetCampaignResponse{camp},
		})
	}

	return groups
}

func (s *SMSCampaignScheduler) dispatchPendingSMSCampaigns(
	parent context.Context,
	jazzAccessToken string,
	pending []dto.BotGetCampaignResponse,
	process func(context.Context, string, dto.BotGetCampaignResponse) error,
) {
	for _, group := range groupSMSCampaignsForDispatch(pending) {
		go func(g smsCampaignDispatchGroup) {
			for _, camp := range g.campaigns {
				if parent.Err() != nil {
					return
				}

				ctx2, cancel2 := context.WithTimeout(parent, campaignExecutionTimeout)
				if err := process(ctx2, jazzAccessToken, camp); err != nil {
					s.logger.Printf("SMS scheduler: process campaign id=%d failed: %v", camp.ID, err)
					s.notifyAdmin(fmt.Sprintf("SMS Scheduler: process campaign failed for campaign id=%d: %v", camp.ID, err))
				}
				cancel2()
			}
		}(group)
	}
}

func (s *SMSCampaignScheduler) processSMSCampaign(ctx context.Context, jazzAccessToken string, c dto.BotGetCampaignResponse) (err error) {
	// Sender from campaign line number
	if c.LineNumber == nil {
		return fmt.Errorf("resolve SMS sender for campaign id=%d: sender is nil", c.ID)
	}
	requestedAudienceCount, err := schedulerConfiguredAudienceCount(c)
	if err != nil {
		return err
	}
	sender := *c.LineNumber
	providerName, provider, err := s.resolveCampaignSMSProvider(ctx, sender)
	if err != nil {
		return fmt.Errorf("resolve SMS provider for campaign id=%d: %w", c.ID, err)
	}
	if readiness, ok := provider.(SMSProviderReadinessChecker); ok {
		if err := readiness.Validate(); err != nil {
			return fmt.Errorf("validate SMS provider %q for campaign id=%d: %w", providerName, c.ID, err)
		}
	}

	if err := s.botClient.MoveCampaignToRunning(ctx, jazzAccessToken, c.ID); err != nil {
		return fmt.Errorf("move campaign id=%d to running: %w", c.ID, err)
	}
	defer releaseUnpreparedCampaignOnFailure(s.db, s.logger, "SMS", c.ID, &err)
	s.logger.Printf("SMS scheduler: campaign id=%d moved to running", c.ID)
	if err := repository.TouchRunningCampaign(ctx, s.db, c.ID); err != nil {
		return fmt.Errorf("heartbeat running campaign id=%d: %w", c.ID, err)
	}

	// Fetch audience data OUTSIDE any DB transaction.
	// AllocateShortLinks and DownloadTargetAudienceExcelFile are external HTTP calls that can
	// take 60+ seconds for large audiences. Holding a Postgres transaction open during these
	// calls triggers idle_in_transaction_session_timeout, killing the connection with
	// "driver: bad connection" on the next SQL statement.
	var (
		phones                    []string
		ids                       []int64
		uids                      []string
		codes                     []string
		unmatchedUID              []string
		bundleAudienceSelectionID *uint
	)
	if usesExcelAudienceTargeting(c) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired before fetching excel UIDs for campaign id=%d: %w", c.ID, err)
		}
		s.logger.Printf("SMS scheduler: campaign id=%d fetching audience UIDs from excel", c.ID)
		fileUIDs, err := fetchTargetAudienceUIDsFromExcel(ctx, s.botClient, jazzAccessToken, c.ID)
		if err != nil {
			return fmt.Errorf("fetch excel UIDs for campaign id=%d: %w", c.ID, err)
		}
		s.logger.Printf("SMS scheduler: campaign id=%d resolving %d UIDs to phones", c.ID, len(fileUIDs))
		excelShortLinkDomain := ""
		if c.ShortLinkDomain != nil {
			excelShortLinkDomain = *c.ShortLinkDomain
		}
		audienceResult, err := fetchAudiencePhonesByUIDs(ctx, s.logger, s.audRepo, s.botClient, c, jazzAccessToken, fileUIDs, excelShortLinkDomain)
		if err != nil {
			return fmt.Errorf("fetch audience phones by UIDs for campaign id=%d: %w", c.ID, err)
		}
		phones = audienceResult.Phones
		ids = audienceResult.IDs
		uids = audienceResult.UIDs
		codes = audienceResult.Codes
		unmatchedUID = audienceResult.UnmatchedUIDs
		s.logger.Printf("SMS scheduler: campaign id=%d fetched %d phones via excel (unmatched=%d)", c.ID, len(phones), len(unmatchedUID))
	} else {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired before fetching audiences for campaign id=%d: %w", c.ID, err)
		}
		// Audience eligibility is platform-neutral; deterministic ordering is
		// enforced by the repository.
		correlationID := uuid.NewString()
		s.logger.Printf("SMS scheduler: campaign id=%d fetching audience phones (correlation_id=%s)", c.ID, correlationID)
		var (
			audienceResult *AudiencePhonesResult
			err            error
		)
		if c.BundleID == nil || *c.BundleID == 0 {
			return fmt.Errorf("campaign id=%d has no bundle", c.ID)
		}
		audienceResult, err = s.fetchSMSAudiencePhonesByBundle(ctx, c, jazzAccessToken, correlationID, providerName)
		if err != nil {
			return fmt.Errorf("fetch audience phones for campaign id=%d: %w", c.ID, err)
		}
		phones = audienceResult.Phones
		ids = audienceResult.IDs
		uids = audienceResult.UIDs
		codes = audienceResult.Codes
		bundleAudienceSelectionID, err = bundleSelectionIDFromAudienceResult(audienceResult)
		if err != nil {
			return fmt.Errorf("resolve selection id for campaign id=%d: %w", c.ID, err)
		}
		s.logger.Printf("SMS scheduler: campaign id=%d fetched %d phones (bundle_audience_selection_id=%d)", c.ID, len(phones), *bundleAudienceSelectionID)
	}

	if len(ids) != len(phones) {
		return fmt.Errorf("audience ids mismatch for campaign id=%d: phones=%d ids=%d", c.ID, len(phones), len(ids))
	}
	if len(codes) != len(phones) {
		return fmt.Errorf("audience codes mismatch for campaign id=%d: phones=%d codes=%d", c.ID, len(phones), len(codes))
	}
	notifyAudienceShortfall(s.logger, s.notifier, s.adminCfg, "SMS", c.ID, requestedAudienceCount, len(ids))
	s.logger.Printf("SMS scheduler: campaign id=%d audience ready: phones=%d unmatched=%d", c.ID, len(phones), len(unmatchedUID))

	campaignJSON, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal campaign id=%d: %w", c.ID, err)
	}

	// Persist ProcessedCampaign and all audience data in one focused transaction.
	// No external calls here — the transaction stays short and the connection stays active.
	var pc *models.ProcessedCampaign
	if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		pc = &models.ProcessedCampaign{
			CampaignID:                c.ID,
			CampaignJSON:              json.RawMessage(campaignJSON),
			AudienceIDs:               pq.Int64Array{},
			AudienceCodes:             []string{},
			LastAudienceID:            nil,
			BundleAudienceSelectionID: bundleAudienceSelectionID,
			Statistics:                nil,
		}
		if err := s.pcRepo.Save(txCtx, pc); err != nil {
			return fmt.Errorf("save processed campaign: %w", err)
		}
		s.logger.Printf("SMS scheduler: persisted processed campaign id=%d for campaign id=%d", pc.ID, c.ID)

		for start := 0; start < len(ids); start += audienceAppendBatchSize {
			end := min(start+audienceAppendBatchSize, len(ids))
			if err := s.pcRepo.AppendAudienceData(txCtx, pc.ID, ids[start:end], codes[start:end]); err != nil {
				return fmt.Errorf("append audience batch [%d,%d): %w", start, end, err)
			}
		}
		pc.UpdatedAt = utils.UTCNow()
		if err := s.pcRepo.UpdateMeta(txCtx, pc); err != nil {
			return fmt.Errorf("update processed campaign meta: %w", err)
		}
		s.logger.Printf("SMS scheduler: updated processed campaign id=%d with %d audience ids", pc.ID, len(ids))
		return nil
	}); err != nil {
		return fmt.Errorf("persist campaign data for campaign id=%d: %w", c.ID, err)
	}
	s.logger.Printf("SMS scheduler: persisted processed campaign id=%d num_phones=%d, num_ids=%d, num_codes=%d, num_unmatched=%d", pc.ID, len(phones), len(ids), len(codes), len(unmatchedUID))

	if len(unmatchedUID) > 0 {
		s.logger.Printf("SMS scheduler: campaign id=%d creating %d unmatched sent rows for processed_campaign_id=%d", c.ID, len(unmatchedUID), pc.ID)
		if err := s.createUnmatchedSentSMSRows(ctx, pc.ID, unmatchedUID, providerName); err != nil {
			return fmt.Errorf("create unmatched sent rows for campaign id=%d: %w", c.ID, err)
		}
	}

	providerBatchSize := provider.MaxBatchSize()
	if providerBatchSize <= 0 {
		return fmt.Errorf("SMS provider %q returned an invalid batch size %d", providerName, providerBatchSize)
	}
	providerBatchSize = min(smsSendBatchSize, providerBatchSize)
	for start := 0; start < len(phones); start += providerBatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired at batch start=%d for campaign id=%d: %w", start, c.ID, err)
		}

		end := min(start+providerBatchSize, len(phones))
		batchPhones := phones[start:end]
		batchIDs := ids[start:end]
		batchUIDs := uids[start:end]
		batchCodes := codes[start:end]

		items := make([]SMSProviderMessage, 0, len(batchPhones))
		rows := make([]*models.SentSMS, 0, len(batchPhones))

		s.logger.Printf("SMS scheduler: campaign id=%d allocating tracking ids for batch [%d,%d)", c.ID, start, end)
		trackingIDs, err := allocateTrackingIDs(ctx, s.db, len(batchPhones))
		if err != nil {
			return fmt.Errorf("allocate tracking ids for batch [%d,%d) campaign id=%d: %w", start, end, c.ID, err)
		}
		var providerCustomerIDs []int64
		if providerName == models.SMSProviderCandoo {
			providerCustomerIDs, err = allocateCandooCustomerIDs(ctx, s.db, len(batchPhones))
			if err != nil {
				return fmt.Errorf("allocate Candoo customer ids for batch [%d,%d) campaign id=%d: %w", start, end, c.ID, err)
			}
		}

		for i, p := range batchPhones {
			body := s.buildSMSBody(c, batchCodes[i], batchUIDs[i])
			trackingID := trackingIDs[i]
			var providerCustomerID *int64
			if len(providerCustomerIDs) > 0 {
				providerCustomerID = utils.ToPtr(providerCustomerIDs[i])
			}
			items = append(items, SMSProviderMessage{
				Recipient:          p,
				Body:               body,
				TrackingID:         trackingID,
				ProviderCustomerID: providerCustomerID,
			})
			rows = append(rows, &models.SentSMS{
				ProcessedCampaignID: pc.ID,
				PhoneNumber:         p,
				PartsDelivered:      0,
				Status:              models.SMSSendStatusPending,
				TrackingID:          trackingID,
				Provider:            providerName,
				ProviderCustomerID:  providerCustomerID,
			})
		}

		lastBatchID := batchIDs[len(batchIDs)-1]
		if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
			if len(rows) > 0 {
				if err := s.sentRepo.SaveBatch(txCtx, rows); err != nil {
					return fmt.Errorf("save batch rows: %w", err)
				}
			}
			pc.LastAudienceID = utils.ToPtr(lastBatchID)
			pc.UpdatedAt = utils.UTCNow()
			if err := s.pcRepo.UpdateMeta(txCtx, pc); err != nil {
				return fmt.Errorf("update meta: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("save batch [%d,%d) for campaign id=%d: %w", start, end, c.ID, err)
		}
		s.logger.Printf("SMS scheduler: campaign id=%d batch [%d,%d) saved, sending to SMS provider", c.ID, start, end)
		if err := repository.TouchRunningCampaign(ctx, s.db, c.ID); err != nil {
			return fmt.Errorf("heartbeat before provider batch [%d,%d) campaign id=%d: %w", start, end, c.ID, err)
		}

		batchResult, batchErr := provider.SendBatch(ctx, sender, items)
		if batchErr != nil {
			s.logger.Printf("SMS scheduler: provider=%s send batch [%d,%d) failed for campaign id=%d: %v", providerName, start, end, c.ID, batchErr)
			smsProviderSendBatchesTotal.WithLabelValues(string(providerName), "error").Inc()
			if providerName == models.SMSProviderCandoo {
				s.notifyAdmin(fmt.Sprintf("SMS Scheduler: Candoo send batch failed for campaign id=%d: %v", c.ID, batchErr))
			}
		} else {
			smsProviderSendBatchesTotal.WithLabelValues(string(providerName), "success").Inc()
		}
		if auditErr := s.persistSMSProviderSendAttempt(ctx, pc.ID, providerName, items, batchResult, batchErr); auditErr != nil {
			s.logger.Printf("SMS scheduler: failed to persist %s send response for campaign id=%d batch [%d,%d): %v", providerName, c.ID, start, end, auditErr)
			s.notifyAdmin(fmt.Sprintf("SMS Scheduler: failed to persist %s send response for campaign id=%d: %v", providerName, c.ID, auditErr))
		}
		if providerName == models.SMSProviderPayamSMS {
			payamItems, payamResult := payamAuditInput(items, batchResult)
			if auditErr := s.persistPayamSMSSendResponse(ctx, pc.ID, payamItems, payamResult, batchErr); auditErr != nil {
				s.logger.Printf("SMS scheduler: failed to persist PayamSMS compatibility audit for campaign id=%d batch [%d,%d): %v", c.ID, start, end, auditErr)
			}
		}

		responseByTrackingID := make(map[string]*SMSProviderSendItem, len(batchResult.Items))
		for i := range batchResult.Items {
			resp := batchResult.Items[i]
			trackingID := strings.TrimSpace(resp.TrackingID)
			if trackingID == "" {
				continue
			}
			respCopy := resp
			responseByTrackingID[trackingID] = &respCopy
		}
		s.logger.Printf("SMS scheduler: campaign id=%d provider=%s batch [%d,%d) responded: sent=%d responses=%d", c.ID, providerName, start, end, len(items), len(batchResult.Items))

		sendUpdates := make([]repository.SentSMSProviderUpdate, 0, len(items))
		statusTrackingIDs := make([]string, 0, len(items))
		immediateOutcomes := make([]SMSProviderSendItem, 0, len(items))
		for _, item := range items {
			trackingID := strings.TrimSpace(item.TrackingID)
			if trackingID == "" {
				continue
			}
			outcome := responseByTrackingID[trackingID]
			update := buildGenericSMSProviderUpdate(providerName, trackingID, item.ProviderCustomerID, outcome, batchErr)
			update.ProcessedCampaignID = utils.ToPtr(pc.ID)
			sendUpdates = append(sendUpdates, update)
			if providerName == models.SMSProviderPayamSMS || (outcome != nil && outcome.TrackDeliveryStatus) {
				statusTrackingIDs = append(statusTrackingIDs, trackingID)
			} else {
				if outcome == nil {
					missing := genericMissingSMSOutcome(trackingID, item.ProviderCustomerID, batchErr)
					outcome = &missing
				}
				immediateOutcomes = append(immediateOutcomes, *outcome)
			}
			if outcome == nil {
				smsProviderMessageOutcomesTotal.WithLabelValues(string(providerName), "unknown").Inc()
			} else if outcome.InternalStatus == models.SMSSendStatusUnsuccessful {
				smsProviderMessageOutcomesTotal.WithLabelValues(string(providerName), "rejected").Inc()
			} else if outcome.TrackDeliveryStatus {
				smsProviderMessageOutcomesTotal.WithLabelValues(string(providerName), "accepted").Inc()
			} else {
				smsProviderMessageOutcomesTotal.WithLabelValues(string(providerName), "unknown").Inc()
			}
		}
		if len(sendUpdates) > 0 {
			if updateErr := s.sentRepo.UpdateProviderFieldsByTrackingIDs(ctx, sendUpdates); updateErr != nil {
				s.logger.Printf("SMS scheduler: failed to batch update sent_sms provider fields for campaign id=%d: %v", c.ID, updateErr)
				// NOTE: Error silent here; not returning to avoid blocking further processing
			}
		}

		if err := s.recordImmediateSMSOutcomes(ctx, pc.ID, providerName, immediateOutcomes); err != nil {
			s.logger.Printf("SMS scheduler: failed to record immediate outcomes for campaign id=%d: %v", c.ID, err)
		}
		if err := s.scheduleStatusCheckJobs(ctx, pc.ID, providerName, statusTrackingIDs); err != nil {
			s.logger.Printf("SMS scheduler: failed to schedule status jobs for campaign id=%d: %v", c.ID, err)
			// NOTE: Error silent here; not returning to avoid blocking further processing
		}
		s.logger.Printf("SMS scheduler: campaign id=%d batch [%d,%d) done", c.ID, start, end)
	}

	stats, err := preparedCampaignStatistics(ctx, s.pcRepo, pc, len(phones), s.updateProcessedCampaignStats)
	if err != nil {
		return fmt.Errorf("update stats for campaign id=%d: %w", c.ID, err)
	}
	if shouldPushPreparedCampaignStatistics(stats, len(phones)) {
		if err := s.botClient.PushCampaignStatistics(ctx, c.ID, stats); err != nil {
			return fmt.Errorf("push statistics for campaign id=%d: %w", c.ID, err)
		}
	}

	s.logger.Printf("SMS scheduler: campaign id=%d all batches sent", c.ID)

	if err := s.botClient.MoveCampaignToExecuted(ctx, jazzAccessToken, c.ID); err != nil {
		return fmt.Errorf("move campaign id=%d to executed: %w", c.ID, err)
	}
	s.logger.Printf("SMS scheduler: campaign id=%d moved to executed", c.ID)

	if len(uids) > 0 {
		go func(campaignID uint, uids, codes []string) {
			pushCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := s.botClient.PushCampaignAudienceUIDs(pushCtx, campaignID, uids, codes); err != nil {
				s.logger.Printf("SMS scheduler: push audience UIDs failed for campaign id=%d: %v", campaignID, err)
				s.notifyAdmin(fmt.Sprintf("SMS Scheduler: push audience UIDs failed for campaign id=%d: %v", campaignID, err))
			}
		}(c.ID, uids, codes)
	}

	return nil
}

func (s *SMSCampaignScheduler) validateSMSCampaign(c dto.BotGetCampaignResponse) error {
	if c.Status != string(models.CampaignStatusApproved) {
		return fmt.Errorf("campaign status is not approved")
	}
	now := utils.UTCNow()
	if c.ScheduleAt != nil && c.ScheduleAt.After(now) {
		return fmt.Errorf("campaign schedule_at is after now")
	}
	if c.CreatedAt.After(now) {
		return fmt.Errorf("campaign created_at is after now")
	}
	if c.UpdatedAt != nil && c.UpdatedAt.After(now) {
		return fmt.Errorf("campaign updated_at is after now")
	}
	if c.LineNumber == nil || *c.LineNumber == "" {
		return fmt.Errorf("campaign line number (sender) is empty")
	}
	if strings.ToLower(strings.TrimSpace(c.Platform)) != models.CampaignPlatformSMS {
		return fmt.Errorf("campaign platform is not sms")
	}
	return nil
}

func (s *SMSCampaignScheduler) resolveCampaignSMSProvider(ctx context.Context, sender string) (models.SMSProvider, SMSProvider, error) {
	if s.lineRepo == nil {
		provider, err := s.providers.Provider(models.SMSProviderPayamSMS)
		if err != nil {
			return "", nil, err
		}
		s.logger.Printf("SMS scheduler: line number repository is unavailable; defaulting sender=%q to PayamSMS", sender)
		return models.SMSProviderPayamSMS, provider, nil
	}
	line, err := s.lineRepo.ByValue(ctx, strings.TrimSpace(sender))
	if err != nil {
		return "", nil, err
	}
	if line == nil {
		// The legacy scheduler only knew the sender string. Preserve that
		// behavior for an old campaign whose line configuration was removed:
		// missing configuration must not silently become a Candoo send.
		provider, providerErr := s.providers.Provider(models.SMSProviderPayamSMS)
		if providerErr != nil {
			return "", nil, providerErr
		}
		s.logger.Printf("SMS scheduler: sender=%q has no line-number configuration; defaulting to PayamSMS", sender)
		return models.SMSProviderPayamSMS, provider, nil
	}
	providerName, err := normalizeSMSProvider(line.Provider)
	if err != nil {
		return "", nil, err
	}
	provider, err := s.providers.Provider(providerName)
	if err != nil {
		return "", nil, err
	}
	return providerName, provider, nil
}

// resolveScoreConstraint looks up the percentile thresholds from src_layer_all_stats
// for the campaign's levels and converts AudienceGrades into a score filter.
// Returns nil when no filter is needed (grades are empty or [A,B,C]). A
// restricted grade set fails closed when percentile statistics are missing.
func (s *SMSCampaignScheduler) resolveScoreConstraint(ctx context.Context, c dto.BotGetCampaignResponse) (*models.NormalizedScoreConstraint, error) {
	if usesSmartAudienceTargeting(c) {
		return nil, nil
	}
	if !gradesNeedScoreFilter(c.AudienceGrades) {
		return nil, nil
	}
	if s.statsRepo == nil {
		return nil, fmt.Errorf("audience score statistics repository is unavailable for campaign id=%d with grades=%v", c.ID, c.AudienceGrades)
	}
	percentiles, err := s.statsRepo.FetchPercentiles(ctx, c.Level1, c.Level2s, c.Level3s)
	if err != nil {
		return nil, fmt.Errorf("fetch percentiles for campaign id=%d: %w", c.ID, err)
	}
	if percentiles == nil {
		return nil, fmt.Errorf("audience score statistics are missing for campaign id=%d levels and grades=%v", c.ID, c.AudienceGrades)
	}
	s.logger.Printf("resolveScoreConstraint: campaign id=%d grades=%v p33=%.4f p66=%.4f", c.ID, c.AudienceGrades, percentiles.P33, percentiles.P66)
	return gradesToScoreConstraint(c.AudienceGrades, percentiles.P33, percentiles.P66), nil
}

// selectTagAudiences applies the delivery provider's color eligibility. Payam
// recipients are selected white-first then pink; Candoo has no color filter.
func (s *SMSCampaignScheduler) selectTagAudiences(
	ctx context.Context,
	campaignID uint,
	tagIDs pq.Int32Array,
	numAudiences int64,
	exclude map[int64]struct{},
	excludeBundleID *uint,
	scoreConstraint *models.NormalizedScoreConstraint,
	allowedColors []string,
) (phones []string, ids []int64, uids []string, err error) {
	if numAudiences <= 0 {
		return []string{}, []int64{}, []string{}, nil
	}
	limit, err := checkedAudienceQueryLimit(numAudiences)
	if err != nil {
		return nil, nil, nil, err
	}

	phones = make([]string, 0, numAudiences)
	ids = make([]int64, 0, numAudiences)
	uids = make([]string, 0, numAudiences)
	excludeIDs := audienceIDsFromSet(exclude)

	appendCandidate := func(ap *models.AudienceProfile) {
		if ap == nil || ap.PhoneNumber == nil || strings.TrimSpace(*ap.PhoneNumber) == "" {
			return
		}
		phones = append(phones, strings.TrimSpace(*ap.PhoneNumber))
		ids = append(ids, int64(ap.ID))
		uids = append(uids, ap.UID)
	}

	selectColor := func(color *string, colorLimit int) error {
		if colorLimit <= 0 {
			return nil
		}
		candidates, err := s.audRepo.SelectCampaignCandidates(ctx, models.AudienceProfileFilter{
			Tags:            &tagIDs,
			Color:           color,
			NormalizedScore: scoreConstraint,
			ExcludeBundleID: excludeBundleID,
		}, excludeIDs, colorLimit)
		if err != nil {
			return err
		}
		colorLabel := "unrestricted"
		if color != nil {
			colorLabel = *color
		}
		s.logger.Printf("selectTagAudiences %s candidates: campaign_id=%d count=%d limit=%d excluded=%d", colorLabel, campaignID, len(candidates), colorLimit, len(excludeIDs))
		for _, ap := range candidates {
			appendCandidate(ap)
		}
		return nil
	}

	if len(allowedColors) == 0 {
		if err := selectColor(nil, limit); err != nil {
			s.logger.Printf("selectTagAudiences fetch unrestricted candidates failed: campaign_id=%d err=%v", campaignID, err)
			return nil, nil, nil, err
		}
		return phones, ids, uids, nil
	}
	for _, color := range allowedColors {
		remaining := limit - len(ids)
		if err := selectColor(utils.ToPtr(color), remaining); err != nil {
			s.logger.Printf("selectTagAudiences fetch %s failed: campaign_id=%d err=%v", color, campaignID, err)
			return nil, nil, nil, err
		}
	}

	return phones, ids, uids, nil
}

// fetchSMSAudiencePhonesByBundle selects audiences for a campaign that belongs to a bundle.
// Uniqueness is enforced across all campaigns in the bundle:
// audiences already selected by earlier campaigns in the same bundle are excluded.
// Unlike the tag-hash path there is no rolling-window reset. Selection and
// persistence run under the Bundle lock and require the exact requested count.
func (s *SMSCampaignScheduler) fetchSMSAudiencePhonesByBundle(
	ctx context.Context,
	c dto.BotGetCampaignResponse,
	jazzAccessToken string,
	correlationID string,
	providerName models.SMSProvider,
) (*AudiencePhonesResult, error) {
	bundleID := *c.BundleID
	numAudiences, err := schedulerConfiguredAudienceCount(c)
	if err != nil {
		return nil, err
	}
	s.logger.Printf("fetchSMSAudiencePhonesByBundle start: campaign_id=%d customer_id=%d bundle_id=%d num_audiences=%d correlation_id=%s",
		c.ID, c.CustomerID, bundleID, numAudiences, correlationID)

	executionTags, tagIDs, err := resolveActiveCampaignTagIDs(ctx, s.tagRepo, c)
	if err != nil {
		s.logger.Printf("fetchSMSAudiencePhonesByBundle tags resolution failed: campaign_id=%d err=%v", c.ID, err)
		return nil, err
	}
	s.logger.Printf("fetchSMSAudiencePhonesByBundle tags resolved: campaign_id=%d requested=%d resolved=%d", c.ID, len(executionTags), len(tagIDs))

	scoreConstraint, err := s.resolveScoreConstraint(ctx, c)
	if err != nil {
		s.logger.Printf("fetchSMSAudiencePhonesByBundle resolve score constraint failed: campaign_id=%d err=%v", c.ID, err)
		return nil, err
	}

	var phones []string
	var ids []int64
	var uids []string
	var selectionID uint
	allowedColors := models.SmartTargetingAllowedColors(c.Platform, providerName)
	if usesSmartAudienceTargeting(c) {
		phones, ids, uids, selectionID, err = selectAndReserveExactSmartTargetingCandidates(ctx, s.db, c, numAudiences, correlationID, allowedColors)
	} else {
		phones, ids, uids, selectionID, err = selectAndReserveStandardBundleCandidates(
			ctx, s.db, s.bundleAudienceCache, c.ID, c.CustomerID, bundleID, numAudiences, correlationID,
			func(selectionCtx context.Context, exclude map[int64]struct{}) ([]string, []int64, []string, error) {
				return s.selectTagAudiences(selectionCtx, c.ID, tagIDs, numAudiences, exclude, &bundleID, scoreConstraint, allowedColors)
			},
			func(selectionCtx context.Context, ids []int64) ([]string, []int64, []string, error) {
				return loadReservedBundleAudience(selectionCtx, s.audRepo, ids)
			},
		)
	}
	if err != nil {
		return nil, err
	}
	if selectionID == 0 {
		return nil, fmt.Errorf("bundle audience selection was not persisted for campaign %d", c.ID)
	}
	s.logger.Printf("fetchSMSAudiencePhonesByBundle selected: campaign_id=%d bundle_id=%d selected=%d requested=%d",
		c.ID, bundleID, len(phones), numAudiences)
	if err := validateSchedulerSelectedAudienceCount(c, numAudiences, len(ids)); err != nil {
		return nil, err
	}

	s.logger.Printf("fetchSMSAudiencePhonesByBundle selection saved: campaign_id=%d bundle_id=%d selection_id=%d selected=%d",
		c.ID, bundleID, selectionID, len(ids))
	if len(phones) == 0 {
		return &AudiencePhonesResult{
			Phones:                    phones,
			IDs:                       ids,
			UIDs:                      uids,
			Codes:                     []string{},
			BundleAudienceSelectionID: utils.ToPtr(selectionID),
		}, nil
	}

	if !hasCampaignAdLink(c.AdLink) {
		s.logger.Printf("fetchSMSAudiencePhonesByBundle skipped short links: campaign_id=%d ad_link=empty", c.ID)
		return &AudiencePhonesResult{
			Phones:                    phones,
			IDs:                       ids,
			UIDs:                      uids,
			Codes:                     make([]string, len(phones)),
			BundleAudienceSelectionID: utils.ToPtr(selectionID),
		}, nil
	}

	if c.ShortLinkDomain == nil || strings.TrimSpace(*c.ShortLinkDomain) == "" {
		s.logger.Printf("fetchSMSAudiencePhonesByBundle skipped short links: campaign_id=%d short_link_domain=empty", c.ID)
		return &AudiencePhonesResult{
			Phones:                    phones,
			IDs:                       ids,
			UIDs:                      uids,
			Codes:                     make([]string, len(phones)),
			BundleAudienceSelectionID: utils.ToPtr(selectionID),
		}, nil
	}

	items := make([]dto.PhoneWithAdLink, len(phones))
	for i, p := range phones {
		adLink := c.AdLink
		if adLink != nil && strings.Contains(*adLink, "{uid}") {
			resolved := strings.ReplaceAll(*adLink, "{uid}", uids[i])
			adLink = &resolved
		}
		items[i] = dto.PhoneWithAdLink{Phone: p, AdLink: adLink}
	}
	codes, err := s.botClient.AllocateShortLinks(ctx, jazzAccessToken, &dto.BotAllocateShortLinksRequest{
		CampaignID:      c.ID,
		Items:           items,
		ShortLinkDomain: *c.ShortLinkDomain,
	})
	if err != nil {
		s.logger.Printf("fetchSMSAudiencePhonesByBundle allocate short links failed: campaign_id=%d bundle_id=%d err=%v", c.ID, bundleID, err)
		return nil, err
	}
	if len(codes) != len(phones) {
		return nil, fmt.Errorf("allocate short links length mismatch for campaign id=%d bundle_id=%d: phones=%d codes=%d", c.ID, bundleID, len(phones), len(codes))
	}
	s.logger.Printf("fetchSMSAudiencePhonesByBundle success: campaign_id=%d bundle_id=%d selected=%d codes=%d selection_id=%d",
		c.ID, bundleID, len(phones), len(codes), selectionID)
	return &AudiencePhonesResult{
		Phones:                    phones,
		IDs:                       ids,
		UIDs:                      uids,
		Codes:                     codes,
		BundleAudienceSelectionID: utils.ToPtr(selectionID),
	}, nil
}

func (s *SMSCampaignScheduler) buildSMSBody(c dto.BotGetCampaignResponse, code string, uid string) string {
	content := ""
	if c.Content != nil {
		content = *c.Content
	}
	if hasCampaignAdLink(c.AdLink) {
		if c.ShortLinkDomain != nil && *c.ShortLinkDomain != "" {
			domain := *c.ShortLinkDomain
			if !strings.HasSuffix(domain, "/") {
				domain += "/"
			}
			shortened := domain + code
			return strings.ReplaceAll(content, "{YOUR_LINK}", shortened) + "\n" + "لغو۱۱"
		}
		injected := strings.ReplaceAll(*c.AdLink, "{uid}", uid)
		return strings.ReplaceAll(content, "{YOUR_LINK}", injected) + "\n" + "لغو۱۱"
	}
	return strings.ReplaceAll(content, "{YOUR_LINK}", "") + "\n" + "لغو۱۱"
}

func (s *SMSCampaignScheduler) createUnmatchedSentSMSRows(ctx context.Context, processedCampaignID uint, unmatchedUIDs []string, provider models.SMSProvider) error {
	pc, err := s.pcRepo.ByID(ctx, processedCampaignID)
	if err != nil {
		return err
	}
	if pc == nil {
		return fmt.Errorf("processed campaign not found for processed campaign id=%d", processedCampaignID)
	}

	trackingIDs, err := allocateTrackingIDs(ctx, s.db, len(unmatchedUIDs))
	if err != nil {
		return err
	}

	const errCode = "AUDIENCE_UID_NOT_FOUND"

	fakeSentSMSs := make([]*models.SentSMS, 0, len(unmatchedUIDs))
	for i, uid := range unmatchedUIDs {
		desc := fmt.Sprintf("Audience uid not found or has no phone number: %s", uid)
		code := errCode
		fakeSentSMSs = append(fakeSentSMSs, &models.SentSMS{
			ProcessedCampaignID: processedCampaignID,
			PhoneNumber:         "",
			TrackingID:          trackingIDs[i],
			PartsDelivered:      0,
			Status:              models.SMSSendStatusUnsuccessful,
			Provider:            provider,
			ServerID:            nil,
			ErrorCode:           &code,
			Description:         &desc,
		})
	}
	if len(fakeSentSMSs) == 0 {
		return nil
	}

	var stats map[string]any
	if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := s.sentRepo.SaveBatch(txCtx, fakeSentSMSs); err != nil {
			return err
		}

		now := utils.UTCNow()
		executedAt := now.Add(time.Second)
		fakeJob := &models.CampaignStatusJob{
			ProcessedCampaignID: processedCampaignID,
			CorrelationID:       uuid.NewString(),
			Platform:            models.CampaignPlatformSMS,
			Provider:            &provider,
			TrackingIDs:         pq.StringArray(trackingIDs),
			RetryCount:          0,
			ScheduledAt:         now,
			ExecutedAt:          &executedAt,
			CreatedAt:           now,
			UpdatedAt:           now.Add(time.Second),
		}
		if err := s.jobRepo.Save(txCtx, fakeJob); err != nil {
			return err
		}

		zeroVal := int64(0)
		zero := &zeroVal
		fakeSMSStatusResults := make([]*models.SMSStatusResult, 0, len(unmatchedUIDs))
		for _, trackingID := range trackingIDs {
			status := errCode
			fakeSMSStatusResults = append(fakeSMSStatusResults, &models.SMSStatusResult{
				JobID:                 fakeJob.ID,
				ProcessedCampaignID:   fakeJob.ProcessedCampaignID,
				TrackingID:            trackingID,
				ServerID:              nil,
				Provider:              provider,
				InternalStatus:        utils.ToPtr(models.SMSSendStatusUnsuccessful),
				TotalParts:            zero,
				TotalDeliveredParts:   zero,
				TotalUndeliveredParts: zero,
				TotalUnknownParts:     zero,
				Status:                &status,
			})
		}
		if err := s.resRepo.SaveBatch(txCtx, fakeSMSStatusResults); err != nil {
			return err
		}

		var err error
		stats, err = s.updateProcessedCampaignStats(txCtx, processedCampaignID)
		return err
	}); err != nil {
		return err
	}

	if stats != nil {
		if stats["aggregatedTotalSent"] != nil && stats["aggregatedTotalSent"].(int64) > 0 {
			if err := s.botClient.PushCampaignStatistics(ctx, pc.CampaignID, stats); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *SMSCampaignScheduler) scheduleStatusCheckJobs(ctx context.Context, processedCampaignID uint, provider models.SMSProvider, trackingIDs []string) error {
	if len(trackingIDs) == 0 || s.jobRepo == nil {
		return nil
	}
	filteredTrackingIDs := make([]string, 0, len(trackingIDs))
	for _, id := range trackingIDs {
		if strings.TrimSpace(id) != "" {
			filteredTrackingIDs = append(filteredTrackingIDs, strings.TrimSpace(id))
		}
	}
	if len(filteredTrackingIDs) == 0 {
		return nil
	}

	corrID := uuid.NewString()
	now := utils.UTCNow()
	offsets := []time.Duration{1 * time.Minute, 5 * time.Minute, 15 * time.Minute, 24 * time.Hour, 48 * time.Hour}
	jobs := make([]*models.CampaignStatusJob, 0, len(offsets))
	for _, off := range offsets {
		jobs = append(jobs, &models.CampaignStatusJob{
			ProcessedCampaignID: processedCampaignID,
			CorrelationID:       corrID,
			Platform:            models.CampaignPlatformSMS,
			Provider:            &provider,
			TrackingIDs:         pq.StringArray(filteredTrackingIDs),
			RetryCount:          0,
			ScheduledAt:         now.Add(off),
			CreatedAt:           now,
			UpdatedAt:           now,
		})
	}
	return s.jobRepo.SaveBatch(ctx, jobs)
}

func (s *SMSCampaignScheduler) startStatusJobWorker(parent context.Context) {
	ticker := time.NewTicker(statusJobWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			if s.jobRepo == nil || s.resRepo == nil {
				continue
			}

			listCtx, listCancel := context.WithTimeout(parent, 30*time.Second)
			jobs, err := s.jobRepo.ListDue(listCtx, models.CampaignPlatformSMS, utils.UTCNow(), numJobsPerTick)
			listCancel()
			if err != nil {
				s.logger.Printf("SMS scheduler: list status jobs failed: %v", err)
				continue
			}
			if len(jobs) == 0 {
				continue
			}

			var (
				payamToken    string
				payamTokenErr error
				payamLoaded   bool
			)

			for i, job := range jobs {
				if parent.Err() != nil {
					return
				}

				providerName, providerErr := statusJobProvider(job)
				if providerErr != nil {
					err = providerErr
				} else if providerName == models.SMSProviderPayamSMS {
					if !payamLoaded {
						tokenCtx, tokenCancel := context.WithTimeout(parent, 30*time.Second)
						payamToken, payamTokenErr = s.smsClient.GetToken(tokenCtx)
						tokenCancel()
						payamLoaded = true
					}
					if payamTokenErr != nil {
						err = fmt.Errorf("PayamSMS token for status jobs: %w", payamTokenErr)
					} else {
						jobCtx, jobCancel := context.WithTimeout(parent, 2*time.Minute)
						err = s.handleStatusJob(jobCtx, job, payamToken)
						jobCancel()
					}
				} else {
					jobCtx, jobCancel := context.WithTimeout(parent, 2*time.Minute)
					err = s.handleStatusJob(jobCtx, job, "")
					jobCancel()
				}

				if err != nil {
					s.logger.Printf("SMS scheduler: handle status job id=%d failed: %v", job.ID, err)
					if job.RetryCount >= smsStatusJobMaxRetry {
						s.notifyAdmin(fmt.Sprintf("SMS scheduler: status job id=%d has failed %d times with error: %v", job.ID, job.RetryCount, err))
					}
				} else {
					s.logger.Printf("SMS scheduler: handle status job id=%d succeeded", job.ID)
				}

				if i < len(jobs)-1 {
					if err := sleepWithContext(parent, time.Second); err != nil {
						return
					}
				}
			}
		}
	}
}

func (s *SMSCampaignScheduler) handleStatusJob(ctx context.Context, job *models.CampaignStatusJob, jazzAccessToken string) error {
	providerName, err := statusJobProvider(job)
	if err != nil {
		return err
	}
	if providerName != models.SMSProviderPayamSMS {
		return s.handleExternalSMSStatusJob(ctx, job, providerName)
	}
	statusResult, fetchErr := s.smsClient.FetchStatus(ctx, jazzAccessToken, []string(job.TrackingIDs))
	job.RawProviderResponse = statusResult.RawResponse
	if fetchErr != nil {
		now := utils.UTCNow()
		job.RetryCount++
		msg := fetchErr.Error()
		job.Error = &msg
		job.UpdatedAt = now
		if job.RetryCount >= smsStatusJobMaxRetry {
			job.ExecutedAt = &now
		} else {
			job.ExecutedAt = nil
		}
		if err := s.jobRepo.Update(ctx, job); err != nil {
			return err
		}
		return fetchErr
	}
	statusItems := statusResult.Items

	txErr := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		now := utils.UTCNow()

		statusRows := make([]*models.SMSStatusResult, 0, len(statusItems))
		for _, item := range statusItems {
			// BUG FIX 7: was `job.TrackingIDs[idx]` (positional array index). The provider
			// API is not guaranteed to return results in the same order as the request, so
			// correlating by position silently maps status results to the wrong tracking IDs.
			// Use the TrackingID that the provider echoes back in each response item instead.
			trackingID := strings.TrimSpace(item.TrackingID)
			if trackingID == "" {
				continue
			}
			statusRows = append(statusRows, &models.SMSStatusResult{
				JobID:                 job.ID,
				ProcessedCampaignID:   job.ProcessedCampaignID,
				TrackingID:            trackingID,
				ServerID:              item.ServerID,
				Provider:              models.SMSProviderPayamSMS,
				ProviderStatusText:    utils.ToPtr(strings.TrimSpace(item.Status)),
				TotalParts:            &item.TotalParts,
				TotalDeliveredParts:   &item.TotalDeliveredParts,
				TotalUndeliveredParts: &item.TotalUndeliveredParts,
				TotalUnknownParts:     &item.TotalUnknownParts,
				Status:                &item.Status,
				Metadata:              json.RawMessage(`{}`),
			})
		}
		if err := s.resRepo.SaveBatch(txCtx, statusRows); err != nil {
			return err
		}
		job.ExecutedAt = &now
		job.Error = nil
		job.UpdatedAt = now
		return s.jobRepo.Update(txCtx, job)
	})
	if txErr != nil {
		return txErr
	}

	stats, err := s.updateProcessedCampaignStats(ctx, job.ProcessedCampaignID)
	if err != nil {
		return err
	}

	if stats != nil {
		pc, err := s.pcRepo.ByID(ctx, job.ProcessedCampaignID)
		if err != nil {
			return err
		}
		if pc == nil {
			return fmt.Errorf("processed campaign not found for processed campaign id=%d", job.ProcessedCampaignID)
		}
		if shouldPushCurrentProcessedCampaignStatistics(pc, stats) {
			if err := s.botClient.PushCampaignStatistics(ctx, pc.CampaignID, stats); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SMSCampaignScheduler) updateProcessedCampaignStats(ctx context.Context, processedCampaignID uint) (map[string]any, error) {
	pc, err := s.pcRepo.ByID(ctx, processedCampaignID)
	if err != nil {
		return nil, err
	}
	if pc == nil {
		return nil, fmt.Errorf("processed campaign not found for processed_campaign_id=%d", processedCampaignID)
	}

	agg, err := s.resRepo.AggregateByCampaign(ctx, processedCampaignID)
	if err != nil {
		return nil, err
	}

	// trackingResults, err := s.resRepo.TrackingResultsByCampaign(ctx, processedCampaignID)
	// if err != nil {
	// 	return nil, err
	// }

	// Fallback before any status jobs land.
	// if agg.AggregatedTotalRecords == 0 && len(trackingResults) == 0 {
	if agg.AggregatedTotalRecords == 0 {
		// s.logger.Printf("updateProcessedCampaignStats: no status results yet for processed_campaign_id=%d, falling back to sent rows", processedCampaignID)
		// return s.updateProcessedCampaignStatsFromSentRows(ctx, pc)
		return nil, nil
	}

	stats := map[string]any{
		"aggregatedTotalRecords":          agg.AggregatedTotalRecords,
		"aggregatedTotalSent":             agg.AggregatedTotalSent,
		"aggregatedTotalParts":            agg.AggregatedTotalParts,
		"aggregatedTotalDeliveredParts":   agg.AggregatedDeliveredParts,
		"aggregatedTotalUnDeliveredParts": agg.AggregatedUndelivered,
		"aggregatedTotalUnKnownParts":     agg.AggregatedUnknown,
		// "trackingResults":                 trackingResults,
		"updatedAt": utils.UTCNow().Format(time.RFC3339),
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return nil, err
	}
	pc.Statistics = data
	pc.UpdatedAt = utils.UTCNow()
	if err := s.pcRepo.UpdateMeta(ctx, pc); err != nil {
		return nil, err
	}
	return stats, nil
}

func (s *SMSCampaignScheduler) updateProcessedCampaignStatsFromSentRows(ctx context.Context, pc *models.ProcessedCampaign) (map[string]any, error) {
	s.logger.Printf("updateProcessedCampaignStatsFromSentRows: computing stats from sent rows for processed_campaign_id=%d", pc.ID)
	type row struct {
		Total      int64
		Successful int64
	}
	var agg row
	if err := s.db.WithContext(ctx).Table("sent_sms").
		Select(`
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN LOWER(BTRIM(status::text)) = 'successful' THEN 1 ELSE 0 END), 0) AS successful`).
		Where("processed_campaign_id = ?", pc.ID).
		Scan(&agg).Error; err != nil {
		return nil, err
	}

	// trackingResults, err := s.sentRepo.TrackingResultsFromSentRows(ctx, pc.ID)
	// if err != nil {
	// 	return nil, err
	// }

	stats := map[string]any{
		"aggregatedTotalRecords":          agg.Total,
		"aggregatedTotalSent":             agg.Successful,
		"aggregatedTotalParts":            agg.Total,
		"aggregatedTotalDeliveredParts":   agg.Successful,
		"aggregatedTotalUnDeliveredParts": agg.Total - agg.Successful,
		"aggregatedTotalUnKnownParts":     int64(0),
		// "trackingResults":                 trackingResults,
		"updatedAt": utils.UTCNow().Format(time.RFC3339),
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return nil, err
	}
	pc.Statistics = data
	pc.UpdatedAt = utils.UTCNow()
	if err := s.pcRepo.UpdateMeta(ctx, pc); err != nil {
		return nil, err
	}
	return stats, nil
}

func (s *SMSCampaignScheduler) notifyAdmin(message string) {
	if s.notifier == nil {
		return
	}
	go func(msg string) {
		for _, mobile := range s.adminCfg.ActiveMobiles() {
			_ = s.notifier.SendSMS(context.Background(), mobile, msg, nil)
		}
	}(message)
}

func buildPayamSMSSendResponse(
	processedCampaignID uint,
	items []PayamSMSItem,
	result PayamSMSSendResult,
	sendErr error,
) (*models.PayamSMSSendResponse, error) {
	trackingIDs := make(pq.StringArray, 0, len(items))
	for _, item := range items {
		if trackingID := strings.TrimSpace(item.TrackingID); trackingID != "" {
			trackingIDs = append(trackingIDs, trackingID)
		}
	}

	headers := result.ResponseHeaders
	if headers == nil {
		headers = make(map[string][]string)
	}
	responseHeaders, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("marshal PayamSMS response headers: %w", err)
	}

	var errorMessage *string
	if sendErr != nil {
		message := sendErr.Error()
		errorMessage = &message
	}

	return &models.PayamSMSSendResponse{
		ProcessedCampaignID: processedCampaignID,
		TrackingIDs:         trackingIDs,
		HTTPStatusCode:      result.HTTPStatusCode,
		ResponseHeaders:     responseHeaders,
		ResponseBody:        result.RawResponse,
		Error:               errorMessage,
		AttemptCount:        result.AttemptCount,
	}, nil
}

// persistPayamSMSSendResponse deliberately detaches the audit write from a
// canceled campaign context. A timeout/transport cancellation is itself one of
// the failures this table is intended to preserve.
func (s *SMSCampaignScheduler) persistPayamSMSSendResponse(
	ctx context.Context,
	processedCampaignID uint,
	items []PayamSMSItem,
	result PayamSMSSendResult,
	sendErr error,
) error {
	row, err := buildPayamSMSSendResponse(processedCampaignID, items, result, sendErr)
	if err != nil {
		return err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.db.WithContext(persistCtx).Create(row).Error
}

func buildSMSProviderUpdate(trackingID string, resp *PayamSMSResponseItem, sendErr error) repository.SentSMSProviderUpdate {
	update := repository.SentSMSProviderUpdate{
		TrackingID: trackingID,
	}
	if sendErr != nil {
		code := "SEND_BATCH_FAILED"
		desc := sendErr.Error()
		update.ErrorCode = &code
		update.Description = &desc
		return update
	}

	if resp == nil {
		code := "MISSING_SEND_RESPONSE"
		desc := fmt.Sprintf("missing send response for tracking_id=%s", trackingID)
		update.ErrorCode = &code
		update.Description = &desc
		return update
	}

	update.ServerID = resp.ServerID
	update.ErrorCode = resp.ErrorCode
	update.Description = resp.Desc
	return update
}

func buildGenericSMSProviderUpdate(
	provider models.SMSProvider,
	trackingID string,
	providerCustomerID *int64,
	resp *SMSProviderSendItem,
	sendErr error,
) repository.SentSMSProviderUpdate {
	update := repository.SentSMSProviderUpdate{
		TrackingID:         trackingID,
		Provider:           &provider,
		ProviderCustomerID: providerCustomerID,
	}
	if resp == nil {
		missing := genericMissingSMSOutcome(trackingID, providerCustomerID, sendErr)
		resp = &missing
	}
	update.ServerID = resp.ProviderMessageID
	update.ErrorCode = resp.ErrorCode
	update.Description = resp.Description
	if provider != models.SMSProviderPayamSMS {
		status := resp.InternalStatus
		update.Status = &status
		partsDelivered := 0
		if status == models.SMSSendStatusSuccessful {
			partsDelivered = 1
		}
		update.PartsDelivered = &partsDelivered
	}
	return update
}

func genericMissingSMSOutcome(trackingID string, providerCustomerID *int64, sendErr error) SMSProviderSendItem {
	code := "MISSING_SEND_RESPONSE"
	description := fmt.Sprintf("missing provider send response for tracking_id=%s", trackingID)
	if sendErr != nil {
		code = "SEND_BATCH_FAILED"
		description = sendErr.Error()
	}
	return SMSProviderSendItem{
		TrackingID:         trackingID,
		ProviderCustomerID: providerCustomerID,
		InternalStatus:     models.SMSSendStatusPending,
		ErrorCode:          &code,
		Description:        &description,
	}
}

func payamAuditInput(items []SMSProviderMessage, result SMSProviderSendResult) ([]PayamSMSItem, PayamSMSSendResult) {
	payamItems := make([]PayamSMSItem, 0, len(items))
	for _, item := range items {
		payamItems = append(payamItems, PayamSMSItem{
			Recipient:  item.Recipient,
			Body:       item.Body,
			TrackingID: item.TrackingID,
		})
	}
	payamResult := PayamSMSSendResult{
		RawResponse:     result.RawResponse,
		ResponseHeaders: result.ResponseHeaders,
		HTTPStatusCode:  result.HTTPStatusCode,
		AttemptCount:    result.AttemptCount,
		Items:           make([]PayamSMSResponseItem, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		payamResult.Items = append(payamResult.Items, PayamSMSResponseItem{
			TrackingID: item.TrackingID,
			ServerID:   item.ProviderMessageID,
			ErrorCode:  item.ErrorCode,
			Desc:       item.Description,
		})
	}
	return payamItems, payamResult
}

func (s *SMSCampaignScheduler) persistSMSProviderSendAttempt(
	ctx context.Context,
	processedCampaignID uint,
	provider models.SMSProvider,
	items []SMSProviderMessage,
	result SMSProviderSendResult,
	sendErr error,
) error {
	trackingIDs := make(pq.StringArray, 0, len(items))
	for _, item := range items {
		if trackingID := strings.TrimSpace(item.TrackingID); trackingID != "" {
			trackingIDs = append(trackingIDs, trackingID)
		}
	}
	headers := result.ResponseHeaders
	if headers == nil {
		headers = make(http.Header)
	}
	headerJSON, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("marshal SMS provider response headers: %w", err)
	}
	var errorMessage *string
	if sendErr != nil {
		message := sendErr.Error()
		errorMessage = &message
	}
	row := &models.SMSProviderSendAttempt{
		ProcessedCampaignID: processedCampaignID,
		Provider:            provider,
		TrackingIDs:         trackingIDs,
		HTTPStatusCode:      result.HTTPStatusCode,
		ResponseHeaders:     headerJSON,
		ResponseBody:        result.RawResponse,
		Error:               errorMessage,
		AttemptCount:        result.AttemptCount,
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.db.WithContext(persistCtx).Create(row).Error
}

func (s *SMSCampaignScheduler) recordImmediateSMSOutcomes(
	ctx context.Context,
	processedCampaignID uint,
	provider models.SMSProvider,
	outcomes []SMSProviderSendItem,
) error {
	if len(outcomes) == 0 || s.jobRepo == nil || s.resRepo == nil {
		return nil
	}
	trackingIDs := make(pq.StringArray, 0, len(outcomes))
	for _, outcome := range outcomes {
		if trackingID := strings.TrimSpace(outcome.TrackingID); trackingID != "" {
			trackingIDs = append(trackingIDs, trackingID)
		}
	}
	if len(trackingIDs) == 0 {
		return nil
	}

	return repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		now := utils.UTCNow()
		job := &models.CampaignStatusJob{
			ProcessedCampaignID: processedCampaignID,
			CorrelationID:       uuid.NewString(),
			Platform:            models.CampaignPlatformSMS,
			Provider:            &provider,
			TrackingIDs:         trackingIDs,
			ScheduledAt:         now,
			ExecutedAt:          &now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := s.jobRepo.Save(txCtx, job); err != nil {
			return err
		}
		rows := make([]*models.SMSStatusResult, 0, len(outcomes))
		for _, outcome := range outcomes {
			trackingID := strings.TrimSpace(outcome.TrackingID)
			if trackingID == "" {
				continue
			}
			total, delivered, undelivered, unknown := immediateSMSStatusParts(outcome.InternalStatus)
			providerStatus := outcome.ProviderStatusText
			if providerStatus == nil || strings.TrimSpace(*providerStatus) == "" {
				providerStatus = outcome.Description
			}
			legacyStatus := providerStatus
			rows = append(rows, &models.SMSStatusResult{
				JobID:                 job.ID,
				ProcessedCampaignID:   processedCampaignID,
				TrackingID:            trackingID,
				ServerID:              outcome.ProviderMessageID,
				Provider:              provider,
				ProviderStatusCode:    outcome.ProviderStatusCode,
				ProviderStatusText:    providerStatus,
				InternalStatus:        utils.ToPtr(outcome.InternalStatus),
				TotalParts:            &total,
				TotalDeliveredParts:   &delivered,
				TotalUndeliveredParts: &undelivered,
				TotalUnknownParts:     &unknown,
				Status:                legacyStatus,
				Metadata:              json.RawMessage(`{}`),
			})
		}
		return s.resRepo.SaveBatch(txCtx, rows)
	})
}

func immediateSMSStatusParts(status models.SMSSendStatus) (total, delivered, undelivered, unknown int64) {
	switch status {
	case models.SMSSendStatusSuccessful:
		return 1, 1, 0, 0
	case models.SMSSendStatusUnsuccessful:
		return 1, 0, 1, 0
	default:
		return 1, 0, 0, 1
	}
}

func statusJobProvider(job *models.CampaignStatusJob) (models.SMSProvider, error) {
	if job == nil {
		return "", fmt.Errorf("SMS status job is nil")
	}
	if job.Provider == nil {
		return models.SMSProviderPayamSMS, nil
	}
	return normalizeSMSProvider(*job.Provider)
}

func (s *SMSCampaignScheduler) handleExternalSMSStatusJob(ctx context.Context, job *models.CampaignStatusJob, providerName models.SMSProvider) error {
	provider, err := s.providers.Provider(providerName)
	if err != nil {
		return err
	}
	rows, err := s.sentRepo.ListByTrackingIDs(ctx, job.ProcessedCampaignID, []string(job.TrackingIDs))
	if err != nil {
		return err
	}
	byLookupID := make(map[string]*models.SentSMS, len(rows))
	lookupIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.Provider != providerName {
			continue
		}
		lookupID, ok := externalSMSStatusRequestID(providerName, row)
		if !ok {
			continue
		}
		if _, exists := byLookupID[lookupID]; exists {
			continue
		}
		byLookupID[lookupID] = row
		lookupIDs = append(lookupIDs, lookupID)
	}
	if len(lookupIDs) == 0 {
		return s.markSMSStatusJobExecuted(ctx, job, nil)
	}

	statusResult, fetchErr := provider.FetchStatus(ctx, lookupIDs)
	job.RawProviderResponse = statusResult.RawResponse
	if fetchErr != nil {
		return s.markSMSStatusJobFailure(ctx, job, fetchErr)
	}
	missingLookupIDs := missingExternalSMSStatusLookupIDs(providerName, lookupIDs, statusResult.Items)
	var partialStatusErr error
	if len(missingLookupIDs) > 0 {
		partialStatusErr = fmt.Errorf("provider %q omitted delivery status for %d of %d lookup IDs", providerName, len(missingLookupIDs), len(lookupIDs))
	}

	if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		now := utils.UTCNow()
		statusRows := make([]*models.SMSStatusResult, 0, len(statusResult.Items))
		updates := make([]repository.SentSMSProviderUpdate, 0, len(statusResult.Items))
		processedLookupIDs := make(map[string]struct{}, len(statusResult.Items))
		for _, item := range statusResult.Items {
			lookupID := externalSMSStatusResponseID(providerName, item)
			if lookupID == "" {
				continue
			}
			if _, duplicate := processedLookupIDs[lookupID]; duplicate {
				continue
			}
			row := byLookupID[lookupID]
			if row == nil {
				continue
			}
			processedLookupIDs[lookupID] = struct{}{}
			statusText := item.ProviderStatusText
			legacyStatus := statusText
			if legacyStatus == nil || strings.TrimSpace(*legacyStatus) == "" {
				legacyStatus = item.ProviderStatusCode
			}
			metadata := item.Metadata
			if len(metadata) == 0 || !json.Valid(metadata) {
				metadata = json.RawMessage(`{}`)
			}
			status := item.InternalStatus
			if status == models.SMSSendStatusPending && item.UnknownParts > 0 {
				smsProviderStatusUnknownTotal.WithLabelValues(string(providerName)).Inc()
			}
			deliveredParts := int(item.DeliveredParts)
			statusRows = append(statusRows, &models.SMSStatusResult{
				JobID:                 job.ID,
				ProcessedCampaignID:   job.ProcessedCampaignID,
				TrackingID:            row.TrackingID,
				ServerID:              row.ServerID,
				Provider:              providerName,
				ProviderStatusCode:    item.ProviderStatusCode,
				ProviderStatusText:    statusText,
				InternalStatus:        &status,
				TotalParts:            &item.TotalParts,
				TotalDeliveredParts:   &item.DeliveredParts,
				TotalUndeliveredParts: &item.UndeliveredParts,
				TotalUnknownParts:     &item.UnknownParts,
				Status:                legacyStatus,
				Metadata:              metadata,
			})
			updates = append(updates, repository.SentSMSProviderUpdate{
				ProcessedCampaignID: utils.ToPtr(job.ProcessedCampaignID),
				TrackingID:          row.TrackingID,
				Provider:            &providerName,
				ServerID:            row.ServerID,
				Status:              &status,
				PartsDelivered:      &deliveredParts,
			})
		}
		if err := s.sentRepo.UpdateProviderFieldsByTrackingIDs(txCtx, updates); err != nil {
			return err
		}
		if err := s.resRepo.SaveBatch(txCtx, statusRows); err != nil {
			return err
		}
		if partialStatusErr != nil {
			job.RetryCount++
			message := partialStatusErr.Error()
			job.Error = &message
			if job.RetryCount >= smsStatusJobMaxRetry {
				job.ExecutedAt = &now
			} else {
				job.ExecutedAt = nil
			}
		} else {
			job.ExecutedAt = &now
			job.Error = nil
		}
		job.UpdatedAt = now
		return s.jobRepo.Update(txCtx, job)
	}); err != nil {
		return err
	}
	statisticsErr := s.publishSMSStatusStatistics(ctx, job.ProcessedCampaignID)
	if partialStatusErr != nil {
		smsProviderStatusJobFailuresTotal.WithLabelValues(string(providerName)).Inc()
		if statisticsErr != nil {
			return fmt.Errorf("%v; publish known partial SMS statistics: %w", partialStatusErr, statisticsErr)
		}
		return partialStatusErr
	}
	return statisticsErr
}

func externalSMSStatusRequestID(provider models.SMSProvider, row *models.SentSMS) (string, bool) {
	if row == nil {
		return "", false
	}
	if provider == models.SMSProviderCandoo {
		if row.ProviderCustomerID == nil || *row.ProviderCustomerID <= 0 {
			return "", false
		}
		return strconv.FormatInt(*row.ProviderCustomerID, 10), true
	}
	if row.ServerID == nil {
		return "", false
	}
	messageID := strings.TrimSpace(*row.ServerID)
	return messageID, messageID != ""
}

func externalSMSStatusResponseID(provider models.SMSProvider, item SMSProviderStatusItem) string {
	if provider == models.SMSProviderCandoo {
		if item.ProviderCustomerID == nil || *item.ProviderCustomerID <= 0 {
			return ""
		}
		return strconv.FormatInt(*item.ProviderCustomerID, 10)
	}
	return strings.TrimSpace(item.ProviderMessageID)
}

func missingExternalSMSStatusLookupIDs(provider models.SMSProvider, lookupIDs []string, statusItems []SMSProviderStatusItem) []string {
	expected := make(map[string]struct{}, len(lookupIDs))
	for _, lookupID := range lookupIDs {
		if lookupID = strings.TrimSpace(lookupID); lookupID != "" {
			expected[lookupID] = struct{}{}
		}
	}
	for _, item := range statusItems {
		delete(expected, externalSMSStatusResponseID(provider, item))
	}
	missing := make([]string, 0, len(expected))
	for _, lookupID := range lookupIDs {
		lookupID = strings.TrimSpace(lookupID)
		if _, exists := expected[lookupID]; exists {
			missing = append(missing, lookupID)
			delete(expected, lookupID)
		}
	}
	return missing
}

func (s *SMSCampaignScheduler) markSMSStatusJobFailure(ctx context.Context, job *models.CampaignStatusJob, fetchErr error) error {
	if provider, err := statusJobProvider(job); err == nil {
		smsProviderStatusJobFailuresTotal.WithLabelValues(string(provider)).Inc()
	}
	now := utils.UTCNow()
	job.RetryCount++
	message := fetchErr.Error()
	job.Error = &message
	job.UpdatedAt = now
	if job.RetryCount >= smsStatusJobMaxRetry {
		job.ExecutedAt = &now
	} else {
		job.ExecutedAt = nil
	}
	if err := s.jobRepo.Update(ctx, job); err != nil {
		return err
	}
	return fetchErr
}

func (s *SMSCampaignScheduler) markSMSStatusJobExecuted(ctx context.Context, job *models.CampaignStatusJob, errText *string) error {
	now := utils.UTCNow()
	job.ExecutedAt = &now
	job.Error = errText
	job.UpdatedAt = now
	return s.jobRepo.Update(ctx, job)
}

func (s *SMSCampaignScheduler) publishSMSStatusStatistics(ctx context.Context, processedCampaignID uint) error {
	stats, err := s.updateProcessedCampaignStats(ctx, processedCampaignID)
	if err != nil || stats == nil {
		return err
	}
	pc, err := s.pcRepo.ByID(ctx, processedCampaignID)
	if err != nil {
		return err
	}
	if pc == nil {
		return fmt.Errorf("processed campaign not found for processed campaign id=%d", processedCampaignID)
	}
	if shouldPushCurrentProcessedCampaignStatistics(pc, stats) {
		return s.botClient.PushCampaignStatistics(ctx, pc.CampaignID, stats)
	}
	return nil
}

func allocateCandooCustomerIDs(ctx context.Context, db *gorm.DB, count int) ([]int64, error) {
	if count <= 0 {
		return nil, nil
	}
	type sequenceValue struct {
		Value int64 `gorm:"column:value"`
	}
	values := make([]sequenceValue, 0, count)
	if err := db.WithContext(ctx).
		Raw("SELECT nextval('candoo_customer_id_seq') AS value FROM generate_series(1, ?)", count).
		Scan(&values).Error; err != nil {
		return nil, err
	}
	if len(values) != count {
		return nil, fmt.Errorf("Candoo customer ID allocation returned %d IDs, want %d", len(values), count)
	}
	ids := make([]int64, len(values))
	for i, value := range values {
		if value.Value <= 0 {
			return nil, fmt.Errorf("Candoo customer ID allocation returned non-positive ID")
		}
		ids[i] = value.Value
	}
	return ids, nil
}
