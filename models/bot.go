package models

import (
	"time"

	"github.com/google/uuid"
)

// Bot represents a system bot that can authenticate similarly to an admin
// Bots are intended for automated actions and integrations
// and are authenticated using a username and password hash
// The table name is `bots`
type Bot struct {
	ID           uint      `gorm:"primaryKey"                                                             json:"id"`
	UUID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:uk_bots_uuid;index:idx_bots_uuid"        json:"uuid"`
	Username     string    `gorm:"size:255;not null;uniqueIndex:uk_bots_username;index:idx_bots_username" json:"username"`
	PasswordHash string    `gorm:"size:255;not null"                                                      json:"-"`

	IsActive    *bool      `gorm:"default:true;index:idx_bots_is_active"                                    json:"is_active"`
	CreatedAt   time.Time  `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC');index:idx_bots_created_at" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"default:(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"                           json:"updated_at"`
	LastLoginAt *time.Time `gorm:"index:idx_bots_last_login_at"                                             json:"last_login_at,omitempty"`
}

func (Bot) TableName() string {
	return "bots"
}

// BotFilter represents filter criteria for bot queries
type BotFilter struct {
	ID              *uint
	UUID            *uuid.UUID
	Username        *string
	IsActive        *bool
	CreatedAfter    *time.Time
	CreatedBefore   *time.Time
	LastLoginAfter  *time.Time
	LastLoginBefore *time.Time
}
