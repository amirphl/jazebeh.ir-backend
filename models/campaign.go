package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// CampaignStatus represents the status of an campaign
type CampaignStatus string
type CampaignPhase string

const (
	CampaignAudienceTargetingStandard                = "standard"
	CampaignAudienceTargetingSmart                   = "smart_targeting"
	CampaignAudienceTargetingExcel                   = "excel"
	CampaignPlatformSMS                              = "sms"
	CampaignPlatformRubika                           = "rubika"
	CampaignPlatformBale                             = "bale"
	CampaignPlatformSPlus                            = "splus"
	CampaignPhaseTest                 CampaignPhase  = "test"
	CampaignPhaseExecution            CampaignPhase  = "execution"
	CampaignStatusInitiated           CampaignStatus = "initiated"
	CampaignStatusInProgress          CampaignStatus = "in-progress"
	CampaignStatusWaitingForApproval  CampaignStatus = "waiting-for-approval"
	CampaignStatusApproved            CampaignStatus = "approved"
	CampaignStatusRunning             CampaignStatus = "running"
	CampaignStatusExecuted            CampaignStatus = "executed"
	CampaignStatusExpired             CampaignStatus = "expired"
	CampaignStatusRejected            CampaignStatus = "rejected"
	CampaignStatusCancelled           CampaignStatus = "cancelled"
	CampaignStatusCancelledByAdmin    CampaignStatus = "cancelled-by-admin"
)

func IsValidCampaignPlatform(p string) bool {
	switch p {
	case CampaignPlatformSMS, CampaignPlatformRubika, CampaignPlatformBale, CampaignPlatformSPlus:
		return true
	default:
		return false
	}
}

// String returns the string representation of the status
func (s CampaignStatus) String() string {
	return string(s)
}

// Valid checks if the status is valid
func (s CampaignStatus) Valid() bool {
	switch s {
	case CampaignStatusInitiated, CampaignStatusInProgress,
		CampaignStatusWaitingForApproval, CampaignStatusApproved,
		CampaignStatusRejected, CampaignStatusCancelled,
		CampaignStatusCancelledByAdmin,
		CampaignStatusRunning, CampaignStatusExecuted, CampaignStatusExpired:
		return true
	default:
		return false
	}
}

// Scan implements the sql.Scanner interface for CampaignStatus
func (s *CampaignStatus) Scan(value any) error {
	if value == nil {
		*s = ""
		return nil
	}

	switch v := value.(type) {
	case string:
		*s = CampaignStatus(v)
	case []byte:
		*s = CampaignStatus(string(v))
	default:
		return fmt.Errorf("cannot scan %T into CampaignStatus", value)
	}

	return nil
}

// Value implements the driver.Valuer interface for CampaignStatus
func (s CampaignStatus) Value() (driver.Value, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("invalid CampaignStatus: %s", s)
	}
	return string(s), nil
}

func (p CampaignPhase) String() string {
	return string(p)
}

func (p CampaignPhase) Valid() bool {
	switch p {
	case CampaignPhaseTest, CampaignPhaseExecution:
		return true
	default:
		return false
	}
}

func (p *CampaignPhase) Scan(value any) error {
	if value == nil {
		*p = ""
		return nil
	}

	switch v := value.(type) {
	case string:
		*p = CampaignPhase(v)
	case []byte:
		*p = CampaignPhase(string(v))
	default:
		return fmt.Errorf("cannot scan %T into CampaignPhase", value)
	}

	return nil
}

func (p CampaignPhase) Value() (driver.Value, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("invalid CampaignPhase: %s", p)
	}
	return string(p), nil
}

