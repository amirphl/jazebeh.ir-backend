// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// AccountTypeRepositoryImpl implements AccountTypeRepository interface
type AccountTypeRepositoryImpl struct {
	*BaseRepository[models.AccountType, models.AccountTypeFilter]
}

// NewAccountTypeRepository creates a new account type repository
func NewAccountTypeRepository(db *gorm.DB) AccountTypeRepository {
	return &AccountTypeRepositoryImpl{
		BaseRepository: NewBaseRepository[models.AccountType, models.AccountTypeFilter](db),
	}
}

// ByID retrieves an account type by its ID
func (r *AccountTypeRepositoryImpl) ByID(ctx context.Context, id uint) (*models.AccountType, error) {
	db := r.getDB(ctx)

	var accountType models.AccountType
	err := db.Last(&accountType, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find account type by ID %d: %w", id, err)
	}

	return &accountType, nil
}

// ByTypeName retrieves an account type by its type name
func (r *AccountTypeRepositoryImpl) ByTypeName(ctx context.Context, typeName string) (*models.AccountType, error) {
	db := r.getDB(ctx)

	var accountType models.AccountType
	err := db.Where("type_name = ?", typeName).
		Last(&accountType).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find account type by name: %w", err)
	}

	return &accountType, nil
}

// applyFilter applies filter criteria to a GORM query
func (r *AccountTypeRepositoryImpl) applyFilter(query *gorm.DB, filter models.AccountTypeFilter) *gorm.DB {
	// Apply filters based on provided values
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}

	if filter.TypeName != nil {
		query = query.Where("type_name = ?", *filter.TypeName)
	}

	if filter.DisplayName != nil {
		query = query.Where("display_name = ?", *filter.DisplayName)
	}

	if filter.Description != nil {
		query = query.Where("description = ?", *filter.Description)
	}

	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}

	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}

	return query
}

// ByFilter retrieves account types based on filter criteria
func (r *AccountTypeRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.AccountTypeFilter,
	orderBy string,
	limit, offset int,
) ([]*models.AccountType, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AccountType{})

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

	var accountTypes []*models.AccountType
	err := query.Find(&accountTypes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to find account types by filter: %w", err)
	}

	return accountTypes, nil
}

// Count returns the number of account types matching the filter
func (r *AccountTypeRepositoryImpl) Count(ctx context.Context, filter models.AccountTypeFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AccountType{})

	// Apply filters
	query = r.applyFilter(query, filter)

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count account types: %w", err)
	}

	return count, nil
}

// Exists checks if any account type matching the filter exists
func (r *AccountTypeRepositoryImpl) Exists(ctx context.Context, filter models.AccountTypeFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
