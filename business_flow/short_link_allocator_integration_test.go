package businessflow

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This opt-in test covers the workflow-level guarantee: a large admin upload
// persists its local allocation before publishing, and a publication retry
// reuses that allocation without reporting created = 0.
func TestAdminShortLinkAllocationPublicationRetryIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN is not set")
	}
	db := newAdminShortLinkIntegrationDB(t, dsn)
	runAdminShortLinkAllocatorMigration(t, db)

	const linkCount = 2_000 // crosses the 500-row publication batch boundary.
	var csv strings.Builder
	csv.WriteString("long_link\n")
	for index := 0; index < linkCount; index++ {
		fmt.Fprintf(&csv, "https://example.com/%d\n", index)
	}
	job := &models.AdminShortLinkUploadJob{
		ID:            uuid.NewString(),
		AllocationKey: strings.Repeat("b", 64),
		ScenarioID:    1,
		ScenarioName:  "allocator integration",
		Domain:        "https://jzbe.ir",
		CSVData:       []byte(csv.String()),
		Status:        "pending",
	}
	jobRepo := repository.NewAdminShortLinkUploadJobRepository(db)
	if err := jobRepo.Save(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	publisher := &adminUploadPublisherStub{err: fmt.Errorf("publisher unavailable")}
	flow := &AdminShortLinkFlowImpl{
		repo:      repository.NewShortLinkRepository(db),
		jobRepo:   jobRepo,
		publisher: publisher,
		db:        db,
	}

	if _, err := flow.processJob(context.Background(), job.ID); err == nil {
		t.Fatal("first publication unexpectedly succeeded")
	}
	failed, err := jobRepo.ByID(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed == nil || failed.Status != "retrying" || failed.Created != linkCount || failed.Published != 0 {
		t.Fatalf("failed job = %#v, want retrying with created=%d and published=0", failed, linkCount)
	}

	// Make the retry immediately claimable; production honors its backoff.
	if err := db.Exec(`UPDATE admin_short_link_upload_jobs SET next_attempt_at = CURRENT_TIMESTAMP WHERE id = ?`, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	publisher.err = nil
	if _, err := flow.processJob(context.Background(), job.ID); err != nil {
		t.Fatalf("publication retry: %v", err)
	}
	completed, err := jobRepo.ByID(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed == nil || completed.Status != "completed" || completed.Created != linkCount || completed.Published != linkCount {
		t.Fatalf("completed job = %#v, want completed with created=published=%d", completed, linkCount)
	}
	if publisher.calls < 2 {
		t.Fatalf("publisher calls = %d, want retry after failure", publisher.calls)
	}
	var links int64
	if err := db.Model(&models.ShortLink{}).Where("allocation_key = ?", job.AllocationKey).Count(&links).Error; err != nil {
		t.Fatal(err)
	}
	if links != linkCount {
		t.Fatalf("allocated rows = %d, want %d", links, linkCount)
	}
}

type adminUploadPublisherStub struct {
	err   error
	calls int
}

func (p *adminUploadPublisherStub) UploadMappings(_ context.Context, _ []*models.ShortLink) error {
	p.calls++
	return p.err
}

func newAdminShortLinkIntegrationDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	bootstrap, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("admin_short_link_it_%d", time.Now().UnixNano())
	if err := bootstrap.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() { _ = bootstrap.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error })
	db, err := gorm.Open(postgres.Open(adminShortLinkDSNWithSearchPath(dsn, schema)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	if err := db.Exec(`
CREATE TABLE short_links (
    id BIGSERIAL PRIMARY KEY,
    uid VARCHAR(64) NOT NULL UNIQUE,
    campaign_id BIGINT,
    client_id BIGINT,
    scenario_id BIGINT,
    scenario_name TEXT,
    phone_number VARCHAR(20),
    long_link TEXT NOT NULL,
    short_link TEXT NOT NULL,
    is_test BOOLEAN NOT NULL DEFAULT FALSE,
    external_published_at TIMESTAMPTZ,
    allocation_key VARCHAR(64),
    allocation_position INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uk_short_links_allocation_position ON short_links (allocation_key, allocation_position);
CREATE TABLE admin_short_link_upload_jobs (
    id VARCHAR(36) PRIMARY KEY,
    allocation_key VARCHAR(64) NOT NULL UNIQUE,
    scenario_id BIGINT NOT NULL UNIQUE,
    scenario_name TEXT NOT NULL,
    domain VARCHAR(255) NOT NULL,
    csv_data BYTEA NOT NULL,
    status VARCHAR(20) NOT NULL,
    total_rows INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    published INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    lease_token VARCHAR(36),
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);`).Error; err != nil {
		t.Fatalf("create integration tables: %v", err)
	}
	return db
}

func adminShortLinkDSNWithSearchPath(dsn, schema string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func runAdminShortLinkAllocatorMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	sql, err := os.ReadFile("../migrations/0163_create_short_link_uid_allocator.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(bytes.TrimSpace(sql))).Error; err != nil {
		t.Fatalf("run allocator migration: %v", err)
	}
}
