package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BalanceSnapshot represents an immutable snapshot of wallet balances at a point in time
// This is the source of truth for wallet balance state - immutable and never updated
type BalanceSnapshot struct {
	ID            uint      `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID          uuid.UUID `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CorrelationID uuid.UUID `gorm:"type:uuid;index;not null"                                 json:"correlation_id"` // Links to transaction

	// Wallet information
	WalletID   uint `gorm:"not null;index" json:"wallet_id"`
	CustomerID uint `gorm:"not null;index" json:"customer_id"`

	// Balance snapshot (immutable - this is the source of truth)
	FreeBalance        uint64 `gorm:"not null" json:"free_balance"`                    // Available for spending
	FrozenBalance      uint64 `gorm:"not null" json:"frozen_balance"`                  // Reserved for pending operations
	LockedBalance      uint64 `gorm:"not null" json:"locked_balance"`                  // Temporarily locked (e.g., for disputes)
	CreditBalance      uint64 `gorm:"not null" json:"credit_balance"`                  // Credit amount provisioned (not necessarily spendable)
	SpentOnCampaign    uint64 `gorm:"not null" json:"spent_on_campaign"`               // Spent on campaigns
	AgencyShareWithTax uint64 `gorm:"not null" json:"agency_share_with_tax,omitempty"` // Agency share including tax
	TotalBalance       uint64 `gorm:"not null" json:"total_balance"`                   // Calculated field (free + frozen + locked)

	// Snapshot metadata
	Reason      string          `gorm:"type:varchar(100);not null" json:"reason"` // e.g., "transaction_created", "daily_snapshot"
	Description string          `gorm:"type:text"                  json:"description"`
	Metadata    json.RawMessage `gorm:"type:jsonb;default:'{}'"    json:"metadata"`

	// Audit fields
	CreatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                              json:"deleted_at,omitempty"`

	// Relationships
	Wallet   Wallet   `gorm:"foreignKey:WalletID;constraint:OnDelete:CASCADE"   json:"wallet,omitempty"`
	Customer Customer `gorm:"foreignKey:CustomerID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
}

// BeforeCreate ensures UUID and CorrelationID are set, and calculates total balance
func (bs *BalanceSnapshot) BeforeCreate(tx *gorm.DB) error {
	if bs.UUID == uuid.Nil {
		bs.UUID = uuid.New()
	}
	if bs.CorrelationID == uuid.Nil {
		bs.CorrelationID = uuid.New()
	}

	// Calculate total balance
	bs.TotalBalance = bs.FreeBalance + bs.FrozenBalance + bs.LockedBalance + bs.CreditBalance + bs.SpentOnCampaign + bs.AgencyShareWithTax

	return nil
}

// GetBalanceMap returns a map representation of balances
func (bs *BalanceSnapshot) GetBalanceMap() (json.RawMessage, error) {
	balanceMap := map[string]uint64{
		"free":                  bs.FreeBalance,
		"frozen":                bs.FrozenBalance,
		"locked":                bs.LockedBalance,
		"credit":                bs.CreditBalance,
		"spent_on_campaign":     bs.SpentOnCampaign,
		"agency_share_with_tax": bs.AgencyShareWithTax,
		"total":                 bs.TotalBalance,
	}

	jsonData, err := json.Marshal(balanceMap)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

// GetAvailableBalance returns the balance available for new transactions
func (bs *BalanceSnapshot) GetAvailableBalance() uint64 {
	return bs.FreeBalance
}

// IsBalanceSufficient checks if the wallet has sufficient free balance for an amount
func (bs *BalanceSnapshot) IsBalanceSufficient(amount uint64) bool {
	return bs.FreeBalance >= amount
}

// GetTotalBalance returns the total balance (free + frozen + locked)
func (bs *BalanceSnapshot) GetTotalBalance() uint64 {
	return bs.TotalBalance
}

// TableName specifies the table name for GORM
func (BalanceSnapshot) TableName() string {
	return "balance_snapshots"
}

// BalanceSnapshotFilter represents filter criteria for balance snapshot queries
type BalanceSnapshotFilter struct {
	ID            *uint      `json:"id,omitempty"`
	UUID          *uuid.UUID `json:"uuid,omitempty"`
	CorrelationID *uuid.UUID `json:"correlation_id,omitempty"`
	WalletID      *uint      `json:"wallet_id,omitempty"`
	CustomerID    *uint      `json:"customer_id,omitempty"`
	Reason        *string    `json:"reason,omitempty"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"`
	CreatedBefore *time.Time `json:"created_before,omitempty"`
}
