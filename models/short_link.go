package models

import "time"

// ShortLink represents a shortened link record for tracking clicks per recipient and campaign
// UID is the short unique token that maps to the original link
// PhoneNumber is now optional (nullable), CampaignID is optional
// ClientID is optional (nullable)
// UserAgent and IP are optional last-known values
type ShortLink struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	UID                 string     `gorm:"size:64;not null;uniqueIndex:uk_short_links_uid;index:idx_short_links_uid" json:"uid"`
	CampaignID          *uint      `gorm:"index:idx_short_links_campaign_id" json:"campaign_id,omitempty"`
	ClientID            *uint      `gorm:"index:idx_short_links_client_id" json:"client_id,omitempty"`
	ScenarioID          *uint      `gorm:"index:idx_short_links_scenario_id" json:"scenario_id,omitempty"`
	ScenarioName        *string    `gorm:"type:text;index:idx_short_links_scenario_name_trgm" json:"scenario_name,omitempty"`
	PhoneNumber         *string    `gorm:"size:20;index:idx_short_links_phone_number" json:"phone_number,omitempty"`
	LongLink            string     `gorm:"type:text;not null" json:"long_link"`
	ShortLink           string     `gorm:"type:text;not null" json:"short_link"`
	IsTest              bool       `gorm:"not null;default:false;index:idx_short_links_test" json:"is_test"`
	ExternalPublishedAt *time.Time `gorm:"index:idx_short_links_external_unpublished,where:external_published_at IS NULL" json:"external_published_at,omitempty"`
	// AllocationKey and AllocationPosition make the scheduler's bulk allocation
	// idempotent across client/proxy timeout ambiguity. They are nil for admin
	// and legacy short links.
	AllocationKey      *string `gorm:"size:64;index:idx_short_links_allocation_key" json:"-"`
	AllocationPosition *int    `gorm:"index:idx_short_links_allocation_key" json:"-"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_short_links_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

// TableName returns the table name for ShortLink
func (ShortLink) TableName() string { return "short_links" }

// ShortLinkFilter provides filter fields for repository queries
type ShortLinkFilter struct {
	ID               *uint
	UID              *string
	CampaignID       *uint
	ClientID         *uint
	ScenarioID       *uint
	ScenarioNameLike *string
	PhoneNumber      *string
	CreatedAfter     *time.Time
	CreatedBefore    *time.Time
}
