package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/gorm"
)

// NewDepositReceiptRepository creates a new repository for deposit receipts.
func NewDepositReceiptRepository(db *gorm.DB) DepositReceiptRepository {
	return &depositReceiptRepository{db: db}
}

type depositReceiptRepository struct {
	db *gorm.DB
}

func (r *depositReceiptRepository) Save(ctx context.Context, receipt *models.DepositReceipt) error {
	if len(receipt.FileData) > 0 {
		compressed, err := compressData(receipt.FileData)
		if err != nil {
			return err
		}
		receipt.FileData = compressed
	}
	return r.db.WithContext(ctx).Create(receipt).Error
}

func (r *depositReceiptRepository) Update(ctx context.Context, receipt *models.DepositReceipt) error {
	if len(receipt.FileData) > 0 && !isGzip(receipt.FileData) {
		compressed, err := compressData(receipt.FileData)
		if err != nil {
			return err
		}
		receipt.FileData = compressed
	}
	return r.db.WithContext(ctx).Save(receipt).Error
}

func (r *depositReceiptRepository) ByID(ctx context.Context, id uint) (*models.DepositReceipt, error) {
	var rec models.DepositReceipt
	if err := r.db.WithContext(ctx).First(&rec, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	if rec.FileData != nil {
		if decompressed, err := decompressData(rec.FileData); err == nil {
			rec.FileData = decompressed
		}
	}
	return &rec, nil
}

func (r *depositReceiptRepository) ByUUID(ctx context.Context, uuid string) (*models.DepositReceipt, error) {
	var rec models.DepositReceipt
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	if rec.FileData != nil {
		if decompressed, err := decompressData(rec.FileData); err == nil {
			rec.FileData = decompressed
		}
	}
	return &rec, nil
}

func (r *depositReceiptRepository) List(
	ctx context.Context,
	f models.DepositReceiptFilter,
	limit, offset int,
	order string,
) ([]*models.DepositReceipt, error) {
	q := r.db.WithContext(ctx).Model(&models.DepositReceipt{})
	if f.CustomerID != nil {
		q = q.Where("customer_id = ?", *f.CustomerID)
	}
	if f.CustomerName != nil {
		name := strings.TrimSpace(*f.CustomerName)
		if name != "" {
			pattern := "%" + name + "%"
			customerSubQuery := q.Session(&gorm.Session{}).
				Model(&models.Customer{}).
				Select("id").
				Where("representative_first_name ILIKE ? OR representative_last_name ILIKE ? OR company_name ILIKE ?", pattern, pattern, pattern)
			q = q.Where("customer_id IN (?)", customerSubQuery)
		}
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.Lang != nil {
		q = q.Where("lang = ?", *f.Lang)
	}
	if f.CreatedAfter != nil {
		q = q.Where("created_at >= ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		q = q.Where("created_at <= ?", *f.CreatedBefore)
	}
	if order != "" {
		q = q.Order(order)
	} else {
		q = q.Order("id DESC")
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	var res []*models.DepositReceipt
	if err := q.Find(&res).Error; err != nil {
		return nil, err
	}
	for _, rec := range res {
		if rec != nil && rec.FileData != nil {
			if decompressed, err := decompressData(rec.FileData); err == nil {
				rec.FileData = decompressed
			}
		}
	}
	return res, nil
}

// ---- compression helpers ----

func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gw.Write(data); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	out, err := io.ReadAll(gr)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}
