package observability

import "github.com/prometheus/client_golang/prometheus"

var mockOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "yamata_mock_external_operations_total",
	Help: "Simulated external operations by provider, action, and outcome.",
}, []string{"provider", "action", "outcome"})

func init() { prometheus.MustRegister(mockOperations) }

// RecordMockOperation uses fixed labels supplied by integration implementations.
func RecordMockOperation(provider, action, outcome string, count int) {
	if count > 0 {
		mockOperations.WithLabelValues(provider, action, outcome).Add(float64(count))
	}
}
