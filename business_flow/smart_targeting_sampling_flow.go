package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/lib/pq"
	"gorm.io/gorm/clause"
)

type smartTargetingTestSample struct {
	results   []dto.SmartTargetingTestSamplingTagResult
	satisfied []dto.SmartTargetingTestSamplingTagResult
	members   []models.CampaignTargetingTestSampleSelectionMember
	effective uint64
}

const smartTargetingTestSamplingCalculationVersion = 2

type smartTargetingTestSamplingInput struct {
	order         []uint
	displayNames  map[uint]*string
	classes       []string
	allowedColors []string
	hash          string
}

type smartTargetingTestSamplingIntent struct {
	satisfied []uint
	effective uint64
}

func checkedSmartTargetingTestAudienceCount(satisfiedTagCount int, sampleSizePerTag uint64) (uint64, error) {
	if satisfiedTagCount < 0 || sampleSizePerTag == 0 || uint64(satisfiedTagCount) > uint64(math.MaxInt64)/sampleSizePerTag {
		return 0, ErrSmartTargetingTestAudienceCountOverflow
	}
	return uint64(satisfiedTagCount) * sampleSizePerTag, nil
}

func smartTargetingTestSamplingHash(campaign *models.Campaign, orderedTagIDs []uint, classes []string) (string, error) {
	if campaign == nil || campaign.BundleID == nil || campaign.SampleSizePerTag == nil {
		return "", ErrSmartTargetingTestPreviewRequired
	}
	parts := make([]string, len(orderedTagIDs))
	for i, id := range orderedTagIDs {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	allowedColors := models.SmartTargetingAllowedColors(campaign.Spec.Platform)
	value := "feature4-v2|campaign=" + strconv.FormatUint(uint64(campaign.ID), 10) +
		"|bundle=" + strconv.FormatUint(uint64(*campaign.BundleID), 10) +
		"|sample=" + strconv.FormatUint(*campaign.SampleSizePerTag, 10) +
		"|tags=" + strings.Join(parts, ",") +
		"|classes=" + strings.Join(classes, ",") +
		"|colors=" + strings.Join(allowedColors, ",")
	return hashSmartTargetingCapacityString(value), nil
}

func currentSmartTargetingTestSamplingInput(ctx context.Context, selectedTagRepo repository.CampaignSelectedTagRepository, campaign *models.Campaign) (*smartTargetingTestSamplingInput, error) {
	if campaign == nil || !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseTest {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	if campaign.BundleID == nil || *campaign.BundleID == 0 {
		return nil, ErrBundleNotFound
	}
	if campaign.SampleSizePerTag == nil {
		return nil, ErrSmartTargetingSampleSizeRequired
	}
	if *campaign.SampleSizePerTag == 0 || *campaign.SampleSizePerTag > math.MaxInt64 {
		return nil, ErrSmartTargetingSampleSizeInvalid
	}
	selected, err := selectedTagRepo.ListSelected(ctx, campaign.ID)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, ErrSmartTargetingTagsRequired
	}
	input := &smartTargetingTestSamplingInput{
		order:        make([]uint, 0, len(selected)),
		displayNames: make(map[uint]*string, len(selected)),
	}
	for position, selectedTag := range selected {
		if selectedTag == nil || selectedTag.CampaignID != campaign.ID || selectedTag.BundleID != *campaign.BundleID ||
			selectedTag.TagID == 0 || selectedTag.SelectionOrder != position {
			return nil, ErrSmartTargetingTagInvalid
		}
		input.order = append(input.order, selectedTag.TagID)
		input.displayNames[selectedTag.TagID] = selectedTag.TagDisplayTitleSnapshot
	}
	input.classes, err = normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
	if err != nil {
		return nil, err
	}
	input.allowedColors = models.SmartTargetingAllowedColors(campaign.Spec.Platform)
	input.hash, err = smartTargetingTestSamplingHash(campaign, input.order, input.classes)
	if err != nil {
		return nil, err
	}
	return input, nil
}

func currentSmartTargetingTestSamplingIntent(ctx context.Context, selectedTagRepo repository.CampaignSelectedTagRepository, campaign *models.Campaign, requireSatisfied bool) (*smartTargetingTestSamplingIntent, error) {
	input, err := currentSmartTargetingTestSamplingInput(ctx, selectedTagRepo, campaign)
	if err != nil {
		return nil, err
	}
	if campaign.SmartTargetingTestSamplingInputHash == nil || campaign.SmartTargetingTestSamplingPreviewedAt == nil || *campaign.SmartTargetingTestSamplingInputHash != input.hash {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	satisfied := make([]uint, 0, len(campaign.SmartTargetingTestSatisfiedTagIDs))
	selectedPosition := 0
	seen := make(map[uint]struct{}, len(campaign.SmartTargetingTestSatisfiedTagIDs))
	for _, rawID := range campaign.SmartTargetingTestSatisfiedTagIDs {
		if rawID <= 0 || uint64(rawID) > uint64(^uint(0)) {
			return nil, ErrSmartTargetingTestPreviewRequired
		}
		id := uint(rawID)
		if _, exists := seen[id]; exists {
			return nil, ErrSmartTargetingTestPreviewRequired
		}
		seen[id] = struct{}{}
		for selectedPosition < len(input.order) && input.order[selectedPosition] != id {
			selectedPosition++
		}
		if selectedPosition == len(input.order) {
			return nil, ErrSmartTargetingTestPreviewRequired
		}
		selectedPosition++
		satisfied = append(satisfied, id)
	}
	if requireSatisfied && len(satisfied) == 0 {
		return nil, ErrSmartTargetingTestNoSatisfiedTags
	}
	effective, err := checkedSmartTargetingTestAudienceCount(len(satisfied), *campaign.SampleSizePerTag)
	if err != nil {
		return nil, err
	}
	return &smartTargetingTestSamplingIntent{
		satisfied: satisfied,
		effective: effective,
	}, nil
}

func smartTargetingTestSamplingAudienceQuery(bundleID uint, tagIDs []int64, input *smartTargetingTestSamplingInput) repository.SmartTargetingAudienceQuery {
	return repository.SmartTargetingAudienceQuery{
		BundleID:                      bundleID,
		ApplyBundleAudienceExclusions: true,
		TagIDs:                        tagIDs,
		ScoreClasses:                  input.classes,
		AllowedColors:                 input.allowedColors,
	}
}

func (s *CampaignFlowImpl) calculateSmartTargetingTestSampleForInput(ctx context.Context, bundleID uint, sampleSizePerTag uint64, input *smartTargetingTestSamplingInput) (*smartTargetingTestSample, error) {
	if bundleID == 0 || input == nil || len(input.order) == 0 || len(input.displayNames) != len(input.order) || len(input.classes) == 0 || sampleSizePerTag == 0 || sampleSizePerTag > math.MaxInt64 {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	tagIDs := make([]int64, 0, len(input.order))
	result := &smartTargetingTestSample{
		results:   make([]dto.SmartTargetingTestSamplingTagResult, 0, len(input.order)),
		satisfied: make([]dto.SmartTargetingTestSamplingTagResult, 0, len(input.order)),
	}
	for _, tagID := range input.order {
		tagIDs = append(tagIDs, int64(tagID))
	}

	// TODO(feature-4-approved-reservations): approved campaigns have no concrete
	// audience IDs until their schedulers run. A preview cannot exclude those
	// future allocations; the scheduler remains the availability source of truth.
	excluded := make([]int64, 0)
	audienceRepo := repository.NewSmartTargetingAudienceRepository(s.db)
	query := smartTargetingTestSamplingAudienceQuery(bundleID, tagIDs, input)
	bounds, err := audienceRepo.CalculateScoreBounds(ctx, query)
	if err != nil {
		return nil, err
	}
	for position, tagID := range tagIDs {
		rows, err := audienceRepo.SelectForTag(ctx, query, bounds, tagID, excluded, int64(sampleSizePerTag))
		if err != nil {
			return nil, err
		}
		item := dto.SmartTargetingTestSamplingTagResult{
			TagID: uint(tagID), TagDisplayName: input.displayNames[uint(tagID)], SelectionOrder: position, AvailableCount: int64(len(rows)),
			Satisfied: uint64(len(rows)) == sampleSizePerTag,
		}
		result.results = append(result.results, item)
		if !item.Satisfied {
			continue
		}
		result.satisfied = append(result.satisfied, item)
		for _, row := range rows {
			if row == nil || row.ID <= 0 {
				return nil, ErrSmartTargetingTestPreviewRequired
			}
			result.members = append(result.members, models.CampaignTargetingTestSampleSelectionMember{
				AudienceID: row.ID, AssignedTagID: uint(tagID), TagSelectionOrder: int64(position),
				SelectionOrder: int64(len(result.members)), AudienceScore: row.NormalizedScore,
			})
			excluded = append(excluded, row.ID)
		}
	}
	effective, err := checkedSmartTargetingTestAudienceCount(len(result.satisfied), sampleSizePerTag)
	if err != nil {
		return nil, err
	}
	result.effective = effective
	return result, nil
}

func checkedCampaignCost(pricePerMessage, audienceCount uint64) (uint64, error) {
	if audienceCount != 0 && pricePerMessage > math.MaxUint64/audienceCount {
		return 0, NewBusinessError("CAMPAIGN_COST_OVERFLOW", "Campaign cost exceeds the supported range", ErrCampaignCostOverflow)
	}
	return pricePerMessage * audienceCount, nil
}

func clearCampaignSmartTargetingTestSamplingPreviewFields(campaign *models.Campaign) {
	if campaign == nil {
		return
	}
	campaign.SmartTargetingTestSatisfiedTagIDs = pq.Int64Array{}
	campaign.SmartTargetingTestSamplingInputHash = nil
	campaign.SmartTargetingTestSamplingPreviewedAt = nil
	campaign.NumAudience = utils.ToPtr(uint64(0))
}


func (s *CampaignFlowImpl) clearCampaignSmartTargetingTestSamplingPreview(ctx context.Context, campaignID uint) error {
	return smartTargetingDB(ctx, s.db).Model(&models.Campaign{}).Where("id = ?", campaignID).Updates(map[string]any{
		"smart_targeting_test_satisfied_tag_ids":     pq.Int64Array{},
		"smart_targeting_test_sampling_input_hash":   nil,
		"smart_targeting_test_sampling_previewed_at": nil,
		"active_smart_targeting_test_selection_id":   nil,
		"num_audience": uint64(0),
		"updated_at":   utils.UTCNow(),
	}).Error
}

func (s *CampaignFlowImpl) ownedSmartTargetingTestCampaign(ctx context.Context, customerID uint, campaignUUID string) (*models.Campaign, error) {
	if customerID == 0 || strings.TrimSpace(campaignUUID) == "" {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_INVALID", "Invalid Smart Targeting Test sampling request", ErrCampaignNotFound)
	}
	campaign, err := getCampaign(ctx, s.campaignRepo, campaignUUID, customerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	if !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseTest {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_INVALID", "Smart Targeting Test campaign is required", ErrInvalidState)
	}
	if campaign.BundleID == nil || *campaign.BundleID == 0 {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Campaign bundle not found", ErrBundleNotFound)
	}
	return &campaign, nil
}

func reusableActiveSmartTargetingTestSampling(campaign *models.Campaign, calculation *models.CampaignTargetingTestSamplingCalculation, currentInput *smartTargetingTestSamplingInput) bool {
	if campaign == nil || calculation == nil || currentInput == nil ||
		calculation.Status != models.CampaignTargetingTestSamplingCalculating || calculation.InputHash != currentInput.hash ||
		calculation.Generation <= 0 || calculation.Generation != campaign.SmartTargetingTestSamplingGeneration {
		return false
	}
	_, _, err := samplingInputFromCalculation(campaign, calculation)
	return err == nil
}

func currentSmartTargetingTestSamplingCalculationInput(
	ctx context.Context,
	selectedTagRepo repository.CampaignSelectedTagRepository,
	campaign *models.Campaign,
	calculation *models.CampaignTargetingTestSamplingCalculation,
) (*smartTargetingTestSamplingInput, uint64, error) {
	if campaign == nil || !campaign.IsEditable() {
		return nil, 0, ErrSmartTargetingTestPreviewRequired
	}
	_, sampleSizePerTag, err := samplingInputFromCalculation(campaign, calculation)
	if err != nil {
		return nil, 0, err
	}
	currentInput, err := currentSmartTargetingTestSamplingInput(ctx, selectedTagRepo, campaign)
	if err != nil {
		return nil, 0, err
	}
	if currentInput.hash != calculation.InputHash {
		return nil, 0, ErrSmartTargetingTestPreviewRequired
	}
	return currentInput, sampleSizePerTag, nil
}

// StartSmartTargetingTestSampling snapshots the current inputs and returns a
// durable job immediately. The audience scan is performed only by the worker.
func (s *CampaignFlowImpl) StartSmartTargetingTestSampling(ctx context.Context, req *dto.SmartTargetingTestSamplingPreviewRequest, metadata *ClientMetadata) (*dto.SmartTargetingTestSamplingCalculationResponse, error) {
	if req == nil || s.samplingCalculationRepo == nil {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_UNAVAILABLE", "Smart Targeting Test sampling is unavailable", ErrInvalidState)
	}
	campaign, err := s.ownedSmartTargetingTestCampaign(ctx, req.CustomerID, req.CampaignUUID)
	if err != nil {
		return nil, err
	}
	if !campaign.IsEditable() {
		return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Sampling preview cannot change a finalized campaign", ErrCampaignUpdateNotAllowed)
	}

	var calculation *models.CampaignTargetingTestSamplingCalculation
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		txDB := smartTargetingDB(txCtx, s.db)
		var lockedCampaign models.Campaign
		if err := txDB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedCampaign, campaign.ID).Error; err != nil {
			return err
		}
		if !lockedCampaign.IsEditable() || !lockedCampaign.Spec.UsesSmartTargeting() || lockedCampaign.Phase != models.CampaignPhaseTest || lockedCampaign.BundleID == nil || *lockedCampaign.BundleID == 0 {
			return ErrCampaignUpdateNotAllowed
		}
		input, err := currentSmartTargetingTestSamplingInput(txCtx, s.selectedTagRepo, &lockedCampaign)
		if err != nil {
			return err
		}
		if err := s.selectedTagRepo.Validate(txCtx, lockedCampaign.ID, *lockedCampaign.BundleID); err != nil {
			return err
		}
		now := time.Now().UTC()
		active, err := s.samplingCalculationRepo.ActiveByCampaignID(txCtx, lockedCampaign.ID)
		if err != nil {
			return err
		}
		if active != nil {
			// Retrying an unchanged request must not discard useful worker progress.
			// API clients can retry a timed-out submission while the worker is
			// already scanning this exact immutable input snapshot.
			if reusableActiveSmartTargetingTestSampling(&lockedCampaign, active, input) {
				calculation = active
				return nil
			}
			if err := s.samplingCalculationRepo.Supersede(txCtx, active.ID, "SMART_TARGETING_TEST_SAMPLING_SUPERSEDED", "A newer campaign configuration replaced this sampling calculation", now); err != nil {
				if !errors.Is(err, repository.ErrCampaignTargetingTestSamplingStateConflict) {
					return err
				}
				remaining, reloadErr := s.samplingCalculationRepo.ActiveByCampaignID(txCtx, lockedCampaign.ID)
				if reloadErr != nil {
					return reloadErr
				}
				if remaining != nil {
					return repository.ErrCampaignTargetingTestSamplingStateConflict
				}
			}
		}
		generation := lockedCampaign.SmartTargetingTestSamplingGeneration + 1
		if err := txDB.Model(&models.Campaign{}).Where("id = ?", lockedCampaign.ID).Updates(map[string]any{
			"smart_targeting_test_sampling_generation":   generation,
			"active_smart_targeting_test_selection_id":   nil,
			"smart_targeting_test_satisfied_tag_ids":     pq.Int64Array{},
			"smart_targeting_test_sampling_input_hash":   nil,
			"smart_targeting_test_sampling_previewed_at": nil,
			"num_audience": uint64(0), "updated_at": utils.UTCNow(),
		}).Error; err != nil {
			return err
		}

		calculation = &models.CampaignTargetingTestSamplingCalculation{
			CampaignID:            lockedCampaign.ID,
			BundleID:              *lockedCampaign.BundleID,
			CustomerID:            lockedCampaign.CustomerID,
			RequestedByCustomerID: req.CustomerID,
			SelectedTagIDs:        tagIDsToInt64(input.order),
			InputHash:             input.hash,
			SelectedScoreClasses:  pq.StringArray(input.classes),
			SelectedTagCount:      len(input.order),
			SampleSizePerTag:      int64(*lockedCampaign.SampleSizePerTag),
			TagResults:            json.RawMessage(`[]`),
			Status:                models.CampaignTargetingTestSamplingCalculating,
			CalculationVersion:    smartTargetingTestSamplingCalculationVersion,
			Generation:            generation,
			AllocationFingerprint: emptySmartTargetingAllocationFingerprint(),
			CreatedAt:             now,
		}
		return s.samplingCalculationRepo.Save(txCtx, calculation)
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSmartTargetingTagsRequired):
			return nil, NewBusinessError("SMART_TARGETING_TAGS_REQUIRED", "Select at least one tag before calculating the Test sample", err)
		case errors.Is(err, ErrSmartTargetingTagInvalid), errors.Is(err, repository.ErrInvalidCampaignSelectedTags):
			return nil, NewBusinessError("SMART_TARGETING_SELECTION_INVALID", "The selected Smart Targeting tags are no longer valid", err)
		case errors.Is(err, ErrCampaignUpdateNotAllowed):
			return nil, NewBusinessError("CAMPAIGN_UPDATE_NOT_ALLOWED", "Sampling preview cannot change a finalized campaign", err)
		default:
			return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_REQUEST_FAILED", "Failed to request Smart Targeting Test sampling", err)
		}
	}
	_ = metadata
	return smartTargetingTestSamplingCalculationDTO(calculation, false, false)
}

