package scrapctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/scrapctl/evidencebundle"
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
	t.Setenv("SECURITY_EVIDENCE_REPORT", writeScrapctlSecurityReportFixture(t))

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
	if _, err := os.Stat(filepath.Join(bundlePath, "manifest.json")); err != nil {
		t.Fatalf("stat manifest.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundlePath, "privacy-scan.json")); err != nil {
		t.Fatalf("stat privacy-scan.json: %v", err)
	}
	if !strings.Contains(stderr.String(), "PASS - Evidence bundle") {
		t.Fatalf("stderr missing pass summary:\n%s", stderr.String())
	}
}

func TestEvidenceBundleCommandFailsWhenGateFails(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "evidence gate failed") {
		t.Fatalf("Run evidence bundle error = %v, want gate failure\nstderr:\n%s", err, stderr.String())
	}
	bundlePath := strings.TrimSpace(stdout.String())
	if bundlePath == "" {
		t.Fatal("bundle path is empty")
	}
	var gate evidencebundle.Gate
	//nolint:gosec // bundlePath is produced by the test-owned evidence command under t.TempDir.
	data, readErr := os.ReadFile(filepath.Join(bundlePath, "gates.json"))
	if readErr != nil {
		t.Fatalf("read gates.json: %v", readErr)
	}
	if err := json.Unmarshal(data, &gate); err != nil {
		t.Fatalf("parse gates.json: %v\n%s", err, data)
	}
	if gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	if !strings.Contains(stderr.String(), "FAIL - Evidence bundle") {
		t.Fatalf("stderr missing fail summary:\n%s", stderr.String())
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
	t.Setenv("EVICTION_PLAN_ID", "plan-env")
	t.Setenv("SCRAP_E2E_SECURITY_REPORT", "/env/security.json")

	cfg, err := parseEvidenceBundleOptions([]string{
		"--bundle-dir=/flag/bundles",
		"--stress-workers=11",
		"--stress-doc-size=4096",
		"--settle-seconds=0",
		"--eviction-plan-id=plan-flag",
		"--security-report=/flag/security.json",
		"mixed",
	})
	if err != nil {
		t.Fatalf("parseEvidenceBundleOptions: %v", err)
	}
	assertParsedEvidenceBundleOptions(t, cfg)
}

func assertParsedEvidenceBundleOptions(t *testing.T, cfg evidencebundle.Config) {
	t.Helper()

	assertString(t, "bundle dir", cfg.BundleDir, "/flag/bundles")
	assertString(t, "admin URL", cfg.AdminURL, "http://admin.env")
	assertString(t, "kubectl", cfg.Kubectl, "kubectl-env")
	assertString(t, "namespace", cfg.Namespace, "scrap-env")
	assertString(t, "kube context", cfg.KubeContext, "ctx-env")
	assertInt(t, "stress workers", cfg.Workers, testBundleWorkers)
	assertInt(t, "stress doc size", cfg.DocSizeBytes, testBundleDocSize)
	assertString(t, "scenario", cfg.Scenario, "mixed")
	assertString(t, "eviction plan ID", cfg.EvictionPlanID, "plan-flag")
	assertString(t, "security report path", cfg.SecurityReportPath, "/flag/security.json")
}

func assertString(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

func assertInt(t *testing.T, label string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", label, got, want)
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
		return `{"status":"ok","security_mode":"test","production_readiness_status":"not_ready","production_readiness_reason":"non_production_security_mode","authorization_status":"configured","rewrap_status":"ok","rewrap_last_result":"ok","rewrap_last_reason":"ok"}`
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

func writeScrapctlSecurityReportFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security-evidence.json")
	body := `{
  "security_mode": "test",
  "production_readiness_status": "not_ready",
  "production_readiness_reason": "non_production_security_mode",
  "authorization_status": "configured",
  "public_unauthorized_denied": true,
  "peer_unauthorized_denied": true,
  "admin_unauthorized_denied": true,
  "audit_samples_recorded": true,
  "encrypted_write_read_ok": true,
  "encrypted_backend_upload_ok": true,
  "encrypted_restore_ok": true,
  "rewrap_ok": true,
  "phase5_entry_blocked": true
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write security report fixture: %v", err)
	}
	return path
}
