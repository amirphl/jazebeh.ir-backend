// Package dto contains Data Transfer Objects for API request and response structures
package dto

// AdminCreateLineNumberRequest represents the payload to create a new line number
// Admin-only endpoint
type AdminCreateLineNumberRequest struct {
	Name        *string `json:"name,omitempty" validate:"omitempty,max=255"`
	LineNumber  string  `json:"line_number" validate:"required,min=3,max=50"`
	Provider    string  `json:"provider,omitempty" validate:"omitempty,oneof=payamsms candoo"`
	PriceFactor float64 `json:"price_factor" validate:"required,gt=0"`
	Priority    *int    `json:"priority,omitempty" validate:"omitempty"`
	IsActive    *bool   `json:"is_active,omitempty" validate:"omitempty"`
}

// AdminLineNumberDTO represents a line number for responses
type AdminLineNumberDTO struct {
	ID          uint    `json:"id"`
	UUID        string  `json:"uuid"`
	Name        *string `json:"name,omitempty"`
	LineNumber  string  `json:"line_number"`
	Provider    string  `json:"provider"`
	PriceFactor float64 `json:"price_factor"`
	Priority    *int    `json:"priority,omitempty"`
	IsActive    *bool   `json:"is_active"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// AdminUpdateLineNumberItem represents one update operation for a line number
// All IDs must exist; price_factor must be > 0; other fields optional
type AdminUpdateLineNumberItem struct {
	ID       uint    `json:"id" validate:"required"`
	Provider *string `json:"provider,omitempty" validate:"omitempty,oneof=payamsms candoo"`
	Priority *int    `json:"priority,omitempty" validate:"omitempty"`
	IsActive *bool   `json:"is_active,omitempty" validate:"omitempty"`
}

type AdminUpdateLineNumbersRequest struct {
	Items []AdminUpdateLineNumberItem `json:"items" validate:"required,min=1,dive"`
}

// AdminUpdateLineNumberPriceFactorRequest updates the price factor of an existing line number.
type AdminUpdateLineNumberPriceFactorRequest struct {
	LineNumber  string  `json:"line_number" validate:"required,min=3,max=50"`
	PriceFactor float64 `json:"price_factor" validate:"required,gt=0"`
}

// AdminLineNumberReportItem is the report row for admin listing
// Values should be computed from message/campaign delivery data
// All numeric fields represent totals for the time range (future extension)
type AdminLineNumberReportItem struct {
	LineNumber            string `json:"line_number"`
	TotalSent             int64  `json:"total_sent"`
	TotalPartsSent        int64  `json:"total_parts_sent"`
	TotalArrivedPartsSent int64  `json:"total_arrived_parts_sent"`
	TotalNonArrivedParts  int64  `json:"total_non_arrived_parts_sent"`
	TotalIncome           int64  `json:"total_income"`
	TotalCost             int64  `json:"total_cost"`
}

// ActiveLineNumberItem is returned to customers
// Only non-sensitive fields are exposed
type ActiveLineNumberItem struct {
	LineNumber  string  `json:"line_number"`
	PriceFactor float64 `json:"price_factor"`
}

// ListActiveLineNumbersResponse wraps the active line numbers list for customers
type ListActiveLineNumbersResponse struct {
	Message string                 `json:"message"`
	Items   []ActiveLineNumberItem `json:"items"`
}
