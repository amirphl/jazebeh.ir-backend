package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// MultimediaAssetRepositoryImpl implements MultimediaAssetRepository interface.
type MultimediaAssetRepositoryImpl struct {
	*BaseRepository[models.MultimediaAsset, models.MultimediaAssetFilter]
}

// NewMultimediaAssetRepository creates a new multimedia asset repository.
func NewMultimediaAssetRepository(db *gorm.DB) MultimediaAssetRepository {
	return &MultimediaAssetRepositoryImpl{
		BaseRepository: NewBaseRepository[models.MultimediaAsset, models.MultimediaAssetFilter](db),
	}
}

// ByID retrieves a multimedia asset by its ID.
func (r *MultimediaAssetRepositoryImpl) ByID(ctx context.Context, id uint) (*models.MultimediaAsset, error) {
	db := r.getDB(ctx)
	var row models.MultimediaAsset
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ByUUID retrieves a multimedia asset by UUID.
func (r *MultimediaAssetRepositoryImpl) ByUUID(ctx context.Context, uuidStr string) (*models.MultimediaAsset, error) {
	parsed, err := utils.ParseUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	rows, err := r.ByFilter(ctx, models.MultimediaAssetFilter{UUID: &parsed}, "", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ExistsByUUID checks whether a multimedia asset exists by UUID.
func (r *MultimediaAssetRepositoryImpl) ExistsByUUID(ctx context.Context, uuidStr string) (bool, error) {
	parsed, err := utils.ParseUUID(uuidStr)
	if err != nil {
		return false, err
	}
	return r.Exists(ctx, models.MultimediaAssetFilter{UUID: &parsed})
}

// ByCustomerID retrieves multimedia assets for a customer.
func (r *MultimediaAssetRepositoryImpl) ByCustomerID(
	ctx context.Context,
	customerID uint,
	limit, offset int,
) ([]*models.MultimediaAsset, error) {
	return r.ByFilter(ctx, models.MultimediaAssetFilter{CustomerID: &customerID}, "id DESC", limit, offset)
}

// applyFilter applies filter criteria to a GORM query.
func (r *MultimediaAssetRepositoryImpl) applyFilter(query *gorm.DB, filter models.MultimediaAssetFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.MediaType != nil {
		query = query.Where("media_type = ?", *filter.MediaType)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}

// ByFilter retrieves multimedia assets based on filter criteria.
func (r *MultimediaAssetRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.MultimediaAssetFilter,
	orderBy string,
	limit, offset int,
) ([]*models.MultimediaAsset, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.MultimediaAsset{})

	query = r.applyFilter(query, filter)

	if orderBy == "" {
		orderBy = "id DESC"
	}
	query = query.Order(orderBy)

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	var rows []*models.MultimediaAsset
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns number of multimedia assets matching filter.
func (r *MultimediaAssetRepositoryImpl) Count(ctx context.Context, filter models.MultimediaAssetFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.MultimediaAsset{})
	query = r.applyFilter(query, filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any multimedia asset matches the filter.
func (r *MultimediaAssetRepositoryImpl) Exists(ctx context.Context, filter models.MultimediaAssetFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
