package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PaymentRequestRepositoryImpl implements PaymentRequestRepository interface
type PaymentRequestRepositoryImpl struct {
	*BaseRepository[models.PaymentRequest, models.PaymentRequestFilter]
}

// NewPaymentRequestRepository creates a new payment request repository
func NewPaymentRequestRepository(db *gorm.DB) PaymentRequestRepository {
	return &PaymentRequestRepositoryImpl{
		BaseRepository: NewBaseRepository[models.PaymentRequest, models.PaymentRequestFilter](db),
	}
}

// ByID finds a payment request by ID
func (r *PaymentRequestRepositoryImpl) ByID(ctx context.Context, id uint) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var request models.PaymentRequest
	err := db.First(&request, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

// ByUUID finds a payment request by UUID
func (r *PaymentRequestRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var request models.PaymentRequest
	err := db.Where("uuid = ?", uuid).Last(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

// ByCorrelationID finds payment requests by correlation ID
func (r *PaymentRequestRepositoryImpl) ByCorrelationID(ctx context.Context, correlationID uuid.UUID) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest
	err := db.Where("correlation_id = ?", correlationID).Order("created_at DESC").Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// ByInvoiceNumber finds a payment request by invoice number
func (r *PaymentRequestRepositoryImpl) ByInvoiceNumber(ctx context.Context, invoiceNumber string) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var request models.PaymentRequest
	err := db.Where("invoice_number = ?", invoiceNumber).Last(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

// ByAtipayToken finds a payment request by Atipay token
func (r *PaymentRequestRepositoryImpl) ByAtipayToken(ctx context.Context, atipayToken string) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var request models.PaymentRequest
	err := db.Where("atipay_token = ?", atipayToken).Last(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

// ByPaymentReference finds a payment request by payment reference
func (r *PaymentRequestRepositoryImpl) ByPaymentReference(ctx context.Context, paymentReference string) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var request models.PaymentRequest
	err := db.Where("payment_reference = ?", paymentReference).Last(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &request, nil
}

// ByCustomerID finds payment requests by customer ID
func (r *PaymentRequestRepositoryImpl) ByCustomerID(
	ctx context.Context,
	customerID uint,
	limit, offset int,
) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest

	query := db.Where("customer_id = ?", customerID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// ByWalletID finds payment requests by wallet ID
func (r *PaymentRequestRepositoryImpl) ByWalletID(ctx context.Context, walletID uint, limit, offset int) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest

	query := db.Where("wallet_id = ?", walletID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// ByStatus finds payment requests by status
func (r *PaymentRequestRepositoryImpl) ByStatus(
	ctx context.Context,
	status models.PaymentRequestStatus,
	limit, offset int,
) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest

	query := db.Where("status = ?", status).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// GetPendingRequests gets pending payment requests
func (r *PaymentRequestRepositoryImpl) GetPendingRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error) {
	return r.ByStatus(ctx, models.PaymentRequestStatusPending, limit, offset)
}

// GetExpiredRequests gets expired payment requests
func (r *PaymentRequestRepositoryImpl) GetExpiredRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest

	query := db.Where("expires_at < ?", utils.UTCNow()).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	err := query.Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// GetCompletedRequests gets completed payment requests
func (r *PaymentRequestRepositoryImpl) GetCompletedRequests(ctx context.Context, limit, offset int) ([]*models.PaymentRequest, error) {
	return r.ByStatus(ctx, models.PaymentRequestStatusCompleted, limit, offset)
}

// ByFilter retrieves payment requests based on filter criteria
func (r *PaymentRequestRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.PaymentRequestFilter,
	orderBy string,
	limit, offset int,
) ([]*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var requests []*models.PaymentRequest

	query := db.Model(&models.PaymentRequest{})
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

	err := query.Find(&requests).Error
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// SaveBatch inserts multiple payment requests in a single transaction
func (r *PaymentRequestRepositoryImpl) SaveBatch(ctx context.Context, requests []*models.PaymentRequest) error {
	if len(requests) == 0 {
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

	err = db.CreateInBatches(requests, 100).Error
	if err != nil {
		return err
	}

	return nil
}

// Update updates a payment request
func (r *PaymentRequestRepositoryImpl) Update(ctx context.Context, request *models.PaymentRequest) error {
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

// LockCustomerInvoiceUUID acquires a transaction-scoped advisory lock for a deposit-receipt invoice UUID.
func (r *PaymentRequestRepositoryImpl) LockCustomerInvoiceUUID(ctx context.Context, invoiceUUID string) error {
	db := r.getDB(ctx)
	lockName := fmt.Sprintf("customer_deposit_receipt_invoice:%s", invoiceUUID)
	return db.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockName).Error
}

// IsCustomerDepositInvoiceUUIDAlreadyLinked checks if an approved deposit-receipt payment request already used invoiceUUID.
func (r *PaymentRequestRepositoryImpl) IsCustomerDepositInvoiceUUIDAlreadyLinked(ctx context.Context, invoiceUUID string) (bool, error) {
	db := r.getDB(ctx)
	var count int64
	err := db.Model(&models.PaymentRequest{}).
		Where("metadata ->> 'source' = ?", "deposit_receipt").
		Where("metadata ->> 'customer_invoice_uuid' = ?", invoiceUUID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FindAdminChargeByIdempotencyKey returns the latest admin direct-charge payment request by idempotency key.
func (r *PaymentRequestRepositoryImpl) FindAdminChargeByIdempotencyKey(
	ctx context.Context,
	idempotencyKey string,
) (*models.PaymentRequest, error) {
	db := r.getDB(ctx)
	var req models.PaymentRequest
	err := db.
		Where("metadata ->> 'source' = ?", "wallet_recharge_admin").
		Where("metadata ->> 'idempotency_key' = ?", idempotencyKey).
		Order("id DESC").
		First(&req).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

// Count returns the number of payment requests matching the filter
func (r *PaymentRequestRepositoryImpl) Count(ctx context.Context, filter models.PaymentRequestFilter) (int64, error) {
	db := r.getDB(ctx)
	var count int64

	query := db.Model(&models.PaymentRequest{})
	query = r.applyFilter(query, filter)

	err := query.Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Exists checks if any payment request matching the filter exists
func (r *PaymentRequestRepositoryImpl) Exists(ctx context.Context, filter models.PaymentRequestFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyFilter applies the filter to the query
func (r *PaymentRequestRepositoryImpl) applyFilter(query *gorm.DB, filter models.PaymentRequestFilter) *gorm.DB {
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
	if filter.WalletID != nil {
		query = query.Where("wallet_id = ?", *filter.WalletID)
	}
	if filter.Amount != nil {
		query = query.Where("amount = ?", *filter.Amount)
	}
	if filter.Currency != nil {
		query = query.Where("currency = ?", *filter.Currency)
	}
	if filter.InvoiceNumber != nil {
		query = query.Where("invoice_number = ?", *filter.InvoiceNumber)
	}
	if filter.AtipayToken != nil {
		query = query.Where("atipay_token = ?", *filter.AtipayToken)
	}
	if filter.PaymentReference != nil {
		query = query.Where("payment_reference = ?", *filter.PaymentReference)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at > ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at < ?", *filter.CreatedBefore)
	}
	if filter.ExpiresAfter != nil {
		query = query.Where("expires_at > ?", *filter.ExpiresAfter)
	}
	if filter.ExpiresBefore != nil {
		query = query.Where("expires_at < ?", *filter.ExpiresBefore)
	}
	return query
}
