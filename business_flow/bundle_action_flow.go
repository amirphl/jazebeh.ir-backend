package businessflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

const (
	maxBundleActionFileBytes             int64 = 50 << 20
	maxBundleActionFileUncompressedBytes int64 = 256 << 20
	// Excelize spills worksheet XML over this limit to its temporary directory,
	// avoiding a full worksheet allocation in the scheduler process.
	maxBundleActionFileXMLInMemoryBytes int64 = 8 << 20
	// The parser must retain unique UIDs until they are persisted, so bound row
	// count as well as compressed and uncompressed workbook sizes.
	maxBundleActionFileRows int64 = 500_000
)

const defaultBundleActionFileStorageRoot = "data/uploads/bundle-action-files"

type BundleActionFlow interface {
	Upload(context.Context, uint, uint, string, string, io.Reader) (*models.BundleActionFile, error)
	Get(context.Context, uint, uint, int64) (*models.BundleActionFile, error)
	List(context.Context, uint, uint, int, int) ([]*models.BundleActionFile, int64, error)
	Delete(context.Context, uint, uint, int64) error
	Summary(context.Context, uint, uint) (*models.BundleActionSummary, error)
	TagMetrics(context.Context, uint, uint) ([]*models.BundleActionTagMetric, error)
	CampaignMetric(context.Context, uint, string) (*models.BundleActionCampaignMetric, error)
	ExecuteBundleActionFile(context.Context, int64, time.Time) error
}
type BundleActionFlowImpl struct {
	repo         repository.BundleActionRepository
	bundleRepo   repository.BundleRepository
	campaignRepo repository.CampaignRepository
	db           *gorm.DB
	storageRoot  string
}

