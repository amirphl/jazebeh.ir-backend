package scheduler

import "context"

// CampaignExecutionLimiter bounds the audience-selection phase across every
// platform scheduler in one worker process. That phase can select a large
// audience and write a large reservation/allocation, so the database
// connection-pool limit alone is not a sufficient back-pressure mechanism.
type CampaignExecutionLimiter struct {
	slots chan struct{}
}

// NewCampaignExecutionLimiter returns a shared limiter. Two concurrent bulk
// selections provide useful throughput without allowing a ready-campaign burst
// to create unbounded lock contention.
func NewCampaignExecutionLimiter(maxParallelRuns int) *CampaignExecutionLimiter {
	if maxParallelRuns <= 0 {
		maxParallelRuns = 2
	}
	return &CampaignExecutionLimiter{slots: make(chan struct{}, maxParallelRuns)}
}

// Acquire waits until an audience-selection slot is available or the scheduler
// is stopped. A nil limiter preserves compatibility for callers that construct
// a scheduler directly (for example focused unit tests).
func (l *CampaignExecutionLimiter) Acquire(ctx context.Context) bool {
	if l == nil {
		return true
	}
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *CampaignExecutionLimiter) Release() {
	if l == nil {
		return
	}
	<-l.slots
}
