package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// TicketRepositoryImpl implements TicketRepository interface
type TicketRepositoryImpl struct {
	*BaseRepository[models.Ticket, models.TicketFilter]
}

// NewTicketRepository creates a new ticket repository
func NewTicketRepository(db *gorm.DB) TicketRepository {
	return &TicketRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Ticket, models.TicketFilter](db),
	}
}

// ByID retrieves a ticket by its ID
func (r *TicketRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Ticket, error) {
	db := r.getDB(ctx)
	var row models.Ticket
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ByUUID retrieves a ticket by UUID
func (r *TicketRepositoryImpl) ByUUID(ctx context.Context, uuidStr string) (*models.Ticket, error) {
	parsed, err := utils.ParseUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	rows, err := r.ByFilter(ctx, models.TicketFilter{UUID: &parsed}, "", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ByCorrelationID lists tickets by correlation ID
func (r *TicketRepositoryImpl) ByCorrelationID(ctx context.Context, correlationIDStr string) ([]*models.Ticket, error) {
	parsed, err := utils.ParseUUID(correlationIDStr)
	if err != nil {
		return nil, err
	}
	return r.ByFilter(ctx, models.TicketFilter{CorrelationID: &parsed}, "id DESC", 0, 0)
}

// applyFilter applies filter criteria to a GORM query
func (r *TicketRepositoryImpl) applyFilter(query *gorm.DB, filter models.TicketFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.CorrelationID != nil {
		query = query.Where("correlation_id = ?", *filter.CorrelationID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.Title != nil {
		query = query.Where("title = ?", *filter.Title)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	if filter.RepliedByAdmin != nil {
		query = query.Where("replied_by_admin = ?", *filter.RepliedByAdmin)
	}
	return query
}

// ByFilter retrieves tickets based on filter criteria
func (r *TicketRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.TicketFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Ticket, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Ticket{})

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

	var rows []*models.Ticket
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns number of tickets matching filter
func (r *TicketRepositoryImpl) Count(ctx context.Context, filter models.TicketFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Ticket{})
	query = r.applyFilter(query, filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any ticket matches the filter
func (r *TicketRepositoryImpl) Exists(ctx context.Context, filter models.TicketFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
