package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// LayerPercentiles holds the p33 and p66 percentile thresholds for a category layer.
type LayerPercentiles struct {
	P33 float64
	P66 float64
}

// SrcLayerAllStatsRepository fetches audience score percentiles from the stats table.
type SrcLayerAllStatsRepository interface {
	// FetchPercentiles returns p33/p66 for the first row matching the given levels.
	// Returns nil if no matching row with non-null percentiles is found.
	FetchPercentiles(ctx context.Context, level1 *string, level2s []string, level3s []string) (*LayerPercentiles, error)
}

type srcLayerAllStatsRepositoryImpl struct {
	db *gorm.DB
}

func NewSrcLayerAllStatsRepository(db *gorm.DB) SrcLayerAllStatsRepository {
	return &srcLayerAllStatsRepositoryImpl{db: db}
}

func (r *srcLayerAllStatsRepositoryImpl) FetchPercentiles(
	ctx context.Context,
	level1 *string,
	level2s []string,
	level3s []string,
) (*LayerPercentiles, error) {
	q := r.db.WithContext(ctx).Model(&models.SrcLayerAllStats{}).
		Where("p33 IS NOT NULL AND p66 IS NOT NULL")

	if level1 != nil && *level1 != "" {
		q = q.Where("layer1_category = ?", *level1)
	}
	if len(level2s) > 0 {
		q = q.Where("layer2_category IN ?", level2s)
	}
	if len(level3s) > 0 {
		q = q.Where("layer3_category IN ?", level3s)
	}

	var row models.SrcLayerAllStats
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if row.P33 == nil || row.P66 == nil {
		return nil, nil
	}
	return &LayerPercentiles{P33: *row.P33, P66: *row.P66}, nil
}
