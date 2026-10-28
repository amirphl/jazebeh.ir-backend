package repository

import (
	"strings"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func currentRowFilterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=localhost user=test dbname=test sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	return db
}

func TestProcessedCampaignFilterCanSelectCurrentAndHistory(t *testing.T) {
	db := currentRowFilterTestDB(t)
	repo := &ProcessedCampaignRepositoryImpl{
		BaseRepository: NewBaseRepository[models.ProcessedCampaign, models.ProcessedCampaignFilter](db),
	}

	for _, current := range []bool{true, false} {
		var rows []*models.ProcessedCampaign
		statement := repo.applyFilter(
			db.Model(&models.ProcessedCampaign{}),
			models.ProcessedCampaignFilter{IsCurrent: &current},
		).Find(&rows).Statement
		if statement.Error != nil {
			t.Fatalf("build processed-campaign current=%t query: %v", current, statement.Error)
		}
		if sql := statement.SQL.String(); !strings.Contains(sql, "is_current =") {
			t.Fatalf("processed-campaign current=%t query has no current-row predicate:\n%s", current, sql)
		}
		if len(statement.Vars) != 1 || statement.Vars[0] != current {
			t.Fatalf("processed-campaign current=%t vars = %#v", current, statement.Vars)
		}
	}
}

func TestSentBaleMessageFilterCanSelectCurrentAndHistory(t *testing.T) {
	db := currentRowFilterTestDB(t)
	repo := &SentBaleMessageRepositoryImpl{
		BaseRepository: NewBaseRepository[models.SentBaleMessage, models.SentBaleMessageFilter](db),
	}

	for _, current := range []bool{true, false} {
		var rows []*models.SentBaleMessage
		statement := repo.applyFilter(
			db.Model(&models.SentBaleMessage{}),
			models.SentBaleMessageFilter{IsCurrent: &current},
		).Find(&rows).Statement
		if statement.Error != nil {
			t.Fatalf("build sent-Bale current=%t query: %v", current, statement.Error)
		}
		if sql := statement.SQL.String(); !strings.Contains(sql, "is_current =") {
			t.Fatalf("sent-Bale current=%t query has no current-row predicate:\n%s", current, sql)
		}
		if len(statement.Vars) != 1 || statement.Vars[0] != current {
			t.Fatalf("sent-Bale current=%t vars = %#v", current, statement.Vars)
		}
	}
}

func TestCurrentMarkersAreReadOnlySoDatabaseDefaultsApply(t *testing.T) {
	db := currentRowFilterTestDB(t)

	for name, value := range map[string]any{
		"processed campaign": models.ProcessedCampaign{},
		"sent Bale message":  models.SentBaleMessage{},
	} {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(value); err != nil {
			t.Fatalf("parse %s schema: %v", name, err)
		}
		field := statement.Schema.LookUpField("IsCurrent")
		if field == nil {
			t.Fatalf("%s has no IsCurrent field", name)
		}
		if field.Creatable || !field.Readable {
			t.Fatalf("%s IsCurrent permissions: creatable=%t readable=%t", name, field.Creatable, field.Readable)
		}
	}
}

func TestCampaignReadProjectionRetainsExecutionReservationVersion(t *testing.T) {
	t.Parallel()

	if !strings.Contains(statisticsWithoutTrackingResults, "campaigns.smart_targeting_execution_reservation_version") {
		t.Fatalf("campaign read projection omits execution reservation version: %s", statisticsWithoutTrackingResults)
	}
}
