package models

import (
	"encoding/json"
	"time"
)

const (
	BundleTagEvaluationEventCreated                  = "evaluation_created"
	BundleTagEvaluationEventStarted                  = "evaluation_started"
	BundleTagEvaluationEventPersonaAnalysisStarted   = "persona_analysis_started"
	BundleTagEvaluationEventPersonaResponseReceived  = "persona_analysis_response_received"
	BundleTagEvaluationEventPersonaAnalysisCompleted = "persona_analysis_completed"
	BundleTagEvaluationEventBatchCreated             = "batch_created"
	BundleTagEvaluationEventBatchStarted             = "batch_started"
	BundleTagEvaluationEventBatchResponseReceived    = "batch_response_received"
	BundleTagEvaluationEventBatchCompleted           = "batch_completed"
	BundleTagEvaluationEventBatchFailed              = "batch_failed"
	BundleTagEvaluationEventCompleted                = "evaluation_completed"
	BundleTagEvaluationEventFailed                   = "evaluation_failed"
)

const (
	BundleTagEvaluationStatusNotEvaluated   = "not_evaluated"
	BundleTagEvaluationStatusEvaluating     = "evaluating"
	BundleTagEvaluationStatusEvaluated      = "evaluated"
	BundleTagEvaluationStatusUpdateRequired = "update_required"
	BundleTagEvaluationStatusError          = "error"
)

type BundleTagEvaluationRun struct {
	ID                            int64           `gorm:"primaryKey;autoIncrement;type:bigserial"                                                                                                                            json:"id"`
	BundleID                      uint            `gorm:"not null;index:idx_bundle_tag_eval_runs_bundle_created,priority:1"                                                                                                  json:"bundle_id"`
	CustomerID                    uint            `gorm:"not null;index:idx_bundle_tag_eval_runs_customer_created,priority:1"                                                                                                json:"customer_id"`
	TargetPersonaSnapshot         string          `gorm:"type:text;not null"                                                                                                                                                 json:"target_persona_snapshot"`
	PersonaAnalysisPromptSnapshot string          `gorm:"type:text;not null"                                                                                                                                                 json:"persona_analysis_prompt_snapshot"`
	ConfigurationSnapshot         json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"                                                                                                                                   json:"configuration_snapshot"`
	TagBatchSize                  int             `gorm:"not null"                                                                                                                                                           json:"tag_batch_size"`
	CreatedAt                     time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_eval_runs_bundle_created,priority:2;index:idx_bundle_tag_eval_runs_customer_created,priority:2" json:"created_at"`
}

func (BundleTagEvaluationRun) TableName() string { return "bundle_tag_evaluation_runs" }

type BundleTagEvaluationEvent struct {
	ID              uint64          `gorm:"primaryKey"                                                                                                                                                      json:"id"`
	EvaluationRunID int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_eval_events_run_created,priority:1"                                                                                    json:"evaluation_run_id"`
	BatchID         *int64          `gorm:"type:bigint"                                                                                                                                                     json:"batch_id,omitempty"`
	EventType       string          `gorm:"type:text;not null;index:idx_bundle_tag_eval_events_type_created,priority:1"                                                                                     json:"event_type"`
	Payload         json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"                                                                                                                                json:"payload"`
	CreatedAt       time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_eval_events_run_created,priority:2;index:idx_bundle_tag_eval_events_type_created,priority:2" json:"created_at"`
}

func (BundleTagEvaluationEvent) TableName() string { return "bundle_tag_evaluation_events" }

type BundleTagPersonaAnalysisAttempt struct {
	ID                    int64           `gorm:"primaryKey;autoIncrement;type:bigserial"                                                                     json:"id"`
	EvaluationRunID       int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_persona_attempts_run_attempt,priority:1"                           json:"evaluation_run_id"`
	AttemptNumber         int             `gorm:"not null"                                                                                                    json:"attempt_number"`
	RequestPayload        json.RawMessage `gorm:"type:jsonb;not null"                                                                                         json:"request_payload"`
	RawResponse           *string         `gorm:"type:text"                                                                                                   json:"raw_response,omitempty"`
	ExtractedResponseText *string         `gorm:"type:text"                                                                                                   json:"extracted_response_text,omitempty"`
	HTTPStatusCode        *int            `                                                                                                                   json:"http_status_code,omitempty"`
	ProviderResponseID    *string         `gorm:"type:text"                                                                                                   json:"provider_response_id,omitempty"`
	ModelName             string          `gorm:"type:text;not null"                                                                                          json:"model_name"`
	UsageMetadata         json.RawMessage `gorm:"type:jsonb"                                                                                                  json:"usage_metadata,omitempty"`
	ErrorMessage          *string         `gorm:"type:text"                                                                                                   json:"error_message,omitempty"`
	RequestedAt           time.Time       `gorm:"not null"                                                                                                    json:"requested_at"`
	RespondedAt           *time.Time      `                                                                                                                   json:"responded_at,omitempty"`
	CreatedAt             time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_persona_attempts_run_attempt,priority:2" json:"created_at"`
}

