package businessflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	smartTargetingCapacityAlgorithmVersion = 3
	smartTargetingCapacityTTL              = 24 * time.Hour
)

var (
	ErrSmartTargetingExactCapacityRequired = errors.New("a current exact Smart Targeting capacity calculation is required")
	ErrSmartTargetingCapacityPending       = errors.New("exact Smart Targeting capacity calculation is pending; please wait and retry")
	ErrSmartTargetingCapacityBusy          = errors.New("an exact Smart Targeting capacity calculation is already running")
	ErrSmartTargetingScoreClassesInvalid   = errors.New("audience score classes are invalid")
)

// SmartTargetingCapacityFlow owns exact-capacity requests, polling and worker
// execution. Its records are append-oriented calculation generations; a
// previously calculated generation becomes stale by derivation when its input
// hash, allocation fingerprint, or expiry no longer matches.
type SmartTargetingCapacityFlow interface {
	Start(ctx context.Context, req *dto.StartSmartTargetingCapacityCalculationRequest, metadata *ClientMetadata) (*dto.SmartTargetingCapacityCalculationResponse, error)
	GetCurrent(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingCapacityCalculationResponse, error)
	GetByID(ctx context.Context, customerID uint, campaignUUID string, calculationID int64) (*dto.SmartTargetingCapacityCalculationResponse, error)
	ExecuteCampaignTargetingCapacityCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) error
}

type SmartTargetingCapacityFlowImpl struct {
	campaignRepo    repository.CampaignRepository
	selectionRepo   repository.CampaignSelectedTagRepository
	calculationRepo repository.CampaignTargetingCapacityRepository
	db              *gorm.DB
}

// SmartTargetingCapacityConflictError keeps duplicate starts idempotent while
// letting HTTP return the active calculation rather than an opaque conflict.
type SmartTargetingCapacityConflictError struct {
	Response *dto.SmartTargetingCapacityCalculationResponse
}

type SmartTargetingCapacityPendingError struct {
	Response *dto.SmartTargetingCapacityCalculationResponse
}

func (e *SmartTargetingCapacityPendingError) Error() string {
	return ErrSmartTargetingCapacityPending.Error()
}
func (e *SmartTargetingCapacityPendingError) Unwrap() error { return ErrSmartTargetingCapacityPending }

func (e *SmartTargetingCapacityConflictError) Error() string {
	return ErrSmartTargetingCapacityBusy.Error()
}

func NewSmartTargetingCapacityFlow(
	campaignRepo repository.CampaignRepository,
	selectionRepo repository.CampaignSelectedTagRepository,
	calculationRepo repository.CampaignTargetingCapacityRepository,
	db *gorm.DB,
) SmartTargetingCapacityFlow {
	return &SmartTargetingCapacityFlowImpl{
		campaignRepo: campaignRepo, selectionRepo: selectionRepo, calculationRepo: calculationRepo, db: db,
	}
}

// smartTargetingDB returns the transaction carried by repository.WithTransaction
// when one exists. Calling db.WithContext(ctx) directly does not recover that
// transaction and would make locks and capacity-result writes autocommit.
func smartTargetingDB(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(repository.TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}

func canCalculateSmartTargetingCapacity(campaign *models.Campaign) bool {
	if campaign == nil {
		return false
	}
	switch campaign.Status {
	case models.CampaignStatusInitiated,
		models.CampaignStatusInProgress,
		models.CampaignStatusWaitingForApproval,
		models.CampaignStatusApproved:
		return true
	default:
		return false
	}
}

// calculationExpiry keeps the count generation valid through scheduled
// execution while retaining a bounded grace period for retries and status work.
func calculationExpiry(now time.Time, campaign *models.Campaign) time.Time {
	expires := now.Add(smartTargetingCapacityTTL)
	if campaign != nil && campaign.Spec.ScheduleAt != nil {
		scheduledExpiry := campaign.Spec.ScheduleAt.UTC().Add(smartTargetingCapacityTTL)
		if scheduledExpiry.After(expires) {
			expires = scheduledExpiry
		}
	}
	return expires
}

func normalizeSmartTargetingScoreClasses(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{"A", "B", "C"}, nil
	}
	seen := make(map[string]struct{}, len(input))
	for _, raw := range input {
		class := strings.ToUpper(strings.TrimSpace(raw))
		if class != "A" && class != "B" && class != "C" {
			return nil, fmt.Errorf("%w: invalid audience score class %q", ErrSmartTargetingScoreClassesInvalid, raw)
		}
		if _, exists := seen[class]; exists {
			return nil, fmt.Errorf("%w: duplicate audience score class %q", ErrSmartTargetingScoreClassesInvalid, class)
		}
		seen[class] = struct{}{}
	}
	classes := make([]string, 0, len(seen))
	for _, class := range []string{"A", "B", "C"} {
		if _, ok := seen[class]; ok {
			classes = append(classes, class)
		}
	}
	return classes, nil
}