// CampaignSpec represents the JSON specification for an campaign
type CampaignSpec struct {
	// Campaign details
	Title *string `json:"title,omitempty"`

	// Target audience
	Level1  *string  `json:"level1,omitempty"`
	Level2s []string `json:"level2s,omitempty"`
	Level3s []string `json:"level3s,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// AudienceTargetingMethod is empty for legacy campaigns. Legacy campaigns
	// with an Excel file are inferred as Excel targeting; all others are
	// inferred as standard targeting. When present, this is authoritative even
	// if values for another targeting mode are also retained in the spec.
	AudienceTargetingMethod *string  `json:"audience_targeting_method,omitempty"`
	AudienceGrades          []string `json:"audience_grades"`
	Sex                     *string  `json:"sex,omitempty"`
	City                    []string `json:"city,omitempty"`

	TargetAudienceExcelFileUUID *string `json:"target_audience_excel_file_uuid,omitempty"`

	// Campaign content
	AdLink  *string `json:"adlink,omitempty"`
	Content *string `json:"content,omitempty"`

	// Scheduling and configuration
	ScheduleAt         *time.Time `json:"schedule_at,omitempty"`
	LineNumber         *string    `json:"line_number,omitempty"`
	MediaUUID          *uuid.UUID `json:"media_uuid,omitempty"`
	PlatformSettingsID *uint      `json:"platform_settings_id,omitempty"`
	Platform           string     `json:"platform"`
	// Short link domain for generated URLs
	ShortLinkDomain *string `json:"short_link_domain,omitempty"`
	// Agency metadata
	Category *string `json:"category,omitempty"`
	Job      *string `json:"job,omitempty"`

	// Budget
	Budget *uint64 `json:"budget,omitempty"`
}

func IsValidCampaignAudienceTargetingMethod(method string) bool {
	switch method {
	case CampaignAudienceTargetingStandard, CampaignAudienceTargetingSmart, CampaignAudienceTargetingExcel:
		return true
	default:
		return false
	}
}

// SmartTargetingAllowedColors returns the delivery-eligible audience colors
// for a campaign platform and SMS provider. Candoo delivery has no color
// restriction; PayamSMS retains the white/pink restriction. An empty result
// means the delivery route has no color restriction.
func SmartTargetingAllowedColors(platform string, provider SMSProvider) []string {
	if strings.EqualFold(strings.TrimSpace(platform), CampaignPlatformSMS) && provider != SMSProviderCandoo {
		return []string{"white", "pink"}
	}
	return nil
}

// EffectiveAudienceTargetingMethod returns the canonical targeting mode while
// preserving campaigns created before AudienceTargetingMethod existed.
//
// An explicit method is authoritative. For legacy specs with no explicit
// method, an attached Excel audience file takes priority over level-based
// standard targeting.
func (s CampaignSpec) EffectiveAudienceTargetingMethod() string {
	method := ""
	if s.AudienceTargetingMethod != nil {
		method = strings.ToLower(strings.TrimSpace(*s.AudienceTargetingMethod))
	}
	if IsValidCampaignAudienceTargetingMethod(method) {
		return method
	}
	if s.TargetAudienceExcelFileUUID != nil && strings.TrimSpace(*s.TargetAudienceExcelFileUUID) != "" {
		return CampaignAudienceTargetingExcel
	}
	if method == CampaignAudienceTargetingExcel {
		return CampaignAudienceTargetingExcel
	}
	return CampaignAudienceTargetingStandard
}

func (s CampaignSpec) UsesSmartTargeting() bool {
	return s.EffectiveAudienceTargetingMethod() == CampaignAudienceTargetingSmart
}

func (s CampaignSpec) UsesExcelTargeting() bool {
	return s.EffectiveAudienceTargetingMethod() == CampaignAudienceTargetingExcel
}

// Value implements the driver.Valuer interface for CampaignSpec
func (s CampaignSpec) Value() (driver.Value, error) {
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface for CampaignSpec
func (s *CampaignSpec) Scan(value any) error {
	if value == nil {
		*s = CampaignSpec{}
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("cannot scan %T into CampaignSpec", value)
	}

	return json.Unmarshal(bytes, s)
}

// Campaign represents an campaign in the database
type Campaign struct {
	ID         uint            `gorm:"primaryKey" json:"id"`
	UUID       uuid.UUID       `gorm:"type:uuid;not null;uniqueIndex:uk_campaigns_uuid;index:idx_campaigns_uuid" json:"uuid"`
	CustomerID uint            `gorm:"not null;index:idx_campaigns_customer_id" json:"customer_id"`
	Hidden     bool            `gorm:"not null;default:false" json:"hidden"`
	Status     CampaignStatus  `gorm:"type:sms_campaign_status;not null;default:'initiated';index:idx_campaigns_status" json:"status"`
	CreatedAt  time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_campaigns_created_at" json:"created_at"`
	UpdatedAt  *time.Time      `gorm:"index:idx_campaigns_updated_at" json:"updated_at,omitempty"`
	Spec       CampaignSpec    `gorm:"type:jsonb;not null" json:"spec"`
	Comment    *string         `gorm:"type:text" json:"comment,omitempty"`
	Statistics json.RawMessage `gorm:"type:jsonb" json:"statistics,omitempty"`

	// Number of targeted audiences
	NumAudience *uint64 `gorm:"type:bigint" json:"num_audience,omitempty"`

	// SampleSizePerTag is required only for Smart Targeting Test campaigns.
	// NumAudience is a derived compatibility snapshot for that combination;
	// pricing and runtime selection derive their authoritative counts from the
	// persisted satisfied-tag intent below.
	SampleSizePerTag *uint64 `gorm:"type:bigint" json:"sample_size_per_tag,omitempty"`
	// SmartTargetingTestSatisfiedTagIDs stores preview results in the user's
	// selection order. It never stores provisionally sampled audience IDs.
	SmartTargetingTestSatisfiedTagIDs pq.Int64Array `gorm:"type:integer[];not null;default:'{}'" json:"smart_targeting_test_satisfied_tag_ids,omitempty"`
	// SmartTargetingTestSamplingInputHash invalidates the preview when the
	// ordered tags, Bundle, sample size, score classes, or platform-specific
	// audience-color eligibility changes.
	SmartTargetingTestSamplingInputHash   *string    `gorm:"type:char(64)" json:"-"`
	SmartTargetingTestSamplingPreviewedAt *time.Time `json:"smart_targeting_test_sampling_previewed_at,omitempty"`
	// The monotonically increasing request generation prevents an older worker
	// from replacing a newer sample. The pointer identifies the sole snapshot
	// eligible for finalization and runtime delivery.
	SmartTargetingTestSamplingGeneration int64  `gorm:"not null;default:0" json:"-"`
	ActiveSmartTargetingTestSelectionID  *int64 `json:"-"`

	BundleID *uint         `gorm:"index:idx_campaigns_bundle_id" json:"bundle_id,omitempty"`
	Phase    CampaignPhase `gorm:"type:campaign_phase;not null;default:'execution'" json:"phase"`

	// Relations
	Customer *Customer `gorm:"foreignKey:CustomerID;references:ID" json:"customer,omitempty"`
	Bundle   *Bundle   `gorm:"foreignKey:BundleID;references:ID" json:"bundle,omitempty"`
}

