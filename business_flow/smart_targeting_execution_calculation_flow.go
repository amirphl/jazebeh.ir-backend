package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/lib/pq"
	"gorm.io/gorm/clause"
)

var errSmartTargetingExecutionCalculationStale = errors.New("execution audience calculation is stale")

type smartTargetingExecutionCalculationInput struct {
	header *models.CampaignTargetingExecutionReservationHeader
	query  repository.SmartTargetingAudienceQuery
}

// executionCalculationInput produces the same immutable eligibility contract
// used by the active reservation, without scanning or reserving anybody.
func (s *CampaignFlowImpl) executionCalculationInput(ctx context.Context, campaign *models.Campaign, expected uint64) (*smartTargetingExecutionCalculationInput, error) {
	if campaign == nil || !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseExecution || campaign.BundleID == nil || *campaign.BundleID == 0 || expected == 0 || expected > math.MaxInt64 {
		return nil, repository.ErrSmartTargetingExecutionReservationUnavailable
	}
	selected, err := s.selectedTagRepo.ListSelected(ctx, campaign.ID)
	if err != nil {
		return nil, err
	}
	tagIDs := make([]int64, 0, len(selected))
	for position, tag := range selected {
		if tag == nil || tag.CampaignID != campaign.ID || tag.BundleID != *campaign.BundleID || tag.TagID == 0 || tag.SelectionOrder != position {
			return nil, repository.ErrSmartTargetingExecutionReservationUnavailable
		}
		tagIDs = append(tagIDs, int64(tag.TagID))
	}
	if len(tagIDs) == 0 {
		return nil, repository.ErrSmartTargetingExecutionReservationUnavailable
	}
	classes, err := normalizeSmartTargetingScoreClasses(campaign.Spec.AudienceGrades)
	if err != nil {
		return nil, err
	}
	allowedColors, err := smartTargetingAllowedColorsForCampaign(ctx, s.lineNumberRepo, campaign)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(repository.ExecutionReservationRequestSnapshot{TagIDs: tagIDs, ScoreClasses: classes, AllowedColors: allowedColors, Platform: strings.ToLower(strings.TrimSpace(campaign.Spec.Platform))})
	if err != nil {
		return nil, err
	}
	hash := smartTargetingInputHash(smartTargetingTagHash(campaign.ID, uint64ToUintSlice(tagIDs)), classes, campaign.Spec.Platform, allowedColors, repository.SmartTargetingSelectionPhaseExecution)
	return &smartTargetingExecutionCalculationInput{
		header: &models.CampaignTargetingExecutionReservationHeader{
			CampaignID: campaign.ID, BundleID: *campaign.BundleID, Phase: models.CampaignPhaseExecution,
			ReservationVersion: models.SmartTargetingExecutionReservationSchemaVersion, RequestedAudienceCount: int64(expected),
			CandidateGeneration:   models.SmartTargetingCapacityAlgorithmVersion,
			SelectionInputVersion: models.SmartTargetingExecutionReservationSelectionInputVersion, SelectionInputHash: hash,
			AllocationFingerprintVersion: models.SmartTargetingExecutionReservationAllocationFingerprintVersion,
			AllocationFingerprint:        emptySmartTargetingAllocationFingerprint(), RequestSnapshot: snapshot,
		},
		query: repository.SmartTargetingAudienceQuery{BundleID: *campaign.BundleID, Phase: repository.SmartTargetingSelectionPhaseExecution, ExcludeActiveExecutionReservationCampaignID: campaign.ID, TagIDs: tagIDs, ScoreClasses: classes, AllowedColors: allowedColors},
	}, nil
}