func sameSmartTargetingScoreClasses(left, right []string) bool {
	return slices.Equal(left, right)
}

func smartTargetingTagHash(campaignID uint, ids []uint) string {
	canonicalIDs := append([]uint(nil), ids...)
	sort.Slice(canonicalIDs, func(i, j int) bool { return canonicalIDs[i] < canonicalIDs[j] })
	parts := make([]string, len(canonicalIDs))
	for i, id := range canonicalIDs {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return hashSmartTargetingCapacityString("v1|campaign=" + strconv.FormatUint(uint64(campaignID), 10) + "|tags=" + strings.Join(parts, ","))
}

func smartTargetingCapacityAppliesBundleExclusions(campaign *models.Campaign) bool {
	return campaign != nil && campaign.Spec.UsesSmartTargeting() && campaign.Phase == models.CampaignPhaseTest
}

func smartTargetingInputHash(tagHash string, classes []string, platform string, applyBundleAudienceExclusions bool) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	return hashSmartTargetingCapacityString(
		"v3|algorithm=" + strconv.Itoa(smartTargetingCapacityAlgorithmVersion) +
			"|tags=" + tagHash +
			"|classes=" + strings.Join(classes, ",") +
			"|platform=" + platform +
			"|bundle_exclusions=" + strconv.FormatBool(applyBundleAudienceExclusions),
	)
}

func hashSmartTargetingCapacityString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// smartTargetingScoreClass defines the deterministic boundary convention used
// by the exact-count and execution queries: C owns the lower boundary, B owns values
// above p33 through p66, and A owns values above p66. A nil score never enters
// a restricted class; it is retained only when the caller selected all classes.
func smartTargetingScoreClass(score, p33, p66 *float64) string {
	if score == nil || p33 == nil || p66 == nil {
		return "unscored"
	}
	if *score <= *p33 {
		return "C"
	}
	if *score <= *p66 {
		return "B"
	}
	return "A"
}

func (f *SmartTargetingCapacityFlowImpl) ownedSmartCampaign(ctx context.Context, customerID uint, campaignUUID string) (*models.Campaign, error) {
	if customerID == 0 {
		return nil, NewBusinessError("MISSING_CUSTOMER_ID", "Customer ID is required", ErrCustomerNotFound)
	}
	campaignUUID = strings.TrimSpace(campaignUUID)
	if _, err := uuid.Parse(campaignUUID); err != nil {
		return nil, NewBusinessError("INVALID_CAMPAIGN_UUID", "Campaign UUID is invalid", ErrCampaignUUIDInvalid)
	}
	campaign, err := f.campaignRepo.ByUUID(ctx, campaignUUID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	if campaign == nil {
		return nil, NewBusinessError("CAMPAIGN_NOT_FOUND", "Campaign not found", ErrCampaignNotFound)
	}
	if campaign.CustomerID != customerID {
		return nil, NewBusinessError("CAMPAIGN_ACCESS_DENIED", "Campaign access denied", ErrCampaignAccessDenied)
	}
	if campaign.BundleID == nil || *campaign.BundleID == 0 {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Campaign bundle not found", ErrBundleNotFound)
	}
	if !campaign.Spec.UsesSmartTargeting() {
		return nil, NewBusinessError("SMART_TARGETING_NOT_ACTIVE", "Smart Targeting is not active for this campaign", ErrInvalidState)
	}
	return campaign, nil
}

func selectedSmartTagIDs(ctx context.Context, selectionRepo repository.CampaignSelectedTagRepository, campaignID uint) ([]uint, error) {
	selected, err := selectionRepo.ListSelected(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(selected))
	for _, row := range selected {
		if row != nil && row.TagID != 0 {
			ids = append(ids, row.TagID)
		}
	}
	if len(ids) == 0 {
		return nil, ErrSmartTargetingTagsRequired
	}
	return ids, nil
}

func tagIDsToInt64(ids []uint) pq.Int64Array {
	result := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		result[i] = int64(id)
	}
	return result
}

