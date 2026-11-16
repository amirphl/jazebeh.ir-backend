package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	businessflow "github.com/amirphl/Yamata-no-Orochi/business_flow"
	"github.com/gofiber/fiber/v3"
)

type currentExecutionCalculationFlow struct {
	businessflow.CampaignFlow
	called     bool
	customerID uint
	campaignID string
}

func (f *currentExecutionCalculationFlow) GetCurrentSmartTargetingExecutionCalculation(_ context.Context, customerID uint, campaignUUID string) (*dto.SmartTargetingExecutionCalculationResponse, error) {
	f.called, f.customerID, f.campaignID = true, customerID, campaignUUID
	return &dto.SmartTargetingExecutionCalculationResponse{CalculationID: 91, CampaignID: 44, BundleID: 7, Status: "pending"}, nil
}

func TestGetCurrentSmartTargetingExecutionCalculationHandler(t *testing.T) {
	flow := &currentExecutionCalculationFlow{}
	handler := &CampaignHandler{campaignFlow: flow}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("customer_id", uint(44))
		return c.Next()
	})
	app.Get("/campaigns/:uuid/smart-targeting/execution-audience-calculations", handler.GetCurrentSmartTargetingExecutionCalculation)

	request := httptest.NewRequest(http.MethodGet, "/campaigns/campaign-uuid/smart-targeting/execution-audience-calculations", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request current calculation: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if !flow.called || flow.customerID != 44 || flow.campaignID != "campaign-uuid" {
		t.Fatalf("flow call = called:%t customer:%d campaign:%q", flow.called, flow.customerID, flow.campaignID)
	}
}

func TestGetCurrentSmartTargetingExecutionCalculationHandlerRequiresCustomer(t *testing.T) {
	handler := &CampaignHandler{}
	app := fiber.New()
	app.Get("/campaigns/:uuid/smart-targeting/execution-audience-calculations", handler.GetCurrentSmartTargetingExecutionCalculation)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/campaigns/campaign-uuid/smart-targeting/execution-audience-calculations", nil))
	if err != nil {
		t.Fatalf("request without customer: %v", err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}
