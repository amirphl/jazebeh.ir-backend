package businessflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

// AdminShortLinkFlow provides use cases for admin short link creation
// The admin uploads a CSV with a 'long_link' column and provides a short_link_domain parameter
// For each row with a valid long_link, a short link will be generated and inserted
// The generated short URL format is: <short_link_domain>/s/<uid>
// Note: This flow assumes a public redirect route exists at /s/:uid
// UIDs are generated sequentially from "0000" upward using base36 digits (0-9 then a-z), expanding up to 5 chars (max "zzzzz").
// This function skips rows with empty long_link values
// It returns a summary with counts and the created short links
// Validations are minimal; consumers may validate long_link formats if needed
// Domain should include scheme (https://) if desired by the caller
// If not provided with a scheme, https:// will be prefixed automatically
type AdminShortLinkFlow interface {
	CreateShortLinksFromCSV(ctx context.Context, csvReader io.Reader, shortLinkDomain string, scenarioName string) (*dto.AdminCreateShortLinksResponse, error)
	UploadJob(ctx context.Context, id string) (*dto.AdminShortLinkUploadJobDTO, error)
	ProcessPendingUploadJobs(ctx context.Context) error
	DownloadShortLinksCSV(ctx context.Context, scenarioID uint) (string, []byte, error)
	DownloadShortLinksWithClicksCSV(ctx context.Context, scenarioID uint) (string, []byte, error)
	DownloadShortLinksWithClicksCSVRange(ctx context.Context, scenarioFrom, scenarioTo uint) (string, []byte, error)
	DownloadShortLinksWithClicksExcelByScenarioNameRegex(ctx context.Context, scenarioNameRegex string) (string, []byte, error)
}

type AdminShortLinkFlowImpl struct {
	repo      repository.ShortLinkRepository
	clickRepo repository.ShortLinkClickRepository
	auditRepo repository.AuditLogRepository
	publisher ShortLinkMappingPublisher
	jobRepo   repository.AdminShortLinkUploadJobRepository
}

func NewAdminShortLinkFlow(repo repository.ShortLinkRepository, clickRepo repository.ShortLinkClickRepository, auditRepo repository.AuditLogRepository, jobRepo repository.AdminShortLinkUploadJobRepository, publishers ...ShortLinkMappingPublisher) AdminShortLinkFlow {
	var publisher ShortLinkMappingPublisher
	if len(publishers) > 0 {
		publisher = publishers[0]
	}
	return &AdminShortLinkFlowImpl{repo: repo, clickRepo: clickRepo, auditRepo: auditRepo, publisher: publisher, jobRepo: jobRepo}
}

func (f *AdminShortLinkFlowImpl) CreateShortLinksFromCSV(ctx context.Context, csvReader io.Reader, shortLinkDomain string, scenarioName string) (*dto.AdminCreateShortLinksResponse, error) {
	if csvReader == nil {
		return nil, NewBusinessError("VALIDATION_ERROR", "CSV file is required", nil)
	}
	shortLinkDomain = normalizeDomain(shortLinkDomain)
	if shortLinkDomain == "" {
		return nil, NewBusinessError("VALIDATION_ERROR", "short_link_domain is required", nil)
	}

	scenarioName = strings.TrimSpace(scenarioName)
	if scenarioName == "" {
		return nil, NewBusinessError("VALIDATION_ERROR", "scenario_name is required", nil)
	}

	if f.jobRepo == nil {
		return nil, NewBusinessError("UPLOAD_JOB_UNAVAILABLE", "Admin upload jobs are not configured", nil)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, csvReader); err != nil {
		return nil, NewBusinessError("CSV_READ_ERROR", "Failed to read CSV", err)
	}
	keyRaw := sha256.Sum256([]byte(uuid.NewString()))
	job := &models.AdminShortLinkUploadJob{ID: uuid.NewString(), AllocationKey: hex.EncodeToString(keyRaw[:]), ScenarioName: scenarioName, Domain: shortLinkDomain, CSVData: buf.Bytes(), Status: "pending"}
	if err := f.jobRepo.Save(ctx, job); err != nil {
		return nil, NewBusinessError("CREATE_UPLOAD_JOB_FAILED", "Failed to create upload job", err)
	}
	_, _ = f.processJob(ctx, job)
	current, _ := f.jobRepo.ByID(ctx, job.ID)
	if current == nil {
		current = job
	}
	resp := &dto.AdminCreateShortLinksResponse{Message: "Upload processing", ScenarioID: current.ScenarioID, Job: mapAdminUploadJob(current)}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminCreateShortLinks, "Admin upload short link CSV", true, nil, map[string]any{
		"scenario_id":   current.ScenarioID,
		"scenario_name": scenarioName,
		"domain":        shortLinkDomain,
	}, nil)
	return resp, nil
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	if !strings.Contains(domain, "://") {
		domain = "https://" + domain
	}
	u, err := url.Parse(domain)
	if err != nil || u.Scheme != "https" || u.Hostname() != "jzbe.ir" || u.User != nil || u.Port() != "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return "https://jzbe.ir"
}

