// Package businessflow contains use cases for user-facing line numbers
package businessflow

import (
	"context"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// LineNumberFlow defines user-facing operations for line numbers for customers
type LineNumberFlow interface {
	ListActiveLineNumbers(ctx context.Context, metadata *ClientMetadata) (*dto.ListActiveLineNumbersResponse, error)
}

type LineNumberFlowImpl struct {
	lineRepo repository.LineNumberRepository
}

func NewLineNumberFlow(lineRepo repository.LineNumberRepository) LineNumberFlow {
	return &LineNumberFlowImpl{lineRepo: lineRepo}
}

// ListActiveLineNumbers returns active line numbers for customers
func (f *LineNumberFlowImpl) ListActiveLineNumbers(
	ctx context.Context,
	metadata *ClientMetadata,
) (*dto.ListActiveLineNumbersResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_ACTIVE_LINE_NUMBERS_FAILED", "Failed to list active line numbers", err)
		}
	}()

	// build filter
	isActive := true
	filter := models.LineNumberFilter{IsActive: &isActive}

	orderBy := "id DESC"

	rows, err := f.lineRepo.ByFilter(ctx, filter, orderBy, 0, 0)
	if err != nil {
		return nil, err
	}

	items := make([]dto.ActiveLineNumberItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, dto.ActiveLineNumberItem{
			LineNumber:  r.LineNumber,
			PriceFactor: r.PriceFactor,
		})
	}

	return &dto.ListActiveLineNumbersResponse{
		Message: "Active line numbers retrieved successfully",
		Items:   items,
	}, nil
}