func (s *CampaignFlowImpl) StartSmartTargetingExecutionCalculation(ctx context.Context, req *dto.SmartTargetingExecutionCalculationRequest, metadata *ClientMetadata) (*dto.SmartTargetingExecutionCalculationResponse, error) {
	if req == nil {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_INVALID", "Execution audience calculation request is invalid", ErrCampaignNotFound)
	}
	campaign, err := getCampaign(ctx, s.campaignRepo, req.CampaignUUID, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	if !campaign.IsEditable() || !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseExecution {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_INVALID", "An editable Smart Targeting Execution campaign is required", ErrCampaignUpdateNotAllowed)
	}
	// This only calculates price/count; it does not select audience members.
	cost, err := s.CalculateCampaignCost(ctx, &dto.CalculateCampaignCostRequest{CampaignID: campaign.ID, CustomerID: campaign.CustomerID}, metadata)
	if err != nil {
		return nil, err
	}
	if cost.NumTargetAudience == 0 || cost.NumTargetAudience > math.MaxInt64 {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_INVALID", "Execution audience count is invalid", ErrInsufficientCampaignCapacity)
	}
	repo := repository.NewCampaignTargetingExecutionCalculationRepository(s.db)
	var result *models.CampaignTargetingExecutionCalculation
	var reused bool
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		txDB := smartTargetingDB(txCtx, s.db)
		var locked models.Campaign
		if err := txDB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, campaign.ID).Error; err != nil {
			return err
		}
		if !locked.IsEditable() || !locked.Spec.UsesSmartTargeting() || locked.Phase != models.CampaignPhaseExecution || locked.UpdatedAt == nil || campaign.UpdatedAt == nil || !locked.UpdatedAt.Equal(*campaign.UpdatedAt) {
			return errSmartTargetingExecutionCalculationStale
		}
		input, err := s.executionCalculationInput(txCtx, &locked, cost.NumTargetAudience)
		if err != nil {
			return err
		}
		if err := s.selectedTagRepo.Validate(txCtx, locked.ID, *locked.BundleID); err != nil {
			if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
				return ErrSmartTargetingTagInvalid
			}
			return err
		}
		if active, err := repo.ActiveByCampaignID(txCtx, locked.ID); err != nil {
			return err
		} else if active != nil {
			if active.SelectionInputHash == input.header.SelectionInputHash && active.RequestedAudienceCount == input.header.RequestedAudienceCount {
				result = active
				reused = true
				return nil
			}
			// Completion is lease-guarded, so a displaced worker cannot publish.
			if err := repo.Supersede(txCtx, active.ID, utils.UTCNow()); err != nil {
				return err
			}
		}
		if ready, err := repo.ReadyByInput(txCtx, locked.ID, input.header.SelectionInputHash, input.header.RequestedAudienceCount); err != nil {
			return err
		} else if ready != nil {
			if matchErr := s.executionCalculationStillMatches(txCtx, &locked, ready, input.header.RequestedAudienceCount); matchErr != nil && !errors.Is(matchErr, errSmartTargetingExecutionCalculationStale) {
				return matchErr
			} else if matchErr != nil {
				// Do not reuse a structurally stale ready row even if its indexed
				// hash and requested count appear to match.
			} else if current, err := s.executionCalculationAllocationCurrent(txCtx, ready); err != nil {
				return err
			} else if current {
				result = ready
				reused = true
				return nil
			}
		}
		result = &models.CampaignTargetingExecutionCalculation{CampaignID: locked.ID, BundleID: *locked.BundleID, CustomerID: locked.CustomerID, RequestedAudienceCount: input.header.RequestedAudienceCount, SelectedTagIDs: pq.Int64Array(input.query.TagIDs), SelectedScoreClasses: pq.StringArray(input.query.ScoreClasses), SelectionInputHash: input.header.SelectionInputHash, RequestSnapshot: input.header.RequestSnapshot, AllocationFingerprint: emptySmartTargetingAllocationFingerprint(), CalculationVersion: models.SmartTargetingExecutionCalculationVersion, Status: models.CampaignTargetingExecutionCalculationPending, CreatedAt: utils.UTCNow()}
		return repo.Save(txCtx, result)
	})
	if err != nil {
		if errors.Is(err, errSmartTargetingExecutionCalculationStale) {
			return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_STALE", "Campaign changed while calculation was requested; retry", ErrInvalidState)
		}
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_REQUEST_FAILED", "Failed to request execution audience calculation", err)
	}
	return smartTargetingExecutionCalculationDTO(result, result.Status == models.CampaignTargetingExecutionCalculationReady, false, reused), nil
}