func NewBundleActionFlow(repo repository.BundleActionRepository, bundles repository.BundleRepository, campaigns repository.CampaignRepository, db *gorm.DB) *BundleActionFlowImpl {
	storageRoot := strings.TrimSpace(os.Getenv("BUNDLE_ACTION_FILE_STORAGE_ROOT"))
	if storageRoot == "" {
		storageRoot = defaultBundleActionFileStorageRoot
	}
	return &BundleActionFlowImpl{repo: repo, bundleRepo: bundles, campaignRepo: campaigns, db: db, storageRoot: storageRoot}
}
func (s *BundleActionFlowImpl) ownedBundle(ctx context.Context, customerID, bundleID uint) (*models.Bundle, error) {
	b, err := s.bundleRepo.ByID(ctx, bundleID)
	if err != nil {
		return nil, err
	}
	if b.CustomerID != customerID {
		return nil, ErrBundleAccessDenied
	}
	return b, nil
}
func actionFileItemPath(root string, bundleID uint, id int64) string {
	return filepath.Join(root, fmt.Sprintf("%d", bundleID), fmt.Sprintf("%d.xlsx", id))
}
func (s *BundleActionFlowImpl) Upload(ctx context.Context, customerID, bundleID uint, name, actionLevel string, source io.Reader) (*models.BundleActionFile, error) {
	if _, err := s.ownedBundle(ctx, customerID, bundleID); err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(name)) != ".xlsx" {
		return nil, NewBusinessError("ACTION_FILE_FORMAT_INVALID", "only .xlsx action files are supported", nil)
	}
	data, err := io.ReadAll(io.LimitReader(source, maxBundleActionFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBundleActionFileBytes {
		return nil, NewBusinessError("ACTION_FILE_TOO_LARGE", "action file exceeds the 50 MB limit", nil)
	}
	if len(data) == 0 {
		return nil, NewBusinessError("ACTION_FILE_EMPTY", "action file is empty", nil)
	}
	hash := sha256.Sum256(data)
	now := utils.UTCNow()
	row := &models.BundleActionFile{BundleID: bundleID, OriginalFileName: filepath.Base(name), ActionLevel: actionLevel, ContentSHA256: hex.EncodeToString(hash[:]), Status: models.BundleActionFilePending, UploadedByCustomerID: customerID, CreatedAt: now, UpdatedAt: now, StoragePath: "pending"}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, err
	}
	path := actionFileItemPath(s.storageRoot, bundleID, row.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create action storage: %w", err)
	}
	if err := os.WriteFile(path, data, 0640); err != nil {
		return nil, fmt.Errorf("store action file: %w", err)
	}
	if err := s.db.WithContext(ctx).Model(&models.BundleActionFile{}).Where("id=?", row.ID).Update("storage_path", path).Error; err != nil {
		return nil, err
	}
	row.StoragePath = path
	return row, nil
}
func (s *BundleActionFlowImpl) Get(ctx context.Context, c, b uint, id int64) (*models.BundleActionFile, error) {
	if _, err := s.ownedBundle(ctx, c, b); err != nil {
		return nil, err
	}
	x, err := s.repo.ByID(ctx, id)
	if err == nil && x.BundleID != b {
		return nil, ErrBundleAccessDenied
	}
	return x, err
}
func (s *BundleActionFlowImpl) List(ctx context.Context, c, b uint, page, limit int) ([]*models.BundleActionFile, int64, error) {
	if _, err := s.ownedBundle(ctx, c, b); err != nil {
		return nil, 0, err
	}
	return s.repo.List(ctx, b, limit, (page-1)*limit)
}
func (s *BundleActionFlowImpl) Delete(ctx context.Context, c, b uint, id int64) error {
	if _, err := s.ownedBundle(ctx, c, b); err != nil {
		return err
	}
	x, err := s.repo.ByID(ctx, id)
	if err != nil {
		return err
	}
	if x.BundleID != b {
		return ErrBundleAccessDenied
	}
	if x.Status != models.BundleActionFileProcessed {
		return NewBusinessError("ACTION_FILE_NOT_DELETABLE", "only processed action files can be deleted", nil)
	}
	return s.repo.RequestDelete(ctx, id, c, utils.UTCNow())
}
func (s *BundleActionFlowImpl) Summary(ctx context.Context, c, b uint) (*models.BundleActionSummary, error) {
	if _, err := s.ownedBundle(ctx, c, b); err != nil {
		return nil, err
	}
	return s.repo.Summary(ctx, b)
}
func (s *BundleActionFlowImpl) TagMetrics(ctx context.Context, c, b uint) ([]*models.BundleActionTagMetric, error) {
	if _, err := s.ownedBundle(ctx, c, b); err != nil {
		return nil, err
	}
	return s.repo.TagMetrics(ctx, b)
}
func (s *BundleActionFlowImpl) CampaignMetric(ctx context.Context, c uint, campaignUUID string) (*models.BundleActionCampaignMetric, error) {
	campaign, err := getCampaign(ctx, s.campaignRepo, campaignUUID, c)
	if err != nil {
		return nil, err
	}
	if campaign.BundleID == nil {
		return &models.BundleActionCampaignMetric{CampaignID: campaign.ID}, nil
	}
	return s.repo.CampaignMetric(ctx, *campaign.BundleID, campaign.ID)
}
func (s *BundleActionFlowImpl) ExecuteBundleActionFile(ctx context.Context, id int64, lease time.Time) error {
	file, err := s.repo.ByID(ctx, id)
	if err != nil {
		return err
	}
	if file.Status == models.BundleActionFileDeletePending {
		return s.repo.Complete(ctx, id, utils.UTCNow())
	}
	if file.Status != models.BundleActionFileProcessing {
		return nil
	}
	uids, total, dupes, invalid, err := parseBundleActionXLSX(file.StoragePath)
	if err != nil {
		_ = s.repo.Fail(context.Background(), id, utils.UTCNow(), "ACTION_FILE_PARSE_FAILED", "The action file could not be parsed")
		return err
	}
	now := utils.UTCNow()
	if err := s.repo.StoreUIDsAndResult(ctx, id, file.BundleID, uids, total, dupes, invalid, now); err != nil {
		return err
	}
	return s.repo.Complete(ctx, id, now)
}
func parseBundleActionXLSX(path string) (uids []string, total, duplicates, invalid int64, err error) {
	f, err := excelize.OpenFile(path, excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    maxBundleActionFileUncompressedBytes,
		UnzipXMLSizeLimit: maxBundleActionFileXMLInMemoryBytes,
	})
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, 0, 0, 0, errors.New("no worksheet")
	}
	rows, err := f.Rows(sheets[0])
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return nil, 0, 0, 0, err
		}
		return nil, 0, 0, 0, errors.New("no data rows")
	}
	header, err := rows.Columns(excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return nil, 0, 0, 0, err
		}
		return nil, 0, 0, 0, errors.New("no data rows")
	}
	uidColumn := -1
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "uid") {
			if uidColumn >= 0 {
				return nil, 0, 0, 0, errors.New("duplicate uid column")
			}
			uidColumn = i
		}
	}
	if uidColumn < 0 {
		return nil, 0, 0, 0, errors.New("missing uid column")
	}
	seen := map[string]struct{}{}
	for {
		total++
		if total > maxBundleActionFileRows {
			return nil, total, duplicates, invalid, fmt.Errorf("action file exceeds the %d row limit", maxBundleActionFileRows)
		}
		row, rowErr := rows.Columns(excelize.Options{RawCellValue: true})
		if rowErr != nil {
			return nil, total, duplicates, invalid, rowErr
		}
		var uid string
		if uidColumn < len(row) {
			uid = strings.TrimSpace(row[uidColumn])
		}
		if uid == "" || len(uid) > 255 {
			invalid++
		} else if _, ok := seen[uid]; ok {
			duplicates++
		} else {
			seen[uid] = struct{}{}
		}
		if !rows.Next() {
			break
		}
	}
	if err := rows.Error(); err != nil {
		return nil, total, duplicates, invalid, err
	}
	if len(seen) == 0 {
		return nil, total, duplicates, invalid, errors.New("no valid uid")
	}
	uids = make([]string, 0, len(seen))
	for uid := range seen {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids, total, duplicates, invalid, nil
}

var _ BundleActionFlow = (*BundleActionFlowImpl)(nil)