func mapAdminUploadJob(j *models.AdminShortLinkUploadJob) dto.AdminShortLinkUploadJobDTO {
	return dto.AdminShortLinkUploadJobDTO{ID: j.ID, ScenarioID: j.ScenarioID, Status: j.Status, TotalRows: j.TotalRows, Created: j.Created, Skipped: j.Skipped, Published: j.Published, Attempts: j.Attempts, NextAttemptAt: j.NextAttemptAt, LastError: j.LastError}
}
func (f *AdminShortLinkFlowImpl) UploadJob(ctx context.Context, id string) (*dto.AdminShortLinkUploadJobDTO, error) {
	j, err := f.jobRepo.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, NewBusinessError("UPLOAD_JOB_NOT_FOUND", "Upload job not found", nil)
	}
	out := mapAdminUploadJob(j)
	return &out, nil
}
func (f *AdminShortLinkFlowImpl) ProcessPendingUploadJobs(ctx context.Context) error {
	jobs, err := f.jobRepo.Due(ctx, time.Now().UTC(), 20)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if _, err := f.processJob(ctx, j); err != nil {
			log.Printf("admin short link upload job %s: %v", j.ID, err)
		}
	}
	return nil
}
func (f *AdminShortLinkFlowImpl) processJob(ctx context.Context, job *models.AdminShortLinkUploadJob) (*models.AdminShortLinkUploadJob, error) {
	if f.publisher == nil {
		return f.failJob(ctx, job, fmt.Errorf("external short-link publisher is not configured"))
	}
	job.Status = "processing"
	job.Attempts++
	job.LastError = nil
	_ = f.jobRepo.Update(ctx, job)
	reader := csv.NewReader(bytes.NewReader(job.CSVData))
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return f.failJob(ctx, job, err)
	}
	longIdx := -1
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "long_link") {
			longIdx = i
		}
	}
	if longIdx < 0 {
		return f.failJob(ctx, job, fmt.Errorf("CSV requires long_link column"))
	}
	links, err := f.repo.ByAllocationKey(ctx, job.AllocationKey)
	if err != nil {
		return f.failJob(ctx, job, err)
	}
	if len(links) == 0 {
		lockShortLinkGen()
		defer unlockShortLinkGen()
		if job.ScenarioID == 0 {
			lastScenarioID, scenarioErr := f.repo.GetLastScenarioID(ctx)
			if scenarioErr != nil {
				return f.failJob(ctx, job, scenarioErr)
			}
			job.ScenarioID = lastScenarioID + 1
			if err := f.jobRepo.Update(ctx, job); err != nil {
				return f.failJob(ctx, job, err)
			}
		}
		last, e := f.repo.GetMaxUIDSince(ctx, time.Date(2025, 11, 10, 15, 45, 11, 401492000, time.UTC))
		if e != nil {
			return f.failJob(ctx, job, e)
		}
		var seq uint64
		if last != "" {
			seq, e = decodeBase36(last)
			if e != nil {
				return f.failJob(ctx, job, e)
			}
			seq++
		}
		batch := make([]*models.ShortLink, 0, 500)
		pos := 0
		for {
			rec, e := reader.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return f.failJob(ctx, job, e)
			}
			job.TotalRows++
			if longIdx >= len(rec) || strings.TrimSpace(rec[longIdx]) == "" {
				job.Skipped++
				continue
			}
			uid, e := formatSequentialUID(seq)
			if e != nil {
				return f.failJob(ctx, job, e)
			}
			seq++
			sid := job.ScenarioID
			sn := job.ScenarioName
			p := pos
			batch = append(batch, &models.ShortLink{UID: uid, ScenarioID: &sid, ScenarioName: &sn, LongLink: strings.TrimSpace(rec[longIdx]), ShortLink: job.Domain + "/" + uid, AllocationKey: &job.AllocationKey, AllocationPosition: &p})
			pos++
			if len(batch) == 500 {
				if e = f.repo.SaveBatch(ctx, batch); e != nil {
					return f.failJob(ctx, job, e)
				}
				batch = batch[:0]
			}
		}
		if len(batch) > 0 {
			if e = f.repo.SaveBatch(ctx, batch); e != nil {
				return f.failJob(ctx, job, e)
			}
		}
		links, e = f.repo.ByAllocationKey(ctx, job.AllocationKey)
		if e != nil {
			return f.failJob(ctx, job, e)
		}
		job.Created = len(links)
	}
	for start := 0; start < len(links); start += 500 {
		end := start + 500
		if end > len(links) {
			end = len(links)
		}
		if err := publishShortLinkMappings(ctx, f.repo, f.publisher, links[start:end]); err != nil {
			return f.failJob(ctx, job, err)
		}
	}
	now := time.Now().UTC()
	job.Status = "completed"
	job.Published = len(links)
	job.CompletedAt = &now
	job.NextAttemptAt = nil
	job.LastError = nil
	err = f.jobRepo.Update(ctx, job)
	return job, err
}
func (f *AdminShortLinkFlowImpl) failJob(ctx context.Context, j *models.AdminShortLinkUploadJob, err error) (*models.AdminShortLinkUploadJob, error) {
	msg := err.Error()
	delay := time.Minute * time.Duration(1<<min(j.Attempts, 6))
	next := time.Now().UTC().Add(delay)
	j.Status = "retrying"
	j.LastError = &msg
	j.NextAttemptAt = &next
	_ = f.jobRepo.Update(ctx, j)
	return j, err
}

