package businessflow

import (
	"errors"

	"github.com/amirphl/Yamata-no-Orochi/repository"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var smartTargetingExecutionReservationFinalizationFailures = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "smart_targeting_execution_reservation_finalization_failures_total",
		Help: "Execution reservation finalization failures by stable reason.",
	},
	[]string{"reason"},
)

func recordSmartTargetingExecutionReservationFinalizationFailure(err error) {
	smartTargetingExecutionReservationFinalizationFailures.WithLabelValues(smartTargetingExecutionReservationFailureReason(err)).Inc()
}

func smartTargetingExecutionReservationFailureReason(err error) string {
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
