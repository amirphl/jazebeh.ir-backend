package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/gofiber/fiber/v3"
)

type expiredAudienceReportJobFlow struct {
	businessflow.CampaignAudienceReportJobFlow
	job *models.CampaignAudienceReportJob
}

func (f *expiredAudienceReportJobFlow) Get(context.Context, string) (*dto.CampaignAudienceReportJobResponse, *models.CampaignAudienceReportJob, error) {
	return &dto.CampaignAudienceReportJobResponse{ID: f.job.ID, Status: f.job.Status, ExpiresAt: f.job.ExpiresAt}, f.job, nil
}

func TestDownloadCampaignAudienceReportJobRejectsElapsedExpiryBeforeCleanup(t *testing.T) {
	expiresAt := time.Now().UTC().Add(-time.Second)
	flow := &expiredAudienceReportJobFlow{job: &models.CampaignAudienceReportJob{
		ID:         "job-id",
		Status:     models.CampaignAudienceReportJobCompleted,
		ExpiresAt:  &expiresAt,
		OutputPath: ptr("/path/that-must-not-be-read.xlsx"),
	}}
	handler := &CampaignHandler{reportJobFlow: flow}
	app := fiber.New()
	app.Get("/campaigns/audience-click-report/jobs/:id/download", handler.DownloadCampaignAudienceReportJob)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/campaigns/audience-click-report/jobs/job-id/download", nil))
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	if response.StatusCode != http.StatusGone {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusGone)
	}
}

func TestDownloadCampaignAudienceReportJobRejectsMissingOutput(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	flow := &expiredAudienceReportJobFlow{job: &models.CampaignAudienceReportJob{
		ID:         "job-id",
		Status:     models.CampaignAudienceReportJobCompleted,
		ExpiresAt:  &expiresAt,
		OutputPath: ptr("/path/that-does-not-exist.xlsx"),
	}}
	handler := &CampaignHandler{reportJobFlow: flow}
	app := fiber.New()
	app.Get("/campaigns/audience-click-report/jobs/:id/download", handler.DownloadCampaignAudienceReportJob)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/campaigns/audience-click-report/jobs/job-id/download", nil))
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func ptr(value string) *string { return &value }