func encodeBase36(n uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 16)
	for n > 0 {
		r := n % 36
		buf = append(buf, digits[r])
		n /= 36
	}
	// reverse in place
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func decodeBase36(s string) (uint64, error) {
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			v = int(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid base36 character: %q", c)
		}
		if v >= 36 {
			return 0, fmt.Errorf("invalid base36 value: %d", v)
		}
		n = n*36 + uint64(v)
	}
	return n, nil
}

func formatSequentialUID(seq uint64) (string, error) {
	s := encodeBase36(seq)
	if len(s) < 4 {
		s = strings.Repeat("0", 4-len(s)) + s
	}
	if len(s) > 5 {
		return "", fmt.Errorf("sequence exhausted at %s", s)
	}
	return s, nil
}

func (f *AdminShortLinkFlowImpl) DownloadShortLinksCSV(ctx context.Context, scenarioID uint) (string, []byte, error) {
	if scenarioID == 0 {
		return "", nil, NewBusinessError("VALIDATION_ERROR", "scenario_id must be greater than 0", nil)
	}

	filter := models.ShortLinkFilter{ScenarioID: &scenarioID}
	rows, err := f.repo.ByFilter(ctx, filter, "id ASC", 0, 0)
	if err != nil {
		return "", nil, NewBusinessError("FETCH_SHORT_LINKS_FAILED", "Failed to fetch short links", err)
	}

	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	alreadyFlushed := false
	defer func() {
		if !alreadyFlushed {
			w.Flush()
			alreadyFlushed = true
		}
	}()

	// Header: all current columns in short_links
	header := []string{
		"id",
		"uid",
		"campaign_id",
		"client_id",
		"scenario_id",
		"scenario_name",
		"phone_number",
		"long_link",
		"short_link",
		"created_at",
		"updated_at",
	}
	if err := w.Write(header); err != nil {
		return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV header", err)
	}

	for _, r := range rows {
		campaignID := ""
		if r.CampaignID != nil {
			campaignID = strconv.FormatUint(uint64(*r.CampaignID), 10)
		}
		clientID := ""
		if r.ClientID != nil {
			clientID = strconv.FormatUint(uint64(*r.ClientID), 10)
		}
		scenario := ""
		if r.ScenarioID != nil {
			scenario = strconv.FormatUint(uint64(*r.ScenarioID), 10)
		}
		phone := ""
		if r.PhoneNumber != nil {
			phone = *r.PhoneNumber
		}

		record := []string{
			strconv.FormatUint(uint64(r.ID), 10),
			r.UID,
			campaignID,
			clientID,
			scenario,
			phone,
			r.LongLink,
			r.ShortLink,
			r.CreatedAt.UTC().Format(time.RFC3339),
			r.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if err := w.Write(record); err != nil {
			return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV row", err)
		}
	}

	filename := fmt.Sprintf("short_links_scenario_%d.csv", scenarioID)
	if !alreadyFlushed {
		w.Flush()
		alreadyFlushed = true
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminDownloadShortLinks, "Admin downloaded short links CSV", true, nil, map[string]any{
		"scenario_id": scenarioID,
		"rows":        len(rows),
	}, nil)
	return filename, buf.Bytes(), nil
}

func (f *AdminShortLinkFlowImpl) DownloadShortLinksWithClicksCSV(ctx context.Context, scenarioID uint) (string, []byte, error) {
	if scenarioID == 0 {
		return "", nil, NewBusinessError("VALIDATION_ERROR", "scenario_id must be greater than 0", nil)
	}

	rows, err := f.repo.ListWithClicksDetailsByScenario(ctx, scenarioID, "short_link_id ASC, id ASC")
	if err != nil {
		return "", nil, NewBusinessError("FETCH_SHORT_LINKS_FAILED", "Failed to fetch short links with clicks", err)
	}

	records := buildClickRecords(rows, buildClickRecord)

	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	alreadyFlushed := false
	defer func() {
		if !alreadyFlushed {
			w.Flush()
			alreadyFlushed = true
		}
	}()

	header := []string{
		"id",
		"uid",
		"campaign_id",
		"client_id",
		"scenario_id",
		"scenario_name",
		"phone_number",
		"long_link",
		"short_link",
		"created_at",
		"updated_at",
		"user_agent",
		"ip",
	}
	if err := w.Write(header); err != nil {
		return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV header", err)
	}

	for _, record := range records {
		if err := w.Write(record); err != nil {
			return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV row", err)
		}
	}

	filename := fmt.Sprintf("short_links_with_clicks_scenario_%d.csv", scenarioID)
	if !alreadyFlushed {
		w.Flush()
		alreadyFlushed = true
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminDownloadShortLinksWithClicks, "Admin downloaded short links with clicks CSV", true, nil, map[string]any{
		"scenario_id": scenarioID,
		"rows":        len(records),
	}, nil)
	return filename, buf.Bytes(), nil
}

func (f *AdminShortLinkFlowImpl) DownloadShortLinksWithClicksCSVRange(ctx context.Context, scenarioFrom, scenarioTo uint) (string, []byte, error) {
	if scenarioFrom == 0 || scenarioTo == 0 {
		return "", nil, NewBusinessError("VALIDATION_ERROR", "scenario_from and scenario_to must be greater than 0", nil)
	}
	if scenarioTo <= scenarioFrom {
		return "", nil, NewBusinessError("VALIDATION_ERROR", "scenario_to must be greater than scenario_from", nil)
	}

	rows, err := f.repo.ListWithClicksDetailsByScenarioRange(ctx, scenarioFrom, scenarioTo, "short_link_id ASC, id ASC")
	if err != nil {
		return "", nil, NewBusinessError("FETCH_SHORT_LINKS_FAILED", "Failed to fetch short links with clicks by range", err)
	}

	records := buildClickRecords(rows, buildClickRecord)

	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	alreadyFlushed := false
	defer func() {
		if !alreadyFlushed {
			w.Flush()
			alreadyFlushed = true
		}
	}()

	header := []string{
		"id",
		"uid",
		"campaign_id",
		"client_id",
		"scenario_id",
		"phone_number",
		"long_link",
		"short_link",
		"created_at",
		"updated_at",
		"user_agent",
		"ip",
	}
	if err := w.Write(header); err != nil {
		return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV header", err)
	}

	for _, record := range records {
		if err := w.Write(record); err != nil {
			return "", nil, NewBusinessError("CSV_WRITE_ERROR", "Failed to write CSV row", err)
		}
	}

	filename := fmt.Sprintf("short_links_with_clicks_scenarios_%d_to_%d.csv", scenarioFrom, scenarioTo)
	if !alreadyFlushed {
		w.Flush()
		alreadyFlushed = true
	}
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminDownloadShortLinksRange, "Admin downloaded short links with clicks by range", true, nil, map[string]any{
		"scenario_from": scenarioFrom,
		"scenario_to":   scenarioTo,
		"rows":          len(records),
	}, nil)
	return filename, buf.Bytes(), nil
}

