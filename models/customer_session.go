// Package models contains domain entities and business models for the authentication system
package models

import (
	"encoding/json"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

type CustomerSession struct {
	ID             uint            `gorm:"primaryKey"                                                                      json:"id"`
	CorrelationID  uuid.UUID       `gorm:"type:uuid;not null;index:idx_sessions_correlation_id"                            json:"correlation_id"` // Groups related session records
	CustomerID     uint            `gorm:"not null;index:idx_sessions_customer_id"                                         json:"customer_id"`
	Customer       Customer        `gorm:"foreignKey:CustomerID;references:ID"                                             json:"customer,omitempty"`
	SessionToken   string          `gorm:"size:255;not null;uniqueIndex:idx_sessions_session_token"                        json:"-"` // Never serialize token
	RefreshToken   *string         `gorm:"size:255;uniqueIndex:idx_sessions_refresh_token"                                 json:"-"` // Never serialize refresh token
	DeviceInfo     json.RawMessage `gorm:"type:jsonb"                                                                      json:"device_info,omitempty"`
	IPAddress      *string         `gorm:"type:inet;index:idx_sessions_ip_address"                                         json:"ip_address,omitempty"`
	UserAgent      *string         `gorm:"type:text"                                                                       json:"user_agent,omitempty"`
	IsActive       *bool           `gorm:"default:true;index:idx_sessions_is_active"                                       json:"is_active"`
	CreatedAt      time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                  json:"created_at"`
	UpdatedAt      time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                                  json:"updated_at"`
	LastAccessedAt time.Time       `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_sessions_last_accessed" json:"last_accessed_at"`
	ExpiresAt      time.Time       `gorm:"not null;index:idx_sessions_expires_at"                                          json:"expires_at"`
}

func (CustomerSession) TableName() string {
	return "customer_sessions"
}

// CustomerSessionFilter represents filter criteria for session queries
type CustomerSessionFilter struct {
	ID             *uint
	CorrelationID  *uuid.UUID
	CustomerID     *uint
	IsActive       *bool
	IPAddress      *string
	CreatedAfter   *time.Time
	CreatedBefore  *time.Time
	ExpiresAfter   *time.Time
	ExpiresBefore  *time.Time
	AccessedAfter  *time.Time
	AccessedBefore *time.Time
	IsExpired      *bool // Helper to filter expired sessions
}

func (s *CustomerSession) IsExpired() bool {
	return utils.UTCNow().After(s.ExpiresAt)
}

func (s *CustomerSession) IsValid() bool {
	return utils.IsTrue(s.IsActive) && !s.IsExpired()
}

// DeviceInfo represents the structure for device information
type DeviceInfo struct {
	Platform   string `json:"platform,omitempty"`
	Browser    string `json:"browser,omitempty"`
	Version    string `json:"version,omitempty"`
	OS         string `json:"os,omitempty"`
	DeviceType string `json:"device_type,omitempty"`
	IsMobile   bool   `json:"is_mobile,omitempty"`
}
