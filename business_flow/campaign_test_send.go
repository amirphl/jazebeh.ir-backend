package businessflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/scheduler"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

const campaignTestCooldown = 30 * time.Second
const asyncCampaignTestSendTimeout = 60 * time.Second

func (s *CampaignFlowImpl) SendCampaignTestMessage(ctx context.Context, req *dto.SendCampaignTestMessageRequest, metadata *ClientMetadata) (*dto.SendCampaignTestMessageResponse, error) {
	if req == nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_INVALID_REQUEST", "request is required", ErrInvalidState)
	}
	if strings.TrimSpace(req.UUID) == "" {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_INVALID_REQUEST", "campaign uuid is required", ErrCampaignUUIDRequired)
	}
	if req.CustomerID == 0 {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_INVALID_REQUEST", "customer id is required", ErrCustomerNotFound)
	}
	recipient := strings.TrimSpace(req.TargetPhoneNumber)
	if recipient == "" {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_RECIPIENT_MISSING", "target phone number is missing", ErrCampaignTestRecipientMissing)
	}

	customer, err := getCustomer(ctx, s.customerRepo, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_CUSTOMER_LOOKUP_FAILED", "failed to lookup customer", err)
	}
	campaign, err := getCampaign(ctx, s.campaignRepo, req.UUID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_CAMPAIGN_LOOKUP_FAILED", "failed to lookup campaign", err)
	}
	if !canTestSendCampaign(campaign.Status) {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_STATE_NOT_ALLOWED", "campaign state does not allow test sending", ErrCampaignTestStateNotAllowed)
	}

	platform, err := sanitizeCampaignPlatform(utils.ToPtr(campaign.Spec.Platform))
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_PLATFORM_INVALID", "campaign platform is invalid", err)
	}

	content := ""
	if campaign.Spec.Content != nil {
		content = strings.TrimSpace(*campaign.Spec.Content)
	}
	if content == "" {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_CONTENT_REQUIRED", "campaign content is required", ErrCampaignContentRequired)
	}

	ok, ttl, err := s.tryAcquireCampaignTestCooldown(ctx, customer.ID, campaignTestCooldown)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_COOLDOWN_UNAVAILABLE", "failed to enforce campaign test cooldown", ErrCampaignTestCooldownUnavailable)
	}
	if !ok {
		secs := int(math.Ceil(ttl.Seconds()))
		if secs <= 0 {
			secs = int(campaignTestCooldown.Seconds())
		}
		return nil, NewBusinessErrorf("CAMPAIGN_TEST_SEND_RATE_LIMITED", "Please wait %d seconds before sending another test message", ErrCampaignTestRateLimited, secs)
	}

	fakeUID := buildFakeAudienceUID()
	fakeResolvedLink, err := s.resolveCampaignTestLink(ctx, campaign, recipient, fakeUID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_SHORT_LINK_FAILED", "failed to create campaign test short link", err)
	}
	body := buildCampaignTestMessageBody(platform, campaign.Spec.Content, fakeResolvedLink)

	softErr, hardErr := s.sendCampaignTestMessageBestEffort(ctx, campaign, platform, recipient, body)
	if hardErr != nil {
		errMsg := fmt.Sprintf("Campaign test send failed: %v", hardErr)
		_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignTestSend, errMsg, false, &errMsg, metadata)
		log.Printf("Error sending campaign test message: %v", hardErr)
		return nil, NewBusinessError("CAMPAIGN_TEST_SEND_FAILED", "campaign test send failed", hardErr)
	}

	var warning *string
	auditSuccess := true
	var auditErrMsg *string
	if softErr != nil {
		w := fmt.Sprintf("Provider call returned error; delivery was attempted best-effort: %v", softErr)
		warning = &w
		auditSuccess = false
		e := softErr.Error()
		auditErrMsg = &e
		log.Printf("Warning during campaign test send: %v", softErr)
	}

	auditDesc := map[string]any{
		"campaign_id":      campaign.ID,
		"campaign_uuid":    campaign.UUID.String(),
		"platform":         platform,
		"delivery":         "best_effort_attempted",
		"recipient_masked": maskPhoneNumber(recipient),
		"has_warning":      softErr != nil,
	}
	if fakeResolvedLink != nil {
		auditDesc["fake_resolved_link"] = *fakeResolvedLink
	}
	auditDescBytes, _ := json.Marshal(auditDesc)
	auditDescText := string(auditDescBytes)
	_ = s.createAuditLog(ctx, &customer, models.AuditActionCampaignTestSend, auditDescText, auditSuccess, auditErrMsg, metadata)

	return &dto.SendCampaignTestMessageResponse{
		Message:   "Campaign test message attempted",
		Platform:  platform,
		Recipient: recipient,
		Delivery:  "best_effort_attempted",
		Warning:   warning,
	}, nil
}

