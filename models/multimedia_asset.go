package models

import (
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MultimediaAsset represents an uploaded image or video stored on disk.
type MultimediaAsset struct {
	ID               uint      `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID             uuid.UUID `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CustomerID       uint      `gorm:"not null;index"                                           json:"customer_id"`
	OriginalFilename string    `gorm:"type:varchar(255);not null"                               json:"original_filename"`
	StoredPath       string    `gorm:"type:text;not null"                                       json:"stored_path"`
	SizeBytes        int64     `gorm:"type:bigint;not null"                                     json:"size_bytes"`
	MimeType         string    `gorm:"type:varchar(100);not null"                               json:"mime_type"`
	MediaType        string    `gorm:"type:varchar(20);not null;index"                          json:"media_type"`
	Extension        string    `gorm:"type:varchar(20);not null"                                json:"extension"`
	CreatedAt        time.Time `gorm:"not null;default:CURRENT_TIMESTAMP;index"                 json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"                       json:"updated_at"`

	Customer *Customer `gorm:"foreignKey:CustomerID;references:ID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
}

func (MultimediaAsset) TableName() string { return "multimedia_assets" }

// BeforeCreate ensures UUID and timestamps are set.
func (m *MultimediaAsset) BeforeCreate(tx *gorm.DB) error {
	if m.UUID == uuid.Nil {
		m.UUID = uuid.New()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = utils.UTCNow()
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = utils.UTCNow()
	}
	return nil
}

// MultimediaAssetFilter represents filter criteria for multimedia asset queries.
type MultimediaAssetFilter struct {
	ID            *uint      `json:"id,omitempty"`
	UUID          *uuid.UUID `json:"uuid,omitempty"`
	CustomerID    *uint      `json:"customer_id,omitempty"`
	MediaType     *string    `json:"media_type,omitempty"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"`
	CreatedBefore *time.Time `json:"created_before,omitempty"`
}
