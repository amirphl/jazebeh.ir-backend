package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DepositReceiptStatus enumerates lifecycle states for manual deposit receipts.
type DepositReceiptStatus string

const (
	DepositReceiptStatusPending  DepositReceiptStatus = "pending"
	DepositReceiptStatusApproved DepositReceiptStatus = "approved"
	DepositReceiptStatusRejected DepositReceiptStatus = "rejected"
)

// DepositReceipt represents a user-submitted offline deposit (receipt upload).
// No FK to payment entities is created; receipt_uuid can be referenced in payment metadata.
type DepositReceipt struct {
	ID            uint                 `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID          uuid.UUID            `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CustomerID    uint                 `gorm:"index;not null"                                           json:"customer_id"`
	Amount        uint64               `gorm:"not null"                                                 json:"amount"`
	Currency      string               `gorm:"type:varchar(3);not null;default:'TMN'"                   json:"currency"`
	Status        DepositReceiptStatus `gorm:"type:varchar(16);index;not null;default:'pending'"        json:"status"`
	StatusReason  string               `gorm:"type:text"                                                json:"status_reason"`
	ReviewerID    *uint                `gorm:"index"                                                    json:"reviewer_id,omitempty"`
	RejectionNote *string              `gorm:"type:text"                                                json:"rejection_note,omitempty"`

	FileName    string `gorm:"type:varchar(255);not null" json:"file_name"`
	ContentType string `gorm:"type:varchar(120);not null" json:"content_type"`
	FileSize    int64  `gorm:"not null"                   json:"file_size"`
	FileData    []byte `gorm:"type:bytea;not null"        json:"-"`

	Lang          string          `gorm:"type:varchar(2);not null;default:'EN'"  json:"lang"`
	InvoiceNumber string          `gorm:"type:varchar(255);uniqueIndex;not null" json:"invoice_number"`
	Metadata      json.RawMessage `gorm:"type:jsonb;default:'{}'"                json:"metadata"`
	CreatedAt     time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP"     json:"created_at"`
	UpdatedAt     time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP"     json:"updated_at"`
	DeletedAt     gorm.DeletedAt  `gorm:"index"                                  json:"deleted_at,omitempty"`
}

func (d *DepositReceipt) BeforeCreate(tx *gorm.DB) error {
	if d.UUID == uuid.Nil {
		d.UUID = uuid.New()
	}
	return nil
}

// DepositReceiptFilter provides filtering options for queries.
type DepositReceiptFilter struct {
	CustomerID    *uint
	CustomerName  *string
	Status        *DepositReceiptStatus
	Lang          *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}
