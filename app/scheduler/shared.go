package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/lib/pq"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	trackingCounterName   = "sms_tracking_id"
	trackingCounterHexLen = 16
	trackingCounterBits   = 16 * 4

	numJobsPerTick          = 250
	statusJobWorkerInterval = 1 * time.Minute

	// statusJobMaxRetry is the maximum number of times a status-check job is
	// retried before it is permanently marked as executed. Used by all platform
	// schedulers (Bale, Splus, SMS, …).
	statusJobMaxRetry = 3

	// audienceAppendBatchSize controls how many audience IDs are flushed to the
	// database per AppendAudienceData call inside the persistence transaction.
	// Used by all platform schedulers.
	audienceAppendBatchSize = 1000

	// Smart Targeting capacity and Test-sampling calculations scan the same
	// large audience population. Keep one deadline for both durable workers so
	// their behavior cannot drift. The lease must remain longer than the worker
	// deadline or another replica could reclaim a calculation that is still
	// running.
	smartTargetingCalculationJobTimeout    = 60 * time.Minute
	smartTargetingCalculationLeaseDuration = smartTargetingCalculationJobTimeout + 10*time.Minute

	// Audience preparation can repeat the large-population scan once per Smart
	// Targeting Test tag. Keep every platform scheduler on the same deadline and
	// reclaim only after a larger hard-crash recovery window.
	campaignExecutionTimeout    = 8 * time.Hour
	campaignExecutionStaleAfter = campaignExecutionTimeout + 2*time.Hour
)

type AudiencePhonesResult struct {
	Phones                    []string
	IDs                       []int64
	UIDs                      []string
	Codes                     []string
	BundleAudienceSelectionID *uint
	MatchedUIDs               []string
	UnmatchedUIDs             []string
}

func zeroAudienceCampaignStatistics() map[string]any {
	return map[string]any{
		"aggregatedTotalRecords":          int64(0),
		"aggregatedTotalSent":             int64(0),
		"aggregatedTotalParts":            int64(0),
		"aggregatedTotalDeliveredParts":   int64(0),
		"aggregatedTotalUnDeliveredParts": int64(0),
		"aggregatedTotalUnKnownParts":     int64(0),
		"updatedAt":                       utils.UTCNow().Format(time.RFC3339),
	}
}

// preparedCampaignStatistics gives a zero-recipient best-effort Test run the
// same durable statistics shape as a delivered run. Refund reconciliation
// reads the campaign copy of these values, so an explicit zero must be pushed
// instead of being represented by a missing statistics object.
func preparedCampaignStatistics(
	ctx context.Context,
	repo repository.ProcessedCampaignRepository,
	processed *models.ProcessedCampaign,
	preparedAudienceCount int,
	update func(context.Context, uint) (map[string]any, error),
) (map[string]any, error) {
	if preparedAudienceCount > 0 {
		if update == nil || processed == nil {
			return nil, errors.New("processed campaign statistics updater is unavailable")
		}
		return update(ctx, processed.ID)
	}
	if repo == nil || processed == nil {
		return nil, errors.New("processed campaign is unavailable for zero-audience statistics")
	}
	stats := zeroAudienceCampaignStatistics()
	data, err := json.Marshal(stats)
	if err != nil {
		return nil, err
	}
	processed.Statistics = data
	processed.UpdatedAt = utils.UTCNow()
	if err := repo.UpdateMeta(ctx, processed); err != nil {
		return nil, err
	}
	return stats, nil
}

func shouldPushPreparedCampaignStatistics(stats map[string]any, preparedAudienceCount int) bool {
	if stats == nil {
		return false
	}
	if preparedAudienceCount == 0 {
		return true
	}
	sent, ok := stats["aggregatedTotalSent"].(int64)
	return ok && sent > 0
}

