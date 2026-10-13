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

// Run against a disposable PostgreSQL database with
// YAMATA_TARGETING_TEST_POSTGRES_DSN='...' go test ./repository -run TargetingContentionIntegration.
func TestTargetingContentionIntegration(t *testing.T) {
	dsn := os.Getenv("YAMATA_TARGETING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("YAMATA_TARGETING_TEST_POSTGRES_DSN is not set")
	}
	bootstrap, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := bootstrap.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := fmt.Sprintf("targeting_contention_it_%d", time.Now().UnixNano())
	if err := bootstrap.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(dsn, schema)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	isolatedPool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer isolatedPool.Close()
	if err := db.Exec(`
 CREATE TABLE audience_profiles (id BIGINT PRIMARY KEY, phone_number TEXT, tags INTEGER[], color TEXT, normalized_score NUMERIC);
 CREATE TABLE campaigns (id INTEGER PRIMARY KEY, spec JSONB);
 CREATE TABLE line_numbers (line_number TEXT PRIMARY KEY, provider TEXT);
 CREATE TABLE campaign_targeting_test_sample_selections (
 id BIGSERIAL PRIMARY KEY, calculation_id BIGINT, campaign_id INTEGER REFERENCES campaigns(id), bundle_id INTEGER,
 generation BIGINT, input_hash CHAR(64), effective_audience_count BIGINT, created_at TIMESTAMPTZ);
 CREATE TABLE campaign_targeting_test_sample_selection_members (
 id BIGSERIAL PRIMARY KEY, selection_id BIGINT REFERENCES campaign_targeting_test_sample_selections(id),
 audience_id BIGINT REFERENCES audience_profiles(id), assigned_tag_id INTEGER, tag_selection_order BIGINT,
 selection_order BIGINT, audience_score NUMERIC, created_at TIMESTAMPTZ);
 CREATE TABLE bundle_audience_selections (id BIGINT PRIMARY KEY, campaign_id INTEGER REFERENCES campaigns(id), bundle_id INTEGER);
 CREATE TABLE bundle_audience_selection_members (id BIGSERIAL PRIMARY KEY, bundle_id INTEGER, audience_id BIGINT);
 CREATE TABLE bundle_audience_exclusions (bundle_id INTEGER, audience_id BIGINT, PRIMARY KEY(bundle_id, audience_id));
 CREATE TABLE campaign_targeting_test_sample_reservations (id BIGSERIAL PRIMARY KEY, bundle_id INTEGER, audience_id BIGINT, campaign_id INTEGER, state TEXT);
 CREATE TABLE campaign_targeting_execution_reservations (id BIGSERIAL PRIMARY KEY, bundle_id INTEGER, audience_id BIGINT, state TEXT);
 CREATE TABLE campaign_targeting_execution_calculations (id BIGINT PRIMARY KEY, bundle_id INTEGER, status TEXT);
 CREATE TABLE campaign_targeting_execution_calculation_members (calculation_id BIGINT, audience_id BIGINT);
 INSERT INTO campaigns VALUES (1, '{"platform":"sms"}');
 INSERT INTO audience_profiles VALUES (1, '09123456789', ARRAY[7], 'white', 1);
 `).Error; err != nil {
		t.Fatal(err)
	}
	score := 1.0
	selection := &models.CampaignTargetingTestSampleSelection{
		CalculationID: 1, CampaignID: 1, BundleID: 10, Generation: 1, EffectiveAudienceCount: 1,
		Members: []models.CampaignTargetingTestSampleSelectionMember{{AudienceID: 1, AssignedTagID: 7, AudienceScore: &score}},
	}
	if err := NewCampaignTargetingTestSampleSelectionRepository(db).Save(context.Background(), selection); err != nil {
		t.Fatalf("save immutable sample: %v", err)
	}
	t.Run("history cannot multiply invalid members and committed checks stay bundle scoped", func(t *testing.T) {
		if err := db.Exec(`INSERT INTO campaign_targeting_execution_calculations SELECT id, 20, 'ready' FROM generate_series(1, 1000) id;
 INSERT INTO campaign_targeting_execution_calculation_members SELECT id, 1 FROM generate_series(1, 1000) id;`).Error; err != nil {
			t.Fatal(err)
		}
		count := func(want int64) {
			t.Helper()
			var got int64
			if err := db.Raw(testSampleSelectionAvailabilityQuery, selection.ID, 10, 10, 1, 10, 10, 10).Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("unavailable = %d, want %d", got, want)
			}
		}
		count(0)
		if err := db.Exec(`UPDATE campaign_targeting_execution_calculations SET status = 'committed' WHERE id = 1`).Error; err != nil {
			t.Fatal(err)
		}
		count(0)
		if err := db.Exec(`UPDATE campaign_targeting_execution_calculations SET bundle_id = 10 WHERE id = 1`).Error; err != nil {
			t.Fatal(err)
		}
		count(1)
		if err := db.Exec(`UPDATE campaign_targeting_execution_calculations SET status = 'ready' WHERE id = 1`).Error; err != nil {
			t.Fatal(err)
		}
		count(0)
	})
}
