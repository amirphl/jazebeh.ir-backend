package dto

import "time"

type BundleActionFileItem struct {
	ID                         int64      `json:"id"`
	OriginalFileName           string     `json:"original_file_name"`
	Status                     string     `json:"status"`
	TotalRowCount              int64      `json:"total_row_count"`
	UniqueUIDCount             int64      `json:"unique_uid_count"`
	NewActionUIDCount          int64      `json:"new_action_uid_count"`
	DuplicateInFileCount       int64      `json:"duplicate_in_file_count"`
	DuplicateInOtherFilesCount int64      `json:"duplicate_in_other_files_count"`
	InvalidUIDCount            int64      `json:"invalid_uid_count"`
	OutsideBundleCount         int64      `json:"outside_bundle_count"`
	UnassignedTagCount         int64      `json:"unassigned_tag_count"`
	MissingDeliveryCount       int64      `json:"missing_delivery_count"`
	EligibleActionUIDCount     int64      `json:"eligible_action_uid_count"`
	ErrorCode                  *string    `json:"error_code,omitempty"`
	ErrorMessage               *string    `json:"error_message,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	ProcessedAt                *time.Time `json:"processed_at,omitempty"`
}
type BundleActionFilesResponse struct {
	Items      []BundleActionFileItem `json:"items"`
	Pagination PaginationInfo         `json:"pagination"`
}
type BundleActionSummaryResponse struct {
	BundleID               uint      `json:"bundle_id"`
	HasActiveActionFiles   bool      `json:"has_active_action_files"`
	ActionCount            int64     `json:"action_count"`
	EligibleDeliveredCount int64     `json:"eligible_delivered_count"`
	BundleAvgATR           *float64  `json:"bundle_avg_atr"`
	UpdatedAt              time.Time `json:"updated_at"`
}
type BundleActionTagMetricItem struct {
	TagID                         uint     `json:"tag_id"`
	TestActionCount               int64    `json:"test_action_count"`
	TestEligibleDeliveredCount    int64    `json:"test_eligible_delivered_count"`
	TestPhaseAvgATR               *float64 `json:"test_phase_avg_atr"`
	OverallActionCount            int64    `json:"overall_action_count"`
	OverallEligibleDeliveredCount int64    `json:"overall_eligible_delivered_count"`
	OverallAvgATR                 *float64 `json:"overall_avg_atr"`
}
type CampaignActionMetricResponse struct {
	CampaignID             uint     `json:"campaign_id"`
	ActionCount            int64    `json:"action_count"`
	EligibleDeliveredCount int64    `json:"eligible_delivered_count"`
	CampaignATR            *float64 `json:"campaign_atr"`
}