func canTestSendCampaign(status models.CampaignStatus) bool {
	return status == models.CampaignStatusInitiated || status == models.CampaignStatusInProgress
}

func (s *CampaignFlowImpl) tryAcquireCampaignTestCooldown(ctx context.Context, customerID uint, ttl time.Duration) (bool, time.Duration, error) {
	if s.rc == nil {
		return false, 0, ErrCampaignTestCooldownUnavailable
	}
	key := redisKey(s.cacheConfig, fmt.Sprintf("campaign_test_send:%d", customerID))
	ok, err := s.rc.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, 0, err
	}
	if ok {
		return true, ttl, nil
	}

	remaining, err := s.rc.TTL(ctx, key).Result()
	if err != nil {
		return false, ttl, nil
	}
	if remaining < 0 {
		remaining = ttl
	}
	return false, remaining, nil
}

func buildFakeAudienceUID() string {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	return "test-" + id
}

func hasCampaignAdLink(adLink *string) bool {
	return adLink != nil && strings.TrimSpace(*adLink) != ""
}

func buildCampaignTestMessageBody(platform string, contentPtr *string, resolvedLink *string) string {
	content := ""
	if contentPtr != nil {
		content = *contentPtr
	}
	replacement := ""
	if resolvedLink != nil {
		replacement = *resolvedLink
	}
	content = strings.ReplaceAll(content, "{YOUR_LINK}", replacement)
	if platform == models.CampaignPlatformSMS {
		return content + "\n" + "لغو۱۱"
	}
	return content
}

func buildCampaignShortLink(shortLinkDomain string, code string) string {
	domain := strings.TrimSpace(shortLinkDomain)
	domain = strings.TrimRight(domain, "/")
	return domain + "/" + code
}

// canonicalShortLinkURL is used exclusively for stored link metadata. SMS
// bodies intentionally retain the scheme-less form to preserve their exact
// length and current provider behaviour.
func canonicalShortLinkURL(shortLink string) string {
	shortLink = strings.TrimSpace(shortLink)
	if strings.HasPrefix(shortLink, "https://") || strings.HasPrefix(shortLink, "http://") {
		return shortLink
	}
	return "https://" + strings.TrimPrefix(shortLink, "//")
}

func (s *CampaignFlowImpl) resolveCampaignTestLink(
	ctx context.Context,
	campaign models.Campaign,
	recipient string,
	fakeUID string,
) (*string, error) {
	if !hasCampaignAdLink(campaign.Spec.AdLink) {
		return nil, nil
	}

	longLink := strings.ReplaceAll(*campaign.Spec.AdLink, "{uid}", fakeUID)
	if campaign.Spec.ShortLinkDomain == nil || strings.TrimSpace(*campaign.Spec.ShortLinkDomain) == "" {
		return &longLink, nil
	}

	lockShortLinkGen()
	defer unlockShortLinkGen()

	fakeCode, err := s.allocateNextCampaignTestShortCode(ctx)
	if err != nil {
		return nil, err
	}
	normalizedRecipient, err := normalizeOTPMobile(recipient)
	if err != nil {
		return nil, err
	}
	messageShortLink := buildCampaignShortLink(*campaign.Spec.ShortLinkDomain, fakeCode)
	shortLinkRow := &models.ShortLink{
		UID:         fakeCode,
		CampaignID:  &campaign.ID,
		PhoneNumber: utils.ToPtr(normalizedRecipient),
		LongLink:    longLink,
		ShortLink:   canonicalShortLinkURL(messageShortLink),
		IsTest:      true,
	}
	if err := s.shortLinkRepo.Save(ctx, shortLinkRow); err != nil {
		return nil, err
	}
	if err := publishShortLinkMappings(ctx, s.shortLinkRepo, s.shortLinkPublisher, []*models.ShortLink{shortLinkRow}); err != nil {
		return nil, fmt.Errorf("publish campaign test short link: %w", err)
	}

	return &messageShortLink, nil
}

