package models

import (
	"encoding/json"
	"time"
)

// SMSStatusResult stores provider status metrics for a given job/tracking ID.
type SMSStatusResult struct {
	ID                    uint            `gorm:"primaryKey"                                                                                                                                                         json:"id"`
	JobID                 uint            `gorm:"column:job_id;index:idx_sms_status_results_job_id;not null"                                                                                                         json:"job_id"`
	ProcessedCampaignID   uint            `gorm:"column:processed_campaign_id;index:idx_sms_status_results_processed_campaign_id;uniqueIndex:idx_sms_status_results_processed_campaign_tracking,priority:1;not null" json:"processed_campaign_id"`
	TrackingID            string          `gorm:"column:tracking_id;type:text;index:idx_sms_status_results_tracking_id;uniqueIndex:idx_sms_status_results_processed_campaign_tracking,priority:2;not null"           json:"tracking_id"`
	ServerID              *string         `gorm:"column:server_id;type:text"                                                                                                                                         json:"server_id,omitempty"`
	Provider              SMSProvider     `gorm:"column:provider;type:text;not null;default:'payamsms'"                                                                                                              json:"provider"`
	ProviderStatusCode    *string         `gorm:"column:provider_status_code;type:text"                                                                                                                              json:"provider_status_code,omitempty"`
	ProviderStatusText    *string         `gorm:"column:provider_status_text;type:text"                                                                                                                              json:"provider_status_text,omitempty"`
	InternalStatus        *SMSSendStatus  `gorm:"column:internal_status;type:sent_sms_status"                                                                                                                        json:"internal_status,omitempty"`
	TotalParts            *int64          `gorm:"column:total_parts"                                                                                                                                                 json:"total_parts,omitempty"`
	TotalDeliveredParts   *int64          `gorm:"column:total_delivered_parts"                                                                                                                                       json:"total_delivered_parts,omitempty"`
	TotalUndeliveredParts *int64          `gorm:"column:total_undelivered_parts"                                                                                                                                     json:"total_undelivered_parts,omitempty"`
	TotalUnknownParts     *int64          `gorm:"column:total_unknown_parts"                                                                                                                                         json:"total_unknown_parts,omitempty"`
	Status                *string         `gorm:"column:status;type:text"                                                                                                                                            json:"status,omitempty"`
	Metadata              json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'"                                                                                                                   json:"metadata"`
	CreatedAt             time.Time       `gorm:"column:created_at;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');not null"                                                                                          json:"created_at"`
}

func (SMSStatusResult) TableName() string { return "sms_status_results" }
