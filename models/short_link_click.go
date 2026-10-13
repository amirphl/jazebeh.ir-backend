package models

import "time"

// ShortLinkClick represents a single click event on a short link
// We keep a reference to short_links via ShortLinkID
// UserAgent and IP capture click-time context
type ShortLinkClick struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	ShortLinkID        uint       `gorm:"index:idx_short_link_clicks_short_link_id;not null" json:"short_link_id"`
	UID                *string    `gorm:"size:64;index:idx_short_link_clicks_uid" json:"uid,omitempty"`
	CampaignID         *uint      `gorm:"index:idx_short_link_clicks_campaign_id" json:"campaign_id,omitempty"`
	ClientID           *uint      `gorm:"index:idx_short_link_clicks_client_id" json:"client_id,omitempty"`
	ScenarioID         *uint      `gorm:"index:idx_short_link_clicks_scenario_id" json:"scenario_id,omitempty"`
	ScenarioName       *string    `gorm:"type:text;index:idx_short_link_clicks_scenario_name_trgm" json:"scenario_name,omitempty"`
	PhoneNumber        *string    `gorm:"size:20;index:idx_short_link_clicks_phone_number" json:"phone_number,omitempty"`
	LongLink           *string    `gorm:"type:text" json:"long_link,omitempty"`
	ShortLink          *string    `gorm:"type:text" json:"short_link,omitempty"`
	IsTest             bool       `gorm:"not null;default:false;index:idx_short_link_clicks_test" json:"is_test"`
	ShortLinkCreatedAt *time.Time `gorm:"column:short_link_created_at" json:"short_link_created_at,omitempty"`
	ShortLinkUpdatedAt *time.Time `gorm:"column:short_link_updated_at" json:"short_link_updated_at,omitempty"`
	UserAgent          *string    `gorm:"type:text" json:"user_agent,omitempty"`
	IP                 *string    `gorm:"size:64" json:"ip,omitempty"`
	Referer            *string    `gorm:"type:text" json:"referer,omitempty"`
	Source             string     `gorm:"size:64;not null;default:local;uniqueIndex:uk_short_link_clicks_source_external_id,priority:1" json:"source"`
	ExternalClickID    *int64     `gorm:"uniqueIndex:uk_short_link_clicks_source_external_id,priority:2" json:"external_click_id,omitempty"`
	CreatedAt          time.Time  `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_short_link_clicks_created_at" json:"created_at"`
}

// TableName returns the table name for ShortLinkClick
func (ShortLinkClick) TableName() string { return "short_link_clicks" }

const ShortLinkClickSourceExternal = "external_shortlink"

// ExternalShortLinkSyncState is the durable ID cursor for a click source.
type ExternalShortLinkSyncState struct {
	Source      string    `gorm:"primaryKey;size:64" json:"source"`
	LastClickID int64     `gorm:"not null" json:"last_click_id"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (ExternalShortLinkSyncState) TableName() string { return "external_short_link_sync_state" }

// ExternalShortLinkClickDeadLetter preserves an invalid external event while
// allowing the ordered importer to advance past it. The uniqueness below is a
// partial index on positive click IDs, so a page retry cannot create duplicates
// while a row whose ID is missing or non-positive - the one case that has no
// cursor value to advance past - is still retained instead of dropped.
type ExternalShortLinkClickDeadLetter struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Source          string    `gorm:"size:64;not null;uniqueIndex:uk_external_short_link_dead_letters_source_click,priority:1,where:external_click_id > 0" json:"source"`
	ExternalClickID int64     `gorm:"not null;uniqueIndex:uk_external_short_link_dead_letters_source_click,priority:2" json:"external_click_id"`
	Payload         string    `gorm:"type:jsonb;not null" json:"payload"`
	Reason          string    `gorm:"type:text;not null" json:"reason"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
}

func (ExternalShortLinkClickDeadLetter) TableName() string {
	return "external_short_link_click_dead_letters"
}

// ExternalShortLinkClick is the wire representation fetched from the external service.
type ExternalShortLinkClick struct {
	ClickID       int64      `json:"click_id"`
	EventID       string     `json:"event_id"`
	ShortCode     string     `json:"short_code"`
	LinkID        int64      `json:"link_id"`
	LongURL       string     `json:"long_url"`
	ShortURL      *string    `json:"short_url,omitempty"`
	SourceLinkID  *int64     `json:"source_link_id,omitempty"`
	CampaignID    *uint      `json:"campaign_id,omitempty"`
	ClientID      *uint      `json:"client_id,omitempty"`
	ScenarioID    *uint      `json:"scenario_id,omitempty"`
	ScenarioName  *string    `json:"scenario_name,omitempty"`
	PhoneNumber   *string    `json:"phone_number,omitempty"`
	IsTest        bool       `json:"is_test"`
	LinkCreatedAt *time.Time `json:"link_created_at,omitempty"`
	LinkUpdatedAt *time.Time `json:"link_updated_at,omitempty"`
	ClickedAt     time.Time  `json:"clicked_at"`
	ClientIP      *string    `json:"client_ip,omitempty"`
	UserAgent     *string    `json:"user_agent,omitempty"`
	Referer       *string    `json:"referer,omitempty"`
}
