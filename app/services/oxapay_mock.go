package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
)

// mockOxapayClient provides a small, deterministic payment lifecycle.  It is
// intentionally process-local: it represents the external provider in a mock
// environment, while the resulting deposit and credit remain durable in the
// application's database.
type mockOxapayClient struct {
	provider string
	mu       sync.Mutex
	invoices map[string]*mockOxapayInvoice
}

type mockOxapayInvoice struct {
	label string
	polls int
}

func NewMockOxapayClient() CryptoPaymentProvider {
	return &mockOxapayClient{provider: "oxapay", invoices: make(map[string]*mockOxapayInvoice)}
}

func (c *mockOxapayClient) Name() string { return c.provider }
func (c *mockOxapayClient) GetQuote(ctx context.Context, in QuoteInput) (*QuoteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(time.Hour)
	log.Printf("mock provider=%s action=quote", c.provider)
	observability.RecordMockOperation(c.provider, "quote", "success", 1)
	return &QuoteResult{ExpectedCoinAmount: fmt.Sprintf("%d", in.FiatAmountToman), ExchangeRate: "1", RateSource: "mock", ExpiresAt: &expires}, nil
}
func (c *mockOxapayClient) ProvisionDeposit(ctx context.Context, in ProvisionInput) (*ProvisionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(time.Hour)
	log.Printf("mock provider=%s action=provision", c.provider)
	observability.RecordMockOperation(c.provider, "provision", "success", 1)
	return &ProvisionResult{DepositAddress: "mock-address-" + in.Label, ProviderRequestID: "mock-" + in.Label, ExpiresAt: &expires}, nil
}
func (c *mockOxapayClient) GetDeposits(ctx context.Context, providerRequestID string) ([]DepositInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	invoice, ok := c.invoices[providerRequestID]
	if !ok || invoice.polls < 2 {
		observability.RecordMockOperation(c.provider, "deposits", "pending", 1)
		return nil, nil
	}
	observability.RecordMockOperation(c.provider, "deposits", "confirmed", 1)
	return []DepositInfo{mockDepositInfo(providerRequestID, invoice.label)}, nil
}
func (c *mockOxapayClient) VerifyTx(ctx context.Context, txHash string) (*DepositInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for trackID, invoice := range c.invoices {
		if txHash == mockTxHash(trackID) && invoice.polls >= 2 {
			deposit := mockDepositInfo(trackID, invoice.label)
			observability.RecordMockOperation(c.provider, "verify_tx", "confirmed", 1)
			return &deposit, nil
		}
	}
	observability.RecordMockOperation(c.provider, "verify_tx", "rejected", 1)
	return nil, errors.New("mock oxapay: transaction is not confirmed")
}
func (c *mockOxapayClient) CreateInvoice(ctx context.Context, in OxapayInvoiceInput) (*OxapayInvoiceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log.Printf("mock provider=%s action=invoice", c.provider)
	observability.RecordMockOperation(c.provider, "invoice", "created", 1)
	trackID := "mock-" + in.Label
	c.mu.Lock()
	c.invoices[trackID] = &mockOxapayInvoice{label: in.Label}
	c.mu.Unlock()
	return &OxapayInvoiceResult{TrackID: trackID, PaymentURL: "/dashboard/wallet"}, nil
}
func (c *mockOxapayClient) GetPaymentInfo(ctx context.Context, id string) (*OxapayPaymentInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	invoice, ok := c.invoices[id]
	if !ok {
		observability.RecordMockOperation(c.provider, "status", "not_found", 1)
		return nil, errors.New("mock oxapay: unknown invoice")
	}
	invoice.polls++
	if invoice.polls < 2 {
		observability.RecordMockOperation(c.provider, "status", "waiting", 1)
		return &OxapayPaymentInfo{TrackID: id, Status: "Waiting"}, nil
	}
	observability.RecordMockOperation(c.provider, "status", "paid", 1)
	deposit := mockDepositInfo(id, invoice.label)
	return &OxapayPaymentInfo{
		TrackID:  id,
		Status:   "Paid",
		Currency: "MOCK",
		Txs: []OxapayPaymentTx{{
			TxHash:        deposit.TxHash,
			Amount:        1,
			Currency:      "MOCK",
			Network:       "mock",
			Address:       deposit.ToAddress,
			Status:        "confirmed",
			Confirmations: 1,
			Date:          deposit.DetectedAt.Unix(),
		}},
	}, nil
}

func mockDepositInfo(trackID, label string) DepositInfo {
	now := time.Now().UTC()
	return DepositInfo{
		TxHash:                mockTxHash(trackID),
		AmountCoin:            "1",
		Confirmations:         1,
		RequiredConfirmations: 1,
		ToAddress:             "mock-address-" + label,
		Status:                "confirmed",
		DetectedAt:            &now,
		ConfirmedAt:           &now,
	}
}

func mockTxHash(trackID string) string {
	return "mock-tx-" + strings.TrimPrefix(trackID, "mock-")
}
