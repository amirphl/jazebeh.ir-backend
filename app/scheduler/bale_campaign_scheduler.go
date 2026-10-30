// Package scheduler implements the campaign scheduling and execution logic for Bale campaigns, including audience fetching, message sending with retry logic, and status tracking.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	baleSendBatchSize = 200
)

type BaleCampaignScheduler struct {
	audRepo   repository.AudienceProfileRepository
	tagRepo   repository.TagRepository
	sentRepo  repository.SentBaleMessageRepository
	pcRepo    repository.ProcessedCampaignRepository
	jobRepo   repository.CampaignStatusJobRepository
	resRepo   repository.BaleStatusResultRepository
	statsRepo repository.SrcLayerAllStatsRepository
	notifier  NotificationSender
	logger    *log.Logger
	interval  time.Duration

	messageDelay time.Duration

	db       *gorm.DB
	adminCfg config.AdminConfig
	baleCfg  config.BaleConfig
	botCfg   config.BotConfig

	botClient  BotClient
	baleClient BaleClient

	logFile *os.File

	schedulerName string

	bundleAudienceCache *BundleAudienceCache
	executionLimiter    *CampaignExecutionLimiter
}

func NewBaleCampaignScheduler(
	audRepo repository.AudienceProfileRepository,
	tagRepo repository.TagRepository,
	sentRepo repository.SentBaleMessageRepository,
	pcRepo repository.ProcessedCampaignRepository,
	jobRepo repository.CampaignStatusJobRepository,
	resRepo repository.BaleStatusResultRepository,
	statsRepo repository.SrcLayerAllStatsRepository,
	notifier NotificationSender,
	db *gorm.DB,
	logger *log.Logger,
	interval time.Duration,
	messageDelay time.Duration,
	baleCfg config.BaleConfig,
	botCfg config.BotConfig,
	adminCfg config.AdminConfig,
	messageSendMockEnabled bool,
	executionLimiter *CampaignExecutionLimiter,
) *BaleCampaignScheduler {
	if interval <= 0 {
		interval = time.Minute
	}
	if botCfg.APIDomain == "" {
		botCfg.APIDomain = defaultBotAPIDomain
	}

	s := &BaleCampaignScheduler{
		audRepo:             audRepo,
		tagRepo:             tagRepo,
		sentRepo:            sentRepo,
		pcRepo:              pcRepo,
		jobRepo:             jobRepo,
		resRepo:             resRepo,
		statsRepo:           statsRepo,
		notifier:            notifier,
		logger:              logger,
		db:                  db,
		interval:            interval,
		messageDelay:        messageDelay,
		adminCfg:            adminCfg,
		baleCfg:             baleCfg,
		botCfg:              botCfg,
		botClient:           newHTTPBotClient(botCfg),
		baleClient:          maybeMockBaleClient(newHTTPBaleClient(baleCfg), messageSendMockEnabled),
		bundleAudienceCache: NewBundleAudienceCache(repository.NewBundleAudienceSelectionRepository(db)),
		executionLimiter:    executionLimiter,
		schedulerName:       "bale",
	}

	if err := s.initSchedulerLogger(); err != nil {
		s.logger = log.New(log.Default().Writer(), "bale_scheduler ", log.LstdFlags|log.Lmicroseconds|log.LUTC)
		s.logger.Printf("Bale scheduler: failed to initialize file logger: %v", err)
	}
	return s
}

func (s *BaleCampaignScheduler) initSchedulerLogger() error {
	l, f, err := initSchedulerLogger(s.schedulerName + "_scheduler")
	if err != nil {
		return err
	}
	s.logFile = f
	s.logger = l
	return nil
}