// Exact-capacity snapshots describe a tag set, so their array representation
// is canonical even though campaign selection and sampling retain user order.
func canonicalSmartTargetingCapacityTagIDs(ids []uint) pq.Int64Array {
	result := tagIDsToInt64(ids)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (f *SmartTargetingCapacityFlowImpl) Start(ctx context.Context, req *dto.StartSmartTargetingCapacityCalculationRequest, metadata *ClientMetadata) (*dto.SmartTargetingCapacityCalculationResponse, error) {
	if req == nil {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_REQUEST_INVALID", "Exact capacity request is invalid", ErrInvalidState)
	}
	campaign, err := f.ownedSmartCampaign(ctx, req.CustomerID, req.CampaignUUID)
	if err != nil {
		return nil, err
	}
	classes, err := normalizeSmartTargetingScoreClasses(req.ScoreClasses)
	if len(req.ScoreClasses) == 0 {
		classes, err = normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
	}
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_SCORE_CLASSES_INVALID", "Audience score classes are invalid", err)
	}
	if !canCalculateSmartTargetingCapacity(campaign) {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_NOT_ALLOWED", "Exact capacity calculation is not allowed in the current campaign state", ErrInvalidState)
	}

	var calculation *models.CampaignTargetingCapacityCalculation
	var reused bool
	err = repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		// Both selection replacement and capacity request lock this campaign row.
		// This prevents a request snapshot from combining two selection versions.
		var lockedCampaign models.Campaign
		txDB := smartTargetingDB(txCtx, f.db)
		if err := txDB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedCampaign, campaign.ID).Error; err != nil {
			return err
		}
		if !canCalculateSmartTargetingCapacity(&lockedCampaign) || !lockedCampaign.Spec.UsesSmartTargeting() || lockedCampaign.BundleID == nil || *lockedCampaign.BundleID == 0 {
			return ErrInvalidState
		}
		platform := strings.ToLower(strings.TrimSpace(lockedCampaign.Spec.Platform))
		if !models.IsValidCampaignPlatform(platform) {
			return ErrInvalidState
		}
		effectiveClasses := classes
		if len(req.ScoreClasses) == 0 {
			effectiveClasses, err = normalizeSmartTargetingScoreClasses(lockedCampaign.Spec.AudienceGrades)
			if err != nil {
				return err
			}
		}
		ids, err := selectedSmartTagIDs(txCtx, f.selectionRepo, lockedCampaign.ID)
		if err != nil {
			return err
		}
		if err := f.selectionRepo.Validate(txCtx, lockedCampaign.ID, *lockedCampaign.BundleID); err != nil {
			return err
		}
		if len(req.ScoreClasses) > 0 {
			configuredClasses, err := normalizeSmartTargetingScoreClasses(lockedCampaign.Spec.AudienceGrades)
			if err != nil {
				return err
			}
			if !sameSmartTargetingScoreClasses(configuredClasses, effectiveClasses) && !lockedCampaign.IsEditable() {
				return ErrInvalidState
			}
			if !sameSmartTargetingScoreClasses(configuredClasses, effectiveClasses) {
				// AudienceGrades is the established campaign-level score-class
				// source. Persisting the request here means later edits are visible
				// to polling, cost, approval, and scheduler validity checks.
				lockedCampaign.Spec.AudienceGrades = append([]string(nil), effectiveClasses...)
				clearCampaignSmartTargetingTestSamplingPreviewFields(&lockedCampaign)
				if err := f.campaignRepo.Update(txCtx, lockedCampaign); err != nil {
					return err
				}
			}
		}

		now := time.Now().UTC()
		tagHash := smartTargetingTagHash(lockedCampaign.ID, ids)
		applyBundleAudienceExclusions := smartTargetingCapacityAppliesBundleExclusions(&lockedCampaign)
		inputHash := smartTargetingInputHash(tagHash, effectiveClasses, platform, applyBundleAudienceExclusions)
		active, err := f.calculationRepo.ActiveByCampaignID(txCtx, lockedCampaign.ID)
		if err != nil {
			return err
		}
		if active != nil {
			if active.InputHash == inputHash && active.ExpiresAt != nil && active.ExpiresAt.After(now) {
				return &SmartTargetingCapacityConflictError{Response: capacityDTO(active, false, false)}
			}
			if err := f.calculationRepo.Supersede(txCtx, active.ID, "SMART_TARGETING_CAPACITY_SUPERSEDED", "A newer targeting configuration replaced this calculation", now); err != nil {
				if !errors.Is(err, repository.ErrCampaignTargetingCapacityStateConflict) {
					return err
				}
				remaining, reloadErr := f.calculationRepo.ActiveByCampaignID(txCtx, lockedCampaign.ID)
				if reloadErr != nil {
					return reloadErr
				}
				if remaining != nil {
					return &SmartTargetingCapacityConflictError{Response: capacityDTO(remaining, false, false)}
				}
			}
		}
		reusable, err := f.calculationRepo.LatestCalculatedByInput(txCtx, lockedCampaign.ID, inputHash)
		if err != nil {
			return err
		}
		if reusable != nil {
			current, err := isCurrentSmartTargetingCapacity(txCtx, f.db, f.selectionRepo, reusable, &lockedCampaign)
			if err != nil {
				return err
			}
			if current {
				calculation = reusable
				reused = true
				return nil
			}
		}
		calculation = &models.CampaignTargetingCapacityCalculation{
			CampaignID:                    lockedCampaign.ID,
			BundleID:                      *lockedCampaign.BundleID,
			CustomerID:                    lockedCampaign.CustomerID,
			Platform:                      platform,
			RequestedByCustomerID:         req.CustomerID,
			SelectedTagIDs:                canonicalSmartTargetingCapacityTagIDs(ids),
			SelectedTagsHash:              tagHash,
			InputHash:                     inputHash,
			SelectedScoreClasses:          pq.StringArray(effectiveClasses),
			SelectedTagCount:              len(ids),
			ApplyBundleAudienceExclusions: applyBundleAudienceExclusions,
			RawAudienceCount:              0,
			AllocationFingerprint:         emptySmartTargetingAllocationFingerprint(),
			Status:                        models.CampaignTargetingCapacityCalculating,
			CalculationVersion:            smartTargetingCapacityAlgorithmVersion,
			CreatedAt:                     now,
			ExpiresAt:                     ptrTime(calculationExpiry(now, &lockedCampaign)),
		}
		if err := f.calculationRepo.Save(txCtx, calculation); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if conflict, ok := err.(*SmartTargetingCapacityConflictError); ok {
			return conflict.Response, nil
		}
		if errors.Is(err, ErrSmartTargetingTagsRequired) {
			return nil, NewBusinessError("SMART_TARGETING_TAGS_REQUIRED", "Select at least one tag before calculating exact capacity", err)
		}
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", "The selected Smart Targeting tags are no longer valid", err)
		}
		if errors.Is(err, ErrInvalidState) {
			return nil, NewBusinessError("SMART_TARGETING_CAPACITY_NOT_ALLOWED", "Exact capacity calculation is not allowed for the requested campaign configuration", err)
		}
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_REQUEST_FAILED", "Failed to request exact capacity calculation", err)
	}
	_ = metadata
	return capacityDTO(calculation, reused, false), nil
}

