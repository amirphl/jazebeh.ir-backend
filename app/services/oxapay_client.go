package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/utils"
)

type OxapayClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
}

func NewOxapayClient(baseURL, apiKey string, timeout time.Duration) *OxapayClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &OxapayClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: timeout},
		Timeout:    timeout,
	}
}

func (c *OxapayClient) Name() string { return "oxapay" }

// Quote via common/prices
// Docs: https://docs.oxapay.com/api-reference/common/prices

type oxapayPricesResp struct {
	Success bool              `json:"success"`
	Data    map[string]string `json:"data"` // e.g., {"ETHUSDT":"3171.96", ...}
}

func (c *OxapayClient) GetQuote(ctx context.Context, in QuoteInput) (*QuoteResult, error) {
	pair := strings.ToUpper(in.Coin) + "USDT"
	url := c.BaseURL + "/common/prices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &QuoteResult{
			ExpectedCoinAmount: "",
			ExchangeRate:       "",
			RateSource:         "oxapay:error",
			ExpiresAt:          nil,
		}, nil
	}
	var out oxapayPricesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	price := out.Data[pair]
	exp := time.Now().Add(1 * time.Minute)
	return &QuoteResult{
		ExpectedCoinAmount: "",
		ExchangeRate:       price,
		RateSource:         "oxapay:prices",
		ExpiresAt:          &exp,
	}, nil
}

// Provision via payment/static-address (merchant_api_key header)
// Spec sample: https://docs.oxapay.com/api-reference/payment/generate-static-address and webhook doc

// Request body per new API spec
// network, to_currency, auto_withdrawal, callback_url, email, order_id, description
type oxapayStaticAddressCreateReq struct {
	Network        string `json:"network"`
	ToCurrency     string `json:"to_currency"`
	AutoWithdrawal bool   `json:"auto_withdrawal"`
	CallbackURL    string `json:"callback_url,omitempty"`
	Email          string `json:"email,omitempty"`
	OrderID        string `json:"order_id,omitempty"`
	Description    string `json:"description,omitempty"`
}

// Response envelope per spec (status/message/error) with data possibly containing address/tag/track_id
type oxapayStaticAddressData struct {
	Address     *string `json:"address"`
	Tag         *string `json:"tag"`
	ID          *string `json:"id"`
	TrackID     *string `json:"track_id"`
	Network     string  `json:"network,omitempty"`
	ToCurrency  string  `json:"to_currency,omitempty"`
	CallbackURL string  `json:"callback_url,omitempty"`
	Email       string  `json:"email,omitempty"`
	OrderID     string  `json:"order_id,omitempty"`
	Description string  `json:"description,omitempty"`
}

type oxapayEnvelope struct {
	Data    oxapayStaticAddressData `json:"data"`
	Message string                  `json:"message"`
	Error   any                     `json:"error"`
	Status  int                     `json:"status"`
	Version string                  `json:"version"`
}

func (c *OxapayClient) ProvisionDeposit(ctx context.Context, in ProvisionInput) (*ProvisionResult, error) {
	body := oxapayStaticAddressCreateReq{
		Network:        in.Network,
		ToCurrency:     strings.ToUpper(in.Coin),
		AutoWithdrawal: false,
		CallbackURL:    in.CallbackURL,
		OrderID:        in.Label,
		Description:    "deposit " + in.Label,
	}
	var env oxapayEnvelope
	if err := c.postMerchantJSON(ctx, "/payment/static-address", body, &env); err != nil {
		return nil, err
	}
	addr := env.Data.Address
	if addr == nil || *addr == "" {
		// return nil, errors.New("oxapay: empty address in response")
	}

	memo := ""
	if env.Data.Tag != nil {
		memo = *env.Data.Tag
	}
	pid := in.Label
	if env.Data.ID != nil && *env.Data.ID != "" {
		pid = *env.Data.ID
	} else if env.Data.TrackID != nil && *env.Data.TrackID != "" {
		pid = *env.Data.TrackID
	}
	return &ProvisionResult{
		// DepositAddress:    *addr,
		DepositAddress:    "", // TODO:
		DepositMemo:       memo,
		ProviderRequestID: pid,
		ExpiresAt:         nil,
	}, nil
}

// Not using polling for now; Oxapay supports callbacks and history endpoints
func (c *OxapayClient) GetDeposits(ctx context.Context, providerRequestID string) ([]DepositInfo, error) {
	return nil, nil
}

