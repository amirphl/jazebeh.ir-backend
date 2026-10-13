package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestExternalShortLinkClientUploadsCompleteAcknowledgedBatch(t *testing.T) {
	t.Parallel()
	var authorization string
	var uploaded externalMappingUpload
	client := &HTTPExternalShortLinkClient{
		baseURL:          "https://links.example",
		token:            "secret",
		mappingBatchSize: 100,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			authorization = request.Header.Get("Authorization")
			if err := json.NewDecoder(request.Body).Decode(&uploaded); err != nil {
				t.Fatalf("decode upload payload: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"persisted":1,"created":1,"existing":0}`)),
				Request:    request,
			}, nil
		})},
	}
	err := client.UploadMappings(context.Background(), []*models.ShortLink{{
		ID: 42, UID: "abc1", LongLink: "https://example.com/long", ShortLink: "https://links.example/abc1", IsTest: true,
	}})
	if err != nil {
		t.Fatalf("UploadMappings() error = %v", err)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if len(uploaded.Links) != 1 || !uploaded.Links[0].IsTest {
		t.Fatalf("uploaded test marker = %#v", uploaded.Links)
	}
}

func TestExternalShortLinkClientCapsRequestBatchesForRustService(t *testing.T) {
	var sizes []int
	client := &HTTPExternalShortLinkClient{
		baseURL:          "https://links.example",
		token:            "secret",
		mappingBatchSize: 10000,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", request.Header.Get("Content-Type"))
			}
			var payload externalMappingUpload
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode upload payload: %v", err)
			}
			sizes = append(sizes, len(payload.Links))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"persisted":` + strconv.Itoa(len(payload.Links)) + `}`)),
				Request:    request,
			}, nil
		})},
	}
	links := make([]*models.ShortLink, maxExternalMappingBatchSize+1)
	for index := range links {
		links[index] = &models.ShortLink{
			ID: uint(index + 1), UID: fmt.Sprintf("code-%d", index), LongLink: "https://example.com/destination",
		}
	}
	if err := client.UploadMappings(context.Background(), links); err != nil {
		t.Fatalf("UploadMappings() error = %v", err)
	}
	if got, want := fmt.Sprint(sizes), "[500 1]"; got != want {
		t.Fatalf("upload sizes = %s, want %s", got, want)
	}
}

func TestExternalShortLinkClientUploadsLargeBatchesWithBoundedParallelism(t *testing.T) {
	var active, maximum atomic.Int32
	client := &HTTPExternalShortLinkClient{
		baseURL:            "https://links.example",
		token:              "secret",
		mappingBatchSize:   1,
		mappingParallelism: 2,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			current := active.Add(1)
			for {
				seen := maximum.Load()
				if current <= seen || maximum.CompareAndSwap(seen, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			active.Add(-1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"persisted":1}`)),
				Request:    request,
			}, nil
		})},
	}
	links := make([]*models.ShortLink, 4)
	for index := range links {
		links[index] = &models.ShortLink{UID: fmt.Sprintf("code-%d", index), LongLink: "https://example.com/destination"}
	}
	if err := client.UploadMappings(context.Background(), links); err != nil {
		t.Fatalf("UploadMappings() error = %v", err)
	}
	if got := maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrent uploads = %d, want 2", got)
	}
}

