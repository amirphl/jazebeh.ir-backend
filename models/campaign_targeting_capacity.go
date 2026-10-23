package models

import (
	"time"

	"github.com/lib/pq"
)

// CampaignTargetingCapacityCalculationStatus is the persisted state of one
// immutable Smart Targeting capacity generation. A generation is never
// replaced; only its short-lived execution state is completed or failed.
type CampaignTargetingCapacityCalculationStatus string

const (
	CampaignTargetingCapacityCalculating CampaignTargetingCapacityCalculationStatus = "calculating"
	CampaignTargetingCapacityCalculated  CampaignTargetingCapacityCalculationStatus = "calculated"
	CampaignTargetingCapacityFailed      CampaignTargetingCapacityCalculationStatus = "failed"

	// SmartTargetingCapacityAlgorithmVersion must be used by both the capacity
	// flow and the delivery scheduler when they persist and consume an exact
	// capacity calculation.
	SmartTargetingCapacityAlgorithmVersion = 4
)

// CampaignTargetingCapacityCalculation captures every input and output needed
// to audit an exact Smart Targeting capacity calculation. Counts are signed in
// storage because PostgreSQL BIGINT is signed; application code validates that
// they are never negative before exposing them as uint64 values.
type CampaignTargetingCapacityCalculation struct {
	ID         int64 `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	CampaignID uint  `gorm:"not null;index:idx_campaign_targeting_capacity_campaign_created,priority:1;index:idx_campaign_targeting_capacity_campaign_status,priority:1" json:"campaign_id"`
	BundleID   uint  `gorm:"not null;index:idx_campaign_targeting_capacity_bundle" json:"bundle_id"`
	CustomerID uint  `gorm:"not null" json:"customer_id"`
	// Platform and AllowedColors are delivery-eligibility snapshots. They
	// participate in calculation identity because the SMS provider controls
	// whether color eligibility is restricted.
	Platform                      string                                     `gorm:"type:varchar(32);not null" json:"platform"`
	AllowedColors                 pq.StringArray                             `gorm:"type:text[];not null;default:'{}'" json:"allowed_colors"`
	RequestedByCustomerID         uint                                       `gorm:"not null" json:"requested_by_customer_id"`
	SelectedTagIDs                pq.Int64Array                              `gorm:"type:bigint[];not null" json:"selected_tag_ids"`
	SelectedTagsHash              string                                     `gorm:"type:char(64);not null;index:idx_campaign_targeting_capacity_tags_hash" json:"selected_tags_hash"`
	InputHash                     string                                     `gorm:"type:char(64);not null" json:"input_hash"`
	SelectedScoreClasses          pq.StringArray                             `gorm:"type:text[];not null" json:"selected_score_classes"`
	SelectedTagCount              int                                        `gorm:"not null" json:"selected_tag_count"`
	Phase                         string                                     `gorm:"type:varchar(16);not null;default:'execution'" json:"phase"`
	ApplyBundleAudienceExclusions bool                                       `gorm:"not null;default:false" json:"apply_bundle_audience_exclusions"`
	RawAudienceCount              int64                                      `gorm:"type:bigint;not null;default:0" json:"raw_audience_count"`
	EligibleUniqueAudienceCount   int64                                      `gorm:"type:bigint;not null;default:0" json:"eligible_unique_audience_count_before_approved_campaign_deduction"`
	ApprovedCampaignDeduction     int64                                      `gorm:"type:bigint;not null;default:0" json:"approved_campaign_audience_deduction"`
	UsableUniqueAudienceCount     int64                                      `gorm:"type:bigint;not null;default:0" json:"usable_unique_audience_count"`
	AllocationFingerprint         string                                     `gorm:"type:char(64);not null" json:"approved_campaign_allocation_fingerprint"`
	Status                        CampaignTargetingCapacityCalculationStatus `gorm:"type:varchar(32);not null;index:idx_campaign_targeting_capacity_campaign_status,priority:2" json:"status"`
	CalculationVersion            int                                        `gorm:"not null;default:3" json:"calculation_version"`
	CreatedAt                     time.Time                                  `gorm:"not null;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_campaign_targeting_capacity_campaign_created,priority:2" json:"created_at"`
	StartedAt                     *time.Time                                 `json:"started_at,omitempty"`
	FinishedAt                    *time.Time                                 `json:"finished_at,omitempty"`
	ExpiresAt                     *time.Time                                 `gorm:"not null;index:idx_campaign_targeting_capacity_expires" json:"expires_at,omitempty"`
	ErrorCode                     *string                                    `gorm:"type:varchar(128)" json:"error_code,omitempty"`
	ErrorMessage                  *string                                    `gorm:"type:text" json:"error_message,omitempty"`
}

func (CampaignTargetingCapacityCalculation) TableName() string {
	return "campaign_targeting_capacity_calculations"
}
