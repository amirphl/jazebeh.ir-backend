package models

import (
	"encoding/json"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PaymentRequestStatus represents the status of a payment request
type PaymentRequestStatus string

const (
	PaymentRequestStatusCreated   PaymentRequestStatus = "created"   // Payment request created, waiting for Atipay token
	PaymentRequestStatusTokenized PaymentRequestStatus = "tokenized" // Atipay token received, waiting for user payment
	PaymentRequestStatusPending   PaymentRequestStatus = "pending"   // User redirected to Atipay, payment in progress
	PaymentRequestStatusCompleted PaymentRequestStatus = "completed" // Payment completed successfully
	PaymentRequestStatusFailed    PaymentRequestStatus = "failed"    // Payment failed
	PaymentRequestStatusCancelled PaymentRequestStatus = "cancelled" // User cancelled payment
	PaymentRequestStatusExpired   PaymentRequestStatus = "expired"   // Payment request expired
	PaymentRequestStatusRefunded  PaymentRequestStatus = "refunded"  // Payment was refunded
)

// PaymentRequest represents a payment request to Atipay for wallet recharge
type PaymentRequest struct {
	ID            uint      `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID          uuid.UUID `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CorrelationID uuid.UUID `gorm:"type:uuid;index;not null"                                 json:"correlation_id"` // Links related records

	// Customer and wallet information
	CustomerID uint `gorm:"not null;index" json:"customer_id"`
	WalletID   uint `gorm:"not null;index" json:"wallet_id"`

	// Payment details
	Amount      uint64 `gorm:"not null"                               json:"amount"` // Amount in Tomans
	Currency    string `gorm:"type:varchar(3);not null;default:'TMN'" json:"currency"`
	Description string `gorm:"type:text"                              json:"description"`
	Lang        string `gorm:"type:varchar(2);not null;default:'EN'"  json:"lang"`

	// Atipay request parameters
	InvoiceNumber string `gorm:"type:varchar(255);uniqueIndex;not null" json:"invoice_number"` // Merchant-side unique ID
	CellNumber    string `gorm:"type:varchar(20)"                       json:"cell_number"`    // Buyer's mobile number
	RedirectURL   string `gorm:"type:text;not null"                     json:"redirect_url"`   // Return URL after payment

	// Atipay response data
	AtipayToken  string `gorm:"type:varchar(255);index" json:"atipay_token"`  // Token from Atipay get-token
	AtipayStatus string `gorm:"type:varchar(50)"        json:"atipay_status"` // Status from Atipay

	// Payment result data (from redirect-to-gateway callback)
	PaymentState       string `gorm:"type:varchar(50)"        json:"payment_state"`       // Atipay state parameter
	PaymentStatus      string `gorm:"type:varchar(50)"        json:"payment_status"`      // Atipay status parameter
	PaymentReference   string `gorm:"type:varchar(255);index" json:"payment_reference"`   // Atipay referenceNumber
	PaymentReservation string `gorm:"type:varchar(255)"       json:"payment_reservation"` // Atipay reservationNumber
	PaymentTerminal    string `gorm:"type:varchar(255)"       json:"payment_terminal"`    // Atipay terminalId
	PaymentTrace       string `gorm:"type:varchar(255)"       json:"payment_trace"`       // Atipay traceNumber
	PaymentMaskedPAN   string `gorm:"type:varchar(255)"       json:"payment_masked_pan"`  // Atipay maskedPan
	PaymentRRN         string `gorm:"type:varchar(255)"       json:"payment_rrn"`         // Atipay RRN

	// Status tracking
	Status       PaymentRequestStatus `gorm:"type:varchar(20);not null;default:'created';index" json:"status"`
	StatusReason string               `gorm:"type:text"                                         json:"status_reason"` // Reason for status change

	// Metadata and audit
	Metadata  json.RawMessage `gorm:"type:jsonb;default:'{}'"            json:"metadata"`
	CreatedAt time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt gorm.DeletedAt  `gorm:"index"                              json:"deleted_at,omitempty"`

	// Expiration tracking
	ExpiresAt *time.Time `gorm:"index" json:"expires_at"` // When payment request expires

	// Relationships
	Customer Customer `gorm:"foreignKey:CustomerID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
	Wallet   Wallet   `gorm:"foreignKey:WalletID;constraint:OnDelete:CASCADE"   json:"wallet,omitempty"`
}

// BeforeCreate ensures UUID and CorrelationID are set
func (pr *PaymentRequest) BeforeCreate(tx *gorm.DB) error {
	if pr.UUID == uuid.Nil {
		pr.UUID = uuid.New()
	}
	if pr.CorrelationID == uuid.Nil {
		pr.CorrelationID = uuid.New()
	}
	return nil
}

// IsFinal returns true if the payment request is in a final state
func (pr *PaymentRequest) IsFinal() bool {
	return pr.Status == PaymentRequestStatusCompleted ||
		pr.Status == PaymentRequestStatusFailed ||
		pr.Status == PaymentRequestStatusCancelled ||
		pr.Status == PaymentRequestStatusExpired ||
		pr.Status == PaymentRequestStatusRefunded
}

// IsPending returns true if the payment request is still being processed
func (pr *PaymentRequest) IsPending() bool {
	return pr.Status == PaymentRequestStatusCreated ||
		pr.Status == PaymentRequestStatusTokenized ||
		pr.Status == PaymentRequestStatusPending
}

// IsExpired returns true if the payment request has expired
func (pr *PaymentRequest) IsExpired() bool {
	if pr.ExpiresAt == nil {
		return false
	}
	return utils.UTCNow().After(*pr.ExpiresAt)
}

// CanBeProcessed returns true if the payment request can still be processed
func (pr *PaymentRequest) CanBeProcessed() bool {
	return pr.IsPending() && !pr.IsExpired()
}

// PaymentRequestFilter represents filter criteria for payment request queries
type PaymentRequestFilter struct {
	ID               *uint                 `json:"id,omitempty"`
	UUID             *uuid.UUID            `json:"uuid,omitempty"`
	CorrelationID    *uuid.UUID            `json:"correlation_id,omitempty"`
	CustomerID       *uint                 `json:"customer_id,omitempty"`
	WalletID         *uint                 `json:"wallet_id,omitempty"`
	Amount           *uint64               `json:"amount,omitempty"`
	Currency         *string               `json:"currency,omitempty"`
	InvoiceNumber    *string               `json:"invoice_number,omitempty"`
	AtipayToken      *string               `json:"atipay_token,omitempty"`
	PaymentReference *string               `json:"payment_reference,omitempty"`
	Status           *PaymentRequestStatus `json:"status,omitempty"`
	CreatedAfter     *time.Time            `json:"created_after,omitempty"`
	CreatedBefore    *time.Time            `json:"created_before,omitempty"`
	ExpiresAfter     *time.Time            `json:"expires_after,omitempty"`
	ExpiresBefore    *time.Time            `json:"expires_before,omitempty"`
}