func (f *SmartTargetingCapacityFlowImpl) GetCurrent(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingCapacityCalculationResponse, error) {
	campaign, err := f.ownedSmartCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	var calculation *models.CampaignTargetingCapacityCalculation
	ids, inputErr := selectedSmartTagIDs(ctx, f.selectionRepo, campaign.ID)
	if inputErr == nil {
		classes, classErr := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
		if classErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_SCORE_CLASSES_INVALID", "Audience score classes are invalid", classErr)
		}
		inputHash := smartTargetingInputHash(
			smartTargetingTagHash(campaign.ID, ids),
			classes,
			campaign.Spec.Platform,
			smartTargetingCapacityAppliesBundleExclusions(campaign),
		)
		calculated, calculatedErr := f.calculationRepo.LatestCalculatedByInput(ctx, campaign.ID, inputHash)
		if calculatedErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to load exact capacity calculation", calculatedErr)
		}
		if calculated != nil {
			current, currentErr := isCurrentSmartTargetingCapacity(ctx, f.db, f.selectionRepo, calculated, campaign)
			if currentErr != nil {
				return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to validate exact capacity calculation", currentErr)
			}
			if current {
				return capacityDTO(calculated, true, false), nil
			}
		}
		calculation, err = f.calculationRepo.LatestByInput(ctx, campaign.ID, inputHash)
	} else if !errors.Is(inputErr, ErrSmartTargetingTagsRequired) {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to load exact capacity inputs", inputErr)
	}
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to load exact capacity calculation", err)
	}
	if calculation != nil {
		return f.statusDTO(ctx, campaign, calculation)
	}

	// Keep an old generation visible as stale audit context, but never let it
	// masquerade as the pending/failed state for the campaign's current input.
	calculation, err = f.calculationRepo.LatestByCampaignID(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to load exact capacity calculation", err)
	}
	if calculation == nil {
		classes, err := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
		if err != nil {
			return nil, NewBusinessError("SMART_TARGETING_SCORE_CLASSES_INVALID", "Audience score classes are invalid", err)
		}
		return &dto.SmartTargetingCapacityCalculationResponse{
			CampaignID: campaign.ID, BundleID: *campaign.BundleID, Status: "not_calculated",
			SelectedScoreClasses: classes,
		}, nil
	}
	response := capacityDTO(calculation, false, true)
	response.Status = "stale"
	return response, nil
}

