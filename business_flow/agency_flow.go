// Package businessflow contains the core business logic and use cases for agency
package businessflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AgencyFlow defines operations for agency-level reporting
type AgencyFlow interface {
	CreateAgencyDiscount(
		ctx context.Context,
		req *dto.CreateAgencyDiscountRequest,
		metadata *ClientMetadata,
	) (*dto.CreateAgencyDiscountResponse, error)
	GetAgencyCustomerReport(
		ctx context.Context,
		req *dto.AgencyCustomerReportRequest,
		metadata *ClientMetadata,
	) (*dto.AgencyCustomerReportResponse, error)
	ListAgencyActiveDiscounts(
		ctx context.Context,
		req *dto.ListAgencyActiveDiscountsRequest,
		metadata *ClientMetadata,
	) (*dto.ListAgencyActiveDiscountsResponse, error)
	ListAgencyCustomerDiscounts(
		ctx context.Context,
		req *dto.ListAgencyCustomerDiscountsRequest,
		metadata *ClientMetadata,
	) (*dto.ListAgencyCustomerDiscountsResponse, error)
	ListAgencyCustomers(
		ctx context.Context,
		req *dto.ListAgencyCustomersRequest,
		metadata *ClientMetadata,
	) (*dto.ListAgencyCustomersResponse, error)
}

// AgencyFlowImpl implements AgencyFlow
type AgencyFlowImpl struct {
	customerRepo       repository.CustomerRepository
	campaignRepo       repository.CampaignRepository
	agencyDiscountRepo repository.AgencyDiscountRepository
	transactionRepo    repository.TransactionRepository
	auditRepo          repository.AuditLogRepository
	db                 *gorm.DB
}

// NewAgencyFlow constructs an AgencyFlow
func NewAgencyFlow(
	customerRepo repository.CustomerRepository,
	campaignRepo repository.CampaignRepository,
	agencyDiscountRepo repository.AgencyDiscountRepository,
	transactionRepo repository.TransactionRepository,
	auditRepo repository.AuditLogRepository,
	db *gorm.DB,
) AgencyFlow {
	return &AgencyFlowImpl{
		customerRepo:       customerRepo,
		campaignRepo:       campaignRepo,
		agencyDiscountRepo: agencyDiscountRepo,
		transactionRepo:    transactionRepo,
		auditRepo:          auditRepo,
		db:                 db,
	}
}

