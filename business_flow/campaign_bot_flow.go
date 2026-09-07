package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// BotCampaignFlow handles campaign listing logic accessible to bots
type BotCampaignFlow interface {
	ListReadyCampaigns(ctx context.Context, platform *string) (*dto.BotListCampaignsResponse, error)
	MoveCampaignToRunning(ctx context.Context, campaignID uint) error
	MoveCampaignToExecuted(ctx context.Context, campaignID uint) error
	DownloadTargetAudienceExcelFile(ctx context.Context, campaignID uint) (string, string, []byte, error)
	UpdateCampaignStatistics(ctx context.Context, campaignID uint, statistics map[string]any) (*dto.BotUpdateCampaignStatisticsResponse, error)
	PushCampaignAudienceUIDs(ctx context.Context, campaignID uint, items []dto.BotAudienceUIDItem) error
}

type BotCampaignFlowImpl struct {
	campaignRepo         repository.CampaignRepository
	multimediaRepo       repository.MultimediaAssetRepository
	platformSettingsRepo repository.PlatformSettingsRepository
	transactionRepo      repository.TransactionRepository
	platformBaseRepo     repository.PlatformBasePriceRepository
	selectedTagRepo      repository.CampaignSelectedTagRepository
	lineNumberRepo       repository.LineNumberRepository
	cacheConfig          config.CacheConfig
	db                   *gorm.DB
	rc                   *redis.Client
}

func NewBotCampaignFlow(
	campaignRepo repository.CampaignRepository,
	multimediaRepo repository.MultimediaAssetRepository,
	platformSettingsRepo repository.PlatformSettingsRepository,
	transactionRepo repository.TransactionRepository,
	platformBaseRepo repository.PlatformBasePriceRepository,
	selectedTagRepo repository.CampaignSelectedTagRepository,
	lineNumberRepo repository.LineNumberRepository,
	cacheConfig config.CacheConfig,
	db *gorm.DB,
	rc *redis.Client,
) BotCampaignFlow {
	return &BotCampaignFlowImpl{
		campaignRepo:         campaignRepo,
		multimediaRepo:       multimediaRepo,
		platformSettingsRepo: platformSettingsRepo,
		transactionRepo:      transactionRepo,
		platformBaseRepo:     platformBaseRepo,
		selectedTagRepo:      selectedTagRepo,
		lineNumberRepo:       lineNumberRepo,
		cacheConfig:          cacheConfig,
		db:                   db,
		rc:                   rc,
	}
}

