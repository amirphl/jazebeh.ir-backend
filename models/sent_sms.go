package models

import "time"

// SMSSendStatus enumerates status of a sent SMS record
type SMSSendStatus string

const (
	SMSSendStatusPending      SMSSendStatus = "pending"
	SMSSendStatusSuccessful   SMSSendStatus = "successful"
	SMSSendStatusUnsuccessful SMSSendStatus = "unsuccessful"
)

// SentSMS records a single phone number delivery attempt under a processed campaign
type SentSMS struct {
	ID                  uint          `gorm:"primaryKey"                                                                             json:"id"`
	ProcessedCampaignID uint          `gorm:"not null;index:idx_sent_sms_processed_campaign_id"                                      json:"processed_campaign_id"`
	PhoneNumber         string        `gorm:"size:20;not null;index:idx_sent_sms_phone_number"                                       json:"phone_number"`
	TrackingID          string        `gorm:"size:64;not null;index:idx_sent_sms_tracking_id"                                        json:"tracking_id"`
	Provider            SMSProvider   `gorm:"size:32;not null;default:'payamsms';index:idx_sent_sms_provider_customer_id,priority:1" json:"provider"`
	ProviderCustomerID  *int64        `gorm:"index:idx_sent_sms_provider_customer_id,priority:2"                                     json:"provider_customer_id,omitempty"`
	PartsDelivered      int           `gorm:"default:0"                                                                              json:"parts_delivered"`
	Status              SMSSendStatus `gorm:"type:sent_sms_status;not null;default:'pending';index:idx_sent_sms_status"              json:"status"`

	// Provider response fields (optional, populated after provider acknowledgement)
	ServerID    *string `gorm:"size:64"   json:"server_id,omitempty"`
	ErrorCode   *string `gorm:"size:64"   json:"error_code,omitempty"`
	Description *string `gorm:"type:text" json:"description,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sent_sms_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                               json:"updated_at"`
}

func (SentSMS) TableName() string { return "sent_sms" }

// SentSMSFilter provides filter fields for repository queries
type SentSMSFilter struct {
	ID                  *uint
	ProcessedCampaignID *uint
	PhoneNumber         *string
	Status              *SMSSendStatus
	CreatedAfter        *time.Time
	CreatedBefore       *time.Time
}
