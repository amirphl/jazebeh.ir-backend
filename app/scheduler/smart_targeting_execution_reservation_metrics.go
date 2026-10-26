package scheduler

import (
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var smartTargetingExecutionReservationSchedulerFailures = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "smart_targeting_execution_reservation_scheduler_failures_total",
		Help: "Execution reservation materialization failures by stable reason.",
	},
	[]string{"reason"},
)

func recordSmartTargetingExecutionReservationSchedulerFailure(err error) {
	smartTargetingExecutionReservationSchedulerFailures.WithLabelValues(smartTargetingExecutionReservationSchedulerFailureReason(err)).Inc()
}

func smartTargetingExecutionReservationSchedulerFailureReason(err error) string {
	switch {
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationInsufficientCapacity):
		return "insufficient_capacity"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationConflict):
		return "conflict"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationConcurrency):
		return "concurrency"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationStale):
		return "stale"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationCorrupt):
		return "corrupt"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationMissing):
		return "missing"
	case errors.Is(err, repository.ErrSmartTargetingExecutionReservationUnavailable):
		return "unavailable"
	default:
		return "other"
	}
}
