package businessflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/lib/pq"
)

func TestCampaignAudienceReportSizeFailureCodes(t *testing.T) {
	if got := campaignAudienceReportFailureCode(errCampaignAudienceReportTooLarge, "fallback"); got != "CAMPAIGN_REPORT_TOO_LARGE" {
		t.Fatalf("row limit code = %q", got)
	}
	writer := &limitedWriter{w: &bytes.Buffer{}, max: 1}
	if _, err := writer.Write([]byte("ab")); !errors.Is(err, errCampaignAudienceReportOutputTooLarge) {
		t.Fatalf("output limit error = %v", err)
	}
	if got := campaignAudienceReportFailureCode(errCampaignAudienceReportOutputTooLarge, "fallback"); got != "CAMPAIGN_REPORT_OUTPUT_TOO_LARGE" {
		t.Fatalf("output limit code = %q", got)
	}
}

func TestAppendStageRowUsesLastMappingInBatch(t *testing.T) {
	batch := make([]campaignAudienceReportStageRow, 0, 3)
	indexes := make(map[string]int, cap(batch))

	batch = appendStageRow(batch, indexes, campaignAudienceReportStageRow{AudienceUID: "first", ShortCode: "old"})
	batch = appendStageRow(batch, indexes, campaignAudienceReportStageRow{AudienceUID: "other", ShortCode: "other-code"})
	batch = appendStageRow(batch, indexes, campaignAudienceReportStageRow{AudienceUID: "first", ShortCode: "new"})

	if len(batch) != 2 {
		t.Fatalf("batch length = %d, want 2", len(batch))
	}
	if batch[indexes["first"]].ShortCode != "new" {
		t.Fatalf("last mapping was not retained: %#v", batch)
	}
}

type transferredReportCampaignRepository struct {
	repository.CampaignRepository
}

func (transferredReportCampaignRepository) ByCustomerIDAndIDs(context.Context, uint, []uint) ([]*models.Campaign, error) {
	return nil, nil
}

func TestEnsureJobCampaignsAreStillOwnedRejectsTransferredCampaign(t *testing.T) {
	flow := &CampaignAudienceReportJobFlowImpl{campaigns: transferredReportCampaignRepository{}}
	err := flow.ensureJobCampaignsAreStillOwned(context.Background(), &models.CampaignAudienceReportJob{CustomerID: 1, CampaignIDs: pq.Int64Array{42}})
	be, ok := err.(*BusinessError)
	if !ok || be.Code != "CAMPAIGN_REPORT_JOB_NOT_FOUND" {
		t.Fatalf("error = %#v, want CAMPAIGN_REPORT_JOB_NOT_FOUND", err)
	}
}

type leaseLostReportJobRepository struct {
	repository.CampaignAudienceReportJobRepository
}

type completionErrorReportJobRepository struct {
	repository.CampaignAudienceReportJobRepository
}

func (completionErrorReportJobRepository) UpdateClaimed(context.Context, *models.CampaignAudienceReportJob) (bool, error) {
	return false, errors.New("database unavailable")
}

func (leaseLostReportJobRepository) UpdateClaimed(context.Context, *models.CampaignAudienceReportJob) (bool, error) {
	return false, nil
}

func TestCompleteRemovesOnlyLostLeaseOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lost-lease.xlsx")
	if err := os.WriteFile(path, []byte("report"), 0o600); err != nil {
		t.Fatalf("create report output: %v", err)
	}
	flow := &CampaignAudienceReportJobFlowImpl{jobs: leaseLostReportJobRepository{}}
	if err := flow.complete(context.Background(), &models.CampaignAudienceReportJob{ID: "job"}, 1, 1, 6, path); err == nil {
		t.Fatal("complete succeeded after lease loss")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost-lease output still exists: %v", err)
	}
}

func TestCompleteRemovesOutputWhenDatabaseFinalizationFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database-error.xlsx")
	if err := os.WriteFile(path, []byte("report"), 0o600); err != nil {
		t.Fatalf("create report output: %v", err)
	}
	flow := &CampaignAudienceReportJobFlowImpl{jobs: completionErrorReportJobRepository{}}
	if err := flow.complete(context.Background(), &models.CampaignAudienceReportJob{ID: "job"}, 1, 1, 6, path); err == nil {
		t.Fatal("complete succeeded after database finalization error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database-error output still exists: %v", err)
	}
}

