package evidencebundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateWritesEvidenceBundleAndPassesWithCurrentRunProof(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{})

	if !result.Gate.Pass {
		t.Fatalf("gate pass = false, checks = %+v", result.Gate.Checks)
	}
	for _, rel := range []string{
		"config.json",
		"run-window.json",
		"stress-results.json",
		"queries.json",
		"gates.json",
		"metrics/rpc_requests_baseline.json",
		"metrics/rpc_requests_cumulative_after.json",
		"metrics/rpc_requests.json",
		"traces/scrapd.json",
		"logs/evidence-probe-health.json",
		"logs/scrapd.json",
		"profiles/cpu.json",
		"profiles/heap_inuse_space.json",
	} {
		if _, err := os.Stat(filepath.Join(result.BundlePath, rel)); err != nil {
			t.Fatalf("expected bundle file %s: %v", rel, err)
		}
	}
	assertBundleCheck(t, result.Gate, "metrics_captured", true)
	assertBundleCheck(t, result.Gate, "traces_captured", true)
	assertBundleCheck(t, result.Gate, "logs_captured", true)
	assertBundleCheck(t, result.Gate, "cpu_profile_captured", true)
	assertBundleCheck(t, result.Gate, "heap_profile_captured", true)
}

func TestGenerateWritesEvictionCampaignEvidence(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{evictionPlanID: "plan-123"})

	assertEvictionEvidenceFiles(t, result.BundlePath)
	assertEvictionCandidateEvidence(t, result.BundlePath)
	assertEvictionValidationEvidence(t, result.BundlePath)
	assertEvictionHealthEvidence(t, result.BundlePath)
}

func assertEvictionEvidenceFiles(t *testing.T, root string) {
	t.Helper()

	for _, rel := range []string{
		"eviction/status.json",
		"eviction/candidate-plan.json",
		"eviction/apply-result.json",
		"eviction/validation-result.json",
		"eviction/health.json",
		"metrics/eviction_plans.json",
		"metrics/eviction_apply_total.json",
		"metrics/eviction_restore_total.json",
		"metrics/eviction_evicted_blocks.json",
		"metrics/eviction_restore_failed_blocks.json",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected eviction evidence file %s: %v", rel, err)
		}
	}
}

func assertEvictionCandidateEvidence(t *testing.T, root string) {
	t.Helper()

	var candidate struct {
		PlanID          string `json:"plan_id"`
		CandidateBlocks int    `json:"candidate_blocks"`
		SelectedBytes   int64  `json:"selected_bytes"`
	}
	readBundleJSON(t, root, "eviction/candidate-plan.json", &candidate)
	if candidate.PlanID != "plan-123" || candidate.CandidateBlocks != 3 || candidate.SelectedBytes != 4096 {
		t.Fatalf("candidate plan = %+v, want plan-123 with candidate evidence", candidate)
	}
}

func assertEvictionValidationEvidence(t *testing.T, root string) {
	t.Helper()

	var validation struct {
		ValidatedBlocks        int `json:"validated_blocks"`
		ValidationFailedBlocks int `json:"validation_failed_blocks"`
		Validations            []struct {
			BlockID uint64 `json:"block_id"`
			Status  string `json:"status"`
		} `json:"validations"`
	}
	readBundleJSON(t, root, "eviction/validation-result.json", &validation)
	if validation.ValidatedBlocks != 1 || validation.ValidationFailedBlocks != 0 || len(validation.Validations) != 1 {
		t.Fatalf("validation result = %+v, want one successful validation", validation)
	}
}

func assertEvictionHealthEvidence(t *testing.T, root string) {
	t.Helper()

	var health struct {
		EvictedBlocks          int `json:"evicted_blocks"`
		HotCleanupNeededBlocks int `json:"hot_cleanup_needed_blocks"`
		RestoreFailedBlocks    int `json:"restore_failed_blocks"`
	}
	readBundleJSON(t, root, "eviction/health.json", &health)
	if health.EvictedBlocks != 2 || health.HotCleanupNeededBlocks != 1 || health.RestoreFailedBlocks != 0 {
		t.Fatalf("health = %+v, want eviction lifecycle snapshot", health)
	}
}

func TestGenerateFailsWhenCurrentRunMetricProofIsMissing(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{missingMetricDelta: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "metrics_captured", false)
}

func TestGenerateFailsWhenLogMarkerIsMissing(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{missingLog: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "logs_captured", false)
}

func TestGenerateFailsWhenAdminLogProbeFails(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{adminProbeFailed: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "logs_captured", false)
}

func TestGenerateFailsWhenTraceEvidenceIsMissing(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{missingTrace: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "traces_captured", false)
}

