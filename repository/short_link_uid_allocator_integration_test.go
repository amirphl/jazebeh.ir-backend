package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// These tests exercise PostgreSQL locking and transaction semantics that a
// formatter-only test cannot cover. They are opt-in because they create and
// drop an isolated schema in the database named by this environment variable.
// Run with: YAMATA_SHORT_LINK_TEST_POSTGRES_DSN='...' go test ./repository -run ShortLinkUIDAllocatorIntegration
func TestShortLinkUIDAllocatorIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_SHORT_LINK_TEST_POSTGRES_DSN is not set")
	}

	t.Run("migration initialization skips an existing 100000", func(t *testing.T) {
		db := newShortLinkUIDAllocatorIntegrationDB(t, dsn)
		if err := db.Exec(`INSERT INTO short_links (uid) VALUES ('100000')`).Error; err != nil {
			t.Fatal(err)
		}
		runShortLinkUIDAllocatorMigration(t, db)

		var next int64
		if err := db.Raw(`SELECT next_value FROM short_link_uid_allocator WHERE allocator_name = 'default'`).Scan(&next).Error; err != nil {
			t.Fatal(err)
		}
		if next != 36*36*36*36*36 {
			t.Fatalf("migration next_value = %d, want %d", next, 36*36*36*36*36)
		}
		codes := reserveShortLinkUIDs(t, db, 1)
		if got, want := codes[0], "100001"; got != want {
			t.Fatalf("first collision-safe code = %q, want %q", got, want)
		}
	})

	t.Run("rollback preserves next value", func(t *testing.T) {
		db := newShortLinkUIDAllocatorIntegrationDB(t, dsn)
		runShortLinkUIDAllocatorMigration(t, db)
		rollback := errors.New("rollback reservation")
		err := WithTransaction(context.Background(), db, func(txCtx context.Context) error {
			codes, err := NewShortLinkRepository(db).ReserveSequentialUIDs(txCtx, 1)
			if err != nil {
				return err
			}
			if codes[0] != "100000" {
				return fmt.Errorf("reserved %q, want 100000", codes[0])
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("rollback transaction error = %v, want %v", err, rollback)
		}
		if got := reserveShortLinkUIDs(t, db, 1)[0]; got != "100000" {
			t.Fatalf("code after rollback = %q, want 100000", got)
		}
	})

	t.Run("FOR UPDATE serializes concurrent reservations", func(t *testing.T) {
		db := newShortLinkUIDAllocatorIntegrationDB(t, dsn)
		runShortLinkUIDAllocatorMigration(t, db)
		dbSQL, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		dbSQL.SetMaxOpenConns(4)

		firstReserved := make(chan struct{})
		releaseFirst := make(chan struct{})
		firstDone := make(chan reservationResult, 1)
		secondDone := make(chan reservationResult, 1)
		reserve := func(hold <-chan struct{}, reserved chan<- struct{}, done chan<- reservationResult) {
			var codes []string
			err := WithTransaction(context.Background(), db, func(txCtx context.Context) error {
				var err error
				codes, err = NewShortLinkRepository(db).ReserveSequentialUIDs(txCtx, 1)
				if err != nil {
					return err
				}
				if reserved != nil {
					close(reserved)
				}
				if hold != nil {
					<-hold
				}
				return nil
			})
			done <- reservationResult{codes: codes, err: err}
		}
		go reserve(releaseFirst, firstReserved, firstDone)
		select {
		case <-firstReserved:
		case <-time.After(5 * time.Second):
			t.Fatal("first transaction did not reserve")
		}
		go reserve(nil, nil, secondDone)
		select {
		case result := <-secondDone:
			t.Fatalf("second reservation completed before first committed: %#v", result)
		case <-time.After(150 * time.Millisecond):
		}
		close(releaseFirst)
		first := <-firstDone
		second := <-secondDone
		if first.err != nil || second.err != nil {
			t.Fatalf("concurrent reservations failed: first=%v second=%v", first.err, second.err)
		}
		if first.codes[0] != "100000" || second.codes[0] != "100001" {
			t.Fatalf("serialized codes = %v then %v, want 100000 then 100001", first.codes, second.codes)
		}
	})

	t.Run("duplicate allocation-key race rolls back the losing reservation", func(t *testing.T) {
		db := newShortLinkUIDAllocatorIntegrationDB(t, dsn)
		runShortLinkUIDAllocatorMigration(t, db)
		key := strings.Repeat("a", 64)
		firstInserted := make(chan struct{})
		releaseFirst := make(chan struct{})
		firstDone := make(chan error, 1)
		secondDone := make(chan error, 1)
		insert := func(hold <-chan struct{}, inserted chan<- struct{}, done chan<- error) {
			done <- WithTransaction(context.Background(), db, func(txCtx context.Context) error {
				codes, err := NewShortLinkRepository(db).ReserveSequentialUIDs(txCtx, 1)
				if err != nil {
					return err
				}
				if err := transactionDB(t, txCtx).Exec(`INSERT INTO short_links (uid, allocation_key, allocation_position) VALUES (?, ?, 0)`, codes[0], key).Error; err != nil {
					return err
				}
				if inserted != nil {
					close(inserted)
				}
				if hold != nil {
					<-hold
				}
				return nil
			})
		}
		go insert(releaseFirst, firstInserted, firstDone)
		select {
		case <-firstInserted:
		case <-time.After(5 * time.Second):
			t.Fatal("first allocation did not insert")
		}
		go insert(nil, nil, secondDone)
		select {
		case result := <-secondDone:
			t.Fatalf("losing allocation completed before winner committed: %v", result)
		case <-time.After(150 * time.Millisecond):
		}
		close(releaseFirst)
		if err := <-firstDone; err != nil {
			t.Fatalf("winning allocation: %v", err)
		}
		if err := <-secondDone; err == nil {
			t.Fatal("duplicate allocation key unexpectedly committed")
		}
		if got := reserveShortLinkUIDs(t, db, 1)[0]; got != "100001" {
			t.Fatalf("code after duplicate-key rollback = %q, want 100001", got)
		}
	})

	t.Run("large reservation remains fixed width and sequential", func(t *testing.T) {
		db := newShortLinkUIDAllocatorIntegrationDB(t, dsn)
		runShortLinkUIDAllocatorMigration(t, db)
		codes := reserveShortLinkUIDs(t, db, 20_000)
		if len(codes) != 20_000 || codes[0] != "100000" {
			t.Fatalf("large reservation starts %q with %d rows", codes[0], len(codes))
		}
		wantLast, err := models.FormatShortLinkUID(36*36*36*36*36 + 19_999)
		if err != nil {
			t.Fatal(err)
		}
		if codes[len(codes)-1] != wantLast {
			t.Fatalf("large reservation last code = %q, want %q", codes[len(codes)-1], wantLast)
		}
	})
}

type reservationResult struct {
	codes []string
	err   error
}

func newShortLinkUIDAllocatorIntegrationDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	bootstrap, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("short_link_allocator_it_%d", time.Now().UnixNano())
	if err := bootstrap.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() { _ = bootstrap.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error })

	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(dsn, schema)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	if err := db.Exec(`
CREATE TABLE short_links (
    id BIGSERIAL PRIMARY KEY,
    uid VARCHAR(64) NOT NULL UNIQUE,
    allocation_key VARCHAR(64),
    allocation_position INTEGER
);
CREATE UNIQUE INDEX uk_short_links_allocation_position
    ON short_links (allocation_key, allocation_position);`).Error; err != nil {
		t.Fatalf("create short-link integration tables: %v", err)
	}
	return db
}

