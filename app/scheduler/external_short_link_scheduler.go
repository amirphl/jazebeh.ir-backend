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
	s.logger.Printf("external short-link click scheduler: started interval=%s page_size=%d max_pages_per_run=%d", s.interval, s.pageSize, s.maxPagesPerRun)
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
		through, newest := externalClickPageCursor(cursor, page.Clicks)
		if through != page.NextAfterID {
			// The cursor below is derived from the rows themselves, so a page
			// that lowers, repeats, or omits an ID still makes progress instead
			// of stalling click import forever. Report the contract violation.
			s.logger.Printf(
				"external short-link click scheduler: page after_id=%d reported next_after_id=%d but its highest click id is %d",
				cursor,
				page.NextAfterID,
				through,
			)
		}
		if err := s.repo.ImportPage(ctx, models.ShortLinkClickSourceExternal, page.Clicks, through); err != nil {
			s.logger.Printf("external short-link click scheduler: import through_click_id=%d failed: %v", through, err)
			return
		}
		if through <= cursor {
			// ImportPage quarantines every row it cannot import, so nothing was
			// lost, but the cursor has nowhere to advance to. Retrying the same
			// page would only repeat that work, so wait for operator review.
			s.logger.Printf(
				"external short-link click scheduler: page after_id=%d carried %d rows but no click id above the cursor; halting this run for operator review",
				cursor,
				len(page.Clicks),
			)
			return
		}
		cursor = through
		externalClickCursor.Set(float64(cursor))
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
	// Rows must be ordered for the import cursor to mean anything, but a single
	// out-of-order, repeated, or unusable ID is quarantined per row by the
	// importer rather than failing the whole page: a hard failure here stops
	// every later click from ever being imported. Only an empty page, which
	// offers nothing to advance the cursor to, is structurally fatal.
	if len(page.Clicks) == 0 && (page.NextAfterID != afterID || page.HasMore) {
		return fmt.Errorf("empty page has inconsistent cursor or has_more")
	}
	return nil
}

// externalClickPageCursor derives the cursor a page may advance to, plus the
// newest click timestamp it carried. The external service is expected to return
// strictly increasing click IDs, but ordering anomalies must not stall the
// importer: the cursor only ever moves to the highest ID actually accounted for
// in the page, so a malformed page can neither skip nor rewind the stream.
func externalClickPageCursor(cursor int64, clicks []models.ExternalShortLinkClick) (int64, time.Time) {
	highest := cursor
	var newest time.Time
	for _, click := range clicks {
		if click.ClickID > highest {
			highest = click.ClickID
		}
		if click.ClickedAt.After(newest) {
			newest = click.ClickedAt
		}
	}
	return highest, newest
}
