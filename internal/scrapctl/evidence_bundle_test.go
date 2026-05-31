package scrapctl

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testBundleWorkers = 11
	testBundleDocSize = 4096
)

func TestEvidenceBundleCommandGeneratesBundle(t *testing.T) {
	bundleDir := t.TempDir()
	repoRoot := t.TempDir()
	fakeGo := filepath.Join(t.TempDir(), "go")
	writeTestExecutable(t, fakeGo, `#!/usr/bin/env bash
printf '{"scenario":"throughput","total_ops":20,"failed_ops":0}'
`)
	t.Setenv("BUNDLE_DIR", bundleDir)
	t.Setenv("SCRAP_REPO_ROOT", repoRoot)
	t.Setenv("EVIDENCE_SETTLE_SECONDS", "0")
	t.Setenv("GO", fakeGo)

	runner := &fakeRunner{run: evidenceMetadataCommand}
	client := &http.Client{Transport: &evidenceBundleRoundTripper{}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"evidence", "bundle", "throughput"}, &stdout, &stderr, Deps{
		Runner:     runner,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("Run evidence bundle: %v\nstderr:\n%s", err, stderr.String())
	}
	bundlePath := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(bundlePath, bundleDir+string(os.PathSeparator)) {
		t.Fatalf("bundle path = %q, want under %q", bundlePath, bundleDir)
	}
	if _, err := os.Stat(filepath.Join(bundlePath, "gates.json")); err != nil {
		t.Fatalf("stat gates.json: %v", err)
	}
	if !strings.Contains(stderr.String(), "PASS - Evidence bundle") {
		t.Fatalf("stderr missing pass summary:\n%s", stderr.String())
	}
}

func TestParseEvidenceBundleOptionsUsesEnvironmentAndFlags(t *testing.T) {
	t.Setenv("BUNDLE_DIR", "/env/bundles")
	t.Setenv("SCRAP_REPO_ROOT", "/env/repo")
	t.Setenv("SCRAP_ADMIN_URL", "http://admin.env")
	t.Setenv("KUBECTL", "kubectl-env")
	t.Setenv("SCRAP_NAMESPACE", "scrap-env")
	t.Setenv("KUBE_CONTEXT", "ctx-env")
	t.Setenv("STRESS_WORKERS", "3")
	t.Setenv("STRESS_DOC_SIZE", "1024")

	cfg, err := parseEvidenceBundleOptions([]string{
		"--bundle-dir=/flag/bundles",
		"--stress-workers=11",
		"--stress-doc-size=4096",
		"--settle-seconds=0",
		"mixed",
	})
	if err != nil {
		t.Fatalf("parseEvidenceBundleOptions: %v", err)
	}
	if cfg.BundleDir != "/flag/bundles" {
		t.Fatalf("bundle dir = %q", cfg.BundleDir)
	}
	if cfg.AdminURL != "http://admin.env" {
		t.Fatalf("admin URL = %q", cfg.AdminURL)
	}
	if cfg.Kubectl != "kubectl-env" || cfg.Namespace != "scrap-env" || cfg.KubeContext != "ctx-env" {
		t.Fatalf("kube defaults = kubectl:%q namespace:%q context:%q", cfg.Kubectl, cfg.Namespace, cfg.KubeContext)
	}
	if cfg.Workers != testBundleWorkers || cfg.DocSizeBytes != testBundleDocSize {
		t.Fatalf("stress config = workers:%d doc_size:%d", cfg.Workers, cfg.DocSizeBytes)
	}
	if cfg.Scenario != "mixed" {
		t.Fatalf("scenario = %q", cfg.Scenario)
	}
}

func TestParseEvidenceBundleOptionsRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "bad env int",
			env:  map[string]string{"STRESS_WORKERS": "nope"},
			want: "STRESS_WORKERS must be an integer",
		},
		{
			name: "negative settle",
			args: []string{"--settle-seconds=-1"},
			want: "EVIDENCE_SETTLE_SECONDS must be a non-negative integer",
		},
		{
			name: "zero probe",
			args: []string{"--probe-timeout-seconds=0"},
			want: "EVIDENCE_PROBE_TIMEOUT_SECONDS must be a positive integer",
		},
		{
			name: "too many scenarios",
			args: []string{"throughput", "mixed"},
			want: "usage: scrapctl evidence bundle [scenario]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, err := parseEvidenceBundleOptions(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func evidenceMetadataCommand(_ string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "rev-parse --short HEAD"):
		return "abc1234\n", nil
	case strings.Contains(joined, "rev-parse HEAD"):
		return "abc1234567890\n", nil
	case strings.Contains(joined, "diff --quiet"):
		return "", nil
	case strings.Contains(joined, "jsonpath={.spec.template.spec.containers[0].image}"):
		return "localhost/scrapd:test\n", nil
	case strings.Contains(joined, "jsonpath={.spec.replicas}"):
		return "3\n", nil
	case strings.Contains(joined, "current-context"):
		return "kind-scrap-evidence\n", nil
	default:
		return "", fmt.Errorf("unexpected command: %s", joined)
	}
}

type evidenceBundleRoundTripper struct {
	rpcQueries int
}

func (rt *evidenceBundleRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.evidenceBundleResponse(req), nil
}

func (rt *evidenceBundleRoundTripper) evidenceBundleResponse(req *http.Request) *http.Response {
	body := rt.evidenceBundleResponseBody(req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

func (rt *evidenceBundleRoundTripper) evidenceBundleResponseBody(req *http.Request) string {
	path := req.URL.Path
	raw := req.URL.RawQuery
	switch {
	case strings.Contains(path, "/healthz"):
		return `{"status":"ok"}`
	case strings.Contains(path, "/loki/api/v1/query_range"):
		return `{"status":"success","data":{"resultType":"streams","result":[{"values":[["1","evidence marker"]]}]}}`
	case strings.Contains(path, "/api/search"):
		return `{"traces":[{"traceID":"abc"}]}`
	case strings.Contains(path, "/pyroscope/render"):
		return `{"timeline":{"samples":[1]}}`
	case strings.Contains(path, "/api/v1/query") && strings.Contains(raw, "scrap_rpc_server_requests_total"):
		rt.rpcQueries++
		return rpcMetricResponse(rt.rpcQueries)
	case strings.Contains(path, "/api/v1/query"):
		return `{"status":"success","data":{"result":[{"metric":{"name":"sample"},"value":[1,"1"]}]}}`
	default:
		return `{}`
	}
}

func rpcMetricResponse(queryCount int) string {
	if queryCount > 1 {
		return `{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1,"120"]}]}}`
	}
	return `{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1,"100"]}]}}`
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	//nolint:gosec // Fake commands in the test-owned temp PATH must be executable.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
