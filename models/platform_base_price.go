package models

import (
	"time"

	"gorm.io/gorm"
)

// PlatformBasePrice holds per-platform base price records (append-only).
type PlatformBasePrice struct {
	ID        uint           `gorm:"primaryKey"                                                             json:"id"`
	Platform  string         `gorm:"type:varchar(50);not null;index:uk_platform_base_price_platform,unique" json:"platform"`
	Price     uint64         `gorm:"not null"                                                               json:"price"`
	CreatedAt time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP"                                     json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                                                                  json:"deleted_at,omitempty"`
}

func (PlatformBasePrice) TableName() string {
	return "platform_base_prices"
}
