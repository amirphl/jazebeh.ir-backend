package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// TagRepositoryImpl implements TagRepository interface
type TagRepositoryImpl struct {
	*BaseRepository[models.Tag, models.TagFilter]
}

// NewTagRepository creates a new tag repository
func NewTagRepository(db *gorm.DB) TagRepository {
	return &TagRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Tag, models.TagFilter](db),
	}
}

// ByID retrieves a tag by its ID
func (r *TagRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Tag, error) {
	db := r.getDB(ctx)
	var row models.Tag
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ByName retrieves a tag by name
func (r *TagRepositoryImpl) ByName(ctx context.Context, name string) (*models.Tag, error) {
	filter := models.TagFilter{Name: &name}
	rows, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ListByIDs retrieves tags for a list of IDs
func (r *TagRepositoryImpl) ListByIDs(ctx context.Context, ids []uint) ([]*models.Tag, error) {
	db := r.getDB(ctx)
	if len(ids) == 0 {
		return []*models.Tag{}, nil
	}
	var rows []*models.Tag
	if err := db.Model(&models.Tag{}).Where("id IN ? AND is_active = ?", ids, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListByNames retrieves tags for a list of names
func (r *TagRepositoryImpl) ListByNames(ctx context.Context, names []string) ([]*models.Tag, error) {
	db := r.getDB(ctx)
	if len(names) == 0 {
		return []*models.Tag{}, nil
	}
	var rows []*models.Tag
	if err := db.Model(&models.Tag{}).Where("name IN ? AND is_active = ?", names, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *TagRepositoryImpl) ListActiveAfterID(ctx context.Context, afterID *uint, limit int) ([]*models.Tag, error) {
	db := r.getDB(ctx).Model(&models.Tag{}).
		Where("is_active = ?", true)

	if afterID != nil && *afterID > 0 {
		db = db.Where("id > ?", *afterID)
	}
	if limit > 0 {
		db = db.Limit(limit)
	}

	var rows []*models.Tag
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// applyFilter applies filter criteria to a GORM query
func (r *TagRepositoryImpl) applyFilter(query *gorm.DB, filter models.TagFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.Name != nil {
		query = query.Where("name = ?", *filter.Name)
	}
	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}

// ByFilter retrieves tags based on filter criteria
func (r *TagRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.TagFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Tag, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Tag{})

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

	var rows []*models.Tag
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns the number of tags matching the filter
func (r *TagRepositoryImpl) Count(ctx context.Context, filter models.TagFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Tag{})
	query = r.applyFilter(query, filter)

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any tag matching the filter exists
func (r *TagRepositoryImpl) Exists(ctx context.Context, filter models.TagFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