// TableName returns the table name for the model
func (Campaign) TableName() string {
	return "campaigns"
}

// BeforeCreate is called before creating a new record
func (c *Campaign) BeforeCreate(tx *gorm.DB) error {
	if c.UUID == uuid.Nil {
		c.UUID = uuid.New()
	}
	if c.Status == "" {
		c.Status = CampaignStatusInitiated
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = utils.UTCNow()
	}
	return nil
}

// BeforeUpdate is called before updating a record
func (c *Campaign) BeforeUpdate(tx *gorm.DB) error {
	now := utils.UTCNow()
	c.UpdatedAt = &now
	return nil
}

// IsEditable checks if the campaign can be edited
func (c *Campaign) IsEditable() bool {
	return c.Status == CampaignStatusInitiated ||
		c.Status == CampaignStatusInProgress
}

// IsDeletable checks if the campaign can be deleted
func (c *Campaign) IsDeletable() bool {
	return false
}

// CanTransitionTo checks if the campaign can transition to the given status
func (c *Campaign) CanTransitionTo(newStatus CampaignStatus) bool {
	switch c.Status {
	case CampaignStatusInitiated:
		return newStatus == CampaignStatusInProgress ||
			newStatus == CampaignStatusWaitingForApproval ||
			newStatus == CampaignStatusRejected
	case CampaignStatusInProgress:
		return newStatus == CampaignStatusWaitingForApproval ||
			newStatus == CampaignStatusRejected
	case CampaignStatusWaitingForApproval:
		return newStatus == CampaignStatusApproved ||
			newStatus == CampaignStatusRejected ||
			newStatus == CampaignStatusCancelled
	default:
		return false
	}
}

