package models

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// PayamSMSSendResponse stores the immediate result of one scheduler batch
// submission to PayamSMS. It is immutable diagnostic history, distinct from
// the later per-recipient delivery statuses.
type PayamSMSSendResponse struct {
	ID                  uint            `gorm:"primaryKey"                                                                                            json:"id"`
	ProcessedCampaignID uint            `gorm:"not null;index:idx_payam_sms_send_responses_campaign"                                                  json:"processed_campaign_id"`
	TrackingIDs         pq.StringArray  `gorm:"type:text[];not null"                                                                                  json:"tracking_ids"`
	HTTPStatusCode      *int            `                                                                                                             json:"http_status_code,omitempty"`
	ResponseHeaders     json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"                                                                      json:"response_headers"`
	ResponseBody        *string         `gorm:"type:text"                                                                                             json:"response_body,omitempty"`
	Error               *string         `gorm:"type:text"                                                                                             json:"error,omitempty"`
	AttemptCount        int             `gorm:"not null;default:0"                                                                                    json:"attempt_count"`
	CreatedAt           time.Time       `gorm:"not null;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_payam_sms_send_responses_created_at" json:"created_at"`
}

func (PayamSMSSendResponse) TableName() string { return "payam_sms_send_responses" }