func (s *CampaignFlowImpl) GetCurrentSmartTargetingTestSampling(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingTestSamplingCalculationResponse, error) {
	if s.samplingCalculationRepo == nil {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_UNAVAILABLE", "Smart Targeting Test sampling is unavailable", ErrInvalidState)
	}
	campaign, err := s.ownedSmartTargetingTestCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	input, inputErr := currentSmartTargetingTestSamplingInput(ctx, s.selectedTagRepo, campaign)
	if inputErr == nil {
		calculated, err := s.samplingCalculationRepo.LatestCalculatedByInput(ctx, campaign.ID, input.hash)
		if err != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to load Smart Targeting Test sampling", err)
		}
		if calculated != nil {
			current, err := s.isCurrentSmartTargetingTestSampling(ctx, campaign, calculated)
			if err != nil {
				return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to validate Smart Targeting Test sampling", err)
			}
			if current {
				return smartTargetingTestSamplingCalculationDTO(calculated, true, false)
			}
		}
		calculation, err := s.samplingCalculationRepo.LatestByInput(ctx, campaign.ID, input.hash)
		if err != nil {
			return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to load Smart Targeting Test sampling", err)
		}
		if calculation != nil {
			return s.smartTargetingTestSamplingStatusDTO(ctx, campaign, calculation)
		}
	} else if !errors.Is(inputErr, ErrSmartTargetingTagsRequired) {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to load Smart Targeting Test sampling inputs", inputErr)
	}

	calculation, err := s.samplingCalculationRepo.LatestByCampaignID(ctx, campaign.ID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to load Smart Targeting Test sampling", err)
	}
	if calculation == nil {
		classes, classErr := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
		if classErr != nil {
			return nil, NewBusinessError("SMART_TARGETING_SCORE_CLASSES_INVALID", "Audience score classes are invalid", classErr)
		}
		response := &dto.SmartTargetingTestSamplingCalculationResponse{
			CampaignID: campaign.ID, BundleID: *campaign.BundleID, Status: "not_calculated",
			SelectedScoreClasses: classes,
		}
		if campaign.SampleSizePerTag != nil {
			response.SampleSizePerTag = *campaign.SampleSizePerTag
		}
		return response, nil
	}
	return smartTargetingTestSamplingCalculationDTO(calculation, false, true)
}

