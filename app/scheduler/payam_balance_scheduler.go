package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

const (
	defaultPayamBalanceMonitorInterval = 5 * time.Minute
	defaultPayamBalanceAlertMaxDelay   = time.Hour
	defaultPayamBalanceThresholdTomans = int64(100_000_000)
	maxPayamBalanceThresholdTomans     = int64(922_337_203_685_477_580)
)

// PayamBalanceScheduler monitors the provider account independently from
// campaign dispatch. Alert state is deliberately process-local: a scheduler
// restart sends a fresh immediate warning if the account remains low.
type PayamBalanceScheduler struct {
	client   PayamBalanceClient
	notifier NotificationSender
	adminCfg config.AdminConfig
	logger   *log.Logger

	interval          time.Duration
	thresholdRial     int64
	maxAlertDelay     time.Duration
	now               func() time.Time
	warningRetryDelay func(int) time.Duration

	nextAlertAt time.Time
	alertLevel  int
}

func NewPayamBalanceScheduler(
	client PayamBalanceClient,
	notifier NotificationSender,
	adminCfg config.AdminConfig,
	logger *log.Logger,
	interval time.Duration,
	thresholdTomans int64,
	maxAlertDelay time.Duration,
) *PayamBalanceScheduler {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = defaultPayamBalanceMonitorInterval
	}
	if thresholdTomans <= 0 || thresholdTomans > maxPayamBalanceThresholdTomans {
		thresholdTomans = defaultPayamBalanceThresholdTomans
	}
	if maxAlertDelay <= 0 {
		maxAlertDelay = defaultPayamBalanceAlertMaxDelay
	}
	if maxAlertDelay < interval {
		maxAlertDelay = interval
	}
	return &PayamBalanceScheduler{
		client:            client,
		notifier:          notifier,
		adminCfg:          adminCfg,
		logger:            logger,
		interval:          interval,
		thresholdRial:     thresholdTomans * 10,
		maxAlertDelay:     maxAlertDelay,
		now:               time.Now,
		warningRetryDelay: payamRetryBackoffDelay,
	}
}

func (s *PayamBalanceScheduler) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.logger.Printf("payamsms balance monitor: started interval=%s threshold_tomans=%s max_alert_interval=%s", s.interval, formatPayamTomans(s.thresholdRial), s.maxAlertDelay)
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

func (s *PayamBalanceScheduler) runOnce(ctx context.Context) {
	if s.client == nil {
		s.logger.Printf("payamsms balance monitor: client is not configured")
		return
	}
	balanceRial, err := s.client.FetchBalance(ctx)
	if err != nil {
		s.logger.Printf("payamsms balance monitor: fetch failed: %v", err)
		return
	}
	if balanceRial >= s.thresholdRial {
		if s.alertLevel > 0 {
			s.logger.Printf("payamsms balance monitor: balance recovered balance_tomans=%s", formatPayamTomans(balanceRial))
		}
		s.alertLevel = 0
		s.nextAlertAt = time.Time{}
		return
	}

	now := s.now().UTC()
	if !s.nextAlertAt.IsZero() && now.Before(s.nextAlertAt) {
		return
	}
	if len(s.adminCfg.ActiveMobiles()) == 0 {
		s.logger.Printf("payamsms balance monitor: balance is low (%s TMN) but no active admin mobiles are configured", formatPayamTomans(balanceRial))
		return
	}

	message := fmt.Sprintf("WARNING: PayamSMS balance is low: %s TMN (threshold: %s TMN).", formatPayamTomans(balanceRial), formatPayamTomans(s.thresholdRial))
	if err := s.sendWarning(ctx, message); err != nil {
		s.logger.Printf("payamsms balance monitor: warning notification failed: %v", err)
		return
	}

	delay := retryBackoffDelay(s.alertLevel, s.interval, s.maxAlertDelay)
	s.alertLevel++
	s.nextAlertAt = now.Add(delay)
	s.logger.Printf("payamsms balance monitor: low balance warning sent balance_tomans=%s next_alert_at=%s", formatPayamTomans(balanceRial), s.nextAlertAt.Format(time.RFC3339))
}

func (s *PayamBalanceScheduler) sendWarning(ctx context.Context, message string) error {
	if s.notifier == nil {
		return fmt.Errorf("notification service is not configured")
	}
	mobiles := s.adminCfg.ActiveMobiles()
	var err error
	for attempt := 0; ; attempt++ {
		err = s.notifier.SendSMSBulk(ctx, mobiles, message, nil)
		if !isPayamRetryableError(err) {
			return err
		}
		if payamRetryMaxAttempts > 0 && attempt+1 >= payamRetryMaxAttempts {
			return err
		}
		delay := payamRetryBackoffDelay(attempt)
		if s.warningRetryDelay != nil {
			delay = s.warningRetryDelay(attempt)
		}
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return ctx.Err()
		}
	}
}

func formatPayamTomans(rials int64) string {
	tomans, remainder := rials/10, rials%10
	if remainder == 0 {
		return fmt.Sprintf("%d", tomans)
	}
	return fmt.Sprintf("%d.%d", tomans, remainder)
}
