package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestExternalShortLinkQuarantineIntegration is opt-in because it needs a
// disposable PostgreSQL database. It exercises the quarantine against real
// constraints: the partial unique index on positive click IDs must accept rows
// whose ID cannot serve as a cursor value, must not store a positive ID twice,
// and must never stop the cursor on a malformed page.
func TestExternalShortLinkQuarantineIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	// Create the tables from the models so the partial unique index under test is
	// the one the application declares.
	if err := db.AutoMigrate(
		&models.ShortLink{},
		&models.ShortLinkClick{},
		&models.ExternalShortLinkClickDeadLetter{},
		&models.ExternalShortLinkSyncState{},
	); err != nil {
		t.Fatalf("migrate disposable schema: %v", err)
	}
	repo := NewExternalShortLinkSyncRepository(db)
	ctx := context.Background()
	source := "integration-quarantine-" + uuid.NewString()
	uid := "integration-code-" + uuid.NewString()
	longURL := "https://example.com/destination"
	if err := db.Exec(
		"INSERT INTO short_links (uid, long_link, short_link) VALUES (?, ?, ?)",
		uid, longURL, "https://jzbe.ir/"+uid,
	).Error; err != nil {
		t.Fatalf("seed short link: %v", err)
	}
	t.Cleanup(func() {
		db.Where("source = ?", source).Delete(&models.ExternalShortLinkClickDeadLetter{})
		db.Where("source = ?", source).Delete(&models.ExternalShortLinkSyncState{})
		db.Where("uid = ?", uid).Delete(&models.ShortLinkClick{})
		db.Exec("DELETE FROM short_links WHERE uid = ?", uid)
	})

	mapped := models.ExternalShortLinkClick{
		ShortCode: uid,
		LinkID:    1,
		LongURL:   longURL,
		ClickedAt: time.Now().UTC(),
	}
	page := []models.ExternalShortLinkClick{
		withClickID(mapped, 12), // imported
		withClickID(mapped, 12), // repeated: cannot be a second row
		withClickID(mapped, 11), // lowered: cannot be ordered behind 12
		withClickID(mapped, 0),  // unusable as a cursor value
	}
	if err := repo.ImportPage(ctx, source, page, 12); err != nil {
		t.Fatalf("ImportPage() error = %v", err)
	}
	cursor, err := repo.Cursor(ctx, source)
	if err != nil {
		t.Fatalf("Cursor() error = %v", err)
	}
	if cursor != 12 {
		t.Fatalf("cursor = %d, want 12: a malformed page must not stop the cursor", cursor)
	}

	var imported []models.ShortLinkClick
	if err := db.Where("source = ?", source).Find(&imported).Error; err != nil {
		t.Fatalf("load imported clicks: %v", err)
	}
	if len(imported) != 1 || imported[0].ExternalClickID == nil || *imported[0].ExternalClickID != 12 {
		t.Fatalf("imported clicks = %v, want exactly the click with external id 12", imported)
	}

	stored := loadDeadLetters(t, db, source)
	if len(stored) != 3 {
		t.Fatalf("quarantined %v, want ids 0, 11, and 12", stored)
	}
	for _, want := range []struct {
		clickID int64
		reason  string
	}{
		{clickID: 0, reason: "external click ID is not a positive integer"},
		{clickID: 11, reason: "external click ID is not strictly increasing"},
		{clickID: 12, reason: "external click ID is not strictly increasing"},
	} {
		if got := stored[want.clickID]; got != want.reason {
			t.Fatalf("click id %d reason = %q, want %q", want.clickID, got, want.reason)
		}
	}

	// Re-importing the same page must stay idempotent for the IDs the cursor can
	// represent, otherwise a retry would alert forever.
	if err := repo.ImportPage(ctx, source, page, 12); err != nil {
		t.Fatalf("second ImportPage() error = %v", err)
	}
	if stored = loadDeadLetters(t, db, source); len(stored) != 3 {
		t.Fatalf("after a retry: %v, want the same three positive-keyed rows", stored)
	}

	// A page whose every ID is at or below the cursor cannot advance it, but it
	// still has to be classified rather than aborting the import.
	below := []models.ExternalShortLinkClick{withClickID(mapped, 5)}
	if err := repo.ImportPage(ctx, source, below, 12); err != nil {
		t.Fatalf("ImportPage() below the cursor error = %v", err)
	}
	if cursor, err = repo.Cursor(ctx, source); err != nil || cursor != 12 {
		t.Fatalf("cursor = %d (err=%v), want 12: the cursor must never rewind", cursor, err)
	}
}

func withClickID(click models.ExternalShortLinkClick, clickID int64) models.ExternalShortLinkClick {
	click.ClickID = clickID
	return click
}

func loadDeadLetters(t *testing.T, db *gorm.DB, source string) map[int64]string {
	t.Helper()
	var rows []models.ExternalShortLinkClickDeadLetter
	if err := db.Where("source = ?", source).Order("external_click_id").Find(&rows).Error; err != nil {
		t.Fatalf("load quarantined rows: %v", err)
	}
	stored := make(map[int64]string, len(rows))
	for _, row := range rows {
		stored[row.ExternalClickID] = row.Reason
	}
	return stored
}