// ListReadyCampaigns retrieves ready campaigns for bot
func (s *BotCampaignFlowImpl) ListReadyCampaigns(ctx context.Context, platform *string) (*dto.BotListCampaignsResponse, error) {
	cf := models.CampaignFilter{
		Status:         utils.ToPtr(models.CampaignStatusApproved),
		ScheduleBefore: utils.ToPtr(utils.UTCNow()),
		ScheduleAfter:  utils.ToPtr(utils.UTCNow().Add(-1 * time.Hour)),
	}
	if platform != nil {
		p := strings.ToLower(strings.TrimSpace(*platform))
		if p != "" {
			if !models.IsValidCampaignPlatform(p) {
				return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Failed to list ready campaigns", ErrCampaignPlatformInvalid)
			}
			cf.Platform = &p
		}
	}

	readyCampaigns, err := s.campaignRepo.ByFilter(ctx, cf, "created_at DESC", 0, 0)
	if err != nil {
		return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Failed to list ready campaigns", err)
	}

	platformSettingsByID, err := s.loadPlatformSettingsSpecs(ctx, readyCampaigns)
	if err != nil {
		return nil, err
	}

	items := make([]dto.BotGetCampaignResponse, 0, len(readyCampaigns))
	for _, c := range readyCampaigns {
		ensureCampaignSpecDefaults(&c.Spec)

		selectedTags, err := s.selectedTags(ctx, c)
		if err != nil {
			return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Failed to resolve campaign targeting tags", err)
		}
		var smartTestSatisfiedTagIDs []uint
		var smartTestSelectionID *int64
		numAudiences := c.NumAudience
		if c.Spec.UsesSmartTargeting() && c.Phase == models.CampaignPhaseTest {
			intent, intentErr := currentSmartTargetingTestSamplingIntent(ctx, s.selectedTagRepo, s.lineNumberRepo, c, true)
			if intentErr != nil {
				return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Smart Targeting Test sampling intent is invalid", intentErr)
			}
			smartTestSatisfiedTagIDs = append([]uint(nil), intent.satisfied...)
			smartTestSelectionID = c.ActiveSmartTargetingTestSelectionID
			numAudiences = utils.ToPtr(intent.effective)
		}

		// ISSUE: N+1 queries.
		platformBasePrice, err := s.resolvePlatformBasePrice(ctx, c.ID, c.Spec.Platform)
		if err != nil {
			return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Failed to list ready campaigns", err)
		}

		var platformSettings *dto.BotCampaignPlatformSettingsSpec
		if c.Spec.PlatformSettingsID != nil && *c.Spec.PlatformSettingsID != 0 {
			platformSettings = platformSettingsByID[*c.Spec.PlatformSettingsID]
		}

		items = append(items, dto.BotGetCampaignResponse{
			ID:                                c.ID,
			CustomerID:                        c.CustomerID,
			Hidden:                            c.Hidden,
			Status:                            c.Status.String(),
			CreatedAt:                         c.CreatedAt,
			UpdatedAt:                         c.UpdatedAt,
			Title:                             c.Spec.Title,
			Level1:                            c.Spec.Level1,
			Level2s:                           c.Spec.Level2s,
			Level3s:                           c.Spec.Level3s,
			Tags:                              c.Spec.Tags,
			SelectedTags:                      selectedTags,
			TargetingMethod:                   campaignAudienceTargetingMethod(c.Spec),
			Sex:                               c.Spec.Sex,
			City:                              c.Spec.City,
			AdLink:                            c.Spec.AdLink,
			Content:                           c.Spec.Content,
			ShortLinkDomain:                   c.Spec.ShortLinkDomain,
			Category:                          c.Spec.Category,
			Job:                               c.Spec.Job,
			ScheduleAt:                        c.Spec.ScheduleAt,
			LineNumber:                        c.Spec.LineNumber,
			MediaUUID:                         c.Spec.MediaUUID,
			PlatformSettingsID:                c.Spec.PlatformSettingsID,
			PlatformSettings:                  platformSettings,
			Platform:                          c.Spec.Platform,
			PlatformBasePrice:                 platformBasePrice,
			Budget:                            c.Spec.Budget,
			Comment:                           c.Comment,
			NumAudiences:                      numAudiences,
			SampleSizePerTag:                  c.SampleSizePerTag,
			SmartTargetingTestSatisfiedTagIDs: smartTestSatisfiedTagIDs,
			SmartTargetingTestSelectionID:     smartTestSelectionID,

			BundleID: c.BundleID,
			Phase:    campaignPhasePtr(c.Phase),

			AudienceGrades: campaignAudienceGradesOrDefault(c.Spec.AudienceGrades),

			TargetAudienceExcelFileUUID: executionExcelFileUUID(c.Spec),
		})
	}

	return &dto.BotListCampaignsResponse{
		Message: "Ready campaigns retrieved successfully",
		Items:   items,
	}, nil
}

func (s *BotCampaignFlowImpl) selectedTags(ctx context.Context, campaign *models.Campaign) ([]string, error) {
	if campaign == nil {
		return nil, ErrCampaignNotFound
	}
	if !campaign.Spec.UsesSmartTargeting() {
		return nil, nil
	}
	if campaign.BundleID == nil {
		return nil, ErrBundleNotFound
	}
	if err := s.selectedTagRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
		return nil, err
	}
	selected, err := s.selectedTagRepo.ListSelected(ctx, campaign.ID)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, ErrSmartTargetingTagsRequired
	}
	tags := make([]string, 0, len(selected))
	for _, item := range selected {
		if item != nil && item.TagID > 0 {
			tags = append(tags, fmt.Sprint(item.TagID))
		}
	}
	if len(tags) == 0 {
		return nil, ErrSmartTargetingTagsRequired
	}
	return tags, nil
}

func executionExcelFileUUID(spec models.CampaignSpec) *string {
	if !spec.UsesExcelTargeting() {
		return nil
	}
	return spec.TargetAudienceExcelFileUUID
}

