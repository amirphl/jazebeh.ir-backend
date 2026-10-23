package models

import "time"

// CampaignTargetingExecutionReservation is the immutable execution-phase
// audience snapshot and its small mutable reservation lifecycle.  Unlike a
// BundleAudienceSelection it can be released while a campaign is waiting for
// approval; once materialized it becomes a permanent bundle allocation.
//
// SelectionOrder, AssignedTagID, and AudienceScore are captured at approval
// time so scheduler retries never have to select or attribute a fresh
// audience.
type CampaignTargetingExecutionReservation struct {
	ID             int64      `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	HeaderID       int64      `gorm:"not null;index" json:"header_id"`
	CampaignID     uint       `gorm:"not null" json:"campaign_id"`
	BundleID       uint       `gorm:"not null" json:"bundle_id"`
	AudienceID     int64      `gorm:"not null" json:"audience_id"`
	AssignedTagID  uint       `gorm:"not null" json:"assigned_tag_id"`
	SelectionOrder int64      `gorm:"not null" json:"selection_order"`
	AudienceScore  *float64   `gorm:"type:numeric" json:"audience_score,omitempty"`
	State          string     `gorm:"type:varchar(16);not null" json:"state"`
	CreatedAt      time.Time  `json:"created_at"`
	ReleasedAt     *time.Time `json:"released_at,omitempty"`
	MaterializedAt *time.Time `json:"materialized_at,omitempty"`
}

func (CampaignTargetingExecutionReservation) TableName() string {
	return "campaign_targeting_execution_reservations"
}