func (f *SmartTargetingCapacityFlowImpl) GetByID(ctx context.Context, customerID uint, campaignUUID string, calculationID int64) (*dto.SmartTargetingCapacityCalculationResponse, error) {
	campaign, err := f.ownedSmartCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	calculation, err := f.calculationRepo.ByID(ctx, calculationID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to load exact capacity calculation", err)
	}
	if calculation == nil || calculation.CampaignID != campaign.ID {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_NOT_FOUND", "Exact capacity calculation not found", ErrCampaignNotFound)
	}
	return f.statusDTO(ctx, campaign, calculation)
}

func (f *SmartTargetingCapacityFlowImpl) statusDTO(ctx context.Context, campaign *models.Campaign, calculation *models.CampaignTargetingCapacityCalculation) (*dto.SmartTargetingCapacityCalculationResponse, error) {
	if calculation != nil && calculation.Status == models.CampaignTargetingCapacityFailed {
		return capacityDTO(calculation, false, false), nil
	}
	if calculation != nil && calculation.ExpiresAt != nil && !calculation.ExpiresAt.After(time.Now().UTC()) {
		response := capacityDTO(calculation, false, true)
		response.Status = "expired"
		return response, nil
	}
	valid, err := isCurrentSmartTargetingCapacity(ctx, f.db, f.selectionRepo, calculation, campaign)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_CAPACITY_LOOKUP_FAILED", "Failed to validate exact capacity calculation", err)
	}
	return capacityDTO(calculation, valid, calculation.Status == models.CampaignTargetingCapacityCalculated && !valid), nil
}

// CurrentSmartTargetingCapacity is shared by campaign cost calculation. It
// intentionally rejects pending, failed, expired and fingerprint-stale rows.
func CurrentSmartTargetingCapacity(ctx context.Context, db *gorm.DB, selectionRepo repository.CampaignSelectedTagRepository, calculationRepo repository.CampaignTargetingCapacityRepository, campaign *models.Campaign) (*models.CampaignTargetingCapacityCalculation, error) {
	if campaign == nil || !campaign.Spec.UsesSmartTargeting() || campaign.BundleID == nil {
		return nil, ErrSmartTargetingExactCapacityRequired
	}
	ids, err := selectedSmartTagIDs(ctx, selectionRepo, campaign.ID)
	if err != nil {
		if errors.Is(err, ErrSmartTargetingTagsRequired) {
			return nil, ErrSmartTargetingExactCapacityRequired
		}
		return nil, err
	}
	classes, err := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
	if err != nil {
		return nil, err
	}
	inputHash := smartTargetingInputHash(
		smartTargetingTagHash(campaign.ID, ids),
		classes,
		campaign.Spec.Platform,
		smartTargetingCapacityAppliesBundleExclusions(campaign),
	)
	calculation, err := calculationRepo.LatestCalculatedByInput(ctx, campaign.ID, inputHash)
	if err != nil {
		return nil, err
	}
	valid, err := isCurrentSmartTargetingCapacity(ctx, db, selectionRepo, calculation, campaign)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrSmartTargetingExactCapacityRequired
	}
	return calculation, nil
}