func (s *BaleCampaignScheduler) Start(parent context.Context) func() {
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

func (s *BaleCampaignScheduler) runOnce(ctx context.Context, parent context.Context) {
	recoverStaleCampaignRuns(ctx, s.db, s.logger, "Bale")
	jazzAccessToken, err := s.botClient.Login(ctx)
	if err != nil {
		s.logger.Printf("Bale scheduler: bot login failed: %v", err)
		s.notifyAdmin(fmt.Sprintf("Bale scheduler: bot login failed: %v", err))
		return
	}

	ready, err := s.botClient.ListReadyCampaigns(ctx, jazzAccessToken, models.CampaignPlatformBale)
	if err != nil {
		s.logger.Printf("Bale scheduler: list ready campaigns failed: %v", err)
		s.notifyAdmin(fmt.Sprintf("Bale scheduler: list ready campaigns failed: %v", err))
		return
	}
	if len(ready) == 0 {
		return
	}
	s.logger.Printf("Bale scheduler: listed %d ready campaigns", len(ready))

	pending := make([]dto.BotGetCampaignResponse, 0, len(ready))
	for _, c := range ready {
		if strings.ToLower(strings.TrimSpace(c.Platform)) != models.CampaignPlatformBale {
			s.logger.Printf("Bale scheduler: campaign id=%d has unsupported platform %q, skipping", c.ID, c.Platform)
			s.notifyAdmin(fmt.Sprintf("Bale scheduler: campaign id=%d has unsupported platform %q, skipping", c.ID, c.Platform))
			continue
		}
		if err := s.validateBaleCampaign(c); err != nil {
			s.logger.Printf("Bale scheduler: validate campaign failed for campaign id=%d (skipped): %v", c.ID, err)
			s.notifyAdmin(fmt.Sprintf("Bale scheduler: validate campaign failed for id=%d: %v", c.ID, err))
			continue
		}
		pc, err := s.pcRepo.ByCampaignID(ctx, c.ID)
		if err != nil {
			s.logger.Printf("Bale scheduler: check processed failed for campaign id=%d (skipped): %v", c.ID, err)
			s.notifyAdmin(fmt.Sprintf("Bale scheduler: check processed failed for id=%d: %v", c.ID, err))
			continue
		}
		if pc == nil {
			pending = append(pending, c)
		} else {
			s.logger.Printf("Bale scheduler: campaign id=%d already processed, skipping", c.ID)
		}
	}
	if len(pending) == 0 {
		return
	}
	s.logger.Printf("Bale scheduler: %d campaigns pending processing...", len(pending))

	s.dispatchPendingBaleCampaigns(parent, jazzAccessToken, pending, s.processBaleCampaign)
}

type baleCampaignDispatchGroup struct {
	campaigns []dto.BotGetCampaignResponse
}

func groupBaleCampaignsForDispatch(pending []dto.BotGetCampaignResponse) []baleCampaignDispatchGroup {
	groups := make([]baleCampaignDispatchGroup, 0, len(pending))
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
		groups = append(groups, baleCampaignDispatchGroup{
			campaigns: []dto.BotGetCampaignResponse{camp},
		})
	}

	return groups
}

func (s *BaleCampaignScheduler) dispatchPendingBaleCampaigns(
	parent context.Context,
	jazzAccessToken string,
	pending []dto.BotGetCampaignResponse,
	process func(context.Context, string, dto.BotGetCampaignResponse) error,
) {
	for _, group := range groupBaleCampaignsForDispatch(pending) {
		go func(g baleCampaignDispatchGroup) {
			for _, camp := range g.campaigns {
				if parent.Err() != nil {
					return
				}

				ctx2, cancel2 := context.WithTimeout(parent, campaignExecutionTimeout)
				if err := process(ctx2, jazzAccessToken, camp); err != nil {
					s.logger.Printf("Bale scheduler: process campaign id=%d failed: %v", camp.ID, err)
					s.notifyAdmin(fmt.Sprintf("Bale scheduler: process campaign failed for campaign id=%d: %v", camp.ID, err))
				}
				cancel2()
			}
		}(group)
	}
}

