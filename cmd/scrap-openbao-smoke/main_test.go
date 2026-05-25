package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/openbaosmoke"
)

func TestRunOpenBaoSmokeRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"unexpected"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("stderr = %q, want unexpected argument", stderr.String())
	}
}

func TestRunOpenBaoSmokeRejectsInvalidFlags(t *testing.T) {
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

func TestRunOpenBaoSmokeRejectsMissingEnvironment(t *testing.T) {
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("BAO_KUBERNETES_JWT", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "BAO_TOKEN") {
		t.Fatalf("stderr = %q, want missing token error", stderr.String())
	}
}

func TestRunOpenBaoSmokeRejectsInvalidTransitKeyPath(t *testing.T) {
	t.Setenv("BAO_TOKEN", "root-token")
	t.Setenv("BAO_KUBERNETES_JWT", "kubernetes-jwt")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--transit-key-path", "transit"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "transit key path") {
		t.Fatalf("stderr = %q, want transit key path error", stderr.String())
	}
}

func TestOpenBaoWriteReportWritesStdoutAndFile(t *testing.T) {
	report := openbaosmoke.Report{
		ReportKind: openbaosmoke.ReportKind,
		Status:     "passed",
		ReleaseSHA: "abc123",
	}
	var stdout bytes.Buffer
	if err := writeReport("", &stdout, report); err != nil {
		t.Fatalf("write stdout report: %v", err)
	}
	decoded := decodeOpenBaoReport(t, stdout.Bytes())
	if decoded.ReleaseSHA != "abc123" {
		t.Fatalf("stdout report release SHA = %q", decoded.ReleaseSHA)
	}

	outPath := filepath.Join(t.TempDir(), "openbao.json")
	if err := writeReport(outPath, &stdout, report); err != nil {
		t.Fatalf("write file report: %v", err)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- test reads report path under t.TempDir.
	if err != nil {
		t.Fatalf("read file report: %v", err)
	}
	decoded = decodeOpenBaoReport(t, data)
	if decoded.ReportKind != openbaosmoke.ReportKind {
		t.Fatalf("file report kind = %q", decoded.ReportKind)
	}
}

func TestOpenBaoReleaseAndDirtyTreeValuesPreferFlagsThenEnvironment(t *testing.T) {
	t.Setenv("SCRAP_RELEASE_SHA", "env-sha")
	t.Setenv("SCRAP_DIRTY_TREE", "env-dirty")

	if got := releaseSHAValue(" flag-sha "); got != "flag-sha" {
		t.Fatalf("release SHA from flag = %q, want flag-sha", got)
	}
	if got := releaseSHAValue(""); got != "env-sha" {
		t.Fatalf("release SHA from env = %q, want env-sha", got)
	}
	if got := dirtyTreeValue(" flag-dirty "); got != "flag-dirty" {
		t.Fatalf("dirty tree from flag = %q, want flag-dirty", got)
	}
	if got := dirtyTreeValue(""); got != "env-dirty" {
		t.Fatalf("dirty tree from env = %q, want env-dirty", got)
	}
}

func TestCloseFileIgnoresNil(t *testing.T) {
	if err := closeFile(nil); err != nil {
		t.Fatalf("close nil file: %v", err)
	}
}

func decodeOpenBaoReport(t *testing.T, data []byte) openbaosmoke.Report {
	t.Helper()
	var report openbaosmoke.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(data))
	}
	return report
}