func (s *BotCampaignFlowImpl) resolvePlatformBasePrice(ctx context.Context, campaignID uint, platform string) (*uint64, error) {
	basePrice, err := s.readPlatformBasePriceFromMetadata(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if basePrice != nil {
		return basePrice, nil
	}

	pbp, err := s.platformBaseRepo.LatestByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	if pbp == nil {
		return nil, nil
	}
	return &pbp.Price, nil
}

func (s *BotCampaignFlowImpl) readPlatformBasePriceFromMetadata(ctx context.Context, campaignID uint) (*uint64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := s.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil
	}

	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil
	}

	basePrice, ok := parseMetadataUint64(meta["base_price"])
	if !ok {
		return nil, nil
	}
	return &basePrice, nil
}

func (s *BotCampaignFlowImpl) loadPlatformSettingsSpecs(ctx context.Context, campaigns []*models.Campaign) (map[uint]*dto.BotCampaignPlatformSettingsSpec, error) {
	ids := make(map[uint]struct{})
	for _, campaign := range campaigns {
		if campaign.Spec.PlatformSettingsID == nil || *campaign.Spec.PlatformSettingsID == 0 {
			continue
		}
		ids[*campaign.Spec.PlatformSettingsID] = struct{}{}
	}
	if len(ids) == 0 {
		return map[uint]*dto.BotCampaignPlatformSettingsSpec{}, nil
	}

	result := make(map[uint]*dto.BotCampaignPlatformSettingsSpec, len(ids))
	for id := range ids {
		row, err := s.platformSettingsRepo.ByID(ctx, id)
		if err != nil {
			return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Failed to fetch platform settings", err)
		}
		if row == nil {
			err := fmt.Errorf("platform settings not found: id=%d", id)
			return nil, NewBusinessError("BOT_LIST_READY_CAMPAIGNS_FAILED", "Campaign references missing platform settings", err)
		}
		result[id] = &dto.BotCampaignPlatformSettingsSpec{
			ID:           row.ID,
			Platform:     row.Platform,
			Name:         row.Name,
			Description:  row.Description,
			MultimediaID: row.MultimediaID,
			Metadata:     row.Metadata,
			Status:       string(row.Status),
		}
	}

	return result, nil
}

// MoveCampaignToRunning moves campaign status to running
func (s *BotCampaignFlowImpl) MoveCampaignToRunning(ctx context.Context, campaignID uint) error {
	err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := repository.LockCampaignForUpdate(txCtx, campaignID); err != nil {
			return err
		}
		campaign, err := s.campaignRepo.ByID(txCtx, campaignID)
		if err != nil {
			return err
		}
		if campaign == nil {
			return ErrCampaignNotFound
		}
		if campaign.Status != models.CampaignStatusApproved {
			return ErrCampaignNotApproved
		}
		campaign.Status = models.CampaignStatusRunning
		err = s.campaignRepo.Update(txCtx, *campaign)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return NewBusinessError("BOT_MOVE_CAMPAIGN_TO_RUNNING_FAILED", "Failed to move campaign to running", err)
	}
	return nil
}

// MoveCampaignToExecuted moves campaign status to executed
func (s *BotCampaignFlowImpl) MoveCampaignToExecuted(ctx context.Context, campaignID uint) error {
	err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		if err := repository.LockCampaignForUpdate(txCtx, campaignID); err != nil {
			return err
		}
		campaign, err := s.campaignRepo.ByID(txCtx, campaignID)
		if err != nil {
			return err
		}
		if campaign == nil {
			return ErrCampaignNotFound
		}
		if campaign.Status != models.CampaignStatusRunning {
			return ErrCampaignNotRunning
		}
		campaign.Status = models.CampaignStatusExecuted
		err = s.campaignRepo.Update(txCtx, *campaign)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return NewBusinessError("BOT_MOVE_CAMPAIGN_TO_EXECUTED_FAILED", "Failed to move campaign to executed", err)
	}
	return nil
}

