// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// AdminRepositoryImpl implements AdminRepository interface
type AdminRepositoryImpl struct {
	*BaseRepository[models.Admin, models.AdminFilter]
}

// NewAdminRepository creates a new admin repository
func NewAdminRepository(db *gorm.DB) AdminRepository {
	return &AdminRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Admin, models.AdminFilter](db),
	}
}

// ByID retrieves an admin by its ID
func (r *AdminRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Admin, error) {
	db := r.getDB(ctx)

	var admin models.Admin
	err := db.Last(&admin, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &admin, nil
}

// ByUUID retrieves an admin by UUID
func (r *AdminRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.Admin, error) {
	parsedUUID, err := utils.ParseUUID(uuid)
	if err != nil {
		return nil, err
	}

	filter := models.AdminFilter{UUID: &parsedUUID}
	admins, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	if len(admins) == 0 {
		return nil, nil
	}

	return admins[0], nil
}

// ByUsername retrieves an admin by username
func (r *AdminRepositoryImpl) ByUsername(ctx context.Context, username string) (*models.Admin, error) {
	filter := models.AdminFilter{Username: &username}
	admins, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	if len(admins) == 0 {
		return nil, nil
	}

	return admins[0], nil
}

// applyFilter applies filter criteria to a GORM query
func (r *AdminRepositoryImpl) applyFilter(query *gorm.DB, filter models.AdminFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.Username != nil {
		query = query.Where("username = ?", *filter.Username)
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
	if filter.LastLoginAfter != nil {
		query = query.Where("last_login_at > ?", *filter.LastLoginAfter)
	}
	if filter.LastLoginBefore != nil {
		query = query.Where("last_login_at < ?", *filter.LastLoginBefore)
	}
	return query
}

// ByFilter retrieves admins based on filter criteria
func (r *AdminRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.AdminFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Admin, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Admin{})

	// Apply filters
	query = r.applyFilter(query, filter)

	// Apply ordering (default to id DESC)
	if orderBy == "" {
		orderBy = "id DESC"
	}
	query = query.Order(orderBy)

	// Apply pagination
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	var admins []*models.Admin
	err := query.Find(&admins).Error
	if err != nil {
		return nil, err
	}

	return admins, nil
}

// Count returns the number of admins matching the filter
func (r *AdminRepositoryImpl) Count(ctx context.Context, filter models.AdminFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Admin{})

	// Apply filters
	query = r.applyFilter(query, filter)

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if any admin matching the filter exists
func (r *AdminRepositoryImpl) Exists(ctx context.Context, filter models.AdminFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
