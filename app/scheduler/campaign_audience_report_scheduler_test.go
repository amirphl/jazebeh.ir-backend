package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockingCampaignAudienceReportExecutor struct {
	started chan struct{}
}

func (e *blockingCampaignAudienceReportExecutor) ExecuteNext(ctx context.Context, _ time.Duration) error {
	e.started <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func (*blockingCampaignAudienceReportExecutor) Cleanup(context.Context) error { return nil }

func TestCampaignAudienceReportSchedulerUsesConfiguredWorkerLimit(t *testing.T) {
	executor := &blockingCampaignAudienceReportExecutor{started: make(chan struct{}, 3)}
	scheduler := NewCampaignAudienceReportScheduler(executor, nil, time.Minute, time.Hour, time.Hour+time.Minute, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	scheduler.runOnce(ctx, &workers)

	for range 2 {
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatal("configured report worker did not start")
		}
	}
	select {
	case <-executor.started:
		t.Fatal("scheduler exceeded the configured report worker limit")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	workers.Wait()
}