func postgresDSNWithSearchPath(dsn, schema string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func runShortLinkUIDAllocatorMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	sql, err := os.ReadFile("../migrations/0163_create_short_link_uid_allocator.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(sql)).Error; err != nil {
		t.Fatalf("run allocator migration: %v", err)
	}
}

func reserveShortLinkUIDs(t *testing.T, db *gorm.DB, count int) []string {
	t.Helper()
	var codes []string
	if err := WithTransaction(context.Background(), db, func(txCtx context.Context) error {
		var err error
		codes, err = NewShortLinkRepository(db).ReserveSequentialUIDs(txCtx, count)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return codes
}

func reserveAndInsertAllocation(t *testing.T, db *gorm.DB, key string) string {
	t.Helper()
	var code string
	if err := WithTransaction(context.Background(), db, func(txCtx context.Context) error {
		codes, err := NewShortLinkRepository(db).ReserveSequentialUIDs(txCtx, 1)
		if err != nil {
			return err
		}
		code = codes[0]
		return transactionDB(t, txCtx).Exec(`INSERT INTO short_links (uid, allocation_key, allocation_position) VALUES (?, ?, 0)`, code, key).Error
	}); err != nil {
		t.Fatal(err)
	}
	return code
}

func transactionDB(t *testing.T, ctx context.Context) *gorm.DB {
	t.Helper()
	tx, ok := ctx.Value(TxContextKey).(*gorm.DB)
	if !ok || tx == nil {
		t.Fatal("transaction context has no database handle")
	}
	return tx
}