func (s *CampaignFlowImpl) allocateNextCampaignTestShortCode(ctx context.Context) (string, error) {
	cutoff := time.Date(2025, 11, 10, 15, 45, 11, 401492000, time.UTC)
	lastUID, err := s.shortLinkRepo.GetMaxUIDSince(ctx, cutoff)
	if err != nil {
		return "", err
	}

	var seq uint64
	if lastUID != "" {
		seq, err = decodeBase36Compat(lastUID)
		if err != nil {
			return "", err
		}
		seq++
	}

	return formatSequentialUIDCompat(seq)
}

func (s *CampaignFlowImpl) sendCampaignTestMessageBestEffort(
	ctx context.Context,
	campaign models.Campaign,
	platform string,
	recipient string,
	body string,
) (softErr error, hardErr error) {
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}

	switch platform {
	case models.CampaignPlatformSMS:
		lineNumber := ""
		if campaign.Spec.LineNumber != nil {
			lineNumber = strings.TrimSpace(*campaign.Spec.LineNumber)
		}
		if lineNumber == "" {
			return nil, ErrCampaignLineNumberRequired
		}
		if _, err := s.fetchLineNumberPriceFactor(ctx, &lineNumber); err != nil {
			return nil, err
		}
		line, err := s.lineNumberRepo.ByValue(ctx, lineNumber)
		if err != nil {
			return nil, err
		}
		if line == nil {
			return nil, ErrLineNumberNotFound
		}
		provider := models.SMSProvider(strings.ToLower(strings.TrimSpace(string(line.Provider))))
		if provider == "" {
			provider = models.SMSProviderPayamSMS
		}
		if !models.IsValidSMSProvider(provider) {
			return nil, fmt.Errorf("SMS provider %q is not supported for campaign test sends", provider)
		}

		if provider == models.SMSProviderCandoo {
			client, err := s.newTestCandooSMSProvider()
			if err != nil {
				return nil, err
			}
			if readiness, ok := client.(scheduler.SMSProviderReadinessChecker); ok {
				if err := readiness.Validate(); err != nil {
					return nil, fmt.Errorf("validate Candoo campaign test configuration: %w", err)
				}
			}
			customerID := int64(campaign.CustomerID)
			if customerID <= 0 {
				return nil, fmt.Errorf("Candoo campaign test send requires a positive customer id")
			}
			s.runAsyncCampaignTestSend(baseCtx, platform, recipient, func(sendCtx context.Context) error {
				s.logTestSendProxyUsage(platform, recipient)
				resp, err := client.SendBatch(sendCtx, lineNumber, []scheduler.SMSProviderMessage{{
					Recipient:          recipient,
					Body:               body,
					TrackingID:         buildProviderTestID("test-sms", campaign.CustomerID),
					ProviderCustomerID: &customerID,
				}})
				if err == nil && len(resp.Items) > 0 {
					log.Printf("sendCampaignTestMessageBestEffort: Candoo response for campaign test send (line number: %s): %+v", lineNumber, resp.Items)
				}
				return err
			})
			return nil, nil
		}

		client, err := s.newTestPayamSMSClient()
		if err != nil {
			return nil, err
		}
		s.runAsyncCampaignTestSend(baseCtx, platform, recipient, func(sendCtx context.Context) error {
			s.logTestSendProxyUsage(platform, recipient)
			resp, err := client.SendBatch(sendCtx, lineNumber, []scheduler.PayamSMSItem{{
				Recipient:  recipient,
				Body:       body,
				TrackingID: buildProviderTestID("test-sms", campaign.CustomerID),
			}})
			if err == nil && len(resp.Items) > 0 {
				log.Printf("sendCampaignTestMessageBestEffort: PayamSMS response for campaign test send (line number: %s): %s", lineNumber, formatPayamSMSResponseItems(resp.Items))
			}
			return err
		})
		return nil, nil

	case models.CampaignPlatformBale:
		settings, err := s.requireActivePlatformSettings(ctx, campaign, platform)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s.baleConfig.APIAccessKey) == "" {
			return nil, fmt.Errorf("bale api-access-key is not configured")
		}
		botID, err := extractBaleBotIDFromSettings(settings)
		if err != nil {
			return nil, err
		}

		baleClient, err := s.newTestBaleClient()
		if err != nil {
			return nil, err
		}
		var mediaPath string
		if campaign.Spec.MediaUUID != nil {
			path, err := s.resolveCampaignMediaPath(ctx, campaign)
			if err != nil {
				return nil, err
			}
			mediaPath = path
		}

		s.runAsyncCampaignTestSend(baseCtx, platform, recipient, func(sendCtx context.Context) error {
			s.logTestSendProxyUsage(platform, recipient)
			var fileID *string
			if mediaPath != "" {
				up, err := baleClient.UploadFile(sendCtx, mediaPath)
				if err != nil {
					return err
				}
				if up != nil && strings.TrimSpace(up.FileID) != "" {
					v := strings.TrimSpace(up.FileID)
					fileID = &v
				}
			}

			resp, err := baleClient.SendMessage(sendCtx, &scheduler.BaleSendMessageRequest{
				RequestID:   buildProviderTestID("test-bale", campaign.CustomerID),
				BotID:       botID,
				PhoneNumber: recipient,
				MessageData: scheduler.BaleSendMessageData{
					Message: &scheduler.BaleMessage{
						Text:   body,
						FileID: fileID,
					},
				},
			})
			if err == nil && resp != nil {
				log.Printf("sendCampaignTestMessageBestEffort: Response from Bale provider: %+v", resp)
			}
			return err
		})
		return nil, nil

	case models.CampaignPlatformRubika:
		settings, err := s.requireActivePlatformSettings(ctx, campaign, platform)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s.rubikaConfig.Token) == "" {
			return nil, fmt.Errorf("rubika token is not configured")
		}
		serviceID, err := extractRubikaServiceIDFromSettings(settings)
		if err != nil {
			return nil, err
		}

		rubikaClient, err := s.newTestRubikaClient()
		if err != nil {
			return nil, err
		}
		var mediaPath string
		if campaign.Spec.MediaUUID != nil {
			path, err := s.resolveCampaignMediaPath(ctx, campaign)
			if err != nil {
				return nil, err
			}
			mediaPath = path
		}

		s.runAsyncCampaignTestSend(baseCtx, platform, recipient, func(sendCtx context.Context) error {
			s.logTestSendProxyUsage(platform, recipient)
			var fileID *string
			if mediaPath != "" {
				up, err := rubikaClient.UploadFile(sendCtx, mediaPath)
				if err != nil {
					return err
				}
				if up != nil && strings.TrimSpace(up.Data.FileID) != "" {
					v := strings.TrimSpace(up.Data.FileID)
					fileID = &v
				}
			}

			resp, err := rubikaClient.SendBulkMessages(sendCtx, serviceID, []scheduler.RubikaMessagePayload{{
				Phone:  recipient,
				Text:   body,
				FileID: fileID,
			}})
			if err == nil && resp != nil {
				log.Printf("sendCampaignTestMessageBestEffort: Response from Rubika provider: %+v", resp)
			}

			return err
		})
		return nil, nil

	case models.CampaignPlatformSPlus:
		settings, err := s.requireActivePlatformSettings(ctx, campaign, platform)
		if err != nil {
			return nil, err
		}
		botID, err := extractSplusBotIDFromSettings(settings)
		if err != nil {
			return nil, err
		}

		splusClient, err := s.newTestSplusClient()
		if err != nil {
			return nil, err
		}
		var mediaPath string
		if campaign.Spec.MediaUUID != nil {
			path, err := s.resolveCampaignMediaPath(ctx, campaign)
			if err != nil {
				return nil, err
			}
			mediaPath = path
		}

		s.runAsyncCampaignTestSend(baseCtx, platform, recipient, func(sendCtx context.Context) error {
			s.logTestSendProxyUsage(platform, recipient)
			var fileID *string
			if mediaPath != "" {
				up, err := splusClient.UploadFile(sendCtx, botID, mediaPath)
				if err != nil {
					return err
				}
				if up != nil && strings.TrimSpace(up.FileID) != "" {
					v := strings.TrimSpace(up.FileID)
					fileID = &v
				}
			}

			req := &scheduler.SplusSendMessageRequest{
				PhoneNumber: recipient,
				Text:        body,
				FileID:      fileID,
			}
			log.Printf(
				"sendCampaignTestMessageBestEffort: sending splus test message to %s (bot_id_len=%d text_len=%d has_file=%t)",
				maskPhoneNumber(recipient),
				len(strings.TrimSpace(botID)),
				len(strings.TrimSpace(body)),
				fileID != nil && strings.TrimSpace(*fileID) != "",
			)
			resp, err := splusClient.SendMessage(sendCtx, botID, req)
			if err == nil && resp != nil {
				log.Printf("sendCampaignTestMessageBestEffort: Response from SPlus provider: %+v", resp)
			}
			return err
		})
		return nil, nil

	default:
		return nil, ErrCampaignPlatformInvalid
	}
}

