package models

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// ProcessedCampaign represents a campaign prepared for sending with resolved audience
// It references the original campaign and stores the ordered audience IDs to send SMS to
// along with the last processed audience ID for resuming
// Table: processed_campaigns
// Indices: campaign_id
// Array columns use PostgreSQL biginteger[]
type ProcessedCampaign struct {
	ID             uint            `gorm:"primaryKey"                                                                                                         json:"id"`
	CampaignID     uint            `gorm:"not null;index:idx_processed_campaigns_campaign_id;uniqueIndex:uk_processed_campaigns_campaign_id,where:is_current" json:"campaign_id"`
	IsCurrent      bool            `gorm:"->;column:is_current"                                                                                               json:"-"`
	CampaignJSON   json.RawMessage `gorm:"type:jsonb;not null"                                                                                                json:"campaign_json"`
	AudienceIDs    pq.Int64Array   `gorm:"type:bigint[];not null"                                                                                             json:"audience_ids"`
	AudienceCodes  pq.StringArray  `gorm:"type:text[];not null"                                                                                               json:"audience_codes"`
	LastAudienceID *int64          `                                                                                                                          json:"last_audience_id,omitempty"`
	Statistics     json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"                                                                                   json:"statistics"`
	// Reference to the bundle-scoped audience selection snapshot used when preparing this campaign
	BundleAudienceSelectionID *uint `gorm:"index:idx_processed_campaigns_bundle_audience_selection_id"                                                         json:"bundle_audience_selection_id,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

func (ProcessedCampaign) TableName() string { return "processed_campaigns" }

// ProcessedCampaignFilter provides filter fields for repository queries
type ProcessedCampaignFilter struct {
	ID         *uint
	CampaignID *uint
	// IsCurrent explicitly selects the elected row (true) or retained history
	// (false). Nil intentionally leaves both visible for audit/history callers.
	IsCurrent     *bool
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}
