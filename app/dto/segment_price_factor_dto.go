package dto

// AdminCreateSegmentPriceFactorRequest represents the payload to add/update a segment price factor for a level3 value.
type AdminCreateSegmentPriceFactorRequest struct {
	Level3      string  `json:"level3"       validate:"required"`
	Platform    string  `json:"platform"     validate:"required,oneof=sms rubika bale splus"`
	PriceFactor float64 `json:"price_factor" validate:"required,gt=0"`
}

type AdminCreateSegmentPriceFactorResponse struct {
	Message string `json:"message"`
}

type AdminSegmentPriceFactorItem struct {
	Level3      string  `json:"level3"`
	Platform    string  `json:"platform"`
	PriceFactor float64 `json:"price_factor"`
	CreatedAt   string  `json:"created_at"`
}

type AdminListSegmentPriceFactorsResponse struct {
	Message string                        `json:"message"`
	Items   []AdminSegmentPriceFactorItem `json:"items"`
}

type AdminListLevel3OptionsResponse struct {
	Message string   `json:"message"`
	Items   []string `json:"items"`
}

type SegmentPriceFactorItem struct {
	Level3      string  `json:"level3"`
	Platform    string  `json:"platform"`
	PriceFactor float64 `json:"price_factor"`
	CreatedAt   string  `json:"created_at"`
}

type ListSegmentPriceFactorsRequest struct {
	Platform *string `json:"platform,omitempty"`
}

type ListLatestSegmentPriceFactorsResponse struct {
	Message string                   `json:"message"`
	Items   []SegmentPriceFactorItem `json:"items"`
}
