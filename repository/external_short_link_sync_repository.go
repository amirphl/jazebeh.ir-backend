package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExternalShortLinkSyncRepositoryImpl struct {
	db *gorm.DB
}

func NewExternalShortLinkSyncRepository(db *gorm.DB) ExternalShortLinkSyncRepository {
	return &ExternalShortLinkSyncRepositoryImpl{db: db}
}

func (r *ExternalShortLinkSyncRepositoryImpl) Cursor(ctx context.Context, source string) (int64, error) {
	var state models.ExternalShortLinkSyncState
	err := r.db.WithContext(ctx).Where("source = ?", source).Take(&state).Error
	if err == nil {
		return state.LastClickID, nil
	}
	if err != gorm.ErrRecordNotFound {
		return 0, err
	}
	state = models.ExternalShortLinkSyncState{Source: source, LastClickID: 0, UpdatedAt: time.Now().UTC()}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return 0, err
	}
	return 0, nil
}

func (r *ExternalShortLinkSyncRepositoryImpl) ImportPage(
	ctx context.Context,
	source string,
	clicks []models.ExternalShortLinkClick,
	throughClickID int64,
) error {
	if source == "" {
		return fmt.Errorf("external click source cannot be empty")
	}
	if throughClickID < 0 {
		return fmt.Errorf("external click cursor cannot be negative")
	}
	if len(clicks) == 0 {
		if throughClickID != 0 {
			return fmt.Errorf("cannot advance external click cursor with an empty page")
		}
	}
	return WithTransaction(ctx, r.db, func(txCtx context.Context) error {
		tx := r.db.WithContext(txCtx)
		if current, ok := txCtx.Value(TxContextKey).(*gorm.DB); ok && current != nil {
			tx = current.WithContext(txCtx)
		}

		// Every row is accounted for by the end of this loop: either imported or
		// quarantined with its payload. A row the ordered cursor cannot carry (a
		// repeated, lowered, or unusable ID) is quarantined instead of failing
		// the page, because failing the page stops the cursor and therefore all
		// later clicks - the stall this quarantine exists to prevent.
		var (
			highest     int64
			valid       = make([]models.ExternalShortLinkClick, 0, len(clicks))
			deadLetters = make([]*models.ExternalShortLinkClickDeadLetter, 0)
		)
		for _, click := range clicks {
			if reason := anomalousExternalClickReason(click, highest); reason != "" {
				deadLetters = append(deadLetters, externalClickDeadLetter(source, click, reason))
				continue
			}
			highest = click.ClickID
			if reason := invalidExternalClickReason(click); reason != "" {
				deadLetters = append(deadLetters, externalClickDeadLetter(source, click, reason))
				continue
			}
			valid = append(valid, click)
		}

		codes := make([]string, 0, len(valid))
		seen := make(map[string]struct{}, len(clicks))
		for _, click := range valid {
			if _, exists := seen[click.ShortCode]; !exists {
				seen[click.ShortCode] = struct{}{}
				codes = append(codes, click.ShortCode)
			}
		}

		linksByCode := make(map[string]models.ShortLink, len(codes))
		const lookupChunkSize = 5000
		for start := 0; start < len(codes); start += lookupChunkSize {
			end := min(start+lookupChunkSize, len(codes))
			var links []models.ShortLink
			if err := tx.Where("uid IN ?", codes[start:end]).Find(&links).Error; err != nil {
				return fmt.Errorf("lookup production short links: %w", err)
			}
			for _, link := range links {
				linksByCode[link.UID] = link
			}
		}

		rows := make([]*models.ShortLinkClick, 0, len(valid))
		for _, external := range valid {
			link, exists := linksByCode[external.ShortCode]
			if !exists {
				deadLetters = append(deadLetters, externalClickDeadLetter(source, external, "production short-link code does not exist"))
				continue
			}
			expectedLongURL, err := externalStoredLongURL(link.LongLink)
			if err != nil || expectedLongURL != external.LongURL {
				deadLetters = append(deadLetters, externalClickDeadLetter(source, external, "destination does not match immutable production mapping"))
				continue
			}
			if external.SourceLinkID != nil && (*external.SourceLinkID <= 0 || uint64(*external.SourceLinkID) != uint64(link.ID)) {
				deadLetters = append(deadLetters, externalClickDeadLetter(source, external, "source link ID does not match production"))
				continue
			}
			uid := external.ShortCode
			longURL := link.LongLink
			shortURL := link.ShortLink
			linkCreatedAt := link.CreatedAt
			linkUpdatedAt := link.UpdatedAt
			rows = append(rows, &models.ShortLinkClick{
				ShortLinkID:        link.ID,
				UID:                &uid,
				CampaignID:         link.CampaignID,
				ClientID:           link.ClientID,
				ScenarioID:         link.ScenarioID,
				ScenarioName:       link.ScenarioName,
				PhoneNumber:        link.PhoneNumber,
				LongLink:           &longURL,
				ShortLink:          &shortURL,
				IsTest:             link.IsTest,
				ShortLinkCreatedAt: &linkCreatedAt,
				ShortLinkUpdatedAt: &linkUpdatedAt,
				UserAgent:          external.UserAgent,
				IP:                 external.ClientIP,
				Referer:            external.Referer,
				Source:             source,
				ExternalClickID:    &external.ClickID,
				CreatedAt:          external.ClickedAt.UTC(),
			})
		}

		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "source"}, {Name: "external_click_id"}},
				DoNothing: true,
			}).CreateInBatches(rows, 500).Error; err != nil {
				return fmt.Errorf("insert external clicks: %w", err)
			}
		}
		if len(deadLetters) > 0 {
			// A bare DO NOTHING satisfies the partial unique index on positive
			// IDs while still retaining rows whose unusable ID has nothing to
			// conflict with, so a malformed page cannot fail this insert.
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(deadLetters, 500).Error; err != nil {
				return fmt.Errorf("quarantine invalid external clicks: %w", err)
			}
			// This is deliberately payload-free: it is alertable without leaking
			// phone numbers, destinations, IPs, or user-agent data into logs.
			log.Printf("external short-link click import: quarantined invalid events source=%s count=%d reasons=%s", source, len(deadLetters), deadLetterReasonSummary(deadLetters))
		}

		// The cursor records what this transaction actually accounted for. The
		// caller's throughClickID is only a floor: recording it directly could
		// skip rows a malformed page never presented. GREATEST below keeps the
		// stored cursor monotonic even if the caller proposes a lower value.
		if throughClickID > highest {
			log.Printf(
				"external short-link click import: proposed cursor %d exceeds the highest accounted click id %d; recording %d",
				throughClickID,
				highest,
				highest,
			)
		}
		state := models.ExternalShortLinkSyncState{
			Source:      source,
			LastClickID: highest,
			UpdatedAt:   time.Now().UTC(),
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "source"}},
			DoUpdates: clause.Assignments(map[string]any{
				"last_click_id": gorm.Expr("GREATEST(external_short_link_sync_state.last_click_id, EXCLUDED.last_click_id)"),
				"updated_at":    state.UpdatedAt,
			}),
		}).Create(&state).Error; err != nil {
			return fmt.Errorf("advance external click cursor: %w", err)
		}
		return nil
	})
}

