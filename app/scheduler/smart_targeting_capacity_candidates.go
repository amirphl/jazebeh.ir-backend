package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"gorm.io/gorm"
)

// selectAndReserveExactSmartTargetingCandidates selects execution campaigns
// after a scheduler claim. Test campaigns materialize their already persisted
// snapshot; only execution-phase campaigns select new candidate rows here.
func selectAndReserveExactSmartTargetingCandidates(
	ctx context.Context,
	db *gorm.DB,
	campaign dto.BotGetCampaignResponse,
	requested int64,
	correlationID string,
	allowedColors []string,
) ([]string, []int64, []string, uint, error) {
	if db == nil || campaign.BundleID == nil || *campaign.BundleID == 0 || requested <= 0 {
		return nil, nil, nil, 0, fmt.Errorf("current exact Smart Targeting capacity is unavailable for campaign %d", campaign.ID)
	}

	var phones []string
	var ids []int64
	var uids []string
	var selectionID uint
	isTest := isSmartTargetingTestCampaign(campaign)
	err := repository.WithTransaction(ctx, db, func(txCtx context.Context) error {
		txDB, ok := txCtx.Value(repository.TxContextKey).(*gorm.DB)
		if !ok || txDB == nil {
			return fmt.Errorf("transaction is unavailable for campaign %d", campaign.ID)
		}
		if err := repository.LockBundleForUpdate(txCtx, *campaign.BundleID); err != nil {
			return err
		}

		bundleSelectionRepo := repository.NewBundleAudienceSelectionRepository(txDB)
		existing, err := bundleSelectionRepo.ByCampaignID(txCtx, campaign.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			phones, ids, uids, err = loadReservedBundleAudience(txCtx, repository.NewAudienceProfileRepository(txDB), []int64(existing.SelectedAudienceIDs))
			if err != nil {
				return err
			}
			if !isTest {
				if err := requireExactAudienceCount(campaign.ID, requested, len(ids)); err != nil {
					return err
				}
			}
			selectionID = existing.ID
			return nil
		}
		if isTest {
			if campaign.SmartTargetingTestSelectionID == nil || *campaign.SmartTargetingTestSelectionID <= 0 {
				return fmt.Errorf("campaign id=%d has no persisted Smart Targeting Test audience selection", campaign.ID)
			}
			testSelectionRepo := repository.NewCampaignTargetingTestSampleSelectionRepository(txDB)
			snapshot, err := testSelectionRepo.ActiveReservedForCampaign(txCtx, campaign.ID, *campaign.SmartTargetingTestSelectionID)
			if err != nil {
				return err
			}
			if int64(len(snapshot.Members)) != requested {
				return fmt.Errorf("persisted Smart Targeting Test audience selection count mismatch for campaign %d", campaign.ID)
			}
			persistedIDs := make([]int64, 0, len(snapshot.Members))
			for position, member := range snapshot.Members {
				if member.AudienceID <= 0 || member.AssignedTagID == 0 || member.SelectionOrder != int64(position) {
					return fmt.Errorf("persisted Smart Targeting Test audience selection is invalid for campaign %d", campaign.ID)
				}
				persistedIDs = append(persistedIDs, member.AudienceID)
			}
			selection, err := bundleSelectionRepo.InsertForCampaign(txCtx, campaign.CustomerID, *campaign.BundleID, campaign.ID, correlationID, persistedIDs)
			if err != nil {
				return err
			}
			phones, ids, uids, err = loadReservedBundleAudience(txCtx, repository.NewAudienceProfileRepository(txDB), persistedIDs)
			if err != nil {
				return err
			}
			attributions := make([]models.CampaignAudienceTagAttribution, 0, len(snapshot.Members))
			for position, member := range snapshot.Members {
				attributions = append(attributions, models.CampaignAudienceTagAttribution{
					CampaignID: campaign.ID, BundleID: *campaign.BundleID, BundleAudienceSelectionID: selection.ID,
					AudienceID: member.AudienceID, AssignedTagID: member.AssignedTagID, PhaseType: models.CampaignPhaseTest,
					SelectionMethod: "random_per_tag", SelectionOrder: int64(position), AudienceScore: member.AudienceScore,
					CreatedAt: time.Now().UTC(),
				})
			}
			if err := txDB.WithContext(txCtx).CreateInBatches(&attributions, 1000).Error; err != nil {
				return err
			}
			if err := testSelectionRepo.Materialize(txCtx, campaign.ID, snapshot.ID, int64(len(snapshot.Members))); err != nil {
				return err
			}
			selectionID = selection.ID
			return nil
		}

		classes, err := normalizeSchedulerScoreClasses(campaign.AudienceGrades)
		if err != nil {
			return err
		}
		_, rawTagIDs, err := parseCampaignTagIDs(campaign)
		if err != nil {
			return err
		}
		tagIDs := make([]int64, len(rawTagIDs))
		for i, id := range rawTagIDs {
			tagIDs[i] = int64(id)
		}
		testSamplingTagIDs := tagIDs
		if isTest {
			testSamplingTagIDs, err = smartTargetingTestSamplingTagIDs(campaign, rawTagIDs)
			if err != nil {
				return err
			}
		}
		capacityTagIDs := append([]int64(nil), tagIDs...)
		sort.Slice(capacityTagIDs, func(i, j int) bool { return capacityTagIDs[i] < capacityTagIDs[j] })
		calculationRepo := repository.NewCampaignTargetingCapacityRepository(txDB)
		platform := strings.ToLower(strings.TrimSpace(campaign.Platform))
		applyBundleAudienceExclusions := isSmartTargetingTestCampaign(campaign)
		calculation, err := calculationRepo.CurrentForExecution(
			txCtx,
			campaign.ID,
			*campaign.BundleID,
			platform,
			applyBundleAudienceExclusions,
			capacityTagIDs,
			classes,
			allowedColors,
			models.SmartTargetingCapacityAlgorithmVersion,
			time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if calculation == nil || (!isTest && calculation.UsableUniqueAudienceCount < requested) {
			return fmt.Errorf("current exact Smart Targeting capacity is unavailable for campaign %d", campaign.ID)
		}

		// fingerprint, err := schedulerApprovedAllocationFingerprint(txCtx, txDB, calculation.BundleID, campaign.ID)
		// if err != nil {
		// 	return err
		// }
		// if fingerprint != calculation.AllocationFingerprint {
		// 	return fmt.Errorf("exact Smart Targeting capacity is stale for campaign %d", campaign.ID)
		// }

		// The approval fingerprint is deliberately not an execution precondition.
		// Other campaigns naturally change from approved to running/materialized
		// after this campaign was approved, which changes that fingerprint without
		// invalidating this campaign's reservation. Under the Bundle lock, the
		// candidate query below is the authoritative current population and the
		// exact-count check fails safely if capacity has genuinely disappeared.
		audienceRepo := repository.NewSmartTargetingAudienceRepository(txDB)
		query := smartTargetingSchedulerAudienceQuery(campaign, *campaign.BundleID, tagIDs, classes, allowedColors)
		var rows []*models.AudienceProfile
		assignedTags := make([]uint, 0)
		selectionMethod := "score_desc"
		if isTest {
			if campaign.SampleSizePerTag == nil || *campaign.SampleSizePerTag == 0 || *campaign.SampleSizePerTag > math.MaxInt64 {
				return fmt.Errorf("Smart Targeting Test campaign %d has invalid sample_size_per_tag", campaign.ID)
			}
			// Test candidates are read independently for each satisfied tag without
			// explicit ordering; score classes only restrict eligibility. Keep the
			// legacy persisted label for compatibility with the existing constraint.
			selectionMethod = "random_per_tag"
			bounds, boundsErr := audienceRepo.CalculateScoreBounds(txCtx, query)
			if boundsErr != nil {
				return boundsErr
			}
			excluded := make([]int64, 0)
			for _, tagID := range testSamplingTagIDs {
				tagRows, sampleErr := audienceRepo.SelectForTag(txCtx, query, bounds, tagID, excluded, int64(*campaign.SampleSizePerTag))
				if sampleErr != nil {
					return sampleErr
				}
				if uint64(len(tagRows)) != *campaign.SampleSizePerTag {
					continue
				}
				for _, row := range tagRows {
					rows = append(rows, row)
					assignedTags = append(assignedTags, uint(tagID))
					excluded = append(excluded, row.ID)
				}
			}
		} else {
			rows, err = audienceRepo.SelectCandidates(txCtx, query, requested)
			if err != nil {
				return err
			}
			// Attribute after score-based selection so tag order cannot affect the
			// audience priority. tagIDs retains persisted selection order.
			assignedTags = assignFirstMatchingTags(rows, tagIDs)
		}
		phones, ids, uids = audienceProfileRows(rows)
		if !isTest {
			if err := requireExactAudienceCount(campaign.ID, requested, len(ids)); err != nil {
				return err
			}
		}
		selection, err := bundleSelectionRepo.InsertForCampaign(txCtx, campaign.CustomerID, *campaign.BundleID, campaign.ID, correlationID, ids)
		if err != nil {
			return err
		}
		if selection == nil || selection.ID == 0 {
			return fmt.Errorf("bundle audience selection was not persisted for campaign %d", campaign.ID)
		}
		selectionID = selection.ID
		if len(assignedTags) != len(rows) || len(rows) != len(ids) {
			return fmt.Errorf("Smart Targeting attribution mismatch for campaign %d", campaign.ID)
		}
		attributions := make([]models.CampaignAudienceTagAttribution, 0, len(rows))
		phase := models.CampaignPhaseExecution
		if isTest {
			phase = models.CampaignPhaseTest
		}
		for position, row := range rows {
			if assignedTags[position] == 0 {
				return fmt.Errorf("Smart Targeting audience %d has no selected-tag attribution", row.ID)
			}
			attributions = append(attributions, models.CampaignAudienceTagAttribution{
				CampaignID:                campaign.ID,
				BundleID:                  *campaign.BundleID,
				BundleAudienceSelectionID: selection.ID,
				AudienceID:                row.ID,
				AssignedTagID:             assignedTags[position],
				PhaseType:                 phase,
				SelectionMethod:           selectionMethod,
				SelectionOrder:            int64(position),
				AudienceScore:             row.NormalizedScore,
				CreatedAt:                 time.Now().UTC(),
			})
		}
		if len(attributions) > 0 {
			if err := txDB.WithContext(txCtx).CreateInBatches(&attributions, 1000).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, 0, err
	}
	return phones, ids, uids, selectionID, nil
}

func assignFirstMatchingTags(rows []*models.AudienceProfile, orderedTagIDs []int64) []uint {
	assigned := make([]uint, len(rows))
	for rowIndex, row := range rows {
		if row == nil {
			continue
		}
		for _, tagID := range orderedTagIDs {
			for _, audienceTagID := range row.Tags {
				if int64(audienceTagID) == tagID {
					assigned[rowIndex] = uint(tagID)
					break
				}
			}
			if assigned[rowIndex] != 0 {
				break
			}
		}
	}
	return assigned
}

func isSmartTargetingTestCampaign(campaign dto.BotGetCampaignResponse) bool {
	return usesSmartAudienceTargeting(campaign) && campaign.Phase != nil && strings.EqualFold(strings.TrimSpace(*campaign.Phase), string(models.CampaignPhaseTest))
}

func smartTargetingSchedulerAudienceQuery(campaign dto.BotGetCampaignResponse, bundleID uint, tagIDs []int64, classes, allowedColors []string) repository.SmartTargetingAudienceQuery {
	query := repository.SmartTargetingAudienceQuery{
		BundleID:      bundleID,
		TagIDs:        tagIDs,
		ScoreClasses:  classes,
		AllowedColors: allowedColors,
	}
	if isSmartTargetingTestCampaign(campaign) {
		query.ApplyBundleAudienceExclusions = true
	}
	return query
}

func smartTargetingTestSamplingTagIDs(
	campaign dto.BotGetCampaignResponse,
	selectedTagIDs []uint,
) ([]int64, error) {
	if !isSmartTargetingTestCampaign(campaign) ||
		len(campaign.SmartTargetingTestSatisfiedTagIDs) == 0 {
		return nil, fmt.Errorf(
			"campaign id=%d has no persisted Smart Targeting Test sampling intent",
			campaign.ID,
		)
	}

	// Validate selectedTagIDs and retain the persisted selection position. Test
	// sampling intent must be an ordered subsequence of that durable order.
	selectedPosition := make(map[uint]int, len(selectedTagIDs))

	for position, tagID := range selectedTagIDs {
		if tagID == 0 {
			return nil, fmt.Errorf(
				"campaign id=%d has invalid selected tag ID=0",
				campaign.ID,
			)
		}

		if _, exists := selectedPosition[tagID]; exists {
			return nil, fmt.Errorf(
				"campaign id=%d has duplicate selected tag ID=%d",
				campaign.ID,
				tagID,
			)
		}

		selectedPosition[tagID] = position
	}

	// Validate satisfied tags while preserving their original order.
	satisfiedSet := make(
		map[uint]struct{},
		len(campaign.SmartTargetingTestSatisfiedTagIDs),
	)

	result := make(
		[]int64,
		0,
		len(campaign.SmartTargetingTestSatisfiedTagIDs),
	)
	lastSelectedPosition := -1

	for _, tagID := range campaign.SmartTargetingTestSatisfiedTagIDs {
		if tagID == 0 {
			return nil, fmt.Errorf(
				"campaign id=%d has invalid satisfied tag ID=0",
				campaign.ID,
			)
		}

		if _, exists := satisfiedSet[tagID]; exists {
			return nil, fmt.Errorf(
				"campaign id=%d has duplicate satisfied tag ID=%d",
				campaign.ID,
				tagID,
			)
		}

		position, exists := selectedPosition[tagID]
		if !exists {
			return nil, fmt.Errorf(
				"campaign id=%d satisfied tag ID=%d is not in selected tags",
				campaign.ID,
				tagID,
			)
		}
		if position <= lastSelectedPosition {
			return nil, fmt.Errorf(
				"campaign id=%d satisfied tag ID=%d is out of persisted selection order",
				campaign.ID,
				tagID,
			)
		}

		satisfiedSet[tagID] = struct{}{}
		lastSelectedPosition = position

		// The validation above guarantees this is also persisted selection order.
		result = append(result, int64(tagID))
	}

	return result, nil
}

func schedulerConfiguredAudienceCount(campaign dto.BotGetCampaignResponse) (int64, error) {
	if isSmartTargetingTestCampaign(campaign) {
		if campaign.SampleSizePerTag == nil || *campaign.SampleSizePerTag == 0 || *campaign.SampleSizePerTag > math.MaxInt64 {
			return 0, fmt.Errorf("Smart Targeting Test campaign %d has invalid sample_size_per_tag", campaign.ID)
		}
		if len(campaign.SmartTargetingTestSatisfiedTagIDs) == 0 || uint64(len(campaign.SmartTargetingTestSatisfiedTagIDs)) > uint64(math.MaxInt64) / *campaign.SampleSizePerTag {
			return 0, fmt.Errorf("Smart Targeting Test campaign %d has invalid satisfied-tag intent", campaign.ID)
		}
		return int64(uint64(len(campaign.SmartTargetingTestSatisfiedTagIDs)) * *campaign.SampleSizePerTag), nil
	}
	if campaign.NumAudiences == nil || *campaign.NumAudiences == 0 || *campaign.NumAudiences > math.MaxInt64 {
		return 0, fmt.Errorf("campaign id=%d has no valid audience count", campaign.ID)
	}
	return int64(*campaign.NumAudiences), nil
}

func validateSchedulerSelectedAudienceCount(campaign dto.BotGetCampaignResponse, intended int64, selected int) error {
	if !isSmartTargetingTestCampaign(campaign) {
		return requireExactAudienceCount(campaign.ID, intended, selected)
	}
	if campaign.SampleSizePerTag == nil || *campaign.SampleSizePerTag == 0 {
		return fmt.Errorf("Smart Targeting Test campaign %d has invalid sample_size_per_tag", campaign.ID)
	}
	if int64(selected) != intended || uint64(selected)%*campaign.SampleSizePerTag != 0 {
		return fmt.Errorf("Smart Targeting Test campaign %d prepared an invalid audience count: intended=%d selected=%d", campaign.ID, intended, selected)
	}
	return nil
}

func audienceProfileRows(rows []*models.AudienceProfile) ([]string, []int64, []string) {
	phones := make([]string, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	uids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.PhoneNumber == nil || strings.TrimSpace(*row.PhoneNumber) == "" {
			continue
		}
		phones = append(phones, strings.TrimSpace(*row.PhoneNumber))
		ids = append(ids, int64(row.ID))
		uids = append(uids, row.UID)
	}
	return phones, ids, uids
}

func normalizeSchedulerScoreClasses(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{"A", "B", "C"}, nil
	}
	seen := make(map[string]struct{}, len(input))
	for _, raw := range input {
		class := strings.ToUpper(strings.TrimSpace(raw))
		if class != "A" && class != "B" && class != "C" {
			return nil, fmt.Errorf("invalid audience score class for campaign execution")
		}
		if _, exists := seen[class]; exists {
			return nil, fmt.Errorf("duplicate audience score class for campaign execution")
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

// func schedulerApprovedAllocationFingerprint(ctx context.Context, db *gorm.DB, bundleID, currentCampaignID uint) (string, error) {
// 	rows, err := repository.ListBundleCampaignAllocations(ctx, db, bundleID, currentCampaignID)
// 	if err != nil {
// 		return "", err
// 	}
// 	parts := make([]string, 0, len(rows))
// 	for _, row := range rows {
// 		if row.NumAudience == nil {
// 			return "", fmt.Errorf("reserved campaign %d has no audience allocation", row.CampaignID)
// 		}
// 		amount := *row.NumAudience
// 		if amount > uint64(math.MaxInt64) {
// 			return "", fmt.Errorf("approved campaign audience allocation overflows bigint")
// 		}
// 		parts = append(parts, strconv.FormatUint(uint64(row.CampaignID), 10)+":"+strconv.FormatUint(amount, 10)+":"+string(row.Status)+":"+strconv.FormatBool(row.Materialized))
// 	}
// 	fingerprintInput := "v3|bundle=" + strconv.FormatUint(uint64(bundleID), 10) + "|allocations=" + strings.Join(parts, ",")
// 	sum := sha256.Sum256([]byte(fingerprintInput))
// 	return hex.EncodeToString(sum[:]), nil
// }
