package models

import (
	"time"

	"github.com/lib/pq"
)

const (
	CampaignAudienceReportJobPending    = "pending"
	CampaignAudienceReportJobProcessing = "processing"
	CampaignAudienceReportJobCompleted  = "completed"
	CampaignAudienceReportJobFailed     = "failed"
	CampaignAudienceReportJobExpired    = "expired"
)

// CampaignAudienceReportJob is the durable state for a large customer export.
// OutputPath is intentionally never serialized: downloads always authorize the
// job owner before opening the server-side file.
type CampaignAudienceReportJob struct {
	ID             string        `gorm:"primaryKey;size:36" json:"id"`
	CustomerID     uint          `gorm:"not null;index" json:"-"`
	CampaignIDs    pq.Int64Array `gorm:"type:bigint[];not null" json:"campaign_ids"`
	Status         string        `gorm:"size:16;not null;index" json:"status"`
	RowCount       int64         `gorm:"not null;default:0" json:"row_count"`
	SheetCount     int           `gorm:"not null;default:0" json:"sheet_count"`
	ByteSize       int64         `gorm:"not null;default:0" json:"byte_size"`
	OutputPath     *string       `gorm:"type:text" json:"-"`
	ErrorCode      *string       `gorm:"size:64" json:"error_code,omitempty"`
	ErrorMessage   *string       `gorm:"type:text" json:"error_message,omitempty"`
	Attempts       int           `gorm:"not null;default:0" json:"attempts"`
	LeaseToken     *string       `gorm:"size:36;index" json:"-"`
	LeaseExpiresAt *time.Time    `gorm:"index" json:"-"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	ExpiresAt      *time.Time    `gorm:"index" json:"expires_at,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

func (CampaignAudienceReportJob) TableName() string { return "campaign_audience_report_jobs" }