// shouldPushCurrentProcessedCampaignStatistics prevents a late status job for
// a retained historical attempt from overwriting the elected campaign's
// statistics. Historical jobs may still update their own processed row and
// delivery/status records; only the campaign-level publication is suppressed.
func shouldPushCurrentProcessedCampaignStatistics(pc *models.ProcessedCampaign, stats map[string]any) bool {
	if pc == nil || !pc.IsCurrent || stats == nil {
		return false
	}
	sent, ok := stats["aggregatedTotalSent"].(int64)
	return ok && sent > 0
}

// releaseUnpreparedCampaignOnFailure settles a failed claim. A checkpoint
// without send intent is retired and requeued; a persisted send intent becomes
// interrupted so an uncertain provider dispatch is never replayed.
func releaseUnpreparedCampaignOnFailure(db *gorm.DB, logger *log.Logger, schedulerName string, campaignID uint, failure *error) {
	if db == nil || failure == nil || *failure == nil {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := repository.ReleaseUnpreparedCampaign(releaseCtx, db, campaignID); err != nil {
		if logger != nil {
			logger.Printf("%s scheduler: release unprepared campaign id=%d failed: %v", schedulerName, campaignID, err)
		}
	}
}

func recoverStaleCampaignRuns(ctx context.Context, db *gorm.DB, logger *log.Logger, schedulerName string) {
	if db == nil {
		return
	}
	result, err := repository.RecoverStaleCampaignRuns(ctx, db, utils.UTCNow().Add(-campaignExecutionStaleAfter))
	if err != nil {
		if logger != nil {
			logger.Printf("%s scheduler: recover stale campaign runs failed: %v", schedulerName, err)
		}
		return
	}
	if result.Requeued > 0 {
		campaignExecutionRecoveryTotal.WithLabelValues(schedulerName, "requeued_undelivered").Add(float64(result.Requeued))
	}
	if result.Interrupted > 0 {
		campaignExecutionRecoveryTotal.WithLabelValues(schedulerName, "interrupted_delivery_recorded").Add(float64(result.Interrupted))
	}
	if (result.Requeued > 0 || result.Interrupted > 0) && logger != nil {
		logger.Printf("%s scheduler: recovered stale campaign runs: requeued_undelivered=%d interrupted_delivery_recorded=%d", schedulerName, result.Requeued, result.Interrupted)
	}
}

// bundleSelectionIDFromAudienceResult validates that a non-Excel audience
// result carries the persisted bundle selection used for the campaign.
func bundleSelectionIDFromAudienceResult(result *AudiencePhonesResult) (*uint, error) {
	if result == nil {
		return nil, errors.New("audience result is nil")
	}
	if result.BundleAudienceSelectionID == nil || *result.BundleAudienceSelectionID == 0 {
		return nil, errors.New("campaign returned no valid bundle audience selection id")
	}
	return result.BundleAudienceSelectionID, nil
}

func initSchedulerLogger(name string) (*log.Logger, *os.File, error) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		clean = "scheduler"
	}

	candidates := []string{"data", "/data"}
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		logPath := filepath.Join(dir, fmt.Sprintf("%s.log", clean))
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}

		// Keep a scheduler-specific file, but also use the application's configured
		// log writer. The latter includes Sentry when it is enabled, so scheduler
		// failures are reported consistently with the rest of the application.
		// It also preserves the configured stdout/file logging destinations.
		mw := io.MultiWriter(log.Default().Writer(), f)
		l := log.New(mw, fmt.Sprintf("%s ", clean), log.LstdFlags|log.Lmicroseconds|log.LUTC)
		return l, f, nil
	}

	return nil, nil, fmt.Errorf("could not create %s log file in any candidate directory", clean)
}

func hasCampaignAdLink(link *string) bool {
	return link != nil && strings.TrimSpace(*link) != ""
}

func hasTargetAudienceExcelFileUUID(fileUUID *string) bool {
	return fileUUID != nil && strings.TrimSpace(*fileUUID) != ""
}

