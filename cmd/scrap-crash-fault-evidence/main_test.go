package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsBadCrashFaultFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"scrap-crash-fault-evidence", "-count=not-a-number"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid value") {
		t.Fatalf("stderr = %q, want injected flag parse error", stderr.String())
	}
}

func TestRunCrashFaultEvidenceExecutesWithInjectedGo(t *testing.T) {
	installFakeGo(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{
		"scrap-crash-fault-evidence",
		"-artifact-uri", "artifact://crash-fault.json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"ready": true`) {
		t.Fatalf("stdout = %q, want ready report", stdout.String())
	}
}

func TestParseOptionsDerivesArtifactURIFromOutputPath(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "crash-fault.json")

	options, gotPath, err := parseOptions([]string{
		"scrap-crash-fault-evidence",
		"-out", outPath,
		"-runner-profile", "runner",
		"-filesystem-profile", "fs",
		"-seed", "seed",
		"-count", "3",
		"-race",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if gotPath != outPath {
		t.Fatalf("out path = %q, want %q", gotPath, outPath)
	}
	if options.Count != 3 || !options.Race {
		t.Fatalf("options count/race = %d/%v, want 3/true", options.Count, options.Race)
	}
	if options.RunnerProfile != "runner" || options.FilesystemProfile != "fs" || options.Seed != "seed" {
		t.Fatalf("options = %#v, want runner/fs/seed", options)
	}
	abs, err := filepath.Abs(outPath)
	if err != nil {
		t.Fatalf("abs out path: %v", err)
	}
	if options.ArtifactURI != "file://"+abs {
		t.Fatalf("artifact URI = %q, want file URI for %q", options.ArtifactURI, abs)
	}
}

func TestArtifactURIForOutputPrefersExplicitURI(t *testing.T) {
	uri, err := artifactURIForOutput("s3://evidence/report.json", filepath.Join(t.TempDir(), "ignored.json"))
	if err != nil {
		t.Fatalf("artifact URI: %v", err)
	}
	if uri != "s3://evidence/report.json" {
		t.Fatalf("artifact URI = %q, want explicit URI", uri)
	}
}

func TestWriteCrashFaultReportWritesStdoutAndFile(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeReport("", &stdout, []byte(`{"ready":true}`)); err != nil {
		t.Fatalf("write stdout report: %v", err)
	}
	if stdout.String() != `{"ready":true}` {
		t.Fatalf("stdout report = %q", stdout.String())
	}

	outPath := filepath.Join(t.TempDir(), "report.json")
	if err := writeReport(outPath, &stdout, []byte(`{"ready":false}`)); err != nil {
		t.Fatalf("write file report: %v", err)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- test reads path under t.TempDir.
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(data) != `{"ready":false}` {
		t.Fatalf("file report = %q", string(data))
	}
}

func TestWriteCrashFaultReportReturnsStdoutErrors(t *testing.T) {
	err := writeReport("", failingWriter{}, []byte(`{"ready":true}`))
	if err == nil || !strings.Contains(err.Error(), "write crash/fault evidence") {
		t.Fatalf("writeReport error = %v, want stdout write error", err)
	}
}

func TestFailWritesOperationContext(t *testing.T) {
	var stderr bytes.Buffer

	code := fail(&stderr, "marshal report", errors.New("boom"))
	if code != 2 {
		t.Fatalf("fail exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "marshal report: boom") {
		t.Fatalf("stderr = %q, want operation context", stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func installFakeGo(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	goPath := filepath.Join(dir, "go")
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("find true executable: %v", err)
	}
	if err := os.Symlink(truePath, goPath); err != nil {
		t.Fatalf("link fake go: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