func (s *CampaignFlowImpl) GetSmartTargetingTestSamplingByID(ctx context.Context, customerID uint, campaignUUID string, calculationID int64) (*dto.SmartTargetingTestSamplingCalculationResponse, error) {
	if s.samplingCalculationRepo == nil {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_UNAVAILABLE", "Smart Targeting Test sampling is unavailable", ErrInvalidState)
	}
	campaign, err := s.ownedSmartTargetingTestCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	calculation, err := s.samplingCalculationRepo.ByID(ctx, calculationID)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_LOOKUP_FAILED", "Failed to load Smart Targeting Test sampling", err)
	}
	if calculation == nil || calculation.CampaignID != campaign.ID {
		return nil, NewBusinessError("SMART_TARGETING_TEST_SAMPLING_NOT_FOUND", "Smart Targeting Test sampling calculation not found", ErrCampaignNotFound)
	}
	return s.smartTargetingTestSamplingStatusDTO(ctx, campaign, calculation)
}

func (s *CampaignFlowImpl) smartTargetingTestSamplingStatusDTO(ctx context.Context, campaign *models.Campaign, calculation *models.CampaignTargetingTestSamplingCalculation) (*dto.SmartTargetingTestSamplingCalculationResponse, error) {
	if calculation == nil || calculation.Status == models.CampaignTargetingTestSamplingFailed {
		return smartTargetingTestSamplingCalculationDTO(calculation, false, false)
	}
	current, err := s.isCurrentSmartTargetingTestSampling(ctx, campaign, calculation)
	if err != nil {
		return nil, err
	}
	return smartTargetingTestSamplingCalculationDTO(calculation, current, calculation.Status == models.CampaignTargetingTestSamplingCalculated && !current)
}

