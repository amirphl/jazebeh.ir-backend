package repository

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSmartTagOrderAllowlist(t *testing.T) {
	tests := []struct {
		sortBy    string
		direction string
		want      string
	}{
		{"database_order", "asc", "available_tags.tag_id ASC"},
		{"tag_capacity", "desc", "available_tags.tag_audience_count DESC NULLS LAST, available_tags.tag_id ASC"},
		{"bundle_persona_fit_score", "asc", "available_tags.bundle_persona_fit_score ASC NULLS LAST, available_tags.tag_id ASC"},
		{"test_phase_avg_ctr", "desc", "tag_test_summary.test_phase_avg_ctr DESC NULLS LAST, available_tags.tag_id ASC"},
		{"overall_avg_ctr", "desc", "tag_overall_summary.overall_avg_ctr DESC NULLS LAST, available_tags.tag_id ASC"},
		{"execution_default", "desc", "tag_test_summary.test_phase_avg_ctr DESC NULLS LAST,\navailable_tags.bundle_persona_fit_score DESC NULLS LAST,\navailable_tags.tag_id ASC"},
	}
	for _, tt := range tests {
		got, err := smartTagOrder(tt.sortBy, tt.direction)
		if err != nil || got != tt.want {
			t.Fatalf("smartTagOrder(%q, %q) = %q, %v; want %q", tt.sortBy, tt.direction, got, err, tt.want)
		}
	}
	if _, err := smartTagOrder("tags.id; DROP TABLE tags", "asc"); err == nil {
		t.Fatal("expected arbitrary sort expression to be rejected")
	}
	if _, err := smartTagOrder("tag_capacity", "sideways"); err == nil {
		t.Fatal("expected invalid direction to be rejected")
	}
}

func TestEscapeLike(t *testing.T) {
	if got, want := escapeLike(`50%_off\today`), `50\%\_off\\today`; got != want {
		t.Fatalf("escapeLike() = %q, want %q", got, want)
	}
}

func TestAvailableSmartTagsQueryPrefersScoreSnapshotWithCatalogFallback(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	var rows []uint
	statement := availableSmartTagsQuery(db, 42).
		Select("available_tags.tag_id").
		Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build available-tag query: %v", statement.Error)
	}
	sql := statement.SQL.String()
	for _, fragment := range []string{
		"FROM current_bundle_tag_scores AS scores",
		"JOIN tags AS scored_tags",
		"scored_tags.is_active = TRUE",
		"scores.tag_name_snapshot",
		"scores.tag_persona_snapshot",
		"scores.tag_audience_count_snapshot",
		"UNION ALL",
		"FROM tags",
		"NOT EXISTS",
		"FROM current_bundle_tag_scores AS existing_scores",
		"existing_tags.is_active = TRUE",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("available-tag query does not contain %q:\n%s", fragment, sql)
		}
	}
	if got, want := len(statement.Vars), 2; got != want {
		t.Fatalf("bundle bind count = %d, want %d", got, want)
	}
	for i, variable := range statement.Vars {
		if variable != uint(42) {
			t.Fatalf("bundle bind %d = %#v, want 42", i, variable)
		}
	}
}

func TestBaseAvailableSmartTagsQueryReadsMaterializedTestPerformance(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	repo := &CampaignSelectedTagRepositoryImpl{db: db}

	var rows []uint
	statement := repo.baseAvailableQuery(t.Context(), 42, "", nil).
		Select("available_tags.tag_id").
		Order("tag_test_summary.test_phase_avg_ctr DESC NULLS LAST").
		Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build materialized tag-performance query: %v", statement.Error)
	}
	sql := statement.SQL.String()
	for _, fragment := range []string{
		"LEFT JOIN tag_test_phase_performance_summaries AS tag_test_summary",
		"LEFT JOIN tag_overall_performance_summaries AS tag_overall_summary",
		"tag_test_summary.bundle_id = $3",
		"tag_overall_summary.tag_id = available_tags.tag_id",
		"ORDER BY tag_test_summary.test_phase_avg_ctr DESC NULLS LAST",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("materialized tag-performance query does not contain %q:\n%s", fragment, sql)
		}
	}
}

func TestBaseAvailableSmartTagsQueryFiltersCapacityStrictly(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	repo := &CampaignSelectedTagRepositoryImpl{db: db}
	capacity := int64(100)
	var rows []uint
	statement := repo.baseAvailableQuery(t.Context(), 42, "", &capacity).
		Select("available_tags.tag_id").
		Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build capacity-filtered tag query: %v", statement.Error)
	}
	if !strings.Contains(statement.SQL.String(), "available_tags.tag_audience_count > $4") {
		t.Fatalf("capacity query is not strict-greater-than:\n%s", statement.SQL.String())
	}
	if got, want := statement.Vars[len(statement.Vars)-1], capacity; got != want {
		t.Fatalf("capacity bind = %#v, want %d", got, want)
	}
}

