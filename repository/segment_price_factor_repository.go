package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// SegmentPriceFactorRepositoryImpl implements SegmentPriceFactorRepository
type SegmentPriceFactorRepositoryImpl struct {
	*BaseRepository[models.SegmentPriceFactor, models.SegmentPriceFactorFilter]
}

// NewSegmentPriceFactorRepository creates a new repository for segment price factors
func NewSegmentPriceFactorRepository(db *gorm.DB) SegmentPriceFactorRepository {
	return &SegmentPriceFactorRepositoryImpl{
		BaseRepository: NewBaseRepository[models.SegmentPriceFactor, models.SegmentPriceFactorFilter](db),
	}
}

// ListLatestByLevel3 returns the latest price factor per level3 (last inserted wins).
func (r *SegmentPriceFactorRepositoryImpl) ListLatestByLevel3(ctx context.Context) ([]*models.SegmentPriceFactor, error) {
	return r.ListLatestByLevel3ForPlatform(ctx, models.SegmentPriceFactorPlatformSMS)
}

// ListLatestByLevel3ForPlatform returns the latest price factor per level3 scoped by platform.
func (r *SegmentPriceFactorRepositoryImpl) ListLatestByLevel3ForPlatform(
	ctx context.Context,
	platform string,
) ([]*models.SegmentPriceFactor, error) {
	db := r.getDB(ctx)

	var rows []*models.SegmentPriceFactor
	err := db.Raw(`
		SELECT DISTINCT ON (level3) id, platform, level3, price_factor, created_at, updated_at
		FROM segment_price_factors
		WHERE platform = ?
		ORDER BY level3, created_at DESC
	`, platform).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	return rows, nil
}

// ByID retrieves a segment price factor by ID.
func (r *SegmentPriceFactorRepositoryImpl) ByID(ctx context.Context, id uint) (*models.SegmentPriceFactor, error) {
	db := r.getDB(ctx)
	var spf models.SegmentPriceFactor
	err := db.Last(&spf, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &spf, nil
}

// applyFilter applies filter conditions to the GORM query
func (r *SegmentPriceFactorRepositoryImpl) applyFilter(db *gorm.DB, filter models.SegmentPriceFactorFilter) *gorm.DB {
	if filter.Platform != nil {
		db = db.Where("platform = ?", *filter.Platform)
	}
	if filter.Level3 != nil {
		db = db.Where("level3 = ?", *filter.Level3)
	}
	return db
}

// LatestByLevel3s returns a map of level3 -> latest price factor for the provided level3 values.
func (r *SegmentPriceFactorRepositoryImpl) LatestByLevel3s(ctx context.Context, level3s []string) (map[string]float64, error) {
	return r.LatestByLevel3sForPlatform(ctx, level3s, models.SegmentPriceFactorPlatformSMS)
}

// LatestByLevel3sForPlatform returns a map of level3 -> latest price factor for the provided level3 values scoped by platform.
func (r *SegmentPriceFactorRepositoryImpl) LatestByLevel3sForPlatform(
	ctx context.Context,
	level3s []string,
	platform string,
) (map[string]float64, error) {
	out := make(map[string]float64)
	if len(level3s) == 0 {
		return out, nil
	}

	type row struct {
		Level3      string  `json:"level3"`
		PriceFactor float64 `json:"price_factor"`
	}
	var rows []row

	db := r.getDB(ctx)
	err := db.Raw(`
		SELECT DISTINCT ON (level3) level3, price_factor
		FROM segment_price_factors
		WHERE platform = ? AND level3 IN ?
		ORDER BY level3, created_at DESC
	`, platform, level3s).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		out[r.Level3] = r.PriceFactor
	}

	return out, nil
}

// ByFilter retrieves segment price factors based on filter criteria.
func (r *SegmentPriceFactorRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.SegmentPriceFactorFilter,
	orderBy string,
	limit, offset int,
) ([]*models.SegmentPriceFactor, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.SegmentPriceFactor{})

	query = r.applyFilter(query, filter)

	if orderBy == "" {
		orderBy = "created_at DESC"
	}
	query = query.Order(orderBy)

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	var rows []*models.SegmentPriceFactor
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns the number of segment price factors matching the filter.
func (r *SegmentPriceFactorRepositoryImpl) Count(ctx context.Context, filter models.SegmentPriceFactorFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.SegmentPriceFactor{})
	query = r.applyFilter(query, filter)

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any segment price factor matching the filter exists.
func (r *SegmentPriceFactorRepositoryImpl) Exists(ctx context.Context, filter models.SegmentPriceFactorFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
