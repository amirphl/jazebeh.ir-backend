package models

import "time"

type TagTestReportStatus string

const (
	TagTestReportStatusNotPrepared TagTestReportStatus = "not_prepared"
	TagTestReportStatusPreparing   TagTestReportStatus = "preparing"
	TagTestReportStatusPrepared    TagTestReportStatus = "prepared"
	TagTestReportStatusFailed      TagTestReportStatus = "failed"
)

// Version 3 excludes campaign test-message clicks from all tag performance
// materializations. It supersedes version 2, which added Execution phase data.
const TagTestPerformanceCalculationVersion = 3

// CampaignTagTestReport is the durable, retryable calculation state for one
// attributable Smart Targeting Campaign. The historical name is retained for
// compatibility. Metric rows remain available while a generation is pending.
type CampaignTagTestReport struct {
	CampaignID            uint                `gorm:"primaryKey" json:"campaign_id"`
	BundleID              uint                `gorm:"not null;index:idx_campaign_tag_test_reports_bundle_status,priority:1" json:"bundle_id"`
	Status                TagTestReportStatus `gorm:"type:varchar(32);not null;index:idx_campaign_tag_test_reports_bundle_status,priority:2" json:"status"`
	CalculationVersion    int                 `gorm:"not null" json:"calculation_version"`
	AttemptCount          int                 `gorm:"not null" json:"attempt_count"`
	RequestedAt           time.Time           `gorm:"not null" json:"requested_at"`
	StartedAt             *time.Time          `json:"started_at,omitempty"`
	FinishedAt            *time.Time          `json:"finished_at,omitempty"`
	NextRetryAt           *time.Time          `json:"next_retry_at,omitempty"`
	LastCalculatedClickID int64               `gorm:"not null" json:"last_calculated_click_id"`
	ErrorCode             *string             `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage          *string             `gorm:"type:varchar(255)" json:"error_message,omitempty"`
	CreatedAt             time.Time           `gorm:"not null" json:"created_at"`
	UpdatedAt             time.Time           `gorm:"not null" json:"updated_at"`
}

func (CampaignTagTestReport) TableName() string { return "campaign_tag_test_reports" }

// CampaignTagTestPerformance is the latest complete per-Campaign/per-tag
// materialization shared by Test and Execution reporting. The historical type
// and table names are retained for migration and API compatibility.
type CampaignTagTestPerformance struct {
	ID                            int64         `gorm:"primaryKey" json:"id"`
	CampaignID                    uint          `gorm:"not null;uniqueIndex:uk_campaign_tag_test_performance,priority:1" json:"campaign_id"`
	BundleID                      uint          `gorm:"not null;index:idx_campaign_tag_test_performances_bundle_tag,priority:1;index:idx_campaign_tag_performances_bundle_phase_tag,priority:1" json:"bundle_id"`
	TagID                         uint          `gorm:"not null;uniqueIndex:uk_campaign_tag_test_performance,priority:2;index:idx_campaign_tag_test_performances_bundle_tag,priority:2;index:idx_campaign_tag_performances_bundle_phase_tag,priority:3" json:"tag_id"`
	PhaseType                     CampaignPhase `gorm:"type:campaign_phase;not null;index:idx_campaign_tag_performances_bundle_phase_tag,priority:2" json:"phase_type"`
	TagDisplayTitleSnapshot       string        `gorm:"type:text;not null" json:"tag_display_title_snapshot"`
	BundlePersonaFitScoreSnapshot *float64      `gorm:"type:numeric(5,2)" json:"bundle_persona_fit_score_snapshot"`
	SelectedCount                 int64         `gorm:"not null" json:"selected_count"`
	SentCount                     int64         `gorm:"not null" json:"sent_count"`
	DeliveredCount                int64         `gorm:"not null" json:"delivered_count"`
	ClickCount                    int64         `gorm:"not null" json:"click_count"`
	TestCampaignCTR               *float64      `gorm:"->;type:numeric" json:"test_campaign_ctr"`
	CalculationVersion            int           `gorm:"not null" json:"calculation_version"`
	CreatedAt                     time.Time     `gorm:"not null" json:"created_at"`
	UpdatedAt                     time.Time     `gorm:"not null" json:"updated_at"`
}

func (CampaignTagTestPerformance) TableName() string {
	return "campaign_tag_test_performances"
}

// TagTestPhasePerformanceSummary stores the weighted Test CTR for a tag in a
// Bundle. TestPhaseAvgCTR is total clicks / total deliveries, never an average
// of Campaign percentages.
type TagTestPhasePerformanceSummary struct {
	ID                      int64     `gorm:"primaryKey" json:"id"`
	BundleID                uint      `gorm:"not null;uniqueIndex:uk_tag_test_phase_performance_bundle_tag,priority:1" json:"bundle_id"`
	TagID                   uint      `gorm:"not null;uniqueIndex:uk_tag_test_phase_performance_bundle_tag,priority:2" json:"tag_id"`
	TotalTestSelectedCount  int64     `gorm:"not null" json:"total_test_selected_count"`
	TotalTestSentCount      int64     `gorm:"not null" json:"total_test_sent_count"`
	TotalTestDeliveredCount int64     `gorm:"not null" json:"total_test_delivered_count"`
	TotalTestClickCount     int64     `gorm:"not null" json:"total_test_click_count"`
	TestPhaseAvgCTR         *float64  `gorm:"->;type:numeric" json:"test_phase_avg_ctr"`
	CalculationVersion      int       `gorm:"not null" json:"calculation_version"`
	CreatedAt               time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt               time.Time `gorm:"not null" json:"updated_at"`
}

func (TagTestPhasePerformanceSummary) TableName() string {
	return "tag_test_phase_performance_summaries"
}

// TagOverallPerformanceSummary is the Bundle-scoped weighted performance of
// one tag across materialized Smart Targeting Test and Execution Campaigns.
type TagOverallPerformanceSummary struct {
	BundleID            uint      `gorm:"primaryKey" json:"bundle_id"`
	TagID               uint      `gorm:"primaryKey" json:"tag_id"`
	TotalSelectedCount  int64     `gorm:"not null" json:"total_selected_count"`
	TotalSentCount      int64     `gorm:"not null" json:"total_sent_count"`
	TotalDeliveredCount int64     `gorm:"not null" json:"total_delivered_count"`
	TotalClickCount     int64     `gorm:"not null" json:"total_click_count"`
	OverallAvgCTR       *float64  `gorm:"->;type:numeric" json:"overall_avg_ctr"`
	CalculationVersion  int       `gorm:"not null" json:"calculation_version"`
	CreatedAt           time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt           time.Time `gorm:"not null" json:"updated_at"`
}

func (TagOverallPerformanceSummary) TableName() string {
	return "tag_overall_performance_summaries"
}

type TagTestPerformanceSchedulerState struct {
	ID               int16     `gorm:"primaryKey" json:"id"`
	LastClickID      int64     `gorm:"not null" json:"last_click_id"`
	LastSourceScanAt time.Time `gorm:"not null" json:"last_source_scan_at"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null" json:"updated_at"`
}

func (TagTestPerformanceSchedulerState) TableName() string {
	return "tag_test_performance_scheduler_state"
}
