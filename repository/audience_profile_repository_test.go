package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newAudienceProfileDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	return db
}

func TestAudienceProfilesByUIDsQueryUsesOneArrayParameter(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	uids := make([]string, 50_000)
	for i := range uids {
		uids[i] = fmt.Sprintf("uid-%d", i)
	}

	var rows []*models.AudienceProfile
	statement := audienceProfilesByUIDsQuery(db, uids).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build UID query: %v", statement.Error)
	}
	sql := statement.SQL.String()
	if !strings.Contains(sql, "uid = ANY($1::varchar[])") {
		t.Fatalf("UID query does not use a typed ANY parameter:\n%s", sql)
	}
	if strings.Contains(sql, "SELECT *") {
		t.Fatalf("UID query unexpectedly selects every column:\n%s", sql)
	}
	if got, want := len(statement.Vars), 1; got != want {
		t.Fatalf("bind parameter count = %d, want %d", got, want)
	}
	if values, ok := statement.Vars[0].(pq.StringArray); !ok || len(values) != len(uids) {
		t.Fatalf("UID bind value = %T with unexpected length", statement.Vars[0])
	}
}

func TestCampaignCandidatesQueryLimitsAndExcludesInDatabase(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	repo := &AudienceProfileRepositoryImpl{}
	tags := pq.Int32Array{10, 20}
	color := "white"
	minimumScore := 29.6
	excluded := make([]int64, 50_000)
	for i := range excluded {
		excluded[i] = int64(i + 1)
	}

	var rows []*models.AudienceProfile
	statement := repo.campaignCandidatesQuery(db, models.AudienceProfileFilter{
		Tags:            &tags,
		Color:           &color,
		NormalizedScore: &models.NormalizedScoreConstraint{GTE: &minimumScore},
	}, excluded, 25_000).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build candidate query: %v", statement.Error)
	}
	sql := statement.SQL.String()
	for _, fragment := range []string{
		"SELECT \"id\",\"uid\",\"phone_number\"",
		"tags &&",
		"phone_number IS NOT NULL",
		"BTRIM(phone_number) <> ''",
		"FROM unnest($4::bigint[])",
		"excluded.id = audience_profiles.id",
		"ORDER BY id DESC",
		"LIMIT $5",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("candidate query does not contain %q:\n%s", fragment, sql)
		}
	}
	if got, want := len(statement.Vars), 5; got != want {
		t.Fatalf("bind parameter count = %d, want %d", got, want)
	}
	if values, ok := statement.Vars[3].(pq.Int64Array); !ok || len(values) != len(excluded) {
		t.Fatalf("excluded-ID bind value = %T with unexpected length", statement.Vars[3])
	}
}

func TestCampaignCandidatesExcludeBundleUsageWithoutPlatformFilter(t *testing.T) {
	t.Parallel()
	db := newAudienceProfileDryRunDB(t)
	repo := &AudienceProfileRepositoryImpl{}
	tags := pq.Int32Array{10}
	bundleID := uint(44)
	var rows []*models.AudienceProfile
	statement := repo.campaignCandidatesQuery(db, models.AudienceProfileFilter{
		Tags: &tags, ExcludeBundleID: &bundleID,
	}, nil, 100).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build bundle candidate query: %v", statement.Error)
	}
	sql := strings.ToLower(statement.SQL.String())
	for _, required := range []string{"bundle_audience_selection_members", "used.bundle_id", "used.audience_id"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("bundle candidate query missing %q:\n%s", required, sql)
		}
	}
	if strings.Contains(sql, "color") || strings.Contains(sql, "platform") {
		t.Fatalf("bundle candidate query contains a platform-dependent filter:\n%s", sql)
	}
}

func TestAudienceProfileScoreConstraintSupportsDisjointGradeBoundaries(t *testing.T) {
	t.Parallel()
	db := newAudienceProfileDryRunDB(t)
	repo := &AudienceProfileRepositoryImpl{}
	p33, p66 := 24.0, 29.6

	tests := []struct {
		name       string
		constraint *models.NormalizedScoreConstraint
		wantSQL    string
	}{
		{name: "A", constraint: &models.NormalizedScoreConstraint{GT: &p66}, wantSQL: "normalized_score > $1"},
		{
			name:       "B",
			constraint: &models.NormalizedScoreConstraint{GT: &p33, LTE: &p66},
			wantSQL:    "normalized_score > $1 AND normalized_score <= $2",
		},
		{
			name:       "A or C",
			constraint: &models.NormalizedScoreConstraint{LTE: &p33, OrGT: &p66},
			wantSQL:    "normalized_score <= $1 OR normalized_score > $2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rows []*models.AudienceProfile
			statement := repo.applyFilter(db.Model(&models.AudienceProfile{}), models.AudienceProfileFilter{NormalizedScore: tt.constraint}).
				Find(&rows).
				Statement
			if statement.Error != nil {
				t.Fatalf("build score query: %v", statement.Error)
			}
			if sql := statement.SQL.String(); !strings.Contains(sql, tt.wantSQL) {
				t.Fatalf("score query does not contain %q:\n%s", tt.wantSQL, sql)
			}
		})
	}
}
