package models

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// CampaignTargetingTestSamplingCalculationStatus is the durable lifecycle of
// one asynchronous Smart Targeting Test sampling calculation.
type CampaignTargetingTestSamplingCalculationStatus string

const (
	CampaignTargetingTestSamplingCalculating CampaignTargetingTestSamplingCalculationStatus = "calculating"
	CampaignTargetingTestSamplingCalculated  CampaignTargetingTestSamplingCalculationStatus = "calculated"
	CampaignTargetingTestSamplingFailed      CampaignTargetingTestSamplingCalculationStatus = "failed"
)

// CampaignTargetingTestSamplingCalculation snapshots every input required by
// the worker. Its immutable concrete audience output is stored separately in
// CampaignTargetingTestSampleSelection once the calculation completes.
type CampaignTargetingTestSamplingCalculation struct {
	ID                     int64                                          `gorm:"primaryKey;autoIncrement;type:bigserial"                                                                                                               json:"id"`
	CampaignID             uint                                           `gorm:"not null;index:idx_campaign_targeting_test_sampling_campaign_created,priority:1;index:idx_campaign_targeting_test_sampling_campaign_status,priority:1" json:"campaign_id"`
	BundleID               uint                                           `gorm:"not null;index:idx_campaign_targeting_test_sampling_bundle"                                                                                            json:"bundle_id"`
	CustomerID             uint                                           `gorm:"not null"                                                                                                                                              json:"customer_id"`
	RequestedByCustomerID  uint                                           `gorm:"not null"                                                                                                                                              json:"requested_by_customer_id"`
	SelectedTagIDs         pq.Int64Array                                  `gorm:"type:bigint[];not null"                                                                                                                                json:"selected_tag_ids"`
	InputHash              string                                         `gorm:"type:char(64);not null;index:idx_campaign_targeting_test_sampling_input"                                                                               json:"input_hash"`
	SelectedScoreClasses   pq.StringArray                                 `gorm:"type:text[];not null"                                                                                                                                  json:"selected_score_classes"`
	SelectedTagCount       int                                            `gorm:"not null"                                                                                                                                              json:"selected_tag_count"`
	SampleSizePerTag       int64                                          `gorm:"type:bigint;not null"                                                                                                                                  json:"sample_size_per_tag"`
	CampaignUpdatedAt      *time.Time                                     `                                                                                                                                                             json:"campaign_updated_at,omitempty"`
	TagResults             json.RawMessage                                `gorm:"type:jsonb;not null;default:'[]'"                                                                                                                      json:"tag_results"`
	SatisfiedTagCount      int                                            `gorm:"not null;default:0"                                                                                                                                    json:"satisfied_tag_count"`
	EffectiveAudienceCount int64                                          `gorm:"type:bigint;not null;default:0"                                                                                                                        json:"effective_audience_count"`
	CampaignCost           uint64                                         `gorm:"type:numeric(20,0);not null;default:0"                                                                                                                 json:"campaign_cost"`
	AllocationFingerprint  string                                         `gorm:"type:char(64);not null"                                                                                                                                json:"-"`
	Status                 CampaignTargetingTestSamplingCalculationStatus `gorm:"type:varchar(32);not null;index:idx_campaign_targeting_test_sampling_campaign_status,priority:2"                                                       json:"status"`
	CalculationVersion     int                                            `gorm:"not null;default:2"                                                                                                                                    json:"calculation_version"`
	Generation             int64                                          `gorm:"not null;default:0"                                                                                                                                    json:"generation"`
	CreatedAt              time.Time                                      `gorm:"not null;default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_campaign_targeting_test_sampling_campaign_created,priority:2"                        json:"created_at"`
	StartedAt              *time.Time                                     `                                                                                                                                                             json:"started_at,omitempty"`
	FinishedAt             *time.Time                                     `                                                                                                                                                             json:"finished_at,omitempty"`
	ErrorCode              *string                                        `gorm:"type:varchar(128)"                                                                                                                                     json:"error_code,omitempty"`
	ErrorMessage           *string                                        `gorm:"type:text"                                                                                                                                             json:"error_message,omitempty"`
}

func (CampaignTargetingTestSamplingCalculation) TableName() string {
	return "campaign_targeting_test_sampling_calculations"
}
