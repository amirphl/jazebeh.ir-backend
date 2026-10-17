package scheduler

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
)

func TestAssignFirstMatchingTagsUsesPersistedSelectionOrder(t *testing.T) {
	rows := []*models.AudienceProfile{
		{ID: 1, Tags: pq.Int32Array{2, 9}},
		{ID: 2, Tags: pq.Int32Array{2}},
	}
	got := assignFirstMatchingTags(rows, []int64{9, 2})
	if len(got) != 2 || got[0] != 9 || got[1] != 2 {
		t.Fatalf("assigned tags = %v, want [9 2]", got)
	}
}

func TestSchedulerConfiguredAudienceCountIgnoresNumAudienceForSmartTest(t *testing.T) {
	testPhase := string(models.CampaignPhaseTest)
	sampleSize := uint64(600)
	compatibilityCount := uint64(1)
	campaign := dto.BotGetCampaignResponse{
		ID:                                17,
		TargetingMethod:                   models.CampaignAudienceTargetingSmart,
		Phase:                             &testPhase,
		SampleSizePerTag:                  &sampleSize,
		SmartTargetingTestSatisfiedTagIDs: []uint{9, 2},
		NumAudiences:                      &compatibilityCount,
	}
	got, err := schedulerConfiguredAudienceCount(campaign)
	if err != nil || got != 1_200 {
		t.Fatalf("Smart Test configured count = (%d, %v), want (1200, nil)", got, err)
	}

	executionPhase := string(models.CampaignPhaseExecution)
	executionCount := uint64(12_000)
	campaign.Phase = &executionPhase
	campaign.NumAudiences = &executionCount
	got, err = schedulerConfiguredAudienceCount(campaign)
	if err != nil || got != 12_000 {
		t.Fatalf("execution configured count = (%d, %v), want (12000, nil)", got, err)
	}
}

func TestSmartTargetingTestSamplingTagIDsPreservePersistedOrder(t *testing.T) {
	phase := string(models.CampaignPhaseTest)
	campaign := dto.BotGetCampaignResponse{
		ID:                                19,
		TargetingMethod:                   models.CampaignAudienceTargetingSmart,
		Phase:                             &phase,
		SmartTargetingTestSatisfiedTagIDs: []uint{9, 5},
	}
	got, err := smartTargetingTestSamplingTagIDs(campaign, []uint{9, 2, 5})
	if err != nil || len(got) != 2 || got[0] != 9 || got[1] != 5 {
		t.Fatalf("sampling tag IDs = (%v, %v), want ([9 5], nil)", got, err)
	}

	campaign.SmartTargetingTestSatisfiedTagIDs = []uint{5, 9}
	if _, err := smartTargetingTestSamplingTagIDs(campaign, []uint{9, 2, 5}); err == nil {
		t.Fatal("out-of-order persisted satisfied tags must fail closed")
	}
}

func TestValidateSchedulerSelectedAudienceCountRequiresPersistedSmartTestCount(t *testing.T) {
	phase := string(models.CampaignPhaseTest)
	sampleSize := uint64(600)
	campaign := dto.BotGetCampaignResponse{
		ID:               23,
		TargetingMethod:  models.CampaignAudienceTargetingSmart,
		Phase:            &phase,
		SampleSizePerTag: &sampleSize,
	}
	for _, selected := range []int{1_200} {
		if err := validateSchedulerSelectedAudienceCount(campaign, 1_200, selected); err != nil {
			t.Fatalf("persisted selected count %d was rejected: %v", selected, err)
		}
	}
	for _, selected := range []int{0, 599, 600, 1_201, 1_800} {
		if err := validateSchedulerSelectedAudienceCount(campaign, 1_200, selected); err == nil {
			t.Fatalf("invalid selected count %d was accepted", selected)
		}
	}
}

func TestZeroAudienceCampaignStatisticsEnableFullRefundAccounting(t *testing.T) {
	stats := zeroAudienceCampaignStatistics()
	if sent, ok := stats["aggregatedTotalSent"].(int64); !ok || sent != 0 {
		t.Fatalf("zero-audience aggregatedTotalSent = %#v, want int64(0)", stats["aggregatedTotalSent"])
	}
	if _, ok := stats["updatedAt"].(string); !ok {
		t.Fatalf("zero-audience updatedAt = %#v, want timestamp string", stats["updatedAt"])
	}
}

func TestNormalizeSchedulerScoreClassesRejectsDuplicate(t *testing.T) {
	if _, err := normalizeSchedulerScoreClasses([]string{"A", "a"}); err == nil {
		t.Fatal("duplicate score classes must fail closed at execution")
	}
	if got, err := normalizeSchedulerScoreClasses([]string{"c", "A"}); err != nil || len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Fatalf("canonical classes = %v, %v", got, err)
	}
	if _, err := normalizeSchedulerScoreClasses([]string{"D"}); err == nil {
		t.Fatal("invalid score class must fail closed at execution")
	}
}