// formatPayamSMSResponseItems deliberately dereferences the optional provider
// fields. Formatting the response structs with %+v renders pointer addresses,
// which conceals the error code and description needed to diagnose rejected
// test sends.
func formatPayamSMSResponseItems(items []scheduler.PayamSMSResponseItem) string {
	formatted := make([]string, 0, len(items))
	for _, item := range items {
		formatted = append(formatted, fmt.Sprintf(
			"{tracking_id=%q mobile=%q server_id=%s error_code=%s description=%s}",
			item.TrackingID,
			item.Mobile,
			formatOptionalPayamSMSResponseValue(item.ServerID),
			formatOptionalPayamSMSResponseValue(item.ErrorCode),
			formatOptionalPayamSMSResponseValue(item.Desc),
		))
	}
	return "[" + strings.Join(formatted, " ") + "]"
}

func formatOptionalPayamSMSResponseValue(value *string) string {
	if value == nil {
		return "null"
	}
	return strconv.Quote(*value)
}

func (s *CampaignFlowImpl) newTestPayamSMSClient() (scheduler.PayamSMSClient, error) {
	if strings.TrimSpace(s.irHTTPSProxy) == "" {
		return scheduler.NewPayamSMSClient(s.payamSMSConfig), nil
	}
	return scheduler.NewPayamSMSClientWithHTTPSProxy(s.payamSMSConfig, s.irHTTPSProxy)
}

