package dto

// CreatePlatformSettingsRequest represents payload for creating platform settings.
type CreatePlatformSettingsRequest struct {
	Name                *string `json:"name,omitempty"                  validate:"omitempty,max=255"`
	Description         *string `json:"description,omitempty"`
	Website             *string `json:"website,omitempty"`
	MultimediaUUID      *string `json:"multimedia_uuid,omitempty"`
	BusinessLicenseUUID *string `json:"business_license_uuid,omitempty"`
	Platform            string  `json:"platform"                        validate:"required,oneof=sms rubika bale splus"`
	Status              *string `json:"status,omitempty"                validate:"omitempty,oneof=initialized in-progress active inactive"`
}

// CreatePlatformSettingsResponse represents a successful creation response.
type CreatePlatformSettingsResponse struct {
	Message             string  `json:"message"`
	ID                  uint    `json:"id"`
	Platform            string  `json:"platform"`
	Name                *string `json:"name,omitempty"`
	Description         *string `json:"description,omitempty"`
	Website             *string `json:"website,omitempty"`
	MultimediaUUID      *string `json:"multimedia_uuid,omitempty"`
	BusinessLicenseUUID *string `json:"business_license_uuid,omitempty"`
	Status              string  `json:"status"`
	CreatedAt           string  `json:"created_at"`
}

// PlatformSettingsItem represents a platform settings row for listing.
type PlatformSettingsItem struct {
	ID                  uint    `json:"id"`
	Platform            string  `json:"platform"`
	Name                *string `json:"name,omitempty"`
	Description         *string `json:"description,omitempty"`
	Website             *string `json:"website,omitempty"`
	MultimediaUUID      *string `json:"multimedia_uuid,omitempty"`
	BusinessLicenseUUID *string `json:"business_license_uuid,omitempty"`
	Status              string  `json:"status"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

// ListPlatformSettingsResponse represents a list of platform settings.
type ListPlatformSettingsResponse struct {
	Message string                 `json:"message"`
	Items   []PlatformSettingsItem `json:"items"`
}

// AdminChangePlatformSettingsStatusRequest represents admin payload for status updates.
type AdminChangePlatformSettingsStatusRequest struct {
	ID     uint   `json:"id"     validate:"required"`
	Status string `json:"status" validate:"required,oneof=in-progress active inactive"`
}

type AdminChangePlatformSettingsStatusResponse struct {
	Message string `json:"message"`
	ID      uint   `json:"id"`
	Status  string `json:"status"`
}

type AdminPlatformSettingsItem struct {
	ID                  uint           `json:"id"`
	UUID                string         `json:"uuid"`
	CustomerID          uint           `json:"customer_id"`
	Platform            string         `json:"platform"`
	Name                *string        `json:"name,omitempty"`
	Description         *string        `json:"description,omitempty"`
	Website             *string        `json:"website,omitempty"`
	MultimediaUUID      *string        `json:"multimedia_uuid,omitempty"`
	BusinessLicenseUUID *string        `json:"business_license_uuid,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	Status              string         `json:"status"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
}

type AdminListPlatformSettingsResponse struct {
	Message string                      `json:"message"`
	Items   []AdminPlatformSettingsItem `json:"items"`
}

type AdminAddPlatformSettingsMetadataRequest struct {
	ID    uint   `json:"id"    validate:"required"`
	Key   string `json:"key"   validate:"required,min=1,max=100"`
	Value string `json:"value" validate:"required"`
}

type AdminAddPlatformSettingsMetadataResponse struct {
	Message  string         `json:"message"`
	ID       uint           `json:"id"`
	Metadata map[string]any `json:"metadata"`
}
