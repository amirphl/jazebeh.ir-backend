package models

import (
	"encoding/json"
	"time"
)

const (
	SmartTargetingExecutionReservationSchemaVersion                = 1
	SmartTargetingExecutionReservationSelectionInputVersion        = 5
	SmartTargetingExecutionReservationAllocationFingerprintVersion = 3
)

// CampaignTargetingExecutionReservationHeader is the immutable selection
// contract for one execution campaign. Members are persisted separately for
// efficient scheduler reads; this record makes their intended count, input,
// allocation generation, and lifecycle auditable and independently verifiable.
type CampaignTargetingExecutionReservationHeader struct {
	ID                           int64           `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	CampaignID                   uint            `gorm:"not null;uniqueIndex" json:"campaign_id"`
	BundleID                     uint            `gorm:"not null" json:"bundle_id"`
	Phase                        CampaignPhase   `gorm:"type:campaign_phase;not null" json:"phase"`
	ReservationVersion           int             `gorm:"not null" json:"reservation_version"`
	RequestedAudienceCount       int64           `gorm:"not null" json:"requested_audience_count"`
	CandidateGeneration          int             `gorm:"not null" json:"candidate_generation"`
	SelectionInputVersion        int             `gorm:"not null" json:"selection_input_version"`
	SelectionInputHash           string          `gorm:"type:char(64);not null" json:"selection_input_hash"`
	AllocationFingerprintVersion int             `gorm:"not null" json:"allocation_fingerprint_version"`
	AllocationFingerprint        string          `gorm:"type:char(64);not null" json:"allocation_fingerprint"`
	RequestSnapshot              json.RawMessage `gorm:"type:jsonb;not null" json:"request_snapshot"`
	State                        string          `gorm:"type:varchar(16);not null" json:"state"`
	CreatedAt                    time.Time       `json:"created_at"`
	ReleasedAt                   *time.Time      `json:"released_at,omitempty"`
	MaterializedAt               *time.Time      `json:"materialized_at,omitempty"`
}

func (CampaignTargetingExecutionReservationHeader) TableName() string {
	return "campaign_targeting_execution_reservation_headers"
}
