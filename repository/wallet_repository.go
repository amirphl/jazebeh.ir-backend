package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WalletRepositoryImpl implements WalletRepository interface
type WalletRepositoryImpl struct {
	*BaseRepository[models.Wallet, models.WalletFilter]
}

// NewWalletRepository creates a new wallet repository
func NewWalletRepository(db *gorm.DB) WalletRepository {
	return &WalletRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Wallet, models.WalletFilter](db),
	}
}

// ByID finds a wallet by ID
func (r *WalletRepositoryImpl) ByID(ctx context.Context, id uint) (*models.Wallet, error) {
	db := r.getDB(ctx)
	var wallet models.Wallet
	err := db.Last(&wallet, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &wallet, nil
}

// ByUUID finds a wallet by UUID
func (r *WalletRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.Wallet, error) {
	db := r.getDB(ctx)
	var wallet models.Wallet
	err := db.Where("uuid = ?", uuid).Last(&wallet).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &wallet, nil
}

// ByCustomerID finds a wallet by customer ID
func (r *WalletRepositoryImpl) ByCustomerID(ctx context.Context, customerID uint) (*models.Wallet, error) {
	db := r.getDB(ctx)
	var wallet models.Wallet
	err := db.Where("customer_id = ?", customerID).Last(&wallet).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &wallet, nil
}

// SaveWithInitialSnapshot creates a wallet with an initial balance snapshot
func (r *WalletRepositoryImpl) SaveWithInitialSnapshot(ctx context.Context, wallet *models.Wallet) error {
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

	// Create wallet first
	if err := db.Create(wallet).Error; err != nil {
		return err
	}

	initialSnapshot := &models.BalanceSnapshot{
		UUID:               uuid.New(),
		CorrelationID:      uuid.New(),
		WalletID:           wallet.ID,
		CustomerID:         wallet.CustomerID,
		FreeBalance:        0,
		FrozenBalance:      0,
		LockedBalance:      0,
		CreditBalance:      0,
		SpentOnCampaign:    0,
		AgencyShareWithTax: 0,
		TotalBalance:       0,
		Reason:             "initial_snapshot",
		Description:        "Initial balance snapshot",
		Metadata:           json.RawMessage(`{}`),
		CreatedAt:          utils.UTCNow(),
		UpdatedAt:          utils.UTCNow(),
	}

	// Create initial balance snapshot
	if err := db.Create(initialSnapshot).Error; err != nil {
		return err
	}

	return nil

}

// GetCurrentBalance gets the current balance snapshot for a wallet
func (r *WalletRepositoryImpl) GetCurrentBalance(ctx context.Context, walletID uint) (*models.BalanceSnapshot, error) {
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

// GetBalanceAtTime gets the balance snapshot at a specific point in time
func (r *WalletRepositoryImpl) GetBalanceAtTime(ctx context.Context, walletID uint, timestamp time.Time) (*models.BalanceSnapshot, error) {
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

// GetBalanceHistory gets the balance history for a wallet
func (r *WalletRepositoryImpl) GetBalanceHistory(ctx context.Context, walletID uint, limit, offset int) ([]*models.BalanceSnapshot, error) {
	db := r.getDB(ctx)
	var snapshots []*models.BalanceSnapshot
	err := db.Where("wallet_id = ?", walletID).
		Order("created_at ASC").
		Limit(limit).
		Offset(offset).
		Find(&snapshots).Error
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// ByFilter retrieves wallets based on filter criteria
func (r *WalletRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.WalletFilter,
	orderBy string,
	limit, offset int,
) ([]*models.Wallet, error) {
	db := r.getDB(ctx)
	var wallets []*models.Wallet

	query := db.Model(&models.Wallet{})
	query = r.applyFilter(query, filter)

	if orderBy != "" {
		query = query.Order(orderBy)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&wallets).Error
	if err != nil {
		return nil, err
	}
	return wallets, nil
}

// SaveBatch inserts multiple wallets in a single transaction
func (r *WalletRepositoryImpl) SaveBatch(ctx context.Context, wallets []*models.Wallet) error {
	if len(wallets) == 0 {
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

	err = db.CreateInBatches(wallets, 100).Error
	if err != nil {
		return err
	}

	return nil
}

// Count returns the number of wallets matching the filter
func (r *WalletRepositoryImpl) Count(ctx context.Context, filter models.WalletFilter) (int64, error) {
	db := r.getDB(ctx)
	var count int64

	query := db.Model(&models.Wallet{})
	query = r.applyFilter(query, filter)

	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any wallet matching the filter exists
func (r *WalletRepositoryImpl) Exists(ctx context.Context, filter models.WalletFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyFilter applies the filter to the query
func (r *WalletRepositoryImpl) applyFilter(query *gorm.DB, filter models.WalletFilter) *gorm.DB {
	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	return query
}