// GetCurrentSmartTargetingExecutionCalculation returns the calculation that
// matches the campaign's current effective input. It lets clients resume
// polling after losing a previously returned calculation ID.
func (s *CampaignFlowImpl) GetCurrentSmartTargetingExecutionCalculation(ctx context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingExecutionCalculationResponse, error) {
	campaign, err := s.ownedSmartTargetingExecutionCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	expected, err := s.currentSmartTargetingExecutionAudienceCount(ctx, campaign)
	if err != nil {
		return nil, err
	}
	input, err := s.executionCalculationInput(ctx, campaign, expected)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_LOOKUP_FAILED", "Failed to load execution audience calculation inputs", err)
	}
	if err := s.selectedTagRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_LOOKUP_FAILED", "Execution audience calculation inputs are invalid", ErrSmartTargetingTagInvalid)
		}
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_LOOKUP_FAILED", "Failed to validate execution audience calculation inputs", err)
	}

	repo := repository.NewCampaignTargetingExecutionCalculationRepository(s.db)
	calculation, err := repo.LatestByInput(ctx, campaign.ID, input.header.SelectionInputHash, input.header.RequestedAudienceCount)
	if err != nil {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_LOOKUP_FAILED", "Failed to load execution audience calculation", err)
	}
	if calculation == nil {
		calculation, err = repo.LatestByCampaignID(ctx, campaign.ID)
		if err != nil {
			return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_LOOKUP_FAILED", "Failed to load execution audience calculation", err)
		}
		if calculation == nil {
			return &dto.SmartTargetingExecutionCalculationResponse{
				CampaignID: campaign.ID, BundleID: *campaign.BundleID, RequestedAudienceCount: expected,
				Status: "not_calculated", RecalculationRequired: true,
			}, nil
		}
		return smartTargetingExecutionCalculationDTO(calculation, false, true, false), nil
	}
	return s.smartTargetingExecutionCalculationStatusDTO(ctx, campaign, calculation, expected)
}

func (s *CampaignFlowImpl) GetSmartTargetingExecutionCalculation(ctx context.Context, customerID uint, campaignUUID string, id int64) (*dto.SmartTargetingExecutionCalculationResponse, error) {
	campaign, err := s.ownedSmartTargetingExecutionCampaign(ctx, customerID, campaignUUID)
	if err != nil {
		return nil, err
	}
	calculation, err := repository.NewCampaignTargetingExecutionCalculationRepository(s.db).ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if calculation == nil || calculation.CampaignID != campaign.ID {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_NOT_FOUND", "Execution audience calculation not found", ErrCampaignNotFound)
	}
	if calculation.Status != models.CampaignTargetingExecutionCalculationReady || !campaign.IsEditable() {
		return smartTargetingExecutionCalculationDTO(calculation, false, calculation.Status == models.CampaignTargetingExecutionCalculationStale, false), nil
	}
	expected, err := s.currentSmartTargetingExecutionAudienceCount(ctx, campaign)
	if err != nil {
		return nil, err
	}
	return s.smartTargetingExecutionCalculationStatusDTO(ctx, campaign, calculation, expected)
}

