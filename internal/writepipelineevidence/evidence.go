package writepipelineevidence

import (
	"fmt"
	"sort"
	"time"
)

const (
	ReportKind = "write-pipeline-performance-evidence"

	StatusPassed = "passed"
	StatusFailed = "failed"
)

type Options struct {
	ReleaseSHA         string
	DirtyTree          string
	RunnerID           string
	SampleCount        int
	Concurrency        int
	DocumentSizeBytes  uint64
	MaxDuration        time.Duration
	MinWritesPerSecond float64
	MaxP99AckLatency   time.Duration
	Now                func() time.Time
}

type Report struct {
	ReportKind                string               `json:"report_kind"`
	GeneratedAt               string               `json:"generated_at"`
	Status                    string               `json:"status"`
	Release                   ReleaseIdentity      `json:"release"`
	Workload                  WorkloadShape        `json:"workload"`
	Results                   Results              `json:"results"`
	Thresholds                Thresholds           `json:"thresholds"`
	ThresholdViolations       []ThresholdViolation `json:"threshold_violations"`
	RequiredFailureResolution string               `json:"required_failure_resolution"`
	EvidenceLimits            []string             `json:"evidence_limits"`
}

type ReleaseIdentity struct {
	ReleaseSHA string `json:"release_sha"`
	DirtyTree  string `json:"dirty_tree"`
}

type WorkloadShape struct {
	RunnerID            string `json:"runner_id"`
	Concurrency         int    `json:"concurrency"`
	SampleCount         int    `json:"sample_count"`
	DocumentSizeBytes   uint64 `json:"document_size_bytes"`
	TotalBytes          uint64 `json:"total_bytes"`
	MaxDurationMillis   int64  `json:"max_duration_millis"`
	NonProductionOnly   bool   `json:"non_production_only"`
	ApplicationBoundary string `json:"application_boundary"`
}

type Results struct {
	DurationMillis   int64             `json:"duration_millis"`
	SuccessCount     int               `json:"success_count"`
	ErrorCount       int               `json:"error_count"`
	TotalBytes       uint64            `json:"total_bytes"`
	Throughput       ThroughputSummary `json:"throughput"`
	ACKLatency       LatencySummary    `json:"ack_latency"`
	Samples          []WriteSample     `json:"samples"`
	ComponentSignals []ComponentSignal `json:"component_signals"`
}

type ThroughputSummary struct {
	WritesPerSecond float64 `json:"writes_per_second"`
	BytesPerSecond  float64 `json:"bytes_per_second"`
}

type LatencySummary struct {
	SampleCount   int   `json:"sample_count"`
	SuccessCount  int   `json:"success_count"`
	ErrorCount    int   `json:"error_count"`
	MinMicros     int64 `json:"min_micros,omitempty"`
	AverageMicros int64 `json:"average_micros,omitempty"`
	P50Micros     int64 `json:"p50_micros,omitempty"`
	P95Micros     int64 `json:"p95_micros,omitempty"`
	P99Micros     int64 `json:"p99_micros,omitempty"`
	MaxMicros     int64 `json:"max_micros,omitempty"`
}

