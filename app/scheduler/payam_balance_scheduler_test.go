package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/config"
)

type stubPayamBalanceClient struct {
	balance int64
	err     error
	calls   int
}

func (c *stubPayamBalanceClient) FetchBalance(context.Context) (int64, error) {
	c.calls++
	return c.balance, c.err
}

type stubBalanceNotifier struct {
	calls    int
	mobiles  []string
	messages []string
	err      error
	failFor  int
}

func (n *stubBalanceNotifier) SendSMS(context.Context, string, string, *int64) error { return nil }

func (n *stubBalanceNotifier) SendSMSBulk(_ context.Context, mobiles []string, message string, _ *int64) error {
	n.calls++
	n.mobiles = append([]string(nil), mobiles...)
	n.messages = append(n.messages, message)
	if n.calls <= n.failFor {
		return n.err
	}
	return nil
}

func TestPayamBalanceSchedulerExponentiallyDelaysAndResetsWarnings(t *testing.T) {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	client := &stubPayamBalanceClient{balance: 999_999_999}
	notifier := &stubBalanceNotifier{}
	s := NewPayamBalanceScheduler(client, notifier, config.AdminConfig{Mobiles: []string{"989121234567"}}, log.New(io.Discard, "", 0), 5*time.Minute, 100_000_000, time.Hour)
	s.now = func() time.Time { return now }

	s.runOnce(context.Background())
	if notifier.calls != 1 || !strings.Contains(notifier.messages[0], "99999999.9 TMN") {
		t.Fatalf("first warning calls=%d messages=%v", notifier.calls, notifier.messages)
	}
	if want := now.Add(5 * time.Minute); !s.nextAlertAt.Equal(want) {
		t.Fatalf("next alert = %s, want %s", s.nextAlertAt, want)
	}

	now = now.Add(4 * time.Minute)
	s.runOnce(context.Background())
	if notifier.calls != 1 {
		t.Fatalf("warning sent before initial delay: calls=%d", notifier.calls)
	}
	now = now.Add(time.Minute)
	s.runOnce(context.Background())
	if notifier.calls != 2 || !s.nextAlertAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("second warning calls=%d next=%s", notifier.calls, s.nextAlertAt)
	}

	client.balance = 1_000_000_000
	s.runOnce(context.Background())
	if s.alertLevel != 0 || !s.nextAlertAt.IsZero() {
		t.Fatalf("recovery did not reset alert state: level=%d next=%s", s.alertLevel, s.nextAlertAt)
	}
	client.balance = 999_999_999
	s.runOnce(context.Background())
	if notifier.calls != 3 {
		t.Fatalf("low balance after recovery did not warn immediately: calls=%d", notifier.calls)
	}
}

func TestPayamBalanceSchedulerDoesNotAdvanceAlertAfterNotificationFailure(t *testing.T) {
	client := &stubPayamBalanceClient{balance: 1}
	notifier := &stubBalanceNotifier{err: errors.New("PayamSMS send http status 503: temporary"), failFor: 2}
	s := NewPayamBalanceScheduler(client, notifier, config.AdminConfig{Mobiles: []string{"989121234567"}}, log.New(io.Discard, "", 0), time.Minute, 100, time.Hour)
	s.warningRetryDelay = func(int) time.Duration { return 0 }

	s.runOnce(context.Background())
	if notifier.calls != 3 || s.alertLevel != 1 || s.nextAlertAt.IsZero() {
		t.Fatalf("calls=%d level=%d next=%s, want retry then one scheduled warning", notifier.calls, s.alertLevel, s.nextAlertAt)
	}

	notifier = &stubBalanceNotifier{err: errors.New("permanent failure"), failFor: 10}
	s.notifier = notifier
	s.nextAlertAt = time.Time{}
	s.alertLevel = 0
	s.runOnce(context.Background())
	if notifier.calls != 1 || s.alertLevel != 0 || !s.nextAlertAt.IsZero() {
		t.Fatalf("failed warning advanced state: calls=%d level=%d next=%s", notifier.calls, s.alertLevel, s.nextAlertAt)
	}
}

func TestPayamBalanceSchedulerClampsOverflowingThreshold(t *testing.T) {
	s := NewPayamBalanceScheduler(nil, nil, config.AdminConfig{}, log.New(io.Discard, "", 0), time.Minute, maxPayamBalanceThresholdTomans+1, time.Hour)
	if s.thresholdRial != defaultPayamBalanceThresholdTomans*10 {
		t.Fatalf("thresholdRial = %d, want default %d", s.thresholdRial, defaultPayamBalanceThresholdTomans*10)
	}
}

func TestPayamBalanceSchedulerSkipsWarningWithoutAdmins(t *testing.T) {
	client := &stubPayamBalanceClient{balance: 1}
	notifier := &stubBalanceNotifier{}
	s := NewPayamBalanceScheduler(client, notifier, config.AdminConfig{}, log.New(io.Discard, "", 0), time.Minute, 100, time.Hour)
	s.runOnce(context.Background())
	if notifier.calls != 0 || s.alertLevel != 0 {
		t.Fatalf("warning sent or state advanced without admins: calls=%d level=%d", notifier.calls, s.alertLevel)
	}
}