func (s *CampaignFlowImpl) newTestCandooSMSProvider() (scheduler.SMSProvider, error) {
	if strings.TrimSpace(s.irHTTPSProxy) == "" {
		return scheduler.NewCandooSMSProvider(s.candooSMSConfig), nil
	}
	return scheduler.NewCandooSMSProviderWithHTTPSProxy(s.candooSMSConfig, s.irHTTPSProxy)
}

func (s *CampaignFlowImpl) newTestBaleClient() (scheduler.BaleClient, error) {
	if strings.TrimSpace(s.irHTTPSProxy) == "" {
		return scheduler.NewBaleClient(s.baleConfig), nil
	}
	return scheduler.NewBaleClientWithHTTPSProxy(s.baleConfig, s.irHTTPSProxy)
}

func (s *CampaignFlowImpl) newTestRubikaClient() (scheduler.RubikaClient, error) {
	if strings.TrimSpace(s.irHTTPSProxy) == "" {
		return scheduler.NewRubikaClient(s.rubikaConfig), nil
	}
	return scheduler.NewRubikaClientWithHTTPSProxy(s.rubikaConfig, s.irHTTPSProxy)
}

func (s *CampaignFlowImpl) newTestSplusClient() (scheduler.SplusClient, error) {
	if strings.TrimSpace(s.irHTTPSProxy) == "" {
		return scheduler.NewSplusClient(s.splusConfig), nil
	}
	return scheduler.NewSplusClientWithHTTPSProxy(s.splusConfig, s.irHTTPSProxy)
}