func deadLetterReasonSummary(deadLetters []*models.ExternalShortLinkClickDeadLetter) string {
	counts := make(map[string]int, len(deadLetters))
	for _, deadLetter := range deadLetters {
		counts[deadLetter.Reason]++
	}
	parts := make([]string, 0, len(counts))
	for reason, count := range counts {
		parts = append(parts, fmt.Sprintf("%q:%d", reason, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// anomalousExternalClickReason reports why a click cannot participate in the
// ordered import cursor. It inspects IDs only, so a row with a usable ID still
// advances the cursor after its payload is quarantined separately.
func anomalousExternalClickReason(click models.ExternalShortLinkClick, highestAccounted int64) string {
	if click.ClickID <= 0 {
		return "external click ID is not a positive integer"
	}
	if click.ClickID <= highestAccounted {
		return "external click ID is not strictly increasing"
	}
	return ""
}

// invalidExternalClickReason reports why an ordered click cannot be imported.
func invalidExternalClickReason(click models.ExternalShortLinkClick) string {
	if click.LinkID <= 0 || strings.TrimSpace(click.ShortCode) == "" || strings.TrimSpace(click.LongURL) == "" || click.ClickedAt.IsZero() {
		return "missing required click fields"
	}
	return ""
}

func externalClickDeadLetter(source string, click models.ExternalShortLinkClick, reason string) *models.ExternalShortLinkClickDeadLetter {
	payload, err := json.Marshal(click)
	if err != nil {
		payload = []byte(`{"serialization_error":true}`)
	}
	return &models.ExternalShortLinkClickDeadLetter{
		Source: source, ExternalClickID: click.ClickID, Payload: string(payload), Reason: reason, CreatedAt: time.Now().UTC(),
	}
}

// externalStoredLongURL derives the external service's persisted destination
// for comparison only. It never changes Yamata's stored long link.
func externalStoredLongURL(longLink string) (string, error) {
	parsed, err := url.Parse(longLink)
	if err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Hostname() != "" {
		return longLink, nil
	}
	if err == nil && parsed.Scheme != "" {
		return "", fmt.Errorf("long link must use http or https")
	}
	if strings.HasPrefix(longLink, "//") {
		longLink = "https:" + longLink
	} else {
		longLink = "https://" + longLink
	}
	parsed, err = url.Parse(longLink)
	if err != nil || !(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) || parsed.Hostname() == "" {
		return "", fmt.Errorf("long link cannot be converted to an absolute http or https URL")
	}
	return longLink, nil
}
