package models

import (
	"time"

	"github.com/lib/pq"
)

// BundleAudienceSelection is one immutable, bundle-scoped campaign allocation.
// The exact retry set is stored once in BundleAudienceSelectionMember.
// SelectedAudienceIDs is a repository-populated transient projection, not
// another persisted copy of the allocation.
type BundleAudienceSelection struct {
	ID                  uint          `gorm:"primaryKey" json:"id"`
	CustomerID          uint          `gorm:"not null;index:idx_bundle_aud_sel_customer_bundle" json:"customer_id"`
	BundleID            uint          `gorm:"not null;index:idx_bundle_aud_sel_customer_bundle" json:"bundle_id"`
	CampaignID          *uint         `gorm:"uniqueIndex:uk_bundle_aud_sel_campaign" json:"campaign_id,omitempty"`
	CorrelationID       string        `gorm:"type:varchar(128);not null;uniqueIndex:uk_bundle_aud_sel_correlation_id" json:"correlation_id"`
	SelectedAudienceIDs pq.Int64Array `gorm:"-" json:"selected_audience_ids"`
	AudienceCount       int64         `gorm:"not null" json:"audience_count"`
	CreatedAt           time.Time     `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
}

func (BundleAudienceSelection) TableName() string { return "bundle_audience_selections" }

// BundleAudienceSelectionMember is the normalized append-only allocation
// ledger. Excel allocations are independent from Smart Targeting allocations.
// Smart Targeting cross-campaign claims are protected by the database claim
// guard instead.
type BundleAudienceSelectionMember struct {
	ID             int64     `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	SelectionID    uint      `gorm:"not null;uniqueIndex:uk_bundle_aud_sel_member_selection_audience,priority:1;uniqueIndex:uk_bundle_aud_sel_member_selection_order,priority:1" json:"selection_id"`
	BundleID       uint      `gorm:"not null;index:idx_bundle_aud_sel_member_bundle_audience,priority:1" json:"bundle_id"`
	AudienceID     int64     `gorm:"not null;uniqueIndex:uk_bundle_aud_sel_member_selection_audience,priority:2;index:idx_bundle_aud_sel_member_bundle_audience,priority:2" json:"audience_id"`
	SelectionOrder int64     `gorm:"not null;check:bundle_aud_sel_member_selection_order_nonnegative,selection_order >= 0;uniqueIndex:uk_bundle_aud_sel_member_selection_order,priority:2" json:"selection_order"`
	CreatedAt      time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
}

// CampaignAudienceTagAttribution records why a Smart Targeting audience was
// selected. It intentionally contains no click/CTR calculation; later reports
// can join this immutable scheduler-time fact to delivery and click data.
type CampaignAudienceTagAttribution struct {
	ID                        int64         `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	CampaignID                uint          `gorm:"not null;uniqueIndex:uk_campaign_audience_tag,priority:1" json:"campaign_id"`
	BundleID                  uint          `gorm:"not null;index:idx_campaign_audience_tag_bundle" json:"bundle_id"`
	BundleAudienceSelectionID uint          `gorm:"not null;index:idx_campaign_audience_tag_selection;uniqueIndex:uk_campaign_audience_tag_selection_order,priority:1" json:"bundle_audience_selection_id"`
	AudienceID                int64         `gorm:"not null;uniqueIndex:uk_campaign_audience_tag,priority:2" json:"audience_id"`
	AssignedTagID             uint          `gorm:"not null;index:idx_campaign_audience_tag_assigned" json:"assigned_tag_id"`
	PhaseType                 CampaignPhase `gorm:"type:campaign_phase;not null" json:"phase_type"`
	SelectionMethod           string        `gorm:"type:varchar(32);not null" json:"selection_method"`
	SelectionOrder            int64         `gorm:"not null;check:campaign_audience_tag_selection_order_nonnegative,selection_order >= 0;uniqueIndex:uk_campaign_audience_tag_selection_order,priority:2" json:"selection_order"`
	AudienceScore             *float64      `gorm:"type:numeric" json:"audience_score,omitempty"`
	CreatedAt                 time.Time     `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
}

func (CampaignAudienceTagAttribution) TableName() string {
	return "campaign_audience_tag_attributions"
}

func (BundleAudienceSelectionMember) TableName() string {
	return "bundle_audience_selection_members"
}