func (BundleTagPersonaAnalysisAttempt) TableName() string {
	return "bundle_tag_persona_analysis_attempts"
}

type BundleTagEvaluationBatch struct {
	ID              int64           `gorm:"primaryKey;autoIncrement;type:bigserial"                                                               json:"id"`
	EvaluationRunID int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_eval_batches_run_batch,priority:1"                           json:"evaluation_run_id"`
	BatchNumber     int             `gorm:"not null"                                                                                              json:"batch_number"`
	TagCount        int             `gorm:"not null"                                                                                              json:"tag_count"`
	FirstTagID      uint            `gorm:"not null"                                                                                              json:"first_tag_id"`
	LastTagID       uint            `gorm:"not null"                                                                                              json:"last_tag_id"`
	TagsSnapshot    json.RawMessage `gorm:"type:jsonb;not null"                                                                                   json:"tags_snapshot"`
	CreatedAt       time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_eval_batches_run_batch,priority:2" json:"created_at"`
}

func (BundleTagEvaluationBatch) TableName() string { return "bundle_tag_evaluation_batches" }

type BundleTagEvaluationBatchAttempt struct {
	ID                 int64           `gorm:"primaryKey;autoIncrement;type:bigserial"                                                                          json:"id"`
	BatchID            int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_eval_batch_attempts_batch_attempt,priority:1"                           json:"batch_id"`
	AttemptNumber      int             `gorm:"not null"                                                                                                         json:"attempt_number"`
	RequestPayload     json.RawMessage `gorm:"type:jsonb;not null"                                                                                              json:"request_payload"`
	RawResponse        *string         `gorm:"type:text"                                                                                                        json:"raw_response,omitempty"`
	HTTPStatusCode     *int            `                                                                                                                        json:"http_status_code,omitempty"`
	ProviderResponseID *string         `gorm:"type:text"                                                                                                        json:"provider_response_id,omitempty"`
	ModelName          string          `gorm:"type:text;not null"                                                                                               json:"model_name"`
	UsageMetadata      json.RawMessage `gorm:"type:jsonb"                                                                                                       json:"usage_metadata,omitempty"`
	ErrorMessage       *string         `gorm:"type:text"                                                                                                        json:"error_message,omitempty"`
	RequestedAt        time.Time       `gorm:"not null"                                                                                                         json:"requested_at"`
	RespondedAt        *time.Time      `                                                                                                                        json:"responded_at,omitempty"`
	CreatedAt          time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_eval_batch_attempts_batch_attempt,priority:2" json:"created_at"`
}

func (BundleTagEvaluationBatchAttempt) TableName() string {
	return "bundle_tag_evaluation_batch_attempts"
}

