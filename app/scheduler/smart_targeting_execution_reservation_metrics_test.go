package scheduler

import (
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/repository"
)

func TestExecutionReservationSchedulerFailureReasonsRemainDistinct(t *testing.T) {
	tests := []struct {
		err    error
		reason string
	}{
		{repository.ErrSmartTargetingExecutionReservationInsufficientCapacity, "insufficient_capacity"},
		{repository.ErrSmartTargetingExecutionReservationConflict, "conflict"},
		{repository.ErrSmartTargetingExecutionReservationConcurrency, "concurrency"},
		{repository.ErrSmartTargetingExecutionReservationStale, "stale"},
		{repository.ErrSmartTargetingExecutionReservationCorrupt, "corrupt"},
		{repository.ErrSmartTargetingExecutionReservationMissing, "missing"},
	}
	for _, test := range tests {
		if got := smartTargetingExecutionReservationSchedulerFailureReason(test.err); got != test.reason {
			t.Fatalf("reason for %v = %q, want %q", test.err, got, test.reason)
		}
	}
}
