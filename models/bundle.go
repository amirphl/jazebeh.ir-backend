package models

import (
	"encoding/json"
	"time"
)

type Bundle struct {
	ID                    uint            `gorm:"primaryKey"                                     json:"id"`
	Title                 string          `gorm:"type:varchar(255);not null"                     json:"title"`
	Objective             string          `gorm:"type:varchar(1023);not null"                    json:"objective"`
	TargetAudiencePersona string          `gorm:"type:varchar(1023);not null"                    json:"target_audience_persona"`
	Adlink                *string         `gorm:"type:varchar(2047)"                             json:"adlink,omitempty"`
	Description           *string         `gorm:"type:varchar(2047)"                             json:"description,omitempty"`
	ShortLinkDomain       *string         `gorm:"type:varchar(255)"                              json:"short_link_domain,omitempty"`
	TargetCustomerName    *string         `gorm:"type:varchar(255)"                              json:"target_customer_name,omitempty"`
	Category              *string         `gorm:"type:varchar(255)"                              json:"category,omitempty"`
	Job                   *string         `gorm:"type:varchar(255)"                              json:"job,omitempty"`
	Metadata              json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"               json:"metadata"`
	Statistics            json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"               json:"statistics"`
	CustomerID            uint            `gorm:"not null"                                       json:"customer_id"`
	CreatedAt             time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
	UpdatedAt             time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`

	Customer *Customer `gorm:"foreignKey:CustomerID;references:ID" json:"customer,omitempty"`
}

func (Bundle) TableName() string {
	return "bundles"
}

type BundleFilter struct {
	ID                    *uint
	CustomerID            *uint
	Title                 *string
	TargetAudiencePersona *string
	TargetCustomerName    *string
	CreatedAfter          *time.Time
	CreatedBefore         *time.Time
	UpdatedAfter          *time.Time
	UpdatedBefore         *time.Time
}
