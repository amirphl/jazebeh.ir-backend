package models

import "time"

// Tag represents a label used to categorize or target entities like audiences
// Table: tags
// Unique by name; indexed by is_active and created_at
// Timestamps default to UTC at DB level
// Name length limited to 255 characters
type Tag struct {
	ID              uint      `gorm:"primaryKey"                                                               json:"id"`
	Name            string    `gorm:"size:255;not null;uniqueIndex:uk_tags_name;index:idx_tags_name"           json:"name"`
	DisplayTitle    *string   `gorm:"type:text"                                                                json:"display_title,omitempty"`
	AudiencePersona *string   `gorm:"type:text"                                                                json:"audience_persona,omitempty"`
	AudienceCount   *int64    `gorm:"type:bigint"                                                              json:"audience_count,omitempty"`
	IsActive        *bool     `gorm:"default:true;index:idx_tags_is_active"                                    json:"is_active"`
	CreatedAt       time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_tags_created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                           json:"updated_at"`
}

func (Tag) TableName() string { return "tags" }

// TagFilter represents filter criteria for tag queries
type TagFilter struct {
	ID            *uint
	Name          *string
	IsActive      *bool
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}
