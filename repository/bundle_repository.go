package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

type BundleRepositoryImpl struct {
	*BaseRepository[models.Bundle, models.BundleFilter]
}

func NewBundleRepository(db *gorm.DB) BundleRepository {
	return &BundleRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Bundle, models.BundleFilter](db),
	}
}

func (r *BundleRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Bundle, error) {
	db := r.getDB(ctx)

	var bundle models.Bundle
	err := db.Preload("Customer").Last(&bundle, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &bundle, nil
}

func (r *BundleRepositoryImpl) ByCustomerID(ctx context.Context, customerID uint, limit, offset int) ([]*models.Bundle, error) {
	filter := models.BundleFilter{CustomerID: &customerID}
	return r.ByFilter(ctx, filter, "id DESC", limit, offset)
}

func (r *BundleRepositoryImpl) Update(ctx context.Context, bundle *models.Bundle) error {
	db, shouldCommit, err := r.getDBForWrite(ctx)
	if err != nil {
		return err
	}

	if shouldCommit {
		defer func() {
			if err != nil {
				db.Rollback()
			} else {
				db.Commit()
			}
		}()
	}

	bundle.UpdatedAt = utils.UTCNow()

	err = db.Save(bundle).Error
	if err != nil {
		return err
	}

	return nil
}

func (r *BundleRepositoryImpl) applyFilter(query *gorm.DB, filter models.BundleFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}

	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}

	if filter.Title != nil {
		title := strings.TrimSpace(*filter.Title)
		if title != "" {
			query = query.Where("title ILIKE ?", "%"+title+"%")
		}
	}

	if filter.TargetAudiencePersona != nil {
		targetAudiencePersona := strings.TrimSpace(*filter.TargetAudiencePersona)
		if targetAudiencePersona != "" {
			query = query.Where("target_audience_persona ILIKE ?", "%"+targetAudiencePersona+"%")
		}
	}

	if filter.TargetCustomerName != nil {
		targetCustomerName := strings.TrimSpace(*filter.TargetCustomerName)
		if targetCustomerName != "" {
			query = query.Where("target_customer_name ILIKE ?", "%"+targetCustomerName+"%")
		}
	}

	if filter.CreatedAfter != nil {
		query = query.Where("created_at >= ?", *filter.CreatedAfter)
	}

	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}

	if filter.UpdatedAfter != nil {
		query = query.Where("updated_at >= ?", *filter.UpdatedAfter)
	}

	if filter.UpdatedBefore != nil {
		query = query.Where("updated_at < ?", *filter.UpdatedBefore)
	}

	return query
}

func (r *BundleRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.BundleFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Bundle, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.Bundle{}), filter)

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

	var bundles []*models.Bundle
	err := query.Preload("Customer").Find(&bundles).Error
	if err != nil {
		return nil, err
	}

	return bundles, nil
}

func (r *BundleRepositoryImpl) Count(ctx context.Context, filter models.BundleFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.Bundle{}), filter)

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}

	return count, nil
}

func (r *BundleRepositoryImpl) Exists(ctx context.Context, filter models.BundleFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