type BundleTagScore struct {
	ID                       uint64          `gorm:"primaryKey"                                                                                                                                                         json:"id"`
	EvaluationRunID          int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_scores_run_tag,priority:1"                                                                                                json:"evaluation_run_id"`
	BatchID                  int64           `gorm:"type:bigint;not null;index:idx_bundle_tag_scores_batch_tag,priority:1"                                                                                              json:"batch_id"`
	BatchAttemptID           int64           `gorm:"type:bigint;not null"                                                                                                                                               json:"batch_attempt_id"`
	BundleID                 uint            `gorm:"not null;index:idx_bundle_tag_scores_bundle_tag_created,priority:1"                                                                                                 json:"bundle_id"`
	TagID                    uint            `gorm:"not null;index:idx_bundle_tag_scores_bundle_tag_created,priority:2;index:idx_bundle_tag_scores_run_tag,priority:2;index:idx_bundle_tag_scores_batch_tag,priority:2" json:"tag_id"`
	TagNameSnapshot          *string         `gorm:"type:text"                                                                                                                                                          json:"tag_name_snapshot,omitempty"`
	TagDisplayTitleSnapshot  *string         `gorm:"type:text"                                                                                                                                                          json:"tag_display_title_snapshot,omitempty"`
	TagPersonaSnapshot       *string         `gorm:"type:text"                                                                                                                                                          json:"tag_persona_snapshot,omitempty"`
	TagAudienceCountSnapshot *int64          `gorm:"type:bigint"                                                                                                                                                        json:"tag_audience_count_snapshot,omitempty"`
	BundleFitScore           float64         `gorm:"type:numeric(5,2);not null"                                                                                                                                         json:"bundle_fit_score"`
	FitLevel                 string          `gorm:"type:text;not null"                                                                                                                                                 json:"fit_level"`
	RelationType             string          `gorm:"type:text;not null"                                                                                                                                                 json:"relation_type"`
	Reason                   string          `gorm:"type:text;not null"                                                                                                                                                 json:"reason"`
	RawResult                json.RawMessage `gorm:"type:jsonb;not null"                                                                                                                                                json:"raw_result"`
	CreatedAt                time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bundle_tag_scores_bundle_tag_created,priority:3"                                                           json:"created_at"`
}

func (BundleTagScore) TableName() string { return "bundle_tag_scores" }

type BundleTagEvaluationRunStatus struct {
	EvaluationRunID       int64           `gorm:"column:evaluation_run_id"       json:"evaluation_run_id"`
	BundleID              uint            `gorm:"column:bundle_id"               json:"bundle_id"`
	CustomerID            uint            `gorm:"column:customer_id"             json:"customer_id"`
	TargetPersonaSnapshot string          `gorm:"column:target_persona_snapshot" json:"target_persona_snapshot"`
	EvaluationCreatedAt   time.Time       `gorm:"column:evaluation_created_at"   json:"evaluation_created_at"`
	LatestEventType       *string         `gorm:"column:latest_event_type"       json:"latest_event_type,omitempty"`
	LatestEventPayload    json.RawMessage `gorm:"column:latest_event_payload"    json:"latest_event_payload,omitempty"`
	LatestEventAt         *time.Time      `gorm:"column:latest_event_at"         json:"latest_event_at,omitempty"`
	RunStatus             string          `gorm:"column:run_status"              json:"run_status"`
}

func (BundleTagEvaluationRunStatus) TableName() string { return "bundle_tag_evaluation_run_status" }

type CurrentBundleTagScore struct {
	BundleTagScore
}

func (CurrentBundleTagScore) TableName() string { return "current_bundle_tag_scores" }

type CurrentBundleTagEvaluationStatus struct {
	BundleID              uint       `gorm:"column:bundle_id"                json:"bundle_id"`
	CustomerID            uint       `gorm:"column:customer_id"              json:"customer_id"`
	Status                string     `gorm:"column:status"                   json:"status"`
	LatestRunID           *int64     `gorm:"column:latest_run_id"            json:"latest_run_id,omitempty"`
	LatestSuccessfulRunID *int64     `gorm:"column:latest_successful_run_id" json:"latest_successful_run_id,omitempty"`
	LatestRunCreatedAt    *time.Time `gorm:"column:latest_run_created_at"    json:"latest_run_created_at,omitempty"`
	LatestCompletedAt     *time.Time `gorm:"column:latest_completed_at"      json:"latest_completed_at,omitempty"`
	LatestErrorMessage    *string    `gorm:"column:latest_error_message"     json:"latest_error_message,omitempty"`
	LatestErrorAt         *time.Time `gorm:"column:latest_error_at"          json:"latest_error_at,omitempty"`
}

func (CurrentBundleTagEvaluationStatus) TableName() string {
	return "current_bundle_tag_evaluation_status"
}

type BundleTagEvaluationTagSnapshot struct {
	TagID              uint   `json:"tag_id"`
	TagName            string `json:"tag_name"`
	TagDisplayTitle    string `json:"tag_display_title"`
	TagAudiencePersona string `json:"tag_audience_persona"`
	TagAudienceCount   int64  `json:"tag_audience_count"`
}
