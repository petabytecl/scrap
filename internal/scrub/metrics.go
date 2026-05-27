package scrub

import "github.com/prometheus/client_golang/prometheus"

const (
	histBucketStart  = 1.0
	histBucketFactor = 2.0
	histBucketCount  = 8
)

type PrometheusMetrics struct {
	runsTotal *prometheus.CounterVec
	duration  prometheus.Observer
}

func NewPrometheusMetrics(reg prometheus.Registerer) *PrometheusMetrics {
	runs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_scrub_light_runs_total",
		Help: "Total number of light scrub runs by result.",
	}, []string{"result"})

	duration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "scrap_scrub_light_duration_seconds",
		Help:    "Duration of light scrub runs in seconds.",
		Buckets: prometheus.ExponentialBuckets(histBucketStart, histBucketFactor, histBucketCount),
	})

	reg.MustRegister(runs, duration)

	return &PrometheusMetrics{
		runsTotal: runs,
		duration:  duration,
	}
}

func (m *PrometheusMetrics) RecordRun(result string, durationSec float64) {
	m.runsTotal.WithLabelValues(result).Inc()
	m.duration.Observe(durationSec)
}
