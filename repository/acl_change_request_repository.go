package repository

import (
	"context"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ACLChangeRequestRepositoryImpl implements ACLChangeRequestRepository.
type ACLChangeRequestRepositoryImpl struct {
	*BaseRepository[models.ACLChangeRequest, models.ACLChangeRequestFilter]
}

// NewACLChangeRequestRepository creates a new repository instance.
func NewACLChangeRequestRepository(db *gorm.DB) ACLChangeRequestRepository {
	return &ACLChangeRequestRepositoryImpl{
		BaseRepository: NewBaseRepository[models.ACLChangeRequest, models.ACLChangeRequestFilter](db),
	}
}

// ByUUID retrieves a change request by UUID.
func (r *ACLChangeRequestRepositoryImpl) ByUUID(ctx context.Context, id uuid.UUID) (*models.ACLChangeRequest, error) {
	db := r.getDB(ctx)
	var req models.ACLChangeRequest
	if err := db.Where("uuid = ?", id).Last(&req).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

// ByFilter returns change requests that match the provided filter.
func (r *ACLChangeRequestRepositoryImpl) ByFilter(
	ctx context.Context,
	f models.ACLChangeRequestFilter,
	orderBy string,
	limit, offset int,
) ([]*models.ACLChangeRequest, error) {
	db := r.applyFilter(r.getDB(ctx).Model(&models.ACLChangeRequest{}), f)
	if orderBy == "" {
		orderBy = "id DESC"
	}
	db = db.Order(orderBy)
	if limit > 0 {
		db = db.Limit(limit)
	}
	if offset > 0 {
		db = db.Offset(offset)
	}

	var items []*models.ACLChangeRequest
	if err := db.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// Count returns the number of change requests matching the filter.
func (r *ACLChangeRequestRepositoryImpl) Count(ctx context.Context, f models.ACLChangeRequestFilter) (int64, error) {
	db := r.applyFilter(r.getDB(ctx).Model(&models.ACLChangeRequest{}), f)
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks whether any change request matches the filter.
func (r *ACLChangeRequestRepositoryImpl) Exists(ctx context.Context, f models.ACLChangeRequestFilter) (bool, error) {
	count, err := r.Count(ctx, f)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyFilter applies filter conditions.
func (r *ACLChangeRequestRepositoryImpl) applyFilter(query *gorm.DB, f models.ACLChangeRequestFilter) *gorm.DB {
	if f.ID != nil {
		query = query.Where("id = ?", *f.ID)
	}
	if f.UUID != nil {
		query = query.Where("uuid = ?", *f.UUID)
	}
	if f.TargetAdminID != nil {
		query = query.Where("target_admin_id = ?", *f.TargetAdminID)
	}
	if f.RequestedByAdminID != nil {
		query = query.Where("requested_by_admin_id = ?", *f.RequestedByAdminID)
	}
	if f.Status != nil {
		query = query.Where("status = ?", *f.Status)
	}
	if f.ExpiresBefore != nil {
		query = query.Where("expires_at IS NOT NULL AND expires_at < ?", *f.ExpiresBefore)
	}
	return query
}
