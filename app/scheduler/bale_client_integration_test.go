package scheduler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

var (
	itBaleAccessToken  = flag.String("bale_access_token", "", "Bale/Najva API access token for integration test")
	itBaleBotToken     = flag.String("bale_bot_token", "", "Bale bot token/id (numeric sender id) for integration test")
	itBalePhones       = flag.String("bale_phone_numbers", "", "Comma separated phone numbers (1-2 numbers)")
	itBaleProvider     = flag.String("bale_provider", "najva_v2", "Provider: najva_v2|legacy|auto")
	itBaleNajvaDomain  = flag.String("bale_najva_domain", "", "Optional Najva domain override")
	itBaleLegacyDomain = flag.String("bale_legacy_domain", "", "Optional Safir domain override")
)

func TestBaleClientIntegrationMainFlows(t *testing.T) {
	cfg, botID, phones := baleITConfig(t)
	client := newHTTPBaleClient(cfg)
	provider := normalizeBaleProvider(cfg.Provider)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Run("SupportsStatusTracking", func(t *testing.T) {
		out := client.SupportsStatusTracking()
		t.Logf("SupportsStatusTracking output: %v", out)
	})

	var collectedMessageIDs []string
	var uploadedFileID string

	t.Run("SendMessage", func(t *testing.T) {
		reqID := fmt.Sprintf("it-send-%d", time.Now().UnixNano())
		req := &BaleSendMessageRequest{
			RequestID:   reqID,
			BotID:       botID,
			PhoneNumber: phones[0],
			MessageData: BaleSendMessageData{
				Message: &BaleMessage{
					Text: fmt.Sprintf("Integration test SendMessage at %s", time.Now().UTC().Format(time.RFC3339)),
				},
			},
		}

		resp, err := client.SendMessage(ctx, req)
		printJSON(t, "SendMessage output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendMessage failed: %v", err)
		}
		if resp == nil {
			t.Fatalf("SendMessage returned nil response")
		}
		if strings.TrimSpace(resp.RequestID) != reqID {
			t.Fatalf("SendMessage request id mismatch: got=%q want=%q", resp.RequestID, reqID)
		}
		if msgID := strings.TrimSpace(resp.MessageID); msgID != "" {
			collectedMessageIDs = append(collectedMessageIDs, msgID)
		}
	})

	t.Run("SendMessageOTP", func(t *testing.T) {
		reqID := fmt.Sprintf("it-otp-%d", time.Now().UnixNano())
		req := &BaleSendMessageRequest{
			RequestID:   reqID,
			BotID:       botID,
			PhoneNumber: phones[0],
			MessageData: BaleSendMessageData{
				OTP: &BaleOTP{OTP: "123456"},
			},
		}

		resp, err := client.SendMessage(ctx, req)
		printJSON(t, "SendMessageOTP output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendMessageOTP failed: %v", err)
		}
		if resp == nil {
			t.Fatalf("SendMessageOTP returned nil response")
		}
		if strings.TrimSpace(resp.RequestID) != reqID {
			t.Fatalf("SendMessageOTP request id mismatch: got=%q want=%q", resp.RequestID, reqID)
		}
		if msgID := strings.TrimSpace(resp.MessageID); msgID != "" {
			collectedMessageIDs = append(collectedMessageIDs, msgID)
		}
	})

	t.Run("SendBatch", func(t *testing.T) {
		items := make([]BaleSendMessageRequest, 0, len(phones))
		for i, phone := range phones {
			items = append(items, BaleSendMessageRequest{
				RequestID:   fmt.Sprintf("it-batch-%d-%d", time.Now().UnixNano(), i),
				BotID:       botID,
				PhoneNumber: phone,
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{
						Text: fmt.Sprintf("Integration test SendBatch at %s", time.Now().UTC().Format(time.RFC3339)),
					},
				},
			})
		}

		resp, err := client.SendBatch(ctx, items)
		printJSON(t, "SendBatch output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendBatch failed: %v", err)
		}
		if len(resp) != len(items) {
			t.Fatalf("SendBatch response length mismatch: got=%d want=%d", len(resp), len(items))
		}
		for _, item := range resp {
			if msgID := strings.TrimSpace(item.MessageID); msgID != "" {
				collectedMessageIDs = append(collectedMessageIDs, msgID)
			}
		}
	})

	t.Run("SendBatchP2P", func(t *testing.T) {
		if len(phones) < 2 {
			t.Skip("SendBatchP2P needs two phone numbers")
		}

		items := []BaleSendMessageRequest{
			{
				RequestID:   fmt.Sprintf("it-p2p-%d-0", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: phones[0],
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: fmt.Sprintf("P2P message A at %s", time.Now().UTC().Format(time.RFC3339))},
				},
			},
			{
				RequestID:   fmt.Sprintf("it-p2p-%d-1", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: phones[1],
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: fmt.Sprintf("P2P message B at %s", time.Now().UTC().Format(time.RFC3339))},
				},
			},
		}

		resp, err := client.SendBatch(ctx, items)
		printJSON(t, "SendBatchP2P output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendBatchP2P failed: %v", err)
		}
		if len(resp) != len(items) {
			t.Fatalf("SendBatchP2P response length mismatch: got=%d want=%d", len(resp), len(items))
		}
		for _, item := range resp {
			if msgID := strings.TrimSpace(item.MessageID); msgID != "" {
				collectedMessageIDs = append(collectedMessageIDs, msgID)
			}
		}
	})

	t.Run("SendBatchFiltersEmptyPhones", func(t *testing.T) {
		items := []BaleSendMessageRequest{
			{
				RequestID:   fmt.Sprintf("it-filter-%d-empty", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: "   ",
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: "this should be filtered out"},
				},
			},
			{
				RequestID:   fmt.Sprintf("it-filter-%d-valid", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: phones[0],
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: fmt.Sprintf("Filtered batch valid item at %s", time.Now().UTC().Format(time.RFC3339))},
				},
			},
		}

		resp, err := client.SendBatch(ctx, items)
		printJSON(t, "SendBatchFiltersEmptyPhones output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendBatchFiltersEmptyPhones failed: %v", err)
		}
		if len(resp) != 1 {
			t.Fatalf("expected exactly one sent item after filtering, got=%d", len(resp))
		}
		if msgID := strings.TrimSpace(resp[0].MessageID); msgID != "" {
			collectedMessageIDs = append(collectedMessageIDs, msgID)
		}
	})

	t.Run("SendBatchAllEmptyPhones", func(t *testing.T) {
		items := []BaleSendMessageRequest{
			{
				RequestID:   fmt.Sprintf("it-filterall-%d-0", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: " ",
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: "should be filtered"},
				},
			},
			{
				RequestID:   fmt.Sprintf("it-filterall-%d-1", time.Now().UnixNano()),
				BotID:       botID,
				PhoneNumber: "\t",
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{Text: "should be filtered"},
				},
			},
		}

		resp, err := client.SendBatch(ctx, items)
		printJSON(t, "SendBatchAllEmptyPhones output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("SendBatchAllEmptyPhones failed: %v", err)
		}
		if len(resp) != 0 {
			t.Fatalf("expected zero sent items when all phones are empty, got=%d", len(resp))
		}
	})

	t.Run("SendMessageNajvaPhoneNormalizationVariants", func(t *testing.T) {
		if provider == baleProviderLegacy {
			t.Skip("phone normalization is Najva-specific")
		}

		base := normalizeNajvaPhoneNumber(phones[0])
		if strings.TrimSpace(base) == "" || !strings.HasPrefix(base, "0") || len(base) < 2 {
			t.Skipf("cannot derive Najva variants from input phone %q", phones[0])
		}

		withoutZero := strings.TrimPrefix(base, "0")
		variants := []string{
			"98" + withoutZero,
			"+98" + withoutZero,
		}

		for i, phoneVariant := range variants {
			reqID := fmt.Sprintf("it-normalize-%d-%d", time.Now().UnixNano(), i)
			req := &BaleSendMessageRequest{
				RequestID:   reqID,
				BotID:       botID,
				PhoneNumber: phoneVariant,
				MessageData: BaleSendMessageData{
					Message: &BaleMessage{
						Text: fmt.Sprintf(
							"Integration test phone normalization variant %d at %s",
							i,
							time.Now().UTC().Format(time.RFC3339),
						),
					},
				},
			}

			resp, err := client.SendMessage(ctx, req)
			printJSON(t, "SendMessageNajvaPhoneNormalizationVariants output", map[string]any{
				"variant":  phoneVariant,
				"response": resp,
				"error":    errString(err),
			})
			if err != nil {
				t.Fatalf("SendMessage variant %q failed: %v", phoneVariant, err)
			}
			if resp == nil {
				t.Fatalf("SendMessage variant %q returned nil response", phoneVariant)
			}
			if strings.TrimSpace(resp.RequestID) != reqID {
				t.Fatalf("SendMessage variant request id mismatch: got=%q want=%q", resp.RequestID, reqID)
			}
			if msgID := strings.TrimSpace(resp.MessageID); msgID != "" {
				collectedMessageIDs = append(collectedMessageIDs, msgID)
			}
		}
	})

	t.Run("UploadFile", func(t *testing.T) {
		path := writeTinyPNG(t)
		resp, err := client.UploadFile(ctx, path)
		printJSON(t, "UploadFile output", map[string]any{"response": resp, "error": errString(err), "path": path})
		if err != nil {
			t.Fatalf("UploadFile failed: %v", err)
		}
		if resp == nil {
			t.Fatalf("UploadFile returned nil response")
		}
		if strings.TrimSpace(resp.FileID) == "" {
			t.Fatalf("UploadFile returned empty file id")
		}
		uploadedFileID = strings.TrimSpace(resp.FileID)
	})

	t.Run("SendMessageWithUploadedFileID", func(t *testing.T) {
		if strings.TrimSpace(uploadedFileID) == "" {
			t.Skip("upload file id is empty; upload test likely skipped/failed")
		}

		reqID := fmt.Sprintf("it-sendfile-%d", time.Now().UnixNano())
		req := &BaleSendMessageRequest{
			RequestID:   reqID,
			BotID:       botID,
			PhoneNumber: phones[0],
			MessageData: BaleSendMessageData{
				Message: &BaleMessage{
					Text:   fmt.Sprintf("Integration test SendMessage with file_id at %s", time.Now().UTC().Format(time.RFC3339)),
					FileID: &uploadedFileID,
				},
			},
		}

		resp, err := client.SendMessage(ctx, req)
		printJSON(t, "SendMessageWithUploadedFileID output", map[string]any{
			"request_file_id": uploadedFileID,
			"response":        resp,
			"error":           errString(err),
		})
		if err != nil {
			t.Fatalf("SendMessageWithUploadedFileID failed: %v", err)
		}
		if resp == nil {
			t.Fatalf("SendMessageWithUploadedFileID returned nil response")
		}
		if strings.TrimSpace(resp.RequestID) != reqID {
			t.Fatalf("SendMessageWithUploadedFileID request id mismatch: got=%q want=%q", resp.RequestID, reqID)
		}
		if msgID := strings.TrimSpace(resp.MessageID); msgID != "" {
			collectedMessageIDs = append(collectedMessageIDs, msgID)
		}
	})

	t.Run("UploadFileInvalidExtension", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.txt")
		if err := os.WriteFile(path, []byte("invalid for najva"), 0o644); err != nil {
			t.Fatalf("write invalid extension sample: %v", err)
		}

		resp, err := client.UploadFile(ctx, path)
		printJSON(t, "UploadFileInvalidExtension output", map[string]any{"response": resp, "error": errString(err), "path": path})
		if strings.EqualFold(normalizeBaleProvider(cfg.Provider), baleProviderLegacy) {
			t.Skip("legacy provider accepts more file types via safir; invalid-extension guard is najva-specific")
		}
		if err == nil {
			t.Fatalf("expected UploadFileInvalidExtension to fail")
		}
	})

	t.Run("FetchStatus", func(t *testing.T) {
		if provider == baleProviderLegacy {
			resp, err := client.FetchStatus(ctx, []string{"1"})
			printJSON(t, "FetchStatus legacy output", map[string]any{"response": resp, "error": errString(err)})
			if err == nil {
				t.Fatalf("legacy provider must not support status tracking")
			}
			return
		}

		if len(collectedMessageIDs) == 0 {
			t.Fatalf("FetchStatus test needs at least one messageID from SendMessage/SendBatch")
		}

		// Keep only numeric message IDs because Najva status endpoint expects integers.
		numericIDs := make([]string, 0, len(collectedMessageIDs))
		for _, id := range collectedMessageIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, err := strconv.ParseInt(id, 10, 64); err == nil {
				numericIDs = append(numericIDs, id)
			}
		}
		if len(numericIDs) == 0 {
			t.Fatalf("no numeric message IDs were returned; cannot call FetchStatus. all IDs=%v", collectedMessageIDs)
		}

		resp, err := client.FetchStatus(ctx, numericIDs)
		printJSON(t, "FetchStatus output", map[string]any{"response": resp, "error": errString(err), "messageIDs": numericIDs})
		if err != nil {
			t.Fatalf("FetchStatus failed: %v", err)
		}
		if len(resp.Items) == 0 || len(resp.Items) != len(numericIDs) {
			t.Fatalf("FetchStatus response length mismatch: got=%d want=%d", len(resp.Items), len(numericIDs))
		}

		expected := make(map[string]struct{}, len(numericIDs))
		for _, id := range numericIDs {
			expected[id] = struct{}{}
		}

		seen := make(map[string]struct{}, len(resp.Items))
		for _, item := range resp.Items {
			id := strings.TrimSpace(item.MessageID)
			if id == "" {
				t.Fatalf("FetchStatus returned empty message_id item: %+v", item)
			}
			if _, ok := expected[id]; !ok {
				t.Fatalf("FetchStatus returned unexpected message_id %q (expected one of %v)", id, numericIDs)
			}
			if _, dup := seen[id]; dup {
				t.Fatalf("FetchStatus returned duplicate message_id %q", id)
			}
			seen[id] = struct{}{}

			if strings.TrimSpace(item.StatusText) == "" {
				t.Fatalf("FetchStatus returned empty status_text for message_id %q", id)
			}
			if item.Status <= 0 {
				t.Fatalf("FetchStatus returned invalid status=%d for message_id %q", item.Status, id)
			}
		}

		for _, id := range numericIDs {
			if _, ok := seen[id]; !ok {
				t.Fatalf("FetchStatus missing message_id %q in response", id)
			}
		}
	})

	t.Run("FetchStatusEdgeCases", func(t *testing.T) {
		resp, err := client.FetchStatus(ctx, []string{"", "   "})
		printJSON(t, "FetchStatusEdgeCases-empty-input output", map[string]any{"response": resp, "error": errString(err)})
		if err != nil {
			t.Fatalf("empty-input FetchStatus should not fail: %v", err)
		}
		if len(resp.Items) != 0 {
			t.Fatalf("expected empty response for empty ids, got=%d", len(resp.Items))
		}

		resp, err = client.FetchStatus(ctx, []string{"bad-id"})
		printJSON(t, "FetchStatusEdgeCases-invalid-id output", map[string]any{"response": resp, "error": errString(err)})
		if err == nil {
			t.Fatalf("invalid id FetchStatus should fail")
		}
	})
}

