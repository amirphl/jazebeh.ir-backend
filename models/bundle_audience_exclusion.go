package models

import "time"

// BundleAudienceExclusion prevents one audience from being selected for Smart
// Targeting Test campaigns in a bundle. The pair is the durable identity so
// repeated/manual inserts cannot create duplicate exclusion rows.
type BundleAudienceExclusion struct {
	BundleID   uint      `gorm:"primaryKey;not null"                                               json:"bundle_id"`
	AudienceID int64     `gorm:"primaryKey;not null;index:idx_bundle_audience_exclusions_audience" json:"audience_id"`
	CreatedAt  time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"                                json:"created_at"`
}

func (BundleAudienceExclusion) TableName() string {
	return "bundle_audience_exclusions"
}
