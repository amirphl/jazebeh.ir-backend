package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

// AsiaTechMonitor refreshes the account MPS limiter and gives operations a
// rate-limited signal for an inaccessible or low-credit account. Credit keeps
// the exact unit reported by UserInfo; the provider documentation specifies no
// currency conversion.
type AsiaTechMonitor struct {
	client             AsiaTechProviderOperations
	notifier           NotificationSender
	admin              config.AdminConfig
	logger             *log.Logger
	interval, maxDelay time.Duration
	threshold          *float64
	nextAlert          time.Time
	level              int
}

func NewAsiaTechMonitor(client AsiaTechProviderOperations, notifier NotificationSender, admin config.AdminConfig, logger *log.Logger, cfg config.AsiaTechSMSConfig) *AsiaTechMonitor {
	if logger == nil {
		logger = log.Default()
	}
	interval := cfg.MonitorInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	maxDelay := cfg.AlertMaxInterval
	if maxDelay < interval {
		maxDelay = time.Hour
	}
	return &AsiaTechMonitor{client: client, notifier: notifier, admin: admin, logger: logger, interval: interval, maxDelay: maxDelay, threshold: cfg.LowCreditThreshold}
}
func (m *AsiaTechMonitor) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(m.interval)
		defer t.Stop()
		m.run(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.run(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}
func (m *AsiaTechMonitor) run(ctx context.Context) {
	if m.client == nil {
		return
	}
	if err := m.client.Ping(ctx); err != nil {
		m.alert(ctx, "AsiaTech health check failed: "+err.Error())
		return
	}
	info, err := m.client.UserInfo(ctx)
	if err != nil {
		m.alert(ctx, "AsiaTech UserInfo failed: "+err.Error())
		return
	}
	if m.threshold != nil && info.Credit < *m.threshold {
		m.alert(ctx, fmt.Sprintf("AsiaTech credit is low: %g (threshold: %g)", info.Credit, *m.threshold))
		return
	}
	m.level = 0
	m.nextAlert = time.Time{}
}
func (m *AsiaTechMonitor) alert(ctx context.Context, text string) {
	now := time.Now().UTC()
	if !m.nextAlert.IsZero() && now.Before(m.nextAlert) {
		return
	}
	m.logger.Printf("asiatech monitor: %s", text)
	if m.notifier == nil || len(m.admin.ActiveMobiles()) == 0 {
		return
	}
	if err := m.notifier.SendSMSBulk(ctx, m.admin.ActiveMobiles(), "WARNING: "+text, nil); err != nil {
		return
	}
	delay := m.interval
	for i := 0; i < m.level && delay < m.maxDelay; i++ {
		delay *= 2
	}
	if delay > m.maxDelay {
		delay = m.maxDelay
	}
	m.level++
	m.nextAlert = now.Add(delay)
}
