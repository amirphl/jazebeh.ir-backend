package dto

import (
	"time"

	"github.com/google/uuid"
)

// AdminCustomersSharesRequest holds the bounded period and page for the report.
type AdminCustomersSharesRequest struct {
	StartDate *string `json:"start_date" validate:"required"`
	EndDate   *string `json:"end_date" validate:"required"`
	Page      int     `json:"page" validate:"min=1"`
	PageSize  int     `json:"page_size" validate:"min=1,max=100"`
}

// AdminCustomersSharesItem represents a single row in the report
type AdminCustomersSharesItem struct {
	CustomerID         uint    `json:"customer_id"`
	FirstName          string  `json:"first_name"`
	LastName           string  `json:"last_name"`
	FullName           string  `json:"full_name"`
	CompanyName        string  `json:"company_name"`
	ReferrerAgencyName string  `json:"referrer_agency_name"`
	AccountTypeName    string  `json:"account_type_name"`
	IsActive           *bool   `json:"is_active"`
	AgencyShareWithTax uint64  `json:"agency_share_with_tax"`
	SystemShare        uint64  `json:"system_share"`
	TaxShare           uint64  `json:"tax_share"`
	TotalSent          uint64  `json:"total_sent"`
	ClickRate          float64 `json:"click_rate"`
}

// AdminCustomersSharesResponse is the API response for the report
type AdminCustomersSharesResponse struct {
	Message               string                     `json:"message"`
	Items                 []AdminCustomersSharesItem `json:"items"`
	SumAgencyShareWithTax uint64                     `json:"sum_agency_share_with_tax"`
	SumSystemShare        uint64                     `json:"sum_system_share"`
	SumTaxShare           uint64                     `json:"sum_tax_share"`
	SumTotalSent          uint64                     `json:"sum_total_sent"`
	Page                  int                        `json:"page"`
	PageSize              int                        `json:"page_size"`
	HasMore               bool                       `json:"has_more"`
}

// AdminCustomerDetailDTO contains full customer info for admin
type AdminCustomerDetailDTO struct {
	ID                      uint       `json:"id"`
	UUID                    string     `json:"uuid"`
	AgencyRefererCode       string     `json:"agency_referer_code"`
	AccountTypeID           uint       `json:"account_type_id"`
	AccountTypeName         string     `json:"account_type_name"`
	CompanyName             *string    `json:"company_name,omitempty"`
	NationalID              *string    `json:"national_id,omitempty"`
	CompanyPhone            *string    `json:"company_phone,omitempty"`
	CompanyAddress          *string    `json:"company_address,omitempty"`
	PostalCode              *string    `json:"postal_code,omitempty"`
	RepresentativeFirstName string     `json:"representative_first_name"`
	RepresentativeLastName  string     `json:"representative_last_name"`
	RepresentativeMobile    string     `json:"representative_mobile"`
	Email                   string     `json:"email"`
	ShebaNumber             *string    `json:"sheba_number,omitempty"`
	ReferrerAgencyID        *uint      `json:"referrer_agency_id,omitempty"`
	IsEmailVerified         *bool      `json:"is_email_verified,omitempty"`
	IsMobileVerified        *bool      `json:"is_mobile_verified,omitempty"`
	IsActive                *bool      `json:"is_active,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at,omitempty"`
	EmailVerifiedAt         *time.Time `json:"email_verified_at,omitempty"`
	MobileVerifiedAt        *time.Time `json:"mobile_verified_at,omitempty"`
	LastLoginAt             *time.Time `json:"last_login_at,omitempty"`
}

