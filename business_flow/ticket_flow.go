package businessflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
)

// TicketFlow defines operations for creating and listing tickets
type TicketFlow interface {
	CreateTicket(ctx context.Context, req *dto.CreateTicketRequest, metadata *ClientMetadata) (*dto.CreateTicketResponse, error)
	CreateResponseTicket(
		ctx context.Context,
		req *dto.CreateResponseTicketRequest,
		metadata *ClientMetadata,
	) (*dto.CreateResponseTicketResponse, error)
	ListTickets(ctx context.Context, req *dto.ListTicketsRequest, metadata *ClientMetadata) (*dto.ListTicketsResponse, error)
	AdminCreateResponseTicket(
		ctx context.Context,
		req *dto.AdminCreateResponseTicketRequest,
		metadata *ClientMetadata,
	) (*dto.AdminCreateResponseTicketResponse, error)
	AdminListTickets(ctx context.Context, req *dto.AdminListTicketsRequest, metadata *ClientMetadata) (*dto.AdminListTicketsResponse, error)
	DownloadTicketAttachment(ctx context.Context, customerID, ticketID uint, fileIndex int) (string, string, []byte, error)
}

// TicketFlowImpl implements TicketFlow
type TicketFlowImpl struct {
	customerRepo repository.CustomerRepository
	ticketRepo   repository.TicketRepository
	notifier     services.NotificationService
	adminCfg     config.AdminConfig
}

func NewTicketFlow(
	customerRepo repository.CustomerRepository,
	ticketRepo repository.TicketRepository,
	notifier services.NotificationService,
	adminCfg config.AdminConfig,
) TicketFlow {
	return &TicketFlowImpl{customerRepo: customerRepo, ticketRepo: ticketRepo, notifier: notifier, adminCfg: adminCfg}
}

const (
	maxTitleLen   = 80
	maxContentLen = 1000
	maxFileSize   = int64(10 * 1024 * 1024) // 10MB
)

var allowedExts = map[string]struct{}{
	".jpg":  {},
	".png":  {},
	".pdf":  {},
	".docx": {},
	".xlsx": {},
	".zip":  {},
}

