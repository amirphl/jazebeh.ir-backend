package models

import "time"

// BundleActionFileStatus deliberately keeps a file out of the live action set
// until its complete contents and the corresponding metric refresh commit.
type BundleActionFileStatus string

const (
	BundleActionFilePending       BundleActionFileStatus = "pending"
	BundleActionFileProcessing    BundleActionFileStatus = "processing"
	BundleActionFileProcessed     BundleActionFileStatus = "processed"
	BundleActionFileFailed        BundleActionFileStatus = "failed"
	BundleActionFileDeletePending BundleActionFileStatus = "delete_pending"
	BundleActionFileDeleted       BundleActionFileStatus = "deleted"
)

type BundleActionFile struct {
	ID                         int64                  `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	BundleID                   uint                   `gorm:"not null;index:idx_bundle_action_files_bundle_status,priority:1" json:"bundle_id"`
	OriginalFileName           string                 `gorm:"type:varchar(255);not null" json:"original_file_name"`
	ActionLevel                string                 `gorm:"type:text;not null;default:''" json:"action_level"`
	StoragePath                string                 `gorm:"type:text;not null" json:"-"`
	ContentSHA256              string                 `gorm:"type:char(64);not null" json:"content_sha256"`
	Status                     BundleActionFileStatus `gorm:"type:varchar(32);not null;index:idx_bundle_action_files_bundle_status,priority:2" json:"status"`
	TotalRowCount              int64                  `gorm:"not null;default:0" json:"total_row_count"`
	UniqueUIDCount             int64                  `gorm:"not null;default:0" json:"unique_uid_count"`
	NewActionUIDCount          int64                  `gorm:"not null;default:0" json:"new_action_uid_count"`
	DuplicateInFileCount       int64                  `gorm:"not null;default:0" json:"duplicate_in_file_count"`
	DuplicateInOtherFilesCount int64                  `gorm:"not null;default:0" json:"duplicate_in_other_files_count"`
	InvalidUIDCount            int64                  `gorm:"not null;default:0" json:"invalid_uid_count"`
	OutsideBundleCount         int64                  `gorm:"not null;default:0" json:"outside_bundle_count"`
	UnassignedTagCount         int64                  `gorm:"not null;default:0" json:"unassigned_tag_count"`
	MissingDeliveryCount       int64                  `gorm:"not null;default:0" json:"missing_delivery_count"`
	EligibleActionUIDCount     int64                  `gorm:"not null;default:0" json:"eligible_action_uid_count"`
	UploadedByCustomerID       uint                   `gorm:"not null" json:"uploaded_by_customer_id"`
	DeletedByCustomerID        *uint                  `json:"deleted_by_customer_id,omitempty"`
	ProcessedAt                *time.Time             `json:"processed_at,omitempty"`
	DeletedAt                  *time.Time             `json:"deleted_at,omitempty"`
	StartedAt                  *time.Time             `json:"started_at,omitempty"`
	ErrorCode                  *string                `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage               *string                `gorm:"type:varchar(255)" json:"error_message,omitempty"`
	CreatedAt                  time.Time              `json:"created_at"`
	UpdatedAt                  time.Time              `json:"updated_at"`
}

func (BundleActionFile) TableName() string { return "bundle_action_files" }

// BundleActionFileUID persists the syntactically-valid action membership. The
// nullable mapping fields are diagnostic snapshots only; metrics always join
// the immutable campaign attribution source of truth at refresh time.
type BundleActionFileUID struct {
	ID                 int64          `gorm:"primaryKey;autoIncrement;type:bigserial" json:"id"`
	BundleActionFileID int64          `gorm:"not null;uniqueIndex:uk_bundle_action_file_uid,priority:1" json:"bundle_action_file_id"`
	BundleID           uint           `gorm:"not null;index:idx_bundle_action_file_uids_bundle_uid,priority:1" json:"bundle_id"`
	UID                string         `gorm:"type:varchar(255);not null;uniqueIndex:uk_bundle_action_file_uid,priority:2;index:idx_bundle_action_file_uids_bundle_uid,priority:2" json:"uid"`
	CampaignID         *uint          `json:"campaign_id,omitempty"`
	AssignedTagID      *uint          `json:"assigned_tag_id,omitempty"`
	PhaseType          *CampaignPhase `gorm:"type:campaign_phase" json:"phase_type,omitempty"`
	ValidationStatus   string         `gorm:"type:varchar(32);not null" json:"validation_status"`
	CreatedAt          time.Time      `json:"created_at"`
}

func (BundleActionFileUID) TableName() string { return "bundle_action_file_uids" }

type BundleActionSummary struct {
	BundleID               uint      `gorm:"primaryKey" json:"bundle_id"`
	HasActiveActionFiles   bool      `gorm:"not null" json:"has_active_action_files"`
	ActionCount            int64     `gorm:"not null" json:"action_count"`
	EligibleDeliveredCount int64     `gorm:"not null" json:"eligible_delivered_count"`
	BundleAvgATR           *float64  `gorm:"->;column:bundle_avg_atr" json:"bundle_avg_atr"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (BundleActionSummary) TableName() string { return "bundle_action_summaries" }

type BundleActionCampaignMetric struct {
	BundleID               uint      `gorm:"primaryKey" json:"bundle_id"`
	CampaignID             uint      `gorm:"primaryKey" json:"campaign_id"`
	ActionCount            int64     `json:"action_count"`
	EligibleDeliveredCount int64     `json:"eligible_delivered_count"`
	CampaignATR            *float64  `gorm:"->;column:campaign_atr" json:"campaign_atr"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (BundleActionCampaignMetric) TableName() string { return "bundle_action_campaign_metrics" }

type BundleActionTagMetric struct {
	BundleID                      uint      `gorm:"primaryKey" json:"bundle_id"`
	TagID                         uint      `gorm:"primaryKey" json:"tag_id"`
	TestActionCount               int64     `json:"test_action_count"`
	TestEligibleDeliveredCount    int64     `json:"test_eligible_delivered_count"`
	TestPhaseAvgATR               *float64  `gorm:"->;column:test_phase_avg_atr" json:"test_phase_avg_atr"`
	OverallActionCount            int64     `json:"overall_action_count"`
	OverallEligibleDeliveredCount int64     `json:"overall_eligible_delivered_count"`
	OverallAvgATR                 *float64  `gorm:"->;column:overall_avg_atr" json:"overall_avg_atr"`
	UpdatedAt                     time.Time `json:"updated_at"`
}

func (BundleActionTagMetric) TableName() string { return "bundle_action_tag_metrics" }