// EnsureCurrentSmartTargetingCapacity is used by every operation that requires
// exact capacity. A missing, failed, expired, or stale generation is submitted
// idempotently and returned as a clear pending condition.
func EnsureCurrentSmartTargetingCapacity(
	ctx context.Context,
	db *gorm.DB,
	campaignRepo repository.CampaignRepository,
	selectionRepo repository.CampaignSelectedTagRepository,
	calculationRepo repository.CampaignTargetingCapacityRepository,
	campaign *models.Campaign,
) (*models.CampaignTargetingCapacityCalculation, error) {
	current, err := CurrentSmartTargetingCapacity(ctx, db, selectionRepo, calculationRepo, campaign)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, ErrSmartTargetingExactCapacityRequired) || campaign == nil {
		return nil, err
	}
	flow := NewSmartTargetingCapacityFlow(campaignRepo, selectionRepo, calculationRepo, db)
	response, startErr := flow.Start(ctx, &dto.StartSmartTargetingCapacityCalculationRequest{
		CustomerID: campaign.CustomerID, CampaignUUID: campaign.UUID.String(),
	}, nil)
	if startErr != nil {
		return nil, startErr
	}
	if response != nil && response.IsCurrent && response.Status == string(models.CampaignTargetingCapacityCalculated) {
		return CurrentSmartTargetingCapacity(ctx, db, selectionRepo, calculationRepo, campaign)
	}
	return nil, &SmartTargetingCapacityPendingError{Response: response}
}

func isCurrentSmartTargetingCapacity(ctx context.Context, db *gorm.DB, selectionRepo repository.CampaignSelectedTagRepository, calculation *models.CampaignTargetingCapacityCalculation, campaign *models.Campaign) (bool, error) {
	if calculation == nil || campaign == nil || calculation.CampaignID != campaign.ID || calculation.BundleID == 0 || campaign.BundleID == nil || calculation.BundleID != *campaign.BundleID {
		return false, nil
	}
	if calculation.Status != models.CampaignTargetingCapacityCalculated || calculation.ExpiresAt == nil || !calculation.ExpiresAt.After(time.Now().UTC()) {
		return false, nil
	}
	if campaign.Spec.ScheduleAt != nil && !calculation.ExpiresAt.After(campaign.Spec.ScheduleAt.UTC()) {
		return false, nil
	}
	ids, err := selectedSmartTagIDs(ctx, selectionRepo, campaign.ID)
	if err != nil {
		if errors.Is(err, ErrSmartTargetingTagsRequired) {
			return false, nil
		}
		return false, err
	}
	// A selected-tag row can outlive its source tag being deactivated or removed
	// from the Bundle's current available-tag source. A matching ID hash alone
	// is therefore insufficient to authorize capacity reuse or approval.
	if err := selectionRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return false, nil
		}
		return false, err
	}
	classes, err := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
	if err != nil {
		return false, err
	}
	platform := strings.ToLower(strings.TrimSpace(campaign.Spec.Platform))
	applyBundleAudienceExclusions := smartTargetingCapacityAppliesBundleExclusions(campaign)
	if calculation.Platform != platform || calculation.ApplyBundleAudienceExclusions != applyBundleAudienceExclusions ||
		!sameSmartTargetingScoreClasses(classes, []string(calculation.SelectedScoreClasses)) {
		return false, nil
	}
	if calculation.CalculationVersion != smartTargetingCapacityAlgorithmVersion ||
		smartTargetingInputHash(smartTargetingTagHash(campaign.ID, ids), classes, platform, applyBundleAudienceExclusions) != calculation.InputHash {
		return false, nil
	}
	_, fingerprint, err := approvedCampaignDeduction(ctx, db, calculation, campaign.ID)
	if err != nil {
		return false, err
	}
	return fingerprint == calculation.AllocationFingerprint, nil
}

// smartTargetingBundleAllocationState computes the shared allocation
// fingerprint used by capacity and Test sampling. Active Test reservations
// alter the candidate population, but are not deducted here because the
// audience query already excludes their concrete members.
func smartTargetingBundleAllocationState(ctx context.Context, db *gorm.DB, bundleID, currentCampaignID uint) (int64, string, error) {
	if bundleID == 0 {
		return 0, "", ErrBundleNotFound
	}
	if db == nil {
		return 0, "", errors.New("bundle allocation database is not configured")
	}
	rows, err := repository.ListBundleCampaignAllocations(ctx, db, bundleID, currentCampaignID)
	if err != nil {
		return 0, "", err
	}
	activeTestReservations, err := repository.ListBundleActiveTestReservations(ctx, db, bundleID, currentCampaignID)
	if err != nil {
		return 0, "", err
	}
	return smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(bundleID, rows, activeTestReservations)
}

