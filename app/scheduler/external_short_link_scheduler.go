package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/prometheus/client_golang/prometheus"
)

const defaultExternalShortLinkClickPageSize = 1_000

var (
	externalMappingLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "yamata_external_shortlink_last_mapping_upload_timestamp_seconds",
		Help: "Unix time of the last successful external short-link mapping batch.",
	})
	externalClickFetchLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "yamata_external_shortlink_last_click_fetch_timestamp_seconds",
		Help: "Unix time of the last successful external click page fetch.",
	})
	externalClickCursor = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "yamata_external_shortlink_imported_click_id",
		Help: "Highest external click ID committed to production.",
	})
	externalClickSyncLag = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "yamata_external_shortlink_click_sync_lag_seconds",
		Help: "Age of the newest click in the most recently imported page.",
	})
)

func init() {
	prometheus.MustRegister(externalMappingLastSuccess, externalClickFetchLastSuccess, externalClickCursor, externalClickSyncLag)
}

type ExternalShortLinkMappingScheduler struct {
	repo      repository.ShortLinkRepository
	client    ExternalShortLinkAPI
	logger    *log.Logger
	interval  time.Duration
	batchSize int
}

func NewExternalShortLinkMappingScheduler(
	repo repository.ShortLinkRepository,
	client ExternalShortLinkAPI,
	logger *log.Logger,
	interval time.Duration,
	batchSize int,
) *ExternalShortLinkMappingScheduler {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ExternalShortLinkMappingScheduler{repo: repo, client: client, logger: logger, interval: interval, batchSize: batchSize}
}

func (s *ExternalShortLinkMappingScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.logger.Printf("external short-link mapping scheduler: started interval=%s batch_size=%d", s.interval, s.batchSize)
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (s *ExternalShortLinkMappingScheduler) runOnce(ctx context.Context) {
	for ctx.Err() == nil {
		links, err := s.repo.ListPendingExternalPublication(ctx, s.batchSize)
		if err != nil {
			s.logger.Printf("external short-link mapping scheduler: list failed: %v", err)
			return
		}
		if len(links) == 0 {
			return
		}
		if err := s.client.UploadMappings(ctx, links); err != nil {
			s.logger.Printf("external short-link mapping scheduler: upload failed: %v", err)
			return
		}
		uids := make([]string, 0, len(links))
		for _, link := range links {
			uids = append(uids, link.UID)
		}
		publishedAt := time.Now().UTC()
		if err := s.repo.MarkExternallyPublished(ctx, uids, publishedAt); err != nil {
			s.logger.Printf("external short-link mapping scheduler: mark published failed: %v", err)
			return
		}
		externalMappingLastSuccess.Set(float64(publishedAt.Unix()))
		s.logger.Printf("external short-link mapping scheduler: published batch count=%d", len(links))
		if len(links) < s.batchSize {
			return
		}
	}
}

type ExternalShortLinkClickScheduler struct {
	repo           repository.ExternalShortLinkSyncRepository
	client         ExternalShortLinkAPI
	logger         *log.Logger
	interval       time.Duration
	pageSize       int
	maxPagesPerRun int
}

func NewExternalShortLinkClickScheduler(
	repo repository.ExternalShortLinkSyncRepository,
	client ExternalShortLinkAPI,
	logger *log.Logger,
	interval time.Duration,
	pageSize int,
	maxPagesPerRun int,
) *ExternalShortLinkClickScheduler {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if pageSize <= 0 {
		pageSize = defaultExternalShortLinkClickPageSize
	}
	if maxPagesPerRun <= 0 {
		maxPagesPerRun = 1000
	}
	return &ExternalShortLinkClickScheduler{
		repo: repo, client: client, logger: logger, interval: interval, pageSize: pageSize, maxPagesPerRun: maxPagesPerRun,
	}
}

func (s *ExternalShortLinkClickScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.logger.Printf(
		"external short-link click scheduler: started interval=%s page_size=%d max_pages_per_run=%d",
		s.interval,
		s.pageSize,
		s.maxPagesPerRun,
	)
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (s *ExternalShortLinkClickScheduler) runOnce(ctx context.Context) {
	cursor, err := s.repo.Cursor(ctx, models.ShortLinkClickSourceExternal)
	if err != nil {
		s.logger.Printf("external short-link click scheduler: read cursor failed: %v", err)
		return
	}
	for pageNumber := 0; pageNumber < s.maxPagesPerRun && ctx.Err() == nil; pageNumber++ {
		page, err := s.client.FetchClicks(ctx, cursor, s.pageSize)
		if err != nil {
			s.logger.Printf("external short-link click scheduler: fetch after_id=%d failed: %v", cursor, err)
			return
		}
		externalClickFetchLastSuccess.Set(float64(time.Now().Unix()))
		if err := validateExternalClickPage(cursor, page); err != nil {
			s.logger.Printf("external short-link click scheduler: invalid page after_id=%d: %v", cursor, err)
			return
		}
		if len(page.Clicks) == 0 {
			// A previous acknowledgement may have failed after the production
			// transaction committed. Retry the cumulative acknowledgement even
			// when no newer click exists, otherwise a quiet source retains old
			// rows forever.
			if cursor > 0 {
				if err := s.client.AcknowledgeClicks(ctx, cursor); err != nil {
					s.logger.Printf("external short-link click scheduler: retry acknowledge through_click_id=%d failed: %v", cursor, err)
				}
			}
			return
		}
		through := page.Clicks[len(page.Clicks)-1].ClickID
		if err := s.repo.ImportPage(ctx, models.ShortLinkClickSourceExternal, page.Clicks, through); err != nil {
			s.logger.Printf("external short-link click scheduler: import through_click_id=%d failed: %v", through, err)
			return
		}
		cursor = through
		externalClickCursor.Set(float64(cursor))
		newest := page.Clicks[len(page.Clicks)-1].ClickedAt
		if !newest.IsZero() {
			externalClickSyncLag.Set(max(0, time.Since(newest).Seconds()))
		}
		if err := s.client.AcknowledgeClicks(ctx, cursor); err != nil {
			// Production commit and cursor advancement already succeeded. The next
			// run retries this cumulative acknowledgement, including on an empty page.
			s.logger.Printf("external short-link click scheduler: acknowledge through_click_id=%d failed: %v", cursor, err)
		}
		s.logger.Printf(
			"external short-link click scheduler: imported page count=%d through_click_id=%d has_more=%t",
			len(page.Clicks),
			cursor,
			page.HasMore,
		)
		if !page.HasMore {
			return
		}
	}
}

func validateExternalClickPage(afterID int64, page *ExternalShortLinkClickPage) error {
	if page == nil {
		return fmt.Errorf("response page is nil")
	}
	previous := afterID
	for _, click := range page.Clicks {
		if click.ClickID <= previous {
			return fmt.Errorf("click IDs are not strictly increasing: previous=%d current=%d", previous, click.ClickID)
		}
		previous = click.ClickID
	}
	if len(page.Clicks) == 0 {
		if page.NextAfterID != afterID || page.HasMore {
			return fmt.Errorf("empty page has inconsistent cursor or has_more")
		}
		return nil
	}
	if page.NextAfterID != previous {
		return fmt.Errorf("next_after_id=%d does not match last click_id=%d", page.NextAfterID, previous)
	}
	return nil
}