// usesExcelAudienceTargeting keeps schedulers compatible with older bot
// responses that did not include audience_targeting_method. Any valid explicit
// method is authoritative; only legacy/invalid responses infer Excel targeting
// from the attached file UUID.
func usesExcelAudienceTargeting(c dto.BotGetCampaignResponse) bool {
	method := strings.ToLower(strings.TrimSpace(c.TargetingMethod))
	if models.IsValidCampaignAudienceTargetingMethod(method) {
		return method == models.CampaignAudienceTargetingExcel
	}
	return hasTargetAudienceExcelFileUUID(c.TargetAudienceExcelFileUUID)
}

func usesSmartAudienceTargeting(c dto.BotGetCampaignResponse) bool {
	return strings.EqualFold(strings.TrimSpace(c.TargetingMethod), models.CampaignAudienceTargetingSmart)
}

func campaignExecutionTags(c dto.BotGetCampaignResponse) []string {
	if usesSmartAudienceTargeting(c) && len(c.SelectedTags) > 0 {
		return c.SelectedTags
	}
	return c.Tags
}

// resolveActiveCampaignTagIDs parses the campaign's effective tags and resolves
// every distinct ID through the active tag catalog. It deliberately fails closed:
// a missing or inactive tag must never turn an intended tag filter into an
// unfiltered audience query.
func resolveActiveCampaignTagIDs(
	ctx context.Context,
	tagRepo repository.TagRepository,
	c dto.BotGetCampaignResponse,
) ([]string, pq.Int32Array, error) {
	executionTags, requestedIDs, err := parseCampaignTagIDs(c)
	if err != nil {
		return executionTags, nil, err
	}

	activeTags, err := tagRepo.ListByIDs(ctx, requestedIDs)
	if err != nil {
		return executionTags, nil, fmt.Errorf("resolve active audience tags for campaign id=%d: %w", c.ID, err)
	}

	resolved, err := requireAllTagsActive(c.ID, requestedIDs, activeTags)
	if err != nil {
		return executionTags, nil, err
	}
	return executionTags, resolved, nil
}

func parseCampaignTagIDs(c dto.BotGetCampaignResponse) ([]string, []uint, error) {
	executionTags := campaignExecutionTags(c)
	if len(executionTags) == 0 {
		return executionTags, nil, fmt.Errorf("campaign id=%d has no audience tags", c.ID)
	}

	requestedIDs := make([]uint, 0, len(executionTags))
	seen := make(map[uint]struct{}, len(executionTags))
	for _, rawTagID := range executionTags {
		tagID, err := strconv.ParseUint(strings.TrimSpace(rawTagID), 10, 31)
		if err != nil || tagID == 0 {
			if err == nil {
				err = errors.New("tag ID must be positive")
			}
			return executionTags, nil, fmt.Errorf("campaign id=%d has invalid audience tag %q: %w", c.ID, rawTagID, err)
		}
		id := uint(tagID)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		requestedIDs = append(requestedIDs, id)
	}
	return executionTags, requestedIDs, nil
}

func requireAllTagsActive(campaignID uint, requestedIDs []uint, activeTags []*models.Tag) (pq.Int32Array, error) {
	activeByID := make(map[uint]struct{}, len(activeTags))
	for _, tag := range activeTags {
		if tag != nil {
			activeByID[tag.ID] = struct{}{}
		}
	}

	resolved := make(pq.Int32Array, 0, len(requestedIDs))
	missingOrInactive := make([]uint, 0)
	for _, id := range requestedIDs {
		if _, active := activeByID[id]; !active {
			missingOrInactive = append(missingOrInactive, id)
			continue
		}
		resolved = append(resolved, int32(id))
	}
	if len(missingOrInactive) > 0 {
		return nil, fmt.Errorf(
			"campaign id=%d has missing or inactive audience tag IDs: %v",
			campaignID,
			missingOrInactive,
		)
	}

	return resolved, nil
}

