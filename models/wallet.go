package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Wallet represents a customer's wallet reference (immutable design)
// The actual balance state is stored in BalanceSnapshot records
type Wallet struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID       uuid.UUID `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CustomerID uint      `gorm:"not null;uniqueIndex;index"                               json:"customer_id"`

	// Metadata for additional wallet information
	Metadata json.RawMessage `gorm:"type:jsonb;default:'{}'" json:"metadata"`

	// Audit fields
	CreatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                              json:"deleted_at,omitempty"`

	// Relationships
	Customer         Customer          `gorm:"foreignKey:CustomerID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
	Transactions     []Transaction     `gorm:"foreignKey:WalletID"                               json:"transactions,omitempty"`
	BalanceSnapshots []BalanceSnapshot `gorm:"foreignKey:WalletID"                               json:"balance_snapshots,omitempty"`
}

// WalletFilter represents filter criteria for wallet queries
type WalletFilter struct {
	ID            *uint      `json:"id,omitempty"`
	UUID          *uuid.UUID `json:"uuid,omitempty"`
	CustomerID    *uint      `json:"customer_id,omitempty"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"`
	CreatedBefore *time.Time `json:"created_before,omitempty"`
}

// BeforeCreate ensures UUID is set
func (w *Wallet) BeforeCreate(tx *gorm.DB) error {
	if w.UUID == uuid.Nil {
		w.UUID = uuid.New()
	}
	return nil
}
