// Package businessflow contains admin customer management operations
package businessflow

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// AdminCustomerManagementFlow exposes admin customer management use cases
type AdminCustomerManagementFlow interface {
	ListCustomers(ctx context.Context) (*dto.AdminListCustomersResponse, error)
	GetCustomersShares(ctx context.Context, req *dto.AdminCustomersSharesRequest) (*dto.AdminCustomersSharesResponse, error)
	GetCustomerWithCampaigns(ctx context.Context, customerID uint) (*dto.AdminCustomerWithCampaignsResponse, error)
	GetCustomerDiscountsHistory(ctx context.Context, customerID uint) (*dto.AdminCustomerDiscountHistoryResponse, error)
	SetCustomerActiveStatus(ctx context.Context, req *dto.AdminSetCustomerActiveStatusRequest) (*dto.AdminSetCustomerActiveStatusResponse, error)
}

// AdminCustomerManagementFlowImpl implements AdminCustomerManagementFlow
type AdminCustomerManagementFlowImpl struct {
	transactionRepo  repository.TransactionRepository
	customerRepo     repository.CustomerRepository
	campaignRepo     repository.CampaignRepository
	auditRepo        repository.AuditLogRepository
	lineNumberRepo   repository.LineNumberRepository
	segmentPriceRepo repository.SegmentPriceFactorRepository
}

const (
	systemCustomerEmail                 = "system@yamata-no-orochi.com"
	taxCustomerEmail                    = "tax@system.yamata-no-orochi.com"
	adminCustomersSharesDefaultPageSize = 50
	adminCustomersSharesMaxDateRange    = 31 * 24 * time.Hour
)

func NewAdminCustomerManagementFlow(
	customerRepo repository.CustomerRepository,
	campaignRepo repository.CampaignRepository,
	transactionRepo repository.TransactionRepository,
	auditRepo repository.AuditLogRepository,
	lineNumberRepo repository.LineNumberRepository,
	segmentPriceRepo repository.SegmentPriceFactorRepository,
) AdminCustomerManagementFlow {
	return &AdminCustomerManagementFlowImpl{
		transactionRepo:  transactionRepo,
		customerRepo:     customerRepo,
		campaignRepo:     campaignRepo,
		auditRepo:        auditRepo,
		lineNumberRepo:   lineNumberRepo,
		segmentPriceRepo: segmentPriceRepo,
	}
}

// ListCustomers returns all customers except system and tax users.
func (f *AdminCustomerManagementFlowImpl) ListCustomers(ctx context.Context) (*dto.AdminListCustomersResponse, error) {
	customers, err := f.customerRepo.ByFilter(ctx, models.CustomerFilter{}, "id DESC", 0, 0)
	if err != nil {
		return nil, NewBusinessError("GET_ADMIN_CUSTOMERS_LIST_FAILED", "Failed to list customers", err)
	}

	items := make([]dto.AdminCustomerDetailDTO, 0, len(customers))
	for _, cust := range customers {
		if cust == nil || isSystemOrTaxCustomer(cust) {
			continue
		}
		items = append(items, toAdminCustomerDetailDTO(*cust))
	}

	resp := &dto.AdminListCustomersResponse{
		Message: "Customers retrieved successfully",
		Items:   items,
		Total:   uint64(len(items)),
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminListCustomers, "Admin listed customers", true, nil, map[string]any{
		"total_returned": resp.Total,
	}, nil)
	return resp, nil
}

