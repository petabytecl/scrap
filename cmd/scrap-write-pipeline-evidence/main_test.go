package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/writepipelineevidence"
)

func TestRunWritesEvidenceToStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{
		"--out", "-",
		"--samples", "2",
		"--concurrency", "1",
		"--document-size", "16",
		"--duration", "5s",
		"--release-sha", "abc123",
		"--dirty-tree", "clean",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	report := decodeReport(t, stdout.Bytes())
	if report.Status != writepipelineevidence.StatusPassed {
		t.Fatalf("status = %q, want passed", report.Status)
	}
	if report.Release.ReleaseSHA != "abc123" || report.Release.DirtyTree != "clean" {
		t.Fatalf("release = %#v, want abc123/clean", report.Release)
	}
	if report.Workload.SampleCount != 2 || report.Workload.DocumentSizeBytes != 16 {
		t.Fatalf("workload = %#v, want two 16-byte samples", report.Workload)
	}
	if signalStatus(report, "block_sync_batch_size") != writepipelineevidence.ComponentSignalObserved {
		t.Fatalf("block sync batch size signal = %#v, want observed", report.Results.ComponentSignals)
	}
	if signalStatus(report, "metadata_command_sync_latency") != writepipelineevidence.ComponentSignalObserved {
		t.Fatalf("metadata sync latency signal = %#v, want observed", report.Results.ComponentSignals)
	}
}

func TestRunWritesFailureReportBeforeReturningNonZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{
		"--out", "-",
		"--samples", "1",
		"--concurrency", "1",
		"--document-size", "16",
		"--duration", "5s",
		"--release-sha", "abc123",
		"--dirty-tree", "clean",
		"--max-p99-ack-latency", "1us",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1", code)
	}
	report := decodeReport(t, stdout.Bytes())
	if report.Status != writepipelineevidence.StatusFailed {
		t.Fatalf("status = %q, want failed", report.Status)
	}
	if len(report.ThresholdViolations) == 0 {
		t.Fatalf("violations = %#v, want threshold violation", report.ThresholdViolations)
	}
	if !strings.Contains(stderr.String(), "write pipeline evidence status failed") {
		t.Fatalf("stderr = %q, want failed status", stderr.String())
	}
}

func TestRunWritesEvidenceFile(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	outPath := filepath.Join(t.TempDir(), "write-pipeline.json")

	code := run([]string{
		"--out", outPath,
		"--samples", "1",
		"--concurrency", "1",
		"--document-size", "8",
		"--duration", "5s",
		"--release-sha", "abc123",
		"--dirty-tree", "clean",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when writing file", stdout.String())
	}
	// #nosec G304 -- test reads the report path created under t.TempDir.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	report := decodeReport(t, data)
	if report.ReportKind != writepipelineevidence.ReportKind {
		t.Fatalf("report kind = %q", report.ReportKind)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"unexpected"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("stderr = %q, want unexpected arguments", stderr.String())
	}
}

func decodeReport(t *testing.T, data []byte) writepipelineevidence.Report {
	t.Helper()
	var report writepipelineevidence.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(data))
	}
	return report
}

func signalStatus(report writepipelineevidence.Report, name string) string {
	for _, signal := range report.Results.ComponentSignals {
		if signal.Name == name {
			return signal.Status
		}
	}
	return ""
}
