package models

import "time"

// PagePrice holds per-platform page price records (append-only).
type PagePrice struct {
	ID               uint      `gorm:"primaryKey"                                                                   json:"id"`
	Platform         string    `gorm:"type:varchar(50);not null;index:idx_page_prices_platform_created_at"          json:"platform"`
	Price            uint64    `gorm:"not null"                                                                     json:"price"`
	CreatedByAdminID *uint     `gorm:"index"                                                                        json:"created_by_admin_id,omitempty"`
	CreatedAt        time.Time `gorm:"not null;default:CURRENT_TIMESTAMP;index:idx_page_prices_platform_created_at" json:"created_at"`
}

func (PagePrice) TableName() string {
	return "page_prices"
}
