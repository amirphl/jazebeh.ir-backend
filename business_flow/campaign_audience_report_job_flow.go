package businessflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

var (
	errCampaignAudienceReportTooLarge       = errors.New("campaign audience report exceeds the row limit")
	errCampaignAudienceReportOutputTooLarge = errors.New("campaign audience report exceeds the output byte limit")
)

const (
	defaultCampaignAudienceReportStorageRoot              = "data/exports/campaign-audience-reports"
	defaultMaxAsyncCampaignAudienceReportRows       int64 = 10_000_000
	defaultMaxAsyncCampaignAudienceReportBytes      int64 = 5 << 30
	defaultMaxCampaignAudienceReportJobsPerCustomer int64 = 100
	campaignAudienceReportPageSize                        = 5_000
	campaignAudienceReportOrphanFileAge                   = 24 * time.Hour
)

type CampaignAudienceReportJobFlow interface {
	Create(ctx context.Context, campaignIDs []uint, metadata *ClientMetadata) (*dto.CampaignAudienceReportJobResponse, error)
	Get(ctx context.Context, id string) (*dto.CampaignAudienceReportJobResponse, *models.CampaignAudienceReportJob, error)
	RecordDownload(ctx context.Context, job *models.CampaignAudienceReportJob, metadata *ClientMetadata)
	ExecuteNext(ctx context.Context, lease time.Duration) error
	Cleanup(ctx context.Context) error
}

func (f *CampaignAudienceReportJobFlowImpl) auditEvent(ctx context.Context, customerID uint, action, description string, success bool, errorMessage *string, metadata *ClientMetadata) {
	if f.audit == nil {
		return
	}
	ipAddress, userAgent := "", ""
	if metadata != nil {
		ipAddress, userAgent = metadata.IPAddress, metadata.UserAgent
	}
	_ = f.audit.Save(ctx, &models.AuditLog{CustomerID: &customerID, Action: action, Description: &description, Success: utils.ToPtr(success), IPAddress: &ipAddress, UserAgent: &userAgent, ErrorMessage: errorMessage})
}

type CampaignAudienceReportJobFlowImpl struct {
	jobs               repository.CampaignAudienceReportJobRepository
	campaigns          repository.CampaignRepository
	customers          repository.CustomerRepository
	audit              repository.AuditLogRepository
	db                 *gorm.DB
	storageRoot        string
	maxRows            int64
	maxBytes           int64
	maxJobsPerCustomer int64
}

func NewCampaignAudienceReportJobFlow(j repository.CampaignAudienceReportJobRepository, c repository.CampaignRepository, customers repository.CustomerRepository, audit repository.AuditLogRepository, db *gorm.DB, root string, maxRows, maxBytes, maxJobsPerCustomer int64) *CampaignAudienceReportJobFlowImpl {
	root = strings.TrimSpace(root)
	if root == "" {
		root = defaultCampaignAudienceReportStorageRoot
	}
	if maxRows <= 0 {
		maxRows = defaultMaxAsyncCampaignAudienceReportRows
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxAsyncCampaignAudienceReportBytes
	}
	if maxJobsPerCustomer <= 0 {
		maxJobsPerCustomer = defaultMaxCampaignAudienceReportJobsPerCustomer
	}
	return &CampaignAudienceReportJobFlowImpl{jobs: j, campaigns: c, customers: customers, audit: audit, db: db, storageRoot: root, maxRows: maxRows, maxBytes: maxBytes, maxJobsPerCustomer: maxJobsPerCustomer}
}

