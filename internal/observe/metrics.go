package observe

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	OutcomeSuccess  = "success"
	OutcomeError    = "error"
	OutcomeNotFound = "not_found"

	VerificationOutcomeMatch    = "match"
	VerificationOutcomeMismatch = "mismatch"
	VerificationOutcomeError    = OutcomeError
	VerificationOutcomeSkipped  = "skipped"

	BackendOperationHead = "head"
)

var defaultMetrics = newMetrics()

type metrics struct {
	registry       *prometheus.Registry
	writeLatency   *prometheus.HistogramVec
	verification   *prometheus.CounterVec
	backendProbe   *prometheus.CounterVec
	raftQueueDepth prometheus.Gauge
}

func newMetrics() *metrics {
	registry := prometheus.NewRegistry()
	m := &metrics{
		registry: registry,
		writeLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "scrap",
			Name:      "write_latency_seconds",
			Help:      "Local block write latency from admission through durable sync.",
			Buckets: []float64{
				0.005,
				0.01,
				0.025,
				0.05,
				0.1,
				0.25,
				0.5,
				1,
				2.5,
				5,
				10,
			},
		}, []string{"outcome"}),
		verification: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "scrap",
			Name:      "verification_total",
			Help:      "Total backend verification window outcomes.",
		}, []string{"outcome"}),
		backendProbe: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "scrap",
			Name:      "backend_probe_total",
			Help:      "Total backend probe outcomes by bounded operation.",
		}, []string{"operation", "outcome"}),
		raftQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "scrap",
			Name:      "raft_queue_depth",
			Help:      "Current number of Raft metadata proposals pending append or apply.",
		}),
	}
	registry.MustRegister(m.writeLatency, m.verification, m.backendProbe, m.raftQueueDepth)
	m.initializeZeroValueSeries()
	return m
}

func (m *metrics) initializeZeroValueSeries() {
	for _, outcome := range []string{OutcomeSuccess, OutcomeError} {
		m.writeLatency.WithLabelValues(outcome)
	}
	for _, outcome := range []string{VerificationOutcomeMatch, VerificationOutcomeMismatch, VerificationOutcomeError, VerificationOutcomeSkipped} {
		m.verification.WithLabelValues(outcome)
	}
	for _, outcome := range []string{OutcomeSuccess, OutcomeNotFound, OutcomeError} {
		m.backendProbe.WithLabelValues(BackendOperationHead, outcome)
	}
	m.raftQueueDepth.Set(0)
}

func Handler() http.Handler {
	return promhttp.HandlerFor(defaultMetrics.registry, promhttp.HandlerOpts{
		Registry: defaultMetrics.registry,
	})
}

func RecordWriteLatency(outcome string, elapsed time.Duration) {
	if elapsed < 0 {
		elapsed = 0
	}
	defaultMetrics.writeLatency.WithLabelValues(normalizeWriteOutcome(outcome)).Observe(elapsed.Seconds())
}

func RecordVerification(outcome string) {
	defaultMetrics.verification.WithLabelValues(normalizeVerificationOutcome(outcome)).Inc()
}

func RecordBackendProbe(operation, outcome string) {
	defaultMetrics.backendProbe.WithLabelValues(normalizeBackendOperation(operation), normalizeBackendOutcome(outcome)).Inc()
}

func SetRaftQueueDepth(depth int) {
	if depth < 0 {
		depth = 0
	}
	defaultMetrics.raftQueueDepth.Set(float64(depth))
}

func IncrementRaftQueueDepth() {
	defaultMetrics.raftQueueDepth.Inc()
}

func DecrementRaftQueueDepth() {
	defaultMetrics.raftQueueDepth.Dec()
}

func normalizeWriteOutcome(outcome string) string {
	if outcome == OutcomeSuccess {
		return OutcomeSuccess
	}
	return OutcomeError
}

func normalizeVerificationOutcome(outcome string) string {
	switch outcome {
	case VerificationOutcomeMatch, VerificationOutcomeMismatch, VerificationOutcomeError, VerificationOutcomeSkipped:
		return outcome
	default:
		return VerificationOutcomeMismatch
	}
}

func normalizeBackendOperation(operation string) string {
	if operation == BackendOperationHead {
		return BackendOperationHead
	}
	return "unknown"
}

func normalizeBackendOutcome(outcome string) string {
	switch outcome {
	case OutcomeSuccess, OutcomeNotFound:
		return outcome
	default:
		return OutcomeError
	}
}
