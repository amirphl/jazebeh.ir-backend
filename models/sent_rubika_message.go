package models

import "time"

// RubikaSendStatus enumerates status of a sent Rubika message record.
type RubikaSendStatus string

const (
	RubikaSendStatusPending      RubikaSendStatus = "pending"
	RubikaSendStatusSuccessful   RubikaSendStatus = "successful"
	RubikaSendStatusUnsuccessful RubikaSendStatus = "unsuccessful"
)

// SentRubikaMessage records a single phone number delivery attempt under a processed campaign.
type SentRubikaMessage struct {
	ID                  uint             `gorm:"primaryKey"                                                                               json:"id"`
	ProcessedCampaignID uint             `gorm:"not null;index:idx_sent_rubika_messages_processed_campaign_id"                            json:"processed_campaign_id"`
	PhoneNumber         string           `gorm:"size:20;not null;index:idx_sent_rubika_messages_phone_number"                             json:"phone_number"`
	TrackingID          string           `gorm:"size:64;not null;index:idx_sent_rubika_messages_tracking_id"                              json:"tracking_id"`
	PartsDelivered      int              `gorm:"default:0"                                                                                json:"parts_delivered"`
	Status              RubikaSendStatus `gorm:"type:rubika_send_status;not null;default:'pending';index:idx_sent_rubika_messages_status" json:"status"`

	// Rubika provider response fields.
	ServerID    *string `gorm:"size:64"   json:"server_id,omitempty"`
	ErrorCode   *string `gorm:"size:64"   json:"error_code,omitempty"`
	Description *string `gorm:"type:text" json:"description,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sent_rubika_messages_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                           json:"updated_at"`
}

func (SentRubikaMessage) TableName() string { return "sent_rubika_messages" }

// SentRubikaMessageFilter provides filter fields for repository queries.
type SentRubikaMessageFilter struct {
	ID                  *uint
	ProcessedCampaignID *uint
	PhoneNumber         *string
	Status              *RubikaSendStatus
	CreatedAfter        *time.Time
	CreatedBefore       *time.Time
}