func (s *CampaignFlowImpl) isCurrentSmartTargetingTestSampling(ctx context.Context, campaign *models.Campaign, calculation *models.CampaignTargetingTestSamplingCalculation) (bool, error) {
	if campaign == nil || calculation == nil || calculation.CampaignID != campaign.ID || campaign.BundleID == nil || calculation.BundleID != *campaign.BundleID ||
		calculation.Status != models.CampaignTargetingTestSamplingCalculated || calculation.CalculationVersion != smartTargetingTestSamplingCalculationVersion || calculation.FinishedAt == nil ||
		calculation.Generation <= 0 || calculation.Generation != campaign.SmartTargetingTestSamplingGeneration || campaign.ActiveSmartTargetingTestSelectionID == nil ||
		campaign.SmartTargetingTestSamplingInputHash == nil || campaign.SmartTargetingTestSamplingPreviewedAt == nil ||
		*campaign.SmartTargetingTestSamplingInputHash != calculation.InputHash || !campaign.SmartTargetingTestSamplingPreviewedAt.Equal(*calculation.FinishedAt) {
		return false, nil
	}
	selection, err := repository.NewCampaignTargetingTestSampleSelectionRepository(s.db).ByCalculationID(ctx, calculation.ID)
	if err != nil {
		return false, err
	}
	if selection == nil || selection.ID != *campaign.ActiveSmartTargetingTestSelectionID || selection.Generation != calculation.Generation ||
		int64(len(selection.Members)) != calculation.EffectiveAudienceCount {
		return false, nil
	}
	storedInput, _, err := samplingInputFromCalculation(campaign, calculation)
	if err != nil {
		return false, nil
	}
	input, err := currentSmartTargetingTestSamplingInput(ctx, s.selectedTagRepo, campaign)
	if err != nil {
		if errors.Is(err, ErrSmartTargetingTestPreviewRequired) || errors.Is(err, ErrSmartTargetingTagsRequired) {
			return false, nil
		}
		return false, err
	}
	if input.hash != calculation.InputHash || storedInput.hash != input.hash {
		return false, nil
	}
	if err := s.selectedTagRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return false, nil
		}
		return false, err
	}
	allocationFingerprint, err := smartTargetingBundleAllocationFingerprint(ctx, s.db, calculation.BundleID, campaign.ID)
	if err != nil {
		return false, err
	}
	return calculation.AllocationFingerprint == allocationFingerprint, nil
}

