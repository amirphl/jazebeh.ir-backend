package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestShortLinkExternalPublicationIntegration is opt-in because it needs a
// disposable PostgreSQL database. It locks in that acknowledging a publication
// writes only the publication marker: updated_at is what the mapping scheduler
// re-sends as source_updated_at, so a bump here would make publish time
// indistinguishable from a mapping change downstream.
func TestShortLinkExternalPublicationIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN is not set")
	}
	db := newShortLinkPublicationIntegrationDB(t, dsn)
	repo := NewShortLinkRepository(db)
	ctx := context.Background()

	sentinel := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, uid := range []string{"publication-1", "publication-2"} {
		if err := db.Exec(
			`INSERT INTO short_links (uid, long_link, short_link, created_at, updated_at)
			 VALUES (?, 'https://example.com/destination', 'https://jzbe.ir/' || ?, ?, ?)`,
			uid, uid, sentinel, sentinel,
		).Error; err != nil {
			t.Fatalf("seed %s: %v", uid, err)
		}
	}

	pending, err := repo.ListPendingExternalPublication(ctx, 100)
	if err != nil {
		t.Fatalf("ListPendingExternalPublication() error = %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending publications = %d, want 2", len(pending))
	}

	publishedAt := time.Date(2026, time.March, 4, 5, 6, 7, 123456000, time.UTC)
	if err := repo.MarkExternallyPublished(ctx, []string{"publication-1"}, publishedAt); err != nil {
		t.Fatalf("MarkExternallyPublished() error = %v", err)
	}

	var marked, untouched struct {
		UID                 string
		UpdatedAt           time.Time
		ExternalPublishedAt *time.Time
	}
	if err := db.Raw(
		"SELECT uid, updated_at, external_published_at FROM short_links WHERE uid = ?", "publication-1",
	).Scan(&marked).Error; err != nil {
		t.Fatalf("read published row: %v", err)
	}
	if marked.ExternalPublishedAt == nil {
		t.Fatal("external_published_at was not recorded")
	}
	if !marked.ExternalPublishedAt.UTC().Truncate(time.Microsecond).Equal(publishedAt) {
		t.Fatalf("external_published_at = %s, want %s", marked.ExternalPublishedAt, publishedAt)
	}
	if !marked.UpdatedAt.UTC().Equal(sentinel) {
		t.Fatalf("publishing changed updated_at to %s, want it kept at %s: the mapping scheduler would re-send it as source_updated_at", marked.UpdatedAt, sentinel)
	}

	if err := db.Raw(
		"SELECT uid, updated_at, external_published_at FROM short_links WHERE uid = ?", "publication-2",
	).Scan(&untouched).Error; err != nil {
		t.Fatalf("read unmarked row: %v", err)
	}
	if untouched.ExternalPublishedAt != nil {
		t.Fatalf("publication-2 was marked although only publication-1 was published: %v", untouched.ExternalPublishedAt)
	}
	if !untouched.UpdatedAt.UTC().Equal(sentinel) {
		t.Fatalf("publication-2 updated_at = %s, want it kept at %s", untouched.UpdatedAt, sentinel)
	}

	pending, err = repo.ListPendingExternalPublication(ctx, 100)
	if err != nil {
		t.Fatalf("second ListPendingExternalPublication() error = %v", err)
	}
	if len(pending) != 1 || pending[0].UID != "publication-2" {
		t.Fatalf("pending after publishing = %v, want only publication-2", pending)
	}
}

func newShortLinkPublicationIntegrationDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	bootstrap, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("short_link_publication_it_%d", time.Now().UnixNano())
	if err := bootstrap.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() { _ = bootstrap.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error })

	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(dsn, schema)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	// The schema comes from the model so the partial pending-publication index
	// under test is the one the application declares.
	if err := db.AutoMigrate(&models.ShortLink{}); err != nil {
		t.Fatalf("migrate isolated schema: %v", err)
	}
	return db
}
