package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

type stubBaleClient struct {
	fetchStatusFn func(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error)
}

func (s *stubBaleClient) SendMessage(ctx context.Context, req *BaleSendMessageRequest) (*BaleSendMessageResponse, error) {
	return nil, nil
}

func (s *stubBaleClient) SendBatch(ctx context.Context, reqs []BaleSendMessageRequest) ([]BaleSendMessageResponse, error) {
	return nil, nil
}

func (s *stubBaleClient) UploadFile(ctx context.Context, path string) (*BaleUploadFileResponse, error) {
	return nil, nil
}

func (s *stubBaleClient) FetchStatus(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error) {
	if s.fetchStatusFn != nil {
		return s.fetchStatusFn(ctx, messageIDs)
	}
	return BaleStatusFetchResult{}, nil
}

func (s *stubBaleClient) SupportsStatusTracking() bool { return true }

type stubSentBaleMessageRepo struct {
	listByTrackingIDsFn func(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentBaleMessage, error)
}

func (s *stubSentBaleMessageRepo) ByFilter(
	ctx context.Context,
	filter models.SentBaleMessageFilter,
	orderBy string,
	limit, offset int,
) ([]*models.SentBaleMessage, error) {
	return nil, nil
}

func (s *stubSentBaleMessageRepo) Save(ctx context.Context, entity *models.SentBaleMessage) error {
	return nil
}

func (s *stubSentBaleMessageRepo) SaveBatch(ctx context.Context, entities []*models.SentBaleMessage) error {
	return nil
}

func (s *stubSentBaleMessageRepo) Count(ctx context.Context, filter models.SentBaleMessageFilter) (int64, error) {
	return 0, nil
}

func (s *stubSentBaleMessageRepo) Exists(ctx context.Context, filter models.SentBaleMessageFilter) (bool, error) {
	return false, nil
}

func (s *stubSentBaleMessageRepo) ByID(ctx context.Context, id uint) (*models.SentBaleMessage, error) {
	return nil, nil
}

func (s *stubSentBaleMessageRepo) ListByProcessedCampaign(
	ctx context.Context,
	processedCampaignID uint,
	limit, offset int,
) ([]*models.SentBaleMessage, error) {
	return nil, nil
}

func (s *stubSentBaleMessageRepo) ListByTrackingIDs(
	ctx context.Context,
	processedCampaignID uint,
	trackingIDs []string,
) ([]*models.SentBaleMessage, error) {
	if s.listByTrackingIDsFn != nil {
		return s.listByTrackingIDsFn(ctx, processedCampaignID, trackingIDs)
	}
	return nil, nil
}

func (s *stubSentBaleMessageRepo) TrackingResultsFromSentRows(
	ctx context.Context,
	processedCampaignID uint,
) ([]repository.BaleTrackingResult, error) {
	return nil, nil
}

func (s *stubSentBaleMessageRepo) UpdateSendResultByTrackingIDs(
	ctx context.Context,
	processedCampaignID uint,
	updates []repository.SentBaleSendResultUpdate,
) error {
	return nil
}

type stubCampaignStatusJobRepo struct {
	updated []*models.CampaignStatusJob
}

func (s *stubCampaignStatusJobRepo) ByFilter(
	ctx context.Context,
	filter any,
	orderBy string,
	limit, offset int,
) ([]*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubCampaignStatusJobRepo) Save(ctx context.Context, entity *models.CampaignStatusJob) error {
	return nil
}

func (s *stubCampaignStatusJobRepo) SaveBatch(ctx context.Context, entities []*models.CampaignStatusJob) error {
	return nil
}

func (s *stubCampaignStatusJobRepo) Count(ctx context.Context, filter any) (int64, error) {
	return 0, nil
}

func (s *stubCampaignStatusJobRepo) Exists(ctx context.Context, filter any) (bool, error) {
	return false, nil
}

func (s *stubCampaignStatusJobRepo) ByID(ctx context.Context, id uint) (*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubCampaignStatusJobRepo) ListDue(
	ctx context.Context,
	platform string,
	now time.Time,
	limit int,
) ([]*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubCampaignStatusJobRepo) Update(ctx context.Context, job *models.CampaignStatusJob) error {
	clone := *job
	s.updated = append(s.updated, &clone)
	return nil
}