func (f *TicketFlowImpl) CreateTicket(
	ctx context.Context,
	req *dto.CreateTicketRequest,
	metadata *ClientMetadata,
) (*dto.CreateTicketResponse, error) {
	// Validate customer
	customer, err := getCustomer(ctx, f.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Validate fields
	if strings.TrimSpace(req.Title) == "" || len([]rune(req.Title)) > maxTitleLen {
		return nil, NewBusinessError("INVALID_TITLE", fmt.Sprintf("title is required and must be <= %d chars", maxTitleLen), nil)
	}
	if strings.TrimSpace(req.Content) == "" || len([]rune(req.Content)) > maxContentLen {
		return nil, NewBusinessError("INVALID_CONTENT", fmt.Sprintf("content is required and must be <= %d chars", maxContentLen), nil)
	}

	files := []string{}
	if req.SavedFilePath != nil {
		files = append(files, *req.SavedFilePath)
	} else if req.AttachedFileURL != nil {
		if req.AttachedFileSize != nil && *req.AttachedFileSize > maxFileSize {
			return nil, NewBusinessError("FILE_TOO_LARGE", "attached file size exceeds 10MB", nil)
		}
		storedPath, err := f.saveFileToDisk(ctx, *req.AttachedFileURL, req.AttachedFileName)
		if err != nil {
			return nil, err
		}
		files = append(files, storedPath)
	}

	// Create ticket model
	t := models.Ticket{
		UUID:          uuid.New(),
		CorrelationID: uuid.New(),
		CustomerID:    customer.ID,
		Title:         req.Title,
		Content:       req.Content,
		Files:         files,
	}

	// Save
	if err = f.ticketRepo.Save(ctx, &t); err != nil {
		return nil, err
	}

	// Notify admins via SMS (best-effort)
	if f.notifier != nil {
		msg := fmt.Sprintf("New ticket: %s (customer %d)", truncate(req.Title, 50), customer.ID)
		for _, mobile := range f.adminCfg.ActiveMobiles() {
			_ = f.notifier.SendSMS(ctx, mobile, msg, nil)
		}
	}

	return &dto.CreateTicketResponse{
		Message:       "Ticket created successfully",
		ID:            t.ID,
		UUID:          t.UUID.String(),
		CorrelationID: t.CorrelationID.String(),
		CreatedAt:     t.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *TicketFlowImpl) CreateResponseTicket(
	ctx context.Context,
	req *dto.CreateResponseTicketRequest,
	metadata *ClientMetadata,
) (*dto.CreateResponseTicketResponse, error) {
	// Validate customer
	customer, err := getCustomer(ctx, f.customerRepo, req.CustomerID)
	if err != nil {
		return nil, err
	}

	// Fetch original ticket
	orig, err := f.ticketRepo.ByID(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}
	if orig == nil {
		return nil, ErrTicketNotFound
	}

	// Ensure the ticket belongs to the customer
	if orig.CustomerID != customer.ID {
		return nil, NewBusinessError("FORBIDDEN", "You can only respond to your own tickets", nil)
	}

	// Validate fields
	if strings.TrimSpace(req.Content) == "" || len([]rune(req.Content)) > maxContentLen {
		return nil, NewBusinessError("INVALID_CONTENT", fmt.Sprintf("content is required and must be <= %d chars", maxContentLen), nil)
	}

	files := []string{}
	if req.SavedFilePath != nil {
		files = append(files, *req.SavedFilePath)
	} else if req.AttachedFileURL != nil {
		if req.AttachedFileSize != nil && *req.AttachedFileSize > maxFileSize {
			return nil, NewBusinessError("FILE_TOO_LARGE", "attached file size exceeds 10MB", nil)
		}
		storedPath, err := f.saveFileToDisk(ctx, *req.AttachedFileURL, req.AttachedFileName)
		if err != nil {
			return nil, err
		}
		files = append(files, storedPath)
	}

	// Mark as not replied by admin (customer response)
	repliedByAdmin := false
	reply := models.Ticket{
		UUID:           uuid.New(),
		CorrelationID:  orig.CorrelationID,
		CustomerID:     customer.ID,
		Title:          orig.Title,
		Content:        req.Content,
		Files:          files,
		RepliedByAdmin: &repliedByAdmin,
	}

	if err := f.ticketRepo.Save(ctx, &reply); err != nil {
		return nil, err
	}

	// Send SMS notification to admins
	if f.notifier != nil {
		// translate to en
		msg := fmt.Sprintf("New response to ticket %d from customer %s %s\nTitle: %s\nContent: %s",
			orig.ID,
			customer.RepresentativeFirstName,
			customer.RepresentativeLastName,
			truncate(orig.Title, 30),
			truncate(req.Content, 50),
		)
		go func() {
			for _, mobile := range f.adminCfg.ActiveMobiles() {
				_ = f.notifier.SendSMS(context.Background(), mobile, msg, nil)
			}
		}()
	}

	return &dto.CreateResponseTicketResponse{
		Message:       "Response ticket created successfully",
		ID:            reply.ID,
		UUID:          reply.UUID.String(),
		CorrelationID: reply.CorrelationID.String(),
		CreatedAt:     reply.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *TicketFlowImpl) ListTickets(
	ctx context.Context,
	req *dto.ListTicketsRequest,
	metadata *ClientMetadata,
) (*dto.ListTicketsResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("LIST_TICKETS_FAILED", "Failed to list tickets", err)
		}
	}()

	// Validate customer exists
	if _, err = getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, err
	}

	filter := models.TicketFilter{CustomerID: &req.CustomerID}
	if req.Title != nil {
		trim := strings.TrimSpace(*req.Title)
		if trim != "" {
			filter.Title = &trim
		}
	}
	if req.StartDate != nil {
		filter.CreatedAfter = req.StartDate
	}
	if req.EndDate != nil {
		filter.CreatedBefore = req.EndDate
	}
	if req.StartDate != nil && req.EndDate != nil && req.StartDate.After(*req.EndDate) {
		return nil, ErrStartDateAfterEndDate
	}

	page := req.Page
	if page == 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize == 0 || pageSize > 100 {
		pageSize = 20
	}
	offset := int((page - 1) * pageSize)

	rows, err := f.ticketRepo.ByFilter(ctx, filter, "correlation_id ASC, id DESC", int(pageSize), offset)
	if err != nil {
		return nil, err
	}

	groupsMap := make(map[string][]dto.TicketItem)
	order := make([]string, 0)
	for _, r := range rows {
		cid := r.CorrelationID.String()
		if _, ok := groupsMap[cid]; !ok {
			groupsMap[cid] = []dto.TicketItem{}
			order = append(order, cid)
		}
		item := dto.TicketItem{
			ID:             r.ID,
			Title:          r.Title,
			Content:        r.Content,
			RepliedByAdmin: r.RepliedByAdmin,
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		}
		if len(r.Files) > 0 {
			item.Attachments = make([]dto.TicketAttachment, 0, len(r.Files))
			for idx, fpath := range r.Files {
				item.Attachments = append(item.Attachments, dto.TicketAttachment{
					Index: idx,
					Name:  filepath.Base(fpath),
				})
			}
		}
		groupsMap[cid] = append(groupsMap[cid], item)
	}

	groups := make([]dto.TicketGroup, 0, len(order))
	for _, cid := range order {
		groups = append(groups, dto.TicketGroup{CorrelationID: cid, Items: groupsMap[cid]})
	}

	return &dto.ListTicketsResponse{
		Message: "Tickets retrieved successfully",
		Groups:  groups,
	}, nil
}