func mapCampaignAudienceReportJob(j *models.CampaignAudienceReportJob) *dto.CampaignAudienceReportJobResponse {
	ids := make([]uint, len(j.CampaignIDs))
	for i, id := range j.CampaignIDs {
		ids[i] = uint(id)
	}
	return &dto.CampaignAudienceReportJobResponse{ID: j.ID, Status: j.Status, CampaignIDs: ids, RowCount: j.RowCount, SheetCount: j.SheetCount, ByteSize: j.ByteSize, ErrorCode: j.ErrorCode, ErrorMessage: j.ErrorMessage, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, CompletedAt: j.CompletedAt, ExpiresAt: j.ExpiresAt}
}
func reportCustomerID(ctx context.Context) (uint, error) {
	id, ok := ctx.Value(utils.CustomerIDKey).(uint)
	if !ok || id == 0 {
		return 0, NewBusinessError("MISSING_CUSTOMER_ID", "customer id is required", ErrCustomerNotFound)
	}
	return id, nil
}
func validateReportCampaignIDs(ids []uint) error {
	if len(ids) == 0 {
		return NewBusinessError("CAMPAIGN_IDS_REQUIRED", "at least one campaign id is required", nil)
	}
	if len(ids) > 100 {
		return NewBusinessError("CAMPAIGN_IDS_LIMIT_EXCEEDED", "at most 100 campaign ids may be exported at once", nil)
	}
	seen := map[uint]struct{}{}
	for _, id := range ids {
		if id == 0 {
			return NewBusinessError("CAMPAIGN_ID_INVALID", "campaign ids must be greater than zero", nil)
		}
		if _, ok := seen[id]; ok {
			return NewBusinessError("CAMPAIGN_IDS_DUPLICATE", "campaign ids must be unique", nil)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// ensureJobCampaignsAreStillOwned prevents a queued job from exporting a
// campaign that was transferred after the job was created.
func (f *CampaignAudienceReportJobFlowImpl) ensureJobCampaignsAreStillOwned(ctx context.Context, j *models.CampaignAudienceReportJob) error {
	ids := make([]uint, len(j.CampaignIDs))
	for i, id := range j.CampaignIDs {
		if id <= 0 || uint64(id) > uint64(^uint(0)) {
			return NewBusinessError("CAMPAIGN_REPORT_JOB_NOT_FOUND", "audience report job not found", nil)
		}
		ids[i] = uint(id)
	}
	campaigns, err := f.campaigns.ByCustomerIDAndIDs(ctx, j.CustomerID, ids)
	if err != nil {
		return err
	}
	if len(campaigns) != len(ids) {
		return NewBusinessError("CAMPAIGN_REPORT_JOB_NOT_FOUND", "audience report job not found", nil)
	}
	return nil
}

func (f *CampaignAudienceReportJobFlowImpl) Create(ctx context.Context, ids []uint, metadata *ClientMetadata) (*dto.CampaignAudienceReportJobResponse, error) {
	if err := validateReportCampaignIDs(ids); err != nil {
		return nil, err
	}
	customerID, err := reportCustomerID(ctx)
	if err != nil {
		return nil, err
	}
	campaigns, err := f.campaigns.ByCustomerIDAndIDs(ctx, customerID, ids)
	if err != nil {
		return nil, err
	}
	if len(campaigns) != len(ids) {
		return nil, NewBusinessError("CAMPAIGN_NOT_FOUND", "one or more campaigns were not found", ErrCampaignNotFound)
	}
	pids := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		pids[i] = int64(id)
	}
	j := &models.CampaignAudienceReportJob{ID: uuid.NewString(), CustomerID: customerID, CampaignIDs: pids, Status: models.CampaignAudienceReportJobPending}
	created, err := f.jobs.CreateWithinCustomerLimit(ctx, j, f.maxJobsPerCustomer)
	if err != nil {
		return nil, NewBusinessError("CAMPAIGN_REPORT_JOB_CREATE_FAILED", "failed to create audience report job", err)
	}
	if !created {
		return nil, NewBusinessError("CAMPAIGN_REPORT_JOB_LIMIT_EXCEEDED", "too many campaign audience report jobs are retained; try again after a job fails or expires", nil)
	}
	f.auditEvent(ctx, customerID, models.AuditActionCampaignAudienceClickReportExported, fmt.Sprintf("Campaign audience report job queued for %d campaigns", len(ids)), true, nil, metadata)
	return mapCampaignAudienceReportJob(j), nil
}
func (f *CampaignAudienceReportJobFlowImpl) Get(ctx context.Context, id string) (*dto.CampaignAudienceReportJobResponse, *models.CampaignAudienceReportJob, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil, NewBusinessError("CAMPAIGN_REPORT_JOB_NOT_FOUND", "audience report job not found", nil)
	}
	customerID, err := reportCustomerID(ctx)
	if err != nil {
		return nil, nil, err
	}
	j, err := f.jobs.ByIDForCustomer(ctx, id, customerID)
	if err != nil {
		return nil, nil, err
	}
	if j == nil {
		return nil, nil, NewBusinessError("CAMPAIGN_REPORT_JOB_NOT_FOUND", "audience report job not found", nil)
	}
	if err := f.ensureJobCampaignsAreStillOwned(ctx, j); err != nil {
		return nil, nil, err
	}
	return mapCampaignAudienceReportJob(j), j, nil
}

func (f *CampaignAudienceReportJobFlowImpl) RecordDownload(ctx context.Context, job *models.CampaignAudienceReportJob, metadata *ClientMetadata) {
	if job != nil {
		f.auditEvent(ctx, job.CustomerID, models.AuditActionCampaignAudienceClickReportExported, "Campaign audience report downloaded", true, nil, metadata)
	}
}

type campaignAudienceReportStageRow struct {
	JobID       string `gorm:"column:job_id"`
	LeaseToken  string `gorm:"column:lease_token"`
	CampaignID  uint   `gorm:"column:campaign_id"`
	AudienceUID string `gorm:"column:audience_uid"`
	ShortCode   string `gorm:"column:short_code"`
}

type reportJobPageRow struct {
	CampaignID   uint
	CampaignUUID string
	BundleID     *uint
	AudienceUID  string
	ShortCode    string
}

func (campaignAudienceReportStageRow) TableName() string { return "campaign_audience_report_job_rows" }

// appendStageRow keeps one row per UID in an INSERT batch. PostgreSQL rejects
// an ON CONFLICT DO UPDATE statement when the same conflict key occurs twice
// in that statement, while the durable JSONL input deliberately permits later
// records to replace an earlier UID-to-code mapping.
func appendStageRow(batch []campaignAudienceReportStageRow, indexes map[string]int, row campaignAudienceReportStageRow) []campaignAudienceReportStageRow {
	if index, ok := indexes[row.AudienceUID]; ok {
		batch[index].ShortCode = row.ShortCode
		return batch
	}
	indexes[row.AudienceUID] = len(batch)
	return append(batch, row)
}

// upsertStageBatch returns only newly-created staging rows. PostgreSQL's xmax
// is zero for INSERT results and nonzero for rows reached through ON CONFLICT
// DO UPDATE, letting the caller keep an exact row count without repeatedly
// scanning the complete staging table.
func (f *CampaignAudienceReportJobFlowImpl) upsertStageBatch(ctx context.Context, batch []campaignAudienceReportStageRow) (int64, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	campaignIDs := make(pq.Int64Array, len(batch))
	uids := make(pq.StringArray, len(batch))
	codes := make(pq.StringArray, len(batch))
	for i, row := range batch {
		campaignIDs[i] = int64(row.CampaignID)
		uids[i] = row.AudienceUID
		codes[i] = row.ShortCode
	}
	rows, err := f.db.WithContext(ctx).Raw(`
INSERT INTO campaign_audience_report_job_rows (job_id, lease_token, campaign_id, audience_uid, short_code)
SELECT ?::uuid, ?::uuid, input.campaign_id, input.audience_uid, input.short_code
FROM unnest(?::bigint[], ?::text[], ?::text[]) AS input(campaign_id, audience_uid, short_code)
ON CONFLICT (job_id, lease_token, campaign_id, audience_uid)
DO UPDATE SET short_code = EXCLUDED.short_code
RETURNING xmax = 0 AS inserted`, batch[0].JobID, batch[0].LeaseToken, campaignIDs, uids, codes).Rows()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var inserted int64
	for rows.Next() {
		var wasInserted bool
		if err := rows.Scan(&wasInserted); err != nil {
			return 0, err
		}
		if wasInserted {
			inserted++
		}
	}
	return inserted, rows.Err()
}

func (f *CampaignAudienceReportJobFlowImpl) stage(ctx context.Context, j *models.CampaignAudienceReportJob) error {
	if j.LeaseToken == nil || *j.LeaseToken == "" {
		return fmt.Errorf("report job is missing a lease token")
	}
	if err := f.ensureJobCampaignsAreStillOwned(ctx, j); err != nil {
		return err
	}
	var stagedRows int64
	if err := f.db.WithContext(ctx).Model(&campaignAudienceReportStageRow{}).Where("job_id=? AND lease_token=?", j.ID, *j.LeaseToken).Count(&stagedRows).Error; err != nil {
		return err
	}
	if stagedRows > f.maxRows {
		return fmt.Errorf("%w: %d rows", errCampaignAudienceReportTooLarge, f.maxRows)
	}
	for _, rawID := range j.CampaignIDs {
		id := uint(rawID)
		batch := make([]campaignAudienceReportStageRow, 0, 1000)
		batchIndexes := make(map[string]int, cap(batch))
		seen := false
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			inserted, err := f.upsertStageBatch(ctx, batch)
			stagedRows += inserted
			if err == nil && stagedRows > f.maxRows {
				err = fmt.Errorf("%w: %d rows", errCampaignAudienceReportTooLarge, f.maxRows)
			}
			batch = batch[:0]
			batchIndexes = make(map[string]int, cap(batch))
			return err
		}
		err := visitCampaignAudienceUIDs(id, func(record campaignAudienceUIDRecord) error {
			seen = true
			batch = appendStageRow(batch, batchIndexes, campaignAudienceReportStageRow{JobID: j.ID, LeaseToken: *j.LeaseToken, CampaignID: id, AudienceUID: record.UID, ShortCode: record.Code})
			if len(batch) == cap(batch) {
				return flush()
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !seen {
			return fmt.Errorf("campaign %d has no audience report data", id)
		}
		if err := flush(); err != nil {
			return err
		}
	}
	return nil
}

type limitedWriter struct {
	w      io.Writer
	n, max int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.n {
		return 0, fmt.Errorf("%w: %d bytes", errCampaignAudienceReportOutputTooLarge, w.max)
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}
func reportOutputPath(root, id, leaseToken string) string {
	return filepath.Join(root, id+"-"+leaseToken+".xlsx")
}
func (f *CampaignAudienceReportJobFlowImpl) write(ctx context.Context, j *models.CampaignAudienceReportJob) (int64, int, int64, string, error) {
	if j.LeaseToken == nil || *j.LeaseToken == "" {
		return 0, 0, 0, "", fmt.Errorf("report job is missing a lease token")
	}
	if err := os.MkdirAll(f.storageRoot, 0750); err != nil {
		return 0, 0, 0, "", err
	}
	// The lease token makes output attempt-specific. A recovered worker can
	// safely remove its own output after losing the lease without touching the
	// newer worker's file for the same job.
	final := reportOutputPath(f.storageRoot, j.ID, *j.LeaseToken)
	partial := final + ".partial"
	_ = os.Remove(partial)
	out, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return 0, 0, 0, "", err
	}
	defer func() { _ = out.Close() }()
	xl := excelize.NewFile()
	defer func() { _ = xl.Close() }()
	sheetNo, rowNo := 0, 0
	var stream *excelize.StreamWriter
	newSheet := func() error {
		sheetNo++
		name := fmt.Sprintf("Campaign Audience Report %d", sheetNo)
		if sheetNo == 1 {
			xl.SetSheetName(xl.GetSheetName(0), name)
		} else {
			xl.NewSheet(name)
		}
		var e error
		stream, e = xl.NewStreamWriter(name)
		if e != nil {
			return e
		}
		if e = stream.SetColWidth(1, 6, 24); e != nil {
			return e
		}
		rowNo = 0
		return stream.SetRow("A1", []interface{}{"Campaign ID", "Campaign UUID", "Audience Profile UID", "Status", "Clicked", "Action"})
	}
	if err := newSheet(); err != nil {
		return 0, 0, 0, "", err
	}
	var afterCampaign uint
	afterUID := ""
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, 0, "", err
		}
		var rows []reportJobPageRow
		q := f.db.WithContext(ctx).Raw(`SELECT r.campaign_id, c.uuid AS campaign_uuid, c.bundle_id, r.audience_uid, r.short_code FROM campaign_audience_report_job_rows r JOIN campaigns c ON c.id=r.campaign_id AND c.customer_id=? WHERE r.job_id=? AND r.lease_token=? AND (r.campaign_id>? OR (r.campaign_id=? AND r.audience_uid>?)) ORDER BY r.campaign_id,r.audience_uid LIMIT ?`, j.CustomerID, j.ID, *j.LeaseToken, afterCampaign, afterCampaign, afterUID, campaignAudienceReportPageSize)
		if err := q.Scan(&rows).Error; err != nil {
			return 0, 0, 0, "", err
		}
		if len(rows) == 0 {
			break
		}
		clicked, actions, err := f.liveStates(ctx, rows)
		if err != nil {
			return 0, 0, 0, "", err
		}
		for _, r := range rows {
			if rowNo >= maxExcelWorksheetDataRows {
				if err := stream.Flush(); err != nil {
					return 0, 0, 0, "", err
				}
				if err := newSheet(); err != nil {
					return 0, 0, 0, "", err
				}
			}
			record := []interface{}{fmt.Sprint(r.CampaignID), excelSafeString(r.CampaignUUID), excelSafeString(r.AudienceUID), "unknown", strconv.FormatBool(clicked[r.CampaignID][r.ShortCode]), actionReportValue(actions[r.CampaignID][r.AudienceUID])}
			if err := stream.SetRow(fmt.Sprintf("A%d", rowNo+2), record); err != nil {
				return 0, 0, 0, "", err
			}
			rowNo++
			total++
		}
		last := rows[len(rows)-1]
		afterCampaign, afterUID = last.CampaignID, last.AudienceUID
	}
	if err := stream.Flush(); err != nil {
		return 0, 0, 0, "", err
	}
	lw := &limitedWriter{w: out, max: f.maxBytes}
	if _, err := xl.WriteTo(lw); err != nil {
		return 0, 0, 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, 0, 0, "", err
	}
	if err := os.Rename(partial, final); err != nil {
		return 0, 0, 0, "", err
	}
	return total, sheetNo, lw.n, final, nil
}
func (f *CampaignAudienceReportJobFlowImpl) liveStates(ctx context.Context, rows []reportJobPageRow) (map[uint]map[string]bool, map[uint]map[string]bool, error) {
	clicked, actions := make(map[uint]map[string]bool), make(map[uint]map[string]bool)
	groups := make(map[uint][]reportJobPageRow)
	for _, row := range rows {
		groups[row.CampaignID] = append(groups[row.CampaignID], row)
	}
	for campaignID, group := range groups {
		codes, uids := make([]string, 0, len(group)), make([]string, 0, len(group))
		for _, row := range group {
			codes = append(codes, row.ShortCode)
			uids = append(uids, row.AudienceUID)
		}
		var foundCodes []string
		clickQuery := repository.ExcludeAutomatedClickTraffic(f.db.WithContext(ctx).Table("short_link_clicks"))
		if err := clickQuery.Where("campaign_id=? AND uid IN ?", campaignID, codes).Distinct("uid").Pluck("uid", &foundCodes).Error; err != nil {
			return nil, nil, err
		}
		clicked[campaignID] = make(map[string]bool, len(foundCodes))
		for _, code := range foundCodes {
			clicked[campaignID][code] = true
		}
		actions[campaignID] = make(map[string]bool)
		if bundleID := group[0].BundleID; bundleID != nil && *bundleID != 0 {
			var foundUIDs []string
			if err := f.db.WithContext(ctx).Table("bundle_action_file_uids AS u").Select("DISTINCT u.uid").Joins("JOIN bundle_action_files AS f ON f.id=u.bundle_action_file_id").Where("u.bundle_id=? AND f.status=? AND u.uid IN ?", *bundleID, models.BundleActionFileProcessed, uids).Scan(&foundUIDs).Error; err != nil {
				return nil, nil, err
			}
			for _, uid := range foundUIDs {
				actions[campaignID][uid] = true
			}
		}
	}
	return clicked, actions, nil
}

func (f *CampaignAudienceReportJobFlowImpl) ExecuteNext(ctx context.Context, lease time.Duration) error {
	job, claimed, err := f.jobs.ClaimDue(ctx, utils.UTCNow(), lease)
	if err != nil || !claimed {
		return err
	}
	// A recovered worker owns a new lease token. It must not see or remove this
	// attempt's staging rows until this worker has finished with them.
	_ = f.db.WithContext(ctx).Where("job_id=? AND lease_token<>?", job.ID, *job.LeaseToken).Delete(&campaignAudienceReportStageRow{}).Error
	defer func() {
		_ = f.db.WithContext(context.Background()).Where("job_id=? AND lease_token=?", job.ID, *job.LeaseToken).Delete(&campaignAudienceReportStageRow{}).Error
	}()
	if err := f.stage(ctx, job); err != nil {
		return f.fail(ctx, job, campaignAudienceReportFailureCode(err, "CAMPAIGN_REPORT_SOURCE_FAILED"), err)
	}
	rows, sheets, bytes, path, err := f.write(ctx, job)
	if err != nil {
		return f.fail(ctx, job, campaignAudienceReportFailureCode(err, "CAMPAIGN_REPORT_EXPORT_FAILED"), err)
	}
	return f.complete(ctx, job, rows, sheets, bytes, path)
}

func (f *CampaignAudienceReportJobFlowImpl) complete(ctx context.Context, job *models.CampaignAudienceReportJob, rows int64, sheets int, bytes int64, path string) error {
	now, expiry := utils.UTCNow(), utils.UTCNow().Add(24*time.Hour)
	job.Status, job.RowCount, job.SheetCount, job.ByteSize, job.OutputPath, job.CompletedAt, job.ExpiresAt = models.CampaignAudienceReportJobCompleted, rows, sheets, bytes, &path, &now, &expiry
	job.ErrorCode, job.ErrorMessage = nil, nil
	updated, err := f.jobs.UpdateClaimed(ctx, job)
	if err != nil {
		if cleanupErr := removeCampaignAudienceReportOutput(path); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("remove uncommitted campaign audience report output: %w", cleanupErr))
		}
		return err
	}
	if updated {
		f.auditEvent(ctx, job.CustomerID, models.AuditActionCampaignAudienceClickReportExported, "Campaign audience report job completed", true, nil, nil)
		return nil
	}
	if err := removeCampaignAudienceReportOutput(path); err != nil {
		return fmt.Errorf("campaign audience report job lease was lost and output cleanup failed: %w", err)
	}
	return fmt.Errorf("campaign audience report job lease was lost before completion")
}

