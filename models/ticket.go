package models

import (
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// Ticket represents a support or system ticket submitted by users or the system
// Table: tickets
// Indices: uuid, correlation_id, created_at
// Files is a list of file addresses/URLs associated with the ticket
// Timestamps default to UTC at DB level
// Title limited to 255 characters
// Content stored as TEXT
// Files stored as TEXT[]
type Ticket struct {
	ID             uint           `gorm:"primaryKey;autoIncrement"                                 json:"id"`
	UUID           uuid.UUID      `gorm:"type:uuid;uniqueIndex;not null;default:gen_random_uuid()" json:"uuid"`
	CorrelationID  uuid.UUID      `gorm:"type:uuid;index;not null"                                 json:"correlation_id"`
	CustomerID     uint           `gorm:"not null;index"                                           json:"customer_id"`
	Title          string         `gorm:"type:varchar(255);not null"                               json:"title"`
	Content        string         `gorm:"type:text;not null"                                       json:"content"`
	Files          pq.StringArray `gorm:"type:text[];not null;default:'{}'"                        json:"files"`
	RepliedByAdmin *bool          `gorm:"default:false;index"                                      json:"replied_by_admin,omitempty"`

	CreatedAt time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`

	// Relations
	Customer *Customer `gorm:"foreignKey:CustomerID;references:ID;constraint:OnDelete:CASCADE" json:"customer,omitempty"`
}

func (Ticket) TableName() string { return "tickets" }

// BeforeCreate ensures UUID and CorrelationID are set
func (t *Ticket) BeforeCreate(tx *gorm.DB) error {
	if t.UUID == uuid.Nil {
		t.UUID = uuid.New()
	}
	if t.CorrelationID == uuid.Nil {
		t.CorrelationID = uuid.New()
	}
	// Normalize timestamps if zero
	if t.CreatedAt.IsZero() {
		t.CreatedAt = utils.UTCNow()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = utils.UTCNow()
	}
	return nil
}

// TicketFilter represents filter criteria for ticket queries
type TicketFilter struct {
	ID             *uint      `json:"id,omitempty"`
	UUID           *uuid.UUID `json:"uuid,omitempty"`
	CorrelationID  *uuid.UUID `json:"correlation_id,omitempty"`
	CustomerID     *uint      `json:"customer_id,omitempty"`
	Title          *string    `json:"title,omitempty"`
	CreatedAfter   *time.Time `json:"created_after,omitempty"`
	CreatedBefore  *time.Time `json:"created_before,omitempty"`
	RepliedByAdmin *bool      `json:"replied_by_admin,omitempty"`
}
