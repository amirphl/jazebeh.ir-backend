package businessflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"gorm.io/gorm"
)

// BotShortLinkFlow handles creation of short links by bot
// Validates inputs and persists records in a transaction for batch
// UIDs are allocated centrally and must never be supplied by callers.
// CampaignID is optional (no FK).
type BotShortLinkFlow interface {
	GenerateAndCreateShortLinks(ctx context.Context, req *dto.BotAllocateShortLinksRequest) ([]string, error)
}

type BotShortLinkFlowImpl struct {
	shortRepo repository.ShortLinkRepository
	db        *gorm.DB
	publisher ShortLinkMappingPublisher
}

func NewBotShortLinkFlow(shortRepo repository.ShortLinkRepository, db *gorm.DB, publishers ...ShortLinkMappingPublisher) BotShortLinkFlow {
	var publisher ShortLinkMappingPublisher
	if len(publishers) > 0 {
		publisher = publishers[0]
	}
	return &BotShortLinkFlowImpl{shortRepo: shortRepo, db: db, publisher: publisher}
}

// GenerateAndCreateShortLinks generates sequential UIDs centrally and creates short links for phones.
// Each item carries its own ad link. Returns codes in the same order as req.Items.
func (s *BotShortLinkFlowImpl) GenerateAndCreateShortLinks(ctx context.Context, req *dto.BotAllocateShortLinksRequest) ([]string, error) {
	if req == nil {
		return nil, NewBusinessError("VALIDATION_ERROR", "request is required", nil)
	}
	if len(req.Items) == 0 {
		return []string{}, nil
	}
	shortLinkDomain := normalizeDomain(req.ShortLinkDomain)
	if shortLinkDomain == "" {
		return nil, NewBusinessError("VALIDATION_ERROR", "short_link_domain is required", nil)
	}
	allocationKey := shortLinkAllocationKey(req, shortLinkDomain)

	var (
		codes []string
		rows  []*models.ShortLink
	)
	if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		existing, err := s.shortRepo.ByAllocationKey(txCtx, allocationKey)
		if err != nil {
			return NewBusinessError("FETCH_SHORT_LINK_ALLOCATION_FAILED", "Failed to load existing short-link allocation", err)
		}
		if len(existing) > 0 {
			codes, err = shortLinkAllocationCodes(existing, len(req.Items))
			if err != nil {
				return NewBusinessError("SHORT_LINK_ALLOCATION_CORRUPT", "Existing short-link allocation is invalid", err)
			}
			rows = existing
			return nil
		}

		codes, err = s.shortRepo.ReserveSequentialUIDs(txCtx, len(req.Items))
		if err != nil {
			return NewBusinessError("UID_SEQUENCE_EXHAUSTED", "No more fixed-width short-link UIDs are available", err)
		}
		newScenarioID, err := s.shortRepo.NextScenarioID(txCtx)
		if err != nil {
			return NewBusinessError("FETCH_SCENARIO_ID_FAILED", "Failed to determine next scenario id", err)
		}
		rows = make([]*models.ShortLink, 0, len(req.Items))
		campaignID := req.CampaignID
		for index, item := range req.Items {
			longLink := ""
			if item.AdLink != nil {
				longLink = *item.AdLink
			}
			phone := item.Phone
			position := index
			rows = append(rows, &models.ShortLink{
				UID:                codes[index],
				CampaignID:         &campaignID,
				ScenarioID:         &newScenarioID,
				PhoneNumber:        &phone,
				LongLink:           longLink,
				ShortLink:          fmt.Sprintf("%s/%s", shortLinkDomain, codes[index]),
				AllocationKey:      &allocationKey,
				AllocationPosition: &position,
			})
		}
		return s.shortRepo.SaveBatch(txCtx, rows)
	}); err != nil {
		// A replica can observe no allocation before it waits on the allocator
		// row, then lose the allocation-key race. Its whole transaction rolls
		// back, including its UID reservation; reuse the winner's rows instead.
		existing, lookupErr := s.shortRepo.ByAllocationKey(ctx, allocationKey)
		if lookupErr == nil && len(existing) > 0 {
			if reusedCodes, reuseErr := shortLinkAllocationCodes(existing, len(req.Items)); reuseErr == nil {
				codes, rows = reusedCodes, existing
			} else {
				return nil, NewBusinessError("SHORT_LINK_ALLOCATION_CORRUPT", "Existing short-link allocation is invalid", reuseErr)
			}
		} else {
			var businessErr *BusinessError
			if errors.As(err, &businessErr) {
				return nil, businessErr
			}
			return nil, NewBusinessError("BOT_CREATE_SHORT_LINKS_FAILED", "Failed to create short links", err)
		}
	}
	if err := publishShortLinkMappings(ctx, s.shortRepo, s.publisher, rows); err != nil {
		// No code is returned to the campaign scheduler until every mapping is
		// durably acknowledged by the external redirect service.
		return nil, NewBusinessError("EXTERNAL_SHORT_LINK_UPLOAD_FAILED", "External short-link mappings were not acknowledged", err)
	}

	return codes, nil
}

func shortLinkAllocationCodes(rows []*models.ShortLink, expected int) ([]string, error) {
	if len(rows) != expected {
		return nil, fmt.Errorf("expected %d rows, found %d", expected, len(rows))
	}
	codes := make([]string, len(rows))
	for position, row := range rows {
		if row == nil || row.AllocationPosition == nil || *row.AllocationPosition != position || row.UID == "" {
			return nil, fmt.Errorf("invalid allocation row at position %d", position)
		}
		codes[position] = row.UID
	}
	return codes, nil
}

// shortLinkAllocationKey is deterministic for a complete allocation request.
// Length-delimited fields make different item boundaries unambiguous, including
// duplicate phone numbers and nil versus empty destination links.
func shortLinkAllocationKey(req *dto.BotAllocateShortLinksRequest, normalizedDomain string) string {
	hash := sha256.New()
	writeField := func(value string) {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{'|'})
	}
	if req == nil {
		writeField("nil")
		return hex.EncodeToString(hash.Sum(nil))
	}
	writeField(strconv.FormatUint(uint64(req.CampaignID), 10))
	writeField(normalizedDomain)
	writeField(strconv.Itoa(len(req.Items)))
	for _, item := range req.Items {
		writeField(item.Phone)
		if item.AdLink == nil {
			writeField("nil")
			continue
		}
		writeField("value")
		writeField(*item.AdLink)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
