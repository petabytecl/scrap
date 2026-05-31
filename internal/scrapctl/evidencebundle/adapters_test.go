package evidencebundle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testPortForwardPort = "12345"
	testStressWorkers   = 2
	testStressDocSize   = 128
)

func TestHTTPAdminProbeEmitsDirect(t *testing.T) {
	var gotMarker string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMarker = req.Header.Get("X-Scrap-Evidence-Marker")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	probe := httpAdminProbe{client: server.Client()}
	result, err := probe.Emit(context.Background(), AdminProbeRequest{
		AdminURL:     server.URL,
		Marker:       "marker-123",
		LogDir:       t.TempDir(),
		ProbeTimeout: time.Second,
		Kubectl:      "missing-kubectl",
		Namespace:    "scrap",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if gotMarker != "marker-123" {
		t.Fatalf("marker = %q", gotMarker)
	}
	if string(result.Body) != `{"status":"ok"}` {
		t.Fatalf("body = %s", result.Body)
	}
	if result.URL != server.URL {
		t.Fatalf("url = %q, want %q", result.URL, server.URL)
	}
}

func TestHTTPAdminProbeReportsDirectAndFallbackFailure(t *testing.T) {
	probe := httpAdminProbe{client: &http.Client{Transport: failingRoundTripper{}}}

	_, err := probe.Emit(context.Background(), AdminProbeRequest{
		AdminURL:     "http://admin.local",
		Marker:       "marker-123",
		LogDir:       t.TempDir(),
		ProbeTimeout: time.Second,
		Kubectl:      filepath.Join(t.TempDir(), "missing-kubectl"),
		Namespace:    "scrap",
	})
	if err == nil || !strings.Contains(err.Error(), "direct probe") || !strings.Contains(err.Error(), "port-forward probe") {
		t.Fatalf("error = %v, want direct and fallback failure", err)
	}
}

func TestWaitForPortForwardPortReadsKubectlLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "admin-port-forward.log")
	err := os.WriteFile(logPath, []byte("Forwarding from 127.0.0.1:"+testPortForwardPort+" -> 9100\n"), 0o600)
	if err != nil {
		t.Fatalf("write log: %v", err)
	}

	port, err := waitForPortForwardPort(context.Background(), logPath)
	if err != nil {
		t.Fatalf("waitForPortForwardPort: %v", err)
	}
	if port != testPortForwardPort {
		t.Fatalf("port = %q, want %q", port, testPortForwardPort)
	}
}

func TestKubectlArgsPreserveGlobalFlags(t *testing.T) {
	args := kubectlArgs(AdminProbeRequest{
		KubeContext: "kind-scrap",
		Kubeconfig:  "/tmp/kubeconfig",
		Namespace:   "scrap",
	}, "port-forward", "svc/scrap", ":9100")

	want := []string{"--context", "kind-scrap", "--kubeconfig", "/tmp/kubeconfig", "-n", "scrap", "port-forward", "svc/scrap", ":9100"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSleepContextStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sleepContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext error = %v, want canceled", err)
	}
}

func TestExecCommandRunnerRunsCommand(t *testing.T) {
	out, err := execCommandRunner{}.Run(context.Background(), "printf", "ok")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
}

func TestExecStressRunnerCapturesStdoutAndStderr(t *testing.T) {
	tempDir := t.TempDir()
	fakeGo := filepath.Join(tempDir, "go")
	writeExecutable(t, fakeGo, `#!/usr/bin/env bash
printf '{"scenario":"throughput","total_ops":1,"failed_ops":0}'
printf 'stress log\n' >&2
`)

	result, err := execStressRunner{}.RunStress(context.Background(), StressRequest{
		RepoRoot:     tempDir,
		GoCommand:    fakeGo,
		Addr:         "127.0.0.1:18090",
		Scenario:     "throughput",
		Workers:      testStressWorkers,
		Duration:     "1s",
		DocSizeBytes: testStressDocSize,
	})
	if err != nil {
		t.Fatalf("RunStress: %v", err)
	}
	if !strings.Contains(result.Stdout, `"scenario":"throughput"`) {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if result.Stderr != "stress log\n" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
}

func TestSystemClockReturnsTime(t *testing.T) {
	if (systemClock{}).Now().IsZero() {
		t.Fatal("systemClock returned zero time")
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network down")
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	//nolint:gosec // Fake commands in the test-owned temp PATH must be executable.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