type WriteSample struct {
	DocumentName  string `json:"document_name"`
	Bytes         uint64 `json:"bytes"`
	LatencyMicros int64  `json:"latency_micros"`
	ErrorClass    string `json:"error_class,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ComponentSignal struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Observation string `json:"observation"`
}

type Thresholds struct {
	MinWritesPerSecond  float64 `json:"min_writes_per_second,omitempty"`
	MaxP99LatencyMicros int64   `json:"max_p99_ack_latency_micros,omitempty"`
}

type ThresholdViolation struct {
	Scope   string `json:"scope"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func BuildReport(opts Options, samples []WriteSample, startedAt, endedAt time.Time) Report {
	opts = defaultOptions(opts)
	duration := endedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	report := Report{
		ReportKind:  ReportKind,
		GeneratedAt: opts.now().Format(time.RFC3339),
		Release: ReleaseIdentity{
			ReleaseSHA: opts.ReleaseSHA,
			DirtyTree:  opts.DirtyTree,
		},
		Workload: WorkloadShape{
			RunnerID:            opts.RunnerID,
			Concurrency:         opts.Concurrency,
			SampleCount:         opts.SampleCount,
			DocumentSizeBytes:   opts.DocumentSizeBytes,
			TotalBytes:          totalConfiguredBytes(opts),
			MaxDurationMillis:   durationMillis(opts.MaxDuration),
			NonProductionOnly:   true,
			ApplicationBoundary: "internal/localstorage.Application.WriteDocument",
		},
		Results: Results{
			DurationMillis:   durationMillis(duration),
			Samples:          append([]WriteSample(nil), samples...),
			ComponentSignals: componentSignals(),
		},
		Thresholds: Thresholds{
			MinWritesPerSecond:  opts.MinWritesPerSecond,
			MaxP99LatencyMicros: durationMicros(opts.MaxP99AckLatency),
		},
		EvidenceLimits: []string{
			"local write-pipeline evidence is regression and performance-smoke evidence, not production capacity approval",
			"the harness uses an in-process localstorage application and does not exercise kind, LocalStack, OpenBao, backend upload, or production topology",
		},
	}
	applyResults(&report, samples, duration)
	report.ThresholdViolations = collectThresholdViolations(report)
	if report.ThresholdViolations == nil {
		report.ThresholdViolations = []ThresholdViolation{}
	}
	if len(report.ThresholdViolations) > 0 {
		report.Status = StatusFailed
		report.RequiredFailureResolution = "Open or update a blocking issue linked to #148, or rerun with an accepted threshold/profile."
	} else {
		report.Status = StatusPassed
	}
	return report
}

func defaultOptions(opts Options) Options {
	if opts.ReleaseSHA == "" {
		opts.ReleaseSHA = "unknown"
	}
	if opts.DirtyTree == "" {
		opts.DirtyTree = "unknown"
	}
	if opts.RunnerID == "" {
		opts.RunnerID = DefaultRunnerID
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return opts
}

func (opts Options) now() time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now().UTC()
}

func totalConfiguredBytes(opts Options) uint64 {
	samples := opts.SampleCount
	if samples < 0 {
		samples = 0
	}
	return uint64(samples) * opts.DocumentSizeBytes
}

func applyResults(report *Report, samples []WriteSample, duration time.Duration) {
	var latencies []int64
	for _, sample := range samples {
		if sample.ErrorClass != "" {
			report.Results.ErrorCount++
			continue
		}
		report.Results.SuccessCount++
		report.Results.TotalBytes += sample.Bytes
		latencies = append(latencies, sample.LatencyMicros)
	}
	report.Results.ACKLatency = summarizeLatency(samples, latencies)
	if duration > 0 {
		seconds := duration.Seconds()
		report.Results.Throughput.WritesPerSecond = float64(report.Results.SuccessCount) / seconds
		report.Results.Throughput.BytesPerSecond = float64(report.Results.TotalBytes) / seconds
	}
}

func summarizeLatency(samples []WriteSample, latencies []int64) LatencySummary {
	summary := LatencySummary{SampleCount: len(samples)}
	for _, sample := range samples {
		if sample.ErrorClass != "" {
			summary.ErrorCount++
		}
	}
	summary.SuccessCount = len(latencies)
	if len(latencies) == 0 {
		return summary
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	summary.MinMicros = latencies[0]
	summary.MaxMicros = latencies[len(latencies)-1]
	summary.P50Micros = percentileMicros(latencies, 50)
	summary.P95Micros = percentileMicros(latencies, 95)
	summary.P99Micros = percentileMicros(latencies, 99)
	var total int64
	for _, latency := range latencies {
		total += latency
	}
	summary.AverageMicros = total / int64(len(latencies))
	return summary
}

func percentileMicros(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func collectThresholdViolations(report Report) []ThresholdViolation {
	var violations []ThresholdViolation
	if report.Results.ErrorCount > 0 {
		violations = append(violations, ThresholdViolation{
			Scope:   "write-pipeline",
			Code:    "SCRAP_WRITE_PIPELINE_SAMPLE_ERRORS",
			Message: fmt.Sprintf("%d write sample(s) failed", report.Results.ErrorCount),
		})
	}
	if report.Thresholds.MinWritesPerSecond > 0 &&
		report.Results.Throughput.WritesPerSecond < report.Thresholds.MinWritesPerSecond {
		violations = append(violations, ThresholdViolation{
			Scope: "write-pipeline",
			Code:  "SCRAP_WRITE_PIPELINE_THROUGHPUT_BELOW_THRESHOLD",
			Message: fmt.Sprintf("throughput %.2f writes/sec is below threshold %.2f writes/sec",
				report.Results.Throughput.WritesPerSecond,
				report.Thresholds.MinWritesPerSecond),
		})
	}
	if report.Thresholds.MaxP99LatencyMicros > 0 &&
		report.Results.ACKLatency.P99Micros > report.Thresholds.MaxP99LatencyMicros {
		violations = append(violations, ThresholdViolation{
			Scope: "write-pipeline",
			Code:  "SCRAP_WRITE_PIPELINE_P99_ABOVE_THRESHOLD",
			Message: fmt.Sprintf("p99 ACK latency %d microseconds is above threshold %d microseconds",
				report.Results.ACKLatency.P99Micros,
				report.Thresholds.MaxP99LatencyMicros),
		})
	}
	return violations
}

func componentSignals() []ComponentSignal {
	return []ComponentSignal{
		{
			Name:        "block_append_sync_latency",
			Status:      "available-via-metrics",
			Observation: "scrap_block_sync_latency_seconds exposes the block durable sync boundary; this local in-process harness does not scrape Prometheus",
		},
		{
			Name:        "block_append_queue_depth",
			Status:      "available-via-metrics",
			Observation: "scrap_block_append_queue_depth exposes append callers waiting for durable sync",
		},
		{
			Name:        "block_sync_batch_size",
			Status:      "available-via-metrics",
			Observation: "scrap_block_sync_batch_size exposes append waiters completed by one durable sync",
		},
		{
			Name:        "metadata_commit_latency",
			Status:      "available-via-metrics",
			Observation: "scrap_metadata_command_sync_latency_seconds exposes the metadata command-log durable sync boundary",
		},
		{
			Name:        "metadata_command_batch_size",
			Status:      "available-via-metrics",
			Observation: "scrap_metadata_command_batch_size exposes commands completed by one durable command-log sync",
		},
		{
			Name:        "raft_queue_depth",
			Status:      "available-via-metrics",
			Observation: "scrap_raft_queue_depth exists, but this local in-process harness does not scrape Prometheus",
		},
	}
}

func durationMillis(duration time.Duration) int64 {
	return duration.Milliseconds()
}

func durationMicros(duration time.Duration) int64 {
	return duration.Microseconds()
}