func (c *OxapayClient) VerifyTx(ctx context.Context, txHash string) (*DepositInfo, error) {
	return nil, errors.New("oxapay: VerifyTx not implemented; use webhook or history endpoint mapping")
}

// HTTP helpers
func (c *OxapayClient) postAuthJSON(ctx context.Context, path string, payload any, out any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oxapay: status %d for %s", resp.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// postMerchantJSON sets the required header 'merchant_api_key' for Oxapay merchant APIs
func (c *OxapayClient) postMerchantJSON(ctx context.Context, path string, payload any, out any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("merchant_api_key", c.APIKey)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oxapay: status %d for %s", resp.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type OxapayInvoiceInput struct {
	FiatAmountToman uint64
	CallbackURL     string
	ReturnURL       string
	Label           string // order_id
	LifetimeMin     int
	MixedPayment    bool
	AutoWithdrawal  bool
	Email           string
	ThanksMessage   string
	Description     string
	Sandbox         bool
}

type OxapayInvoiceResult struct {
	TrackID    string
	PaymentURL string
	ExpiredAt  *time.Time
}

// request/response for invoice endpoint
// POST /payment/invoice (merchant_api_key header)
type oxapayInvoiceReq struct {
	Amount         float64  `json:"amount"`
	Currency       string   `json:"currency"`
	LifeTime       int      `json:"lifetime,omitempty"`
	FeePaidByPayer *int     `json:"fee_paid_by_payer,omitempty"`
	UnderPaidCover *float64 `json:"under_paid_coverage,omitempty"`
	ToCurrency     string   `json:"to_currency,omitempty"`
	AutoWithdrawal bool     `json:"auto_withdrawal"`
	MixedPayment   bool     `json:"mixed_payment"`
	CallbackURL    string   `json:"callback_url"`
	ReturnURL      string   `json:"return_url,omitempty"`
	Email          string   `json:"email,omitempty"`
	OrderID        string   `json:"order_id"`
	ThanksMessage  string   `json:"thanks_message,omitempty"`
	Description    string   `json:"description,omitempty"`
	Sandbox        bool     `json:"sandbox"`
}

type oxapayInvoiceData struct {
	TrackID    string `json:"track_id"`
	PaymentURL string `json:"payment_url"`
	ExpiredAt  int64  `json:"expired_at"`
	Date       int64  `json:"date"`
}

type oxapayInvoiceEnv struct {
	Data    oxapayInvoiceData `json:"data"`
	Message string            `json:"message"`
	Error   any               `json:"error"`
	Status  int               `json:"status"`
	Version string            `json:"version"`
}

func (c *OxapayClient) CreateInvoice(ctx context.Context, in OxapayInvoiceInput) (*OxapayInvoiceResult, error) {
	// Convert toman to USDT using Wallex USDTTMN price (Toman per USDT)
	priceTMNStr, err := c.fetchWallexPrice(ctx, "USDTTMN")
	if err != nil || priceTMNStr == "" {
		return nil, fmt.Errorf("wallex usdttmp: %w", err)
	}
	priceTMN, err := strconv.ParseFloat(priceTMNStr, 64)
	if err != nil || priceTMN <= 0 {
		return nil, fmt.Errorf("invalid usdt/tmn price")
	}
	amountUSDT := float64(in.FiatAmountToman) / priceTMN

	life := in.LifetimeMin
	if life <= 0 {
		life = 60
	}
	body := oxapayInvoiceReq{
		Amount:         amountUSDT,
		Currency:       "USDT", // invoice denominated in USDT
		LifeTime:       life,
		ToCurrency:     "",
		FeePaidByPayer: utils.ToPtr(1),
		// UnderPaidCover: utils.ToPtr(10),
		AutoWithdrawal: in.AutoWithdrawal,
		MixedPayment:   in.MixedPayment,
		CallbackURL:    in.CallbackURL,
		ReturnURL:      in.ReturnURL,
		Email:          in.Email,
		OrderID:        in.Label,
		ThanksMessage:  in.ThanksMessage,
		Description:    in.Description,
		Sandbox:        in.Sandbox,
	}
	var env oxapayInvoiceEnv
	if err := c.postMerchantJSON(ctx, "/payment/invoice", body, &env); err != nil {
		return nil, err
	}
	if env.Data.PaymentURL == "" || env.Data.TrackID == "" {
		return nil, errors.New("oxapay: empty invoice response")
	}
	var exp *time.Time
	if env.Data.ExpiredAt > 0 {
		t := time.Unix(env.Data.ExpiredAt, 0).UTC()
		exp = &t
	}
	return &OxapayInvoiceResult{TrackID: env.Data.TrackID, PaymentURL: env.Data.PaymentURL, ExpiredAt: exp}, nil
}

// wallex trades for USDTIRR
type wallexTradesResponseOxapay struct {
	Result struct {
		LatestTrades []struct {
			Symbol    string `json:"symbol"`
			Price     string `json:"price"`
			Timestamp string `json:"timestamp"`
		} `json:"latestTrades"`
	} `json:"result"`
	Message string `json:"message"`
	Success bool   `json:"success"`
}

func (c *OxapayClient) fetchWallexPrice(ctx context.Context, pair string) (string, error) {
	url := "https://api.wallex.ir/v1/trades?symbol=" + strings.ToLower(pair)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wallex: http %d", resp.StatusCode)
	}
	var wr wallexTradesResponseOxapay
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		return "", err
	}
	if !wr.Success || len(wr.Result.LatestTrades) == 0 {
		return "", errors.New("wallex: empty trades")
	}
	return wr.Result.LatestTrades[0].Price, nil
}