func (s *CampaignFlowImpl) ownedSmartTargetingExecutionCampaign(ctx context.Context, customerID uint, campaignUUID string) (*models.Campaign, error) {
	if customerID == 0 || strings.TrimSpace(campaignUUID) == "" {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_INVALID", "Invalid execution audience calculation request", ErrCampaignNotFound)
	}
	campaign, err := getCampaign(ctx, s.campaignRepo, campaignUUID, customerID)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_LOOKUP_FAILED", "Failed to lookup campaign", err)
	}
	if !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseExecution {
		return nil, NewBusinessError("SMART_TARGETING_EXECUTION_CALCULATION_INVALID", "Smart Targeting Execution campaign is required", ErrInvalidState)
	}
	if campaign.BundleID == nil || *campaign.BundleID == 0 {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Campaign bundle not found", ErrBundleNotFound)
	}
	return &campaign, nil
}

func (s *CampaignFlowImpl) currentSmartTargetingExecutionAudienceCount(ctx context.Context, campaign *models.Campaign) (uint64, error) {
	if campaign == nil {
		return 0, ErrCampaignNotFound
	}
	cost, err := s.CalculateCampaignCost(ctx, &dto.CalculateCampaignCostRequest{CampaignID: campaign.ID, CustomerID: campaign.CustomerID}, nil)
	if err != nil {
		return 0, err
	}
	if cost.NumTargetAudience == 0 || cost.NumTargetAudience > math.MaxInt64 {
		return 0, ErrInsufficientCampaignCapacity
	}
	return cost.NumTargetAudience, nil
}

func (s *CampaignFlowImpl) smartTargetingExecutionCalculationStatusDTO(ctx context.Context, campaign *models.Campaign, calculation *models.CampaignTargetingExecutionCalculation, expected uint64) (*dto.SmartTargetingExecutionCalculationResponse, error) {
	if calculation == nil {
		return nil, nil
	}
	if calculation.Status != models.CampaignTargetingExecutionCalculationReady {
		return smartTargetingExecutionCalculationDTO(calculation, false, calculation.Status == models.CampaignTargetingExecutionCalculationFailed || calculation.Status == models.CampaignTargetingExecutionCalculationStale, false), nil
	}
	matchErr := s.executionCalculationStillMatches(ctx, campaign, calculation, int64(expected))
	if matchErr != nil && !errors.Is(matchErr, errSmartTargetingExecutionCalculationStale) {
		return nil, matchErr
	}
	current := matchErr == nil
	if current {
		var err error
		current, err = s.executionCalculationAllocationCurrent(ctx, calculation)
		if err != nil {
			return nil, err
		}
	}
	return smartTargetingExecutionCalculationDTO(calculation, current, !current, false), nil
}

func (s *CampaignFlowImpl) executionCalculationAllocationCurrent(ctx context.Context, calculation *models.CampaignTargetingExecutionCalculation) (bool, error) {
	if calculation == nil || calculation.Status != models.CampaignTargetingExecutionCalculationReady || len(calculation.AllocationFingerprint) != 64 {
		return false, nil
	}
	fingerprint, err := smartTargetingBundleAllocationFingerprint(ctx, s.db, calculation.BundleID, calculation.CampaignID)
	if err != nil {
		return false, err
	}
	return fingerprint == calculation.AllocationFingerprint, nil
}

func smartTargetingExecutionCalculationDTO(c *models.CampaignTargetingExecutionCalculation, current, recalc, reused bool) *dto.SmartTargetingExecutionCalculationResponse {
	if c == nil {
		return nil
	}
	return &dto.SmartTargetingExecutionCalculationResponse{CalculationID: c.ID, CampaignID: c.CampaignID, BundleID: c.BundleID, RequestedAudienceCount: uint64(c.RequestedAudienceCount), Status: string(c.Status), IsCurrent: current, RecalculationRequired: recalc, Reused: reused, CreatedAt: c.CreatedAt, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, ErrorCode: c.ErrorCode, ErrorMessage: c.ErrorMessage}
}

