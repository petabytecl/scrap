package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMainHelp(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"help"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "scrapctl <doctor|status|upload-pressure|peers|leader|fault|evidence>") {
		t.Fatalf("unexpected help output: %s", stdout.String())
	}
}

func TestRunMainError(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"unknown"}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `scrapctl: unknown command "unknown"`) {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