func TestGenerateFailsWhenCPUProfileIsMissing(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{missingCPUProfile: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "cpu_profile_captured", false)
	assertBundleCheck(t, result.Gate, "heap_profile_captured", true)
}

func TestGenerateFailsWhenHeapProfileIsMissing(t *testing.T) {
	result := generateTestBundle(t, fakeSignals{missingHeapProfile: true})

	if result.Gate.Pass {
		t.Fatalf("gate pass = true, want false")
	}
	assertBundleCheck(t, result.Gate, "cpu_profile_captured", true)
	assertBundleCheck(t, result.Gate, "heap_profile_captured", false)
}

func generateTestBundle(t *testing.T, signals fakeSignals) Result {
	t.Helper()

	bundleDir := t.TempDir()
	clock := &sequenceClock{values: []time.Time{
		time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 29, 12, 1, 0, 0, time.UTC),
		time.Date(2026, 5, 29, 12, 1, 15, 0, time.UTC),
	}}

	result, err := Generate(context.Background(), Options{
		Config: Config{
			RepoRoot:       "/repo",
			BundleDir:      bundleDir,
			GrafanaURL:     "http://grafana.local",
			AdminURL:       "http://admin.local",
			MimirProxy:     "http://grafana.local/mimir",
			TempoProxy:     "http://grafana.local/tempo",
			LokiProxy:      "http://grafana.local/loki",
			PyroscopeURL:   "http://grafana.local/pyroscope",
			EvictionPlanID: signals.evictionPlanID,
			Scenario:       "throughput",
			StressAddr:     "127.0.0.1:18090",
			Workers:        8,
			Duration:       "60s",
			DocSizeBytes:   16384,
			Settle:         0,
		},
		Clock:        clock,
		Sleeper:      noSleep,
		Command:      fakeMetadataCommand{},
		StressRunner: fakeStressRunner{scenario: "throughput"},
		AdminProbe:   fakeAdminProbe{failed: signals.adminProbeFailed},
		HTTPClient:   &http.Client{Transport: fakeEvidenceTransport{signals: signals}},
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(result.BundlePath, bundleDir+string(os.PathSeparator)) {
		t.Fatalf("bundle path %q escaped %q", result.BundlePath, bundleDir)
	}

	data, err := os.ReadFile(filepath.Join(result.BundlePath, "gates.json"))
	if err != nil {
		t.Fatalf("read gates.json: %v", err)
	}
	var gate Gate
	if err := json.Unmarshal(data, &gate); err != nil {
		t.Fatalf("parse gates.json: %v\n%s", err, data)
	}
	if fmt.Sprintf("%+v", gate) != fmt.Sprintf("%+v", result.Gate) {
		t.Fatalf("written gate = %+v, result gate = %+v", gate, result.Gate)
	}
	return result
}

type fakeSignals struct {
	missingMetricDelta bool
	missingLog         bool
	missingTrace       bool
	missingCPUProfile  bool
	missingHeapProfile bool
	adminProbeFailed   bool
	evictionPlanID     string
}

type sequenceClock struct {
	values []time.Time
	index  int
}

func (c *sequenceClock) Now() time.Time {
	if c.index >= len(c.values) {
		return c.values[len(c.values)-1]
	}
	out := c.values[c.index]
	c.index++
	return out
}

func noSleep(context.Context, time.Duration) error {
	return nil
}

type fakeMetadataCommand struct{}

func (fakeMetadataCommand) Run(_ context.Context, name string, args ...string) (string, error) {
	command := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(command, "rev-parse --short HEAD"):
		return "abc1234\n", nil
	case strings.Contains(command, "rev-parse HEAD"):
		return "abc1234567890\n", nil
	case strings.Contains(command, "diff --quiet"):
		return "", nil
	case strings.Contains(command, "jsonpath={.spec.template.spec.containers[0].image}"):
		return "localhost/scrapd:test\n", nil
	case strings.Contains(command, "jsonpath={.spec.replicas}"):
		return "3\n", nil
	case strings.Contains(command, "current-context"):
		return "kind-scrap-evidence\n", nil
	default:
		return "", fmt.Errorf("unexpected command: %s", command)
	}
}

type fakeStressRunner struct {
	scenario string
}

func (r fakeStressRunner) RunStress(context.Context, StressRequest) (StressResult, error) {
	switch r.scenario {
	case "mixed":
		return StressResult{Stdout: `{"scenario":"mixed","write":{"total_ops":10,"failed_ops":0},"read":{"total_ops":5,"failed_ops":0},"head":{"total_ops":5,"failed_ops":0}}`}, nil
	case "pressure":
		return StressResult{Stdout: `{"scenario":"pressure","total_writes":10,"other_errors":0,"pressure_rejections":3}`}, nil
	default:
		return StressResult{Stdout: `{"scenario":"throughput","total_ops":20,"failed_ops":0}`}, nil
	}
}