func TestCampaignTestPerformanceJoinIsBundleScoped(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	repo := &CampaignSelectedTagRepositoryImpl{db: db}

	var rows []uint
	statement := applyCampaignTestPerformance(repo.baseAvailableQuery(t.Context(), 42, "", nil), 77, 42).
		Select("available_tags.tag_id").
		Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build Campaign tag-performance query: %v", statement.Error)
	}
	sql := statement.SQL.String()
	for _, fragment := range []string{
		"campaign_test_performance.campaign_id = $4",
		"campaign_test_performance.bundle_id = $5",
		"campaign_test_performance.tag_id = available_tags.tag_id",
		"campaign_test_performance.phase_type = 'test'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("Campaign tag-performance query does not contain %q:\n%s", fragment, sql)
		}
	}
}

func TestBuildCampaignSelectedTagRowsPreservesRequestOrder(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	measuredCTR := 0.08
	overallCTR := 0.11
	snapshots := []campaignSelectedTagSnapshot{
		{TagID: 2},
		{TagID: 5, TestPhaseAvgCTR: &measuredCTR, OverallAvgCTR: &overallCTR},
		{TagID: 9},
	}

	rows, err := buildCampaignSelectedTagRows(17, 3, 7, []uint{9, 2, 5}, snapshots, now)
	if err != nil {
		t.Fatalf("build selected-tag rows: %v", err)
	}
	gotIDs := make([]uint, 0, len(rows))
	gotPositions := make([]int, 0, len(rows))
	for _, row := range rows {
		gotIDs = append(gotIDs, row.TagID)
		gotPositions = append(gotPositions, row.SelectionOrder)
	}
	if want := []uint{9, 2, 5}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("persisted tag IDs = %v, want %v", gotIDs, want)
	}
	if want := []int{0, 1, 2}; !reflect.DeepEqual(gotPositions, want) {
		t.Fatalf("selection positions = %v, want %v", gotPositions, want)
	}
	if rows[2].TestPhaseAvgCTRSnapshot == nil || *rows[2].TestPhaseAvgCTRSnapshot != measuredCTR {
		t.Fatalf("tag 5 Test CTR snapshot = %v, want %v", rows[2].TestPhaseAvgCTRSnapshot, measuredCTR)
	}
	if rows[2].OverallAvgCTRSnapshot == nil || *rows[2].OverallAvgCTRSnapshot != overallCTR {
		t.Fatalf("tag 5 overall CTR snapshot = %v, want %v", rows[2].OverallAvgCTRSnapshot, overallCTR)
	}

	if _, err := buildCampaignSelectedTagRows(17, 3, 7, []uint{9, 4}, snapshots, now); !errors.Is(err, ErrInvalidCampaignSelectedTags) {
		t.Fatalf("missing snapshot error = %v, want ErrInvalidCampaignSelectedTags", err)
	}
}

func TestOrderedCampaignSelectedTagsQueryUsesPersistedOrder(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	var rows []*models.CampaignSelectedTag
	statement := orderedCampaignSelectedTagsQuery(db, 17).Find(&rows).Statement
	if statement.Error != nil {
		t.Fatalf("build ordered selected-tag query: %v", statement.Error)
	}
	if sql := statement.SQL.String(); !strings.Contains(sql, "ORDER BY selection_order ASC, id ASC") {
		t.Fatalf("selected-tag query does not preserve persisted order:\n%s", sql)
	}
}

func TestSameOrderedCampaignSelectedTags(t *testing.T) {
	current := []*models.CampaignSelectedTag{
		{TagID: 9, SelectionOrder: 0},
		{TagID: 2, SelectionOrder: 1},
		{TagID: 5, SelectionOrder: 2},
	}
	if !sameOrderedCampaignSelectedTags(current, []uint{9, 2, 5}) {
		t.Fatal("identical ordered selection must be treated as unchanged")
	}
	if sameOrderedCampaignSelectedTags(current, []uint{2, 9, 5}) {
		t.Fatal("reordered selection must be treated as changed")
	}
	if sameOrderedCampaignSelectedTags(current, []uint{9, 2}) {
		t.Fatal("shorter selection must be treated as changed")
	}
	current[1].SelectionOrder = 7
	if sameOrderedCampaignSelectedTags(current, []uint{9, 2, 5}) {
		t.Fatal("invalid persisted positions must not bypass replacement")
	}
}
