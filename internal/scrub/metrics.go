package scrub

import "github.com/prometheus/client_golang/prometheus"

const (
	histBucketStart  = 1.0
	histBucketFactor = 2.0
	histBucketCount  = 8
)

type PrometheusMetrics struct {
	runsTotal      *prometheus.CounterVec
	duration       prometheus.Observer
	rebuildsTotal  prometheus.Counter
	rebuildSeconds prometheus.Observer
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

	rebuilds := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "scrap_scrub_index_rebuilds_total",
		Help: "Total number of projection index rebuilds triggered.",
	})

	rebuildDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "scrap_scrub_index_rebuild_duration_seconds",
		Help:    "Duration of projection index rebuilds in seconds.",
		Buckets: prometheus.ExponentialBuckets(histBucketStart, histBucketFactor, histBucketCount),
	})

	reg.MustRegister(runs, duration, rebuilds, rebuildDuration)

	return &PrometheusMetrics{
		runsTotal:      runs,
		duration:       duration,
		rebuildsTotal:  rebuilds,
		rebuildSeconds: rebuildDuration,
	}
}

func (m *PrometheusMetrics) RecordRun(result string, durationSec float64) {
	m.runsTotal.WithLabelValues(result).Inc()
	m.duration.Observe(durationSec)
}

func (m *PrometheusMetrics) RecordRebuild(durationSec float64) {
	m.rebuildsTotal.Inc()
	m.rebuildSeconds.Observe(durationSec)
}

type DeepScrubPrometheusMetrics struct {
	deepRunsTotal     *prometheus.CounterVec
	framesVerified    prometheus.Counter
	corruptionsTotal  *prometheus.CounterVec
	quarantinesTotal  prometheus.Counter
	blocksQuarantined prometheus.Gauge
	progressRatio     prometheus.Gauge
	badDiskSuspected  prometheus.Gauge
	pausedTotal       prometheus.Counter
	deepDuration      prometheus.Observer
	repairsTotal      *prometheus.CounterVec
}

func NewDeepScrubPrometheusMetrics(reg prometheus.Registerer) *DeepScrubPrometheusMetrics {
	deepRuns := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_scrub_deep_runs_total",
		Help: "Total number of deep scrub runs by result.",
	}, []string{"result"})

	frames := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "scrap_scrub_frames_verified_total",
		Help: "Total number of frames verified by deep scrub.",
	})

	corruptions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_scrub_corruptions_detected_total",
		Help: "Total corruptions detected by type.",
	}, []string{"type"})

	quarantines := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "scrap_scrub_quarantines_total",
		Help: "Total number of blocks quarantined.",
	})

	blocksQ := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "scrap_scrub_blocks_quarantined",
		Help: "Current number of quarantined blocks.",
	})

	progress := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "scrap_scrub_deep_progress_ratio",
		Help: "Progress ratio of current deep scrub cycle.",
	})

	badDisk := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "scrap_scrub_bad_disk_suspected",
		Help: "Set to 1 when bad disk is suspected.",
	})

	paused := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "scrap_scrub_paused_total",
		Help: "Total number of deep scrub pauses due to latency backpressure.",
	})

	duration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "scrap_scrub_deep_duration_seconds",
		Help:    "Duration of deep scrub runs in seconds.",
		Buckets: prometheus.ExponentialBuckets(histBucketStart, histBucketFactor, histBucketCount),
	})

	repairs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_scrub_repairs_total",
		Help: "Total number of block repair attempts by result.",
	}, []string{"result"})

	reg.MustRegister(deepRuns, frames, corruptions, quarantines, blocksQ, progress, badDisk, paused, duration, repairs)

	return &DeepScrubPrometheusMetrics{
		deepRunsTotal:     deepRuns,
		framesVerified:    frames,
		corruptionsTotal:  corruptions,
		quarantinesTotal:  quarantines,
		blocksQuarantined: blocksQ,
		progressRatio:     progress,
		badDiskSuspected:  badDisk,
		pausedTotal:       paused,
		deepDuration:      duration,
		repairsTotal:      repairs,
	}
}

func (m *DeepScrubPrometheusMetrics) RecordDeepRun(result string, durationSec float64) {
	m.deepRunsTotal.WithLabelValues(result).Inc()
	m.deepDuration.Observe(durationSec)
}

func (m *DeepScrubPrometheusMetrics) RecordFramesVerified(n uint64) {
	m.framesVerified.Add(float64(n))
}

func (m *DeepScrubPrometheusMetrics) RecordCorruption(corruptionType string) {
	m.corruptionsTotal.WithLabelValues(corruptionType).Inc()
}

func (m *DeepScrubPrometheusMetrics) RecordQuarantine() {
	m.quarantinesTotal.Inc()
	m.blocksQuarantined.Inc()
}

func (m *DeepScrubPrometheusMetrics) SetBadDiskSuspected(v bool) {
	if v {
		m.badDiskSuspected.Set(1)
	} else {
		m.badDiskSuspected.Set(0)
	}
}

func (m *DeepScrubPrometheusMetrics) RecordPause() {
	m.pausedTotal.Inc()
}

func (m *DeepScrubPrometheusMetrics) SetProgressRatio(v float64) {
	m.progressRatio.Set(v)
}

func (m *DeepScrubPrometheusMetrics) RecordRepair(result string) {
	m.repairsTotal.WithLabelValues(result).Inc()
}

func (m *DeepScrubPrometheusMetrics) DecrementQuarantined() {
	m.blocksQuarantined.Dec()
}