func (s *CampaignFlowImpl) logTestSendProxyUsage(platform string, recipient string) {
	proxyURL := strings.TrimSpace(s.irHTTPSProxy)
	if proxyURL == "" {
		log.Printf(
			"sendCampaignTestMessageBestEffort: using direct connection for %s test send to %s",
			platform,
			maskPhoneNumber(recipient),
		)
		return
	}
	log.Printf(
		"sendCampaignTestMessageBestEffort: using IR HTTPS proxy for %s test send to %s via %s",
		platform,
		maskPhoneNumber(recipient),
		maskProxyURL(proxyURL),
	)
}

func (s *CampaignFlowImpl) runAsyncCampaignTestSend(
	baseCtx context.Context,
	platform string,
	recipient string,
	fn func(context.Context) error,
) {
	go func() {
		sendCtx, cancel := context.WithTimeout(baseCtx, asyncCampaignTestSendTimeout)
		defer cancel()

		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("sendCampaignTestMessageBestEffort: panic during async %s test send to %s: %v", platform, maskPhoneNumber(recipient), recovered)
			}
		}()

		if err := fn(sendCtx); err != nil {
			log.Printf("sendCampaignTestMessageBestEffort: async %s test send to %s failed: %v", platform, maskPhoneNumber(recipient), err)
		}
	}()
}

func (s *CampaignFlowImpl) resolveCampaignMediaPath(ctx context.Context, campaign models.Campaign) (string, error) {
	if campaign.Spec.MediaUUID == nil {
		return "", ErrCampaignMediaNotFound
	}

	asset, err := s.multimediaRepo.ByUUID(ctx, campaign.Spec.MediaUUID.String())
	if err != nil {
		return "", err
	}
	if asset == nil || asset.CustomerID != campaign.CustomerID {
		return "", ErrCampaignMediaNotFound
	}

	path, err := sanitizeMultimediaPath(asset.StoredPath)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", ErrCampaignMediaNotFound
		}
		return "", err
	}
	return filepath.Clean(path), nil
}

func (s *CampaignFlowImpl) requireActivePlatformSettings(ctx context.Context, campaign models.Campaign, platform string) (*models.PlatformSettings, error) {
	if campaign.Spec.PlatformSettingsID == nil || *campaign.Spec.PlatformSettingsID == 0 {
		return nil, ErrCampaignPlatformSettingRequired
	}
	rows, err := s.platformSettingsRepo.ByFilter(ctx, models.PlatformSettingsFilter{
		ID:         campaign.Spec.PlatformSettingsID,
		CustomerID: &campaign.CustomerID,
		Platform:   &platform,
		Status:     utils.ToPtr(models.PlatformSettingsStatusActive),
	}, "", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0] == nil {
		return nil, ErrCampaignTestPlatformSettingsInvalid
	}
	return rows[0], nil
}

func extractBaleBotIDFromSettings(settings *models.PlatformSettings) (int64, error) {
	if settings == nil || settings.Metadata == nil {
		return 0, fmt.Errorf("%w: missing metadata", ErrCampaignTestPlatformSettingsInvalid)
	}
	raw, ok := settings.Metadata["bale_bot_id"]
	if !ok {
		return 0, fmt.Errorf("%w: bale_bot_id is missing", ErrCampaignTestPlatformSettingsInvalid)
	}

	switch v := raw.(type) {
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("%w: bale_bot_id must be positive", ErrCampaignTestPlatformSettingsInvalid)
		}
		return int64(v), nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("%w: bale_bot_id must be positive", ErrCampaignTestPlatformSettingsInvalid)
		}
		return v, nil
	case float64:
		if v <= 0 || v != float64(int64(v)) {
			return 0, fmt.Errorf("%w: bale_bot_id must be positive integer", ErrCampaignTestPlatformSettingsInvalid)
		}
		return int64(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, fmt.Errorf("%w: bale_bot_id must not be empty", ErrCampaignTestPlatformSettingsInvalid)
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("%w: bale_bot_id must be positive integer", ErrCampaignTestPlatformSettingsInvalid)
		}
		return id, nil
	case json.Number:
		id, err := v.Int64()
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("%w: bale_bot_id must be positive integer", ErrCampaignTestPlatformSettingsInvalid)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("%w: bale_bot_id has unsupported type %T", ErrCampaignTestPlatformSettingsInvalid, raw)
	}
}