// requireExactAudienceCount validates an allocation cannot exceed its approved
// audience count. Runtime eligibility can legitimately shrink after approval,
// so a smaller (including empty) allocation is valid and is reported to admins
// by the scheduler before execution continues.
func requireExactAudienceCount(campaignID uint, requested int64, selected int) error {
	if requested <= 0 {
		return fmt.Errorf("campaign id=%d has invalid requested audience count %d", campaignID, requested)
	}
	if selected < 0 || int64(selected) > requested {
		return fmt.Errorf(
			"campaign id=%d requested at most %d audiences, but %d eligible audiences were retrieved",
			campaignID,
			requested,
			selected,
		)
	}
	return nil
}

// requireAudienceMatch preserves the historical behavior for legacy campaigns
// that predate mandatory bundles: they may execute with an available subset,
// but must never create an empty processed campaign. Bundle campaigns use the
// stricter exact-count reservation path.
func requireAudienceMatch(campaignID uint, tagIDs pq.Int32Array, selected int) error {
	if selected > 0 {
		return nil
	}
	return fmt.Errorf(
		"no audience profiles matched campaign id=%d tag IDs=%v and targeting constraints",
		campaignID,
		[]int32(tagIDs),
	)
}

type standardBundleAudienceSelector func(context.Context, map[int64]struct{}) ([]string, []int64, []string, error)
type reservedBundleAudienceLoader func(context.Context, []int64) ([]string, []int64, []string, error)