func (s *BaleCampaignScheduler) processBaleCampaign(ctx context.Context, jazzAccessToken string, c dto.BotGetCampaignResponse) (err error) {
	botID, err := extractBaleBotID(c)
	if err != nil {
		return fmt.Errorf("resolve Bale bot id for campaign id=%d: %w", c.ID, err)
	}
	requestedAudienceCount, err := schedulerConfiguredAudienceCount(c)
	if err != nil {
		return err
	}

	if err := s.botClient.MoveCampaignToRunning(ctx, jazzAccessToken, c.ID); err != nil {
		return fmt.Errorf("move campaign id=%d to running: %w", c.ID, err)
	}
	defer releaseUnpreparedCampaignOnFailure(s.db, s.logger, "Bale", c.ID, &err)
	s.logger.Printf("Bale scheduler: campaign id=%d moved to running", c.ID)
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
		s.logger.Printf("Bale scheduler: campaign id=%d fetching audience UIDs from excel", c.ID)
		fileUIDs, err := fetchTargetAudienceUIDsFromExcel(ctx, s.botClient, jazzAccessToken, c.ID)
		if err != nil {
			return fmt.Errorf("fetch excel UIDs for campaign id=%d: %w", c.ID, err)
		}
		s.logger.Printf("Bale scheduler: campaign id=%d resolving %d UIDs to phones", c.ID, len(fileUIDs))
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
		s.logger.Printf("Bale scheduler: campaign id=%d fetched %d phones via excel (unmatched=%d)", c.ID, len(phones), len(unmatchedUID))
	} else {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired before fetching audiences for campaign id=%d: %w", c.ID, err)
		}
		correlationID := uuid.NewString()
		s.logger.Printf("Bale scheduler: campaign id=%d fetching audience phones (correlation_id=%s)", c.ID, correlationID)
		var (
			audienceResult *AudiencePhonesResult
			err            error
		)
		if c.BundleID == nil || *c.BundleID == 0 {
			return fmt.Errorf("campaign id=%d has no bundle", c.ID)
		}
		if !s.executionLimiter.Acquire(ctx) {
			return fmt.Errorf("campaign execution slot unavailable before fetching audience phones for campaign id=%d: %w", c.ID, ctx.Err())
		}
		audienceResult, err = s.fetchBaleAudiencePhonesByBundle(ctx, c, jazzAccessToken, correlationID)
		s.executionLimiter.Release()
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
		s.logger.Printf("Bale scheduler: campaign id=%d fetched %d phones (bundle_audience_selection_id=%d)", c.ID, len(phones), *bundleAudienceSelectionID)
	}

	if len(ids) != len(phones) {
		return fmt.Errorf("audience ids mismatch for campaign id=%d: phones=%d ids=%d", c.ID, len(phones), len(ids))
	}
	if len(codes) != len(phones) {
		return fmt.Errorf("audience codes mismatch for campaign id=%d: phones=%d codes=%d", c.ID, len(phones), len(codes))
	}
	notifyAudienceShortfall(s.logger, s.notifier, s.adminCfg, "Bale", c.ID, requestedAudienceCount, len(ids))
	s.logger.Printf("Bale scheduler: campaign id=%d audience ready: phones=%d unmatched=%d", c.ID, len(phones), len(unmatchedUID))

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
		s.logger.Printf("Bale scheduler: persisted processed campaign id=%d for campaign id=%d", pc.ID, c.ID)

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
		s.logger.Printf("Bale scheduler: updated processed campaign id=%d with %d audience ids", pc.ID, len(ids))
		return nil
	}); err != nil {
		return fmt.Errorf("persist campaign data for campaign id=%d: %w", c.ID, err)
	}
	s.logger.Printf("Bale scheduler: persisted processed campaign id=%d num_phones=%d, num_ids=%d, num_codes=%d, num_unmatched=%d", pc.ID, len(phones), len(ids), len(codes), len(unmatchedUID))

	if len(unmatchedUID) > 0 {
		s.logger.Printf("Bale scheduler: campaign id=%d creating %d unmatched sent rows for processed_campaign_id=%d", c.ID, len(unmatchedUID), pc.ID)
		if err := s.createUnmatchedSentBaleRows(ctx, pc.ID, unmatchedUID); err != nil {
			return fmt.Errorf("create unmatched sent rows for campaign id=%d: %w", c.ID, err)
		}
	}

	var fileID *string
	if len(phones) > 0 && c.MediaUUID != nil {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired before uploading media for campaign id=%d: %w", c.ID, err)
		}
		s.logger.Printf("Bale scheduler: campaign id=%d uploading media uuid=%s", c.ID, c.MediaUUID)
		id, err := s.uploadCampaignMedia(ctx, jazzAccessToken, c)
		if err != nil {
			return fmt.Errorf("upload media for campaign id=%d: %w", c.ID, err)
		}
		fileID = id
		s.logger.Printf("Bale scheduler: campaign id=%d media uploaded file_id=%v", c.ID, fileID)
	}

	for start := 0; start < len(phones); start += baleSendBatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context expired at batch start=%d for campaign id=%d: %w", start, c.ID, err)
		}

		end := min(start+baleSendBatchSize, len(phones))
		batchPhones := phones[start:end]
		batchIDs := ids[start:end]
		batchUIDs := uids[start:end]
		batchCodes := codes[start:end]

		items := make([]BaleSendMessageRequest, 0, len(batchPhones))
		rows := make([]*models.SentBaleMessage, 0, len(batchPhones))

		s.logger.Printf("Bale scheduler: campaign id=%d allocating tracking ids for batch [%d,%d)", c.ID, start, end)
		trackingIDs, err := allocateTrackingIDs(ctx, s.db, len(batchPhones))
		if err != nil {
			return fmt.Errorf("allocate tracking ids for batch [%d,%d) campaign id=%d: %w", start, end, c.ID, err)
		}

		for i, p := range batchPhones {
			body := s.buildBaleMessageBody(c, batchCodes[i], batchUIDs[i])
			trackingID := trackingIDs[i]
			items = append(items, BaleSendMessageRequest{
				RequestID:   trackingID,
				BotID:       botID,
				PhoneNumber: p,
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{
						Text:   body,
						FileID: fileID,
					},
				},
			})
			rows = append(rows, &models.SentBaleMessage{
				ProcessedCampaignID: pc.ID,
				PhoneNumber:         p,
				PartsDelivered:      0,
				Status:              models.BaleSendStatusPending,
				TrackingID:          trackingID,
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
		s.logger.Printf("Bale scheduler: campaign id=%d batch [%d,%d) saved, sending to Bale", c.ID, start, end)
		if err := repository.TouchRunningCampaign(ctx, s.db, c.ID); err != nil {
			return fmt.Errorf("heartbeat before provider batch [%d,%d) campaign id=%d: %w", start, end, c.ID, err)
		}

		batchResponses, batchErr := s.baleClient.SendBatch(ctx, items)
		if batchErr != nil {
			s.logger.Printf("Bale scheduler: send batch [%d,%d) failed for campaign id=%d: %v", start, end, c.ID, batchErr)
		}
		responseByRequestID := make(map[string]*BaleSendMessageResponse, len(batchResponses))
		for i := range batchResponses {
			resp := batchResponses[i]
			reqID := strings.TrimSpace(resp.RequestID)
			if reqID == "" && i < len(items) {
				// Fallback: some Bale API responses omit request_id; match by position.
				reqID = strings.TrimSpace(items[i].RequestID)
			}
			if reqID == "" {
				continue
			}
			respCopy := resp
			responseByRequestID[reqID] = &respCopy
		}
		s.logger.Printf("Bale scheduler: campaign id=%d batch [%d,%d) Bale responded: sent=%d responses=%d", c.ID, start, end, len(items), len(batchResponses))

		sendUpdates := make([]repository.SentBaleSendResultUpdate, 0, len(items))
		for _, item := range items {
			reqID := strings.TrimSpace(item.RequestID)
			resp := responseByRequestID[reqID]
			sendErr := error(nil)
			if resp == nil {
				if batchErr != nil {
					sendErr = batchErr
				} else {
					sendErr = fmt.Errorf("missing send response for tracking_id=%s", reqID)
				}
			}
			sendUpdates = append(sendUpdates, buildBaleSendResultUpdate(reqID, resp, sendErr))
		}

		if len(sendUpdates) > 0 {
			if updateErr := s.sentRepo.UpdateSendResultByTrackingIDs(ctx, pc.ID, sendUpdates); updateErr != nil {
				s.logger.Printf("Bale scheduler: failed to batch update sent_bale provider fields for campaign id=%d: %v", c.ID, updateErr)
				// NOTE: Error silent here; not returning to avoid blocking further processing
			}
		}

		if err := s.scheduleStatusCheckJobs(ctx, pc.ID, trackingIDs); err != nil {
			s.logger.Printf("Bale scheduler: failed to schedule status jobs for campaign id=%d: %v", c.ID, err)
			// NOTE: Error silent here; not returning to avoid blocking further processing
		}
		s.logger.Printf("Bale scheduler: campaign id=%d batch [%d,%d) done, sleeping message_delay", c.ID, start, end)
		if err := sleepWithContext(ctx, s.messageDelay); err != nil {
			return fmt.Errorf("interrupted during batch delay at [%d,%d) for campaign id=%d: %w", start, end, c.ID, err)
		}
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

	s.logger.Printf("Bale scheduler: campaign id=%d all batches sent", c.ID)

	if err := s.botClient.MoveCampaignToExecuted(ctx, jazzAccessToken, c.ID); err != nil {
		return fmt.Errorf("move campaign id=%d to executed: %w", c.ID, err)
	}
	s.logger.Printf("Bale scheduler: campaign id=%d moved to executed", c.ID)

	if len(uids) > 0 {
		go func(campaignID uint, uids, codes []string) {
			pushCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := s.botClient.PushCampaignAudienceUIDs(pushCtx, campaignID, uids, codes); err != nil {
				s.logger.Printf("Bale scheduler: push audience UIDs failed for campaign id=%d: %v", campaignID, err)
				s.notifyAdmin(fmt.Sprintf("Bale Scheduler: push audience UIDs failed for campaign id=%d: %v", campaignID, err))
			}
		}(c.ID, uids, codes)
	}

	return nil
}

func (s *BaleCampaignScheduler) validateBaleCampaign(c dto.BotGetCampaignResponse) error {
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
	if strings.TrimSpace(s.baleCfg.APIAccessKey) == "" {
		return fmt.Errorf("Bale api-access-key is not configured")
	}
	if _, err := extractBaleBotID(c); err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(c.Platform)) != models.CampaignPlatformBale {
		return fmt.Errorf("campaign platform is not bale")
	}
	return nil
}

