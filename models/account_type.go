// Package models contains domain entities and business models for the authentication system
package models

import (
	"time"
)

type AccountType struct {
	ID          uint      `gorm:"primaryKey"                                     json:"id"`
	TypeName    string    `gorm:"type:account_type_enum;not null;uniqueIndex"    json:"type_name"`
	DisplayName string    `gorm:"size:50;not null"                               json:"display_name"`
	Description *string   `gorm:"type:text"                                      json:"description,omitempty"`
	CreatedAt   time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
	UpdatedAt   time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

func (AccountType) TableName() string {
	return "account_types"
}

// AccountTypeFilter represents filter criteria for account type queries
type AccountTypeFilter struct {
	ID            *uint
	TypeName      *string
	DisplayName   *string
	Description   *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

// Account type constants
const (
	AccountTypeIndividual         = "individual"
	AccountTypeIndependentCompany = "independent_company"
	AccountTypeMarketingAgency    = "marketing_agency"
)