func (f *AdminShortLinkFlowImpl) DownloadShortLinksWithClicksExcelByScenarioNameRegex(ctx context.Context, scenarioNameRegex string) (string, []byte, error) {
	pattern := strings.TrimSpace(scenarioNameRegex)
	if pattern == "" {
		return "", nil, NewBusinessError("VALIDATION_ERROR", "scenario_name_regex must not be empty", nil)
	}

	// rows, err := f.repo.ListWithClicksDetailsByScenarioNameRegex(ctx, pattern, "scenario_id ASC, short_link_id ASC, id ASC")
	rows, err := f.repo.ListWithClicksDetailsByScenarioNameLike(ctx, pattern, "scenario_id ASC, short_link_id ASC, id ASC")
	if err != nil {
		return "", nil, NewBusinessError("FETCH_SHORT_LINKS_FAILED", "Failed to fetch short links by scenario name regex", err)
	}

	// Build Excel with one sheet per scenario
	// Lazy import to avoid unused if excelize not referenced elsewhere
	type excelFile interface{}
	_ = excelFile(nil)

	// Create workbook
	xl := excelize.NewFile()
	defer func() { _ = xl.Close() }()

	// Prepare grouping by scenario
	type rowData = *repository.ShortLinkWithClick
	byScenario := make(map[uint][]rowData)
	nameByScenario := make(map[uint]string)
	order := make([]uint, 0)
	for _, r := range rows {
		if r.ScenarioID == nil {
			continue
		}
		sid := *r.ScenarioID
		byScenario[sid] = append(byScenario[sid], r)
		if _, ok := nameByScenario[sid]; !ok {
			if r.ScenarioName != nil && strings.TrimSpace(*r.ScenarioName) != "" {
				nameByScenario[sid] = *r.ScenarioName
			} else {
				nameByScenario[sid] = fmt.Sprintf("scenario_%d", sid)
			}
			order = append(order, sid)
		}
	}

	// Create sheets
	usedNames := map[string]bool{}
	for i, sid := range order {
		baseName := sanitizeSheetName(nameByScenario[sid])
		name := baseName
		idx := 1
		for usedNames[name] {
			idx++
			name = truncateSheetName(fmt.Sprintf("%s_%d", baseName, idx))
		}
		usedNames[name] = true
		if i == 0 {
			// Rename default sheet
			xl.SetSheetName(xl.GetSheetName(0), name)
		} else {
			_, _ = xl.NewSheet(name)
		}

		header := []string{"id", "uid", "campaign_id", "client_id", "scenario_id", "scenario_name", "phone_number", "long_link", "short_link", "created_at", "updated_at", "user_agent", "ip"}
		_ = xl.SetSheetRow(name, "A1", &header)

		rowsForScenario := byScenario[sid]
		records := buildClickRecords(rowsForScenario, func(r *repository.ShortLinkWithClick) []string {
			return buildClickRecordWithScenario(sid, nameByScenario[sid], r)
		})
		for ri, record := range records {
			cellRef, _ := excelize.CoordinatesToCellName(1, ri+2)
			_ = xl.SetSheetRow(name, cellRef, &record)
		}
	}

	buf, err := xl.WriteToBuffer()
	if err != nil {
		return "", nil, NewBusinessError("EXCEL_WRITE_ERROR", "Failed to write Excel file", err)
	}
	filename := "short_links_with_clicks_by_scenario_name.xlsx"
	logAdminAction(ctx, f.auditRepo, models.AuditActionAdminDownloadShortLinksByScenarioName, "Admin downloaded short links with clicks by scenario name regex", true, nil, map[string]any{
		"scenario_name_regex": pattern,
		"scenarios":           len(order),
	}, nil)
	return filename, buf.Bytes(), nil
}