func smartTargetingBundleAllocationStateFromRows(bundleID uint, rows []repository.BundleCampaignAllocation) (int64, string, error) {
	return smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(bundleID, rows, nil)
}

func smartTargetingBundleAllocationStateFromRowsAndActiveTestReservations(bundleID uint, rows []repository.BundleCampaignAllocation, activeTestReservations []repository.BundleActiveTestReservation) (int64, string, error) {
	if bundleID == 0 {
		return 0, "", ErrBundleNotFound
	}
	var total int64
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.NumAudience == nil {
			return 0, "", fmt.Errorf("reserved campaign %d has no audience allocation", row.CampaignID)
		}
		amount := *row.NumAudience
		if amount > uint64(math.MaxInt64) {
			return 0, "", fmt.Errorf("approved campaign audience deduction overflow")
		}
		if (row.Status == models.CampaignStatusApproved || row.Status == models.CampaignStatusRunning) && !row.Materialized {
			if total > math.MaxInt64-int64(amount) {
				return 0, "", fmt.Errorf("approved campaign audience deduction overflow")
			}
			total += int64(amount)
		}
		parts = append(parts, strconv.FormatUint(uint64(row.CampaignID), 10)+":"+strconv.FormatUint(amount, 10)+":"+string(row.Status)+":"+strconv.FormatBool(row.Materialized))
	}
	fingerprintInput := "v3|bundle=" + strconv.FormatUint(uint64(bundleID), 10) + "|allocations=" + strings.Join(parts, ",")
	if len(activeTestReservations) > 0 {
		reservations := append([]repository.BundleActiveTestReservation(nil), activeTestReservations...)
		sort.Slice(reservations, func(i, j int) bool {
			if reservations[i].CampaignID != reservations[j].CampaignID {
				return reservations[i].CampaignID < reservations[j].CampaignID
			}
			return reservations[i].SelectionID < reservations[j].SelectionID
		})
		reservationParts := make([]string, 0, len(reservations))
		for _, reservation := range reservations {
			if reservation.CampaignID == 0 || reservation.SelectionID <= 0 || reservation.AudienceCount <= 0 {
				return 0, "", fmt.Errorf("active Test reservation is invalid")
			}
			reservationParts = append(reservationParts,
				strconv.FormatUint(uint64(reservation.CampaignID), 10)+":"+
					strconv.FormatInt(reservation.SelectionID, 10)+":"+
					strconv.FormatInt(reservation.AudienceCount, 10))
		}
		fingerprintInput += "|active_test_reservations=" + strings.Join(reservationParts, ",")
	}
	return total, hashSmartTargetingCapacityString(fingerprintInput), nil
}

func smartTargetingBundleAllocationFingerprint(ctx context.Context, db *gorm.DB, bundleID, currentCampaignID uint) (string, error) {
	_, fingerprint, err := smartTargetingBundleAllocationState(ctx, db, bundleID, currentCampaignID)
	return fingerprint, err
}

func approvedCampaignDeduction(ctx context.Context, db *gorm.DB, calculation *models.CampaignTargetingCapacityCalculation, currentCampaignID uint) (int64, string, error) {
	if calculation == nil {
		return 0, "", ErrInvalidState
	}
	return smartTargetingBundleAllocationState(ctx, db, calculation.BundleID, currentCampaignID)
}

func emptySmartTargetingAllocationFingerprint() string {
	return hashSmartTargetingCapacityString("v3|bundle=0|allocations=")
}

func smartTargetingCapacityAudienceQuery(calculation *models.CampaignTargetingCapacityCalculation) repository.SmartTargetingAudienceQuery {
	if calculation == nil {
		return repository.SmartTargetingAudienceQuery{}
	}
	return repository.SmartTargetingAudienceQuery{
		BundleID:                               calculation.BundleID,
		ExcludeActiveTestReservationCampaignID: calculation.CampaignID,
		ApplyBundleAudienceExclusions:          calculation.ApplyBundleAudienceExclusions,
		TagIDs:                                 []int64(calculation.SelectedTagIDs),
		ScoreClasses:                           []string(calculation.SelectedScoreClasses),
		AllowedColors:                          models.SmartTargetingAllowedColors(calculation.Platform),
	}
}