func (s *BaleCampaignScheduler) resolveScoreConstraint(ctx context.Context, c dto.BotGetCampaignResponse) (*models.NormalizedScoreConstraint, error) {
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

// selectBaleTagAudiences fetches audience profiles matching tagIDs, skipping any IDs in exclude,
// up to numAudiences. Bale does not segment by color, so all matching profiles are queried.
func (s *BaleCampaignScheduler) selectBaleTagAudiences(
	ctx context.Context,
	campaignID uint,
	tagIDs pq.Int32Array,
	numAudiences int64,
	exclude map[int64]struct{},
	excludeBundleID *uint,
	scoreConstraint *models.NormalizedScoreConstraint,
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

	// Bale intentionally does not segment audiences by color.
	candidates, err := s.audRepo.SelectCampaignCandidates(
		ctx,
		models.AudienceProfileFilter{Tags: &tagIDs, NormalizedScore: scoreConstraint, ExcludeBundleID: excludeBundleID},
		excludeIDs,
		limit,
	)
	if err != nil {
		s.logger.Printf("selectBaleTagAudiences fetch candidates failed: campaign_id=%d err=%v", campaignID, err)
		return nil, nil, nil, err
	}
	s.logger.Printf("selectBaleTagAudiences candidates: campaign_id=%d count=%d limit=%d excluded=%d", campaignID, len(candidates), limit, len(excludeIDs))

	for _, ap := range candidates {
		if ap == nil || ap.PhoneNumber == nil || strings.TrimSpace(*ap.PhoneNumber) == "" {
			continue
		}
		phones = append(phones, strings.TrimSpace(*ap.PhoneNumber))
		ids = append(ids, int64(ap.ID))
		uids = append(uids, ap.UID)
	}

	return phones, ids, uids, nil
}

// fetchBaleAudiencePhonesByBundle selects audiences for a Bale campaign that belongs to a bundle.
// Uniqueness is enforced across all campaigns in the bundle:
// audiences already selected by earlier campaigns in the same bundle are excluded.
// There is no rolling-window reset. Selection and persistence run under the
// Bundle lock and require the exact requested count.
func (s *BaleCampaignScheduler) fetchBaleAudiencePhonesByBundle(
	ctx context.Context,
	c dto.BotGetCampaignResponse,
	jazzAccessToken string,
	correlationID string,
) (*AudiencePhonesResult, error) {
	bundleID := *c.BundleID
	numAudiences, err := schedulerConfiguredAudienceCount(c)
	if err != nil {
		return nil, err
	}
	s.logger.Printf("fetchBaleAudiencePhonesByBundle start: campaign_id=%d customer_id=%d bundle_id=%d num_audiences=%d correlation_id=%s",
		c.ID, c.CustomerID, bundleID, numAudiences, correlationID)

	executionTags, tagIDs, err := resolveActiveCampaignTagIDs(ctx, s.tagRepo, c)
	if err != nil {
		s.logger.Printf("fetchBaleAudiencePhonesByBundle tags resolution failed: campaign_id=%d err=%v", c.ID, err)
		return nil, err
	}
	s.logger.Printf("fetchBaleAudiencePhonesByBundle tags resolved: campaign_id=%d requested=%d resolved=%d", c.ID, len(executionTags), len(tagIDs))

	scoreConstraint, err := s.resolveScoreConstraint(ctx, c)
	if err != nil {
		s.logger.Printf("fetchBaleAudiencePhonesByBundle resolve score constraint failed: campaign_id=%d err=%v", c.ID, err)
		return nil, err
	}

	var phones []string
	var ids []int64
	var uids []string
	var selectionID uint
	if usesSmartAudienceTargeting(c) {
		phones, ids, uids, selectionID, err = selectAndReserveExactSmartTargetingCandidates(ctx, s.db, c, numAudiences, correlationID, nil)
	} else {
		phones, ids, uids, selectionID, err = selectAndReserveStandardBundleCandidates(
			ctx, s.db, s.bundleAudienceCache, c.ID, c.CustomerID, bundleID, numAudiences, correlationID,
			func(selectionCtx context.Context, exclude map[int64]struct{}) ([]string, []int64, []string, error) {
				return s.selectBaleTagAudiences(selectionCtx, c.ID, tagIDs, numAudiences, exclude, &bundleID, scoreConstraint)
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
	s.logger.Printf("fetchBaleAudiencePhonesByBundle selected: campaign_id=%d bundle_id=%d selected=%d requested=%d",
		c.ID, bundleID, len(phones), numAudiences)
	if err := validateSchedulerSelectedAudienceCount(c, numAudiences, len(ids)); err != nil {
		return nil, err
	}

	s.logger.Printf("fetchBaleAudiencePhonesByBundle selection saved: campaign_id=%d bundle_id=%d selection_id=%d selected=%d",
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
		s.logger.Printf("fetchBaleAudiencePhonesByBundle skipped short links: campaign_id=%d ad_link=empty", c.ID)
		return &AudiencePhonesResult{
			Phones:                    phones,
			IDs:                       ids,
			UIDs:                      uids,
			Codes:                     make([]string, len(phones)),
			BundleAudienceSelectionID: utils.ToPtr(selectionID),
		}, nil
	}

	if c.ShortLinkDomain == nil || strings.TrimSpace(*c.ShortLinkDomain) == "" {
		s.logger.Printf("fetchBaleAudiencePhonesByBundle skipped short links: campaign_id=%d short_link_domain=empty", c.ID)
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
		s.logger.Printf("fetchBaleAudiencePhonesByBundle allocate short links failed: campaign_id=%d bundle_id=%d err=%v", c.ID, bundleID, err)
		return nil, err
	}
	if len(codes) != len(phones) {
		return nil, fmt.Errorf("allocate short links length mismatch for campaign id=%d bundle_id=%d: phones=%d codes=%d", c.ID, bundleID, len(phones), len(codes))
	}
	s.logger.Printf("fetchBaleAudiencePhonesByBundle success: campaign_id=%d bundle_id=%d selected=%d codes=%d selection_id=%d",
		c.ID, bundleID, len(phones), len(codes), selectionID)
	return &AudiencePhonesResult{
		Phones:                    phones,
		IDs:                       ids,
		UIDs:                      uids,
		Codes:                     codes,
		BundleAudienceSelectionID: utils.ToPtr(selectionID),
	}, nil
}

func (s *BaleCampaignScheduler) buildBaleMessageBody(c dto.BotGetCampaignResponse, code string, uid string) string {
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
			return strings.ReplaceAll(content, "{YOUR_LINK}", shortened)
		} else {
			injected := strings.ReplaceAll(*c.AdLink, "{uid}", uid)
			return strings.ReplaceAll(content, "{YOUR_LINK}", injected)
		}
	}
	return strings.ReplaceAll(content, "{YOUR_LINK}", "")
}

func (s *BaleCampaignScheduler) createUnmatchedSentBaleRows(ctx context.Context, processedCampaignID uint, unmatchedUIDs []string) error {
	if len(unmatchedUIDs) == 0 {
		return nil
	}

	const errCode = "AUDIENCE_UID_NOT_FOUND"
	rows := make([]*models.SentBaleMessage, 0, len(unmatchedUIDs))
	for _, uid := range unmatchedUIDs {
		desc := fmt.Sprintf("Audience uid not found or has no phone number: %s", uid)
		code := errCode
		rows = append(rows, &models.SentBaleMessage{
			ProcessedCampaignID: processedCampaignID,
			PhoneNumber:         "",
			PartsDelivered:      0,
			Status:              models.BaleSendStatusUnsuccessful,
			TrackingID:          uuid.NewString(),
			ServerID:            nil,
			ErrorCode:           &code,
			Description:         &desc,
		})
	}
	return s.sentRepo.SaveBatch(ctx, rows)
}

func (s *BaleCampaignScheduler) scheduleStatusCheckJobs(ctx context.Context, processedCampaignID uint, trackingIDs []string) error {
	if len(trackingIDs) == 0 || !s.baleClient.SupportsStatusTracking() || s.jobRepo == nil {
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
			Platform:            models.CampaignPlatformBale,
			TrackingIDs:         pq.StringArray(filteredTrackingIDs),
			RetryCount:          0,
			ScheduledAt:         now.Add(off),
			CreatedAt:           now,
			UpdatedAt:           now,
		})
	}
	return s.jobRepo.SaveBatch(ctx, jobs)
}

