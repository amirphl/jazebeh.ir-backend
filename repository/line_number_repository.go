// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"
	"strconv"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// LineNumberRepositoryImpl implements LineNumberRepository interface
type LineNumberRepositoryImpl struct {
	*BaseRepository[models.LineNumber, models.LineNumberFilter]
}

// NewLineNumberRepository creates a new line number repository
func NewLineNumberRepository(db *gorm.DB) LineNumberRepository {
	return &LineNumberRepositoryImpl{
		BaseRepository: NewBaseRepository[models.LineNumber, models.LineNumberFilter](db),
	}
}

// ByID retrieves a line number by its ID
func (r *LineNumberRepositoryImpl) ByID(ctx context.Context, id uint) (*models.LineNumber, error) {
	db := r.getDB(ctx)

	var line models.LineNumber
	err := db.Last(&line, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &line, nil
}

// ByUUID retrieves a line number by UUID (string)
func (r *LineNumberRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.LineNumber, error) {
	parsed, err := utils.ParseUUID(uuid)
	if err != nil {
		return nil, err
	}

	filter := models.LineNumberFilter{UUID: &parsed}
	items, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

// ByValue retrieves a line number by its value (line_number)
func (r *LineNumberRepositoryImpl) ByValue(ctx context.Context, value string) (*models.LineNumber, error) {
	filter := models.LineNumberFilter{LineNumber: &value}
	items, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

// applyFilter applies filter criteria to a GORM query
func (r *LineNumberRepositoryImpl) applyFilter(query *gorm.DB, filter models.LineNumberFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.Name != nil {
		query = query.Where("name = ?", *filter.Name)
	}
	if filter.LineNumber != nil {
		query = query.Where("line_number = ?", *filter.LineNumber)
	}
	if filter.Provider != nil {
		query = query.Where("provider = ?", *filter.Provider)
	}
	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}
	if filter.Priority != nil {
		query = query.Where("priority = ?", *filter.Priority)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}

// ByFilter retrieves line numbers based on filter criteria
func (r *LineNumberRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.LineNumberFilter,
	orderBy string,
	limit, offset int,
) ([]*models.LineNumber, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.LineNumber{})

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

	var lines []*models.LineNumber
	if err := query.Find(&lines).Error; err != nil {
		return nil, err
	}
	return lines, nil
}

// Count returns the number of line numbers matching the filter
func (r *LineNumberRepositoryImpl) Count(ctx context.Context, filter models.LineNumberFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.LineNumber{})
	query = r.applyFilter(query, filter)

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any line number matching the filter exists
func (r *LineNumberRepositoryImpl) Exists(ctx context.Context, filter models.LineNumberFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Update updates mutable fields for a line number by ID
func (r *LineNumberRepositoryImpl) Update(ctx context.Context, line *models.LineNumber) error {
	if line == nil {
		return errors.New("line number payload is nil")
	}
	if line.ID == 0 {
		return errors.New("line number ID is required for update")
	}

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

	updates := map[string]any{
		"updated_at": utils.UTCNow(),
	}
	if line.Name != nil {
		updates["name"] = *line.Name
	}
	if line.LineNumber != "" {
		updates["line_number"] = line.LineNumber
	}
	if line.Provider != "" {
		updates["provider"] = line.Provider
	}
	if line.PriceFactor != 0 {
		updates["price_factor"] = line.PriceFactor
	}
	if line.Priority != nil {
		updates["priority"] = *line.Priority
	}
	if line.IsActive != nil {
		updates["is_active"] = *line.IsActive
	}

	result := db.Model(&models.LineNumber{}).
		Where("id = ?", line.ID).
		Updates(updates)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("line number not found with ID: " + strconv.Itoa(int(line.ID)))
	}
	return nil
}

// UpdateBatch updates multiple line numbers in a single transaction
func (r *LineNumberRepositoryImpl) UpdateBatch(ctx context.Context, lines []*models.LineNumber) error {
	if len(lines) == 0 {
		return nil
	}
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
	for _, line := range lines {
		if line == nil || line.ID == 0 {
			return errors.New("invalid line payload in batch (nil or missing ID)")
		}
		updates := map[string]any{
			"updated_at": utils.UTCNow(),
		}
		if line.Name != nil {
			updates["name"] = *line.Name
		}
		if line.LineNumber != "" {
			updates["line_number"] = line.LineNumber
		}
		if line.Provider != "" {
			updates["provider"] = line.Provider
		}
		if line.PriceFactor != 0 {
			updates["price_factor"] = line.PriceFactor
		}
		if line.Priority != nil {
			updates["priority"] = *line.Priority
		}
		if line.IsActive != nil {
			updates["is_active"] = *line.IsActive
		}
		if err := db.Model(&models.LineNumber{}).
			Where("id = ?", line.ID).
			Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