// CampaignFilter represents filter criteria for campaigns
type CampaignFilter struct {
	ID                 *uint           `json:"id,omitempty"`
	UUID               *uuid.UUID      `json:"uuid,omitempty"`
	CustomerID         *uint           `json:"customer_id,omitempty"`
	Hidden             *bool           `json:"hidden,omitempty"`
	Status             *CampaignStatus `json:"status,omitempty"`
	CampaignTitle      *string         `json:"campaign_title,omitempty"`
	BundleTitle        *string         `json:"bundle_title,omitempty"`
	CustomerName       *string         `json:"customer_name,omitempty"`
	Level1             *string         `json:"level1,omitempty"`
	Sex                *string         `json:"sex,omitempty"`
	City               *string         `json:"city,omitempty"`
	LineNumber         *string         `json:"line_number,omitempty"`
	MediaUUID          *uuid.UUID      `json:"media_uuid,omitempty"`
	PlatformSettingsID *uint           `json:"platform_settings_id,omitempty"`
	Platform           *string         `json:"platform,omitempty"`
	CreatedAfter       *time.Time      `json:"created_after,omitempty"`
	CreatedBefore      *time.Time      `json:"created_before,omitempty"`
	UpdatedAfter       *time.Time      `json:"updated_after,omitempty"`
	UpdatedBefore      *time.Time      `json:"updated_before,omitempty"`
	ScheduleAfter      *time.Time      `json:"schedule_after,omitempty"`
	ScheduleBefore     *time.Time      `json:"schedule_before,omitempty"`
	MinBudget          *uint64         `json:"min_budget,omitempty"`
	MaxBudget          *uint64         `json:"max_budget,omitempty"`
	BundleID           *uint           `json:"bundle_id,omitempty"`
	Phase              *CampaignPhase  `json:"phase,omitempty"`
}

// GetStatusDisplayName returns a human-readable status name
func (c *Campaign) GetStatusDisplayName() string {
	switch c.Status {
	case CampaignStatusInitiated:
		return "Initiated"
	case CampaignStatusInProgress:
		return "In Progress"
	case CampaignStatusWaitingForApproval:
		return "Waiting for Approval"
	case CampaignStatusApproved:
		return "Approved"
	case CampaignStatusExpired:
		return "Expired"
	case CampaignStatusRejected:
		return "Rejected"
	case CampaignStatusCancelled:
		return "Cancelled"
	case CampaignStatusCancelledByAdmin:
		return "Cancelled by Admin"
	case CampaignStatusRunning:
		return "Running"
	case CampaignStatusExecuted:
		return "Executed"
	default:
		return "Unknown"
	}
}

// GetStatusColor returns a color code for the status (for UI purposes)
func (c *Campaign) GetStatusColor() string {
	switch c.Status {
	case CampaignStatusInitiated:
		return "#6c757d" // gray
	case CampaignStatusInProgress:
		return "#007bff" // blue
	case CampaignStatusWaitingForApproval:
		return "#ffc107" // yellow
	case CampaignStatusApproved:
		return "#28a745" // green
	case CampaignStatusExpired:
		return "#fd7e14" // orange
	case CampaignStatusRejected:
		return "#dc3545" // red
	case CampaignStatusCancelled, CampaignStatusCancelledByAdmin:
		return "#6c757d" // gray
	case CampaignStatusRunning:
		return "#17a2b8" // cyan
	case CampaignStatusExecuted:
		return "#343a40" // dark gray
	default:
		return "#6c757d" // gray
	}
}
