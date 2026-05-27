package shard

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	uploadHistBucketStart  = 0.001
	uploadHistBucketFactor = 2.0
	uploadHistBucketCount  = 16
)

type UploadMetrics interface {
	SetPending(shardID uint64, pendingBytes int64, pendingBlocks int)
	RecordUpload(shardID uint64, status string, duration time.Duration)
	RecordVerify(shardID uint64, status string)
	SetPressureLevel(shardID uint64, level UploadPressureLevel)
	SetConcurrency(shardID uint64, concurrency int)
	SetAuthPaused(shardID uint64, paused bool)
}

type UploadPrometheusMetrics struct {
	pendingBytes  *prometheus.GaugeVec
	pendingBlocks *prometheus.GaugeVec
	uploadsTotal  *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	verifyTotal   *prometheus.CounterVec
	pressureLevel *prometheus.GaugeVec
	concurrency   *prometheus.GaugeVec
	authPaused    *prometheus.GaugeVec
}

func NewUploadPrometheusMetrics(reg prometheus.Registerer) *UploadPrometheusMetrics {
	pendingBytes := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scrap_upload_pending_bytes",
		Help: "Bytes in the Upload Outbox awaiting backend upload.",
	}, []string{"shard"})
	pendingBlocks := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scrap_upload_pending_blocks",
		Help: "Blocks in the Upload Outbox awaiting backend upload.",
	}, []string{"shard"})
	uploads := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_upload_total",
		Help: "Total backend block upload outcomes by status.",
	}, []string{"shard", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "scrap_upload_duration_seconds",
		Help:    "Duration of backend block uploads in seconds.",
		Buckets: prometheus.ExponentialBuckets(uploadHistBucketStart, uploadHistBucketFactor, uploadHistBucketCount),
	}, []string{"shard"})
	verify := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scrap_upload_verify_total",
		Help: "Total backend upload HEAD verification outcomes by status.",
	}, []string{"shard", "status"})
	pressure := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scrap_upload_pressure_level",
		Help: "Current upload pressure level: 0=ok, 1=warn, 2=pressure, 3=critical.",
	}, []string{"shard"})
	concurrency := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scrap_upload_concurrency",
		Help: "Current effective backend upload concurrency.",
	}, []string{"shard"})
	authPaused := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scrap_upload_auth_paused",
		Help: "Whether backend uploads are paused due to authentication failure.",
	}, []string{"shard"})

	reg.MustRegister(pendingBytes, pendingBlocks, uploads, duration, verify, pressure, concurrency, authPaused)
	return &UploadPrometheusMetrics{
		pendingBytes:  pendingBytes,
		pendingBlocks: pendingBlocks,
		uploadsTotal:  uploads,
		duration:      duration,
		verifyTotal:   verify,
		pressureLevel: pressure,
		concurrency:   concurrency,
		authPaused:    authPaused,
	}
}

func (m *UploadPrometheusMetrics) SetPending(shardID uint64, pendingBytes int64, pendingBlocks int) {
	shard := shardMetricLabel(shardID)
	m.pendingBytes.WithLabelValues(shard).Set(float64(pendingBytes))
	m.pendingBlocks.WithLabelValues(shard).Set(float64(pendingBlocks))
}

func (m *UploadPrometheusMetrics) RecordUpload(shardID uint64, status string, duration time.Duration) {
	shard := shardMetricLabel(shardID)
	m.uploadsTotal.WithLabelValues(shard, status).Inc()
	m.duration.WithLabelValues(shard).Observe(duration.Seconds())
}

func (m *UploadPrometheusMetrics) RecordVerify(shardID uint64, status string) {
	m.verifyTotal.WithLabelValues(shardMetricLabel(shardID), status).Inc()
}

func (m *UploadPrometheusMetrics) SetPressureLevel(shardID uint64, level UploadPressureLevel) {
	m.pressureLevel.WithLabelValues(shardMetricLabel(shardID)).Set(float64(level))
}

func (m *UploadPrometheusMetrics) SetConcurrency(shardID uint64, concurrency int) {
	m.concurrency.WithLabelValues(shardMetricLabel(shardID)).Set(float64(concurrency))
}

func (m *UploadPrometheusMetrics) SetAuthPaused(shardID uint64, paused bool) {
	value := 0.0
	if paused {
		value = 1
	}
	m.authPaused.WithLabelValues(shardMetricLabel(shardID)).Set(value)
}

func (s *Shard) recordUploadPressureMetrics(stats uploadPressureStats, level UploadPressureLevel, concurrency int) {
	if s.upload.Metrics == nil {
		return
	}
	s.upload.Metrics.SetPending(s.shardID, stats.pendingBytes, stats.pendingBlocks)
	s.upload.Metrics.SetPressureLevel(s.shardID, level)
	s.upload.Metrics.SetConcurrency(s.shardID, concurrency)
}

func (s *Shard) recordUploadMetric(status string, duration time.Duration) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.RecordUpload(s.shardID, status, duration)
	}
}

func (s *Shard) recordUploadVerifyMetric(status string) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.RecordVerify(s.shardID, status)
	}
}

func (s *Shard) setUploadConcurrencyMetric(concurrency int) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.SetConcurrency(s.shardID, concurrency)
	}
}

func (s *Shard) setUploadAuthPausedMetric(paused bool) {
	if s.upload.Metrics != nil {
		s.upload.Metrics.SetAuthPaused(s.shardID, paused)
	}
}

func shardMetricLabel(shardID uint64) string {
	return strconv.FormatUint(shardID, 10)
}
