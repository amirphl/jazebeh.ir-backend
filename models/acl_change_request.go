package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type ACLChangeRequestStatus string

const (
	ACLChangeRequestStatusPending  ACLChangeRequestStatus = "pending"
	ACLChangeRequestStatusApproved ACLChangeRequestStatus = "approved"
	ACLChangeRequestStatusRejected ACLChangeRequestStatus = "rejected"
)

// ACLChangeRequest captures maker-checker changes to admin roles/overrides.
type ACLChangeRequest struct {
	ID                 uint                   `gorm:"primaryKey"                                        json:"id"`
	UUID               uuid.UUID              `gorm:"type:uuid;not null;uniqueIndex"                    json:"uuid"`
	TargetAdminID      uint                   `gorm:"not null;index"                                    json:"target_admin_id"`
	RequestedByAdminID uint                   `gorm:"not null;index"                                    json:"requested_by_admin_id"`
	ApprovedByAdminID  *uint                  `gorm:"index"                                             json:"approved_by_admin_id,omitempty"`
	Status             ACLChangeRequestStatus `gorm:"type:varchar(20);not null;default:'pending';index" json:"status"`
	Reason             string                 `gorm:"type:text"                                         json:"reason,omitempty"`
	BeforeRoles        pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"before_roles"`
	AfterRoles         pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"after_roles"`
	BeforeAllowed      pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"before_allowed"`
	AfterAllowed       pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"after_allowed"`
	BeforeDenied       pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"before_denied"`
	AfterDenied        pq.StringArray         `gorm:"type:text[];not null;default:'{}'"                 json:"after_denied"`
	CreatedAt          time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP"                json:"created_at"`
	UpdatedAt          time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP"                json:"updated_at"`
	ExpiresAt          *time.Time             `                                                         json:"expires_at,omitempty"`
	AppliedAt          *time.Time             `                                                         json:"applied_at,omitempty"`
}

func (ACLChangeRequest) TableName() string {
	return "acl_change_requests"
}

// ACLChangeRequestFilter represents filtering options.
type ACLChangeRequestFilter struct {
	ID                 *uint
	UUID               *uuid.UUID
	TargetAdminID      *uint
	RequestedByAdminID *uint
	Status             *ACLChangeRequestStatus
	ExpiresBefore      *time.Time
}
