package dto

type AdminUpdatePlatformBasePriceRequest struct {
	Platform string `json:"platform" validate:"required,oneof=sms rubika bale splus"`
	Price    uint64 `json:"price"    validate:"required,gt=0"`
}

type AdminUpdatePlatformBasePriceResponse struct {
	Message  string `json:"message"`
	Platform string `json:"platform"`
	Price    uint64 `json:"price"`
}

type AdminPlatformBasePriceItem struct {
	Platform string `json:"platform"`
	Price    uint64 `json:"price"`
}

type AdminListPlatformBasePricesResponse struct {
	Message string                       `json:"message"`
	Items   []AdminPlatformBasePriceItem `json:"items"`
}

type PlatformBasePriceItem struct {
	Platform string `json:"platform"`
	Price    uint64 `json:"price"`
}

type ListPlatformBasePricesResponse struct {
	Message string                  `json:"message"`
	Items   []PlatformBasePriceItem `json:"items"`
}