// GetCustomersShares returns aggregated shares per customer with optional date range
func (f *AdminCustomerManagementFlowImpl) GetCustomersShares(ctx context.Context, req *dto.AdminCustomersSharesRequest) (*dto.AdminCustomersSharesResponse, error) {
	var err error
	startStr := ""
	endStr := ""
	if req != nil && req.StartDate != nil {
		startStr = strings.TrimSpace(*req.StartDate)
	}
	if req != nil && req.EndDate != nil {
		endStr = strings.TrimSpace(*req.EndDate)
	}
	defer func() {
		if err != nil {
			err = NewBusinessError("GET_ADMIN_CUSTOMERS_SHARES_FAILED", "Failed to get customers shares", err)
			logAdminAction(ctx, f.auditRepo, models.AuditActionAdminViewCustomerShares, "Admin viewed customers shares", false, nil, map[string]any{
				"start_date": startStr,
				"end_date":   endStr,
			}, err)
		}
	}()

	var startDate *time.Time
	if req != nil && req.StartDate != nil && *req.StartDate != "" {
		st, e := time.Parse(time.RFC3339, *req.StartDate)
		if e != nil {
			return nil, NewBusinessError("VALIDATION_ERROR", "Invalid start_date format", e)
		}
		startDate = &st
	}
	var endDate *time.Time
	if req != nil && req.EndDate != nil && *req.EndDate != "" {
		et, e := time.Parse(time.RFC3339, *req.EndDate)
		if e != nil {
			return nil, NewBusinessError("VALIDATION_ERROR", "Invalid end_date format", e)
		}
		endDate = &et
	}
	if startDate != nil && endDate != nil && startDate.After(*endDate) {
		return nil, NewBusinessError("VALIDATION_ERROR", "start_date cannot be after end_date", ErrStartDateAfterEndDate)
	}
	if startDate == nil || endDate == nil {
		return nil, NewBusinessError("VALIDATION_ERROR", ErrReportDateRangeRequired.Error(), ErrReportDateRangeRequired)
	}
	if endDate.Sub(*startDate) > adminCustomersSharesMaxDateRange {
		return nil, NewBusinessError("VALIDATION_ERROR", ErrReportDateRangeTooLarge.Error(), ErrReportDateRangeTooLarge)
	}
	page, pageSize := req.Page, req.PageSize
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = adminCustomersSharesDefaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return nil, NewBusinessError("VALIDATION_ERROR", "page must be at least 1 and page_size must be between 1 and 100", ErrInvalidPage)
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		return nil, NewBusinessError("VALIDATION_ERROR", "page is too large", ErrInvalidPage)
	}
	offset := (page - 1) * pageSize

	rows, err := f.transactionRepo.AggregateCustomersShares(ctx, startDate, endDate, pageSize+1, offset)
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}

	customerIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		customerIDs = append(customerIDs, r.CustomerID)
	}
	totalSentByCustomer, err := f.campaignRepo.AggregateTotalSentByCustomerIDs(ctx, customerIDs)
	if err != nil {
		return nil, err
	}
	clickCountsByCustomer, err := f.campaignRepo.AggregateClickCountsByCustomerIDs(ctx, customerIDs, *startDate, *endDate)
	if err != nil {
		return nil, err
	}

	items := make([]dto.AdminCustomersSharesItem, 0, len(rows))
	var sumAgency, sumSystem, sumTax, sumTotalSent uint64
	for _, r := range rows {
		totalSent := totalSentByCustomer[r.CustomerID]
		clicks := clickCountsByCustomer[r.CustomerID]
		// NOTE: totalSent and clicks aggregate ALL campaigns for this customer
		// regardless of the startDate/endDate filter applied to the shares data.
		// ClickRate therefore represents lifetime performance, not period performance.
		// DTO uses float64 (not *float64), so fall back to 0.0 when no messages sent.
		cr := computeClickRate(clicks, float64(totalSent))
		clickRate := 0.0
		if cr != nil {
			clickRate = *cr
		}
		items = append(items, dto.AdminCustomersSharesItem{
			CustomerID:         r.CustomerID,
			FirstName:          r.FirstName,
			LastName:           r.LastName,
			FullName:           r.FullName,
			CompanyName:        r.CompanyName,
			ReferrerAgencyName: r.ReferrerAgencyName,
			AccountTypeName:    r.AccountTypeName,
			IsActive:           r.IsActive,
			AgencyShareWithTax: r.AgencyShareWithTax,
			SystemShare:        r.SystemShare,
			TaxShare:           r.TaxShare,
			TotalSent:          totalSent,
			ClickRate:          clickRate,
		})
		sumAgency += r.AgencyShareWithTax
		sumSystem += r.SystemShare
		sumTax += r.TaxShare
		sumTotalSent += totalSent
	}

	resp := &dto.AdminCustomersSharesResponse{
		Message:               "Customers shares retrieved successfully",
		Items:                 items,
		SumAgencyShareWithTax: sumAgency,
		SumSystemShare:        sumSystem,
		SumTaxShare:           sumTax,
		SumTotalSent:          sumTotalSent,
		Page:                  page,
		PageSize:              pageSize,
		HasMore:               hasMore,
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminViewCustomerShares, "Admin viewed customers shares", true, nil, map[string]any{
		"items":      len(items),
		"start_date": startStr,
		"end_date":   endStr,
	}, nil)
	return resp, nil
}

