package models

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// SMSProviderSendAttempt is immutable transport/audit history for one SMS
// provider batch. It deliberately contains no request body, which avoids
// persisting campaign text or recipient numbers twice.
type SMSProviderSendAttempt struct {
	ID                  uint            `gorm:"primaryKey"                                                                                                               json:"id"`
	ProcessedCampaignID uint            `gorm:"not null;index:idx_sms_provider_send_attempts_campaign"                                                                   json:"processed_campaign_id"`
	Provider            SMSProvider     `gorm:"size:32;not null;index:idx_sms_provider_send_attempts_provider_created,priority:1"                                        json:"provider"`
	TrackingIDs         pq.StringArray  `gorm:"type:text[];not null"                                                                                                     json:"tracking_ids"`
	HTTPStatusCode      *int            `                                                                                                                                json:"http_status_code,omitempty"`
	ResponseHeaders     json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"                                                                                         json:"response_headers"`
	ResponseBody        *string         `gorm:"type:text"                                                                                                                json:"response_body,omitempty"`
	Error               *string         `gorm:"type:text"                                                                                                                json:"error,omitempty"`
	AttemptCount        int             `gorm:"not null;default:0"                                                                                                       json:"attempt_count"`
	CreatedAt           time.Time       `gorm:"not null;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sms_provider_send_attempts_provider_created,priority:2" json:"created_at"`
}

func (SMSProviderSendAttempt) TableName() string { return "sms_provider_send_attempts" }