func (s *BaleCampaignScheduler) startStatusJobWorker(parent context.Context) {
	ticker := time.NewTicker(statusJobWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			if !s.baleClient.SupportsStatusTracking() || s.jobRepo == nil || s.resRepo == nil {
				continue
			}

			listCtx, listCancel := context.WithTimeout(parent, 30*time.Second)
			jobs, err := s.jobRepo.ListDue(listCtx, models.CampaignPlatformBale, utils.UTCNow(), numJobsPerTick)
			listCancel()
			if err != nil {
				s.logger.Printf("Bale scheduler: list status jobs failed: %v", err)
				continue
			}
			if len(jobs) == 0 {
				continue
			}

			for i, job := range jobs {
				if parent.Err() != nil {
					return
				}

				jobCtx, jobCancel := context.WithTimeout(parent, 2*time.Minute)
				err := s.handleStatusJob(jobCtx, job)
				jobCancel()

				if err != nil {
					s.logger.Printf("Bale scheduler: handle status job id=%d failed: %v", job.ID, err)
					if job.RetryCount >= statusJobMaxRetry {
						s.notifyAdmin(fmt.Sprintf("Bale scheduler: status job id=%d has failed %d times with error: %v", job.ID, job.RetryCount, err))
					}
				} else {
					s.logger.Printf("Bale scheduler: handle status job id=%d succeeded", job.ID)
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

func (s *BaleCampaignScheduler) handleStatusJob(ctx context.Context, job *models.CampaignStatusJob) error {
	rows, err := s.sentRepo.ListByTrackingIDs(ctx, job.ProcessedCampaignID, []string(job.TrackingIDs))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return s.markStatusJobExecuted(ctx, job, nil)
	}

	rowByServerID := make(map[string]*models.SentBaleMessage, len(rows))
	serverIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.ServerID == nil {
			continue
		}
		serverID := strings.TrimSpace(*row.ServerID)
		if serverID == "" {
			continue
		}
		if _, seen := rowByServerID[serverID]; seen {
			continue
		}
		rowByServerID[serverID] = row
		serverIDs = append(serverIDs, serverID)
	}
	if len(serverIDs) == 0 {
		return s.markStatusJobExecuted(ctx, job, nil)
	}

	statusResult, fetchErr := s.baleClient.FetchStatus(ctx, serverIDs)
	job.RawProviderResponse = statusResult.RawResponse
	if fetchErr != nil {
		now := utils.UTCNow()
		job.RetryCount++
		msg := fetchErr.Error()
		job.Error = &msg
		job.UpdatedAt = now
		// Keep job open for retries until max retry threshold is reached.
		if job.RetryCount >= statusJobMaxRetry {
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

		statusRows := make([]*models.BaleStatusResult, 0, len(statusItems))
		sendUpdates := make([]repository.SentBaleSendResultUpdate, 0, len(statusItems))
		for _, item := range statusItems {
			serverID := strings.TrimSpace(item.MessageID)
			if serverID == "" {
				s.logger.Printf("handleStatusJob: skipping status item with empty server_id for job_id=%d processed_campaign_id=%d", job.ID, job.ProcessedCampaignID)
				continue
			}
			row := rowByServerID[serverID]
			if row == nil {
				s.logger.Printf("handleStatusJob: no sent row found for server_id=%s job_id=%d processed_campaign_id=%d", serverID, job.ID, job.ProcessedCampaignID)
				continue
			}

			totalParts, deliveredParts, undeliveredParts, unknownParts, status := mapBaleProviderStatus(item.Status)
			statusText := strings.TrimSpace(item.StatusText)

			var errorCode *string
			var description *string
			if status == models.BaleSendStatusUnsuccessful {
				code := strconv.Itoa(item.Status)
				errorCode = &code
			}
			if metadataDesc := buildBaleStatusMetadataDescription(row.Description, item.Status, statusText); metadataDesc != "" {
				description = &metadataDesc
			}

			sendUpdates = append(sendUpdates, repository.SentBaleSendResultUpdate{
				TrackingID:     row.TrackingID,
				Status:         status,
				PartsDelivered: int(deliveredParts),
				ServerID:       row.ServerID,
				ErrorCode:      errorCode,
				Description:    description,
			})

			statusValue := strconv.Itoa(item.Status)
			providerStatusCode := int64(item.Status)
			provider := strings.TrimSpace(item.Provider)
			var statusTextPtr *string
			if statusText != "" {
				statusTextPtr = &statusText
			}
			var providerPtr *string
			if provider != "" {
				providerPtr = &provider
			}
			metadata := buildBaleStatusResultMetadata(item, status)

			statusRows = append(statusRows, &models.BaleStatusResult{
				JobID:                 job.ID,
				ProcessedCampaignID:   job.ProcessedCampaignID,
				TrackingID:            row.TrackingID,
				ServerID:              row.ServerID,
				Provider:              providerPtr,
				ProviderStatusCode:    &providerStatusCode,
				ProviderStatusText:    statusTextPtr,
				TotalParts:            &totalParts,
				TotalDeliveredParts:   &deliveredParts,
				TotalUndeliveredParts: &undeliveredParts,
				TotalUnknownParts:     &unknownParts,
				Status:                &statusValue,
				Metadata:              metadata,
			})
		}
		if len(sendUpdates) == 0 {
			s.logger.Printf("handleStatusJob: all status items filtered out (no valid server_id or matched row) for job_id=%d processed_campaign_id=%d", job.ID, job.ProcessedCampaignID)
		}
		if err := s.sentRepo.UpdateSendResultByTrackingIDs(txCtx, job.ProcessedCampaignID, sendUpdates); err != nil {
			return err
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

func (s *BaleCampaignScheduler) markStatusJobExecuted(ctx context.Context, job *models.CampaignStatusJob, errText *string) error {
	now := utils.UTCNow()
	job.ExecutedAt = &now
	job.UpdatedAt = now
	job.Error = errText
	return s.jobRepo.Update(ctx, job)
}

func mapBaleProviderStatus(statusCode int) (totalParts int64, deliveredParts int64, undeliveredParts int64, unknownParts int64, status models.BaleSendStatus) {
	totalParts = 1
	switch statusCode {
	case 10:
		return 1, 1, 0, 0, models.BaleSendStatusSuccessful
	case 6, 11, 13, 14, 100:
		return 1, 0, 1, 0, models.BaleSendStatusUnsuccessful
	case 1, 2, 4:
		return 1, 0, 0, 1, models.BaleSendStatusPending
	default:
		return 1, 0, 0, 1, models.BaleSendStatusPending
	}
}

func (s *BaleCampaignScheduler) uploadCampaignMedia(ctx context.Context, jazzAccessToken string, c dto.BotGetCampaignResponse) (*string, error) {
	if c.MediaUUID == nil || *c.MediaUUID == uuid.Nil {
		return nil, nil
	}

	path, err := s.botClient.DownloadCampaignMedia(ctx, jazzAccessToken, c.MediaUUID.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(path) }()

	resp, err := s.baleClient.UploadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	return &resp.FileID, nil
}

func buildBaleSendResultUpdate(trackingID string, resp *BaleSendMessageResponse, sendErr error) repository.SentBaleSendResultUpdate {
	update := repository.SentBaleSendResultUpdate{
		TrackingID:     trackingID,
		PartsDelivered: 0,
		Status:         models.BaleSendStatusUnsuccessful,
	}

	if sendErr != nil {
		code := "SEND_FAILED"
		desc := sendErr.Error()
		update.ErrorCode = &code
		update.Description = &desc
		return update
	}

	if resp != nil && len(resp.ErrorData) > 0 {
		first := resp.ErrorData[0]
		code := first.CodeString()
		desc := buildBaleSendMetadataDescription(resp, marshalBaleErrorSpec(resp.ErrorData))
		update.ErrorCode = &code
		update.Description = &desc
		return update
	}

	update.Status = models.BaleSendStatusSuccessful
	update.PartsDelivered = 1
	if resp != nil && strings.TrimSpace(resp.MessageID) != "" {
		id := strings.TrimSpace(resp.MessageID)
		update.ServerID = &id
	}
	desc := buildBaleSendMetadataDescription(resp, "")
	if strings.TrimSpace(desc) != "" {
		update.Description = &desc
	}
	return update
}

func buildBaleSendMetadataDescription(resp *BaleSendMessageResponse, fallback string) string {
	if resp == nil {
		return strings.TrimSpace(fallback)
	}

	metadata := map[string]any{
		"provider": resp.Provider,
	}
	if strings.TrimSpace(resp.MessageID) != "" {
		metadata["messageID"] = strings.TrimSpace(resp.MessageID)
	}
	if len(resp.ErrorData) > 0 {
		metadata["errorData"] = resp.ErrorData
	}
	if trimmedFallback := strings.TrimSpace(fallback); trimmedFallback != "" {
		metadata["error"] = trimmedFallback
	}
	if len(resp.RawBody) > 0 {
		metadata["raw"] = json.RawMessage(resp.RawBody)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		if strings.TrimSpace(fallback) != "" {
			return strings.TrimSpace(fallback)
		}
		if len(resp.RawBody) > 0 {
			return strings.TrimSpace(string(resp.RawBody))
		}
		return ""
	}
	return string(data)
}

func buildBaleStatusMetadataDescription(existing *string, statusCode int, statusText string) string {
	metadata := map[string]any{}

	if existing != nil && strings.TrimSpace(*existing) != "" {
		var existingJSON map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(*existing)), &existingJSON); err == nil {
			for k, v := range existingJSON {
				metadata[k] = v
			}
		} else {
			metadata["previousDescription"] = strings.TrimSpace(*existing)
		}
	}

	metadata["lastStatus"] = map[string]any{
		"code": statusCode,
		"text": strings.TrimSpace(statusText),
		"at":   utils.UTCNow().Format(time.RFC3339),
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		return strings.TrimSpace(statusText)
	}
	return string(data)
}

func buildBaleStatusResultMetadata(item BaleStatusResponse, normalizedStatus models.BaleSendStatus) json.RawMessage {
	metadata := map[string]any{
		"provider":         strings.TrimSpace(item.Provider),
		"messageID":        strings.TrimSpace(item.MessageID),
		"statusCode":       item.Status,
		"statusText":       strings.TrimSpace(item.StatusText),
		"normalizedStatus": string(normalizedStatus),
	}
	if len(item.RawBody) > 0 {
		metadata["raw"] = json.RawMessage(item.RawBody)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func marshalBaleErrorSpec(spec []BaleErrorData) string {
	if len(spec) == 0 {
		return ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return fmt.Sprintf("%v", spec)
	}
	return string(b)
}

func (s *BaleCampaignScheduler) updateProcessedCampaignStats(ctx context.Context, processedCampaignID uint) (map[string]any, error) {
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

func (s *BaleCampaignScheduler) updateProcessedCampaignStatsFromSentRows(ctx context.Context, pc *models.ProcessedCampaign) (map[string]any, error) {
	s.logger.Printf("updateProcessedCampaignStatsFromSentRows: computing stats from sent rows for processed_campaign_id=%d", pc.ID)
	type row struct {
		Total      int64
		Successful int64
	}
	var agg row
	if err := s.db.WithContext(ctx).Table("sent_bale_messages").
		Select(`
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN LOWER(BTRIM(status::text)) = 'successful' THEN 1 ELSE 0 END), 0) AS successful`).
		Where("processed_campaign_id = ? AND is_current", pc.ID).
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

func extractBaleBotID(c dto.BotGetCampaignResponse) (int64, error) {
	if c.PlatformSettings == nil {
		return 0, fmt.Errorf("campaign platform_settings is missing")
	}
	if c.PlatformSettings.Metadata == nil {
		return 0, fmt.Errorf("campaign platform_settings.metadata is missing")
	}
	raw, ok := c.PlatformSettings.Metadata["bale_bot_id"]
	if !ok {
		return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id is missing")
	}

	switch v := raw.(type) {
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must be positive")
		}
		return int64(v), nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must be positive")
		}
		return v, nil
	case float64:
		if v <= 0 || v != float64(int64(v)) {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must be a positive integer")
		}
		return int64(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must not be empty")
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must be a positive integer")
		}
		return id, nil
	case json.Number:
		id, err := v.Int64()
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id must be a positive integer")
		}
		return id, nil
	default:
		return 0, fmt.Errorf("campaign platform_settings.metadata.bale_bot_id has unsupported type %T", raw)
	}
}

func (s *BaleCampaignScheduler) notifyAdmin(message string) {
	if s.notifier == nil {
		return
	}
	go func(msg string) {
		for _, mobile := range s.adminCfg.ActiveMobiles() {
			_ = s.notifier.SendSMS(context.Background(), mobile, msg, nil)
		}
	}(message)
}
