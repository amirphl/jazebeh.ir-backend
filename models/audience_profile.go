package models

import (
	"time"

	"github.com/lib/pq"
)

// AudienceProfile represents a profile of an audience used for campaign targeting
// Arrays are stored as PostgreSQL TEXT[] columns
// PhoneNumber is optional and unique when present
// UID is a required unique identifier (external/system specific)
type AudienceProfile struct {
	ID              int64         `gorm:"primaryKey;autoIncrement;type:bigserial"                       json:"id"`
	UID             string        `gorm:"size:255;not null;uniqueIndex:audience_profiles_uid_key"       json:"uid"`
	PhoneNumber     *string       `gorm:"size:20;uniqueIndex:audience_profiles_phone_number_key"        json:"phone_number,omitempty"`
	Tags            pq.Int32Array `gorm:"type:integer[];index:idx_audience_profiles_tags_gin,using:gin" json:"tags"`
	Color           string        `gorm:"size:20;not null;index:idx_audience_profiles_color"            json:"color"`
	NormalizedScore *float64      `gorm:"column:normalized_score"                                       json:"normalized_score,omitempty"`

	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_audience_profiles_created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                        json:"updated_at"`
}

func (AudienceProfile) TableName() string {
	return "audience_profiles"
}

// NormalizedScoreConstraint expresses a WHERE predicate on normalized_score.
//
// Exactly one of the following patterns should be set per query:
//   - GTE only:       normalized_score >= GTE
//   - GT only:        normalized_score > GT
//   - LTE only:       normalized_score <= LTE
//   - GTE + LTE:      GTE <= normalized_score <= LTE
//   - GT + LTE:       GT < normalized_score <= LTE
//   - LTE + OrGTE:    normalized_score <= LTE OR normalized_score >= OrGTE
//   - LTE + OrGT:     normalized_score <= LTE OR normalized_score > OrGT
type NormalizedScoreConstraint struct {
	GTE   *float64
	GT    *float64
	LTE   *float64
	OrGTE *float64 // paired with LTE to form OR pattern
	OrGT  *float64 // paired with LTE to form a strict-upper-boundary OR pattern
}

// AudienceProfileFilter represents filter criteria for audience profile queries
// For array fields, use the *Contains filters to match any single value presence
type AudienceProfileFilter struct {
	ID              *uint
	IDs             []int64
	UID             *string
	PhoneNumber     *string
	Tags            *pq.Int32Array
	Color           *string
	CreatedAfter    *time.Time
	CreatedBefore   *time.Time
	NormalizedScore *NormalizedScoreConstraint
	// ExcludeBundleID excludes the append-only audience usage ledger for a
	// bundle. It is intentionally independent of campaign platform.
	ExcludeBundleID *uint
}
