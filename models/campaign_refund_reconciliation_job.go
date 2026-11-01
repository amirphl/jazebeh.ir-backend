package models

import "time"

// CampaignRefundReconciliationJob is the durable work record for the
// under-delivery refund calculation. It deliberately lives outside campaign
// statistics: statistics are customer-facing delivery data, not a job queue.
type CampaignRefundReconciliationJob struct {
	CampaignID     uint       `gorm:"primaryKey" json:"campaign_id"`
	State          string     `json:"state"`
	EligibleAt     time.Time  `json:"eligible_at"`
	AttemptCount   int        `json:"attempt_count"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ErrorCode      *string    `json:"error_code,omitempty"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CampaignRefundReconciliationSchedulerState advances the bounded historical
// backfill scan. New executions enqueue their own job transactionally; this
// cursor exists only to import pre-deployment or otherwise missed work without
// rescanning all historical executed campaigns on every poll.
type CampaignRefundReconciliationSchedulerState struct {
	ID             int16     `gorm:"primaryKey" json:"id"`
	LastCampaignID uint      `json:"last_campaign_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (CampaignRefundReconciliationSchedulerState) TableName() string {
	return "campaign_refund_reconciliation_scheduler_state"
}

func (CampaignRefundReconciliationJob) TableName() string {
	return "campaign_refund_reconciliation_jobs"
}

const (
	CampaignRefundJobPending      = "pending"
	CampaignRefundJobProcessing   = "processing"
	CampaignRefundJobRetry        = "retry"
	CampaignRefundJobCompleted    = "completed"
	CampaignRefundJobManualReview = "manual_review"
)