func (f *TicketFlowImpl) AdminCreateResponseTicket(
	ctx context.Context,
	req *dto.AdminCreateResponseTicketRequest,
	metadata *ClientMetadata,
) (*dto.AdminCreateResponseTicketResponse, error) {
	// Fetch original ticket
	orig, err := f.ticketRepo.ByID(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}
	if orig == nil {
		return nil, ErrTicketNotFound
	}

	// Validate fields
	if strings.TrimSpace(req.Content) == "" || len([]rune(req.Content)) > maxContentLen {
		return nil, NewBusinessError("INVALID_CONTENT", fmt.Sprintf("content is required and must be <= %d chars", maxContentLen), nil)
	}

	files := []string{}
	if req.SavedFilePath != nil {
		files = append(files, *req.SavedFilePath)
	} else if req.AttachedFileURL != nil {
		if req.AttachedFileSize != nil && *req.AttachedFileSize > maxFileSize {
			return nil, NewBusinessError("FILE_TOO_LARGE", "attached file size exceeds 10MB", nil)
		}
		storedPath, err := f.saveFileToDisk(ctx, *req.AttachedFileURL, req.AttachedFileName)
		if err != nil {
			return nil, err
		}
		files = append(files, storedPath)
	}

	replied := true
	reply := models.Ticket{
		UUID:           uuid.New(),
		CorrelationID:  orig.CorrelationID,
		CustomerID:     orig.CustomerID,
		Title:          orig.Title,
		Content:        req.Content,
		Files:          files,
		RepliedByAdmin: &replied,
	}

	if err := f.ticketRepo.Save(ctx, &reply); err != nil {
		return nil, err
	}

	return &dto.AdminCreateResponseTicketResponse{
		Message:       "Admin response created successfully",
		ID:            reply.ID,
		UUID:          reply.UUID.String(),
		CorrelationID: reply.CorrelationID.String(),
		CreatedAt:     reply.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (f *TicketFlowImpl) AdminListTickets(
	ctx context.Context,
	req *dto.AdminListTicketsRequest,
	metadata *ClientMetadata,
) (*dto.AdminListTicketsResponse, error) {
	var err error
	defer func() {
		if err != nil {
			err = NewBusinessError("ADMIN_LIST_TICKETS_FAILED", "Failed to list tickets", err)
		}
	}()

	filter := models.TicketFilter{}
	if req.CustomerID != nil {
		filter.CustomerID = req.CustomerID
	}
	if req.Title != nil {
		trim := strings.TrimSpace(*req.Title)
		if trim != "" {
			filter.Title = &trim
		}
	}
	if req.StartDate != nil {
		filter.CreatedAfter = req.StartDate
	}
	if req.EndDate != nil {
		filter.CreatedBefore = req.EndDate
	}
	if req.StartDate != nil && req.EndDate != nil && req.StartDate.After(*req.EndDate) {
		return nil, ErrStartDateAfterEndDate
	}
	if req.RepliedByAdmin != nil {
		filter.RepliedByAdmin = req.RepliedByAdmin
	}

	page := req.Page
	if page == 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize == 0 || pageSize > 100 {
		pageSize = 20
	}
	offset := int((page - 1) * pageSize)

	rows, err := f.ticketRepo.ByFilter(ctx, filter, "correlation_id ASC, id DESC", int(pageSize), offset)
	if err != nil {
		return nil, err
	}

	// collect customer IDs to fetch details in batch
	custIDs := make(map[uint]struct{}, len(rows))
	for _, r := range rows {
		custIDs[r.CustomerID] = struct{}{}
	}
	// load customers by IDs
	customers := make(map[uint]*models.Customer, len(custIDs))
	if len(custIDs) > 0 {
		// Since repository does not have batch-by-ids directly, loop with small batches
		ids := make([]uint, 0, len(custIDs))
		for id := range custIDs {
			ids = append(ids, id)
		}

		res, err := f.customerRepo.FindByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, c := range res {
			customers[c.ID] = c
		}
	}

	groupsMap := make(map[string][]dto.TicketItem)
	order := make([]string, 0)
	for _, r := range rows {
		cid := r.CorrelationID.String()
		if _, ok := groupsMap[cid]; !ok {
			groupsMap[cid] = []dto.TicketItem{}
			order = append(order, cid)
		}
		item := dto.TicketItem{
			ID:             r.ID,
			Title:          r.Title,
			Content:        r.Content,
			RepliedByAdmin: r.RepliedByAdmin,
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		}
		if len(r.Files) > 0 {
			item.Attachments = make([]dto.TicketAttachment, 0, len(r.Files))
			for idx, fpath := range r.Files {
				item.Attachments = append(item.Attachments, dto.TicketAttachment{
					Index: idx,
					Name:  filepath.Base(fpath),
				})
			}
		}
		if c := customers[r.CustomerID]; c != nil {
			fn := c.RepresentativeFirstName
			ln := c.RepresentativeLastName
			agencyName := (*string)(nil)
			if c.ReferrerAgency != nil && c.ReferrerAgency.CompanyName != nil {
				agencyName = c.ReferrerAgency.CompanyName
			}
			item.CustomerFirstName = &fn
			item.CustomerLastName = &ln
			item.CompanyName = c.CompanyName
			mobile := c.RepresentativeMobile
			item.PhoneNumber = &mobile
			item.AgencyName = agencyName
		}
		groupsMap[cid] = append(groupsMap[cid], item)
	}

	groups := make([]dto.TicketGroup, 0, len(order))
	for _, cid := range order {
		groups = append(groups, dto.TicketGroup{CorrelationID: cid, Items: groupsMap[cid]})
	}

	return &dto.AdminListTicketsResponse{
		Message: "Admin tickets retrieved successfully",
		Groups:  groups,
	}, nil
}

// DownloadTicketAttachment returns a ticket attachment for the owning customer.
func (f *TicketFlowImpl) DownloadTicketAttachment(
	ctx context.Context,
	customerID, ticketID uint,
	fileIndex int,
) (string, string, []byte, error) {
	if fileIndex < 0 {
		return "", "", nil, NewBusinessError("ATTACHMENT_NOT_FOUND", "attachment not found", nil)
	}

	// Validate customer
	customer, err := getCustomer(ctx, f.customerRepo, customerID)
	if err != nil {
		return "", "", nil, err
	}

	// Fetch ticket
	ticket, err := f.ticketRepo.ByID(ctx, ticketID)
	if err != nil {
		return "", "", nil, err
	}
	if ticket == nil {
		return "", "", nil, ErrTicketNotFound
	}
	if ticket.CustomerID != customer.ID {
		return "", "", nil, NewBusinessError("FORBIDDEN", "You can only download your own ticket attachments", nil)
	}

	if len(ticket.Files) == 0 || fileIndex >= len(ticket.Files) {
		return "", "", nil, NewBusinessError("ATTACHMENT_NOT_FOUND", "attachment not found", nil)
	}

	cleanPath, err := sanitizeTicketAttachmentPath(ticket.Files[fileIndex])
	if err != nil {
		return "", "", nil, err
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil, NewBusinessError("ATTACHMENT_NOT_FOUND", "attachment not found", err)
		}
		return "", "", nil, err
	}

	filename := filepath.Base(cleanPath)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return filename, contentType, data, nil
}

// saveFileToDisk downloads a file from a URL and stores it under data/uploads/tickets/YYYY-MM-DD/
// It enforces allowed extensions and a 10MB size limit and returns the relative stored path
func (f *TicketFlowImpl) saveFileToDisk(ctx context.Context, urlStr string, originalFilename *string) (string, error) {
	// Determine extension from original name or URL path
	ext := ""
	if originalFilename != nil {
		ext = strings.ToLower(filepath.Ext(*originalFilename))
	}
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(strings.Split(urlStr, "?")[0]))
	}
	if _, ok := allowedExts[ext]; !ok {
		return "", NewBusinessError("INVALID_FILE_TYPE", "allowed file types: jpg, png, pdf, docx, xlsx, zip", nil)
	}

	// Prepare directory
	dateDir := utils.UTCNow().Format("2006-01-02")
	baseDir := filepath.Join("data", "uploads", "tickets", dateDir)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	// Generate unique filename
	fname := uuid.New().String() + ext
	fullPath := filepath.Join(baseDir, fname)

	// Download with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", NewBusinessError(
			"FILE_DOWNLOAD_FAILED",
			fmt.Sprintf("failed to download file: status %d", resp.StatusCode),
			errors.New(resp.Status),
		)
	}

	// Limit to maxFileSize
	lr := &io.LimitedReader{R: resp.Body, N: maxFileSize + 1}
	out, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	written, err := io.Copy(out, lr)
	if err != nil {
		return "", err
	}
	if written > maxFileSize || lr.N == 0 {
		_ = os.Remove(fullPath)
		return "", NewBusinessError("FILE_TOO_LARGE", "attached file size exceeds 10MB", nil)
	}

	// Optionally sanity-check content-type
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if est := mime.TypeByExtension(ext); est != "" && !strings.HasPrefix(ct, strings.Split(est, ";")[0]) {
			// not fatal: allow mismatch but could log later
		}
	}

	// Return relative path (from data/)
	return filepath.ToSlash(filepath.Join("data", "uploads", "tickets", dateDir, fname)), nil
}

func sanitizeTicketAttachmentPath(storedPath string) (string, error) {
	cleaned := strings.TrimSpace(storedPath)
	if cleaned == "" {
		return "", NewBusinessError("ATTACHMENT_NOT_FOUND", "attachment path missing", nil)
	}
	cleaned = filepath.ToSlash(filepath.Clean(filepath.FromSlash(cleaned)))
	if filepath.IsAbs(cleaned) {
		return "", NewBusinessError("INVALID_ATTACHMENT_PATH", "invalid attachment path", nil)
	}

	base := filepath.ToSlash(filepath.Clean(filepath.Join("data", "uploads", "tickets")))
	if cleaned != base && !strings.HasPrefix(cleaned, base+"/") {
		return "", NewBusinessError("INVALID_ATTACHMENT_PATH", "invalid attachment path", nil)
	}

	return filepath.FromSlash(cleaned), nil
}

func truncate(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
