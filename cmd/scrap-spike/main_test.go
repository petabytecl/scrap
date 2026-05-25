package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSpikeRejectsUnexpectedArguments(t *testing.T) {
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

func TestRunSpikeRejectsInvalidFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-transactions", "not-a-number"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "invalid value") {
		t.Fatalf("stderr = %q, want flag parse error", stderr.String())
	}
}

func TestRunSpikeReportsRunnerErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{
		"-raft-barrier",
		"-raft-durable-barrier",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "scrap-spike failed") {
		t.Fatalf("stderr = %q, want runner error", stderr.String())
	}
}

func TestRunSpikeExecutesSmallWorkload(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{
		"-transactions", "1",
		"-concurrency", "1",
		"-chunk-size", "16",
		"-read-back=false",
		"-seed", "7",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "documents_completed") {
		t.Fatalf("stdout = %q, want writepath report", stdout.String())
	}
}