func loadReservedBundleAudience(ctx context.Context, repo repository.AudienceProfileRepository, reserved []int64) ([]string, []int64, []string, error) {
	rows, err := repo.ByIDs(ctx, reserved)
	if err != nil {
		return nil, nil, nil, err
	}
	byID := make(map[int64]*models.AudienceProfile, len(rows))
	for _, row := range rows {
		if row != nil {
			byID[int64(row.ID)] = row
		}
	}
	phones := make([]string, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	uids := make([]string, 0, len(rows))
	for _, audienceID := range reserved {
		row := byID[audienceID]
		if row == nil || row.PhoneNumber == nil || strings.TrimSpace(*row.PhoneNumber) == "" {
			// A previously reserved profile can be deleted or lose its usable
			// contact data between approval and execution. Leave it out of this
			// run; the scheduler records and alerts on the resulting shortfall.
			continue
		}
		phones = append(phones, strings.TrimSpace(*row.PhoneNumber))
		ids = append(ids, int64(row.ID))
		uids = append(uids, row.UID)
	}
	return phones, ids, uids, nil
}

// notifyAudienceShortfall is deliberately best-effort: alert delivery must
// never prevent an otherwise valid partial campaign execution.
func notifyAudienceShortfall(logger *log.Logger, notifier NotificationSender, adminCfg config.AdminConfig, platform string, campaignID uint, requested int64, eligible int) {
	if requested <= 0 || int64(eligible) >= requested || notifier == nil {
		return
	}
	message := fmt.Sprintf("WARNING: %s campaign id=%d requested %d audiences, but only %d eligible audiences were found; execution will continue.", platform, campaignID, requested, eligible)
	for _, mobile := range adminCfg.ActiveMobiles() {
		mobile := mobile
		go func() {
			if err := notifier.SendSMS(context.Background(), mobile, message, nil); err != nil && logger != nil {
				logger.Printf("campaign audience-shortfall warning failed: campaign_id=%d mobile=%s err=%v", campaignID, mobile, err)
			}
		}()
	}
}

// selectAndReserveStandardBundleCandidates serializes standard targeting for a
// bundle across application replicas. The Bundle UPDATE lock covers the full
// read/select/validate/merge sequence, so two concurrent campaigns cannot read
// the same exclusion snapshot and reserve overlapping audience IDs.
func selectAndReserveStandardBundleCandidates(
	ctx context.Context,
	db *gorm.DB,
	cache *BundleAudienceCache,
	campaignID uint,
	customerID uint,
	bundleID uint,
	requested int64,
	correlationID string,
	selectCandidates standardBundleAudienceSelector,
	loadReserved reservedBundleAudienceLoader,
) ([]string, []int64, []string, uint, error) {
	if db == nil {
		return nil, nil, nil, 0, fmt.Errorf("database is unavailable for campaign %d bundle audience reservation", campaignID)
	}
	if cache == nil || selectCandidates == nil || loadReserved == nil {
		return nil, nil, nil, 0, fmt.Errorf("bundle audience selection is unavailable for campaign %d", campaignID)
	}

	var phones []string
	var ids []int64
	var uids []string
	var selectionID uint
	err := repository.WithTransaction(ctx, db, func(txCtx context.Context) error {
		if err := repository.LockBundleForUpdate(txCtx, bundleID); err != nil {
			return fmt.Errorf("lock bundle %d for campaign %d audience reservation: %w", bundleID, campaignID, err)
		}
		existing, err := cache.ByCampaignID(txCtx, campaignID)
		if err != nil {
			return fmt.Errorf("load campaign %d bundle allocation: %w", campaignID, err)
		}
		if existing != nil {
			reserved := audienceIDsFromSet(existing.IDs)
			phones, ids, uids, err = loadReserved(txCtx, reserved)
			if err != nil {
				return err
			}
			if err := requireExactAudienceCount(campaignID, requested, len(ids)); err != nil {
				return err
			}
			selectionID = existing.ID
			return nil
		}

		phones, ids, uids, err = selectCandidates(txCtx, nil)
		if err != nil {
			return err
		}
		if err := requireExactAudienceCount(campaignID, requested, len(ids)); err != nil {
			return err
		}

		saved, err := cache.SaveForCampaign(txCtx, customerID, bundleID, campaignID, correlationID, ids)
		if err != nil {
			return fmt.Errorf("save bundle %d audience selection for campaign %d: %w", bundleID, campaignID, err)
		}
		if saved == nil || saved.ID == 0 {
			return fmt.Errorf("bundle audience selection was not persisted for campaign %d", campaignID)
		}
		selectionID = saved.ID
		return nil
	})
	if err != nil {
		return nil, nil, nil, 0, err
	}
	return phones, ids, uids, selectionID, nil
}

func fetchTargetAudienceUIDsFromExcel(ctx context.Context, botClient BotClient, jazzToken string, campaignID uint) ([]string, error) {
	data, err := botClient.DownloadTargetAudienceExcelFile(ctx, jazzToken, campaignID)
	if err != nil {
		return nil, err
	}

	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    2 << 30, // 2GB
		UnzipXMLSizeLimit: 1 << 30, // 1GB
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open target audience excel file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("target audience excel file has no sheets")
	}

	rows, err := f.Rows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("cannot iterate target audience excel rows: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	uids := make([]string, 0)
	rowIndex := 0
	for rows.Next() {
		row, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("failed to read target audience excel row: %w", err)
		}
		if rowIndex == 0 {
			rowIndex++
			continue // header row
		}
		if len(row) == 0 {
			rowIndex++
			continue
		}
		uid := strings.TrimSpace(row[0])
		if uid != "" {
			uids = append(uids, uid)
		}
		rowIndex++
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("failed while reading target audience excel rows: %w", err)
	}
	return uids, nil
}