func TestDispatchPendingBaleCampaignsSerializesSameBundle(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	bundleID := uint(42)
	otherBundleID := uint(77)
	started := make(chan uint, 3)
	releaseFirst := make(chan struct{})

	s := &BaleCampaignScheduler{
		logger: log.New(io.Discard, "", 0),
	}

	pending := []dto.BotGetCampaignResponse{
		{ID: 1, CustomerID: 10, BundleID: &bundleID},
		{ID: 2, CustomerID: 10, BundleID: &bundleID},
		{ID: 3, CustomerID: 10, BundleID: &otherBundleID},
	}

	s.dispatchPendingBaleCampaigns(parent, "token-1", pending, func(ctx context.Context, token string, c dto.BotGetCampaignResponse) error {
		if token != "token-1" {
			t.Errorf("unexpected token: %q", token)
		}
		started <- c.ID
		if c.ID == 1 {
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})

	seen := map[uint]bool{}
	deadline := time.After(time.Second)
	for !(seen[1] && seen[3]) {
		select {
		case id := <-started:
			if id == 2 {
				t.Fatalf("campaign 2 started before prior campaign in the same bundle completed")
			}
			seen[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for first campaign in each bundle to start; seen=%v", seen)
		}
	}

	close(releaseFirst)

	select {
	case id := <-started:
		if id != 2 {
			t.Fatalf("expected campaign 2 to start after releasing campaign 1, got campaign %d", id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for second campaign in same bundle to start")
	}
}

func TestHandleStatusJobFetchFailureKeepsJobRetryable(t *testing.T) {
	t.Parallel()

	repo := &stubSentBaleMessageRepo{
		listByTrackingIDsFn: func(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentBaleMessage, error) {
			serverID := "1001"
			return []*models.SentBaleMessage{{TrackingID: "trk-1", ServerID: &serverID}}, nil
		},
	}
	jobRepo := &stubCampaignStatusJobRepo{}
	clientErr := errors.New("temporary status provider failure")

	s := &BaleCampaignScheduler{
		sentRepo: repo,
		jobRepo:  jobRepo,
		baleClient: &stubBaleClient{
			fetchStatusFn: func(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error) {
				if len(messageIDs) != 1 || messageIDs[0] != "1001" {
					t.Fatalf("unexpected messageIDs: %#v", messageIDs)
				}
				raw := `{"error":"temporarily unavailable"}`
				return BaleStatusFetchResult{RawResponse: &raw}, clientErr
			},
		},
	}

	job := &models.CampaignStatusJob{
		ID:                  10,
		ProcessedCampaignID: 77,
		TrackingIDs:         []string{"trk-1"},
		RetryCount:          0,
	}

	err := s.handleStatusJob(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), clientErr.Error()) {
		t.Fatalf("expected provider error in return, got: %v", err)
	}
	if job.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got=%d", job.RetryCount)
	}
	if job.ExecutedAt != nil {
		t.Fatalf("expected executed_at to remain nil before max retries")
	}
	if len(jobRepo.updated) != 1 {
		t.Fatalf("expected one job update, got=%d", len(jobRepo.updated))
	}
	if job.RawProviderResponse == nil || *job.RawProviderResponse != `{"error":"temporarily unavailable"}` {
		t.Fatalf("expected raw provider response to be retained, got=%v", job.RawProviderResponse)
	}
}

func TestHandleStatusJobFetchFailureMarksExecutedAtOnMaxRetry(t *testing.T) {
	t.Parallel()

	repo := &stubSentBaleMessageRepo{
		listByTrackingIDsFn: func(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentBaleMessage, error) {
			serverID := "1002"
			return []*models.SentBaleMessage{{TrackingID: "trk-2", ServerID: &serverID}}, nil
		},
	}
	jobRepo := &stubCampaignStatusJobRepo{}

	s := &BaleCampaignScheduler{
		sentRepo: repo,
		jobRepo:  jobRepo,
		baleClient: &stubBaleClient{
			fetchStatusFn: func(ctx context.Context, messageIDs []string) (BaleStatusFetchResult, error) {
				return BaleStatusFetchResult{}, errors.New("provider still down")
			},
		},
	}

	job := &models.CampaignStatusJob{
		ID:                  11,
		ProcessedCampaignID: 88,
		TrackingIDs:         []string{"trk-2"},
		RetryCount:          statusJobMaxRetry - 1,
	}

	err := s.handleStatusJob(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error")
	}
	if job.RetryCount != statusJobMaxRetry {
		t.Fatalf("expected retry_count=%d, got=%d", statusJobMaxRetry, job.RetryCount)
	}
	if job.ExecutedAt == nil {
		t.Fatalf("expected executed_at to be set at retry limit")
	}
	if len(jobRepo.updated) != 1 {
		t.Fatalf("expected one job update, got=%d", len(jobRepo.updated))
	}
}
