package models

import "time"

// SplusSendStatus enumerates status of a sent Splus message record.
type SplusSendStatus string

const (
	SplusSendStatusPending      SplusSendStatus = "pending"
	SplusSendStatusSuccessful   SplusSendStatus = "successful"
	SplusSendStatusUnsuccessful SplusSendStatus = "unsuccessful"
)

// SentSplusMessage records a single phone number delivery attempt under a processed campaign.
type SentSplusMessage struct {
	ID                  uint            `gorm:"primaryKey"                                                                             json:"id"`
	ProcessedCampaignID uint            `gorm:"not null;index:idx_sent_splus_messages_processed_campaign_id"                           json:"processed_campaign_id"`
	PhoneNumber         string          `gorm:"size:20;not null;index:idx_sent_splus_messages_phone_number"                            json:"phone_number"`
	TrackingID          string          `gorm:"size:64;not null;index:idx_sent_splus_messages_tracking_id"                             json:"tracking_id"`
	PartsDelivered      int             `gorm:"default:0"                                                                              json:"parts_delivered"`
	Status              SplusSendStatus `gorm:"type:splus_send_status;not null;default:'pending';index:idx_sent_splus_messages_status" json:"status"`

	// Splus provider response fields.
	ServerID    *string `gorm:"size:64"   json:"server_id,omitempty"`
	ErrorCode   *string `gorm:"size:64"   json:"error_code,omitempty"`
	Description *string `gorm:"type:text" json:"description,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sent_splus_messages_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                          json:"updated_at"`
}

func (SentSplusMessage) TableName() string { return "sent_splus_messages" }

// SentSplusMessageFilter provides filter fields for repository queries.
type SentSplusMessageFilter struct {
	ID                  *uint
	ProcessedCampaignID *uint
	PhoneNumber         *string
	Status              *SplusSendStatus
	CreatedAfter        *time.Time
	CreatedBefore       *time.Time
}
