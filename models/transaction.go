package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TransactionType represents the type of transaction
type TransactionType string

const (
	TransactionTypeDeposit                     TransactionType = "deposit"                         // Wallet recharge via Atipay
	TransactionTypeWithdrawal                  TransactionType = "withdrawal"                      //
	TransactionTypeFreeze                      TransactionType = "freeze"                          // Reserve funds for pending transaction
	TransactionTypeUnfreeze                    TransactionType = "unfreeze"                        // Release frozen funds
	TransactionTypeLock                        TransactionType = "lock"                            // Lock funds (e.g., for disputes)
	TransactionTypeUnlock                      TransactionType = "unlock"                          // Unlock funds
	TransactionTypeRefund                      TransactionType = "refund"                          // Refund from failed/returned transaction
	TransactionTypeFee                         TransactionType = "fee"                             // Service fees
	TransactionTypeAdjustment                  TransactionType = "adjustment"                      // Manual balance adjustments
	TransactionTypeCredit                      TransactionType = "credit"                          // Credit provisioned to wallet
	TransactionTypeDebit                       TransactionType = "debit"                           // Debit from wallet
	TransactionTypeChargeAgencyShareWithTax    TransactionType = "charge_agency_share_with_tax"    // Charge Agency share including tax
	TransactionTypeDischargeAgencyShareWithTax TransactionType = "discharge_agency_share_with_tax" // Discharge Agency share including tax
)

// TransactionStatus represents the current status of a transaction
type TransactionStatus string

const (
	TransactionStatusPending   TransactionStatus = "pending"   // Transaction is being processed
	TransactionStatusCompleted TransactionStatus = "completed" // Transaction completed successfully
	TransactionStatusFailed    TransactionStatus = "failed"    // Transaction failed
	TransactionStatusCancelled TransactionStatus = "cancelled" // Transaction was cancelled
	TransactionStatusReversed  TransactionStatus = "reversed"  // Transaction was reversed/refunded
)

const (
	TransactionSourceIncreaseAgencyShareWithTax     = "payment_callback_increase_agency_share_with_tax"
	TransactionSourceIncreaseRealSystemShare        = "payment_callback_increase_system_locked_(real_system_share)"
	TransactionSourceIncreaseTaxSystemShare         = "payment_callback_increase_tax_locked_(tax_system_share)"
	TransactionSourceIncreaseCustomerFreePlusCredit = "payment_callback_increase_customer_free_plus_credit"
)

// Transaction represents an immutable financial transaction in the system
type Transaction struct {
	ID            uint      `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID          uuid.UUID `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CorrelationID uuid.UUID `gorm:"type:uuid;index;not null"                                 json:"correlation_id"` // Links related transactions

	// Transaction details
	Type     TransactionType   `gorm:"type:varchar(20);not null;index"                   json:"type"`
	Status   TransactionStatus `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	Amount   uint64            `gorm:"not null"                                          json:"amount"` // Amount in Tomans
	Currency string            `gorm:"type:varchar(3);not null;default:'TMN'"            json:"currency"`

	// Wallet and customer information
	WalletID   uint `gorm:"not null;index" json:"wallet_id"`
	CustomerID uint `gorm:"not null;index" json:"customer_id"`

	// Balance snapshots before and after transaction (immutable)
	BalanceBefore json.RawMessage `gorm:"type:jsonb;not null" json:"balance_before"` // Free, Frozen, Locked before
	BalanceAfter  json.RawMessage `gorm:"type:jsonb;not null" json:"balance_after"`  // Free, Frozen, Locked after

	// External payment information (for Atipay transactions)
	ExternalReference string `gorm:"type:varchar(255);index" json:"external_reference"`  // Atipay referenceNumber
	ExternalTrace     string `gorm:"type:varchar(255)"       json:"external_trace"`      // Atipay traceNumber
	ExternalRRN       string `gorm:"type:varchar(255)"       json:"external_rrn"`        // Atipay RRN
	ExternalMaskedPAN string `gorm:"type:varchar(255)"       json:"external_masked_pan"` // Masked card number

	// Transaction metadata
	Description string          `gorm:"type:text"               json:"description"`
	Metadata    json.RawMessage `gorm:"type:jsonb;default:'{}'" json:"metadata"`

	// Audit fields
	CreatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                              json:"deleted_at,omitempty"`

	// Relationships
	Wallet   Wallet   `gorm:"foreignKey:WalletID;constraint:OnDelete:CASCADE"   json:"wallet,omitempty"`
	Customer Customer `gorm:"foreignKey:CustomerID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
}

// BeforeCreate ensures UUID and CorrelationID are set
func (t *Transaction) BeforeCreate(tx *gorm.DB) error {
	if t.UUID == uuid.Nil {
		t.UUID = uuid.New()
	}
	if t.CorrelationID == uuid.Nil {
		t.CorrelationID = uuid.New()
	}
	return nil
}

// IsCompleted returns true if the transaction is in a final state
func (t *Transaction) IsCompleted() bool {
	return t.Status == TransactionStatusCompleted ||
		t.Status == TransactionStatusFailed ||
		t.Status == TransactionStatusCancelled ||
		t.Status == TransactionStatusReversed
}

// IsPending returns true if the transaction is still being processed
func (t *Transaction) IsPending() bool {
	return t.Status == TransactionStatusPending
}

// CanBeReversed returns true if the transaction can be reversed
func (t *Transaction) CanBeReversed() bool {
	return t.Status == TransactionStatusCompleted
}

// TransactionFilter represents filter criteria for transaction queries
type TransactionFilter struct {
	ID                *uint              `json:"id,omitempty"`
	UUID              *uuid.UUID         `json:"uuid,omitempty"`
	CorrelationID     *uuid.UUID         `json:"correlation_id,omitempty"`
	Type              *TransactionType   `json:"type,omitempty"`
	Status            *TransactionStatus `json:"status,omitempty"`
	Amount            *uint64            `json:"amount,omitempty"`
	Currency          *string            `json:"currency,omitempty"`
	WalletID          *uint              `json:"wallet_id,omitempty"`
	CustomerID        *uint              `json:"customer_id,omitempty"`
	CustomerName      *string            `json:"customer_name,omitempty"`
	ExternalReference *string            `json:"external_reference,omitempty"`
	CreatedAfter      *time.Time         `json:"created_after,omitempty"`
	CreatedBefore     *time.Time         `json:"created_before,omitempty"`

	// source and operation filter
	Source    *string `json:"source,omitempty"`
	Operation *string `json:"operation,omitempty"`

	// campaign filter
	CampaignID *uint `json:"campaign_id,omitempty"`
}