// GetCustomerWithCampaigns retrieves full customer info plus their campaigns
func (f *AdminCustomerManagementFlowImpl) GetCustomerWithCampaigns(ctx context.Context, customerID uint) (*dto.AdminCustomerWithCampaignsResponse, error) {
	customer, err := f.customerRepo.ByID(ctx, customerID)
	if err != nil {
		return nil, NewBusinessError("GET_ADMIN_CUSTOMER_FAILED", "Failed to get customer", err)
	}
	if customer == nil {
		return nil, NewBusinessError("CUSTOMER_NOT_FOUND", "Customer not found", ErrCustomerNotFound)
	}

	campaigns, err := f.campaignRepo.ByCustomerID(ctx, customerID, 0, 0)
	if err != nil {
		return nil, NewBusinessError("GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED", "Failed to get customer campaigns", err)
	}

	campaignIDs := make([]uint, 0, len(campaigns))
	for _, c := range campaigns {
		campaignIDs = append(campaignIDs, c.ID)
	}
	clickCounts, err := f.campaignRepo.AggregateClickCountsByCampaignIDs(ctx, campaignIDs)
	if err != nil {
		return nil, NewBusinessError("GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED", "Failed to get campaign clicks", err)
	}

	resp := &dto.AdminCustomerWithCampaignsResponse{
		Message:   "Customer details retrieved successfully",
		Customer:  toAdminCustomerDetailDTO(*customer),
		Campaigns: make([]dto.AdminCustomerCampaignItem, 0, len(campaigns)),
	}
	for _, c := range campaigns {
		var stats map[string]any
		if len(c.Statistics) > 0 {
			_ = json.Unmarshal(c.Statistics, &stats)
		}
		// aggregatedTotalSent: number of recipients for whom delivery completed.
		// Use parseAggregatedTotalSentFromMap so the click-rate denominator is the
		// same float64 value that computeClickRate works with (avoids uint64 truncation
		// for very large campaigns and keeps logic consistent across all flows).
		totalSentFloat := parseAggregatedTotalSentFromMap(stats)
		totalSent := uint64(totalSentFloat) // uint64 is only used for TotalSent response field

		totalDelivered := uint64(0)
		if v, ok := stats["aggregatedTotalDeliveredParts"]; ok {
			switch n := v.(type) {
			case float64:
				if n > 0 {
					totalDelivered = uint64(n)
				}
			case int64:
				if n > 0 {
					totalDelivered = uint64(n)
				}
			case json.Number:
				if f, e := n.Float64(); e == nil && f > 0 {
					totalDelivered = uint64(f)
				}
			}
		}
		clicks := clickCounts[c.ID]
		totalClicks := clicks
		// DTO uses float64 (not *float64), so fall back to 0.0 when there is no
		// delivery data yet rather than leaving the field absent.
		cr := computeClickRate(clicks, totalSentFloat)
		clickRate := 0.0
		if cr != nil {
			clickRate = *cr
		}

		segmentPriceFactor, lineNumberPriceFactor, err := f.readCampaignPriceFactorsFromMetadata(ctx, c.ID)
		if err != nil {
			return nil, NewBusinessError("GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED", "Failed to get campaign metadata", err)
		}
		if segmentPriceFactor == nil && len(c.Spec.Level3s) > 0 {
			factors, factorErr := f.segmentPriceRepo.LatestByLevel3sForPlatform(ctx, c.Spec.Level3s, c.Spec.Platform)
			if factorErr != nil {
				return nil, NewBusinessError("GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED", "Failed to get segment price factor", factorErr)
			}
			maxFactor := 0.0
			for _, level3 := range c.Spec.Level3s {
				if factor, ok := factors[level3]; ok && factor > maxFactor {
					maxFactor = factor
				}
			}
			if maxFactor > 0 {
				segmentPriceFactor = &maxFactor
			}
		}
		if lineNumberPriceFactor == nil && c.Spec.LineNumber != nil {
			lineNumber, lineErr := f.lineNumberRepo.ByValue(ctx, *c.Spec.LineNumber)
			if lineErr != nil {
				return nil, NewBusinessError("GET_ADMIN_CUSTOMER_CAMPAIGNS_FAILED", "Failed to get line number price factor", lineErr)
			}
			if lineNumber != nil {
				lineNumberPriceFactor = &lineNumber.PriceFactor
			}
		}
		segmentPriceFactorValue := float64(-1)
		if segmentPriceFactor != nil {
			segmentPriceFactorValue = *segmentPriceFactor
		}
		lineNumberPriceFactorValue := float64(-1)
		if lineNumberPriceFactor != nil {
			lineNumberPriceFactorValue = *lineNumberPriceFactor
		}
		resp.Campaigns = append(resp.Campaigns, dto.AdminCustomerCampaignItem{
			CampaignID:                  c.ID,
			ID:                          c.ID,
			UUID:                        c.UUID.String(),
			Status:                      c.Status.String(),
			CreatedAt:                   c.CreatedAt,
			UpdatedAt:                   c.UpdatedAt,
			Title:                       c.Spec.Title,
			Level1:                      c.Spec.Level1,
			Level2s:                     c.Spec.Level2s,
			Level3s:                     c.Spec.Level3s,
			Tags:                        c.Spec.Tags,
			TargetingMethod:             campaignAudienceTargetingMethod(c.Spec),
			Sex:                         c.Spec.Sex,
			City:                        c.Spec.City,
			AdLink:                      c.Spec.AdLink,
			Content:                     c.Spec.Content,
			ShortLinkDomain:             c.Spec.ShortLinkDomain,
			Category:                    c.Spec.Category,
			Job:                         c.Spec.Job,
			ScheduleAt:                  c.Spec.ScheduleAt,
			LineNumber:                  c.Spec.LineNumber,
			MediaUUID:                   c.Spec.MediaUUID,
			PlatformSettingsID:          c.Spec.PlatformSettingsID,
			Platform:                    c.Spec.Platform,
			Budget:                      c.Spec.Budget,
			Comment:                     c.Comment,
			SegmentPriceFactor:          segmentPriceFactorValue,
			LineNumberPriceFactor:       lineNumberPriceFactorValue,
			Statistics:                  stats,
			TotalClicks:                 &totalClicks,
			ClickRate:                   clickRate,
			NumAudience:                 c.NumAudience,
			SampleSizePerTag:            c.SampleSizePerTag,
			CustomerFullName:            formatCampaignPartyFullName(c.Customer),
			AgencyFullName:              formatCampaignAgencyFullName(c.Customer),
			TargetAudienceExcelFileUUID: c.Spec.TargetAudienceExcelFileUUID,
			TotalSent:                   totalSent,
			TotalDelivered:              totalDelivered,
		})
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminViewCustomer, "Admin fetched customer with campaigns", true, &customerID, map[string]any{
		"campaigns": len(campaigns),
	}, nil)
	return resp, nil
}

