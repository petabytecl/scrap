package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/releasegate"
)

func TestRunReleaseGateWritesReport(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	manifestPath := writeReleaseGateManifest(t, releasegate.TierNormalPR)

	code := run([]string{"-tier", "normal_pr", "-manifest", manifestPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	var report releasegate.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Tier != releasegate.TierNormalPR {
		t.Fatalf("tier = %q, want normal_pr", report.Tier)
	}
	if !report.Ready {
		t.Fatalf("report should be ready for normal_pr: %#v", report)
	}
}

func TestRunReleaseGateRejectsInvalidTier(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-tier", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown release gate tier") {
		t.Fatalf("stderr = %q, want unsupported tier", stderr.String())
	}
}

func TestRunReleaseGateRejectsInvalidFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want flag parse error", stderr.String())
	}
}

func TestRunReleaseGateRejectsUnexpectedArgs(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-tier", "normal_pr", "extra"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("stderr = %q, want unexpected args error", stderr.String())
	}
}

func TestRunReleaseGateReadsManifest(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	err := os.WriteFile(manifestPath, []byte(`{"evidence":[{"gate":"generated-code","status":"accepted","artifact":"file:///metadata.json"}]}`), 0o600)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-tier", "release", "-manifest", manifestPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1 because release gate needs all evidence; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report releasegate.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Ready {
		t.Fatalf("release report unexpectedly ready: %#v", report)
	}
}

func TestRunReleaseGateReportsManifestOpenAndReadErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	missingPath := filepath.Join(t.TempDir(), "missing.json")

	code := run([]string{"-manifest", missingPath}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("missing manifest exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "open manifest") {
		t.Fatalf("stderr = %q, want open manifest error", stderr.String())
	}

	stderr.Reset()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"evidence":`), 0o600); err != nil {
		t.Fatalf("write malformed manifest: %v", err)
	}
	code = run([]string{"-manifest", manifestPath}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("malformed manifest exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "read manifest") {
		t.Fatalf("stderr = %q, want read manifest error", stderr.String())
	}
}

func TestRunReleaseGateReportsStdoutWriteErrors(t *testing.T) {
	var stderr bytes.Buffer

	code := run([]string{"-tier", "normal_pr", "-manifest", writeReleaseGateManifest(t, releasegate.TierNormalPR)}, failingWriter{}, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "write report") {
		t.Fatalf("stderr = %q, want write report error", stderr.String())
	}
}

func writeReleaseGateManifest(t *testing.T, tier releasegate.Tier) string {
	t.Helper()
	manifest := releasegate.Manifest{Evidence: make([]releasegate.Evidence, 0, len(releasegate.RequiredForTier(tier)))}
	for _, definition := range releasegate.RequiredForTier(tier) {
		manifest.Evidence = append(manifest.Evidence, releasegate.Evidence{
			GateID:   definition.ID,
			Status:   releasegate.EvidenceAccepted,
			Artifact: "artifact://" + definition.ID,
		})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