func TestSmartTargetingSchedulerAudienceQueryEnablesBundleExclusionsOnlyForTest(t *testing.T) {
	testPhase := string(models.CampaignPhaseTest)
	campaign := dto.BotGetCampaignResponse{
		ID:              17,
		TargetingMethod: models.CampaignAudienceTargetingSmart,
		Phase:           &testPhase,
		Platform:        string(models.CampaignPlatformSMS),
	}
	query := smartTargetingSchedulerAudienceQuery(campaign, 3, []int64{9, 2}, []string{"A", "C"}, []string{"white", "pink"})
	if !query.ApplyBundleAudienceExclusions || query.BundleID != 3 || len(query.TagIDs) != 2 || len(query.AllowedColors) != 2 {
		t.Fatalf("Smart Test scheduler audience query = %#v, want Bundle-scoped SMS query", query)
	}

	executionPhase := string(models.CampaignPhaseExecution)
	campaign.Phase = &executionPhase
	query = smartTargetingSchedulerAudienceQuery(campaign, 3, []int64{9, 2}, []string{"A", "C"}, nil)
	if query.ApplyBundleAudienceExclusions {
		t.Fatalf("execution scheduler audience query applies Test-only settings: %#v", query)
	}
	if len(query.AllowedColors) != 0 {
		t.Fatalf("Candoo-compatible scheduler query color filter = %v, want none", query.AllowedColors)
	}
}

type capacitySchedulerTestRepo struct {
	mu              sync.Mutex
	claimCalls      int
	claimed         []*models.CampaignTargetingCapacityCalculation
	lastLimit       int
	lastStaleBefore time.Time
}

func (r *capacitySchedulerTestRepo) Save(context.Context, *models.CampaignTargetingCapacityCalculation) error {
	return nil
}

func (r *capacitySchedulerTestRepo) ByID(context.Context, int64) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) LatestByCampaignID(context.Context, uint) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) ActiveByCampaignID(context.Context, uint) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) LatestCalculatedByInput(context.Context, uint, string) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) LatestByInput(context.Context, uint, string) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) CurrentForExecution(context.Context, uint, uint, string, bool, []int64, []string, []string, int, time.Time) (*models.CampaignTargetingCapacityCalculation, error) {
	return nil, nil
}

func (r *capacitySchedulerTestRepo) Supersede(context.Context, int64, string, string, time.Time) error {
	return nil
}

func (r *capacitySchedulerTestRepo) ClaimPending(_ context.Context, limit int, staleBefore, _ time.Time) ([]*models.CampaignTargetingCapacityCalculation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCalls++
	r.lastLimit = limit
	r.lastStaleBefore = staleBefore
	rows := r.claimed
	r.claimed = nil
	return rows, nil
}

func (r *capacitySchedulerTestRepo) Complete(context.Context, int64, time.Time, int64, int64, int64, int64, string, time.Time) error {
	return nil
}

func (r *capacitySchedulerTestRepo) Fail(context.Context, int64, time.Time, string, string, time.Time) error {
	return nil
}

type capacitySchedulerTestExecutor struct {
	mu    sync.Mutex
	ids   []int64
	block <-chan struct{}
}

func (e *capacitySchedulerTestExecutor) ExecuteCampaignTargetingCapacityCalculation(_ context.Context, id int64, _ time.Time) error {
	e.mu.Lock()
	e.ids = append(e.ids, id)
	block := e.block
	e.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}

func TestSmartTargetingCapacitySchedulerUsesDurableClaimsAndCleanup(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &capacitySchedulerTestRepo{
		claimed: []*models.CampaignTargetingCapacityCalculation{{ID: 41, StartedAt: &startedAt}},
	}
	executor := &capacitySchedulerTestExecutor{}
	scheduler := NewSmartTargetingCapacityScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 1)

	var workers sync.WaitGroup
	scheduler.runOnce(t.Context(), &workers)
	workers.Wait()

	repo.mu.Lock()
	claimCalls, limit := repo.claimCalls, repo.lastLimit
	leaseAge := time.Since(repo.lastStaleBefore)
	repo.mu.Unlock()
	if claimCalls != 1 || limit != 1 {
		t.Fatalf("claim calls = %d with limit %d, want 1 with limit 1", claimCalls, limit)
	}
	if leaseAge < smartTargetingCalculationLeaseDuration || leaseAge > smartTargetingCalculationLeaseDuration+time.Minute {
		t.Fatalf("stale lease age = %s, want approximately %s", leaseAge, smartTargetingCalculationLeaseDuration)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.ids) != 1 || executor.ids[0] != 41 {
		t.Fatalf("executed IDs = %v, want [41]", executor.ids)
	}
}

func TestSmartTargetingCapacitySchedulerExecutesDuplicateClaimOnce(t *testing.T) {
	startedAt := time.Now().UTC()
	repo := &capacitySchedulerTestRepo{claimed: []*models.CampaignTargetingCapacityCalculation{
		{ID: 73, StartedAt: &startedAt}, {ID: 73, StartedAt: &startedAt},
	}}
	release := make(chan struct{})
	executor := &capacitySchedulerTestExecutor{block: release}
	scheduler := NewSmartTargetingCapacityScheduler(executor, repo, log.New(io.Discard, "", 0), time.Hour, 2)

	var workers sync.WaitGroup
	scheduler.runOnce(t.Context(), &workers)
	close(release)
	workers.Wait()

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.ids) != 1 || executor.ids[0] != 73 {
		t.Fatalf("executed IDs = %v, want one execution of 73", executor.ids)
	}
}