func baleITConfig(t *testing.T) (config.BaleConfig, int64, []string) {
	t.Helper()

	accessToken := firstNonEmpty(
		strings.TrimSpace(*itBaleAccessToken),
		strings.TrimSpace(os.Getenv("BALE_ACCESS_TOKEN")),
	)
	botToken := firstNonEmpty(
		strings.TrimSpace(*itBaleBotToken),
		strings.TrimSpace(os.Getenv("BALE_BOT_TOKEN")),
	)
	phonesRaw := firstNonEmpty(
		strings.TrimSpace(*itBalePhones),
		strings.TrimSpace(os.Getenv("BALE_PHONE_NUMBERS")),
	)

	if accessToken == "" || botToken == "" || phonesRaw == "" {
		t.Skip("integration args are missing; run with -bale_access_token, -bale_bot_token, -bale_phone_numbers")
	}

	botID, err := strconv.ParseInt(botToken, 10, 64)
	if err != nil || botID <= 0 {
		t.Fatalf("invalid bale bot token/id %q: must be positive integer", botToken)
	}

	rawPhones := strings.Split(phonesRaw, ",")
	phones := make([]string, 0, len(rawPhones))
	for _, p := range rawPhones {
		p = strings.TrimSpace(p)
		if p != "" {
			phones = append(phones, p)
		}
	}
	if len(phones) == 0 || len(phones) > 2 {
		t.Fatalf("provide 1 or 2 phone numbers, got %d", len(phones))
	}

	cfg := config.BaleConfig{
		APIAccessKey: accessToken,
		Provider: firstNonEmpty(
			strings.TrimSpace(*itBaleProvider),
			strings.TrimSpace(os.Getenv("BALE_PROVIDER")),
			baleProviderNajvaV2,
		),
		NajvaDomain: firstNonEmpty(
			strings.TrimSpace(*itBaleNajvaDomain),
			strings.TrimSpace(os.Getenv("BALE_NAJVA_DOMAIN")),
		),
		LegacyDomain: firstNonEmpty(
			strings.TrimSpace(*itBaleLegacyDomain),
			strings.TrimSpace(os.Getenv("BALE_LEGACY_DOMAIN")),
		),
	}

	return cfg, botID, phones
}

func writeTinyPNG(t *testing.T) string {
	t.Helper()

	// 1x1 PNG file.
	const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jX1cAAAAASUVORK5CYII="
	content, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}

	path := filepath.Join(t.TempDir(), "it_upload.png")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write tiny png: %v", err)
	}
	return path
}

func printJSON(t *testing.T, title string, payload any) {
	t.Helper()

	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Logf("%s (marshal failed): %+v", title, payload)
		return
	}
	t.Logf("%s:\n%s", title, string(out))
}
