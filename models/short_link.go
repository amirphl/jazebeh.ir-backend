package models

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

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
	// AllocationKey and AllocationPosition make allocator-backed creation
	// idempotent across client/proxy timeout ambiguity. They are nil only for
	// historical rows created before the fixed-width allocator.
	AllocationKey      *string `gorm:"size:64;index:idx_short_links_allocation_key" json:"-"`
	AllocationPosition *int    `gorm:"index:idx_short_links_allocation_key" json:"-"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_short_links_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

// TableName returns the table name for ShortLink
func (ShortLink) TableName() string { return "short_links" }

// BeforeCreate rejects writes which do not originate from the allocator
// contract. It protects direct GORM writes; the repository also applies the
// same check before its COPY fast path, which does not run GORM callbacks.
func (s *ShortLink) BeforeCreate(*gorm.DB) error {
	return validateNewShortLink(s)
}

func validateNewShortLink(s *ShortLink) error {
	if s == nil {
		return fmt.Errorf("short-link row is required")
	}
	if len(s.UID) != ShortLinkUIDLength {
		return fmt.Errorf("new short-link UID must be %d characters", ShortLinkUIDLength)
	}
	for index := 0; index < len(s.UID); index++ {
		char := s.UID[index]
		if char < '0' || char > '9' && char < 'a' || char > 'z' {
			return fmt.Errorf("new short-link UID must be lowercase base36")
		}
	}
	if s.AllocationKey == nil || len(strings.TrimSpace(*s.AllocationKey)) != 64 {
		return fmt.Errorf("new short-link requires an allocator allocation key")
	}
	if s.AllocationPosition == nil || *s.AllocationPosition < 0 {
		return fmt.Errorf("new short-link requires a non-negative allocator allocation position")
	}
	return nil
}

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