type fakeAdminProbe struct {
	failed bool
}

func (p fakeAdminProbe) Emit(context.Context, AdminProbeRequest) (AdminProbeResult, error) {
	if p.failed {
		return AdminProbeResult{Body: []byte(`{"error":"admin evidence log probe failed"}`)}, errors.New("probe failed")
	}
	return AdminProbeResult{Body: []byte(`{"status":"ok"}`)}, nil
}

type fakeEvidenceTransport struct {
	signals fakeSignals
}

func (t fakeEvidenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := t.responseBody(req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (t fakeEvidenceTransport) responseBody(req *http.Request) string {
	path := req.URL.Path
	raw := req.URL.RawQuery
	switch {
	case strings.Contains(path, "/healthz"):
		return `{"status":"ok","eviction_pressure":"degraded","evicted_blocks":2,"evicted_bytes":4096,"hot_cleanup_needed_blocks":1,"metadata_loss_blocks":0,"restore_failed_blocks":0}`
	case strings.Contains(path, "/admin/eviction/plans/plan-123"):
		return `{"plan_id":"plan-123","status":"completed","plan":{"plan_id":"plan-123","reason":"evidence_run","candidate_blocks":3,"candidate_bytes":8192,"eligible_blocks":2,"eligible_bytes":4096,"selected_bytes":4096,"selected_blocks":[{"block_id":1,"shard_id":7,"size_bytes":4096}],"skip_counts_by_reason":{"hot_residency_window":1}},"apply_result":{"plan_id":"plan-123","status":"completed","selected_blocks":1,"evicted_blocks":1,"validated_blocks":1,"validation_failed_blocks":0,"bytes_freed":4096,"blocks":[{"block_id":1,"shard_id":7,"size_bytes":4096,"status":"evicted","bytes_freed":4096}],"validations":[{"block_id":1,"shard_id":7,"status":"passed"}]}}`
	case strings.Contains(path, "/loki/api/v1/query_range"):
		return t.logResponse()
	case strings.Contains(path, "/api/v1/query"):
		return t.metricResponse(raw)
	case strings.Contains(path, "/api/search"):
		return t.traceResponse()
	case strings.Contains(path, "/pyroscope/render"):
		return t.profileResponse(raw)
	default:
		return `{}`
	}
}

func readBundleJSON(t *testing.T, root, rel string, target any) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // rel is a test-owned bundle path selected by the test.
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("parse %s: %v\n%s", rel, err, data)
	}
}

func (t fakeEvidenceTransport) logResponse() string {
	if t.signals.missingLog {
		return `{"status":"success","data":{"resultType":"streams","result":[]}}`
	}
	return `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"service_name":"scrapd"},"values":[["1780056000000000000","evidence_marker=scrap-evidence-20260529T120000Z-abc1234"]]}]}}`
}

func (t fakeEvidenceTransport) metricResponse(rawQuery string) string {
	if strings.Contains(rawQuery, "scrap_rpc_server_requests_total") && strings.Contains(rawQuery, "time=1780056000") {
		return `{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1780056000,"100"]}]}}`
	}
	if strings.Contains(rawQuery, "scrap_rpc_server_requests_total") {
		return t.rpcAfterResponse()
	}
	return `{"status":"success","data":{"result":[{"metric":{"name":"sample"},"value":[1780056075,"1"]}]}}`
}

func (t fakeEvidenceTransport) rpcAfterResponse() string {
	if t.signals.missingMetricDelta {
		return `{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1780056075,"100"]}]}}`
	}
	return `{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1780056075,"120"]}]}}`
}

func (t fakeEvidenceTransport) traceResponse() string {
	if t.signals.missingTrace {
		return `{"traces":[]}`
	}
	return `{"traces":[{"traceID":"abc"}]}`
}

func (t fakeEvidenceTransport) profileResponse(rawQuery string) string {
	if strings.Contains(rawQuery, "process_cpu") && t.signals.missingCPUProfile {
		return `{"timeline":{"samples":[]}}`
	}
	if !strings.Contains(rawQuery, "process_cpu") && t.signals.missingHeapProfile {
		return `{"timeline":{"samples":[]}}`
	}
	return `{"timeline":{"samples":[1]}}`
}

func assertBundleCheck(t *testing.T, gate Gate, name string, want bool) {
	t.Helper()

	for _, check := range gate.Checks {
		if check.Name == name {
			if check.Pass != want {
				t.Fatalf("check %s pass=%v, want %v; reason=%s", name, check.Pass, want, check.Reason)
			}
			return
		}
	}
	t.Fatalf("check %s not found in %+v", name, gate.Checks)
}
