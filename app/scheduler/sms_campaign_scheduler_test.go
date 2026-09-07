package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/lib/pq"
)

type stubSMSClient struct {
	fetchStatusFn func(ctx context.Context, token string, ids []string) (PayamStatusFetchResult, error)
}

func (s *stubSMSClient) SendBatch(ctx context.Context, sender string, items []PayamSMSItem) (PayamSMSSendResult, error) {
	return PayamSMSSendResult{}, nil
}

func (s *stubSMSClient) GetToken(ctx context.Context) (string, error) {
	return "", nil
}

func (s *stubSMSClient) FetchStatus(ctx context.Context, token string, ids []string) (PayamStatusFetchResult, error) {
	if s.fetchStatusFn != nil {
		return s.fetchStatusFn(ctx, token, ids)
	}
	return PayamStatusFetchResult{}, nil
}

type stubSMSCampaignStatusJobRepo struct {
	updated []*models.CampaignStatusJob
}

type stubSMSAudienceProfileRepo struct {
	selectCandidatesFn func(context.Context, models.AudienceProfileFilter, []int64, int) ([]*models.AudienceProfile, error)
	byUIDsFn           func(context.Context, []string) ([]*models.AudienceProfile, error)
}

func (s *stubSMSAudienceProfileRepo) ByFilter(context.Context, models.AudienceProfileFilter, string, int, int) ([]*models.AudienceProfile, error) {
	return nil, nil
}

func (s *stubSMSAudienceProfileRepo) Save(context.Context, *models.AudienceProfile) error {
	return nil
}

func (s *stubSMSAudienceProfileRepo) SaveBatch(context.Context, []*models.AudienceProfile) error {
	return nil
}

func (s *stubSMSAudienceProfileRepo) Count(context.Context, models.AudienceProfileFilter) (int64, error) {
	return 0, nil
}

func (s *stubSMSAudienceProfileRepo) Exists(context.Context, models.AudienceProfileFilter) (bool, error) {
	return false, nil
}

func (s *stubSMSAudienceProfileRepo) ByID(context.Context, uint) (*models.AudienceProfile, error) {
	return nil, nil
}

func (s *stubSMSAudienceProfileRepo) ByIDs(context.Context, []int64) ([]*models.AudienceProfile, error) {
	return nil, nil
}

func (s *stubSMSAudienceProfileRepo) ByUID(context.Context, string) (*models.AudienceProfile, error) {
	return nil, nil
}

func (s *stubSMSAudienceProfileRepo) ByUIDs(ctx context.Context, uids []string) ([]*models.AudienceProfile, error) {
	if s.byUIDsFn != nil {
		return s.byUIDsFn(ctx, uids)
	}
	return nil, nil
}

func (s *stubSMSAudienceProfileRepo) SelectCampaignCandidates(ctx context.Context, filter models.AudienceProfileFilter, excludeIDs []int64, limit int) ([]*models.AudienceProfile, error) {
	return s.selectCandidatesFn(ctx, filter, excludeIDs, limit)
}

var _ repository.AudienceProfileRepository = (*stubSMSAudienceProfileRepo)(nil)

func (s *stubSMSCampaignStatusJobRepo) ByFilter(ctx context.Context, filter any, orderBy string, limit, offset int) ([]*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubSMSCampaignStatusJobRepo) Save(ctx context.Context, entity *models.CampaignStatusJob) error {
	return nil
}

func (s *stubSMSCampaignStatusJobRepo) SaveBatch(ctx context.Context, entities []*models.CampaignStatusJob) error {
	return nil
}

func (s *stubSMSCampaignStatusJobRepo) Count(ctx context.Context, filter any) (int64, error) {
	return 0, nil
}

func (s *stubSMSCampaignStatusJobRepo) Exists(ctx context.Context, filter any) (bool, error) {
	return false, nil
}

func (s *stubSMSCampaignStatusJobRepo) ByID(ctx context.Context, id uint) (*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubSMSCampaignStatusJobRepo) ListDue(ctx context.Context, platform string, now time.Time, limit int) ([]*models.CampaignStatusJob, error) {
	return nil, nil
}

func (s *stubSMSCampaignStatusJobRepo) Update(ctx context.Context, job *models.CampaignStatusJob) error {
	clone := *job
	s.updated = append(s.updated, &clone)
	return nil
}

