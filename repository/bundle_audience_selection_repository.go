package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

type BundleAudienceSelectionRepository interface {
	ByCampaignID(ctx context.Context, campaignID uint) (*models.BundleAudienceSelection, error)
	InsertForCampaign(
		ctx context.Context,
		customerID, bundleID, campaignID uint,
		correlationID string,
		ids []int64,
	) (*models.BundleAudienceSelection, error)
}

type BundleAudienceSelectionRepositoryImpl struct {
	DB *gorm.DB
}

func NewBundleAudienceSelectionRepository(db *gorm.DB) BundleAudienceSelectionRepository {
	return &BundleAudienceSelectionRepositoryImpl{DB: db}
}

func (r *BundleAudienceSelectionRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.DB.WithContext(ctx)
}

func (r *BundleAudienceSelectionRepositoryImpl) ByCampaignID(
	ctx context.Context,
	campaignID uint,
) (*models.BundleAudienceSelection, error) {
	return loadBundleAudienceSelectionByCampaign(r.getDB(ctx), campaignID)
}

func loadBundleAudienceSelectionByCampaign(db *gorm.DB, campaignID uint) (*models.BundleAudienceSelection, error) {
	var row models.BundleAudienceSelection
	err := db.Where("campaign_id = ?", campaignID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := db.Model(&models.BundleAudienceSelectionMember{}).
		Where("selection_id = ?", row.ID).
		Order("selection_order ASC").
		Pluck("audience_id", &ids).Error; err != nil {
		return nil, err
	}
	row.SelectedAudienceIDs = ids
	return &row, nil
}

// InsertForCampaign appends one immutable allocation and its normalized
// members. The caller may already hold the bundle lock; otherwise this method
// takes it. A retry returns the original campaign allocation unchanged.
func (r *BundleAudienceSelectionRepositoryImpl) InsertForCampaign(
	ctx context.Context,
	customerID, bundleID, campaignID uint,
	correlationID string,
	ids []int64,
) (*models.BundleAudienceSelection, error) {
	db := r.getDB(ctx)
	var inserted models.BundleAudienceSelection

	persist := func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT id FROM bundles WHERE id = ? FOR UPDATE", bundleID).Error; err != nil {
			return err
		}
		existing, err := loadBundleAudienceSelectionByCampaign(tx, campaignID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.CustomerID != customerID || existing.BundleID != bundleID ||
				existing.AudienceCount != int64(len(existing.SelectedAudienceIDs)) {
				return errors.New("persisted bundle audience allocation does not match the campaign scope")
			}
			inserted = *existing
			return nil
		}
		normalized := append([]int64(nil), ids...)
		seen := make(map[int64]struct{}, len(normalized))
		for _, id := range normalized {
			if id <= 0 {
				return errors.New("bundle audience allocation contains an invalid audience ID")
			}
			if _, exists := seen[id]; exists {
				return errors.New("bundle audience allocation contains duplicate audience IDs")
			}
			seen[id] = struct{}{}
		}

		row := models.BundleAudienceSelection{
			CustomerID: customerID, BundleID: bundleID, CampaignID: &campaignID,
			CorrelationID: correlationID, AudienceCount: int64(len(normalized)), CreatedAt: utils.UTCNow(),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		members := make([]models.BundleAudienceSelectionMember, 0, len(normalized))
		for position, audienceID := range normalized {
			members = append(members, models.BundleAudienceSelectionMember{
				SelectionID: row.ID, BundleID: bundleID, AudienceID: audienceID, SelectionOrder: int64(position), CreatedAt: row.CreatedAt,
			})
		}
		if len(members) > 0 {
			if err := tx.CreateInBatches(&members, 1000).Error; err != nil {
				return err
			}
		}
		row.SelectedAudienceIDs = normalized
		inserted = row
		return nil
	}
	var err error
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		// The caller owns the transaction and may already hold the Bundle lock.
		// Persist directly so selection and merge remain one atomic operation.
		err = persist(db)
	} else {
		err = db.Transaction(persist)
	}
	if err != nil {
		return nil, err
	}
	return &inserted, nil
}