type cleanupReportJobRepository struct {
	repository.CampaignAudienceReportJobRepository
	purgeBefore time.Time
}

type failingExpiryMarkReportJobRepository struct {
	repository.CampaignAudienceReportJobRepository
	path string
}

func (r *failingExpiryMarkReportJobRepository) ExpiredOutputPaths(context.Context, time.Time) ([]string, error) {
	return []string{r.path}, nil
}

func (r *failingExpiryMarkReportJobRepository) MarkExpiredOutput(context.Context, string, time.Time) error {
	return errors.New("database unavailable")
}

func TestCleanupKeepsOutputWhenExpiryStateCannotBePersisted(t *testing.T) {
	dir := t.TempDir()
	path := reportOutputPath(dir, "4a5e47fd-e075-4d11-8e77-c654ab006b29", "d4f91cf4-3a76-4bcd-8bb3-5631f6f0f016")
	if err := os.WriteFile(path, []byte("report"), 0o600); err != nil {
		t.Fatalf("create report output: %v", err)
	}
	flow := &CampaignAudienceReportJobFlowImpl{jobs: &failingExpiryMarkReportJobRepository{path: path}, storageRoot: dir}
	if err := flow.Cleanup(context.Background()); err == nil {
		t.Fatal("cleanup succeeded despite expiry update failure")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("output was removed before the expiry state was persisted: %v", err)
	}
}

func (r *cleanupReportJobRepository) ExpiredOutputPaths(context.Context, time.Time) ([]string, error) {
	return nil, nil
}

func (r *cleanupReportJobRepository) ReferencedOutputPaths(context.Context) ([]string, error) {
	return nil, nil
}

func (r *cleanupReportJobRepository) PurgeTerminalBefore(_ context.Context, before time.Time) error {
	r.purgeBefore = before
	return nil
}

func TestRemoveUnreferencedOutputFilesKeepsReferencedAndRecentReportOnly(t *testing.T) {
	dir := t.TempDir()
	kept := reportOutputPath(dir, "4a5e47fd-e075-4d11-8e77-c654ab006b29", "d4f91cf4-3a76-4bcd-8bb3-5631f6f0f016")
	stale := reportOutputPath(dir, "5cf0cb59-8ed6-4e37-ae00-1165b2d703a6", "f48fa38c-b78f-477e-8eae-bf1223c87f7d")
	partial := stale + ".partial"
	for _, path := range []string{kept, stale, partial} {
		if err := os.WriteFile(path, []byte("report"), 0o600); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		old := time.Now().Add(-campaignAudienceReportOrphanFileAge - time.Second)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("age %s: %v", path, err)
		}
	}
	recent := reportOutputPath(dir, "615161f2-01dc-4fdb-9fa7-85e5bc7f75db", "a60a6e22-9548-42f7-a1ee-9071c33d48a4")
	if err := os.WriteFile(recent, []byte("recent report"), 0o600); err != nil {
		t.Fatalf("create recent report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep-me.txt"), []byte("other"), 0o600); err != nil {
		t.Fatalf("create unrelated file: %v", err)
	}
	flow := &CampaignAudienceReportJobFlowImpl{storageRoot: dir}
	if err := flow.removeUnreferencedOutputFiles([]string{kept}, time.Now().Add(-campaignAudienceReportOrphanFileAge)); err != nil {
		t.Fatalf("remove unreferenced output files: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("referenced output was removed: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent unreferenced output was removed: %v", err)
	}
	for _, path := range []string{stale, partial} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale output remains at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "keep-me.txt")); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}

func TestCleanupPurgesTerminalMetadataAfterThirtyDays(t *testing.T) {
	repo := &cleanupReportJobRepository{}
	flow := &CampaignAudienceReportJobFlowImpl{jobs: repo}
	before := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := flow.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if repo.purgeBefore.IsZero() {
		t.Fatal("cleanup did not purge terminal metadata")
	}
	if delta := repo.purgeBefore.Sub(before); delta < -time.Second || delta > time.Second {
		t.Fatalf("purge cutoff = %s, want approximately %s", repo.purgeBefore, before)
	}
}
