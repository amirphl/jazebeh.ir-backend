package dto

import "time"

// ListSmartTargetingTagsRequest carries normalized list filters from either a
// campaign-scoped or bundle-scoped Smart Targeting endpoint.
type ListSmartTargetingTagsRequest struct {
	CustomerID   uint
	BundleID     uint
	CampaignUUID string
	Search       string
	// Capacity, when present, returns only tags whose capacity is strictly
	// greater than this value. A pointer keeps an omitted filter distinct from
	// a zero-capacity threshold.
	Capacity      *int64
	SortBy        string
	SortDirection string
	Page          int
	PageSize      int
}

// SmartTargetingTagItem is one selectable tag row. Evaluated bundles expose
// the tag metadata and score explanation captured by their latest completed
// evaluation. Unevaluated bundles expose live tag metadata and nil evaluation
// fields. Nullable CTR values mean that those metrics are not yet available.
type SmartTargetingTagItem struct {
	TagID uint `json:"tag_id"`
	// TagName               string   `json:"tag_name"`
	TagDisplayTitle *string `json:"tag_display_title"`
	// TagAudiencePersona    *string  `json:"tag_audience_persona"`
	TagCapacity           *int64   `json:"tag_capacity"`
	BundlePersonaFitScore *float64 `json:"bundle_persona_fit_score"`
	EvaluationRunID       *int64   `json:"evaluation_run_id"`
	FitLevel              *string  `json:"fit_level"`
	RelationType          *string  `json:"relation_type"`
	// TestPhaseAvgCTR is the weighted Bundle/tag Test CTR. It is null when the
	// materialized denominator is zero or no Test report exists.
	TestPhaseAvgCTR         *float64 `json:"test_phase_avg_ctr"`
	TotalTestSelectedCount  *int64   `json:"total_test_selected_count"`
	TotalTestSentCount      *int64   `json:"total_test_sent_count"`
	TotalTestDeliveredCount *int64   `json:"total_test_delivered_count"`
	TotalTestClickCount     *int64   `json:"total_test_click_count"`

	// Campaign-specific fields are populated only by the campaign-scoped tag
	// endpoint and remain null before a report has been prepared.
	SelectedCount   *int64   `json:"selected_count"`
	SentCount       *int64   `json:"sent_count"`
	DeliveredCount  *int64   `json:"delivered_count"`
	ClickCount      *int64   `json:"click_count"`
	TestCampaignCTR *float64 `json:"test_campaign_ctr"`

	// OverallAvgCTR is global across attributable Test and Execution Campaigns
	// and remains null when no attributed audience has been delivered.
	OverallAvgCTR *float64 `json:"overall_avg_ctr"`
	Selected      bool     `json:"selected"`
}

// SmartTargetingSelectionSummary describes the complete selection, not only
// the tags visible on the current page.
type SmartTargetingSelectionSummary struct {
	SelectedTagCount    int64 `json:"selected_tag_count"`
	SelectedRawCapacity int64 `json:"selected_raw_capacity"`
}

// ListSmartTargetingTagsResponse combines the requested page with the complete
// selected-ID set so pagination never loses the customer's selection state.
type ListSmartTargetingTagsResponse struct {
	Items                  []SmartTargetingTagItem        `json:"items"`
	Pagination             PaginationInfo                 `json:"pagination"`
	SelectedTagIDs         []uint                         `json:"selected_tag_ids"`
	Summary                SmartTargetingSelectionSummary `json:"summary"`
	EvaluationAvailable    bool                           `json:"evaluation_available"`
	EffectiveSortBy        string                         `json:"effective_sort_by"`
	EffectiveSortDirection string                         `json:"effective_sort_direction"`
}

// ReplaceSmartTargetingSelectionRequest replaces the entire campaign
// selection. TagIDs must be unique and available in the bundle's effective
// source: its current score snapshot, or active live tags when no scores exist.
type ReplaceSmartTargetingSelectionRequest struct {
	CustomerID   uint   `json:"-"`
	CampaignUUID string `json:"-"`
	TagIDs       []uint `json:"tag_ids" validate:"required,min=1,max=10000,dive,min=1"`
}

// AutoSelectSmartTargetingTagsRequest selects the first Count tags from the
// complete normalized filter and sort order.
type AutoSelectSmartTargetingTagsRequest struct {
	CustomerID    uint   `json:"-"`
	CampaignUUID  string `json:"-"`
	Count         int    `json:"count" validate:"required,min=1,max=10000"`
	Search        string `json:"search,omitempty" validate:"omitempty,max=200"`
	SortBy        string `json:"sort_by,omitempty"`
	SortDirection string `json:"sort_direction,omitempty"`
}

// SmartTargetingSelectionResponse returns the complete persisted selection.
type SmartTargetingSelectionResponse struct {
	SelectedTagIDs []uint                         `json:"selected_tag_ids"`
	Summary        SmartTargetingSelectionSummary `json:"summary"`
}

