package dto

import "time"

// CreateCryptoPaymentRequest represents a frontend request to create a crypto payment request
type CreateCryptoPaymentRequest struct {
	CustomerID    uint   `json:"-"` // injected from auth
	AmountWithTax uint64 `json:"amount_with_tax" validate:"required,min=1000"`
	Coin          string `json:"coin"            validate:"required,oneof=ETH DOGE XRP BNB"`
	Network       string `json:"network"         validate:"required"`
	Platform      string `json:"platform"        validate:"required"`
}

// CreateCryptoPaymentResponse returns the quote and deposit address
type CreateCryptoPaymentResponse struct {
	RequestUUID        string  `json:"request_uuid"`
	DepositAddress     string  `json:"deposit_address"`
	DepositMemo        *string `json:"deposit_memo,omitempty"`
	ExpectedCoinAmount string  `json:"expected_coin_amount"`
	ExchangeRate       string  `json:"exchange_rate"`
	RateSource         string  `json:"rate_source"`
	ExpiresAt          *string `json:"expires_at,omitempty"`
	PaymentURL         *string `json:"payment_url,omitempty"`
}

// GetCryptoPaymentStatusRequest queries the status of a crypto payment request
type GetCryptoPaymentStatusRequest struct {
	CustomerID uint   `json:"-"`
	UUID       string `json:"uuid" validate:"required"`
}

// DepositInfoDTO describes detected or confirmed deposit
type DepositInfoDTO struct {
	TxHash                string  `json:"tx_hash"`
	AmountCoin            string  `json:"amount_coin"`
	Confirmations         int     `json:"confirmations"`
	RequiredConfirmations int     `json:"required_confirmations"`
	Status                string  `json:"status"`
	DetectedAt            *string `json:"detected_at,omitempty"`
	ConfirmedAt           *string `json:"confirmed_at,omitempty"`
	CreditedAt            *string `json:"credited_at,omitempty"`
}

// GetCryptoPaymentStatusResponse returns current status and deposits
type GetCryptoPaymentStatusResponse struct {
	Status       string           `json:"status"`
	StatusReason string           `json:"status_reason"`
	FiatAmount   uint64           `json:"fiat_amount_toman"`
	Coin         string           `json:"coin"`
	Network      string           `json:"network"`
	Platform     string           `json:"platform"`
	ExpectedCoin string           `json:"expected_coin_amount"`
	DepositAddr  string           `json:"deposit_address"`
	DepositMemo  *string          `json:"deposit_memo,omitempty"`
	Deposits     []DepositInfoDTO `json:"deposits"`
	ExpiresAt    *string          `json:"expires_at,omitempty"`
}

// GetSupportedAssetsResponse lists available platforms/coins/networks
type GetSupportedAssetsResponse struct {
	Platforms []SupportedPlatform `json:"platforms"`
}

type SupportedPlatform struct {
	Name     string         `json:"name"`
	Coins    []string       `json:"coins"`
	Networks map[string]int `json:"networks"` // network -> required confirmations
}

// ManualVerifyCryptoDepositRequest triggers verification by tx hash
type ManualVerifyCryptoDepositRequest struct {
	CustomerID  uint   `json:"-"`
	RequestUUID string `json:"request_uuid" validate:"required"`
	TxHash      string `json:"tx_hash"      validate:"required"`
}

// ManualVerifyCryptoDepositResponse returns updated status
type ManualVerifyCryptoDepositResponse struct {
	Status     string  `json:"status"`
	Credited   bool    `json:"credited"`
	CreditedAt *string `json:"credited_at,omitempty"`
}

// CancelCryptoPaymentRequest cancels an open crypto payment request
type CancelCryptoPaymentRequest struct {
	CustomerID uint   `json:"-"`
	UUID       string `json:"uuid" validate:"required"`
}

// OxapayWebhookTx describes one transaction object inside Oxapay webhook
type OxapayWebhookTx struct {
	Status          string  `json:"status"`
	TxHash          string  `json:"tx_hash"`
	SentAmount      float64 `json:"sent_amount"`
	ReceivedAmount  float64 `json:"received_amount"`
	Value           float64 `json:"value"`
	SentValue       float64 `json:"sent_value"`
	Currency        string  `json:"currency"`
	Network         string  `json:"network"`
	SenderAddress   string  `json:"sender_address"`
	Address         string  `json:"address"`
	Rate            float64 `json:"rate"`
	Confirmations   int     `json:"confirmations"`
	AutoConvertAmt  float64 `json:"auto_convert_amount"`
	AutoConvertCurr string  `json:"auto_convert_currency"`
	Date            int64   `json:"date"`
}

// OxapayWebhookPayload is the payment webhook schema (subset based on docs)
type OxapayWebhookPayload struct {
	TrackID     string            `json:"track_id"`
	Status      string            `json:"status"`
	Type        string            `json:"type"`
	ModuleName  string            `json:"module_name"`
	Amount      float64           `json:"amount"`
	Value       float64           `json:"value"`
	SentValue   float64           `json:"sent_value"`
	Currency    string            `json:"currency"`
	OrderID     string            `json:"order_id"`
	Email       string            `json:"email"`
	Note        string            `json:"note"`
	FeePaidBy   int               `json:"fee_paid_by_payer"`
	UnderPaid   float64           `json:"under_paid_coverage"`
	Description string            `json:"description"`
	Date        int64             `json:"date"`
	Txs         []OxapayWebhookTx `json:"txs"`
}

// utility
func FormatTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