// CreateAgencyDiscount creates a new discount for a customer by an agency and expires old active ones
func (a *AgencyFlowImpl) CreateAgencyDiscount(
	ctx context.Context,
	req *dto.CreateAgencyDiscountRequest,
	metadata *ClientMetadata,
) (*dto.CreateAgencyDiscountResponse, error) {
	_, err := getAgency(ctx, a.customerRepo, req.AgencyID)
	if err != nil {
		return nil, err
	}

	customer, err := getCustomer(ctx, a.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	if req.DiscountRate < 0 || req.DiscountRate > 0.5 {
		return nil, NewBusinessError(
			"CREATE_AGENCY_DISCOUNT_VALIDATION_FAILED",
			"Rate must be between 0 and 0.5",
			ErrDiscountRateOutOfRange,
		)
	}

	if customer.ID == req.AgencyID {
		return nil, NewBusinessError(
			"CREATE_AGENCY_DISCOUNT_VALIDATION_FAILED",
			"Agency cannot create discount for itself",
			ErrAgencyCannotCreateDiscountForItself,
		)
	}

	if customer.ReferrerAgencyID == nil {
		return nil, NewBusinessError(
			"CREATE_AGENCY_DISCOUNT_VALIDATION_FAILED",
			"Customer is not under any agency",
			ErrCustomerNotUnderAgency,
		)
	}

	if *customer.ReferrerAgencyID != req.AgencyID {
		return nil, NewBusinessError(
			"CREATE_AGENCY_DISCOUNT_VALIDATION_FAILED",
			"Customer is not under this agency",
			ErrCustomerNotUnderAgency,
		)
	}

	var resp *dto.CreateAgencyDiscountResponse
	err = repository.WithTransaction(ctx, a.db, func(txCtx context.Context) error {
		// expire old active discounts
		if err := a.agencyDiscountRepo.ExpireActiveByAgencyAndCustomer(txCtx, req.AgencyID, customer.ID, utils.UTCNow()); err != nil {
			return err
		}

		// create new discount
		meta := map[string]any{
			"source":     "api",
			"created_by": "agency",
		}
		metaJSON, _ := json.Marshal(meta)
		row := &models.AgencyDiscount{
			UUID:         uuid.New(),
			AgencyID:     req.AgencyID,
			CustomerID:   customer.ID,
			DiscountRate: req.DiscountRate,
			ExpiresAt:    nil,
			Reason:       utils.ToPtr("Created by agency"),
			Metadata:     metaJSON,
			CreatedAt:    utils.UTCNow(),
			UpdatedAt:    utils.UTCNow(),
		}
		if err := a.agencyDiscountRepo.Save(txCtx, row); err != nil {
			return err
		}

		resp = &dto.CreateAgencyDiscountResponse{
			Message:      "Discount created successfully",
			DiscountRate: row.DiscountRate,
		}
		return nil
	})

	if err != nil {
		errMsg := fmt.Sprintf("Create agency discount failed for customer %d: %s", customer.ID, err.Error())
		_ = createAuditLog(ctx, a.auditRepo, &customer, models.AuditActionCreateDiscountByAgencyFailed, errMsg, false, &errMsg, metadata)

		return nil, NewBusinessError("CREATE_AGENCY_DISCOUNT_FAILED", "Failed to create discount", err)
	}

	// Create success audit log
	msg := fmt.Sprintf("Agency discount created for customer %d", customer.ID)
	_ = createAuditLog(ctx, a.auditRepo, &customer, models.AuditActionCreateDiscountByAgencyCompleted, msg, true, nil, metadata)

	return resp, nil
}

// GetAgencyCustomerReport retrieves aggregated stats of campaigns created by customers of an agency per customer
// Supports pagination, sorting, and filtering by start/end date, name, and company name
func (a *AgencyFlowImpl) GetAgencyCustomerReport(
	ctx context.Context,
	req *dto.AgencyCustomerReportRequest,
	metadata *ClientMetadata,
) (*dto.AgencyCustomerReportResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("GET_AGENCY_CUSTOMER_REPORT_FAILED", "Get agency customer report failed", err)
		}
	}()

	_, err = getAgency(ctx, a.customerRepo, req.AgencyID)
	if err != nil {
		return nil, err
	}

	nameLike := ""
	if req.Filter.Name != nil && *req.Filter.Name != "" {
		nameLike = *req.Filter.Name
	}

	var startDate *time.Time
	if req.Filter.StartDate != nil && *req.Filter.StartDate != "" {
		t, err := time.Parse(time.RFC3339, *req.Filter.StartDate)
		if err != nil {
			return nil, err
		}
		startDate = &t
	}

	var endDate *time.Time
	if req.Filter.EndDate != nil && *req.Filter.EndDate != "" {
		t, err := time.Parse(time.RFC3339, *req.Filter.EndDate)
		if err != nil {
			return nil, err
		}
		endDate = &t
	}

	rows, err := a.transactionRepo.AggregateAgencyTransactionsByCustomers(ctx, req.AgencyID, nameLike, startDate, endDate, req.OrderBy)
	if err != nil {
		return nil, err
	}

	customerIDs := make([]uint, 0, len(rows))
	seen := make(map[uint]struct{})
	for _, row := range rows {
		if _, ok := seen[row.CustomerID]; ok {
			continue
		}
		seen[row.CustomerID] = struct{}{}
		customerIDs = append(customerIDs, row.CustomerID)
	}

	totalSentByCustomer, err := a.campaignRepo.AggregateTotalSentByCustomerIDs(ctx, customerIDs)
	if err != nil {
		return nil, err
	}

	totalShare := uint64(0)
	totalSent := uint64(0)
	for _, item := range rows {
		totalShare += item.AgencyShareWithTax
		totalSent += totalSentByCustomer[item.CustomerID]
	}

	items := make([]dto.AgencyCustomerReportItem, 0)
	for _, item := range rows {
		customerTotalSent := totalSentByCustomer[item.CustomerID]
		items = append(items, dto.AgencyCustomerReportItem{
			CustomerID:              item.CustomerID,
			RepresentativeFirstName: item.RepresentativeFirstName,
			RepresentativeLastName:  item.RepresentativeLastName,
			CompanyName:             item.CompanyName,
			TotalSent:               customerTotalSent,
			TotalAgencyShareWithTax: item.AgencyShareWithTax,
		})
	}

	return &dto.AgencyCustomerReportResponse{
		Message:                    "Agency customer report retrieved successfully",
		Items:                      items,
		SumTotalAgencyShareWithTax: totalShare,
		SumTotalSent:               totalSent,
	}, nil
}

