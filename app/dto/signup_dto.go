// Package dto contains Data Transfer Objects for API request and response structures
package dto

// SignupRequest represents the signup form data
type SignupRequest struct {
	// Account type selection
	AccountType string `json:"account_type" validate:"required,oneof=individual independent_company marketing_agency"`

	// Company fields (required for independent_company and marketing_agency)
	CompanyName    *string `json:"company_name,omitempty"    validate:"omitempty,max=60"`
	NationalID     *string `json:"national_id,omitempty"     validate:"omitempty,min=10,max=20,numeric"`
	CompanyPhone   *string `json:"company_phone,omitempty"   validate:"omitempty,min=10"`
	CompanyAddress *string `json:"company_address,omitempty" validate:"omitempty,max=255"`
	PostalCode     *string `json:"postal_code,omitempty"     validate:"omitempty,min=10,max=20,numeric"`
	ShebaNumber    *string `json:"sheba_number,omitempty"    validate:"omitempty,max=255"`

	// Representative/Individual fields (required for all types)
	RepresentativeFirstName string `json:"representative_first_name" validate:"required,max=255"`
	RepresentativeLastName  string `json:"representative_last_name"  validate:"required,max=255"`
	RepresentativeMobile    string `json:"representative_mobile"     validate:"required,mobile_format"`

	// Additional fields (mandatory for non-agency customers; enforced in business logic)
	Job      *string `json:"job,omitempty"      validate:"omitempty,max=255"`
	Category *string `json:"category,omitempty" validate:"omitempty,max=255"`

	// Common fields (required for all types)
	Email           string `json:"email"            validate:"required,email,max=255"`
	Password        string `json:"password"         validate:"required,min=8,password_strength"`
	ConfirmPassword string `json:"confirm_password" validate:"required,eqfield=Password"`

	// Optional agency referral
	ReferrerAgencyCode *string `json:"referrer_agency_code,omitempty" validate:"omitempty,max=255"`
}

// SignupResponse represents the response after successful signup initiation
type SignupResponse struct {
	Message    string `json:"message"`
	CustomerID uint   `json:"customer_id"`
	OTPSent    bool   `json:"otp_sent"`
	OTPTarget  string `json:"otp_target"` // Mobile number (masked for security)
}

// OTPVerificationRequest represents the OTP verification request
type OTPVerificationRequest struct {
	CustomerID uint   `json:"customer_id" validate:"required"`
	OTPCode    string `json:"otp_code"    validate:"required,len=6,numeric"`
	OTPType    string `json:"otp_type"    validate:"required,oneof=mobile email"`
}

// OTPVerificationResponse represents the response after successful OTP verification
type OTPVerificationResponse struct {
	Message      string          `json:"message"`
	Token        string          `json:"token"`
	RefreshToken string          `json:"refresh_token"`
	Customer     AuthCustomerDTO `json:"customer"`
}

type OTPResendRequest struct {
	CustomerID uint   `json:"customer_id" validate:"required"`
	OTPType    string `json:"otp_type"    validate:"required,oneof=mobile email"`
}

type OTPResendResponse struct {
	Message         string `json:"message"`
	OTPSent         bool   `json:"otp_sent"`
	MaskedOTPTarget string `json:"masked_otp_target"`
}
