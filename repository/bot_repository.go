package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

// BotRepositoryImpl implements BotRepository interface
type BotRepositoryImpl struct {
	*BaseRepository[models.Bot, models.BotFilter]
}

// NewBotRepository creates a new bot repository
func NewBotRepository(db *gorm.DB) BotRepository {
	return &BotRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Bot, models.BotFilter](db),
	}
}

// ByID retrieves a bot by its ID
func (r *BotRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Bot, error) {
	db := r.getDB(ctx)

	var bot models.Bot
	err := db.Last(&bot, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &bot, nil
}

// ByUUID retrieves a bot by UUID
func (r *BotRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.Bot, error) {
	parsedUUID, err := utils.ParseUUID(uuid)
	if err != nil {
		return nil, err
	}

	filter := models.BotFilter{UUID: &parsedUUID}
	bots, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	if len(bots) == 0 {
		return nil, nil
	}

	return bots[0], nil
}

// ByUsername retrieves a bot by username
func (r *BotRepositoryImpl) ByUsername(ctx context.Context, username string) (*models.Bot, error) {
	filter := models.BotFilter{Username: &username}
	bots, err := r.ByFilter(ctx, filter, "", 0, 0)
	if err != nil {
		return nil, err
	}

	if len(bots) == 0 {
		return nil, nil
	}

	return bots[0], nil
}

// applyFilter applies filter criteria to a GORM query
func (r *BotRepositoryImpl) applyFilter(query *gorm.DB, filter models.BotFilter) *gorm.DB {
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

// ByFilter retrieves bots based on filter criteria
func (r *BotRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.BotFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Bot, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Bot{})

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

	var bots []*models.Bot
	err := query.Find(&bots).Error
	if err != nil {
		return nil, err
	}

	return bots, nil
}

// Count returns the number of bots matching the filter
func (r *BotRepositoryImpl) Count(ctx context.Context, filter models.BotFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.Bot{})

	// Apply filters
	query = r.applyFilter(query, filter)

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if any bot matching the filter exists
func (r *BotRepositoryImpl) Exists(ctx context.Context, filter models.BotFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
