package dto

import "time"

type AgencyCustomerReportFilter struct {
	StartDate *string `json:"start_date,omitempty" validate:"omitempty"`
	EndDate   *string `json:"end_date,omitempty"   validate:"omitempty"`
	Name      *string `json:"name,omitempty"       validate:"omitempty,max=255"`
}

type AgencyCustomerReportRequest struct {
	AgencyID uint                        `json:"-"`
	OrderBy  string                      `json:"orderby"` // e.g., name_asc, name_desc, sent_desc, share_desc
	Filter   *AgencyCustomerReportFilter `json:"filter,omitempty" validate:"omitempty"`
}

type AgencyCustomerReportItem struct {
	CustomerID              uint   `json:"customer_id"`
	RepresentativeFirstName string `json:"representative_first_name"`
	RepresentativeLastName  string `json:"representative_last_name"`
	CompanyName             string `json:"company_name"`
	TotalSent               uint64 `json:"total_sent"`
	TotalAgencyShareWithTax uint64 `json:"total_agency_share_with_tax"`
}

type AgencyCustomerReportResponse struct {
	Message                    string                     `json:"message"`
	Items                      []AgencyCustomerReportItem `json:"items"`
	SumTotalAgencyShareWithTax uint64                     `json:"sum_total_agency_share_with_tax"`
	SumTotalSent               uint64                     `json:"sum_total_sent"`
}

type ListAgencyActiveDiscountsFilter struct {
	Name *string `json:"name,omitempty" validate:"omitempty,max=255"`
}

type ListAgencyActiveDiscountsRequest struct {
	AgencyID uint                             `json:"-"`
	Filter   *ListAgencyActiveDiscountsFilter `json:"filter,omitempty" validate:"omitempty"`
}

type AgencyActiveDiscountItem struct {
	CustomerID              uint      `json:"customer_id"`
	RepresentativeFirstName string    `json:"representative_first_name"`
	RepresentativeLastName  string    `json:"representative_last_name"`
	CompanyName             *string   `json:"company_name"`
	DiscountRate            float64   `json:"discount_rate"`
	CreatedAt               time.Time `json:"created_at"`
}

type ListAgencyActiveDiscountsResponse struct {
	Message string                     `json:"message"`
	Items   []AgencyActiveDiscountItem `json:"items"`
}

type ListAgencyCustomerDiscountsRequest struct {
	AgencyID   uint `json:"-"`
	CustomerID uint `json:"-"`
}

type AgencyCustomerDiscountItem struct {
	DiscountRate       float64    `json:"discount_rate"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"  validate:"omitempty"`
	TotalSent          uint64     `json:"total_sent"`
	AgencyShareWithTax uint64     `json:"agency_share_with_tax"`
}

type ListAgencyCustomerDiscountsResponse struct {
	Message string                       `json:"message"`
	Items   []AgencyCustomerDiscountItem `json:"items"`
}

type CreateAgencyDiscountRequest struct {
	AgencyID     uint    `json:"-"`
	CustomerID   uint    `json:"customer_id"   validate:"required,min=1"`
	Name         string  `json:"name"          validate:"required,min=1,max=255"`
	DiscountRate float64 `json:"discount_rate" validate:"gte=0,lte=0.5"`
}

type CreateAgencyDiscountResponse struct {
	Message      string  `json:"message"`
	DiscountRate float64 `json:"discount_rate"`
}

type ListAgencyCustomersRequest struct {
	AgencyID uint `json:"-"`
}

type AgencyCustomerItem struct {
	CustomerID              uint      `json:"customer_id"`
	RepresentativeFirstName string    `json:"representative_first_name"`
	RepresentativeLastName  string    `json:"representative_last_name"`
	CompanyName             *string   `json:"company_name,omitempty"    validate:"omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	IsActive                *bool     `json:"is_active"`
}

type ListAgencyCustomersResponse struct {
	Message string               `json:"message"`
	Items   []AgencyCustomerItem `json:"items"`
}
