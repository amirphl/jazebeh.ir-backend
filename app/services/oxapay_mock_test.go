package services

import (
	"context"
	"strings"
	"testing"
)

func TestMockOxapayInvoiceDoesNotReturnExternalURL(t *testing.T) {
	provider := NewMockOxapayClient()
	invoices, ok := provider.(interface {
		CreateInvoice(context.Context, OxapayInvoiceInput) (*OxapayInvoiceResult, error)
		GetPaymentInfo(context.Context, string) (*OxapayPaymentInfo, error)
	})
	if !ok {
		t.Fatal("mock provider must support invoice operations")
	}
	invoice, err := invoices.CreateInvoice(context.Background(), OxapayInvoiceInput{Label: "invoice-123", ReturnURL: "https://example.com/return"})
	if err != nil {
		t.Fatal(err)
	}
	if invoice.PaymentURL != "/dashboard/wallet" || !strings.HasPrefix(invoice.TrackID, "mock-") {
		t.Fatalf("unexpected mock invoice: %+v", invoice)
	}
	status, err := invoices.GetPaymentInfo(context.Background(), invoice.TrackID)
	if err != nil || status.TrackID != invoice.TrackID || status.Status != "Waiting" || len(status.Txs) != 0 {
		t.Fatalf("unexpected mock status: %+v, %v", status, err)
	}
	status, err = invoices.GetPaymentInfo(context.Background(), invoice.TrackID)
	if err != nil || status.Status != "Paid" || len(status.Txs) != 1 || status.Txs[0].TxHash == "" {
		t.Fatalf("unexpected paid mock status: %+v, %v", status, err)
	}

	deposits, err := provider.GetDeposits(context.Background(), invoice.TrackID)
	if err != nil || len(deposits) != 1 || deposits[0].TxHash != status.Txs[0].TxHash || deposits[0].ConfirmedAt == nil {
		t.Fatalf("unexpected mock deposits: %+v, %v", deposits, err)
	}
}