func toAdminCustomerDetailDTO(c models.Customer) dto.AdminCustomerDetailDTO {
	return dto.AdminCustomerDetailDTO{
		ID:                      c.ID,
		UUID:                    c.UUID.String(),
		AgencyRefererCode:       c.AgencyRefererCode,
		AccountTypeID:           c.AccountTypeID,
		AccountTypeName:         c.AccountType.TypeName,
		CompanyName:             c.CompanyName,
		NationalID:              c.NationalID,
		CompanyPhone:            c.CompanyPhone,
		CompanyAddress:          c.CompanyAddress,
		PostalCode:              c.PostalCode,
		RepresentativeFirstName: c.RepresentativeFirstName,
		RepresentativeLastName:  c.RepresentativeLastName,
		RepresentativeMobile:    c.RepresentativeMobile,
		Email:                   c.Email,
		ShebaNumber:             c.ShebaNumber,
		ReferrerAgencyID:        c.ReferrerAgencyID,
		IsEmailVerified:         c.IsEmailVerified,
		IsMobileVerified:        c.IsMobileVerified,
		IsActive:                c.IsActive,
		CreatedAt:               c.CreatedAt,
		UpdatedAt:               c.UpdatedAt,
		EmailVerifiedAt:         c.EmailVerifiedAt,
		MobileVerifiedAt:        c.MobileVerifiedAt,
		LastLoginAt:             c.LastLoginAt,
	}
}

// GetCustomerDiscountsHistory returns discounts used by a customer across agencies
func (f *AdminCustomerManagementFlowImpl) GetCustomerDiscountsHistory(ctx context.Context, customerID uint) (*dto.AdminCustomerDiscountHistoryResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("GET_ADMIN_CUSTOMER_DISCOUNTS_HISTORY_FAILED", "Failed to get customer discounts history", err)
			logAdminAction(ctx, f.auditRepo, models.AuditActionAdminViewCustomerDiscounts, "Admin viewed customer discount history", false, &customerID, nil, err)
		}
	}()

	customer, err := f.customerRepo.ByID(ctx, customerID)
	if err != nil {
		return nil, err
	}
	if customer == nil {
		return nil, ErrCustomerNotFound
	}

	rows, err := f.transactionRepo.AggregateCustomerTransactionsByDiscounts(ctx, customerID, "share_desc")
	if err != nil {
		return nil, err
	}

	items := make([]dto.AdminCustomerDiscountHistoryItem, 0, len(rows))
	for _, v := range rows {
		items = append(items, dto.AdminCustomerDiscountHistoryItem{
			DiscountRate:       v.DiscountRate,
			CreatedAt:          v.CreatedAt,
			ExpiresAt:          v.ExpiresAt,
			TotalSent:          0, // TODO: integrate sent counts when available
			AgencyShareWithTax: v.AgencyShareWithTax,
		})
	}

	resp := &dto.AdminCustomerDiscountHistoryResponse{
		Message: "Customer discounts history retrieved successfully",
		Items:   items,
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminViewCustomerDiscounts, "Admin viewed customer discount history", true, &customerID, map[string]any{
		"items": len(items),
	}, nil)
	return resp, nil
}