// DownloadTargetAudienceExcelFile downloads campaign target-audience Excel file when available.
func (s *BotCampaignFlowImpl) DownloadTargetAudienceExcelFile(ctx context.Context, campaignID uint) (string, string, []byte, error) {
	if campaignID == 0 {
		return "", "", nil, NewBusinessError("INVALID_CAMPAIGN_ID", "campaign id must be greater than 0", nil)
	}

	campaign, err := s.campaignRepo.ByID(ctx, campaignID)
	if err != nil {
		return "", "", nil, NewBusinessError("BOT_DOWNLOAD_TARGET_AUDIENCE_EXCEL_FILE_FAILED", "Failed to fetch campaign", err)
	}
	if campaign == nil {
		return "", "", nil, ErrCampaignNotFound
	}

	if !campaign.Spec.UsesExcelTargeting() ||
		campaign.Spec.TargetAudienceExcelFileUUID == nil ||
		strings.TrimSpace(*campaign.Spec.TargetAudienceExcelFileUUID) == "" {
		return "", "", nil, NewBusinessError(
			"TARGET_AUDIENCE_EXCEL_FILE_NOT_FOUND",
			"Campaign has no target audience excel file",
			ErrCampaignTargetAudienceExcelMediaNotFound,
		)
	}

	excelFileUUID := strings.TrimSpace(*campaign.Spec.TargetAudienceExcelFileUUID)
	asset, err := s.multimediaRepo.ByUUID(ctx, excelFileUUID)
	if err != nil {
		return "", "", nil, NewBusinessError("BOT_DOWNLOAD_TARGET_AUDIENCE_EXCEL_FILE_FAILED", "Failed to fetch target audience excel file", err)
	}
	if asset == nil {
		return "", "", nil, NewBusinessError(
			"TARGET_AUDIENCE_EXCEL_FILE_NOT_FOUND",
			"Target audience excel file not found",
			ErrCampaignTargetAudienceExcelMediaNotFound,
		)
	}

	cleanPath, err := sanitizeMultimediaPath(asset.StoredPath)
	if err != nil {
		return "", "", nil, NewBusinessError("BOT_DOWNLOAD_TARGET_AUDIENCE_EXCEL_FILE_FAILED", "Failed to resolve target audience excel path", err)
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil, NewBusinessError(
				"TARGET_AUDIENCE_EXCEL_FILE_NOT_FOUND",
				"Target audience excel file not found",
				ErrCampaignTargetAudienceExcelMediaNotFound,
			)
		}
		return "", "", nil, NewBusinessError("BOT_DOWNLOAD_TARGET_AUDIENCE_EXCEL_FILE_FAILED", "Failed to read target audience excel file", err)
	}

	filename := strings.TrimSpace(asset.OriginalFilename)
	if filename == "" {
		filename = filepath.Base(cleanPath)
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		contentType = strings.TrimSpace(asset.MimeType)
	}

	return filename, contentType, data, nil
}

// UpdateCampaignStatistics updates the statistics JSON field of a campaign
func (s *BotCampaignFlowImpl) UpdateCampaignStatistics(ctx context.Context, campaignID uint, statistics map[string]any) (*dto.BotUpdateCampaignStatisticsResponse, error) {
	if campaignID == 0 {
		return nil, NewBusinessError("VALIDATION_ERROR", "campaign_id must be greater than 0", nil)
	}
	campaign, err := s.campaignRepo.ByID(ctx, campaignID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_FETCH_FAILED", "Failed to fetch campaign", err)
	}
	if campaign == nil {
		return nil, ErrCampaignNotFound
	}

	data, err := json.Marshal(statistics)
	if err != nil {
		return nil, NewBusinessError("STATISTICS_MARSHAL_FAILED", "Failed to marshal statistics", err)
	}

	if err := s.campaignRepo.UpdateStatistics(ctx, campaignID, data); err != nil {
		return nil, NewBusinessError("CAMPAIGN_STATISTICS_UPDATE_FAILED", "Failed to update campaign statistics", err)
	}

	return &dto.BotUpdateCampaignStatisticsResponse{Message: "Campaign statistics updated"}, nil
}

const audienceUIDsTTL = 900 * 24 * time.Hour

// PushCampaignAudienceUIDs appends a batch of audience uid/code pairs to the campaign's
// file-backed store. Called repeatedly for large campaigns (one call per scheduler chunk).
// The export flow de-duplicates by UID and treats files older than 900 days as expired.
func (s *BotCampaignFlowImpl) PushCampaignAudienceUIDs(ctx context.Context, campaignID uint, items []dto.BotAudienceUIDItem) error {
	if campaignID == 0 {
		return NewBusinessError("VALIDATION_ERROR", "campaign_id must be greater than 0", nil)
	}
	if len(items) == 0 {
		return nil
	}

	if err := appendCampaignAudienceUIDs(campaignID, items); err != nil {
		return NewBusinessError("AUDIENCE_UIDS_STORE_FAILED", "Failed to store audience UIDs", err)
	}
	return nil
}
