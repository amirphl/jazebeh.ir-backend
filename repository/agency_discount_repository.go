// Package repository provides data access layer implementations and interfaces for database operations
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"gorm.io/gorm"
)

type AgencyDiscountRepositoryImpl struct {
	*BaseRepository[models.AgencyDiscount, models.AgencyDiscountFilter]
}

func NewAgencyDiscountRepository(db *gorm.DB) AgencyDiscountRepository {
	return &AgencyDiscountRepositoryImpl{
		BaseRepository: NewBaseRepository[models.AgencyDiscount, models.AgencyDiscountFilter](db),
	}
}

// AgencyDiscountWithCustomer is a projection row joining discount and customer info
type AgencyDiscountWithCustomer struct {
	DiscountID              uint       `gorm:"column:discount_id"`
	DiscountUUID            string     `gorm:"column:discount_uuid"`
	AgencyID                uint       `gorm:"column:agency_id"`
	CustomerID              uint       `gorm:"column:customer_id"`
	CustomerUUID            string     `gorm:"column:customer_uuid"`
	RepresentativeFirstName string     `gorm:"column:representative_first_name"`
	RepresentativeLastName  string     `gorm:"column:representative_last_name"`
	CompanyName             *string    `gorm:"column:company_name"`
	DiscountRate            float64    `gorm:"column:discount_rate"`
	CreatedAt               time.Time  `gorm:"column:created_at"`
	ExpiresAt               *time.Time `gorm:"column:expires_at"`
}

