package dto

import "time"

// CreateTicketRequest carries data to create a new ticket
// AttachedFile* fields are optional and used for validation (type/size) and storage address
// Only a single file is supported for now
// FileType should be one of: jpg, png, pdf, docx, xlsx, zip
// FileSizeBytes must be <= 10MB if provided
type CreateTicketRequest struct {
	CustomerID       uint    `json:"customer_id"`
	Title            string  `json:"title"`
	Content          string  `json:"content"`
	AttachedFileURL  *string `json:"attached_file_url,omitempty"  validate:"omitempty"`
	AttachedFileName *string `json:"attached_file_name,omitempty" validate:"omitempty"`
	AttachedFileSize *int64  `json:"attached_file_size,omitempty" validate:"omitempty"`
	// Internal: populated by handler when user uploads a file (not exposed in API)
	SavedFilePath *string `json:"-"`
}

// CreateTicketResponse returns created ticket identifiers and timestamps
type CreateTicketResponse struct {
	Message       string `json:"message"`
	ID            uint   `json:"id"`
	UUID          string `json:"uuid"`
	CorrelationID string `json:"correlation_id"`
	CreatedAt     string `json:"created_at"`
}

// TicketItem represents a ticket row in listings
type TicketItem struct {
	ID             uint               `json:"id"`
	Title          string             `json:"title"`
	Content        string             `json:"content"`
	CreatedAt      string             `json:"created_at"`
	RepliedByAdmin *bool              `json:"replied_by_admin"`
	Attachments    []TicketAttachment `json:"attachments,omitempty"`
	// Admin-only fields (populated in admin listings only)
	CustomerFirstName *string `json:"customer_first_name,omitempty"`
	CustomerLastName  *string `json:"customer_last_name,omitempty"`
	CompanyName       *string `json:"company_name,omitempty"`
	PhoneNumber       *string `json:"phone_number,omitempty"`
	AgencyName        *string `json:"agency_name,omitempty"`
}

// TicketAttachment represents a downloadable attachment for a ticket.
type TicketAttachment struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

// TicketGroup groups tickets by correlation_id
type TicketGroup struct {
	CorrelationID string       `json:"correlation_id"`
	Items         []TicketItem `json:"items"`
}

// ListTicketsRequest filters for listing tickets
// Title is matched exactly; StartDate/EndDate are inclusive bounds
// Groups are built per correlation_id
type ListTicketsRequest struct {
	CustomerID uint       `json:"customer_id"`
	Title      *string    `json:"title,omitempty"`
	StartDate  *time.Time `json:"start_date,omitempty"`
	EndDate    *time.Time `json:"end_date,omitempty"`
	Page       uint       `json:"page,omitempty"`
	PageSize   uint       `json:"page_size,omitempty"`
}

// ListTicketsResponse returns grouped ticket items
type ListTicketsResponse struct {
	Message string        `json:"message"`
	Groups  []TicketGroup `json:"groups"`
}

// CreateResponseTicketRequest carries data for a customer to reply to a ticket
// It creates a new ticket with the same correlation_id as the original
// AttachedFile* optional; same validation rules
type CreateResponseTicketRequest struct {
	CustomerID       uint    `json:"customer_id"`
	TicketID         uint    `json:"ticket_id"`
	Content          string  `json:"content"`
	AttachedFileURL  *string `json:"attached_file_url,omitempty"  validate:"omitempty"`
	AttachedFileName *string `json:"attached_file_name,omitempty" validate:"omitempty"`
	AttachedFileSize *int64  `json:"attached_file_size,omitempty" validate:"omitempty"`
	// Internal: populated by handler when customer uploads a file
	SavedFilePath *string `json:"-"`
}

// CreateResponseTicketResponse returns created response ticket identifiers and timestamps
type CreateResponseTicketResponse struct {
	Message       string `json:"message"`
	ID            uint   `json:"id"`
	UUID          string `json:"uuid"`
	CorrelationID string `json:"correlation_id"`
	CreatedAt     string `json:"created_at"`
}

// AdminCreateResponseTicketRequest carries data to create an admin reply to a ticket
// It creates a new ticket with the same correlation_id as the original
// AttachedFile* optional; same validation rules
type AdminCreateResponseTicketRequest struct {
	TicketID         uint    `json:"ticket_id"`
	Content          string  `json:"content"`
	AttachedFileURL  *string `json:"attached_file_url,omitempty"  validate:"omitempty"`
	AttachedFileName *string `json:"attached_file_name,omitempty" validate:"omitempty"`
	AttachedFileSize *int64  `json:"attached_file_size,omitempty" validate:"omitempty"`
	// Internal: populated by handler when admin uploads a file
	SavedFilePath *string `json:"-"`
}

// AdminCreateResponseTicketResponse contains the created admin reply ticket
type AdminCreateResponseTicketResponse struct {
	Message       string `json:"message"`
	ID            uint   `json:"id"`
	UUID          string `json:"uuid"`
	CorrelationID string `json:"correlation_id"`
	CreatedAt     string `json:"created_at"`
}

// AdminListTicketsRequest filters for admin listing tickets by optional customer, title, date range, and replied flag
type AdminListTicketsRequest struct {
	CustomerID     *uint      `json:"customer_id,omitempty"`
	Title          *string    `json:"title,omitempty"`
	StartDate      *time.Time `json:"start_date,omitempty"`
	EndDate        *time.Time `json:"end_date,omitempty"`
	RepliedByAdmin *bool      `json:"replied_by_admin,omitempty"`
	Page           uint       `json:"page,omitempty"`
	PageSize       uint       `json:"page_size,omitempty"`
}

// AdminListTicketsResponse returns grouped list of tickets for admin
type AdminListTicketsResponse struct {
	Message string        `json:"message"`
	Groups  []TicketGroup `json:"groups"`
}
