package repository

import (
	"context"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BalanceSnapshotRepositoryImpl implements BalanceSnapshotRepository interface
type BalanceSnapshotRepositoryImpl struct {
	*BaseRepository[models.BalanceSnapshot, models.BalanceSnapshotFilter]
}

// NewBalanceSnapshotRepository creates a new balance snapshot repository
func NewBalanceSnapshotRepository(db *gorm.DB) BalanceSnapshotRepository {
	return &BalanceSnapshotRepositoryImpl{
		BaseRepository: NewBaseRepository[models.BalanceSnapshot, models.BalanceSnapshotFilter](db),
	}
}

// ByID finds a balance snapshot by ID
func (r *BalanceSnapshotRepositoryImpl) ByID(ctx context.Context, id uint) (*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshot models.BalanceSnapshot
	err := db.Last(&snapshot, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

// ByUUID finds a balance snapshot by UUID
func (r *BalanceSnapshotRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshot models.BalanceSnapshot
	err := db.Where("uuid = ?", uuid).Last(&snapshot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

// ByCorrelationID finds balance snapshots by correlation ID
func (r *BalanceSnapshotRepositoryImpl) ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshots []*models.BalanceSnapshot
	err := db.Where("correlation_id = ?", correlationID).Order("created_at DESC").Find(&snapshots).Error
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// ByWalletID finds balance snapshots by wallet ID
func (r *BalanceSnapshotRepositoryImpl) ByWalletID(
	ctx context.Context,
	walletID uint,
	limit, offset int,
) ([]*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshots []*models.BalanceSnapshot

	query := db.Where("wallet_id = ?", walletID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&snapshots).Error
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// ByCustomerID finds balance snapshots by customer ID
func (r *BalanceSnapshotRepositoryImpl) ByCustomerID(
	ctx context.Context,
	customerID uint,
	limit, offset int,
) ([]*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshots []*models.BalanceSnapshot

	query := db.Where("customer_id = ?", customerID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&snapshots).Error
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// GetLatestByWalletID gets the latest balance snapshot for a wallet
func (r *BalanceSnapshotRepositoryImpl) GetLatestByWalletID(ctx context.Context, walletID uint) (*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshot models.BalanceSnapshot
	err := db.Where("wallet_id = ?", walletID).
		Order("created_at DESC").
		First(&snapshot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

// GetLatestByWalletIDBeforeTime gets the latest balance snapshot for a wallet before a specific time
func (r *BalanceSnapshotRepositoryImpl) GetLatestByWalletIDBeforeTime(
	ctx context.Context,
	walletID uint,
	timestamp time.Time,
) (*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshot models.BalanceSnapshot
	err := db.Where("wallet_id = ? AND created_at <= ?", walletID, timestamp).
		Order("created_at DESC").
		First(&snapshot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

// ByFilter retrieves balance snapshots based on filter criteria
func (r *BalanceSnapshotRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.BalanceSnapshotFilter,
	orderBy string,
	limit, offset int,
) ([]*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshots []*models.BalanceSnapshot

	query := db.Model(&models.BalanceSnapshot{})
	query = r.applyFilter(query, filter)

	if orderBy != "" {
		query = query.Order(orderBy)
	} else {
		query = query.Order("created_at DESC")
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&snapshots).Error
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// SaveBatch inserts multiple balance snapshots in a single transaction
func (r *BalanceSnapshotRepositoryImpl) SaveBatch(ctx context.Context, snapshots []*models.BalanceSnapshot) error {
	if len(snapshots) == 0 {
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

	err = db.CreateInBatches(snapshots, 100).Error
	if err != nil {
		return err
	}
	return nil
}

// Count returns the number of balance snapshots matching the filter
func (r *BalanceSnapshotRepositoryImpl) Count(ctx context.Context, filter models.BalanceSnapshotFilter) (int64, error) {
	db := r.getDB(ctx)
	var count int64

	query := db.Model(&models.BalanceSnapshot{})
	query = r.applyFilter(query, filter)

	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any balance snapshot matching the filter exists
func (r *BalanceSnapshotRepositoryImpl) Exists(ctx context.Context, filter models.BalanceSnapshotFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyFilter applies the filter to the query
func (r *BalanceSnapshotRepositoryImpl) applyFilter(query *gorm.DB, filter models.BalanceSnapshotFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.CorrelationID != nil {
		query = query.Where("correlation_id = ?", *filter.CorrelationID)
	}
	if filter.WalletID != nil {
		query = query.Where("wallet_id = ?", *filter.WalletID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.Reason != nil {
		query = query.Where("reason = ?", *filter.Reason)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}
