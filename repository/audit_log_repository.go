// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// AuditLogRepositoryImpl implements AuditLogRepository interface
type AuditLogRepositoryImpl struct {
	*BaseRepository[models.AuditLog, models.AuditLogFilter]
}

// NewAuditLogRepository creates a new audit log repository
func NewAuditLogRepository(db *gorm.DB) AuditLogRepository {
	return &AuditLogRepositoryImpl{
		BaseRepository: NewBaseRepository[models.AuditLog, models.AuditLogFilter](db),
	}
}

// ByID retrieves an audit log by its ID with preloaded relationships
func (r *AuditLogRepositoryImpl) ByID(ctx context.Context, id uint) (*models.AuditLog, error) {
	db := r.getDB(ctx)

	var auditLog models.AuditLog
	err := db.Preload("Customer").
		Last(&auditLog, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &auditLog, nil
}

// ListByCustomer retrieves audit logs for a specific customer with pagination
func (r *AuditLogRepositoryImpl) ListByCustomer(ctx context.Context, customerID uint, limit, offset int) ([]*models.AuditLog, error) {
	db := r.getDB(ctx)

	var logs []*models.AuditLog
	err := db.Where("customer_id = ?", customerID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Preload("Customer").
		Find(&logs).Error

	if err != nil {
		return nil, err
	}

	return logs, nil
}

// ListByAction retrieves audit logs for a specific action with pagination
func (r *AuditLogRepositoryImpl) ListByAction(ctx context.Context, action string, limit, offset int) ([]*models.AuditLog, error) {
	db := r.getDB(ctx)

	var logs []*models.AuditLog
	err := db.Where("action = ?", action).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Preload("Customer").
		Find(&logs).Error

	if err != nil {
		return nil, err
	}

	return logs, nil
}

// ListFailedActions retrieves all failed audit log entries with pagination
func (r *AuditLogRepositoryImpl) ListFailedActions(ctx context.Context, limit, offset int) ([]*models.AuditLog, error) {
	db := r.getDB(ctx)

	var logs []*models.AuditLog
	err := db.Where("success = ?", false).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Preload("Customer").
		Find(&logs).Error

	if err != nil {
		return nil, err
	}

	return logs, nil
}

// ListSecurityEvents retrieves security-related audit log entries with pagination
func (r *AuditLogRepositoryImpl) ListSecurityEvents(ctx context.Context, limit, offset int) ([]*models.AuditLog, error) {
	db := r.getDB(ctx)

	var logs []*models.AuditLog
	err := db.Where("action IN ?", models.SecurityActions).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Preload("Customer").
		Find(&logs).Error

	if err != nil {
		return nil, err
	}

	return logs, nil
}

// applyFilter applies filter criteria to a GORM query
func (r *AuditLogRepositoryImpl) applyFilter(query *gorm.DB, filter models.AuditLogFilter) *gorm.DB {
	// Apply filters based on provided values
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}

	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}

	if filter.Action != nil {
		query = query.Where("action = ?", *filter.Action)
	}

	if filter.Success != nil {
		query = query.Where("success = ?", *filter.Success)
	}

	if filter.IPAddress != nil {
		query = query.Where("ip_address = ?", *filter.IPAddress)
	}

	if filter.RequestID != nil {
		query = query.Where("request_id = ?", *filter.RequestID)
	}

	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}

	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}

	return query
}

// ByFilter retrieves audit logs based on filter criteria
func (r *AuditLogRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.AuditLogFilter,
	orderBy string,
	limit, offset int,
) ([]*models.AuditLog, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AuditLog{})

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

	var logs []*models.AuditLog
	err := query.Find(&logs).Error
	if err != nil {
		return nil, err
	}

	return logs, nil
}

// Count returns the number of audit logs matching the filter
func (r *AuditLogRepositoryImpl) Count(ctx context.Context, filter models.AuditLogFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AuditLog{})

	// Apply filters
	query = r.applyFilter(query, filter)

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if any audit log matching the filter exists
func (r *AuditLogRepositoryImpl) Exists(ctx context.Context, filter models.AuditLogFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
