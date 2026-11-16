package router

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionCalculationCollectionRoutePrecedesIDRoute(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	text := string(source)
	collection := `campaigns.Get("/:uuid/smart-targeting/execution-audience-calculations", r.campaignHandler.GetCurrentSmartTargetingExecutionCalculation)`
	byID := `campaigns.Get("/:uuid/smart-targeting/execution-audience-calculations/:calculation_id", r.campaignHandler.GetSmartTargetingExecutionCalculation)`
	collectionIndex, byIDIndex := strings.Index(text, collection), strings.Index(text, byID)
	if collectionIndex < 0 || byIDIndex < 0 {
		t.Fatalf("execution calculation polling routes are missing")
	}
	if collectionIndex > byIDIndex {
		t.Fatal("collection route must be registered before the by-ID route")
	}
}
