package dto

import "time"

type CreateBundleRequest struct {
	CustomerID            uint    `json:"-"`
	Title                 string  `json:"title"                          validate:"required,max=255"`
	Objective             string  `json:"objective"                      validate:"required,max=1023"`
	TargetAudiencePersona string  `json:"target_audience_persona"        validate:"required,max=1023"`
	AdLink                *string `json:"adlink,omitempty"               validate:"omitempty,max=2047"`
	ShortLinkDomain       *string `json:"short_link_domain,omitempty"    validate:"omitempty,max=255"`
	Description           *string `json:"description,omitempty"          validate:"omitempty,max=2047"`
	TargetCustomerName    *string `json:"target_customer_name,omitempty" validate:"omitempty,max=255"`
	Category              *string `json:"job_category,omitempty"         validate:"omitempty,max=255"`
	Job                   *string `json:"job,omitempty"                  validate:"omitempty,max=255"`
}

type CreateBundleResponse struct {
	Message   string    `json:"message"`
	ID        uint      `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type UpdateBundleRequest struct {
	CustomerID            uint    `json:"-"`
	ID                    uint    `json:"-"`
	Title                 string  `json:"title"                          validate:"required,max=255"`
	Objective             string  `json:"objective"                      validate:"required,max=1023"`
	TargetAudiencePersona string  `json:"target_audience_persona"        validate:"required,max=1023"`
	AdLink                *string `json:"adlink,omitempty"               validate:"omitempty,max=2047"`
	ShortLinkDomain       *string `json:"short_link_domain,omitempty"    validate:"omitempty,max=255"`
	Description           *string `json:"description,omitempty"          validate:"omitempty,max=2047"`
	TargetCustomerName    *string `json:"target_customer_name,omitempty" validate:"omitempty,max=255"`
	Category              *string `json:"job_category,omitempty"         validate:"omitempty,max=255"`
	Job                   *string `json:"job,omitempty"                  validate:"omitempty,max=255"`
}

type UpdateBundleResponse struct {
	Message   string    `json:"message"`
	ID        uint      `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GetBundleRequest struct {
	CustomerID uint `json:"-"`
	ID         uint `json:"id" validate:"required,min=1"`
}

type GetBundleResponse struct {
	Message string      `json:"message"`
	Item    *BundleItem `json:"item,omitempty"`
}

type ListBundlesFilter struct {
	Title              *string `json:"title,omitempty"                validate:"omitempty,max=255"`
	TargetCustomerName *string `json:"target_customer_name,omitempty" validate:"omitempty,max=255"`
}

type ListBundlesRequest struct {
	CustomerID uint               `json:"-"`
	Page       int                `json:"page"             validate:"omitempty,min=1,max=100"`
	Limit      int                `json:"limit"            validate:"omitempty,min=1,max=100"`
	Filter     *ListBundlesFilter `json:"filter,omitempty" validate:"omitempty"`
}

type BundleItem struct {
	ID                    uint           `json:"id"`
	Title                 string         `json:"title"`
	Objective             string         `json:"objective"`
	TargetAudiencePersona string         `json:"target_audience_persona"`
	AdLink                *string        `json:"adlink,omitempty"`
	Description           *string        `json:"description,omitempty"`
	ShortLinkDomain       *string        `json:"short_link_domain,omitempty"`
	TargetCustomerName    *string        `json:"target_customer_name,omitempty"`
	Category              *string        `json:"job_category,omitempty"`
	Job                   *string        `json:"job,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`
	Statistics            map[string]any `json:"statistics,omitempty"`
	TagEvaluationStatus   *string        `json:"tag_evaluation_status,omitempty"`
	TagEvaluatedAt        *time.Time     `json:"tag_evaluated_at,omitempty"`
	CustomerID            uint           `json:"customer_id"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type ListBundlesResponse struct {
	Message    string         `json:"message"`
	Items      []BundleItem   `json:"items"`
	Pagination PaginationInfo `json:"pagination"`
}

type RequestBundleTagEvaluationRequest struct {
	CustomerID uint `json:"-"`
	BundleID   uint `json:"-"`
}

type RequestBundleTagEvaluationResponse struct {
	Message         string    `json:"message"`
	EvaluationRunID int64     `json:"evaluation_run_id"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type GetBundleTagEvaluationStatusRequest struct {
	CustomerID uint `json:"-"`
	BundleID   uint `json:"-"`
}

type BundleTagEvaluationStatusItem struct {
	BundleID              uint       `json:"bundle_id"`
	Status                string     `json:"status"`
	LatestRunID           *int64     `json:"latest_run_id,omitempty"`
	LatestSuccessfulRunID *int64     `json:"latest_successful_run_id,omitempty"`
	LatestRunCreatedAt    *time.Time `json:"latest_run_created_at,omitempty"`
	LatestCompletedAt     *time.Time `json:"latest_completed_at,omitempty"`
	LatestErrorMessage    *string    `json:"latest_error_message,omitempty"`
	LatestErrorAt         *time.Time `json:"latest_error_at,omitempty"`
}

type GetBundleTagEvaluationStatusResponse struct {
	Message string                         `json:"message"`
	Item    *BundleTagEvaluationStatusItem `json:"item,omitempty"`
}

type ListBundleTagScoresRequest struct {
	CustomerID uint `json:"-"`
	BundleID   uint `json:"-"`
	Page       int  `json:"page"  validate:"omitempty,min=1,max=100"`
	Limit      int  `json:"limit" validate:"omitempty,min=1,max=100"`
}

type BundleTagScoreItem struct {
	EvaluationRunID          int64   `json:"evaluation_run_id"`
	TagID                    uint    `json:"tag_id"`
	TagNameSnapshot          *string `json:"tag_name_snapshot,omitempty"`
	TagDisplayTitleSnapshot  *string `json:"tag_display_title_snapshot,omitempty"`
	TagPersonaSnapshot       *string `json:"tag_persona_snapshot,omitempty"`
	TagAudienceCountSnapshot *int64  `json:"tag_audience_count_snapshot,omitempty"`
	BundleFitScore           float64 `json:"bundle_fit_score"`
	FitLevel                 string  `json:"fit_level"`
	RelationType             string  `json:"relation_type"`
	Reason                   string  `json:"reason"`
}

type ListBundleTagScoresResponse struct {
	Message    string               `json:"message"`
	Items      []BundleTagScoreItem `json:"items"`
	Pagination PaginationInfo       `json:"pagination"`
}