// ListAgencyActiveDiscounts returns the last non-expired active discount per customer for an agency
func (a *AgencyFlowImpl) ListAgencyActiveDiscounts(
	ctx context.Context,
	req *dto.ListAgencyActiveDiscountsRequest,
	metadata *ClientMetadata,
) (*dto.ListAgencyActiveDiscountsResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_AGENCY_ACTIVE_DISCOUNTS_FAILED", "List agency active discounts failed", err)
		}
	}()

	_, err = getAgency(ctx, a.customerRepo, req.AgencyID)
	if err != nil {
		return nil, err
	}

	nameLike := ""
	if req.Filter.Name != nil && *req.Filter.Name != "" {
		nameLike = *req.Filter.Name
	}

	rows, err := a.agencyDiscountRepo.ListActiveDiscountsWithCustomer(ctx, req.AgencyID, nameLike, "created_desc")
	if err != nil {
		return nil, err
	}

	items := make([]dto.AgencyActiveDiscountItem, 0, len(rows))
	for _, v := range rows {
		items = append(items, dto.AgencyActiveDiscountItem{
			CustomerID:              v.CustomerID,
			RepresentativeFirstName: v.RepresentativeFirstName,
			RepresentativeLastName:  v.RepresentativeLastName,
			CompanyName:             v.CompanyName,
			DiscountRate:            v.DiscountRate,
			CreatedAt:               v.CreatedAt,
		})
	}

	return &dto.ListAgencyActiveDiscountsResponse{
		Message: "Active discounts retrieved successfully",
		Items:   items,
	}, nil
}

// ListAgencyCustomerDiscounts returns the discounts of a specific customer under an agency
func (a *AgencyFlowImpl) ListAgencyCustomerDiscounts(
	ctx context.Context,
	req *dto.ListAgencyCustomerDiscountsRequest,
	metadata *ClientMetadata,
) (*dto.ListAgencyCustomerDiscountsResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_AGENCY_CUSTOMER_DISCOUNTS_FAILED", "List agency customer discounts failed", err)
		}
	}()

	customer, err := getCustomer(ctx, a.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	_, err = getAgency(ctx, a.customerRepo, req.AgencyID)
	if err != nil {
		return nil, err
	}

	if customer.ID == req.AgencyID {
		return nil, ErrAgencyCannotListDiscountsForItself
	}

	if customer.ReferrerAgencyID == nil {
		return nil, ErrCustomerNotUnderAgency
	}

	if *customer.ReferrerAgencyID != req.AgencyID {
		return nil, ErrCustomerNotUnderAgency
	}

	agencyID := req.AgencyID
	customerID := req.CustomerID
	rows, err := a.transactionRepo.AggregateAgencyTransactionsByDiscounts(ctx, agencyID, customerID, "share_desc")
	if err != nil {
		return nil, err
	}

	totalSentByCustomer, err := a.campaignRepo.AggregateTotalSentByCustomerIDs(ctx, []uint{customerID})
	if err != nil {
		return nil, err
	}

	customerTotalSent := totalSentByCustomer[customerID]

	items := make([]dto.AgencyCustomerDiscountItem, 0, len(rows))
	for _, v := range rows {
		items = append(items, dto.AgencyCustomerDiscountItem{
			DiscountRate:       v.DiscountRate,
			CreatedAt:          v.CreatedAt,
			ExpiresAt:          v.ExpiresAt,
			TotalSent:          customerTotalSent, // TODO:
			AgencyShareWithTax: v.AgencyShareWithTax,
		})
	}

	return &dto.ListAgencyCustomerDiscountsResponse{
		Message: "Customer discounts retrieved successfully",
		Items:   items,
	}, nil
}

// ListAgencyCustomers returns all active customers referred by the agency
func (a *AgencyFlowImpl) ListAgencyCustomers(
	ctx context.Context,
	req *dto.ListAgencyCustomersRequest,
	metadata *ClientMetadata,
) (*dto.ListAgencyCustomersResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_AGENCY_CUSTOMERS_FAILED", "List agency customers failed", err)
		}
	}()

	_, err = getAgency(ctx, a.customerRepo, req.AgencyID)
	if err != nil {
		return nil, err
	}

	rows, err := a.customerRepo.ListByAgency(ctx, req.AgencyID)
	if err != nil {
		return nil, err
	}

	items := make([]dto.AgencyCustomerItem, 0, len(rows))
	for _, c := range rows {
		items = append(items, dto.AgencyCustomerItem{
			CustomerID:              c.ID,
			RepresentativeFirstName: c.RepresentativeFirstName,
			RepresentativeLastName:  c.RepresentativeLastName,
			CompanyName:             c.CompanyName,
			CreatedAt:               c.CreatedAt,
			IsActive:                c.IsActive,
		})
	}

	return &dto.ListAgencyCustomersResponse{Message: "Agency customers retrieved successfully", Items: items}, nil
}