func (s *CampaignFlowImpl) executionCalculationStillMatches(ctx context.Context, campaign *models.Campaign, calculation *models.CampaignTargetingExecutionCalculation, expected int64) error {
	if campaign == nil || calculation == nil || !campaign.IsEditable() || !campaign.Spec.UsesSmartTargeting() || campaign.Phase != models.CampaignPhaseExecution || campaign.BundleID == nil || *campaign.BundleID != calculation.BundleID || calculation.CalculationVersion != models.SmartTargetingExecutionCalculationVersion {
		return errSmartTargetingExecutionCalculationStale
	}
	if expected <= 0 || calculation.RequestedAudienceCount != expected {
		return errSmartTargetingExecutionCalculationStale
	}
	input, err := s.executionCalculationInput(ctx, campaign, uint64(expected))
	if err != nil {
		return err
	}
	if input.header.SelectionInputHash != calculation.SelectionInputHash || string(input.header.RequestSnapshot) != string(calculation.RequestSnapshot) {
		return errSmartTargetingExecutionCalculationStale
	}
	if err := s.selectedTagRepo.Validate(ctx, campaign.ID, *campaign.BundleID); err != nil {
		if errors.Is(err, repository.ErrInvalidCampaignSelectedTags) {
			return errSmartTargetingExecutionCalculationStale
		}
		return err
	}
	return nil
}

func (s *CampaignFlowImpl) ExecuteSmartTargetingExecutionCalculation(ctx context.Context, calculationID int64, leaseStartedAt time.Time) (err error) {
	repo := repository.NewCampaignTargetingExecutionCalculationRepository(s.db)
	calculation, err := repo.ByID(ctx, calculationID)
	if err != nil {
		return err
	}
	if calculation == nil || calculation.Status != models.CampaignTargetingExecutionCalculationPending || calculation.StartedAt == nil || !calculation.StartedAt.Equal(leaseStartedAt) {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("execution audience calculation worker panicked: %v", recovered)
		}
		if err != nil {
			failCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = repo.Finish(failCtx, calculationID, leaseStartedAt, models.CampaignTargetingExecutionCalculationFailed, "EXECUTION_AUDIENCE_CALCULATION_FAILED", "Execution audience calculation could not be completed", utils.UTCNow())
		}
	}()
	campaign, err := s.campaignRepo.ByID(ctx, calculation.CampaignID)
	if err != nil {
		return err
	}
	if matchErr := s.executionCalculationStillMatches(ctx, campaign, calculation, calculation.RequestedAudienceCount); matchErr != nil {
		if errors.Is(matchErr, errSmartTargetingExecutionCalculationStale) {
			return repo.Finish(ctx, calculationID, leaseStartedAt, models.CampaignTargetingExecutionCalculationStale, "CAMPAIGN_CHANGED", "Campaign inputs changed before selection completed", utils.UTCNow())
		}
		return matchErr
	}
	var snapshot repository.ExecutionReservationRequestSnapshot
	if err := json.Unmarshal(calculation.RequestSnapshot, &snapshot); err != nil {
		return err
	}
	allocationFingerprint, err := smartTargetingBundleAllocationFingerprint(ctx, s.db, calculation.BundleID, calculation.CampaignID)
	if err != nil {
		return err
	}
	rows, err := repository.NewSmartTargetingAudienceRepository(s.db).SelectCandidates(ctx, repository.SmartTargetingAudienceQuery{BundleID: calculation.BundleID, Phase: repository.SmartTargetingSelectionPhaseExecution, ExcludeActiveExecutionReservationCampaignID: calculation.CampaignID, TagIDs: snapshot.TagIDs, ScoreClasses: snapshot.ScoreClasses, AllowedColors: snapshot.AllowedColors}, calculation.RequestedAudienceCount)
	if err != nil {
		return err
	}
	if int64(len(rows)) != calculation.RequestedAudienceCount {
		return repo.Finish(ctx, calculationID, leaseStartedAt, models.CampaignTargetingExecutionCalculationFailed, "INSUFFICIENT_CAPACITY", "The requested execution audience is no longer available", utils.UTCNow())
	}
	members := make([]models.CampaignTargetingExecutionCalculationMember, 0, len(rows))
	for i, row := range rows {
		if row == nil || row.ID <= 0 {
			return fmt.Errorf("invalid candidate")
		}
		tag := firstMatchingSmartTargetingTag(row.Tags, snapshot.TagIDs)
		if tag == 0 {
			return fmt.Errorf("candidate lost assigned tag")
		}
		members = append(members, models.CampaignTargetingExecutionCalculationMember{AudienceID: row.ID, AssignedTagID: tag, SelectionOrder: int64(i), AudienceScore: row.NormalizedScore})
	}
	err = repository.WithTransaction(ctx, s.db, func(txCtx context.Context) error {
		txDB := smartTargetingDB(txCtx, s.db)
		var locked models.Campaign
		if err := txDB.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, calculation.CampaignID).Error; err != nil {
			return err
		}
		if err := s.executionCalculationStillMatches(txCtx, &locked, calculation, calculation.RequestedAudienceCount); err != nil {
			if errors.Is(err, errSmartTargetingExecutionCalculationStale) {
				return errSmartTargetingExecutionCalculationStale
			}
			return err
		}
		if err := repository.LockBundleForShare(txCtx, calculation.BundleID); err != nil {
			return err
		}
		fingerprint, err := smartTargetingBundleAllocationFingerprint(txCtx, s.db, calculation.BundleID, calculation.CampaignID)
		if err != nil {
			return err
		}
		if fingerprint != allocationFingerprint {
			return errSmartTargetingExecutionCalculationStale
		}
		return repo.Complete(txCtx, calculationID, leaseStartedAt, fingerprint, members, utils.UTCNow())
	})
	if errors.Is(err, errSmartTargetingExecutionCalculationStale) {
		return repo.Finish(ctx, calculationID, leaseStartedAt, models.CampaignTargetingExecutionCalculationStale, "ALLOCATION_CHANGED", "Bundle availability changed during selection", utils.UTCNow())
	}
	return err
}

