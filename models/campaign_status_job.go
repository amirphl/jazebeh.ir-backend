package models

import (
	"time"

	"github.com/lib/pq"
)

// CampaignStatusJob represents a scheduled job to fetch delivery status across platforms.
type CampaignStatusJob struct {
	ID                  uint           `gorm:"primaryKey"                                                                                                                 json:"id"`
	CorrelationID       string         `gorm:"size:64;index:idx_campaign_status_jobs_corr_id;not null"                                                                    json:"correlation_id"`
	ProcessedCampaignID uint           `gorm:"index:idx_campaign_status_jobs_processed_campaign_id;not null"                                                              json:"processed_campaign_id"`
	Platform            string         `gorm:"size:20;index:idx_campaign_status_jobs_platform_scheduled_retry,priority:1;not null"                                        json:"platform"`
	Provider            *SMSProvider   `gorm:"size:32"                                                                                                                    json:"provider,omitempty"`
	TrackingIDs         pq.StringArray `gorm:"type:text[];not null"                                                                                                       json:"tracking_ids"`
	RetryCount          int            `gorm:"index:idx_campaign_status_jobs_platform_scheduled_retry,priority:3;not null;default:0"                                      json:"retry_count"`
	ScheduledAt         time.Time      `gorm:"index:idx_campaign_status_jobs_scheduled_retry;index:idx_campaign_status_jobs_platform_scheduled_retry,priority:2;not null" json:"scheduled_at"`
	ExecutedAt          *time.Time     `                                                                                                                                  json:"executed_at,omitempty"`
	Error               *string        `gorm:"type:text"                                                                                                                  json:"error,omitempty"`
	RawProviderResponse *string        `gorm:"type:text"                                                                                                                  json:"raw_provider_response,omitempty"`
	CreatedAt           time.Time      `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');not null"                                                                    json:"created_at"`
	UpdatedAt           time.Time      `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');not null"                                                                    json:"updated_at"`
}

func (CampaignStatusJob) TableName() string { return "campaign_status_jobs" }
