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

type stubSplusClient struct {
	fetchStatusFn func(ctx context.Context, messageIDs []string) ([]SplusStatusResponse, error)
}

func (s *stubSplusClient) SendMessage(_ context.Context, _ string, _ *SplusSendMessageRequest) (*SplusResponse, error) {
	return nil, nil
}

func (s *stubSplusClient) UploadFile(_ context.Context, _ string, _ string) (*SplusUploadResponse, error) {
	return nil, nil
}

func (s *stubSplusClient) FetchStatus(ctx context.Context, messageIDs []string) ([]SplusStatusResponse, error) {
	if s.fetchStatusFn != nil {
		return s.fetchStatusFn(ctx, messageIDs)
	}
	return nil, nil
}

func (s *stubSplusClient) SupportsStatusTracking() bool { return true }

type stubSentSplusMessageRepo struct {
	listByTrackingIDsFn func(ctx context.Context, processedCampaignID uint, trackingIDs []string) ([]*models.SentSplusMessage, error)
}

func (s *stubSentSplusMessageRepo) ByFilter(
	_ context.Context,
	_ models.SentSplusMessageFilter,
	_ string,
	_, _ int,
) ([]*models.SentSplusMessage, error) {
	return nil, nil
}

func (s *stubSentSplusMessageRepo) Save(_ context.Context, _ *models.SentSplusMessage) error {
	return nil
}

func (s *stubSentSplusMessageRepo) SaveBatch(_ context.Context, _ []*models.SentSplusMessage) error {
	return nil
}

func (s *stubSentSplusMessageRepo) Count(_ context.Context, _ models.SentSplusMessageFilter) (int64, error) {
	return 0, nil
}

func (s *stubSentSplusMessageRepo) Exists(_ context.Context, _ models.SentSplusMessageFilter) (bool, error) {
	return false, nil
}

func (s *stubSentSplusMessageRepo) ByID(_ context.Context, _ uint) (*models.SentSplusMessage, error) {
	return nil, nil
}

func (s *stubSentSplusMessageRepo) ListByProcessedCampaign(_ context.Context, _ uint, _, _ int) ([]*models.SentSplusMessage, error) {
	return nil, nil
}

func (s *stubSentSplusMessageRepo) ListByTrackingIDs(
	ctx context.Context,
	processedCampaignID uint,
	trackingIDs []string,
) ([]*models.SentSplusMessage, error) {
	if s.listByTrackingIDsFn != nil {
		return s.listByTrackingIDsFn(ctx, processedCampaignID, trackingIDs)
	}
	return nil, nil
}

func (s *stubSentSplusMessageRepo) TrackingResultsFromSentRows(_ context.Context, _ uint) ([]repository.SplusTrackingResult, error) {
	return nil, nil
}

func (s *stubSentSplusMessageRepo) UpdateSendResultByTrackingID(
	_ context.Context,
	_ string,
	_ models.SplusSendStatus,
	_ int,
	_, _, _ *string,
) error {
	return nil
}

func (s *stubSentSplusMessageRepo) UpdateSendResultByTrackingIDs(_ context.Context, _ []repository.SentSplusSendResultUpdate) error {
	return nil
}

func TestDispatchPendingSplusCampaignsSerializesSameBundle(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	bundleID := uint(42)
	otherBundleID := uint(77)
	started := make(chan uint, 3)
	releaseFirst := make(chan struct{})

	s := &SplusCampaignScheduler{
		logger: log.New(io.Discard, "", 0),
	}

	pending := []dto.BotGetCampaignResponse{
		{ID: 1, CustomerID: 10, BundleID: &bundleID},
		{ID: 2, CustomerID: 10, BundleID: &bundleID},
		{ID: 3, CustomerID: 10, BundleID: &otherBundleID},
	}

	s.dispatchPendingSplusCampaigns(
		parent,
		"token-1",
		pending,
		func(ctx context.Context, token string, c dto.BotGetCampaignResponse) error {
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
		},
	)

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

func TestSplusHandleStatusJobFetchFailureKeepsJobRetryable(t *testing.T) {
	t.Parallel()

	repo := &stubSentSplusMessageRepo{
		listByTrackingIDsFn: func(_ context.Context, _ uint, _ []string) ([]*models.SentSplusMessage, error) {
			serverID := "1001"
			return []*models.SentSplusMessage{{TrackingID: "trk-1", ServerID: &serverID}}, nil
		},
	}
	jobRepo := &stubCampaignStatusJobRepo{}
	clientErr := errors.New("temporary status provider failure")

	s := &SplusCampaignScheduler{
		sentRepo: repo,
		jobRepo:  jobRepo,
		splusClient: &stubSplusClient{
			fetchStatusFn: func(_ context.Context, messageIDs []string) ([]SplusStatusResponse, error) {
				if len(messageIDs) != 1 || messageIDs[0] != "1001" {
					t.Fatalf("unexpected messageIDs: %#v", messageIDs)
				}
				return nil, clientErr
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
}

func TestSplusHandleStatusJobFetchFailureMarksExecutedAtOnMaxRetry(t *testing.T) {
	t.Parallel()

	repo := &stubSentSplusMessageRepo{
		listByTrackingIDsFn: func(_ context.Context, _ uint, _ []string) ([]*models.SentSplusMessage, error) {
			serverID := "1002"
			return []*models.SentSplusMessage{{TrackingID: "trk-2", ServerID: &serverID}}, nil
		},
	}
	jobRepo := &stubCampaignStatusJobRepo{}

	s := &SplusCampaignScheduler{
		sentRepo: repo,
		jobRepo:  jobRepo,
		splusClient: &stubSplusClient{
			fetchStatusFn: func(_ context.Context, _ []string) ([]SplusStatusResponse, error) {
				return nil, errors.New("provider still down")
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

func TestSplusSendRetriesTransientTransportErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		errors.New("connection reset by peer"),
		errors.New("dial tcp: connection refused"),
		errors.New("unexpected EOF"),
	} {
		if !isRetryableSplusError(nil, err) {
			t.Fatalf("transient transport error %q must be retryable", err)
		}
	}
	if isRetryableSplusError(nil, errors.New("invalid request")) {
		t.Fatal("permanent request error must not be retryable")
	}
}