func (s *CampaignFlowImpl) requireCurrentSmartTargetingTestSampling(ctx context.Context, campaign *models.Campaign) error {
	if campaign == nil || s.samplingCalculationRepo == nil {
		return ErrSmartTargetingTestPreviewRequired
	}
	input, err := currentSmartTargetingTestSamplingInput(ctx, s.selectedTagRepo, campaign)
	if err != nil {
		return err
	}
	calculation, err := s.samplingCalculationRepo.LatestCalculatedByInput(ctx, campaign.ID, input.hash)
	if err != nil {
		return err
	}
	current, err := s.isCurrentSmartTargetingTestSampling(ctx, campaign, calculation)
	if err != nil {
		return err
	}
	if !current {
		return ErrSmartTargetingTestPreviewRequired
	}
	return nil
}

func samplingInputFromCalculation(campaign *models.Campaign, calculation *models.CampaignTargetingTestSamplingCalculation) (*smartTargetingTestSamplingInput, uint64, error) {
	if campaign == nil || calculation == nil || calculation.CampaignID != campaign.ID || calculation.CalculationVersion != smartTargetingTestSamplingCalculationVersion || calculation.SampleSizePerTag <= 0 ||
		campaign.BundleID == nil || *campaign.BundleID != calculation.BundleID || campaign.SampleSizePerTag == nil || *campaign.SampleSizePerTag != uint64(calculation.SampleSizePerTag) ||
		calculation.SelectedTagCount <= 0 || calculation.SelectedTagCount != len(calculation.SelectedTagIDs) {
		return nil, 0, ErrSmartTargetingTestPreviewRequired
	}
	order := make([]uint, len(calculation.SelectedTagIDs))
	seen := make(map[uint]struct{}, len(calculation.SelectedTagIDs))
	for i, rawID := range calculation.SelectedTagIDs {
		if rawID <= 0 || uint64(rawID) > uint64(^uint(0)) {
			return nil, 0, ErrSmartTargetingTagInvalid
		}
		tagID := uint(rawID)
		if _, exists := seen[tagID]; exists {
			return nil, 0, ErrSmartTargetingTagInvalid
		}
		seen[tagID] = struct{}{}
		order[i] = tagID
	}
	classes, err := normalizeSmartTargetingScoreClasses([]string(calculation.SelectedScoreClasses))
	if err != nil || !sameSmartTargetingScoreClasses(classes, []string(calculation.SelectedScoreClasses)) {
		return nil, 0, ErrSmartTargetingScoreClassesInvalid
	}
	hash, err := smartTargetingTestSamplingHash(campaign, order, classes)
	if err != nil || hash != calculation.InputHash {
		return nil, 0, ErrSmartTargetingTestPreviewRequired
	}
	return &smartTargetingTestSamplingInput{
		order:         order,
		classes:       classes,
		allowedColors: models.SmartTargetingAllowedColors(campaign.Spec.Platform),
		hash:          hash,
	}, uint64(calculation.SampleSizePerTag), nil
}

