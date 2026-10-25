package repository

import (
	"context"
	"testing"
	"time"
)

func TestCurrentForPhaseRejectsUnknownSelectionPhase(t *testing.T) {
	repo := &CampaignTargetingCapacityRepositoryImpl{db: newAudienceProfileDryRunDB(t)}
	_, err := repo.CurrentForPhase(
		context.Background(), 17, 3, "sms", SmartTargetingSelectionPhase("unknown"),
		[]int64{9}, []string{"A"}, []string{"white"}, 5, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("capacity lookup accepted an unknown phase and could infer execution semantics")
	}
}