func sanitizeSheetName(name string) string {
	// Excel sheet names cannot contain: : \\ / ? * [ ] and must be <= 31 chars
	replacer := strings.NewReplacer(":", "_", "\\", "_", "/", "_", "?", "_", "*", "_", "[", "_", "]", "_")
	safe := replacer.Replace(name)
	return truncateSheetName(strings.TrimSpace(safe))
}

func truncateSheetName(name string) string {
	if len(name) > 31 {
		return name[:31]
	}
	if name == "" {
		return "Sheet"
	}
	return name
}

func buildClickRecords(rows []*repository.ShortLinkWithClick, builder func(*repository.ShortLinkWithClick) []string) [][]string {
	if len(rows) == 0 {
		return [][]string{}
	}
	out := make([][]string, len(rows))
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > len(rows) {
		workers = len(rows)
	}
	chunk := (len(rows) + workers - 1) / workers

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		start := w * chunk
		if start >= len(rows) {
			break
		}
		end := start + chunk
		if end > len(rows) {
			end = len(rows)
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				out[i] = builder(rows[i])
			}
		}(start, end)
	}
	wg.Wait()
	return out
}

func buildClickRecord(r *repository.ShortLinkWithClick) []string {
	campaignID := ""
	if r.CampaignID != nil {
		campaignID = strconv.FormatUint(uint64(*r.CampaignID), 10)
	}
	clientID := ""
	if r.ClientID != nil {
		clientID = strconv.FormatUint(uint64(*r.ClientID), 10)
	}
	scenario := ""
	if r.ScenarioID != nil {
		scenario = strconv.FormatUint(uint64(*r.ScenarioID), 10)
	}
	phone := ""
	if r.PhoneNumber != nil {
		phone = *r.PhoneNumber
	}
	scName := ""
	if r.ScenarioName != nil {
		scName = *r.ScenarioName
	}
	ua := ""
	if r.ClickUserAgent != nil {
		ua = *r.ClickUserAgent
	}
	ip := ""
	if r.ClickIP != nil {
		ip = *r.ClickIP
	}

	return []string{
		strconv.FormatUint(uint64(r.ID), 10),
		r.UID,
		campaignID,
		clientID,
		scenario,
		scName,
		phone,
		r.LongLink,
		r.ShortLink,
		r.CreatedAt.UTC().Format(time.RFC3339),
		r.UpdatedAt.UTC().Format(time.RFC3339),
		ua,
		ip,
	}
}

func buildClickRecordWithScenario(sid uint, scenarioName string, r *repository.ShortLinkWithClick) []string {
	// Reuse base builder but override scenario id/name to avoid nil checks
	record := buildClickRecord(r)
	record[4] = strconv.FormatUint(uint64(sid), 10)
	record[5] = scenarioName
	return record
}
