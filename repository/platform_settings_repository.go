package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// PlatformSettingsRepositoryImpl implements PlatformSettingsRepository interface.
type PlatformSettingsRepositoryImpl struct {
	*BaseRepository[models.PlatformSettings, models.PlatformSettingsFilter]
}

// NewPlatformSettingsRepository creates a new platform settings repository.
func NewPlatformSettingsRepository(db *gorm.DB) PlatformSettingsRepository {
	return &PlatformSettingsRepositoryImpl{
		BaseRepository: NewBaseRepository[models.PlatformSettings, models.PlatformSettingsFilter](db),
	}
}

// ByID retrieves platform settings by ID.
func (r *PlatformSettingsRepositoryImpl) ByID(ctx context.Context, id uint) (*models.PlatformSettings, error) {
	db := r.getDB(ctx)
	var row models.PlatformSettings
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ByUUID retrieves platform settings by UUID.
func (r *PlatformSettingsRepositoryImpl) ByUUID(ctx context.Context, uuidStr string) (*models.PlatformSettings, error) {
	parsed, err := utils.ParseUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	rows, err := r.ByFilter(ctx, models.PlatformSettingsFilter{UUID: &parsed}, "", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// applyFilter applies filter criteria to a GORM query.
func (r *PlatformSettingsRepositoryImpl) applyFilter(query *gorm.DB, filter models.PlatformSettingsFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.Name != nil {
		query = query.Where("name = ?", *filter.Name)
	}
	if filter.Platform != nil {
		query = query.Where("platform = ?", *filter.Platform)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.MultimediaID != nil {
		query = query.Where("multimedia_id = ?", *filter.MultimediaID)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}

// ByFilter retrieves platform settings based on filter criteria.
func (r *PlatformSettingsRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.PlatformSettingsFilter,
	orderBy string,
	limit, offset int,
) ([]*models.PlatformSettings, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.PlatformSettings{})

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

	var rows []*models.PlatformSettings
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns number of platform settings matching filter.
func (r *PlatformSettingsRepositoryImpl) Count(ctx context.Context, filter models.PlatformSettingsFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.PlatformSettings{})
	query = r.applyFilter(query, filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any platform settings match the filter.
func (r *PlatformSettingsRepositoryImpl) Exists(ctx context.Context, filter models.PlatformSettingsFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

// UpdateStatus updates only status/updated_at for a platform settings row.
func (r *PlatformSettingsRepositoryImpl) UpdateStatus(ctx context.Context, id uint, status models.PlatformSettingsStatus) error {
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

	err = db.Model(&models.PlatformSettings{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     status,
			"updated_at": utils.UTCNow(),
		}).Error
	if err != nil {
		return err
	}
	return nil
}

// AppendMetadata merges a single key-value pair into metadata without replacing existing keys.
func (r *PlatformSettingsRepositoryImpl) AppendMetadata(ctx context.Context, id uint, key, value string) error {
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

	err = db.Model(&models.PlatformSettings{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"metadata":   gorm.Expr("COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(?::text, to_jsonb(?::text))", key, value),
			"updated_at": utils.UTCNow(),
		}).Error
	if err != nil {
		return err
	}

	return nil
}
