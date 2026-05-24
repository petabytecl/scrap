package writepipelineevidence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/storageapp"
)

func TestBuildReportSerializesRequiredEvidence(t *testing.T) {
	started := time.Unix(1700000000, 0).UTC()
	opts := Options{
		ReleaseSHA:        "abc123",
		DirtyTree:         "clean",
		RunnerID:          "local-application",
		SampleCount:       3,
		Concurrency:       2,
		DocumentSizeBytes: 64,
		MaxDuration:       time.Second,
		Now:               func() time.Time { return started },
	}
	samples := []WriteSample{
		{DocumentName: "doc-001.xml", Bytes: 64, LatencyMicros: 1000},
		{DocumentName: "doc-002.xml", Bytes: 64, LatencyMicros: 2000},
		{DocumentName: "doc-003.xml", Bytes: 64, LatencyMicros: 3000},
	}

	report := BuildReport(opts, samples, started, started.Add(10*time.Millisecond))
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal report: %v", err)
	}
	raw := string(encoded)
	for _, want := range []string{
		`"report_kind":"write-pipeline-performance-evidence"`,
		`"release_sha":"abc123"`,
		`"dirty_tree":"clean"`,
		`"concurrency":2`,
		`"document_size_bytes":64`,
		`"p50_micros":2000`,
		`"p95_micros":3000`,
		`"p99_micros":3000`,
		`"name":"metadata_command_sync_latency"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("encoded report missing %s: %s", want, raw)
		}
	}
	if strings.Contains(raw, `"name":"metadata_commit_latency"`) {
		t.Fatalf("encoded report uses stale metadata signal name: %s", raw)
	}
	if got := report.Results.Throughput.WritesPerSecond; got < 299.9 || got > 300.1 {
		t.Fatalf("writes per second = %.4f, want near 300", got)
	}
	if report.Status != StatusPassed {
		t.Fatalf("status = %q, want %q", report.Status, StatusPassed)
	}
	if len(report.EvidenceLimits) == 0 {
		t.Fatal("evidence limits are empty")
	}
	if !strings.Contains(report.EvidenceLimits[0], "not production capacity approval") {
		t.Fatalf("evidence limits = %#v, want production boundary caveat", report.EvidenceLimits)
	}
}

func TestBuildReportSerializesObservedComponentEvidence(t *testing.T) {
	started := time.Unix(1700000000, 0).UTC()
	report := BuildReport(Options{
		ReleaseSHA:        "abc123",
		DirtyTree:         "clean",
		RunnerID:          "local-application",
		SampleCount:       1,
		Concurrency:       1,
		DocumentSizeBytes: 64,
		MaxDuration:       time.Second,
		ComponentSignals: []ComponentSignal{
			{
				Name:        "block_sync_batch_size",
				Status:      ComponentSignalObserved,
				Observation: "observed batch size",
				Unit:        "waiters_per_sync",
				Count:       2,
				Sum:         float64Ptr(8),
				Average:     float64Ptr(4),
				Required:    true,
			},
			{
				Name:        "block_append_queue_depth",
				Status:      ComponentSignalSampled,
				Observation: "sampled queue depth",
				Unit:        "waiters",
				Current:     float64Ptr(0),
				MaxObserved: float64Ptr(4),
				Samples:     10,
			},
		},
		Now: func() time.Time { return started },
	}, []WriteSample{{DocumentName: "doc-001.xml", Bytes: 64, LatencyMicros: 1000}}, started, started.Add(time.Millisecond))

	if report.Status != StatusPassed {
		t.Fatalf("status = %q, want %q; violations=%#v", report.Status, StatusPassed, report.ThresholdViolations)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal report: %v", err)
	}
	raw := string(encoded)
	for _, want := range []string{
		`"status":"observed"`,
		`"unit":"waiters_per_sync"`,
		`"count":2`,
		`"average":4`,
		`"max_observed":4`,
		`"samples":10`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("encoded report missing %s: %s", want, raw)
		}
	}
}

func TestBuildReportFailsWhenRequiredComponentEvidenceIsMissing(t *testing.T) {
	started := time.Unix(1700000000, 0).UTC()
	report := BuildReport(Options{
		ReleaseSHA:        "abc123",
		DirtyTree:         "clean",
		RunnerID:          "local-application",
		SampleCount:       1,
		Concurrency:       1,
		DocumentSizeBytes: 64,
		MaxDuration:       time.Second,
		ComponentSignals: []ComponentSignal{
			{
				Name:        "metadata_command_batch_size",
				Status:      ComponentSignalNotObserved,
				Observation: "missing metadata batch size",
				Required:    true,
			},
		},
		Now: func() time.Time { return started },
	}, []WriteSample{{DocumentName: "doc-001.xml", Bytes: 64, LatencyMicros: 1000}}, started, started.Add(time.Millisecond))

	if report.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", report.Status, StatusFailed)
	}
	if !hasViolation(report.ThresholdViolations, "SCRAP_WRITE_PIPELINE_SIGNAL_NOT_OBSERVED") {
		t.Fatalf("violations = %#v, want component signal violation", report.ThresholdViolations)
	}
}

func TestBuildReportFailsWhenThresholdsAreExceeded(t *testing.T) {
	started := time.Unix(1700000000, 0).UTC()
	opts := Options{
		ReleaseSHA:         "abc123",
		DirtyTree:          "clean",
		RunnerID:           "local-application",
		SampleCount:        1,
		Concurrency:        1,
		DocumentSizeBytes:  64,
		MaxDuration:        time.Second,
		MinWritesPerSecond: 500,
		MaxP99AckLatency:   2 * time.Millisecond,
		Now:                func() time.Time { return started },
	}
	samples := []WriteSample{
		{DocumentName: "doc-001.xml", Bytes: 64, LatencyMicros: 5000},
	}

	report := BuildReport(opts, samples, started, started.Add(time.Second))
	if report.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", report.Status, StatusFailed)
	}
	if !hasViolation(report.ThresholdViolations, "SCRAP_WRITE_PIPELINE_THROUGHPUT_BELOW_THRESHOLD") {
		t.Fatalf("violations = %#v, want throughput threshold violation", report.ThresholdViolations)
	}
	if !hasViolation(report.ThresholdViolations, "SCRAP_WRITE_PIPELINE_P99_ABOVE_THRESHOLD") {
		t.Fatalf("violations = %#v, want p99 threshold violation", report.ThresholdViolations)
	}
	if !strings.Contains(report.RequiredFailureResolution, "#148") {
		t.Fatalf("failure resolution = %q, want #148 reference", report.RequiredFailureResolution)
	}
}

func TestRunWritesThroughApplicationBoundary(t *testing.T) {
	started := time.Unix(1700000000, 0).UTC()
	app := &recordingWriteApplication{}

	report, err := Run(context.Background(), Options{
		ReleaseSHA:        "abc123",
		DirtyTree:         "clean",
		RunnerID:          "test-runner",
		SampleCount:       4,
		Concurrency:       2,
		DocumentSizeBytes: 32,
		MaxDuration:       time.Second,
		Now:               func() time.Time { return started },
	}, app)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if app.writeCount != 4 {
		t.Fatalf("write count = %d, want 4", app.writeCount)
	}
	if report.Status != StatusPassed {
		t.Fatalf("status = %q, want %q; violations=%#v", report.Status, StatusPassed, report.ThresholdViolations)
	}
	if report.Results.SuccessCount != 4 {
		t.Fatalf("success count = %d, want 4", report.Results.SuccessCount)
	}
	if report.Workload.ApplicationBoundary != "internal/localstorage.Application.WriteDocument" {
		t.Fatalf("application boundary = %q", report.Workload.ApplicationBoundary)
	}
}

func hasViolation(violations []ThresholdViolation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

func float64Ptr(value float64) *float64 {
	return &value
}

type recordingWriteApplication struct {
	mu         sync.Mutex
	writeCount int
}

func (a *recordingWriteApplication) WriteDocument(_ context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (storageapp.WriteDocumentResult, error) {
	a.mu.Lock()
	a.writeCount++
	a.mu.Unlock()
	var length uint64
	for {
		chunk, err := chunks.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return storageapp.WriteDocumentResult{}, err
		}
		length += uint64(len(chunk))
	}
	return storageapp.WriteDocumentResult{
		Metadata: storageapp.DocumentMetadata{
			Identity: init.Identity,
			Length:   length,
		},
	}, nil
}
