// Package models contains domain entities and business models for the authentication system
package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AgencyDiscount struct {
	ID         uint      `gorm:"primaryKey"                                              json:"id"`
	UUID       uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:uk_agency_discounts_uuid" json:"uuid"`
	AgencyID   uint      `gorm:"not null;index:idx_agency_discounts_agency_id"           json:"agency_id"`
	CustomerID uint      `gorm:"not null;index:idx_agency_discounts_customer_id"         json:"customer_id"`
	// DiscountRate must be between 0 and 0.5 inclusive
	DiscountRate float64 `gorm:"type:numeric(5,4);not null"                              json:"discount_rate"`
	// ExpiresAt can be null to indicate the discount does not expire
	ExpiresAt *time.Time `                                                               json:"expires_at,omitempty"`

	// Optional fields
	Reason   *string         `gorm:"size:255"   json:"reason,omitempty"`
	Metadata json.RawMessage `gorm:"type:jsonb" json:"metadata,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

func (AgencyDiscount) TableName() string {
	return "agency_discounts"
}

// BeforeCreate ensures UUID is set for AgencyDiscount
func (a *AgencyDiscount) BeforeCreate(tx *gorm.DB) error {
	if a.UUID == uuid.Nil {
		a.UUID = uuid.New()
	}
	return nil
}

// AgencyDiscountFilter represents filter criteria for agency discount queries
type AgencyDiscountFilter struct {
	ID            *uint      `json:"id,omitempty"`
	UUID          *uuid.UUID `json:"uuid,omitempty"`
	AgencyID      *uint      `json:"agency_id,omitempty"`
	CustomerID    *uint      `json:"customer_id,omitempty"`
	DiscountRate  *float64   `json:"discount_rate,omitempty"`
	IsActive      *bool      `json:"is_active,omitempty"`
	ExpiresAfter  *time.Time `json:"expires_after,omitempty"`
	ExpiresBefore *time.Time `json:"expires_before,omitempty"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"`
	CreatedBefore *time.Time `json:"created_before,omitempty"`
}