func (s *CampaignFlowImpl) executionReservationPlanFromCalculation(ctx context.Context, campaign *models.Campaign, calculationID int64, expected uint64) (*smartTargetingExecutionReservationPlan, error) {
	if calculationID <= 0 || expected == 0 || expected > math.MaxInt64 {
		return nil, ErrSmartTargetingExecutionCalculationRequired
	}
	repo := repository.NewCampaignTargetingExecutionCalculationRepository(s.db)
	calculation, err := repo.ReadyForUpdate(ctx, calculationID)
	if err != nil {
		return nil, err
	}
	if calculation == nil || calculation.CampaignID != campaign.ID || calculation.RequestedAudienceCount != int64(expected) {
		return nil, ErrSmartTargetingExecutionCalculationRequired
	}
	if err := s.executionCalculationStillMatches(ctx, campaign, calculation, int64(expected)); err != nil {
		if errors.Is(err, errSmartTargetingExecutionCalculationStale) {
			return nil, ErrSmartTargetingExecutionCalculationStale
		}
		return nil, err
	}
	header := &models.CampaignTargetingExecutionReservationHeader{CampaignID: campaign.ID, BundleID: calculation.BundleID, Phase: models.CampaignPhaseExecution, ReservationVersion: models.SmartTargetingExecutionReservationSchemaVersion, RequestedAudienceCount: calculation.RequestedAudienceCount, CandidateGeneration: models.SmartTargetingCapacityAlgorithmVersion, SelectionInputVersion: models.SmartTargetingExecutionReservationSelectionInputVersion, SelectionInputHash: calculation.SelectionInputHash, AllocationFingerprintVersion: models.SmartTargetingExecutionReservationAllocationFingerprintVersion, AllocationFingerprint: calculation.AllocationFingerprint, RequestSnapshot: calculation.RequestSnapshot}
	return &smartTargetingExecutionReservationPlan{header: header}, nil
}