func fetchAudiencePhonesByUIDs(
	ctx context.Context,
	logger *log.Logger,
	audRepo repository.AudienceProfileRepository,
	botClient BotClient,
	c dto.BotGetCampaignResponse,
	token string,
	inputUIDs []string,
	shortLinkDomain string,
) (*AudiencePhonesResult, error) {
	if len(inputUIDs) == 0 {
		return &AudiencePhonesResult{}, nil
	}

	uniqueUIDs := make([]string, 0, len(inputUIDs))
	seen := make(map[string]struct{}, len(inputUIDs))
	for _, raw := range inputUIDs {
		uid := strings.TrimSpace(raw)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		uniqueUIDs = append(uniqueUIDs, uid)
	}
	if len(uniqueUIDs) == 0 {
		return &AudiencePhonesResult{}, nil
	}

	profiles, err := audRepo.ByUIDs(ctx, uniqueUIDs)
	if err != nil {
		return nil, err
	}

	byUID := make(map[string]*models.AudienceProfile, len(profiles))
	for _, p := range profiles {
		if p == nil {
			continue
		}
		byUID[p.UID] = p
	}

	type matchedAudience struct {
		id    int64
		phone string
		uid   string
	}

	matched := make([]matchedAudience, 0, len(uniqueUIDs))
	unmatchedUIDs := make([]string, 0)
	for _, uid := range uniqueUIDs {
		profile, ok := byUID[uid]
		if !ok {
			unmatchedUIDs = append(unmatchedUIDs, uid)
			continue
		}
		if profile.PhoneNumber == nil || strings.TrimSpace(*profile.PhoneNumber) == "" {
			unmatchedUIDs = append(unmatchedUIDs, uid)
			continue
		}
		matched = append(matched, matchedAudience{
			id:    profile.ID,
			phone: strings.TrimSpace(*profile.PhoneNumber),
			uid:   uid,
		})
	}

	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].id < matched[j].id
	})

	phones := make([]string, 0, len(matched))
	ids := make([]int64, 0, len(matched))
	uids := make([]string, 0, len(matched))
	matchedUIDs := make([]string, 0, len(matched))
	for _, item := range matched {
		phones = append(phones, item.phone)
		ids = append(ids, item.id)
		uids = append(uids, item.uid)
		matchedUIDs = append(matchedUIDs, item.uid)
	}
	if !hasCampaignAdLink(c.AdLink) {
		logger.Printf("fetchAudiencePhonesByUIDs skipped short links generation: campaign_id=%d ad_link=empty", c.ID)
		return &AudiencePhonesResult{
			Phones:        phones,
			IDs:           ids,
			UIDs:          uids,
			Codes:         make([]string, len(phones)),
			MatchedUIDs:   matchedUIDs,
			UnmatchedUIDs: unmatchedUIDs,
		}, nil
	}
	if strings.TrimSpace(shortLinkDomain) == "" {
		logger.Printf("fetchAudiencePhonesByUIDs skipped short links generation: campaign_id=%d short_link_domain=empty", c.ID)
		return &AudiencePhonesResult{
			Phones:        phones,
			IDs:           ids,
			UIDs:          uids,
			Codes:         make([]string, len(phones)),
			MatchedUIDs:   matchedUIDs,
			UnmatchedUIDs: unmatchedUIDs,
		}, nil
	}

	items := make([]dto.PhoneWithAdLink, len(phones))
	for i, p := range phones {
		adLink := c.AdLink
		if adLink != nil && strings.Contains(*adLink, "{uid}") {
			resolved := strings.ReplaceAll(*adLink, "{uid}", matchedUIDs[i])
			adLink = &resolved
		}
		items[i] = dto.PhoneWithAdLink{Phone: p, AdLink: adLink}
	}
	codes, err := botClient.AllocateShortLinks(ctx, token, &dto.BotAllocateShortLinksRequest{
		CampaignID:      c.ID,
		Items:           items,
		ShortLinkDomain: shortLinkDomain,
	})
	if err != nil {
		logger.Printf("fetchAudiencePhonesByUIDs allocate short links failed: campaign_id=%d selected=%d err=%v", c.ID, len(phones), err)
		return nil, err
	}

	return &AudiencePhonesResult{
		Phones:        phones,
		IDs:           ids,
		UIDs:          uids,
		Codes:         codes,
		MatchedUIDs:   matchedUIDs,
		UnmatchedUIDs: unmatchedUIDs,
	}, nil
}

