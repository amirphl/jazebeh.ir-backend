package models

import "time"

// SequenceCounter stores the last value for named monotonic counters.
type SequenceCounter struct {
	Name      string    `gorm:"primaryKey;size:64"                             json:"name"`
	LastValue string    `gorm:"size:16;not null"                               json:"last_value"`
	CreatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"created_at"`
	UpdatedAt time.Time `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')" json:"updated_at"`
}

func (SequenceCounter) TableName() string { return "sequence_counters" }
