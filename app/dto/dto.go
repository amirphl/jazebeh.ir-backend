package dto

// APIResponse represents the standard API response structure
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"  validate:"omitempty"`
	Error   any    `json:"error,omitempty" validate:"omitempty"`
}

// ErrorDetail represents error details in API responses
type ErrorDetail struct {
	Code    string `json:"code"`
	Details any    `json:"details,omitempty" validate:"omitempty"`
}