// AdminCustomerCampaignItem summarizes a campaign for admin list
type AdminCustomerCampaignItem struct {
	CampaignID                  uint           `json:"campaign_id"`
	ID                          uint           `json:"id"`
	UUID                        string         `json:"uuid"`
	Status                      string         `json:"status"`
	CreatedAt                   time.Time      `json:"created_at"`
	UpdatedAt                   *time.Time     `json:"updated_at,omitempty"`
	Title                       *string        `json:"title,omitempty"`
	Level1                      *string        `json:"level1,omitempty"`
	Level2s                     []string       `json:"level2s,omitempty"`
	Level3s                     []string       `json:"level3s,omitempty"`
	Tags                        []string       `json:"tags,omitempty"`
	TargetingMethod             string         `json:"audience_targeting_method"`
	Sex                         *string        `json:"sex,omitempty"`
	City                        []string       `json:"city,omitempty"`
	AdLink                      *string        `json:"adlink,omitempty"`
	Content                     *string        `json:"content,omitempty"`
	ShortLinkDomain             *string        `json:"short_link_domain,omitempty"`
	Category                    *string        `json:"job_category,omitempty"`
	Job                         *string        `json:"job,omitempty"`
	ScheduleAt                  *time.Time     `json:"scheduleat,omitempty"`
	LineNumber                  *string        `json:"line_number,omitempty"`
	MediaUUID                   *uuid.UUID     `json:"media_uuid,omitempty"`
	PlatformSettingsID          *uint          `json:"platform_settings_id,omitempty"`
	Platform                    string         `json:"platform"`
	Budget                      *uint64        `json:"budget,omitempty"`
	Comment                     *string        `json:"comment,omitempty"`
	SegmentPriceFactor          float64        `json:"segment_price_factor,omitempty"`
	LineNumberPriceFactor       float64        `json:"line_number_price_factor,omitempty"`
	Statistics                  map[string]any `json:"statistics,omitempty"`
	TotalClicks                 *int64         `json:"total_clicks,omitempty"`
	ClickRate                   float64        `json:"click_rate"`
	NumAudience                 *uint64        `json:"num_audience,omitempty"`
	SampleSizePerTag            *uint64        `json:"sample_size_per_tag,omitempty"`
	CustomerFullName            *string        `json:"customer_full_name,omitempty"`
	AgencyFullName              *string        `json:"agency_full_name,omitempty"`
	TargetAudienceExcelFileUUID *string        `json:"target_audience_excel_file_uuid,omitempty"`
	TotalSent                   uint64         `json:"total_sent"`
	TotalDelivered              uint64         `json:"total_delivered"`
}

// AdminCustomerWithCampaignsResponse response payload
type AdminCustomerWithCampaignsResponse struct {
	Message   string                      `json:"message"`
	Customer  AdminCustomerDetailDTO      `json:"customer"`
	Campaigns []AdminCustomerCampaignItem `json:"campaigns"`
}

// AdminCustomerDiscountHistoryItem represents a discount used by customer across agencies
type AdminCustomerDiscountHistoryItem struct {
	DiscountRate       float64    `json:"discount_rate"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty" validate:"omitempty"`
	TotalSent          uint64     `json:"total_sent"`
	AgencyShareWithTax uint64     `json:"agency_share_with_tax"`
}

// AdminCustomerDiscountHistoryResponse is the response for customer discounts history
type AdminCustomerDiscountHistoryResponse struct {
	Message string                             `json:"message"`
	Items   []AdminCustomerDiscountHistoryItem `json:"items"`
}

// AdminSetCustomerActiveStatusRequest is the request to toggle customer activity
type AdminSetCustomerActiveStatusRequest struct {
	CustomerID uint `json:"customer_id" validate:"required,min=1"`
	IsActive   bool `json:"is_active"`
}

// AdminSetCustomerActiveStatusResponse reports the resulting status
type AdminSetCustomerActiveStatusResponse struct {
	Message  string `json:"message"`
	IsActive bool   `json:"is_active"`
}

// AdminListCustomersResponse is the response for listing customers by admin.
type AdminListCustomersResponse struct {
	Message string                   `json:"message"`
	Items   []AdminCustomerDetailDTO `json:"items"`
	Total   uint64                   `json:"total"`
}
