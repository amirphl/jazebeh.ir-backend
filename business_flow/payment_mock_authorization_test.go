package businessflow

import (
	"context"
	"errors"
	"testing"
)

func TestAuthorizeMockPaymentCustomer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		owner   uint
		allowed bool
	}{
		{"missing authentication", context.Background(), 42, false},
		{"wrong customer", WithMockPaymentCustomer(context.Background(), 43), 42, false},
		{"owner", WithMockPaymentCustomer(context.Background(), 42), 42, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizeMockPaymentCustomer(tc.ctx, tc.owner)
			if tc.allowed && err != nil {
				t.Fatalf("owner rejected: %v", err)
			}
			if !tc.allowed && !errors.Is(err, ErrForbidden) {
				t.Fatalf("unauthorized customer: %v", err)
			}
		})
	}
}