func (r *AgencyDiscountRepositoryImpl) ByID(ctx context.Context, id uint) (*models.AgencyDiscount, error) {
	db := r.getDB(ctx)
	var ad models.AgencyDiscount
	if err := db.Last(&ad, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ad, nil
}

func (r *AgencyDiscountRepositoryImpl) ByUUID(ctx context.Context, uuid string) (*models.AgencyDiscount, error) {
	db := r.getDB(ctx)
	parsed, err := utils.ParseUUID(uuid)
	if err != nil {
		return nil, err
	}
	var ad models.AgencyDiscount
	if err := db.Where("uuid = ?", parsed).Last(&ad).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ad, nil
}

func (r *AgencyDiscountRepositoryImpl) ByAgencyAndCustomer(
	ctx context.Context,
	agencyID, customerID uint,
) ([]*models.AgencyDiscount, error) {
	filter := models.AgencyDiscountFilter{AgencyID: &agencyID, CustomerID: &customerID}
	return r.ByFilter(ctx, filter, "id DESC", 0, 0)
}

func (r *AgencyDiscountRepositoryImpl) GetActiveDiscount(ctx context.Context, agencyID, customerID uint) (*models.AgencyDiscount, error) {
	db := r.getDB(ctx)
	var ad models.AgencyDiscount
	if err := db.Where("agency_id = ? AND customer_id = ? AND (expires_at IS NULL OR expires_at > ?)", agencyID, customerID, utils.UTCNow()).
		Order("id DESC").
		Last(&ad).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ad, nil
}

// ExpireActiveByAgencyAndCustomer sets expires_at for all currently non-expired discounts of an agency for a customer
func (r *AgencyDiscountRepositoryImpl) ExpireActiveByAgencyAndCustomer(
	ctx context.Context,
	agencyID, customerID uint,
	expiredAt time.Time,
) error {
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

	res := db.Model(&models.AgencyDiscount{}).
		Where("agency_id = ? AND customer_id = ? AND (expires_at IS NULL OR expires_at > ?)", agencyID, customerID, utils.UTCNow()).
		Update("expires_at", expiredAt)
	if res.Error != nil {
		return res.Error
	}
	return nil
}

// ListActiveDiscountsWithCustomer returns active (non-expired) discounts of an agency joined with customer info
// Optional nameLike filters by representative first+last name using ILIKE
func (r *AgencyDiscountRepositoryImpl) ListActiveDiscountsWithCustomer(
	ctx context.Context,
	agencyID uint,
	nameLike, orderBy string,
) ([]*AgencyDiscountWithCustomer, error) {
	db := r.getDB(ctx)

	// Sanitize orderBy to a small whitelist to avoid SQL injection
	allowed := map[string]string{
		"created_desc": "ad.created_at DESC",
		"created_asc":  "ad.created_at ASC",
		"name_asc":     "c.representative_first_name ASC, c.representative_last_name ASC, c.company_name ASC",
		"name_desc":    "c.representative_first_name DESC, c.representative_last_name DESC, c.company_name DESC",
		"rate_desc":    "ad.discount_rate DESC",
		"rate_asc":     "ad.discount_rate ASC",
	}
	order := allowed[orderBy]
	if order == "" {
		order = "ad.created_at DESC"
	}

	q := db.Table("agency_discounts AS ad").
		Select(`ad.id AS discount_id,
			ad.uuid::text AS discount_uuid,
			ad.agency_id AS agency_id,
			ad.customer_id AS customer_id,
			c.uuid::text AS customer_uuid,
			c.representative_first_name AS representative_first_name,
			c.representative_last_name AS representative_last_name,
			c.company_name AS company_name,
			ad.discount_rate AS discount_rate,
			ad.created_at AS created_at,
			ad.expires_at AS expires_at`).
		Joins("JOIN customers c ON c.id = ad.customer_id").
		Where("ad.agency_id = ? AND (ad.expires_at IS NULL OR ad.expires_at > ?)", agencyID, utils.UTCNow())

	if trimmed := strings.TrimSpace(nameLike); trimmed != "" {
		pattern := "%" + strings.ToLower(trimmed) + "%"
		q = q.Where(`
				LOWER(COALESCE(c.representative_first_name, '')) LIKE ? OR
				LOWER(COALESCE(c.representative_last_name, '')) LIKE ? OR
				LOWER(COALESCE(c.company_name, '')) LIKE ?
			`, pattern, pattern, pattern)
	}

	q = q.Order(order)

	var rows []*AgencyDiscountWithCustomer
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ByFilter retrieves records matching filter with ordering/pagination
func (r *AgencyDiscountRepositoryImpl) ByFilter(
	ctx context.Context,
	filter models.AgencyDiscountFilter,
	orderBy string,
	limit, offset int,
) ([]*models.AgencyDiscount, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AgencyDiscount{})

	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.AgencyID != nil {
		query = query.Where("agency_id = ?", *filter.AgencyID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.DiscountRate != nil {
		query = query.Where("discount_rate = ?", *filter.DiscountRate)
	}
	if filter.IsActive != nil && *filter.IsActive {
		query = query.Where("expires_at IS NULL OR expires_at > ?", utils.UTCNow())
	}
	if filter.ExpiresAfter != nil {
		query = query.Where("expires_at >= ?", *filter.ExpiresAfter)
	}
	if filter.ExpiresBefore != nil {
		query = query.Where("expires_at <= ?", *filter.ExpiresBefore)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at >= ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at <= ?", *filter.CreatedBefore)
	}

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

	var rows []*models.AgencyDiscount
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Count counts rows by filter
func (r *AgencyDiscountRepositoryImpl) Count(ctx context.Context, filter models.AgencyDiscountFilter) (int64, error) {
	db := r.getDB(ctx)
	query := db.Model(&models.AgencyDiscount{})

	if filter.ID != nil {
		query = query.Where("id = ?", *filter.ID)
	}
	if filter.UUID != nil {
		query = query.Where("uuid = ?", *filter.UUID)
	}
	if filter.AgencyID != nil {
		query = query.Where("agency_id = ?", *filter.AgencyID)
	}
	if filter.CustomerID != nil {
		query = query.Where("customer_id = ?", *filter.CustomerID)
	}
	if filter.DiscountRate != nil {
		query = query.Where("discount_rate = ?", *filter.DiscountRate)
	}
	if filter.IsActive != nil && *filter.IsActive {
		query = query.Where("expires_at IS NULL OR expires_at > ?", utils.UTCNow())
	}
	if filter.ExpiresAfter != nil {
		query = query.Where("expires_at >= ?", *filter.ExpiresAfter)
	}
	if filter.ExpiresBefore != nil {
		query = query.Where("expires_at <= ?", *filter.ExpiresBefore)
	}
	if filter.CreatedAfter != nil {
		query = query.Where("created_at >= ?", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query = query.Where("created_at <= ?", *filter.CreatedBefore)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Exists reports if any row matches filter
func (r *AgencyDiscountRepositoryImpl) Exists(ctx context.Context, filter models.AgencyDiscountFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}