func (f *SmartTargetingCapacityFlowImpl) ExecuteCampaignTargetingCapacityCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) (err error) {
	calculation, err := f.calculationRepo.ByID(ctx, calculationID)
	if err != nil {
		return err
	}
	if calculation == nil || calculation.Status != models.CampaignTargetingCapacityCalculating || calculation.StartedAt == nil || !calculation.StartedAt.Equal(leaseStartedAt) {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("exact Smart Targeting capacity worker panicked: %v", recovered)
		}
		if err == nil {
			return
		}
		// Persist only a stable, non-sensitive error surface. The original error
		// remains in worker logs, never in polling responses.
		failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = f.calculationRepo.Fail(failCtx, calculation.ID, leaseStartedAt, "SMART_TARGETING_CAPACITY_CALCULATION_FAILED", "Exact capacity calculation could not be completed", time.Now().UTC())
	}()
	if calculation.ExpiresAt == nil || !calculation.ExpiresAt.After(time.Now().UTC()) {
		return errors.New("exact Smart Targeting capacity calculation expired before execution")
	}

	return repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		txDB := smartTargetingDB(txCtx, f.db)
		var current models.CampaignTargetingCapacityCalculation
		if err := txDB.First(&current, calculation.ID).Error; err != nil {
			return err
		}
		if current.Status != models.CampaignTargetingCapacityCalculating || current.StartedAt == nil || !current.StartedAt.Equal(leaseStartedAt) {
			return nil
		}
		if current.ExpiresAt == nil || !current.ExpiresAt.After(time.Now().UTC()) {
			return errors.New("exact Smart Targeting capacity calculation expired before counting")
		}
		// Approval and Bundle audience selection use an UPDATE lock on this row.
		// Holding a SHARE lock keeps prior-use and reservation state stable for the
		// count, fingerprint, and completion transaction.
		if err := repository.LockBundleForShare(txCtx, current.BundleID); err != nil {
			return err
		}
		capacity, err := repository.NewSmartTargetingAudienceRepository(txDB).CalculateCapacity(txCtx, smartTargetingCapacityAudienceQuery(&current))
		if err != nil {
			return err
		}
		deduction, fingerprint, err := approvedCampaignDeduction(txCtx, f.db, &current, current.CampaignID)
		if err != nil {
			return err
		}
		usable := capacity.EligibleUniqueCount - deduction
		if usable < 0 {
			usable = 0
		}
		return f.calculationRepo.Complete(txCtx, current.ID, leaseStartedAt, capacity.RawUniqueCount, capacity.EligibleUniqueCount, deduction, usable, fingerprint, time.Now().UTC())
	})
}

func capacityDTO(calculation *models.CampaignTargetingCapacityCalculation, current, recalculationRequired bool) *dto.SmartTargetingCapacityCalculationResponse {
	if calculation == nil {
		return &dto.SmartTargetingCapacityCalculationResponse{Status: "not_calculated", SelectedScoreClasses: []string{"A", "B", "C"}}
	}
	response := &dto.SmartTargetingCapacityCalculationResponse{
		CalculationID: calculation.ID, CampaignID: calculation.CampaignID, BundleID: calculation.BundleID,
		Status: string(calculation.Status), IsCurrent: current, RecalculationRequired: recalculationRequired,
		SelectedScoreClasses: append([]string(nil), calculation.SelectedScoreClasses...),
		SelectedTagCount:     calculation.SelectedTagCount, CreatedAt: calculation.CreatedAt,
		StartedAt: calculation.StartedAt, FinishedAt: calculation.FinishedAt, ExpiresAt: calculation.ExpiresAt,
	}
	if recalculationRequired {
		response.Status = "stale"
	}
	if calculation.Status == models.CampaignTargetingCapacityFailed {
		response.ErrorCode, response.ErrorMessage = calculation.ErrorCode, calculation.ErrorMessage
	}
	if current {
		response.RawAudienceCount = int64ToUint64Ptr(calculation.RawAudienceCount)
		response.EligibleUniqueCount = int64ToUint64Ptr(calculation.EligibleUniqueAudienceCount)
		response.ApprovedDeduction = int64ToUint64Ptr(calculation.ApprovedCampaignDeduction)
		response.UsableUniqueCount = int64ToUint64Ptr(calculation.UsableUniqueAudienceCount)
	}
	return response
}

func int64ToUint64Ptr(value int64) *uint64 {
	if value < 0 {
		return nil
	}
	result := uint64(value)
	return &result
}

func ptrTime(value time.Time) *time.Time { return &value }
