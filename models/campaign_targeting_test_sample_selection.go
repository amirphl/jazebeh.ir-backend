package models

import "time"

// CampaignTargetingTestSampleSelection is the immutable concrete audience
// output of one Test sampling calculation. It is intentionally distinct from
// BundleAudienceSelection: unfinalized preview history must not permanently
// consume Bundle audiences.
type CampaignTargetingTestSampleSelection struct {
	ID                     int64                                        `gorm:"primaryKey;autoIncrement;type:bigserial"                    json:"id"`
	CalculationID          int64                                        `gorm:"not null;uniqueIndex"                                       json:"calculation_id"`
	CampaignID             uint                                         `gorm:"not null;index:idx_campaign_test_sample_selection_campaign" json:"campaign_id"`
	BundleID               uint                                         `gorm:"not null"                                                   json:"bundle_id"`
	Generation             int64                                        `gorm:"not null"                                                   json:"generation"`
	InputHash              string                                       `gorm:"type:char(64);not null"                                     json:"-"`
	EffectiveAudienceCount int64                                        `gorm:"type:bigint;not null"                                       json:"effective_audience_count"`
	CreatedAt              time.Time                                    `                                                                  json:"created_at"`
	Members                []CampaignTargetingTestSampleSelectionMember `gorm:"-"                                                          json:"members,omitempty"`
}

func (CampaignTargetingTestSampleSelection) TableName() string {
	return "campaign_targeting_test_sample_selections"
}

type CampaignTargetingTestSampleSelectionMember struct {
	ID                int64     `gorm:"primaryKey;autoIncrement;type:bigserial"                                                                                             json:"id"`
	SelectionID       int64     `gorm:"not null;uniqueIndex:uk_campaign_test_sample_member_audience,priority:1;uniqueIndex:uk_campaign_test_sample_member_order,priority:1" json:"selection_id"`
	AudienceID        int64     `gorm:"not null;uniqueIndex:uk_campaign_test_sample_member_audience,priority:2"                                                             json:"audience_id"`
	AssignedTagID     uint      `gorm:"not null"                                                                                                                            json:"assigned_tag_id"`
	TagSelectionOrder int64     `gorm:"not null"                                                                                                                            json:"tag_selection_order"`
	SelectionOrder    int64     `gorm:"not null;uniqueIndex:uk_campaign_test_sample_member_order,priority:2"                                                                json:"selection_order"`
	AudienceScore     *float64  `gorm:"type:numeric"                                                                                                                        json:"audience_score,omitempty"`
	CreatedAt         time.Time `                                                                                                                                           json:"created_at"`
}

func (CampaignTargetingTestSampleSelectionMember) TableName() string {
	return "campaign_targeting_test_sample_selection_members"
}

type CampaignTargetingTestSampleReservation struct {
	ID             int64      `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	SelectionID    int64      `gorm:"not null"                                json:"selection_id"`
	CampaignID     uint       `gorm:"not null"                                json:"campaign_id"`
	BundleID       uint       `gorm:"not null"                                json:"bundle_id"`
	AudienceID     int64      `gorm:"not null"                                json:"audience_id"`
	State          string     `gorm:"type:varchar(16);not null"               json:"state"`
	CreatedAt      time.Time  `                                               json:"created_at"`
	ReleasedAt     *time.Time `                                               json:"released_at,omitempty"`
	MaterializedAt *time.Time `                                               json:"materialized_at,omitempty"`
}

func (CampaignTargetingTestSampleReservation) TableName() string {
	return "campaign_targeting_test_sample_reservations"
}
