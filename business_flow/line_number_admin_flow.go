// Package businessflow contains the core business logic and use cases for authentication workflows
package businessflow

import (
	"context"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AdminLineNumberFlow handles admin operations on line numbers
type AdminLineNumberFlow interface {
	Create(ctx context.Context, req *dto.AdminCreateLineNumberRequest, metadata *ClientMetadata) (*dto.AdminLineNumberDTO, error)
	ListAll(ctx context.Context, metadata *ClientMetadata) ([]*dto.AdminLineNumberDTO, error)
	UpdateBatch(ctx context.Context, req *dto.AdminUpdateLineNumbersRequest, metadata *ClientMetadata) error
	UpdatePriceFactor(ctx context.Context, req *dto.AdminUpdateLineNumberPriceFactorRequest, metadata *ClientMetadata) (*dto.AdminLineNumberDTO, error)
	GetReport(ctx context.Context, metadata *ClientMetadata) ([]*dto.AdminLineNumberReportItem, error)
}

type AdminLineNumberFlowImpl struct {
	lineRepo  repository.LineNumberRepository
	db        *gorm.DB
	auditRepo repository.AuditLogRepository
}

func NewAdminLineNumberFlow(lineRepo repository.LineNumberRepository, db *gorm.DB, auditRepo repository.AuditLogRepository) AdminLineNumberFlow {
	return &AdminLineNumberFlowImpl{
		lineRepo:  lineRepo,
		db:        db,
		auditRepo: auditRepo,
	}
}

func (f *AdminLineNumberFlowImpl) Create(ctx context.Context, req *dto.AdminCreateLineNumberRequest, metadata *ClientMetadata) (*dto.AdminLineNumberDTO, error) {
	// Validate
	if req == nil {
		return nil, NewBusinessError("LINE_NUMBER_VALIDATION_FAILED", "Create line number validation failed", ErrLineNumberValueRequired)
	}
	value := strings.TrimSpace(req.LineNumber)
	if value == "" {
		return nil, NewBusinessError("LINE_NUMBER_REQUIRED", "Line number is required", ErrLineNumberValueRequired)
	}
	if len(value) > 50 {
		value = value[:50]
	}
	if req.PriceFactor <= 0 {
		return nil, NewBusinessError("PRICE_FACTOR_INVALID", "Price factor must be greater than zero", ErrPriceFactorInvalid)
	}
	provider := models.SMSProvider(strings.ToLower(strings.TrimSpace(req.Provider)))
	if provider == "" {
		provider = models.SMSProviderPayamSMS
	}
	if !models.IsValidSMSProvider(provider) {
		return nil, NewBusinessError("LINE_NUMBER_PROVIDER_INVALID", "Line number provider is invalid", ErrLineNumberNotFound)
	}

	// Uniqueness check
	existing, err := f.lineRepo.ByValue(ctx, value)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		existing.Name = req.Name
		existing.Provider = provider
		existing.PriceFactor = req.PriceFactor
		existing.Priority = req.Priority
		existing.IsActive = req.IsActive
		existing.UpdatedAt = utils.UTCNow()

		if err := f.lineRepo.Update(ctx, existing); err != nil {
			logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin update existing line number", false, nil, map[string]any{
				"line_number": value,
				"id":          existing.ID,
			}, err)
			return nil, err
		}

		resp := ToLineNumberDTO(*existing)
		logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin update existing line number", true, nil, map[string]any{
			"line_number": value,
			"id":          existing.ID,
		}, nil)
		return &resp, nil
	}

	// Build entity
	ln := models.LineNumber{
		UUID:        uuid.New(),
		Name:        req.Name,
		LineNumber:  value,
		Provider:    provider,
		PriceFactor: req.PriceFactor,
		Priority:    req.Priority,
		IsActive:    req.IsActive,
		CreatedAt:   utils.UTCNow(),
		UpdatedAt:   utils.UTCNow(),
	}

	// Save
	if err := f.lineRepo.Save(ctx, &ln); err != nil {
		logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberCreate, "Admin create line number", false, nil, map[string]any{
			"line_number": value,
		}, err)
		return nil, err
	}

	resp := ToLineNumberDTO(ln)
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberCreate, "Admin create line number", true, nil, map[string]any{
		"line_number": value,
	}, nil)
	return &resp, nil
}