// ExecuteSmartTargetingTestSamplingCalculation performs the expensive scan and
// atomically publishes both the job result and campaign sampling intent.
func (s *CampaignFlowImpl) ExecuteSmartTargetingTestSamplingCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) (err error) {
	calculation, err := s.samplingCalculationRepo.ByID(ctx, calculationID)
	if err != nil {
		return err
	}
	if calculation == nil || calculation.Status != models.CampaignTargetingTestSamplingCalculating || calculation.StartedAt == nil || !calculation.StartedAt.Equal(leaseStartedAt) {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Smart Targeting Test sampling worker panicked: %v", recovered)
		}
		if err == nil {
			return
		}
		failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.samplingCalculationRepo.Fail(failCtx, calculation.ID, leaseStartedAt, "SMART_TARGETING_TEST_SAMPLING_CALCULATION_FAILED", "Smart Targeting Test sampling could not be completed", time.Now().UTC())
	}()

	campaign, err := s.campaignRepo.ByID(ctx, calculation.CampaignID)
	if err != nil {
		return err
	}
	currentInput, sampleSizePerTag, err := currentSmartTargetingTestSamplingCalculationInput(ctx, s.selectedTagRepo, campaign, calculation)
	if err != nil {
		return err
	}
	if err := s.selectedTagRepo.Validate(ctx, campaign.ID, calculation.BundleID); err != nil {
		return err
	}
	allocationFingerprint, err := smartTargetingBundleAllocationFingerprint(ctx, s.db, calculation.BundleID, campaign.ID)
	if err != nil {
		return err
	}

	sample, err := s.calculateSmartTargetingTestSampleForInput(ctx, calculation.BundleID, sampleSizePerTag, currentInput)
	if err != nil {
		return err
	}
	tagResults, err := json.Marshal(sample.results)
	if err != nil {
		return err
	}

	return repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		txDB := smartTargetingDB(txCtx, s.db)
		var lockedCampaign models.Campaign
		if err := txDB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedCampaign, calculation.CampaignID).Error; err != nil {
			return err
		}
		if _, _, err := currentSmartTargetingTestSamplingCalculationInput(txCtx, s.selectedTagRepo, &lockedCampaign, calculation); err != nil {
			return err
		}
		// Approval and audience materialization take an UPDATE lock on the
		// Bundle. Holding a SHARE lock makes sampling's own allocation fingerprint
		// and campaign preview publication one atomic view of Bundle allocations.
		if err := repository.LockBundleForShare(txCtx, *lockedCampaign.BundleID); err != nil {
			return err
		}
		if err := s.selectedTagRepo.Validate(txCtx, lockedCampaign.ID, *lockedCampaign.BundleID); err != nil {
			return err
		}
		currentAllocationFingerprint, err := smartTargetingBundleAllocationFingerprint(txCtx, s.db, calculation.BundleID, lockedCampaign.ID)
		if err != nil {
			return err
		}
		if currentAllocationFingerprint != allocationFingerprint {
			return ErrSmartTargetingTestPreviewRequired
		}
		// Pricing must use the same final, locked campaign state that receives
		// the published preview. Unrelated saves no longer invalidate sampling,
		// so computing this before the lock could publish a stale cost.
		pricePerMessage, err := s.computePricePerMessage(txCtx, lockedCampaign)
		if err != nil {
			return err
		}
		cost, err := checkedCampaignCost(pricePerMessage, sample.effective)
		if err != nil {
			return err
		}
		if customer, lookupErr := getCustomer(txCtx, s.customerRepo, calculation.CustomerID); lookupErr == nil && s.adminConfig.HasMobile(customer.RepresentativeMobile) {
			cost = 0
		}
		satisfied := make(pq.Int64Array, 0, len(sample.satisfied))
		for _, item := range sample.satisfied {
			satisfied = append(satisfied, int64(item.TagID))
		}
		finishedAt := time.Now().UTC()
		if err := txDB.Model(&models.Campaign{}).Where("id = ?", lockedCampaign.ID).Updates(map[string]any{
			"smart_targeting_test_satisfied_tag_ids":     satisfied,
			"smart_targeting_test_sampling_input_hash":   calculation.InputHash,
			"smart_targeting_test_sampling_previewed_at": finishedAt,
			"active_smart_targeting_test_selection_id":   nil,
			"num_audience": sample.effective, "updated_at": utils.UTCNow(),
		}).Error; err != nil {
			return err
		}
		selection := &models.CampaignTargetingTestSampleSelection{
			CalculationID: calculation.ID, CampaignID: lockedCampaign.ID, BundleID: calculation.BundleID,
			Generation: calculation.Generation, InputHash: calculation.InputHash,
			EffectiveAudienceCount: int64(sample.effective), CreatedAt: finishedAt, Members: sample.members,
		}
		selectionRepo := repository.NewCampaignTargetingTestSampleSelectionRepository(txDB)
		if err := selectionRepo.Save(txCtx, selection); err != nil {
			return err
		}
		if err := txDB.Model(&models.Campaign{}).Where("id = ? AND smart_targeting_test_sampling_generation = ?", lockedCampaign.ID, calculation.Generation).
			Update("active_smart_targeting_test_selection_id", selection.ID).Error; err != nil {
			return err
		}
		return s.samplingCalculationRepo.Complete(txCtx, calculation.ID, leaseStartedAt, tagResults, len(sample.satisfied), int64(sample.effective), cost, allocationFingerprint, finishedAt)
	})
}