// StartSmartTargetingCapacityCalculationRequest starts an asynchronous exact
// capacity generation for the campaign identified by the URL. An omitted
// score_classes value reuses the campaign's persisted audience grades; an
// empty persisted selection is the deterministic shorthand for all classes.
type StartSmartTargetingCapacityCalculationRequest struct {
	CustomerID   uint     `json:"-"`
	CampaignUUID string   `json:"-"`
	ScoreClasses []string `json:"score_classes,omitempty" validate:"omitempty,max=3,dive,oneof=A B C a b c"`
}

// SmartTargetingCapacityCalculationResponse is used both by the start
// endpoint and by polling endpoints. Count pointers distinguish a real zero
// result from a result which is pending, failed, expired, or stale.
type SmartTargetingCapacityCalculationResponse struct {
	CalculationID         int64      `json:"calculation_id"`
	CampaignID            uint       `json:"campaign_id"`
	BundleID              uint       `json:"bundle_id"`
	Status                string     `json:"status"`
	IsCurrent             bool       `json:"is_current"`
	RecalculationRequired bool       `json:"recalculation_required"`
	SelectedScoreClasses  []string   `json:"selected_score_classes"`
	SelectedTagCount      int        `json:"selected_tag_count"`
	RawAudienceCount      *uint64    `json:"raw_audience_count,omitempty"`
	EligibleUniqueCount   *uint64    `json:"eligible_unique_audience_count_before_approved_campaign_deduction,omitempty"`
	ApprovedDeduction     *uint64    `json:"approved_campaign_audience_deduction,omitempty"`
	UsableUniqueCount     *uint64    `json:"usable_unique_audience_count,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	ErrorCode             *string    `json:"error_code,omitempty"`
	ErrorMessage          *string    `json:"error_message,omitempty"`
}

// SmartTargetingTestSamplingPreviewRequest submits an asynchronous sampling
// calculation for the campaign identified by the URL.
type SmartTargetingTestSamplingPreviewRequest struct {
	CustomerID   uint   `json:"-"`
	CampaignUUID string `json:"-"`
}

type SmartTargetingTestSamplingTagResult struct {
	TagID          uint    `json:"tag_id"`
	TagDisplayName *string `json:"tag_display_name"`
	SelectionOrder int     `json:"selection_order"`
	Satisfied      bool    `json:"satisfied"`
	AvailableCount int64   `json:"available_count"`
}

type SmartTargetingTestSamplingPreviewResponse struct {
	SampleSizePerTag       uint64                                `json:"sample_size_per_tag"`
	TagSamplingOrder       []uint                                `json:"tag_sampling_order"`
	SatisfiedTags          []SmartTargetingTestSamplingTagResult `json:"satisfied_tags"`
	UnsatisfiedTags        []SmartTargetingTestSamplingTagResult `json:"unsatisfied_tags"`
	SatisfiedTagCount      int                                   `json:"satisfied_tag_count"`
	EffectiveAudienceCount uint64                                `json:"effective_audience_count"`
	CampaignCost           uint64                                `json:"campaign_cost"`
}

// SmartTargetingTestSamplingCalculationResponse is returned by both job
// submission and polling. Result pointers distinguish a completed zero result
// from a calculation which has not completed.
type SmartTargetingTestSamplingCalculationResponse struct {
	CalculationID          int64                                 `json:"calculation_id"`
	CampaignID             uint                                  `json:"campaign_id"`
	BundleID               uint                                  `json:"bundle_id"`
	Status                 string                                `json:"status"`
	IsCurrent              bool                                  `json:"is_current"`
	RecalculationRequired  bool                                  `json:"recalculation_required"`
	SampleSizePerTag       uint64                                `json:"sample_size_per_tag"`
	TagSamplingOrder       []uint                                `json:"tag_sampling_order"`
	SelectedScoreClasses   []string                              `json:"selected_score_classes"`
	SatisfiedTags          []SmartTargetingTestSamplingTagResult `json:"satisfied_tags,omitempty"`
	UnsatisfiedTags        []SmartTargetingTestSamplingTagResult `json:"unsatisfied_tags,omitempty"`
	SatisfiedTagCount      *int                                  `json:"satisfied_tag_count,omitempty"`
	EffectiveAudienceCount *uint64                               `json:"effective_audience_count,omitempty"`
	CampaignCost           *uint64                               `json:"campaign_cost,omitempty"`
	CreatedAt              time.Time                             `json:"created_at"`
	StartedAt              *time.Time                            `json:"started_at,omitempty"`
	FinishedAt             *time.Time                            `json:"finished_at,omitempty"`
	ErrorCode              *string                               `json:"error_code,omitempty"`
	ErrorMessage           *string                               `json:"error_message,omitempty"`
}
