package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSmartTargetingTestSelectionUnavailable = errors.New("smart targeting test sample selection is unavailable")
	ErrSmartTargetingTestSelectionConflict    = errors.New("smart targeting test sample selection conflicts with current bundle availability")
)

// CampaignTargetingTestSampleSelectionRepository owns immutable Test sampling
// output and its separate, releasable reservation lifecycle.
type CampaignTargetingTestSampleSelectionRepository interface {
	Save(ctx context.Context, selection *models.CampaignTargetingTestSampleSelection) error
	ByID(ctx context.Context, id int64) (*models.CampaignTargetingTestSampleSelection, error)
	ByCalculationID(ctx context.Context, calculationID int64) (*models.CampaignTargetingTestSampleSelection, error)
	ReserveForCampaign(ctx context.Context, campaign *models.Campaign) error
	ActiveReservedForCampaign(ctx context.Context, campaignID uint, selectionID int64) (*models.CampaignTargetingTestSampleSelection, error)
	Materialize(ctx context.Context, campaignID uint, selectionID int64, expectedMembers int64) error
	ReleaseForCampaign(ctx context.Context, campaignID uint) error
}

type CampaignTargetingTestSampleSelectionRepositoryImpl struct{ db *gorm.DB }

func NewCampaignTargetingTestSampleSelectionRepository(db *gorm.DB) CampaignTargetingTestSampleSelectionRepository {
	return &CampaignTargetingTestSampleSelectionRepositoryImpl{db: db}
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) Save(ctx context.Context, selection *models.CampaignTargetingTestSampleSelection) error {
	if selection == nil || selection.CalculationID <= 0 || selection.CampaignID == 0 || selection.BundleID == 0 || selection.Generation <= 0 ||
		selection.EffectiveAudienceCount < 0 || int64(len(selection.Members)) != selection.EffectiveAudienceCount {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	seen := make(map[int64]struct{}, len(selection.Members))
	for index := range selection.Members {
		member := &selection.Members[index]
		if member.AudienceID <= 0 || member.AssignedTagID == 0 || member.SelectionOrder != int64(index) || member.TagSelectionOrder < 0 {
			return ErrSmartTargetingTestSelectionUnavailable
		}
		if _, exists := seen[member.AudienceID]; exists {
			return ErrSmartTargetingTestSelectionUnavailable
		}
		seen[member.AudienceID] = struct{}{}
	}
	db := r.getDB(ctx)
	if err := db.Omit("Members").Create(selection).Error; err != nil {
		return err
	}
	for index := range selection.Members {
		selection.Members[index].SelectionID = selection.ID
		selection.Members[index].CreatedAt = selection.CreatedAt
	}
	if len(selection.Members) > 0 {
		if err := db.CreateInBatches(&selection.Members, 1000).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) ByID(ctx context.Context, id int64) (*models.CampaignTargetingTestSampleSelection, error) {
	if id <= 0 {
		return nil, nil
	}
	var selection models.CampaignTargetingTestSampleSelection
	if err := r.getDB(ctx).First(&selection, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.loadMembers(ctx, &selection); err != nil {
		return nil, err
	}
	return &selection, nil
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) ByCalculationID(ctx context.Context, calculationID int64) (*models.CampaignTargetingTestSampleSelection, error) {
	if calculationID <= 0 {
		return nil, nil
	}
	var selection models.CampaignTargetingTestSampleSelection
	if err := r.getDB(ctx).Where("calculation_id = ?", calculationID).First(&selection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.loadMembers(ctx, &selection); err != nil {
		return nil, err
	}
	return &selection, nil
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) loadMembers(ctx context.Context, selection *models.CampaignTargetingTestSampleSelection) error {
	if selection == nil {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	return r.getDB(ctx).Where("selection_id = ?", selection.ID).Order("selection_order ASC").Find(&selection.Members).Error
}

// ReserveForCampaign is called in the campaign-finalization transaction while
// the caller holds the Bundle UPDATE lock. It never turns preview history into
// a permanent bundle allocation; cancellation can release these rows.
func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) ReserveForCampaign(ctx context.Context, campaign *models.Campaign) error {
	if campaign == nil || campaign.ID == 0 || campaign.BundleID == nil || *campaign.BundleID == 0 || campaign.ActiveSmartTargetingTestSelectionID == nil {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	selection, err := r.ByID(ctx, *campaign.ActiveSmartTargetingTestSelectionID)
	if err != nil {
		return err
	}
	if selection == nil || selection.CampaignID != campaign.ID || selection.BundleID != *campaign.BundleID ||
		selection.Generation != campaign.SmartTargetingTestSamplingGeneration || len(selection.Members) == 0 || int64(len(selection.Members)) != selection.EffectiveAudienceCount {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	db := r.getDB(ctx)
	var activeCount int64
	if err := db.Model(&models.CampaignTargetingTestSampleReservation{}).
		Where("campaign_id = ? AND selection_id = ? AND state = 'active'", campaign.ID, selection.ID).
		Count(&activeCount).Error; err != nil {
		return err
	}
	if activeCount > 0 {
		if activeCount != int64(len(selection.Members)) {
			return ErrSmartTargetingTestSelectionUnavailable
		}
		return nil
	}

	// Check the immutable snapshot against the current hard-safety population.
	// Do not substitute candidates here: a collision requires a fresh sample.
	var unavailable int64
	query := `
SELECT COUNT(*)
FROM campaign_targeting_test_sample_selection_members AS member
LEFT JOIN audience_profiles AS audience ON audience.id = member.audience_id
LEFT JOIN bundle_audience_selection_members AS materialized
  ON materialized.bundle_id = ? AND materialized.audience_id = member.audience_id
LEFT JOIN campaign_targeting_test_sample_reservations AS reserved
  ON reserved.bundle_id = ? AND reserved.audience_id = member.audience_id
 AND reserved.state = 'active' AND reserved.campaign_id <> ?
LEFT JOIN bundle_audience_exclusions AS excluded
  ON excluded.bundle_id = ? AND excluded.audience_id = member.audience_id
WHERE member.selection_id = ?
  AND (audience.id IS NULL OR audience.phone_number IS NULL OR BTRIM(audience.phone_number) = ''
       OR materialized.id IS NOT NULL OR reserved.id IS NOT NULL OR excluded.id IS NOT NULL)`
	if err := db.Raw(query, selection.BundleID, selection.BundleID, campaign.ID, selection.BundleID, selection.ID).Scan(&unavailable).Error; err != nil {
		return err
	}
	if unavailable != 0 {
		return ErrSmartTargetingTestSelectionConflict
	}
	now := utils.UTCNow()
	rows := make([]models.CampaignTargetingTestSampleReservation, 0, len(selection.Members))
	for _, member := range selection.Members {
		rows = append(rows, models.CampaignTargetingTestSampleReservation{
			SelectionID: selection.ID, CampaignID: campaign.ID, BundleID: selection.BundleID,
			AudienceID: member.AudienceID, State: "active", CreatedAt: now,
		})
	}
	if err := db.CreateInBatches(&rows, 1000).Error; err != nil {
		return fmt.Errorf("reserve smart targeting test selection: %w", err)
	}
	return nil
}

func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) ActiveReservedForCampaign(ctx context.Context, campaignID uint, selectionID int64) (*models.CampaignTargetingTestSampleSelection, error) {
	if _, err := transactionForLock(ctx); err != nil {
		return nil, err
	}
	selection, err := r.ByID(ctx, selectionID)
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.CampaignID != campaignID || len(selection.Members) == 0 {
		return nil, ErrSmartTargetingTestSelectionUnavailable
	}
	activeReservations := make([]models.CampaignTargetingTestSampleReservation, 0, len(selection.Members))
	if err := activeReservationsForUpdateQuery(r.getDB(ctx), campaignID, selectionID).
		Find(&activeReservations).Error; err != nil {
		return nil, err
	}
	if len(activeReservations) != len(selection.Members) {
		return nil, ErrSmartTargetingTestSelectionUnavailable
	}
	membersByAudienceID := make(map[int64]struct{}, len(selection.Members))
	for _, member := range selection.Members {
		membersByAudienceID[member.AudienceID] = struct{}{}
	}
	for _, reservation := range activeReservations {
		if _, exists := membersByAudienceID[reservation.AudienceID]; !exists {
			return nil, ErrSmartTargetingTestSelectionUnavailable
		}
	}
	if int64(len(activeReservations)) != selection.EffectiveAudienceCount {
		return nil, ErrSmartTargetingTestSelectionUnavailable
	}
	return selection, nil
}

// Materialize atomically promotes every active reservation in a previously
// locked snapshot. A partial or already released transition must fail so its
// enclosing transaction rolls back the permanent bundle allocation.
func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) Materialize(ctx context.Context, campaignID uint, selectionID int64, expectedMembers int64) error {
	if expectedMembers <= 0 {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	if _, err := transactionForLock(ctx); err != nil {
		return err
	}
	now := utils.UTCNow()
	result := materializeActiveReservationsQuery(r.getDB(ctx), campaignID, selectionID).
		Updates(map[string]any{"state": "materialized", "materialized_at": now})
	if result.Error != nil {
		return result.Error
	}
	return requireExpectedReservationRows(expectedMembers, result.RowsAffected)
}

func activeReservationsForUpdateQuery(db *gorm.DB, campaignID uint, selectionID int64) *gorm.DB {
	return db.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("audience_id").
		Where("campaign_id = ? AND selection_id = ? AND state = 'active'", campaignID, selectionID)
}

func materializeActiveReservationsQuery(db *gorm.DB, campaignID uint, selectionID int64) *gorm.DB {
	return db.Model(&models.CampaignTargetingTestSampleReservation{}).
		Where("campaign_id = ? AND selection_id = ? AND state = 'active'", campaignID, selectionID)
}

func requireExpectedReservationRows(expectedMembers, affectedRows int64) error {
	if expectedMembers <= 0 || affectedRows != expectedMembers {
		return ErrSmartTargetingTestSelectionUnavailable
	}
	return nil
}

// ReleaseForCampaign is part of a terminal campaign status transition. It
// locks the campaign and then its Bundle so releasing a Test reservation is
// serialized with capacity scans (which hold a Bundle SHARE lock), approval,
// and audience materialization. Callers must make the status decision in the
// same transaction.
func (r *CampaignTargetingTestSampleSelectionRepositoryImpl) ReleaseForCampaign(ctx context.Context, campaignID uint) error {
	if campaignID == 0 {
		return nil
	}
	if err := LockCampaignForUpdate(ctx, campaignID); err != nil {
		return err
	}
	db := r.getDB(ctx)
	var bundleID uint
	if err := activeTestReservationBundleQuery(db, campaignID).Scan(&bundleID).Error; err != nil {
		return err
	}
	if bundleID != 0 {
		if err := LockBundleForUpdate(ctx, bundleID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	return db.Model(&models.CampaignTargetingTestSampleReservation{}).
		Where("campaign_id = ? AND state = 'active'", campaignID).
		Updates(map[string]any{"state": "released", "released_at": now}).Error
}

func activeTestReservationBundleQuery(db *gorm.DB, campaignID uint) *gorm.DB {
	return db.Model(&models.CampaignTargetingTestSampleReservation{}).
		Select("bundle_id").
		Where("campaign_id = ? AND state = 'active'", campaignID).
		Order("bundle_id ASC").
		Limit(1)
}