func extractSplusBotIDFromSettings(settings *models.PlatformSettings) (string, error) {
	if settings == nil || settings.Metadata == nil {
		return "", fmt.Errorf("%w: missing metadata", ErrCampaignTestPlatformSettingsInvalid)
	}
	raw, ok := settings.Metadata["splus_bot_id"]
	if !ok {
		return "", fmt.Errorf("%w: splus_bot_id is missing", ErrCampaignTestPlatformSettingsInvalid)
	}

	switch v := raw.(type) {
	case string:
		id := strings.TrimSpace(v)
		if id == "" {
			return "", fmt.Errorf("%w: splus_bot_id must not be empty", ErrCampaignTestPlatformSettingsInvalid)
		}
		return id, nil
	default:
		return "", fmt.Errorf("%w: splus_bot_id has unsupported type %T", ErrCampaignTestPlatformSettingsInvalid, raw)
	}
}

func extractRubikaServiceIDFromSettings(settings *models.PlatformSettings) (string, error) {
	if settings == nil || settings.Metadata == nil {
		return "", fmt.Errorf("%w: missing metadata", ErrCampaignTestPlatformSettingsInvalid)
	}
	raw, ok := settings.Metadata["rubika_service_id"]
	if !ok {
		return "", fmt.Errorf("%w: rubika_service_id is missing", ErrCampaignTestPlatformSettingsInvalid)
	}

	switch v := raw.(type) {
	case string:
		id := strings.TrimSpace(v)
		if id == "" {
			return "", fmt.Errorf("%w: rubika_service_id must not be empty", ErrCampaignTestPlatformSettingsInvalid)
		}
		return id, nil
	case int:
		if v <= 0 {
			return "", fmt.Errorf("%w: rubika_service_id must be positive", ErrCampaignTestPlatformSettingsInvalid)
		}
		return strconv.Itoa(v), nil
	case int64:
		if v <= 0 {
			return "", fmt.Errorf("%w: rubika_service_id must be positive", ErrCampaignTestPlatformSettingsInvalid)
		}
		return strconv.FormatInt(v, 10), nil
	case float64:
		if v <= 0 {
			return "", fmt.Errorf("%w: rubika_service_id must be positive", ErrCampaignTestPlatformSettingsInvalid)
		}
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), nil
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case json.Number:
		s := strings.TrimSpace(v.String())
		if s == "" {
			return "", fmt.Errorf("%w: rubika_service_id must not be empty", ErrCampaignTestPlatformSettingsInvalid)
		}
		return s, nil
	default:
		return "", fmt.Errorf("%w: rubika_service_id has unsupported type %T", ErrCampaignTestPlatformSettingsInvalid, raw)
	}
}

func maskPhoneNumber(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if len(trimmed) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(trimmed)-4) + trimmed[len(trimmed)-4:]
}

func maskProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "[invalid proxy url]"
	}

	host := parsed.Hostname()
	if host == "" {
		host = "[unknown-host]"
	}

	if port := parsed.Port(); port != "" {
		host = host + ":" + port
	}

	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}

	if parsed.User != nil {
		return fmt.Sprintf("%s://[redacted]@%s", scheme, host)
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func buildProviderTestID(prefix string, customerID uint) string {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(raw) > 8 {
		raw = raw[:8]
	}
	return fmt.Sprintf("%s-%d-%s", prefix, customerID, raw)
}
