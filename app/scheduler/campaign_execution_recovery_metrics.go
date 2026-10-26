package scheduler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var campaignExecutionRecoveryTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "campaign_execution_recovery_total",
		Help: "Stale campaign execution recovery outcomes by scheduler and safety decision.",
	},
	[]string{"scheduler", "outcome"},
)
