package businessflow

import (
	"context"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

type lineNumberPriceFactorRepoStub struct {
	byValueResult *models.LineNumber
	updated       *models.LineNumber
}

func (s *lineNumberPriceFactorRepoStub) ByFilter(context.Context, models.LineNumberFilter, string, int, int) ([]*models.LineNumber, error) {
	return nil, nil
}

func (s *lineNumberPriceFactorRepoStub) Save(context.Context, *models.LineNumber) error { return nil }

func (s *lineNumberPriceFactorRepoStub) SaveBatch(context.Context, []*models.LineNumber) error {
	return nil
}

func (s *lineNumberPriceFactorRepoStub) Count(context.Context, models.LineNumberFilter) (int64, error) {
	return 0, nil
}

func (s *lineNumberPriceFactorRepoStub) Exists(context.Context, models.LineNumberFilter) (bool, error) {
	return false, nil
}

func (s *lineNumberPriceFactorRepoStub) ByID(context.Context, uint) (*models.LineNumber, error) {
	return nil, nil
}

func (s *lineNumberPriceFactorRepoStub) ByUUID(context.Context, string) (*models.LineNumber, error) {
	return nil, nil
}

func (s *lineNumberPriceFactorRepoStub) ByValue(context.Context, string) (*models.LineNumber, error) {
	return s.byValueResult, nil
}

func (s *lineNumberPriceFactorRepoStub) Update(_ context.Context, line *models.LineNumber) error {
	s.updated = line
	return nil
}

func (s *lineNumberPriceFactorRepoStub) UpdateBatch(context.Context, []*models.LineNumber) error {
	return nil
}

func TestAdminLineNumberFlowUpdatePriceFactor(t *testing.T) {
	existing := &models.LineNumber{ID: 12, LineNumber: "0912000000", PriceFactor: 1.0}
	repo := &lineNumberPriceFactorRepoStub{byValueResult: existing}
	flow := &AdminLineNumberFlowImpl{lineRepo: repo}

	got, err := flow.UpdatePriceFactor(context.Background(), &dto.AdminUpdateLineNumberPriceFactorRequest{
		LineNumber:  "0912000000",
		PriceFactor: 1.35,
	}, nil)
	if err != nil {
		t.Fatalf("UpdatePriceFactor returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected updated line number DTO")
	}
	if got.LineNumber != "0912000000" {
		t.Fatalf("line_number = %q, want %q", got.LineNumber, "0912000000")
	}
	if got.PriceFactor != 1.35 {
		t.Fatalf("price_factor = %v, want 1.35", got.PriceFactor)
	}
	if existing.PriceFactor != 1.35 {
		t.Fatalf("record price_factor = %v, want 1.35", existing.PriceFactor)
	}
}
