package models

import "time"

// BaleSendStatus enumerates status of a sent Bale message record.
type BaleSendStatus string

const (
	BaleSendStatusPending      BaleSendStatus = "pending"
	BaleSendStatusSuccessful   BaleSendStatus = "successful"
	BaleSendStatusUnsuccessful BaleSendStatus = "unsuccessful"
)

// SentBaleMessage records a single phone number delivery attempt under a processed campaign.
type SentBaleMessage struct {
	ID                  uint           `gorm:"primaryKey"                                                                                                                                   json:"id"`
	ProcessedCampaignID uint           `gorm:"not null;index:idx_sent_bale_messages_processed_campaign_id;uniqueIndex:uk_sent_bale_messages_processed_tracking,priority:1,where:is_current" json:"processed_campaign_id"`
	IsCurrent           bool           `gorm:"->;column:is_current"                                                                                                                         json:"-"`
	PhoneNumber         string         `gorm:"size:20;not null;index:idx_sent_bale_messages_phone_number"                                                                                   json:"phone_number"`
	TrackingID          string         `gorm:"size:64;not null;index:idx_sent_bale_messages_tracking_id;uniqueIndex:uk_sent_bale_messages_processed_tracking,priority:2,where:is_current"   json:"tracking_id"`
	PartsDelivered      int            `gorm:"default:0"                                                                                                                                    json:"parts_delivered"`
	Status              BaleSendStatus `gorm:"type:bale_send_status;not null;default:'pending';index:idx_sent_bale_messages_status"                                                         json:"status"`

	// Bale provider response fields (optional, populated after provider acknowledgement).
	ServerID    *string `gorm:"size:64"   json:"server_id,omitempty"`
	ErrorCode   *string `gorm:"size:64"   json:"error_code,omitempty"`
	Description *string `gorm:"type:text" json:"description,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sent_bale_messages_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                         json:"updated_at"`
}

func (SentBaleMessage) TableName() string { return "sent_bale_messages" }

// SentBaleMessageFilter provides filter fields for repository queries.
type SentBaleMessageFilter struct {
	ID                  *uint
	ProcessedCampaignID *uint
	// IsCurrent explicitly selects the elected row (true) or retained history
	// (false). Nil intentionally leaves both visible for audit/history callers.
	IsCurrent     *bool
	PhoneNumber   *string
	Status        *BaleSendStatus
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}