func TestExternalShortLinkClientRejectsPartialMappingAcknowledgement(t *testing.T) {
	client := &HTTPExternalShortLinkClient{
		baseURL:          "https://links.example",
		token:            "secret",
		mappingBatchSize: 100,
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"persisted":0}`)),
				Request:    request,
			}, nil
		})},
	}
	err := client.UploadMappings(context.Background(), []*models.ShortLink{{UID: "abc1", LongLink: "https://example.com"}})
	if err == nil || !strings.Contains(err.Error(), "acknowledged 0 of 1") {
		t.Fatalf("UploadMappings() error = %v, want partial acknowledgement error", err)
	}
}

func TestNewExternalShortLinkClientRejectsNonOriginBaseURL(t *testing.T) {
	_, err := NewExternalShortLinkClient(config.ExternalShortLinkConfig{
		BaseURL:        "https://links.example/api?unexpected=true",
		APIToken:       strings.Repeat("x", 32),
		RequestTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("NewExternalShortLinkClient() error = nil for non-origin base URL")
	}
}

func TestNewExternalShortLinkClientRejectsNonURLSafeToken(t *testing.T) {
	_, err := NewExternalShortLinkClient(config.ExternalShortLinkConfig{
		BaseURL:        "https://links.example",
		APIToken:       strings.Repeat("x", 31) + "!",
		RequestTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "URL-safe") {
		t.Fatalf("NewExternalShortLinkClient() error = %v, want URL-safe token error", err)
	}
}

func TestNewExternalShortLinkClickSchedulerDefaultsToSupportedPageSize(t *testing.T) {
	scheduler := NewExternalShortLinkClickScheduler(nil, nil, log.New(io.Discard, "", 0), time.Minute, 0, 1)
	if got, want := scheduler.pageSize, 1000; got != want {
		t.Fatalf("page size = %d, want %d", got, want)
	}
}

type fakeExternalSyncRepository struct {
	cursor    int64
	imported  []models.ExternalShortLinkClick
	importErr error
	through   int64
	events    *[]string
}

func (r *fakeExternalSyncRepository) Cursor(context.Context, string) (int64, error) {
	return r.cursor, nil
}

func (r *fakeExternalSyncRepository) ImportPage(_ context.Context, _ string, clicks []models.ExternalShortLinkClick, through int64) error {
	*r.events = append(*r.events, "import")
	if r.importErr != nil {
		return r.importErr
	}
	r.imported = append(r.imported, clicks...)
	r.through = through
	r.cursor = through
	return nil
}

type fakeExternalShortLinkAPI struct {
	pages    []*ExternalShortLinkClickPage
	acks     []int64
	fetches  []int64
	ackError error
	events   *[]string
}

func (c *fakeExternalShortLinkAPI) UploadMappings(context.Context, []*models.ShortLink) error {
	return nil
}

func (c *fakeExternalShortLinkAPI) FetchClicks(_ context.Context, afterID int64, _ int) (*ExternalShortLinkClickPage, error) {
	c.fetches = append(c.fetches, afterID)
	if len(c.pages) == 0 {
		return nil, errors.New("no page")
	}
	page := c.pages[0]
	c.pages = c.pages[1:]
	return page, nil
}

func (c *fakeExternalShortLinkAPI) AcknowledgeClicks(_ context.Context, through int64) error {
	*c.events = append(*c.events, "ack")
	c.acks = append(c.acks, through)
	return c.ackError
}

func TestExternalClickSchedulerImportsBeforeAcknowledging(t *testing.T) {
	events := []string{}
	now := time.Now().UTC()
	repo := &fakeExternalSyncRepository{events: &events}
	client := &fakeExternalShortLinkAPI{events: &events, pages: []*ExternalShortLinkClickPage{{
		Clicks:      []models.ExternalShortLinkClick{{ClickID: 10, ShortCode: "abc1", LongURL: "https://example.com", ClickedAt: now}},
		NextAfterID: 10,
	}}}
	scheduler := NewExternalShortLinkClickScheduler(repo, client, log.New(io.Discard, "", 0), time.Minute, 100, 10)
	scheduler.runOnce(context.Background())
	if repo.through != 10 || len(client.acks) != 1 || client.acks[0] != 10 {
		t.Fatalf("through=%d acks=%v", repo.through, client.acks)
	}
	if strings.Join(events, ",") != "import,ack" {
		t.Fatalf("operation order = %v, want import then ack", events)
	}
}

func TestExternalClickSchedulerDoesNotAcknowledgeFailedImport(t *testing.T) {
	events := []string{}
	repo := &fakeExternalSyncRepository{events: &events, importErr: errors.New("database down")}
	client := &fakeExternalShortLinkAPI{events: &events, pages: []*ExternalShortLinkClickPage{{
		Clicks:      []models.ExternalShortLinkClick{{ClickID: 10, ShortCode: "abc1", LongURL: "https://example.com", ClickedAt: time.Now().UTC()}},
		NextAfterID: 10,
	}}}
	scheduler := NewExternalShortLinkClickScheduler(repo, client, log.New(io.Discard, "", 0), time.Minute, 100, 10)
	scheduler.runOnce(context.Background())
	if len(client.acks) != 0 || repo.cursor != 0 {
		t.Fatalf("acks=%v cursor=%d, want no acknowledgement or cursor advance", client.acks, repo.cursor)
	}
}

func TestExternalClickSchedulerRetriesCommittedCursorAcknowledgementOnEmptyPage(t *testing.T) {
	events := []string{}
	repo := &fakeExternalSyncRepository{cursor: 10, events: &events}
	client := &fakeExternalShortLinkAPI{events: &events, pages: []*ExternalShortLinkClickPage{{
		Clicks:      []models.ExternalShortLinkClick{},
		NextAfterID: 10,
	}}}
	scheduler := NewExternalShortLinkClickScheduler(repo, client, log.New(io.Discard, "", 0), time.Minute, 100, 10)
	scheduler.runOnce(context.Background())
	if len(client.acks) != 1 || client.acks[0] != 10 {
		t.Fatalf("acks=%v, want cumulative retry through 10", client.acks)
	}
}

func TestValidateExternalClickPageToleratesOutOfOrderIDs(t *testing.T) {
	// The importer quarantines a repeated or lowered ID per row. Rejecting the
	// whole page here is what used to stop every later click from importing.
	err := validateExternalClickPage(5, &ExternalShortLinkClickPage{
		Clicks:      []models.ExternalShortLinkClick{{ClickID: 7}, {ClickID: 6}},
		NextAfterID: 6,
	})
	if err != nil {
		t.Fatalf("validateExternalClickPage() error = %v, want nil so the importer can quarantine the row", err)
	}
}

func TestValidateExternalClickPageRejectsUnusableEmptyPage(t *testing.T) {
	if err := validateExternalClickPage(5, nil); err == nil {
		t.Fatal("validateExternalClickPage() error = nil for a nil page")
	}
	if err := validateExternalClickPage(5, &ExternalShortLinkClickPage{NextAfterID: 4}); err == nil {
		t.Fatal("validateExternalClickPage() error = nil for an empty page that rewinds the cursor")
	}
	if err := validateExternalClickPage(5, &ExternalShortLinkClickPage{NextAfterID: 5, HasMore: true}); err == nil {
		t.Fatal("validateExternalClickPage() error = nil for an empty page that claims more rows")
	}
}

func TestExternalClickPageCursorUsesTheHighestID(t *testing.T) {
	newest := time.Now().UTC()
	highest, observed := externalClickPageCursor(4, []models.ExternalShortLinkClick{
		{ClickID: 9, ClickedAt: newest.Add(-time.Hour)},
		{ClickID: 6, ClickedAt: newest},
		{ClickID: 0, ClickedAt: newest},
	})
	if highest != 9 {
		t.Fatalf("highest = %d, want 9", highest)
	}
	if !observed.Equal(newest) {
		t.Fatalf("newest = %s, want %s", observed, newest)
	}
}

func TestExternalClickPageCursorNeverRewinds(t *testing.T) {
	highest, observed := externalClickPageCursor(20, []models.ExternalShortLinkClick{{ClickID: 6}})
	if highest != 20 {
		t.Fatalf("highest = %d, want the existing cursor 20", highest)
	}
	if !observed.IsZero() {
		t.Fatalf("newest = %s, want the zero time", observed)
	}
}

func TestExternalClickSchedulerAdvancesPastOutOfOrderPage(t *testing.T) {
	events := []string{}
	now := time.Now().UTC()
	repo := &fakeExternalSyncRepository{events: &events}
	client := &fakeExternalShortLinkAPI{events: &events, pages: []*ExternalShortLinkClickPage{{
		Clicks: []models.ExternalShortLinkClick{
			{ClickID: 12, ShortCode: "abc1", LongURL: "https://example.com", ClickedAt: now},
			{ClickID: 11, ShortCode: "abc1", LongURL: "https://example.com", ClickedAt: now},
		},
		NextAfterID: 11,
	}}}
	scheduler := NewExternalShortLinkClickScheduler(repo, client, log.New(io.Discard, "", 0), time.Minute, 100, 10)
	scheduler.runOnce(context.Background())
	if repo.through != 12 {
		t.Fatalf("through=%d, want the highest click id 12", repo.through)
	}
	if len(repo.imported) != 2 {
		t.Fatalf("imported %d rows, want both rows handed over for quarantine", len(repo.imported))
	}
	if len(client.acks) != 1 || client.acks[0] != 12 {
		t.Fatalf("acks=%v, want one acknowledgement through 12", client.acks)
	}
}

func TestExternalClickSchedulerHaltsOnPageWithoutUsableID(t *testing.T) {
	events := []string{}
	repo := &fakeExternalSyncRepository{cursor: 10, events: &events}
	client := &fakeExternalShortLinkAPI{events: &events, pages: []*ExternalShortLinkClickPage{{
		Clicks: []models.ExternalShortLinkClick{
			{ClickID: 0, ShortCode: "abc1", LongURL: "https://example.com", ClickedAt: time.Now().UTC()},
		},
		NextAfterID: 0,
	}}}
	scheduler := NewExternalShortLinkClickScheduler(repo, client, log.New(io.Discard, "", 0), time.Minute, 100, 10)
	scheduler.runOnce(context.Background())
	if len(repo.imported) != 1 {
		t.Fatalf("imported %d rows, want the unusable row handed over for quarantine", len(repo.imported))
	}
	if len(client.acks) != 0 {
		t.Fatalf("acks=%v, want no acknowledgement for a page that cannot advance the cursor", client.acks)
	}
	if len(client.fetches) != 1 {
		t.Fatalf("fetches=%v, want a single fetch: a page with no usable id must not be re-fetched in a loop", client.fetches)
	}
}
