package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// CryptoDepositRepositoryImpl implements CryptoDepositRepository
type CryptoDepositRepositoryImpl struct {
	*BaseRepository[models.CryptoDeposit, models.CryptoDepositFilter]
}

// NewCryptoDepositRepository creates a new crypto deposit repository
func NewCryptoDepositRepository(db *gorm.DB) CryptoDepositRepository {
	return &CryptoDepositRepositoryImpl{
		BaseRepository: NewBaseRepository[models.CryptoDeposit, models.CryptoDepositFilter](db),
	}
}

func (r *CryptoDepositRepositoryImpl) ByID(ctx context.Context, id uint) (*models.CryptoDeposit, error) {
	db := r.getDB(ctx)
	var dep models.CryptoDeposit
	if err := db.First(&dep, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &dep, nil
}

func (r *CryptoDepositRepositoryImpl) ByUUID(ctx context.Context, u string) (*models.CryptoDeposit, error) {
	db := r.getDB(ctx)
	var dep models.CryptoDeposit
	if err := db.Where("uuid = ?", u).Last(&dep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &dep, nil
}

func (r *CryptoDepositRepositoryImpl) ByTxHash(ctx context.Context, txHash string) (*models.CryptoDeposit, error) {
	db := r.getDB(ctx)
	var dep models.CryptoDeposit
	if err := db.Where("tx_hash = ?", txHash).Last(&dep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &dep, nil
}

func (r *CryptoDepositRepositoryImpl) ListUncreditedConfirmed(ctx context.Context, limit, offset int) ([]*models.CryptoDeposit, error) {
	db := r.getDB(ctx)
	var deps []*models.CryptoDeposit
	q := db.Where("status = ?", "confirmed").Where("credited_at IS NULL")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Order("created_at ASC").Find(&deps).Error; err != nil {
		return nil, err
	}
	return deps, nil
}

func (r *CryptoDepositRepositoryImpl) Update(ctx context.Context, dep *models.CryptoDeposit) error {
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
	err = db.Save(dep).Error
	if err != nil {
		return err
	}
	return nil
}

func (r *CryptoDepositRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.CryptoDepositFilter,
	orderBy string,
	limit, offset int,
) ([]*models.CryptoDeposit, error) {
	db := r.getDB(ctx)
	var deps []*models.CryptoDeposit
	q := db.Model(&models.CryptoDeposit{})
	q = r.applyFilter(q, filter)
	if orderBy != "" {
		q = q.Order(orderBy)
	} else {
		q = q.Order("created_at DESC")
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&deps).Error; err != nil {
		return nil, err
	}
	return deps, nil
}

func (r *CryptoDepositRepositoryImpl) Count(ctx context.Context, filter models.CryptoDepositFilter) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	q := db.Model(&models.CryptoDeposit{})
	q = r.applyFilter(q, filter)
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *CryptoDepositRepositoryImpl) Exists(ctx context.Context, filter models.CryptoDepositFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *CryptoDepositRepositoryImpl) applyFilter(q *gorm.DB, f models.CryptoDepositFilter) *gorm.DB {
	if f.ID != nil {
		q = q.Where("id = ?", *f.ID)
	}
	if f.UUID != nil {
		q = q.Where("uuid = ?", *f.UUID)
	}
	if f.CorrelationID != nil {
		q = q.Where("correlation_id = ?", *f.CorrelationID)
	}
	if f.CryptoPaymentRequestID != nil {
		q = q.Where("crypto_payment_request_id = ?", *f.CryptoPaymentRequestID)
	}
	if f.CustomerID != nil {
		q = q.Where("customer_id = ?", *f.CustomerID)
	}
	if f.WalletID != nil {
		q = q.Where("wallet_id = ?", *f.WalletID)
	}
	if f.Coin != nil {
		q = q.Where("coin = ?", *f.Coin)
	}
	if f.Network != nil {
		q = q.Where("network = ?", *f.Network)
	}
	if f.Platform != nil {
		q = q.Where("platform = ?", *f.Platform)
	}
	if f.TxHash != nil {
		q = q.Where("tx_hash = ?", *f.TxHash)
	}
	if f.ToAddress != nil {
		q = q.Where("to_address = ?", *f.ToAddress)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.CreatedAfter != nil {
		q = q.Where("created_at > ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		q = q.Where("created_at < ?", *f.CreatedBefore)
	}
	if f.ConfirmedOnly != nil && *f.ConfirmedOnly {
		q = q.Where("confirmed_at IS NOT NULL")
	}
	return q
}