func (f *AdminLineNumberFlowImpl) ListAll(ctx context.Context, metadata *ClientMetadata) ([]*dto.AdminLineNumberDTO, error) {
	lines, err := f.lineRepo.ByFilter(ctx, models.LineNumberFilter{}, "id DESC", 0, 0)
	if err != nil {
		return nil, NewBusinessError("LINE_NUMBER_LIST_FAILED", "Failed to list line numbers", err)
	}
	result := make([]*dto.AdminLineNumberDTO, 0, len(lines))
	for _, ln := range lines {
		dtoItem := ToLineNumberDTO(*ln)
		result = append(result, &dtoItem)
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberList, "Admin list line numbers", true, nil, map[string]any{
		"count": len(result),
	}, nil)
	return result, nil
}

func (f *AdminLineNumberFlowImpl) UpdateBatch(ctx context.Context, req *dto.AdminUpdateLineNumbersRequest, metadata *ClientMetadata) error {
	if req == nil || len(req.Items) == 0 {
		return nil
	}
	// Validate and map
	updates := make([]*models.LineNumber, 0, len(req.Items))
	for _, item := range req.Items {
		if item.ID == 0 {
			return NewBusinessError("LINE_NUMBER_UPDATE_VALIDATION_FAILED", "Line number ID is required", ErrLineNumberValueRequired)
		}

		line := &models.LineNumber{
			ID:        item.ID,
			Priority:  item.Priority,
			IsActive:  item.IsActive,
			UpdatedAt: utils.UTCNow(),
		}
		if item.Provider != nil {
			provider := models.SMSProvider(strings.ToLower(strings.TrimSpace(*item.Provider)))
			if !models.IsValidSMSProvider(provider) {
				return NewBusinessError("LINE_NUMBER_PROVIDER_INVALID", "Line number provider is invalid", ErrLineNumberNotFound)
			}
			line.Provider = provider
		}
		updates = append(updates, line)
	}
	// Persist
	if err := f.lineRepo.UpdateBatch(ctx, updates); err != nil {
		logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin batch update line numbers", false, nil, map[string]any{
			"updates": len(updates),
		}, err)
		return NewBusinessError("LINE_NUMBER_BATCH_UPDATE_FAILED", "Failed to update line numbers", err)
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin batch update line numbers", true, nil, map[string]any{
		"updates": len(updates),
	}, nil)
	return nil
}

func (f *AdminLineNumberFlowImpl) UpdatePriceFactor(ctx context.Context, req *dto.AdminUpdateLineNumberPriceFactorRequest, metadata *ClientMetadata) (*dto.AdminLineNumberDTO, error) {
	if req == nil {
		return nil, NewBusinessError("LINE_NUMBER_VALIDATION_FAILED", "Update line number price factor validation failed", ErrLineNumberValueRequired)
	}

	value := strings.TrimSpace(req.LineNumber)
	if value == "" {
		return nil, NewBusinessError("LINE_NUMBER_REQUIRED", "Line number is required", ErrLineNumberValueRequired)
	}
	if req.PriceFactor <= 0 {
		return nil, NewBusinessError("PRICE_FACTOR_INVALID", "Price factor must be greater than zero", ErrPriceFactorInvalid)
	}

	existing, err := f.lineRepo.ByValue(ctx, value)
	if err != nil {
		return nil, NewBusinessError("LINE_NUMBER_FETCH_FAILED", "Failed to fetch line number", err)
	}
	if existing == nil {
		return nil, NewBusinessError("LINE_NUMBER_NOT_FOUND", "Line number not found", ErrLineNumberNotFound)
	}

	existing.PriceFactor = req.PriceFactor
	existing.UpdatedAt = utils.UTCNow()
	if err := f.lineRepo.Update(ctx, existing); err != nil {
		logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin update line number price factor", false, nil, map[string]any{
			"line_number":  value,
			"price_factor": req.PriceFactor,
		}, err)
		return nil, NewBusinessError("LINE_NUMBER_UPDATE_FAILED", "Failed to update line number price factor", err)
	}

	resp := ToLineNumberDTO(*existing)
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberUpdate, "Admin update line number price factor", true, nil, map[string]any{
		"line_number":  value,
		"price_factor": req.PriceFactor,
	}, nil)
	return &resp, nil
}

func (f *AdminLineNumberFlowImpl) GetReport(ctx context.Context, metadata *ClientMetadata) ([]*dto.AdminLineNumberReportItem, error) {
	// TODO: implement aggregation logic across messages/campaigns/transactions
	items := []*dto.AdminLineNumberReportItem{}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminLineNumberReport, "Admin requested line number report", true, nil, map[string]any{
		"items": len(items),
	}, nil)
	return items, nil
}
