// Package models contains domain entities and business models for the authentication system
package models

import (
	"time"

	"github.com/google/uuid"
)

type SMSProvider string

const (
	SMSProviderPayamSMS SMSProvider = "payamsms"
	SMSProviderCandoo   SMSProvider = "candoo"
	SMSProviderAsiaTech SMSProvider = "asiatech"
)

func IsValidSMSProvider(provider SMSProvider) bool {
	switch provider {
	case SMSProviderPayamSMS, SMSProviderCandoo, SMSProviderAsiaTech:
		return true
	default:
		return false
	}
}

// LineNumber represents a sender line with pricing factor and optional priority
// Used by campaign and messaging subsystems for cost calculation and selection
// Table: line_numbers
// Unique by LineNumber value
// Indices on uuid, is_active, created_at, priority
// Timestamps default to UTC at DB level
// PriceFactor is a multiplier applied to base price (e.g., 1.1000)
type LineNumber struct {
	ID   uint      `gorm:"primaryKey" json:"id"`
	UUID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:uk_line_numbers_uuid;index:idx_line_numbers_uuid" json:"uuid"`

	Name        *string     `gorm:"size:255" json:"name,omitempty"`
	LineNumber  string      `gorm:"size:20;not null;uniqueIndex:uk_line_numbers_value;index:idx_line_numbers_value" json:"line_number"`
	Provider    SMSProvider `gorm:"size:32;not null;default:'payamsms'" json:"provider"`
	PriceFactor float64     `gorm:"type:numeric(10,4);not null" json:"price_factor"`
	Priority    *int        `gorm:"index:idx_line_numbers_priority" json:"priority,omitempty"`

	IsActive  *bool     `gorm:"default:true;index:idx_line_numbers_is_active" json:"is_active"`
	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_line_numbers_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

func (LineNumber) TableName() string {
	return "line_numbers"
}

// LineNumberFilter represents filter criteria for line number queries
type LineNumberFilter struct {
	ID            *uint
	UUID          *uuid.UUID
	Name          *string
	LineNumber    *string
	Provider      *SMSProvider
	IsActive      *bool
	Priority      *int
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}
