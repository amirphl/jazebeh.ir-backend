package repository

import (
	"context"
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CryptoPaymentRequestRepositoryImpl implements CryptoPaymentRequestRepository
type CryptoPaymentRequestRepositoryImpl struct {
	*BaseRepository[models.CryptoPaymentRequest, models.CryptoPaymentRequestFilter]
}

// NewCryptoPaymentRequestRepository creates a new crypto payment request repository
func NewCryptoPaymentRequestRepository(db *gorm.DB) CryptoPaymentRequestRepository {
	return &CryptoPaymentRequestRepositoryImpl{
		BaseRepository: NewBaseRepository[models.CryptoPaymentRequest, models.CryptoPaymentRequestFilter](db),
	}
}

func (r *CryptoPaymentRequestRepositoryImpl) ByID(ctx context.Context, id uint) (*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var req models.CryptoPaymentRequest
	if err := db.First(&req, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByUUID(ctx context.Context, u string) (*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var req models.CryptoPaymentRequest
	if err := db.Where("uuid = ?", u).Last(&req).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByCorrelationID(
	ctx context.Context,
	correlationID uuid.UUID,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	if err := db.Where("correlation_id = ?", correlationID).Order("created_at DESC").Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByDepositAddress(
	ctx context.Context,
	address, memo string,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	q := db.Where("deposit_address = ?", address)
	if memo != "" {
		q = q.Where("deposit_memo = ?", memo)
	}
	if err := q.Order("created_at DESC").Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByProviderRequestID(
	ctx context.Context,
	providerRequestID string,
) (*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var req models.CryptoPaymentRequest
	if err := db.Where("provider_request_id = ?", providerRequestID).Last(&req).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByCustomerID(
	ctx context.Context,
	customerID uint,
	limit, offset int,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	q := db.Where("customer_id = ?", customerID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByWalletID(
	ctx context.Context,
	walletID uint,
	limit, offset int,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	q := db.Where("wallet_id = ?", walletID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) ByStatus(
	ctx context.Context,
	status models.CryptoPaymentStatus,
	limit, offset int,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	q := db.Where("status = ?", status).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) GetPendingRequests(
	ctx context.Context,
	limit, offset int,
) ([]*models.CryptoPaymentRequest, error) {
	return r.ByStatus(ctx, models.CryptoPaymentStatusPending, limit, offset)
}

func (r *CryptoPaymentRequestRepositoryImpl) Update(ctx context.Context, request *models.CryptoPaymentRequest) error {
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
	err = db.Save(request).Error
	if err != nil {
		return err
	}
	return nil
}

// ByFilter with ordering and pagination
func (r *CryptoPaymentRequestRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.CryptoPaymentRequestFilter,
	orderBy string,
	limit, offset int,
) ([]*models.CryptoPaymentRequest, error) {
	db := r.getDB(ctx)
	var reqs []*models.CryptoPaymentRequest
	q := db.Model(&models.CryptoPaymentRequest{})
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
	if err := q.Find(&reqs).Error; err != nil {
		return nil, err
	}
	return reqs, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) Count(ctx context.Context, filter models.CryptoPaymentRequestFilter) (int64, error) {
	db := r.getDB(ctx)
	var count int64
	q := db.Model(&models.CryptoPaymentRequest{})
	q = r.applyFilter(q, filter)
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) Exists(ctx context.Context, filter models.CryptoPaymentRequestFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *CryptoPaymentRequestRepositoryImpl) applyFilter(q *gorm.DB, f models.CryptoPaymentRequestFilter) *gorm.DB {
	if f.ID != nil {
		q = q.Where("id = ?", *f.ID)
	}
	if f.UUID != nil {
		q = q.Where("uuid = ?", *f.UUID)
	}
	if f.CorrelationID != nil {
		q = q.Where("correlation_id = ?", *f.CorrelationID)
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
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.DepositAddress != nil {
		q = q.Where("deposit_address = ?", *f.DepositAddress)
	}
	if f.DepositMemo != nil {
		q = q.Where("deposit_memo = ?", *f.DepositMemo)
	}
	if f.ProviderRequestID != nil {
		q = q.Where("provider_request_id = ?", *f.ProviderRequestID)
	}
	if f.CreatedAfter != nil {
		q = q.Where("created_at > ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		q = q.Where("created_at < ?", *f.CreatedBefore)
	}
	if f.ExpiresAfter != nil {
		q = q.Where("expires_at > ?", *f.ExpiresAfter)
	}
	if f.ExpiresBefore != nil {
		q = q.Where("expires_at < ?", *f.ExpiresBefore)
	}
	return q
}
