package models

import "time"

// AdminShortLinkUploadJob keeps the source CSV and its deterministic allocation
// key so an interrupted external publication never creates a second code set.
type AdminShortLinkUploadJob struct {
	ID            string     `gorm:"primaryKey;size:36" json:"id"`
	AllocationKey string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	ScenarioID    uint       `gorm:"not null;uniqueIndex" json:"scenario_id"`
	ScenarioName  string     `gorm:"type:text;not null" json:"scenario_name"`
	Domain        string     `gorm:"size:255;not null" json:"domain"`
	CSVData       []byte     `gorm:"type:bytea;not null" json:"-"`
	Status        string     `gorm:"size:20;index;not null" json:"status"`
	TotalRows     int        `gorm:"not null;default:0" json:"total_rows"`
	Created       int        `gorm:"not null;default:0" json:"created"`
	Skipped       int        `gorm:"not null;default:0" json:"skipped"`
	Published     int        `gorm:"not null;default:0" json:"published"`
	Attempts      int        `gorm:"not null;default:0" json:"attempts"`
	NextAttemptAt *time.Time `gorm:"index" json:"next_attempt_at,omitempty"`
	LastError     *string    `gorm:"type:text" json:"last_error,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (AdminShortLinkUploadJob) TableName() string { return "admin_short_link_upload_jobs" }
