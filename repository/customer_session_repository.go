// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CustomerSessionRepositoryImpl implements CustomerSessionRepository interface
type CustomerSessionRepositoryImpl struct {
	*BaseRepository[models.CustomerSession, models.CustomerSessionFilter]
}

// NewCustomerSessionRepository creates a new customer session repository
func NewCustomerSessionRepository(db *gorm.DB) CustomerSessionRepository {
	return &CustomerSessionRepositoryImpl{
		BaseRepository: NewBaseRepository[models.CustomerSession, models.CustomerSessionFilter](db),
	}
}

// ByID retrieves a customer session by its ID with preloaded relationships
func (r *CustomerSessionRepositoryImpl) ByID(ctx context.Context, id uint) (*models.CustomerSession, error) {
	db := r.getDB(ctx)

	var session models.CustomerSession
	err := db.Preload("Customer").
		Last(&session, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &session, nil
}

// BySessionToken retrieves a session by session token
func (r *CustomerSessionRepositoryImpl) BySessionToken(ctx context.Context, token string) (*models.CustomerSession, error) {
	db := r.getDB(ctx)

	var session models.CustomerSession
	err := db.Where("session_token = ? AND is_active = ? AND expires_at > ?",
		token, true, utils.UTCNow()).
		Preload("Customer").
		Last(&session).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &session, nil
}

// ByRefreshToken retrieves a session by refresh token
func (r *CustomerSessionRepositoryImpl) ByRefreshToken(ctx context.Context, token string) (*models.CustomerSession, error) {
	db := r.getDB(ctx)

	var session models.CustomerSession
	err := db.Where("refresh_token = ? AND is_active = ? AND expires_at > ?",
		token, true, utils.UTCNow()).
		Preload("Customer").
		Last(&session).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &session, nil
}

// ListActiveSessionsByCustomer retrieves all active sessions for a customer
func (r *CustomerSessionRepositoryImpl) ListActiveSessionsByCustomer(
	ctx context.Context,
	customerID uint,
) ([]*models.CustomerSession, error) {
	filter := models.CustomerSessionFilter{
		CustomerID: &customerID,
		IsActive:   utils.ToPtr(true),
	}

	sessions, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	// Filter out expired sessions
	var activeSessions []*models.CustomerSession
	now := utils.UTCNow()
	for _, session := range sessions {
		if session.ExpiresAt.After(now) {
			activeSessions = append(activeSessions, session)
		}
	}

	return activeSessions, nil
}

// Update updates a customer session
func (r *CustomerSessionRepositoryImpl) Update(ctx context.Context, session *models.CustomerSession) error {
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

	result := db.Model(&models.CustomerSession{}).
		Where("id = ?", session.ID).
		Updates(session).
		Update("updated_at", utils.UTCNow())

	if result.Error != nil {
		return result.Error
	}

	return nil
}

// applyFilter applies filter criteria to a GORM query
func (r *CustomerSessionRepositoryImpl) applyFilter(query *gorm.DB, filter models.CustomerSessionFilter) *gorm.DB {
	// Apply filters based on provided values
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}

	if filter.CorrelationID != nil {
		query = query.Where("correlation_id = ?", *filter.CorrelationID)
	}

	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}

	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}

	if filter.IPAddress != nil {
		query = query.Where("ip_address = ?", *filter.IPAddress)
	}

	if filter.CreatedAfter != nil {
		query = query.Where("created_at >= ?", *filter.CreatedAfter)
	}

	if filter.CreatedBefore != nil {
		query = query.Where("created_at <= ?", *filter.CreatedBefore)
	}

	if filter.ExpiresAfter != nil {
		query = query.Where("expires_at >= ?", *filter.ExpiresAfter)
	}

	if filter.ExpiresBefore != nil {
		query = query.Where("expires_at <= ?", *filter.ExpiresBefore)
	}

	if filter.AccessedAfter != nil {
		query = query.Where("last_accessed_at >= ?", *filter.AccessedAfter)
	}

	if filter.AccessedBefore != nil {
		query = query.Where("last_accessed_at <= ?", *filter.AccessedBefore)
	}

	// Special handling for IsExpired - filter expired sessions
	if filter.IsExpired != nil && *filter.IsExpired {
		query = query.Where("expires_at <= ?", utils.UTCNow())
	}

	return query
}

// ByFilter retrieves customer sessions based on filter criteria
func (r *CustomerSessionRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.CustomerSessionFilter,
	orderBy string,
	limit, offset int,
) ([]*models.CustomerSession, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.CustomerSession{})

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

	var sessions []*models.CustomerSession
	err := query.Find(&sessions).Error
	if err != nil {
		return nil, err
	}

	return sessions, nil
}

// Count returns the number of customer sessions matching the filter
func (r *CustomerSessionRepositoryImpl) Count(ctx context.Context, filter models.CustomerSessionFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.CustomerSession{})

	// Apply filters
	query = r.applyFilter(query, filter)

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if any customer session matching the filter exists
func (r *CustomerSessionRepositoryImpl) Exists(ctx context.Context, filter models.CustomerSessionFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// GetLatestByCorrelationID retrieves the latest session record for a given correlation ID
func (r *CustomerSessionRepositoryImpl) GetLatestByCorrelationID(
	ctx context.Context,
	correlationID uuid.UUID,
) (*models.CustomerSession, error) {
	db := r.getDB(ctx)

	var session models.CustomerSession
	err := db.Where("correlation_id = ?", correlationID).
		Order("id DESC").
		First(&session).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &session, nil
}

// GetHistoryByCorrelationID retrieves all session records for a given correlation ID (full history)
func (r *CustomerSessionRepositoryImpl) GetHistoryByCorrelationID(
	ctx context.Context,
	correlationID uuid.UUID,
) ([]*models.CustomerSession, error) {
	db := r.getDB(ctx)

	var sessions []*models.CustomerSession
	err := db.Where("correlation_id = ?", correlationID).
		Order("id DESC").
		Find(&sessions).Error

	if err != nil {
		return nil, err
	}

	return sessions, nil
}