func removeCampaignAudienceReportOutput(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func campaignAudienceReportFailureCode(cause error, fallback string) string {
	switch {
	case errors.Is(cause, errCampaignAudienceReportTooLarge):
		return "CAMPAIGN_REPORT_TOO_LARGE"
	case errors.Is(cause, errCampaignAudienceReportOutputTooLarge):
		return "CAMPAIGN_REPORT_OUTPUT_TOO_LARGE"
	default:
		return fallback
	}
}

func (f *CampaignAudienceReportJobFlowImpl) fail(ctx context.Context, job *models.CampaignAudienceReportJob, code string, cause error) error {
	if job.LeaseToken != nil {
		_ = os.Remove(reportOutputPath(f.storageRoot, job.ID, *job.LeaseToken) + ".partial")
	}
	message := "Unable to generate campaign audience report"
	switch code {
	case "CAMPAIGN_REPORT_TOO_LARGE":
		message = "Campaign audience report exceeds the maximum row limit"
	case "CAMPAIGN_REPORT_OUTPUT_TOO_LARGE":
		message = "Campaign audience report exceeds the maximum output size"
	}
	job.Status, job.ErrorCode, job.ErrorMessage = models.CampaignAudienceReportJobFailed, &code, &message
	// The worker's context may already be timed out. Persisting the terminal
	// state still needs a short independent budget; UpdateClaimed keeps it safe
	// if this worker has meanwhile lost its lease.
	persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updated, updateErr := f.jobs.UpdateClaimed(persistCtx, job)
	if updateErr != nil {
		return updateErr
	}
	if !updated {
		return fmt.Errorf("campaign audience report job lease was lost while recording failure")
	}
	f.auditEvent(persistCtx, job.CustomerID, models.AuditActionCampaignAudienceClickReportExportFailed, "Campaign audience report job failed", false, &message, nil)
	return cause
}
func (f *CampaignAudienceReportJobFlowImpl) Cleanup(ctx context.Context) error {
	now := utils.UTCNow()
	paths, err := f.jobs.ExpiredOutputPaths(ctx, now)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		// Make the report unavailable before removing its file. If the database
		// update fails, keeping an orphan is recoverable; deleting first leaves a
		// completed job that incorrectly points to a missing download.
		if err := f.jobs.MarkExpiredOutput(ctx, path, now); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	paths, err = f.jobs.ReferencedOutputPaths(ctx)
	if err != nil {
		return err
	}
	if err := f.removeUnreferencedOutputFiles(paths, now.Add(-campaignAudienceReportOrphanFileAge)); err != nil {
		return err
	}
	return f.jobs.PurgeTerminalBefore(ctx, now.Add(-30*24*time.Hour))
}

// removeUnreferencedOutputFiles reaps old final and partial artifacts from
// workers that crashed or lost database connectivity after writing a file.
// The age guard avoids racing a worker that has renamed its output but has not
// yet committed the completed state. Only files matching this report's
// UUID-based naming scheme inside its dedicated root are eligible; unrelated
// files in the directory are left untouched.
func (f *CampaignAudienceReportJobFlowImpl) removeUnreferencedOutputFiles(referencedPaths []string, before time.Time) error {
	referenced := make(map[string]struct{}, len(referencedPaths))
	for _, path := range referencedPaths {
		referenced[filepath.Clean(path)] = struct{}{}
	}
	entries, err := os.ReadDir(f.storageRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !isCampaignAudienceReportOutputName(entry.Name()) {
			continue
		}
		path := filepath.Join(f.storageRoot, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(before) {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".partial") {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if _, ok := referenced[filepath.Clean(path)]; ok {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func isCampaignAudienceReportOutputName(name string) bool {
	base := strings.TrimSuffix(name, ".partial")
	if !strings.HasSuffix(base, ".xlsx") {
		return false
	}
	base = strings.TrimSuffix(base, ".xlsx")
	if len(base) != 73 || base[36] != '-' {
		return false
	}
	_, firstErr := uuid.Parse(base[:36])
	_, secondErr := uuid.Parse(base[37:])
	return firstErr == nil && secondErr == nil
}