func smartTargetingTestSamplingCalculationDTO(calculation *models.CampaignTargetingTestSamplingCalculation, current, recalculationRequired bool) (*dto.SmartTargetingTestSamplingCalculationResponse, error) {
	if calculation == nil {
		return &dto.SmartTargetingTestSamplingCalculationResponse{Status: "not_calculated"}, nil
	}
	if calculation.SampleSizePerTag <= 0 {
		return nil, ErrSmartTargetingSampleSizeInvalid
	}
	if calculation.SelectedTagCount <= 0 || calculation.SelectedTagCount != len(calculation.SelectedTagIDs) {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	order := make([]uint, len(calculation.SelectedTagIDs))
	seen := make(map[uint]struct{}, len(calculation.SelectedTagIDs))
	for i, rawID := range calculation.SelectedTagIDs {
		if rawID <= 0 || uint64(rawID) > uint64(^uint(0)) {
			return nil, ErrSmartTargetingTagInvalid
		}
		tagID := uint(rawID)
		if _, exists := seen[tagID]; exists {
			return nil, ErrSmartTargetingTagInvalid
		}
		seen[tagID] = struct{}{}
		order[i] = tagID
	}
	response := &dto.SmartTargetingTestSamplingCalculationResponse{
		CalculationID: calculation.ID, CampaignID: calculation.CampaignID, BundleID: calculation.BundleID,
		Status: string(calculation.Status), IsCurrent: current, RecalculationRequired: recalculationRequired,
		SampleSizePerTag: uint64(calculation.SampleSizePerTag), TagSamplingOrder: order,
		SelectedScoreClasses: append([]string(nil), calculation.SelectedScoreClasses...),
		CreatedAt:            calculation.CreatedAt, StartedAt: calculation.StartedAt, FinishedAt: calculation.FinishedAt,
	}
	if recalculationRequired {
		response.Status = "stale"
	}
	if calculation.Status == models.CampaignTargetingTestSamplingFailed {
		response.ErrorCode, response.ErrorMessage = calculation.ErrorCode, calculation.ErrorMessage
	}
	// A completed generation can become stale when campaign inputs or Bundle
	// allocations change. Match exact-capacity polling and never expose stale
	// counts as usable sampling output.
	if calculation.Status != models.CampaignTargetingTestSamplingCalculated || !current {
		return response, nil
	}
	var results []dto.SmartTargetingTestSamplingTagResult
	if err := json.Unmarshal(calculation.TagResults, &results); err != nil {
		return nil, err
	}
	if len(results) != len(order) || calculation.SatisfiedTagCount < 0 || calculation.SatisfiedTagCount > len(results) {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	response.SatisfiedTags = make([]dto.SmartTargetingTestSamplingTagResult, 0, calculation.SatisfiedTagCount)
	response.UnsatisfiedTags = make([]dto.SmartTargetingTestSamplingTagResult, 0, len(results)-calculation.SatisfiedTagCount)
	actualSatisfied := 0
	for position, item := range results {
		if item.TagID != order[position] || item.SelectionOrder != position || item.AvailableCount < 0 ||
			uint64(item.AvailableCount) > uint64(calculation.SampleSizePerTag) || item.Satisfied != (item.AvailableCount == calculation.SampleSizePerTag) {
			return nil, ErrSmartTargetingTestPreviewRequired
		}
		if item.Satisfied {
			actualSatisfied++
			response.SatisfiedTags = append(response.SatisfiedTags, item)
		} else {
			response.UnsatisfiedTags = append(response.UnsatisfiedTags, item)
		}
	}
	if actualSatisfied != calculation.SatisfiedTagCount {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	if calculation.EffectiveAudienceCount < 0 {
		return nil, ErrSmartTargetingTestAudienceCountOverflow
	}
	expectedEffective, err := checkedSmartTargetingTestAudienceCount(actualSatisfied, uint64(calculation.SampleSizePerTag))
	if err != nil || expectedEffective != uint64(calculation.EffectiveAudienceCount) {
		return nil, ErrSmartTargetingTestPreviewRequired
	}
	satisfiedCount := calculation.SatisfiedTagCount
	effective := uint64(calculation.EffectiveAudienceCount)
	cost := calculation.CampaignCost
	response.SatisfiedTagCount = &satisfiedCount
	response.EffectiveAudienceCount = &effective
	response.CampaignCost = &cost
	return response, nil
}