func TestDispatchPendingSMSCampaignsSerializesSameBundle(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	bundleID := uint(42)
	otherBundleID := uint(77)
	started := make(chan uint, 3)
	releaseFirst := make(chan struct{})

	s := &SMSCampaignScheduler{
		logger: log.New(io.Discard, "", 0),
	}

	pending := []dto.BotGetCampaignResponse{
		{ID: 1, CustomerID: 10, BundleID: &bundleID},
		{ID: 2, CustomerID: 10, BundleID: &bundleID},
		{ID: 3, CustomerID: 10, BundleID: &otherBundleID},
	}

	s.dispatchPendingSMSCampaigns(parent, "token-1", pending, func(ctx context.Context, token string, c dto.BotGetCampaignResponse) error {
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

func TestSelectTagAudiencesPushesLimitAndExclusionsIntoRepository(t *testing.T) {
	t.Parallel()

	phone1 := "09120000001"
	phone2 := "09120000002"
	phone3 := "09120000003"
	excluded := make(map[int64]struct{}, 50_000)
	for i := int64(1); i <= 50_000; i++ {
		excluded[i] = struct{}{}
	}

	call := 0
	audRepo := &stubSMSAudienceProfileRepo{selectCandidatesFn: func(_ context.Context, filter models.AudienceProfileFilter, excludeIDs []int64, limit int) ([]*models.AudienceProfile, error) {
		call++
		if len(excludeIDs) != len(excluded) {
			t.Fatalf("exclusion count = %d, want %d", len(excludeIDs), len(excluded))
		}
		if filter.Color == nil {
			t.Fatal("standard SMS candidate query must filter audience color")
		}
		switch call {
		case 1:
			if *filter.Color != "white" || limit != 3 {
				t.Fatalf("first query color=%q limit=%d, want white/3", *filter.Color, limit)
			}
			return []*models.AudienceProfile{{ID: 60_002, UID: "u2", PhoneNumber: &phone2}}, nil
		case 2:
			if *filter.Color != "pink" || limit != 2 {
				t.Fatalf("second query color=%q limit=%d, want pink/2", *filter.Color, limit)
			}
			return []*models.AudienceProfile{
				{ID: 60_001, UID: "u1", PhoneNumber: &phone1},
				{ID: 60_003, UID: "u3", PhoneNumber: &phone3},
			}, nil
		default:
			t.Fatalf("unexpected candidate query %d", call)
			return nil, nil
		}
	}}
	s := &SMSCampaignScheduler{audRepo: audRepo, logger: log.New(io.Discard, "", 0)}
	tags := pq.Int32Array{10, 20}

	phones, ids, uids, err := s.selectTagAudiences(context.Background(), 99, tags, 3, excluded, nil, nil, []string{"white", "pink"})
	if err != nil {
		t.Fatalf("select tag audiences: %v", err)
	}
	if call != 2 {
		t.Fatalf("candidate query calls = %d, want 2", call)
	}
	if got, want := strings.Join(phones, ","), strings.Join([]string{phone2, phone1, phone3}, ","); got != want {
		t.Fatalf("phones = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(ids), fmt.Sprint([]int64{60_002, 60_001, 60_003}); got != want {
		t.Fatalf("ids = %s, want %s", got, want)
	}
	if got, want := strings.Join(uids, ","), "u2,u1,u3"; got != want {
		t.Fatalf("uids = %q, want %q", got, want)
	}
}

func TestSelectTagAudiencesDoesNotFilterCandooCandidatesByColor(t *testing.T) {
	phone := "09120000001"
	call := 0
	audRepo := &stubSMSAudienceProfileRepo{selectCandidatesFn: func(_ context.Context, filter models.AudienceProfileFilter, _ []int64, limit int) ([]*models.AudienceProfile, error) {
		call++
		if filter.Color != nil || limit != 1 {
			t.Fatalf("Candoo candidate query color=%v limit=%d, want unrestricted/1", filter.Color, limit)
		}
		return []*models.AudienceProfile{{ID: 7, UID: "u7", PhoneNumber: &phone}}, nil
	}}
	s := &SMSCampaignScheduler{audRepo: audRepo, logger: log.New(io.Discard, "", 0)}
	phones, ids, uids, err := s.selectTagAudiences(context.Background(), 99, pq.Int32Array{10}, 1, nil, nil, nil, nil)
	if err != nil || call != 1 || len(phones) != 1 || len(ids) != 1 || len(uids) != 1 {
		t.Fatalf("Candoo audience selection = (%v, %v, %v, calls=%d, err=%v)", phones, ids, uids, call, err)
	}
}

func TestSMSHandleStatusJobFetchFailureKeepsJobRetryable(t *testing.T) {
	t.Parallel()

	jobRepo := &stubSMSCampaignStatusJobRepo{}
	clientErr := errors.New("temporary status provider failure")
	s := &SMSCampaignScheduler{
		jobRepo: jobRepo,
		smsClient: &stubSMSClient{
			fetchStatusFn: func(ctx context.Context, token string, ids []string) (PayamStatusFetchResult, error) {
				if token != "token-1" {
					t.Fatalf("unexpected token: %q", token)
				}
				if len(ids) != 1 || ids[0] != "trk-1" {
					t.Fatalf("unexpected ids: %#v", ids)
				}
				raw := `{"error":"temporarily unavailable"}`
				return PayamStatusFetchResult{RawResponse: &raw}, clientErr
			},
		},
	}

	job := &models.CampaignStatusJob{
		ID:                  10,
		ProcessedCampaignID: 77,
		TrackingIDs:         []string{"trk-1"},
		RetryCount:          0,
	}

	err := s.handleStatusJob(context.Background(), job, "token-1")
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

func TestSMSHandleStatusJobFetchFailureMarksExecutedAtOnMaxRetry(t *testing.T) {
	t.Parallel()

	jobRepo := &stubSMSCampaignStatusJobRepo{}
	s := &SMSCampaignScheduler{
		jobRepo: jobRepo,
		smsClient: &stubSMSClient{
			fetchStatusFn: func(ctx context.Context, token string, ids []string) (PayamStatusFetchResult, error) {
				return PayamStatusFetchResult{}, errors.New("provider still down")
			},
		},
	}

	job := &models.CampaignStatusJob{
		ID:                  11,
		ProcessedCampaignID: 88,
		TrackingIDs:         []string{"trk-2"},
		RetryCount:          smsStatusJobMaxRetry - 1,
	}

	err := s.handleStatusJob(context.Background(), job, "token-2")
	if err == nil {
		t.Fatalf("expected error")
	}
	if job.RetryCount != smsStatusJobMaxRetry {
		t.Fatalf("expected retry_count=%d, got=%d", smsStatusJobMaxRetry, job.RetryCount)
	}
	if job.ExecutedAt == nil {
		t.Fatalf("expected executed_at to be set at retry limit")
	}
	if len(jobRepo.updated) != 1 {
		t.Fatalf("expected one job update, got=%d", len(jobRepo.updated))
	}
}

func TestBuildSMSProviderUpdateMissingResponse(t *testing.T) {
	t.Parallel()

	update := buildSMSProviderUpdate("trk-3", nil, nil)
	if update.TrackingID != "trk-3" {
		t.Fatalf("unexpected tracking id: %q", update.TrackingID)
	}
	if update.ErrorCode == nil || *update.ErrorCode != "MISSING_SEND_RESPONSE" {
		t.Fatalf("expected missing response error code, got=%v", update.ErrorCode)
	}
	if update.Description == nil || !strings.Contains(*update.Description, "trk-3") {
		t.Fatalf("expected missing response description to include tracking id, got=%v", update.Description)
	}
}

func TestBuildPayamSMSSendResponsePreservesFailureDetails(t *testing.T) {
	t.Parallel()

	statusCode := http.StatusBadGateway
	body := `{"error":"upstream unavailable"}`
	sendErr := errors.New("payamsms sendMultiple http status: 502")
	row, err := buildPayamSMSSendResponse(
		77,
		[]PayamSMSItem{{TrackingID: " tracking-1 "}, {TrackingID: "tracking-2"}},
		PayamSMSSendResult{
			RawResponse:     &body,
			ResponseHeaders: http.Header{"X-Request-Id": []string{"request-1"}},
			HTTPStatusCode:  &statusCode,
			AttemptCount:    5,
		},
		sendErr,
	)
	if err != nil {
		t.Fatalf("buildPayamSMSSendResponse returned an error: %v", err)
	}
	if row.ProcessedCampaignID != 77 || len(row.TrackingIDs) != 2 || row.TrackingIDs[0] != "tracking-1" {
		t.Fatalf("campaign/tracking correlation mismatch: %+v", row)
	}
	if row.HTTPStatusCode == nil || *row.HTTPStatusCode != http.StatusBadGateway {
		t.Fatalf("HTTP status mismatch: %v", row.HTTPStatusCode)
	}
	if row.ResponseBody == nil || *row.ResponseBody != body {
		t.Fatalf("response body mismatch: %v", row.ResponseBody)
	}
	if row.Error == nil || *row.Error != sendErr.Error() || row.AttemptCount != 5 {
		t.Fatalf("error/attempt details mismatch: error=%v attempts=%d", row.Error, row.AttemptCount)
	}
	var headers http.Header
	if err := json.Unmarshal(row.ResponseHeaders, &headers); err != nil {
		t.Fatalf("unmarshal response headers: %v", err)
	}
	if got := headers.Get("X-Request-Id"); got != "request-1" {
		t.Fatalf("response headers mismatch: %q", got)
	}
}

func TestBuildPayamSMSSendResponseRecordsFailureWithoutHTTPResponse(t *testing.T) {
	t.Parallel()

	sendErr := errors.New("connection refused")
	row, err := buildPayamSMSSendResponse(
		88,
		[]PayamSMSItem{{TrackingID: "tracking-3"}},
		PayamSMSSendResult{},
		sendErr,
	)
	if err != nil {
		t.Fatalf("buildPayamSMSSendResponse returned an error: %v", err)
	}
	if row.ResponseBody != nil || row.HTTPStatusCode != nil {
		t.Fatalf("transport failure unexpectedly has HTTP details: %+v", row)
	}
	if row.Error == nil || *row.Error != sendErr.Error() {
		t.Fatalf("transport error mismatch: %v", row.Error)
	}
	if string(row.ResponseHeaders) != `{}` {
		t.Fatalf("empty response headers should be stored as an object, got=%s", row.ResponseHeaders)
	}
}
