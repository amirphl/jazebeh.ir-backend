package models

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// CampaignTargetingExecutionCalculationStatus is deliberately separate from
// the active reservation lifecycle. A ready calculation is only a proposal;
// it has not consumed any audience capacity or customer funds.
type CampaignTargetingExecutionCalculationStatus string

const (
	CampaignTargetingExecutionCalculationPending   CampaignTargetingExecutionCalculationStatus = "pending"
	CampaignTargetingExecutionCalculationReady     CampaignTargetingExecutionCalculationStatus = "ready"
	CampaignTargetingExecutionCalculationFailed    CampaignTargetingExecutionCalculationStatus = "failed"
	CampaignTargetingExecutionCalculationStale     CampaignTargetingExecutionCalculationStatus = "stale"
	CampaignTargetingExecutionCalculationCommitted CampaignTargetingExecutionCalculationStatus = "committed"
)

const SmartTargetingExecutionCalculationVersion = 1

// CampaignTargetingExecutionCalculation is the durable, non-reserving output
// of the expensive execution audience scan. Once finalization atomically
// changes it to committed, these members are the active execution reservation;
// approval never copies or locks the whole audience again.
type CampaignTargetingExecutionCalculation struct {
	ID                     int64                                       `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	CampaignID             uint                                        `gorm:"not null;index:idx_execution_calculation_campaign_created,priority:1;index:idx_execution_calculation_campaign_status,priority:1" json:"campaign_id"`
	BundleID               uint                                        `gorm:"not null;index" json:"bundle_id"`
	CustomerID             uint                                        `gorm:"not null" json:"customer_id"`
	RequestedAudienceCount int64                                       `gorm:"not null" json:"requested_audience_count"`
	SelectedTagIDs         pq.Int64Array                               `gorm:"type:bigint[];not null" json:"-"`
	SelectedScoreClasses   pq.StringArray                              `gorm:"type:text[];not null" json:"-"`
	SelectionInputHash     string                                      `gorm:"type:char(64);not null;index" json:"-"`
	RequestSnapshot        json.RawMessage                             `gorm:"type:jsonb;not null" json:"-"`
	AllocationFingerprint  string                                      `gorm:"type:char(64);not null" json:"-"`
	CalculationVersion     int                                         `gorm:"not null" json:"-"`
	Status                 CampaignTargetingExecutionCalculationStatus `gorm:"type:varchar(16);not null;index:idx_execution_calculation_campaign_status,priority:2" json:"status"`
	CreatedAt              time.Time                                   `gorm:"not null;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_execution_calculation_campaign_created,priority:2" json:"created_at"`
	StartedAt              *time.Time                                  `json:"started_at,omitempty"`
	FinishedAt             *time.Time                                  `json:"finished_at,omitempty"`
	CommittedAt            *time.Time                                  `json:"committed_at,omitempty"`
	ErrorCode              *string                                     `gorm:"type:varchar(128)" json:"error_code,omitempty"`
	ErrorMessage           *string                                     `gorm:"type:text" json:"error_message,omitempty"`
}

func (CampaignTargetingExecutionCalculation) TableName() string {
	return "campaign_targeting_execution_calculations"
}

// CampaignTargetingExecutionCalculationMember is immutable proposed output.
// It is intentionally not a reservation and therefore has no active state.
type CampaignTargetingExecutionCalculationMember struct {
	ID             int64    `gorm:"primaryKey;autoIncrement;type:bigserial" json:"-"`
	CalculationID  int64    `gorm:"not null;index" json:"-"`
	AudienceID     int64    `gorm:"not null" json:"-"`
	AssignedTagID  uint     `gorm:"not null" json:"-"`
	SelectionOrder int64    `gorm:"not null" json:"-"`
	AudienceScore  *float64 `gorm:"type:numeric" json:"-"`
}

func (CampaignTargetingExecutionCalculationMember) TableName() string {
	return "campaign_targeting_execution_calculation_members"
}