// SetCustomerActiveStatus sets the is_active flag based on request body
func (f *AdminCustomerManagementFlowImpl) SetCustomerActiveStatus(ctx context.Context, req *dto.AdminSetCustomerActiveStatusRequest) (*dto.AdminSetCustomerActiveStatusResponse, error) {
	if req == nil || req.CustomerID == 0 {
		return nil, NewBusinessError("VALIDATION_ERROR", "Invalid request", nil)
	}
	customer, err := f.customerRepo.ByID(ctx, req.CustomerID)
	if err != nil {
		return nil, NewBusinessError("SET_CUSTOMER_ACTIVE_STATUS_FAILED", "Failed to get customer", err)
	}
	if customer == nil {
		return nil, NewBusinessError("CUSTOMER_NOT_FOUND", "Customer not found", ErrCustomerNotFound)
	}
	prevActive := false
	if customer.IsActive != nil {
		prevActive = *customer.IsActive
	}
	if !req.IsActive {
		if isSystemOrTaxCustomer(customer) {
			return nil, NewBusinessError("FORBIDDEN_OPERATION", "System and Tax users cannot be deactivated", ErrAccountInactive)
		}
	}
	if customer.IsActive != nil && *customer.IsActive == req.IsActive {
		resp := &dto.AdminSetCustomerActiveStatusResponse{
			Message:  "No change required",
			IsActive: req.IsActive,
		}
		return resp, nil
	}
	if err := f.customerRepo.UpdateActiveStatus(ctx, req.CustomerID, req.IsActive); err != nil {
		logAdminAction(ctx, f.auditRepo, models.AuditActionAdminSetCustomerStatus, "Admin toggled customer active status", false, &req.CustomerID, map[string]any{
			"desired_active": req.IsActive,
			"previous":       prevActive,
		}, err)
		return nil, NewBusinessError("SET_CUSTOMER_ACTIVE_STATUS_FAILED", "Failed to update active status", err)
	}
	resp := &dto.AdminSetCustomerActiveStatusResponse{
		Message:  "Customer status updated successfully",
		IsActive: req.IsActive,
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminSetCustomerStatus, "Admin toggled customer active status", true, &req.CustomerID, map[string]any{
		"desired_active": req.IsActive,
		"previous":       prevActive,
		"changed":        true,
	}, nil)
	return resp, nil
}

func isSystemOrTaxCustomer(cust *models.Customer) bool {
	if cust == nil {
		return false
	}
	fullName := strings.TrimSpace(cust.RepresentativeFirstName + " " + cust.RepresentativeLastName)
	return strings.EqualFold(cust.Email, systemCustomerEmail) ||
		strings.EqualFold(cust.Email, taxCustomerEmail) ||
		strings.EqualFold(fullName, "System Account") ||
		strings.EqualFold(fullName, "Tax Collector")
}

func (f *AdminCustomerManagementFlowImpl) readCampaignPriceFactorsFromMetadata(ctx context.Context, campaignID uint) (*float64, *float64, error) {
	source := "campaign_update"
	operation := "reserve_budget"
	txs, err := f.transactionRepo.ByFilter(ctx, models.TransactionFilter{
		CampaignID: &campaignID,
		Source:     &source,
		Operation:  &operation,
	}, "id DESC", 1, 0)
	if err != nil {
		return nil, nil, err
	}
	if len(txs) == 0 || len(txs[0].Metadata) == 0 {
		return nil, nil, nil
	}

	var meta map[string]any
	if err := json.Unmarshal(txs[0].Metadata, &meta); err != nil {
		return nil, nil, nil
	}

	segmentPriceFactor := parseMetadataFloat(meta["segment_price_factor"])
	lineNumberPriceFactor := parseMetadataFloat(meta["line_number_price_factor"])
	return segmentPriceFactor, lineNumberPriceFactor, nil
}
