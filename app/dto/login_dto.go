// Package dto contains Data Transfer Objects for API request and response structures
package dto

import (
	"time"
)

// LoginRequest represents the request payload for user login
type LoginRequest struct {
	Identifier string `json:"identifier" validate:"required,mobile_format" example:"+989123456789"`
	Password   string `json:"password"   validate:"required,min=8,max=100" example:"SecurePass123!"`
	OTPCode    string `json:"otp_code"   validate:"required,len=6,numeric" example:"123456"`
}

// LoginResponse represents the result of a login attempt
type LoginResponse struct {
	Customer AuthCustomerDTO
	Session  CustomerSessionDTO
}

type LoginOTPRequest struct {
	Identifier      string `json:"identifier"                   validate:"required,mobile_format" example:"+989123456789"`
	LogOTPToConsole bool   `json:"log_otp_to_console,omitempty"                                   example:"false"`
}

type LoginOTPResponse struct {
	Message     string    `json:"message"`
	CustomerID  uint      `json:"customer_id"`
	MaskedPhone string    `json:"masked_phone"`
	OTPSent     bool      `json:"otp_sent"`
	AlreadySent bool      `json:"already_sent"`
	OTPExpiry   time.Time `json:"otp_expiry"`
}

// ForgotPasswordRequest represents the request to initiate password reset
type ForgotPasswordRequest struct {
	Identifier string `json:"identifier" validate:"required,min=3,max=255" example:"user@example.com or +989123456789"`
}

// ForgetPasswordResponse represents the result of a password reset request
type ForgetPasswordResponse struct {
	CustomerID  uint
	MaskedPhone string
	OTPExpiry   time.Time
}

// ResetPasswordRequest represents the request to reset password with OTP
type ResetPasswordRequest struct {
	CustomerID      uint   `json:"customer_id"      validate:"required"                                 example:"1"`
	OTPCode         string `json:"otp_code"         validate:"required,len=6,numeric"                   example:"123456"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=100,password_strength" example:"NewSecurePass123!"`
	ConfirmPassword string `json:"confirm_password" validate:"required,eqfield=NewPassword"             example:"NewSecurePass123!"`
}

// ResetPasswordResponse represents the result of a password reset request
type ResetPasswordResponse struct {
	Customer AuthCustomerDTO
	Session  CustomerSessionDTO
}

// MaskPhoneNumber masks the middle digits of a phone number for security
func MaskPhoneNumber(phone string) string {
	if len(phone) < 8 {
		return phone
	}

	// For numbers like +989123456789, show +9891234*****
	if len(phone) >= 10 {
		return phone[:7] + "*****"
	}

	// For shorter numbers, mask the middle part
	start := len(phone) / 3
	end := len(phone) - start
	masked := phone[:start] + "*****" + phone[end:]
	return masked
}

// AuthCustomerDTO represents minimal customer data for authentication responses
type AuthCustomerDTO struct {
	ID                      uint    `json:"id"                           example:"1"`
	UUID                    string  `json:"uuid"                         example:"550e8400-e29b-41d4-a716-446655440000"`
	Email                   string  `json:"email"                        example:"user@example.com"`
	RepresentativeFirstName string  `json:"representative_first_name"    example:"John"`
	RepresentativeLastName  string  `json:"representative_last_name"     example:"Doe"`
	RepresentativeMobile    string  `json:"representative_mobile"        example:"+989123456789"`
	AccountType             string  `json:"account_type"                 example:"individual"`
	CompanyName             *string `json:"company_name,omitempty"       example:"Tech Company Ltd"                     validate:"omitempty"`
	IsActive                *bool   `json:"is_active"                    example:"true"`
	IsEmailVerified         *bool   `json:"is_email_verified"            example:"true"`
	IsMobileVerified        *bool   `json:"is_mobile_verified"           example:"true"`
	CreatedAt               string  `json:"created_at"                   example:"2024-01-15T10:30:00Z"`
	ReferrerAgencyID        *uint   `json:"referrer_agency_id,omitempty" example:"123"                                  validate:"omitempty"`
}

type CustomerSessionDTO struct {
	SessionToken string  `json:"session_token"           example:"1234567890"`
	RefreshToken *string `json:"refresh_token,omitempty" example:"1234567890"           validate:"omitempty"`
	ExpiresIn    int     `json:"expires_in"              example:"3600"`
	TokenType    string  `json:"token_type"              example:"Bearer"`
	CreatedAt    string  `json:"created_at"              example:"2024-01-15T10:30:00Z"`
}
