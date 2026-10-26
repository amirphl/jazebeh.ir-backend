package businessflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"gorm.io/gorm"
)

// BotShortLinkFlow handles creation of short links by bot
// Validates inputs and persists records in a transaction for batch
// UID must be unique and provided by the caller
// CampaignID is optional (no FK)
type BotShortLinkFlow interface {
	CreateShortLink(ctx context.Context, req *dto.BotCreateShortLinkRequest) (*dto.BotCreateShortLinkResponse, error)
	CreateShortLinks(ctx context.Context, req *dto.BotCreateShortLinksRequest) (*dto.BotCreateShortLinksResponse, error)
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

func (s *BotShortLinkFlowImpl) CreateShortLink(ctx context.Context, req *dto.BotCreateShortLinkRequest) (*dto.BotCreateShortLinkResponse, error) {
	if req == nil {
		return nil, NewBusinessError("VALIDATION_ERROR", "Request body is required", nil)
	}
	if req.UID == "" || req.LongLink == "" || req.ShortLink == "" {
		return nil, NewBusinessError("VALIDATION_ERROR", "uid, long_link and short_link are required", nil)
	}

	lockShortLinkGen()
	defer unlockShortLinkGen()

	// read last scenario id from last short link from database and increment it
	lastScenarioID, err := s.shortRepo.GetLastScenarioID(ctx)
	if err != nil {
		return nil, NewBusinessError("FETCH_SCENARIO_ID_FAILED", "Failed to determine next scenario id", err)
	}
	newScenarioID := lastScenarioID + 1

	row := &models.ShortLink{
		UID:         req.UID,
		CampaignID:  req.CampaignID,
		ClientID:    req.ClientID,
		ScenarioID:  &newScenarioID,
		PhoneNumber: req.PhoneNumber,
		LongLink:    req.LongLink,
		ShortLink:   canonicalShortLinkURL(req.ShortLink),
	}
	if err := s.shortRepo.Save(ctx, row); err != nil {
		return nil, NewBusinessError("BOT_CREATE_SHORT_LINK_FAILED", "Failed to create short link", err)
	}
	if err := publishShortLinkMappings(ctx, s.shortRepo, s.publisher, []*models.ShortLink{row}); err != nil {
		return nil, NewBusinessError("EXTERNAL_SHORT_LINK_UPLOAD_FAILED", "External short-link mapping was not acknowledged", err)
	}
	return &dto.BotCreateShortLinkResponse{
		Message: "Short link created",
		Item:    mapShortLinkDTO(row),
	}, nil
}

func (s *BotShortLinkFlowImpl) CreateShortLinks(ctx context.Context, req *dto.BotCreateShortLinksRequest) (*dto.BotCreateShortLinksResponse, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, NewBusinessError("VALIDATION_ERROR", "items must contain at least one element", nil)
	}

	lockShortLinkGen()
	defer unlockShortLinkGen()

	// read last scenario id from last short link from database and increment it
	lastScenarioID, err := s.shortRepo.GetLastScenarioID(ctx)
	if err != nil {
		return nil, NewBusinessError("FETCH_SCENARIO_ID_FAILED", "Failed to determine next scenario id", err)
	}
	newScenarioID := lastScenarioID + 1

	rows := make([]*models.ShortLink, 0, len(req.Items))
	for _, it := range req.Items {
		if it.UID == "" || it.LongLink == "" || it.ShortLink == "" {
			return nil, NewBusinessError("VALIDATION_ERROR", "uid, long_link and short_link are required for all items", nil)
		}
		rows = append(rows, &models.ShortLink{
			UID:         it.UID,
			CampaignID:  it.CampaignID,
			ClientID:    it.ClientID,
			ScenarioID:  &newScenarioID,
			PhoneNumber: it.PhoneNumber,
			LongLink:    it.LongLink,
			ShortLink:   canonicalShortLinkURL(it.ShortLink),
		})
	}

	// Persist in a single transaction for consistency
	if err := repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		return s.shortRepo.SaveBatch(txCtx, rows)
	}); err != nil {
		return nil, NewBusinessError("BOT_CREATE_SHORT_LINKS_FAILED", "Failed to create short links", err)
	}
	if err := publishShortLinkMappings(ctx, s.shortRepo, s.publisher, rows); err != nil {
		return nil, NewBusinessError("EXTERNAL_SHORT_LINK_UPLOAD_FAILED", "External short-link mappings were not acknowledged", err)
	}

	out := make([]dto.ShortLinkDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapShortLinkDTO(r))
	}
	return &dto.BotCreateShortLinksResponse{
		Message: "Short links created",
		Items:   out,
	}, nil
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

	// Only UID/scenario reservation and the database insert need the process
	// mutex. Holding it during a potentially long external mapping upload
	// serializes every short-link operation behind this request.
	lockShortLinkGen()
	var (
		codes []string
		rows  []*models.ShortLink
	)
	operationErr := func() error {
		defer unlockShortLinkGen()
		existing, existingErr := s.shortRepo.ByAllocationKey(ctx, allocationKey)
		if existingErr != nil {
			return NewBusinessError("FETCH_SHORT_LINK_ALLOCATION_FAILED", "Failed to load existing short-link allocation", existingErr)
		}
		if len(existing) > 0 {
			codes, existingErr = shortLinkAllocationCodes(existing, len(req.Items))
			if existingErr != nil {
				return NewBusinessError("SHORT_LINK_ALLOCATION_CORRUPT", "Existing short-link allocation is invalid", existingErr)
			}
			rows = existing
			return nil
		}

		cutoff := time.Date(2025, 11, 10, 15, 45, 11, 401492000, time.UTC)
		lastUID, err := s.shortRepo.GetMaxUIDSince(ctx, cutoff)
		if err != nil {
			return NewBusinessError("FETCH_MAX_UID_FAILED", "Failed to determine highest uid since cutoff", err)
		}
		var seq uint64
		if lastUID != "" {
			n, decodeErr := decodeBase36Compat(lastUID)
			if decodeErr != nil {
				return NewBusinessError("INVALID_EXISTING_UID", "Found invalid uid in database", decodeErr)
			}
			seq = n + 1
		}

		lastScenarioID, scenarioErr := s.shortRepo.GetLastScenarioID(ctx)
		if scenarioErr != nil {
			return NewBusinessError("FETCH_SCENARIO_ID_FAILED", "Failed to determine next scenario id", scenarioErr)
		}
		newScenarioID := lastScenarioID + 1

		codes = make([]string, len(req.Items))
		rows = make([]*models.ShortLink, 0, len(req.Items))
		campaignID := req.CampaignID
		for i, item := range req.Items {
			uid, formatErr := formatSequentialUIDCompat(seq)
			if formatErr != nil {
				return NewBusinessError("UID_SEQUENCE_EXHAUSTED", "No more UIDs available up to zzzzz", formatErr)
			}
			seq++
			codes[i] = uid
			longLink := ""
			if item.AdLink != nil {
				longLink = *item.AdLink
			}
			phone := item.Phone
			position := i
			rows = append(rows, &models.ShortLink{
				UID:                uid,
				CampaignID:         &campaignID,
				ScenarioID:         &newScenarioID,
				PhoneNumber:        &phone,
				LongLink:           longLink,
				ShortLink:          fmt.Sprintf("%s/%s", shortLinkDomain, uid),
				AllocationKey:      &allocationKey,
				AllocationPosition: &position,
			})
		}
		if saveErr := s.shortRepo.SaveBatch(ctx, rows); saveErr != nil {
			// A different application replica can win the unique allocation-key
			// race. Reuse its complete committed allocation rather than exposing
			// an ambiguous failure to the scheduler.
			existing, lookupErr := s.shortRepo.ByAllocationKey(ctx, allocationKey)
			if lookupErr == nil && len(existing) > 0 {
				if reusedCodes, reuseErr := shortLinkAllocationCodes(existing, len(req.Items)); reuseErr == nil {
					codes, rows = reusedCodes, existing
					return nil
				}
			}
			return NewBusinessError("BOT_CREATE_SHORT_LINKS_FAILED", "Failed to create short links", saveErr)
		}
		return nil
	}()
	if operationErr != nil {
		return nil, operationErr
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

// duplicate minimal helpers (kept private to this file) for base36 sequential IDs
func decodeBase36Compat(s string) (uint64, error) {
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			v = int(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid base36 character: %q", c)
		}
		if v >= 36 {
			return 0, fmt.Errorf("invalid base36 value: %d", v)
		}
		n = n*36 + uint64(v)
	}
	return n, nil
}

func formatSequentialUIDCompat(seq uint64) (string, error) {
	s := encodeBase36Compat(seq)
	if len(s) < 4 {
		s = strings.Repeat("0", 4-len(s)) + s
	}
	if len(s) > 5 {
		return "", fmt.Errorf("sequence exhausted at %s", s)
	}
	return s, nil
}

func encodeBase36Compat(n uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 16)
	for n > 0 {
		r := n % 36
		buf = append(buf, digits[r])
		n /= 36
	}
	// reverse in place
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func mapShortLinkDTO(m *models.ShortLink) dto.ShortLinkDTO {
	return dto.ShortLinkDTO{
		ID:          m.ID,
		UID:         m.UID,
		CampaignID:  m.CampaignID,
		ClientID:    m.ClientID,
		PhoneNumber: m.PhoneNumber,
		LongLink:    m.LongLink,
		ShortLink:   m.ShortLink,
	}
}