func allocateTrackingIDs(ctx context.Context, db *gorm.DB, count int) ([]string, error) {
	if count <= 0 {
		return nil, nil
	}

	var ids []string
	err := repository.WithTransaction(ctx, db, func(txCtx context.Context) error {
		db := db.WithContext(txCtx)
		if tx, ok := txCtx.Value(repository.TxContextKey).(*gorm.DB); ok && tx != nil {
			db = tx.WithContext(txCtx)
		}

		var counter models.SequenceCounter
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&counter, "name = ?", trackingCounterName).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := utils.UTCNow()
			counter = models.SequenceCounter{
				Name:      trackingCounterName,
				LastValue: strings.Repeat("0", trackingCounterHexLen),
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := db.Create(&counter).Error; err != nil {
				return err
			}
		}

		last := strings.TrimSpace(counter.LastValue)
		if last == "" {
			last = strings.Repeat("0", trackingCounterHexLen)
		}
		if len(last) > trackingCounterHexLen {
			return fmt.Errorf("tracking counter exceeds %d hex chars", trackingCounterHexLen)
		}
		last = strings.Repeat("0", trackingCounterHexLen-len(last)) + strings.ToLower(last)
		base := new(big.Int)
		if _, ok := base.SetString(last, 16); !ok {
			return fmt.Errorf("invalid tracking counter value")
		}

		ids = make([]string, count)
		for i := 0; i < count; i++ {
			base.Add(base, big.NewInt(1))
			if base.BitLen() > trackingCounterBits {
				return fmt.Errorf("tracking counter overflow")
			}
			ids[i] = fmt.Sprintf("%0*x", trackingCounterHexLen, base)
		}

		counter.LastValue = ids[len(ids)-1]
		counter.UpdatedAt = utils.UTCNow()
		return db.Model(&models.SequenceCounter{}).
			Where("name = ?", counter.Name).
			Updates(map[string]any{
				"last_value": counter.LastValue,
				"updated_at": counter.UpdatedAt,
			}).Error
	})
	if err != nil {
		return nil, err
	}

	return ids, nil
}

// retryBackoffDelay returns an exponential back-off duration for the given
// attempt index (0-based), starting at base and capped at max.
func retryBackoffDelay(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

func checkedAudienceQueryLimit(count int64) (int, error) {
	if count <= 0 {
		return 0, nil
	}
	if count > int64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("requested audience count %d exceeds platform limit", count)
	}
	return int(count), nil
}

func audienceIDsFromSet(ids map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// gradesNeedScoreFilter reports whether a standard-targeting campaign needs
// percentile data. Empty grades and the complete A/B/C set select the whole
// scored population, so looking up percentiles cannot change their result.
func gradesNeedScoreFilter(grades []string) bool {
	if len(grades) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(grades))
	for _, grade := range grades {
		set[strings.ToUpper(strings.TrimSpace(grade))] = struct{}{}
	}
	_, hasA := set["A"]
	_, hasB := set["B"]
	_, hasC := set["C"]
	return !(hasA && hasB && hasC)
}

// gradesToScoreConstraint converts a grade set plus resolved percentiles into a
// NormalizedScoreConstraint. Grade semantics:
//
//	A         → score > p66
//	B         → p33 < score <= p66
//	C         → score <= p33
//	A+B       → score > p33
//	B+C       → score <= p66
//	A+C       → score <= p33 OR score > p66
//	A+B+C / ∅ → no constraint
func gradesToScoreConstraint(grades []string, p33, p66 float64) *models.NormalizedScoreConstraint {
	set := make(map[string]struct{}, len(grades))
	for _, g := range grades {
		set[strings.ToUpper(strings.TrimSpace(g))] = struct{}{}
	}
	_, hasA := set["A"]
	_, hasB := set["B"]
	_, hasC := set["C"]

	switch {
	case hasA && hasB && hasC:
		return nil
	case hasA && hasB:
		v := p33
		return &models.NormalizedScoreConstraint{GT: &v}
	case hasB && hasC:
		v := p66
		return &models.NormalizedScoreConstraint{LTE: &v}
	case hasA && hasC:
		lte, gt := p33, p66
		return &models.NormalizedScoreConstraint{LTE: &lte, OrGT: &gt}
	case hasA:
		v := p66
		return &models.NormalizedScoreConstraint{GT: &v}
	case hasB:
		lo, hi := p33, p66
		return &models.NormalizedScoreConstraint{GT: &lo, LTE: &hi}
	case hasC:
		v := p33
		return &models.NormalizedScoreConstraint{LTE: &v}
	default:
		return nil
	}
}
