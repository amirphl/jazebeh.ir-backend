package repository

import (
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

func TestCampaignRepositoryTitleFiltersApplyToListAndCount(t *testing.T) {
	t.Parallel()

	db := newAudienceProfileDryRunDB(t)
	repo := &CampaignRepositoryImpl{
		BaseRepository: NewBaseRepository[models.Campaign, models.CampaignFilter](db),
	}
	campaignTitle := "launch"
	bundleTitle := "spring"
	filter := models.CampaignFilter{
		CampaignTitle: &campaignTitle,
		BundleTitle:   &bundleTitle,
	}

	var campaigns []*models.Campaign
	listStatement := repo.applyFilter(db.Model(&models.Campaign{}), filter).
		Order("campaigns.created_at DESC").
		Limit(10).
		Find(&campaigns).Statement
	assertCampaignTitleFilterSQL(t, listStatement.SQL.String())

	var count int64
	countStatement := repo.applyFilter(db.Model(&models.Campaign{}), filter).
		Count(&count).Statement
	assertCampaignTitleFilterSQL(t, countStatement.SQL.String())
}

func TestNonAutomatedClickTrafficSQLUsesAllBotSignals(t *testing.T) {
	condition := nonAutomatedClickTrafficSQL("click")

	for _, fragment := range []string{
		"click.is_test IS NOT TRUE",
		"COALESCE(click.ip, '') !~ '^(66\\.249\\.|74\\.125\\.)'",
		"COALESCE(click.user_agent, '') !~* '" + automatedClickUserAgentPattern + "'",
		"COALESCE(click.user_agent, '') ~* 'Chrome'",
		"COALESCE(click.user_agent, '') !~* '(Edg|OPR|Opera)'",
		"COALESCE(click.user_agent, '') ~* 'X11; Linux|Linux'",
		"COALESCE(click.user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'",
	} {
		if !strings.Contains(condition, fragment) {
			t.Fatalf("bot filter does not contain %q:\n%s", fragment, condition)
		}
	}
}

func TestTagPerformanceSQLUsesSharedBotFilter(t *testing.T) {
	for _, fragment := range []string{
		"COALESCE(click.ip, '') !~ '^(66\\.249\\.|74\\.125\\.)'",
		"COALESCE(click.user_agent, '') !~* '" + automatedClickUserAgentPattern + "'",
	} {
		if !strings.Contains(recomputeCampaignTagPerformanceSQL, fragment) {
			t.Fatalf("tag performance query does not contain %q", fragment)
		}
	}
}

func TestShortLinkClickExportsUseSharedBotFilter(t *testing.T) {
	db := newAudienceProfileDryRunDB(t)
	statement := realShortLinkClickTraffic(db.Table("short_link_clicks")).
		Where("scenario_id = ?", 42).
		Find(&[]ShortLinkWithClick{}).Statement
	if statement.Error != nil {
		t.Fatalf("build short-link click export query: %v", statement.Error)
	}

	sql := statement.SQL.String()
	for _, fragment := range []string{
		"is_test IS NOT TRUE",
		"COALESCE(user_agent, '') !~* '" + automatedClickUserAgentPattern + "'",
		"scenario_id = $1",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("short-link click export query does not contain %q:\n%s", fragment, sql)
		}
	}
}

func assertCampaignTitleFilterSQL(t *testing.T, sql string) {
	t.Helper()
	for _, fragment := range []string{
		"campaigns.spec->>'title' ILIKE",
		"LEFT JOIN bundles ON bundles.id = campaigns.bundle_id",
		"bundles.title ILIKE",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("campaign title filter query does not contain %q:\n%s", fragment, sql)
		}
	}
}