type OxapayPaymentTx struct {
	TxHash        string
	Amount        float64
	Currency      string
	Network       string
	Address       string
	Status        string
	Confirmations int
	Date          int64
}

type OxapayPaymentInfo struct {
	TrackID   string
	Type      string
	Amount    float64
	Currency  string
	Status    string
	Txs       []OxapayPaymentTx
	ExpiredAt *time.Time
}

type oxapayPaymentTxJSON struct {
	TxHash        string  `json:"tx_hash"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Network       string  `json:"network"`
	Address       string  `json:"address"`
	Status        string  `json:"status"`
	Confirmations int     `json:"confirmations"`
	AutoConvert   *struct {
		Processed *bool    `json:"processed"`
		Amount    *float64 `json:"amount"`
		Currency  *string  `json:"currency"`
	} `json:"auto_convert"`
	AutoWithdrawal *struct {
		Processed *bool `json:"processed"`
	}
	Date int64 `json:"date"`
}

type oxapayPaymentInfoData struct {
	TrackID   string                `json:"track_id"`
	Type      string                `json:"type"`
	Amount    float64               `json:"amount"`
	Currency  string                `json:"currency"`
	Status    string                `json:"status"`
	ExpiredAt int64                 `json:"expired_at"`
	Date      int64                 `json:"date"`
	Txs       []oxapayPaymentTxJSON `json:"txs"`
}

type oxapayPaymentInfoEnv struct {
	Data    oxapayPaymentInfoData `json:"data"`
	Message string                `json:"message"`
	Error   any                   `json:"error"`
	Status  int                   `json:"status"`
	Version string                `json:"version"`
}

func (c *OxapayClient) GetPaymentInfo(ctx context.Context, trackID string) (*OxapayPaymentInfo, error) {
	if strings.TrimSpace(trackID) == "" {
		return nil, errors.New("oxapay: empty track_id")
	}
	url := c.BaseURL + "/payment/" + trackID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("merchant_api_key", c.APIKey)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oxapay: status %d for payment info", resp.StatusCode)
	}
	var env oxapayPaymentInfoEnv
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	out := &OxapayPaymentInfo{
		TrackID:  env.Data.TrackID,
		Type:     env.Data.Type,
		Amount:   env.Data.Amount,
		Currency: env.Data.Currency,
		Status:   env.Data.Status,
		Txs:      make([]OxapayPaymentTx, 0, len(env.Data.Txs)),
	}
	if env.Data.ExpiredAt > 0 {
		t := time.Unix(env.Data.ExpiredAt, 0).UTC()
		out.ExpiredAt = &t
	}
	for _, t := range env.Data.Txs {
		out.Txs = append(out.Txs, OxapayPaymentTx{
			TxHash:        t.TxHash,
			Amount:        t.Amount,
			Currency:      t.Currency,
			Network:       t.Network,
			Address:       t.Address,
			Status:        t.Status,
			Confirmations: t.Confirmations,
			Date:          t.Date,
		})
	}
	return out, nil
}
