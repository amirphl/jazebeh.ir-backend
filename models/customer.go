// Package models contains domain entities and business models for the authentication system
package models

import (
	"time"

	"github.com/google/uuid"
)

type Customer struct {
	ID                uint        `gorm:"primaryKey"                                                                                             json:"id"`
	UUID              uuid.UUID   `gorm:"type:uuid;not null;uniqueIndex:uk_customers_uuid;index:idx_customers_uuid"                              json:"uuid"`
	AgencyRefererCode string      `gorm:"size:255;not null;uniqueIndex:uk_customers_agency_referer_code;index:idx_customers_agency_referer_code" json:"agency_referer_code"`
	AccountTypeID     uint        `gorm:"not null;index:idx_customers_account_type_id"                                                           json:"account_type_id"`
	AccountType       AccountType `gorm:"foreignKey:AccountTypeID;references:ID"                                                                 json:"account_type,omitempty"`

	// Company fields (required for independent_company and marketing_agency)
	CompanyName    *string `gorm:"size:60"  json:"company_name,omitempty"`
	NationalID     *string `gorm:"size:11"  json:"national_id,omitempty"`
	CompanyPhone   *string `gorm:"size:20"  json:"company_phone,omitempty"`
	CompanyAddress *string `gorm:"size:255" json:"company_address,omitempty"`
	PostalCode     *string `gorm:"size:10"  json:"postal_code,omitempty"`

	// Representative/Individual fields (required for all types)
	RepresentativeFirstName string `gorm:"size:255;not null"                                                json:"representative_first_name"`
	RepresentativeLastName  string `gorm:"size:255;not null"                                                json:"representative_last_name"`
	RepresentativeMobile    string `gorm:"size:15;not null;uniqueIndex:idx_customers_representative_mobile" json:"representative_mobile"`

	// Common fields (required for all types)
	Email        string  `gorm:"size:255;not null;uniqueIndex:idx_customers_email" json:"email"`
	PasswordHash string  `gorm:"size:255;not null"                                 json:"-"` // Never serialize password hash
	ShebaNumber  *string `gorm:"size:255"                                          json:"sheba_number,omitempty"`
	Job          *string `gorm:"size:255"                                          json:"job,omitempty"`
	Category     *string `gorm:"size:255"                                          json:"category,omitempty"`

	// Agency relationship (optional for individuals and independent companies)
	ReferrerAgencyID *uint     `gorm:"index:idx_customers_referrer_agency_id"    json:"referrer_agency_id,omitempty"`
	ReferrerAgency   *Customer `gorm:"foreignKey:ReferrerAgencyID;references:ID" json:"referrer_agency,omitempty"`

	// Status and verification
	IsEmailVerified  *bool `gorm:"default:false"                              json:"is_email_verified"`
	IsMobileVerified *bool `gorm:"default:false"                              json:"is_mobile_verified"`
	IsActive         *bool `gorm:"default:true;index:idx_customers_is_active" json:"is_active"`

	// Timestamps
	CreatedAt        time.Time  `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_customers_created_at" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                json:"updated_at"`
	EmailVerifiedAt  *time.Time `                                                                                     json:"email_verified_at,omitempty"`
	MobileVerifiedAt *time.Time `                                                                                     json:"mobile_verified_at,omitempty"`
	LastLoginAt      *time.Time `gorm:"index:idx_customers_last_login_at"                                             json:"last_login_at,omitempty"`

	// Relations
	Sessions          []CustomerSession `gorm:"foreignKey:CustomerID"       json:"-"`
	AuditLogs         []AuditLog        `gorm:"foreignKey:CustomerID"       json:"-"`
	Wallet            *Wallet           `gorm:"foreignKey:CustomerID"       json:"wallet,omitempty"`
	ReferredCustomers []Customer        `gorm:"foreignKey:ReferrerAgencyID" json:"referred_customers,omitempty"`
}

func (Customer) TableName() string {
	return "customers"
}

// CustomerFilter represents filter criteria for customer queries
type CustomerFilter struct {
	ID                   *uint
	UUID                 *uuid.UUID
	AgencyRefererCode    *string
	AccountTypeID        *uint
	AccountTypeName      *string
	Email                *string
	RepresentativeMobile *string
	CompanyName          *string
	NationalID           *string
	ReferrerAgencyID     *uint
	IsEmailVerified      *bool
	IsMobileVerified     *bool
	IsActive             *bool
	CreatedAfter         *time.Time
	CreatedBefore        *time.Time
	LastLoginAfter       *time.Time
	LastLoginBefore      *time.Time
}

func (c *Customer) IsIndividual() bool {
	return c.AccountType.TypeName == AccountTypeIndividual
}

func (c *Customer) IsCompany() bool {
	return c.AccountType.TypeName == AccountTypeIndependentCompany
}

func (c *Customer) IsAgency() bool {
	return c.AccountType.TypeName == AccountTypeMarketingAgency
}

func (c *Customer) RequiresCompanyFields() bool {
	return c.IsCompany() || c.IsAgency()
}
