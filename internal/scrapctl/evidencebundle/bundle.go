package evidencebundle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	timestampFormat          = "20060102T150405Z"
	rpcCounterQuery          = "sum(scrap_rpc_server_requests_total) by (rpc_method, rpc_grpc_status_code)"
	kubectlGlobalArgCapacity = 4
)

type bundlePaths struct {
	root     string
	metrics  string
	traces   string
	logs     string
	profiles string
	eviction string
	security string
}

type metricQuery struct {
	name  string
	query string
}

type runWindow struct {
	StartUnixSeconds     int64  `json:"start_unix_seconds"`
	StressEndUnixSeconds int64  `json:"stress_end_unix_seconds"`
	QueryUnixSeconds     int64  `json:"query_unix_seconds"`
	QueryWindowSeconds   int64  `json:"query_window_seconds"`
	PrometheusRange      string `json:"prometheus_range"`
}

type runConfig struct {
	Scenario       string `json:"scenario"`
	Timestamp      string `json:"timestamp"`
	GitSHA         string `json:"git_sha"`
	GitSHAShort    string `json:"git_sha_short"`
	GitDirty       bool   `json:"git_dirty"`
	Image          string `json:"image"`
	Replicas       int    `json:"replicas"`
	Workers        int    `json:"workers"`
	Duration       string `json:"duration"`
	DocSizeBytes   int    `json:"doc_size_bytes"`
	EvictionPlanID string `json:"eviction_plan_id,omitempty"`
	Cluster        string `json:"cluster"`
}

type signalResults struct {
	metricFailures      int
	metricEmptyRequired int
	traceOK             bool
	traceReason         string
	logOK               bool
	logReason           string
	cpuProfileOK        bool
	cpuReason           string
	heapProfileOK       bool
	heapReason          string
	securityModeOK      bool
	securityModeReason  string
	authzOK             bool
	authzReason         string
	auditOK             bool
	auditReason         string
	encryptionOK        bool
	encryptionReason    string
	rewrapOK            bool
	rewrapReason        string
	phase5GateOK        bool
	phase5GateReason    string
	privacyScanOK       bool
	privacyScanReason   string
}

type generationState struct {
	paths        bundlePaths
	marker       string
	baseline     []metricSample
	stressOutput string
	adminHealth  []byte
	window       runWindow
	probeFailed  bool
	signals      signalResults
}

// Generate creates a Tier 3 evidence bundle and returns its evaluated gate.
func Generate(ctx context.Context, opts Options) (Result, error) {
	cfg, err := opts.Config.withDefaults()
	if err != nil {
		return Result{}, err
	}
	if err := validateConfiguredDurations(cfg); err != nil {
		return Result{}, err
	}
	opts = opts.withDefaults(cfg)

	state, err := initializeBundle(ctx, cfg, opts)
	if err != nil {
		return Result{}, err
	}
	if err := captureRunEvidence(ctx, cfg, opts, &state); err != nil {
		return Result{}, err
	}
	if err := captureTelemetryEvidence(ctx, cfg, opts, &state); err != nil {
		return Result{}, err
	}
	privacyReport, err := writePrivacyScan(state.paths.root)
	if err != nil {
		return Result{}, err
	}
	state.signals.privacyScanOK = privacyReport.Status == privacyStatusPass
	state.signals.privacyScanReason = privacyGateReason(privacyReport)
	gate, err := writeGate(ctx, opts, state)
	if err != nil {
		return Result{}, err
	}
	if err := writeManifest(cfg, state, gate, privacyReport); err != nil {
		return Result{}, err
	}
	return Result{BundlePath: state.paths.root, Gate: gate}, nil
}

func initializeBundle(ctx context.Context, cfg Config, opts Options) (generationState, error) {
	timestamp := opts.Clock.Now().UTC()
	shortSHA := commandOutput(ctx, opts.Command, "unknown", "git", "-C", cfg.RepoRoot, "rev-parse", "--short", "HEAD")
	bundleName := cfg.Scenario + "-" + timestamp.Format(timestampFormat) + "-" + shortSHA
	paths := bundlePaths{
		root:     filepath.Join(cfg.BundleDir, bundleName),
		metrics:  filepath.Join(cfg.BundleDir, bundleName, "metrics"),
		traces:   filepath.Join(cfg.BundleDir, bundleName, "traces"),
		logs:     filepath.Join(cfg.BundleDir, bundleName, "logs"),
		profiles: filepath.Join(cfg.BundleDir, bundleName, "profiles"),
		eviction: filepath.Join(cfg.BundleDir, bundleName, "eviction"),
		security: filepath.Join(cfg.BundleDir, bundleName, "security"),
	}
	if err := createBundleDirs(paths); err != nil {
		return generationState{}, err
	}

	logMarker := "scrap-evidence-" + timestamp.Format(timestampFormat) + "-" + shortSHA
	opts.logf("Bundle: %s", bundleName)
	opts.logf("Scenario: %s (workers=%d, duration=%s, doc_size=%d)", cfg.Scenario, cfg.Workers, cfg.Duration, cfg.DocSizeBytes)

	if err := writeRunConfig(ctx, cfg, opts.Command, timestamp, shortSHA, paths.root); err != nil {
		return generationState{}, err
	}
	opts.logf("Wrote config.json")
	return generationState{paths: paths, marker: logMarker}, nil
}

func captureRunEvidence(ctx context.Context, cfg Config, opts Options, state *generationState) error {
	startEpoch := opts.Clock.Now().UTC().Unix()
	if err := captureBaselineMetric(ctx, cfg, opts, state, startEpoch); err != nil {
		return err
	}
	if err := emitAdminProbe(ctx, cfg, opts, state); err != nil {
		return err
	}
	stressOutput, err := runStress(ctx, cfg, opts.StressRunner, state.paths.root)
	if err != nil {
		opts.logf("WARNING: stress test command failed: %v", err)
	}
	state.stressOutput = stressOutput
	opts.logf("Wrote stress-results.json")
	return captureRunWindow(ctx, cfg, opts, state, startEpoch)
}

func captureBaselineMetric(ctx context.Context, cfg Config, opts Options, state *generationState, startEpoch int64) error {
	baseline, ok := queryMetricSamples(ctx, opts.HTTPClient, cfg.MimirProxy, rpcCounterQuery, startEpoch)
	if !ok {
		state.signals.metricFailures++
		opts.logf("WARNING: required metric baseline query failed: rpc_requests")
	}
	state.baseline = baseline
	return writeJSONFile(filepath.Join(state.paths.metrics, "rpc_requests_baseline.json"), baseline)
}

func emitAdminProbe(ctx context.Context, cfg Config, opts Options, state *generationState) error {
	probeResult, probeErr := opts.AdminProbe.Emit(ctx, AdminProbeRequest{
		AdminURL:     cfg.AdminURL,
		Marker:       state.marker,
		LogDir:       state.paths.logs,
		ProbeTimeout: cfg.ProbeTimeout,
		Kubectl:      cfg.Kubectl,
		Namespace:    cfg.Namespace,
		KubeContext:  cfg.KubeContext,
		Kubeconfig:   cfg.Kubeconfig,
	})
	if probeErr != nil {
		state.probeFailed = true
		opts.logf("WARNING: admin evidence log probe failed")
	}
	if len(probeResult.Body) == 0 {
		probeResult.Body = []byte(`{"error":"admin evidence log probe failed"}`)
	}
	state.adminHealth = append([]byte(nil), probeResult.Body...)
	return writeRawJSONFile(filepath.Join(state.paths.logs, "evidence-probe-health.json"), probeResult.Body)
}

func captureRunWindow(ctx context.Context, cfg Config, opts Options, state *generationState, startEpoch int64) error {
	endEpoch := opts.Clock.Now().UTC().Unix()
	if endEpoch <= startEpoch {
		endEpoch = startEpoch + 1
	}
	if cfg.Settle > 0 {
		opts.logf("Waiting %s for telemetry export...", cfg.Settle)
		if err := opts.Sleeper(ctx, cfg.Settle); err != nil {
			return fmt.Errorf("wait for telemetry export: %w", err)
		}
	}
	queryEpoch := opts.Clock.Now().UTC().Unix()
	if queryEpoch <= endEpoch {
		queryEpoch = endEpoch + 1
	}
	window := runWindow{
		StartUnixSeconds:     startEpoch,
		StressEndUnixSeconds: endEpoch,
		QueryUnixSeconds:     queryEpoch,
		QueryWindowSeconds:   queryEpoch - startEpoch,
		PrometheusRange:      strconv.FormatInt(queryEpoch-startEpoch, 10) + "s",
	}
	state.window = window
	if err := writeJSONFile(filepath.Join(state.paths.root, "run-window.json"), window); err != nil {
		return err
	}
	opts.logf("Wrote run-window.json")
	return nil
}

func captureTelemetryEvidence(ctx context.Context, cfg Config, opts Options, state *generationState) error {
	if err := captureEvictionHealth(ctx, opts.HTTPClient, cfg, state.paths.eviction); err != nil {
		return err
	}
	if err := captureSecurityEvidence(cfg, state); err != nil {
		return err
	}
	if err := captureMetricEvidence(ctx, opts.HTTPClient, cfg, state.paths.metrics, state.baseline, state.window.QueryUnixSeconds, &state.signals, opts.Logf); err != nil {
		return err
	}
	opts.logf("Wrote metric snapshots (%d query failure(s), %d required-empty)", state.signals.metricFailures, state.signals.metricEmptyRequired)

	if err := writeQueriesFile(cfg, state.marker, state.paths.root); err != nil {
		return err
	}
	opts.logf("Wrote queries.json")

	if err := captureTraceLogProfileEvidence(ctx, opts.HTTPClient, cfg, state.paths, state.window, state.marker, state.probeFailed, &state.signals); err != nil {
		return err
	}
	if err := captureEvictionCampaign(ctx, opts.HTTPClient, cfg, state.paths.eviction); err != nil {
		return err
	}
	opts.logf("Wrote trace/log/profile evidence")
	return nil
}

func writeGate(_ context.Context, opts Options, state generationState) (Gate, error) {
	gate := EvaluateGate(GateInput{
		StressResults:       state.stressOutput,
		MetricFailures:      state.signals.metricFailures,
		MetricEmptyRequired: state.signals.metricEmptyRequired,
		TraceOK:             state.signals.traceOK,
		TraceReason:         state.signals.traceReason,
		LogOK:               state.signals.logOK,
		LogReason:           state.signals.logReason,
		CPUProfileOK:        state.signals.cpuProfileOK,
		CPUReason:           state.signals.cpuReason,
		HeapProfileOK:       state.signals.heapProfileOK,
		HeapReason:          state.signals.heapReason,
		SecurityModeOK:      state.signals.securityModeOK,
		SecurityModeReason:  state.signals.securityModeReason,
		AuthzOK:             state.signals.authzOK,
		AuthzReason:         state.signals.authzReason,
		AuditOK:             state.signals.auditOK,
		AuditReason:         state.signals.auditReason,
		EncryptionOK:        state.signals.encryptionOK,
		EncryptionReason:    state.signals.encryptionReason,
		RewrapOK:            state.signals.rewrapOK,
		RewrapReason:        state.signals.rewrapReason,
		Phase5GateOK:        state.signals.phase5GateOK,
		Phase5GateReason:    state.signals.phase5GateReason,
		PrivacyScanOK:       state.signals.privacyScanOK,
		PrivacyScanReason:   state.signals.privacyScanReason,
	})
	if err := writeJSONFile(filepath.Join(state.paths.root, "gates.json"), gate); err != nil {
		return Gate{}, err
	}
	opts.logf("Wrote gates.json")
	if gate.Pass {
		opts.logf("PASS - Evidence bundle: %s", state.paths.root)
	} else {
		opts.logf("FAIL - Evidence bundle: %s", state.paths.root)
	}
	logBundleContents(state.paths.root, opts.logf)
	return gate, nil
}

func (opts Options) withDefaults(cfg Config) Options {
	out := opts
	if out.Command == nil {
		out.Command = execCommandRunner{}
	}
	if out.HTTPClient == nil {
		out.HTTPClient = http.DefaultClient
	}
	if out.StressRunner == nil {
		out.StressRunner = execStressRunner{}
	}
	if out.AdminProbe == nil {
		out.AdminProbe = httpAdminProbe{client: out.HTTPClient}
	}
	if out.Clock == nil {
		out.Clock = systemClock{}
	}
	if out.Sleeper == nil {
		out.Sleeper = sleepContext
	}
	if out.Logf == nil {
		out.Logf = func(string, ...any) {}
	}
	out.Config = cfg
	return out
}

func (opts Options) logf(format string, args ...any) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
	}
}

func createBundleDirs(paths bundlePaths) error {
	for _, dir := range []string{paths.root, paths.metrics, paths.traces, paths.logs, paths.profiles, paths.eviction, paths.security} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create bundle dir %s: %w", dir, err)
		}
	}
	return nil
}

func writeRunConfig(ctx context.Context, cfg Config, runner CommandRunner, timestamp time.Time, shortSHA, root string) error {
	fullSHA := commandOutput(ctx, runner, "unknown", "git", "-C", cfg.RepoRoot, "rev-parse", "HEAD")
	image := commandOutput(ctx, runner, "unknown", cfg.Kubectl, kubectlGlobalArgs(cfg, "get", "statefulset", "scrapd", "-n", cfg.Namespace, "-o", "jsonpath={.spec.template.spec.containers[0].image}")...)
	replicasText := commandOutput(ctx, runner, "0", cfg.Kubectl, kubectlGlobalArgs(cfg, "get", "statefulset", "scrapd", "-n", cfg.Namespace, "-o", "jsonpath={.spec.replicas}")...)
	cluster := commandOutput(ctx, runner, "unknown", cfg.Kubectl, kubectlGlobalArgs(cfg, "config", "current-context")...)
	replicas, err := strconv.Atoi(strings.TrimSpace(replicasText))
	if err != nil {
		replicas = 0
	}
	report := runConfig{
		Scenario:       cfg.Scenario,
		Timestamp:      timestamp.Format(timestampFormat),
		GitSHA:         fullSHA,
		GitSHAShort:    shortSHA,
		GitDirty:       gitDirty(ctx, runner, cfg.RepoRoot),
		Image:          image,
		Replicas:       replicas,
		Workers:        cfg.Workers,
		Duration:       cfg.Duration,
		DocSizeBytes:   cfg.DocSizeBytes,
		EvictionPlanID: cfg.EvictionPlanID,
		Cluster:        cluster,
	}
	return writeJSONFile(filepath.Join(root, "config.json"), report)
}

func runStress(ctx context.Context, cfg Config, runner StressRunner, root string) (string, error) {
	result, err := runner.RunStress(ctx, StressRequest{
		RepoRoot:     cfg.RepoRoot,
		GoCommand:    cfg.GoCommand,
		Addr:         cfg.StressAddr,
		Scenario:     cfg.Scenario,
		Workers:      cfg.Workers,
		Duration:     cfg.Duration,
		DocSizeBytes: cfg.DocSizeBytes,
	})
	if result.Stderr != "" || err != nil {
		logText := result.Stderr
		if err != nil {
			logText = strings.TrimRight(logText, "\n") + "\n" + err.Error() + "\n"
		}
		if writeErr := os.WriteFile(filepath.Join(root, "stress.log"), []byte(logText), 0o600); writeErr != nil {
			return "", fmt.Errorf("write stress.log: %w", writeErr)
		}
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		output = `{"error":"no output"}`
	}
	if err := writeRawJSONFile(filepath.Join(root, "stress-results.json"), []byte(output)); err != nil {
		return "", err
	}
	return output, err
}

func captureMetricEvidence(ctx context.Context, client *http.Client, cfg Config, metricsDir string, baseline []metricSample, queryEpoch int64, signals *signalResults, logf func(string, ...any)) error {
	if err := captureRequiredRPCMetric(ctx, client, cfg, metricsDir, baseline, queryEpoch, signals, logf); err != nil {
		return err
	}
	return captureOptionalMetrics(ctx, client, cfg, metricsDir, queryEpoch, signals, logf)
}

func captureRequiredRPCMetric(ctx context.Context, client *http.Client, cfg Config, metricsDir string, baseline []metricSample, queryEpoch int64, signals *signalResults, logf func(string, ...any)) error {
	after, ok := queryMetricSamples(ctx, client, cfg.MimirProxy, rpcCounterQuery, queryEpoch)
	if !ok {
		signals.metricFailures++
		after = []metricSample{}
		if logf != nil {
			logf("WARNING: required metric query failed (no data envelope): rpc_requests")
		}
	}
	if err := writeJSONFile(filepath.Join(metricsDir, "rpc_requests_cumulative_after.json"), after); err != nil {
		return err
	}
	delta := rpcCounterDelta(baseline, after, queryEpoch)
	if err := writeJSONFile(filepath.Join(metricsDir, "rpc_requests.json"), delta); err != nil {
		return err
	}
	if !samplesHavePositiveValue(delta) {
		signals.metricEmptyRequired++
		if logf != nil {
			logf("WARNING: required metric returned no positive current-run delta: rpc_requests")
		}
	}
	return nil
}

func captureOptionalMetrics(ctx context.Context, client *http.Client, cfg Config, metricsDir string, queryEpoch int64, signals *signalResults, logf func(string, ...any)) error {
	for _, query := range metricQueries() {
		if err := captureOptionalMetric(ctx, client, cfg, metricsDir, queryEpoch, query, signals, logf); err != nil {
			return err
		}
	}
	return nil
}

func captureOptionalMetric(ctx context.Context, client *http.Client, cfg Config, metricsDir string, queryEpoch int64, query metricQuery, signals *signalResults, logf func(string, ...any)) error {
	samples, ok := queryMetricSamples(ctx, client, cfg.MimirProxy, query.query, queryEpoch)
	if !ok {
		signals.metricFailures++
		samples = []metricSample{}
		if logf != nil {
			logf("WARNING: metric query failed (no data envelope): %s", query.name)
		}
	}
	return writeJSONFile(filepath.Join(metricsDir, query.name+".json"), samples)
}

func metricQueries() []metricQuery {
	return []metricQuery{
		{name: "rpc_duration_p99", query: "histogram_quantile(0.99, sum(rate(scrap_rpc_server_duration_seconds_bucket[5m])) by (le, rpc_method))"},
		{name: "write_stage_duration", query: "sum(rate(scrap_write_stage_duration_seconds_sum[5m])) by (scrap_write_stage) / sum(rate(scrap_write_stage_duration_seconds_count[5m])) by (scrap_write_stage)"},
		{name: "upload_pressure", query: "scrap_upload_pressure_level"},
		{name: "upload_pending_bytes", query: "scrap_upload_pending_bytes"},
		{name: "raft_is_leader", query: "scrap_raft_is_leader"},
		{name: "goroutines", query: "process_runtime_go_goroutines"},
		{name: "heap_alloc", query: "process_runtime_go_mem_heap_alloc_bytes"},
		{name: "eviction_plans", query: "sum(scrap_eviction_plans_total) by (reason)"},
		{name: "eviction_skips", query: "sum(scrap_eviction_skips_total) by (reason)"},
		{name: "eviction_candidate_blocks", query: "scrap_eviction_candidate_blocks"},
		{name: "eviction_selected_bytes", query: "scrap_eviction_selected_bytes"},
		{name: "eviction_apply_total", query: "sum(scrap_eviction_apply_total_total) by (reason, status)"},
		{name: "eviction_restore_total", query: "sum(scrap_eviction_restore_total_total) by (reason, result, failure_reason)"},
		{name: "eviction_evicted_blocks", query: "scrap_eviction_evicted_blocks"},
		{name: "eviction_restore_failed_blocks", query: "scrap_eviction_restore_failed_blocks"},
		{name: "eviction_quarantined_blocks", query: "scrap_eviction_quarantined_blocks"},
		{name: "avscan_lag_blocks", query: "scrap_avscan_lag_blocks"},
		{name: "avscan_engine_unavailable", query: "sum(scrap_avscan_engine_unavailable_total) by (scrap_shard_id)"},
		{name: "avscan_failures", query: "sum(rate(scrap_avscan_failures_total[5m])) by (scrap_shard_id, reason)"},
		{name: "security_rate_limit_denials", query: "sum(rate(scrap_security_rate_limit_denials_total[5m])) by (scrap_surface, scrap_operation, scrap_reason)"},
		{name: "security_authorization_denials", query: "sum(rate(scrap_security_authorization_denials_total[5m])) by (scrap_surface, scrap_operation, scrap_reason, scrap_authorization_status)"},
		{name: "scrub_deep_corruptions", query: "sum(rate(scrap_scrub_deep_corruptions_total[5m])) by (scrap_shard_id)"},
	}
}

type securityHealthEvidence struct {
	SecurityMode              string `json:"security_mode"`
	ProductionReadinessStatus string `json:"production_readiness_status"`
	ProductionReadinessReason string `json:"production_readiness_reason"`
	AuthorizationStatus       string `json:"authorization_status"`
	RewrapStatus              string `json:"rewrap_status"`
	RewrapLastResult          string `json:"rewrap_last_result"`
	RewrapLastReason          string `json:"rewrap_last_reason"`
}

type securityReportEvidence struct {
	SecurityMode              string `json:"security_mode"`
	ProductionReadinessStatus string `json:"production_readiness_status"`
	ProductionReadinessReason string `json:"production_readiness_reason"`
	AuthorizationStatus       string `json:"authorization_status"`
	PublicUnauthorizedDenied  bool   `json:"public_unauthorized_denied"`
	PeerUnauthorizedDenied    bool   `json:"peer_unauthorized_denied"`
	AdminUnauthorizedDenied   bool   `json:"admin_unauthorized_denied"`
	AuditSamplesRecorded      bool   `json:"audit_samples_recorded"`
	EncryptedWriteReadOK      bool   `json:"encrypted_write_read_ok"`
	EncryptedBackendUploadOK  bool   `json:"encrypted_backend_upload_ok"`
	EncryptedRestoreOK        bool   `json:"encrypted_restore_ok"`
	RewrapOK                  bool   `json:"rewrap_ok"`
	Phase5EntryBlocked        bool   `json:"phase5_entry_blocked"`
}

func captureSecurityEvidence(cfg Config, state *generationState) error {
	healthBody := state.adminHealth
	if len(healthBody) == 0 {
		healthBody = []byte(`{"error":"admin security health unavailable"}`)
	}
	if err := writeRawJSONFile(filepath.Join(state.paths.security, "health.json"), healthBody); err != nil {
		return err
	}
	health := parseSecurityHealth(healthBody)
	report, reportLoaded, err := captureSecurityReport(cfg.SecurityReportPath, state.paths.security)
	if err != nil {
		return err
	}
	applySecuritySignals(&state.signals, health, report, reportLoaded)
	return nil
}

func applySecuritySignals(signals *signalResults, health securityHealthEvidence, report securityReportEvidence, reportLoaded bool) {
	if !reportLoaded {
		applyMissingSecurityReportSignals(signals)
		return
	}
	signals.securityModeOK = securityReportModeOK(report)
	signals.securityModeReason = securityReportModeReason(report, health)
	signals.authzOK = securityReportAuthzOK(report)
	signals.authzReason = securityReportReason(signals.authzOK, reportLoaded, "unauthorized public/peer/admin denied", "missing unauthorized denial proof")
	signals.auditOK = report.AuditSamplesRecorded
	signals.auditReason = securityReportReason(signals.auditOK, reportLoaded, "audit samples recorded", "missing audit sample proof")
	signals.encryptionOK = securityReportEncryptionOK(report)
	signals.encryptionReason = securityReportReason(signals.encryptionOK, reportLoaded, "encrypted write/read, backend upload, and restore passed", "missing encrypted write/read/upload/restore proof")
	signals.rewrapOK = securityReportRewrapOK(report)
	signals.rewrapReason = rewrapEvidenceReason(signals.rewrapOK, reportLoaded)
	signals.phase5GateOK = securityReportPhase5GateOK(report)
	signals.phase5GateReason = phase5GateReason(signals.phase5GateOK, reportLoaded, report)
}

func securityReportModeOK(report securityReportEvidence) bool {
	if report.SecurityMode == "" || report.ProductionReadinessStatus == "" {
		return false
	}
	modeOK := report.SecurityMode == "test" || report.SecurityMode == "production"
	authzOK := report.AuthorizationStatus == "configured" || report.AuthorizationStatus == "denied"
	return modeOK && authzOK
}

func applyMissingSecurityReportSignals(signals *signalResults) {
	signals.securityModeOK = false
	signals.securityModeReason = "security evidence report unavailable"
	signals.authzOK = false
	signals.authzReason = "security evidence report unavailable"
	signals.auditOK = false
	signals.auditReason = "security evidence report unavailable"
	signals.encryptionOK = false
	signals.encryptionReason = "security evidence report unavailable"
	signals.rewrapOK = false
	signals.rewrapReason = "security evidence report unavailable"
	signals.phase5GateOK = false
	signals.phase5GateReason = "security evidence report unavailable"
}

func securityReportAuthzOK(report securityReportEvidence) bool {
	return report.PublicUnauthorizedDenied && report.PeerUnauthorizedDenied && report.AdminUnauthorizedDenied
}

func securityReportEncryptionOK(report securityReportEvidence) bool {
	return report.EncryptedWriteReadOK && report.EncryptedBackendUploadOK && report.EncryptedRestoreOK
}

func securityReportRewrapOK(report securityReportEvidence) bool {
	return report.RewrapOK
}

func securityReportPhase5GateOK(report securityReportEvidence) bool {
	return report.Phase5EntryBlocked || report.ProductionReadinessStatus == "ready"
}

func parseSecurityHealth(body []byte) securityHealthEvidence {
	var health securityHealthEvidence
	_ = json.Unmarshal(body, &health)
	return health
}

func captureSecurityReport(path, dir string) (securityReportEvidence, bool, error) {
	if strings.TrimSpace(path) == "" {
		body := []byte(`{"error":"security evidence report path not configured"}`)
		return securityReportEvidence{}, false, writeRawJSONFile(filepath.Join(dir, "e2e-report.json"), body)
	}
	body, err := os.ReadFile(path) //nolint:gosec // Operator-selected report path is copied into the bundle.
	if err != nil {
		body = []byte(`{"error":"security evidence report unavailable"}`)
		return securityReportEvidence{}, false, writeRawJSONFile(filepath.Join(dir, "e2e-report.json"), body)
	}
	if err := writeRawJSONFile(filepath.Join(dir, "e2e-report.json"), body); err != nil {
		return securityReportEvidence{}, false, err
	}
	var report securityReportEvidence
	if err := json.Unmarshal(body, &report); err != nil {
		return securityReportEvidence{}, false, fmt.Errorf("parse security evidence report: %w", err)
	}
	return report, true, nil
}

func securityReportModeReason(report securityReportEvidence, health securityHealthEvidence) string {
	if report.SecurityMode == "" {
		return "missing security_mode"
	}
	if report.ProductionReadinessStatus == "" {
		return "missing production_readiness_status"
	}
	reasonText := "report_mode=" + report.SecurityMode + " report_readiness=" + report.ProductionReadinessStatus
	if report.ProductionReadinessReason != "" {
		reasonText += " reason=" + report.ProductionReadinessReason
	}
	if report.AuthorizationStatus != "" {
		reasonText += " report_authz=" + report.AuthorizationStatus
	}
	if health.SecurityMode != "" {
		reasonText += " health_mode=" + health.SecurityMode
	}
	return reasonText
}

func securityReportReason(ok, reportLoaded bool, pass, fail string) string {
	if ok {
		return pass
	}
	if !reportLoaded {
		return "security evidence report unavailable"
	}
	return fail
}

func rewrapEvidenceReason(ok, reportLoaded bool) string {
	if ok {
		return "rewrap recorded"
	}
	return securityReportReason(false, reportLoaded, "", "missing rewrap proof")
}

func phase5GateReason(ok, reportLoaded bool, report securityReportEvidence) string {
	if ok {
		if report.ProductionReadinessStatus == "ready" {
			return "phase5 gate can evaluate production readiness"
		}
		return "phase5 entry blocked by non-ready production posture"
	}
	return securityReportReason(false, reportLoaded, "", "missing phase5 gate proof")
}

func writeQueriesFile(cfg Config, marker, root string) error {
	queries := map[string]any{
		"traces": map[string]any{
			"grafana_explore_url": strings.TrimRight(cfg.GrafanaURL, "/") + "/explore?orgId=1&left=%7B%22datasource%22%3A%22tempo%22%2C%22queries%22%3A%5B%7B%22queryType%22%3A%22traceqlSearch%22%7D%5D%7D",
			"gate_query":          "service.name=scrapd",
			"slow_writes":         `{ span.scrap.write.stage = "raft_propose" } | duration > 100ms`,
			"errors":              "{ status = error }",
		},
		"logs": map[string]any{
			"grafana_explore_url": strings.TrimRight(cfg.GrafanaURL, "/") + "/explore?orgId=1&left=%7B%22datasource%22%3A%22loki%22%7D",
			"gate_query":          `{service_name="scrapd"} |= "` + marker + `"`,
			"marker":              marker,
			"probe_url":           strings.TrimRight(cfg.AdminURL, "/") + "/healthz",
		},
		"profiles": map[string]any{
			"grafana_explore_url": strings.TrimRight(cfg.GrafanaURL, "/") + "/explore?orgId=1&left=%7B%22datasource%22%3A%22pyroscope%22%7D",
			"cpu_query":           `process_cpu:cpu:nanoseconds:cpu:nanoseconds{service_name="scrapd"}`,
			"heap_queries": []string{
				`memory:inuse_space:bytes:space:bytes{service_name="scrapd"}`,
				`memory:alloc_space:bytes:space:bytes{service_name="scrapd"}`,
			},
		},
	}
	return writeJSONFile(filepath.Join(root, "queries.json"), queries)
}

func captureTraceLogProfileEvidence(ctx context.Context, client *http.Client, cfg Config, paths bundlePaths, window runWindow, marker string, probeFailed bool, signals *signalResults) error {
	traceOK, traceReason, err := captureTraceEvidence(ctx, client, cfg, paths.traces, window)
	if err != nil {
		return err
	}
	signals.traceOK = traceOK
	signals.traceReason = traceReason

	logOK, logReason, err := captureLogEvidence(ctx, client, cfg, paths.logs, window, marker, probeFailed)
	if err != nil {
		return err
	}
	signals.logOK = logOK
	signals.logReason = logReason

	cpuOK, cpuReason, err := captureCPUProfile(ctx, client, cfg, paths.profiles, window)
	if err != nil {
		return err
	}
	signals.cpuProfileOK = cpuOK
	signals.cpuReason = cpuReason

	heapOK, heapReason, err := captureHeapProfile(ctx, client, cfg, paths.profiles, window)
	if err != nil {
		return err
	}
	signals.heapProfileOK = heapOK
	signals.heapReason = heapReason
	return nil
}

type evictionStatusEvidence struct {
	Plan        json.RawMessage `json:"plan"`
	ApplyResult json.RawMessage `json:"apply_result"`
}

type evictionApplyEvidence struct {
	ValidatedBlocks        int               `json:"validated_blocks"`
	ValidationFailedBlocks int               `json:"validation_failed_blocks"`
	Validations            []json.RawMessage `json:"validations"`
}

func captureEvictionHealth(ctx context.Context, client *http.Client, cfg Config, dir string) error {
	if strings.TrimSpace(cfg.EvictionPlanID) == "" {
		return nil
	}
	body, ok := getJSON(ctx, client, strings.TrimRight(cfg.AdminURL, "/")+"/healthz", nil)
	if !ok {
		body = []byte(`{"error":"admin health snapshot unavailable"}`)
	}
	return writeRawJSONFile(filepath.Join(dir, "health.json"), body)
}

func captureEvictionCampaign(ctx context.Context, client *http.Client, cfg Config, dir string) error {
	if strings.TrimSpace(cfg.EvictionPlanID) == "" {
		return nil
	}
	endpoint := strings.TrimRight(cfg.AdminURL, "/") + "/admin/eviction/plans/" + url.PathEscape(cfg.EvictionPlanID)
	body, ok := getJSON(ctx, client, endpoint, nil)
	if !ok {
		return fmt.Errorf("capture eviction status for plan %s", cfg.EvictionPlanID)
	}
	if err := writeRawJSONFile(filepath.Join(dir, "status.json"), body); err != nil {
		return err
	}
	return writeEvictionCampaignArtifacts(dir, body)
}

func writeEvictionCampaignArtifacts(dir string, statusBody []byte) error {
	var status evictionStatusEvidence
	if err := json.Unmarshal(statusBody, &status); err != nil {
		return fmt.Errorf("parse eviction status evidence: %w", err)
	}
	if len(status.Plan) == 0 {
		status.Plan = []byte(`null`)
	}
	if len(status.ApplyResult) == 0 {
		status.ApplyResult = []byte(`null`)
	}
	if err := writeRawJSONFile(filepath.Join(dir, "candidate-plan.json"), status.Plan); err != nil {
		return err
	}
	if err := writeRawJSONFile(filepath.Join(dir, "apply-result.json"), status.ApplyResult); err != nil {
		return err
	}
	return writeEvictionValidationResult(dir, status.ApplyResult)
}

func writeEvictionValidationResult(dir string, applyBody []byte) error {
	var apply evictionApplyEvidence
	if string(applyBody) != "null" {
		if err := json.Unmarshal(applyBody, &apply); err != nil {
			return fmt.Errorf("parse eviction apply evidence: %w", err)
		}
	}
	if apply.Validations == nil {
		apply.Validations = []json.RawMessage{}
	}
	return writeJSONFile(filepath.Join(dir, "validation-result.json"), apply)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeRawJSONFile(path string, data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return writeJSONFile(path, map[string]string{"error": "invalid JSON"})
	}
	return writeJSONFile(path, value)
}

func commandOutput(ctx context.Context, runner CommandRunner, fallback, name string, args ...string) string {
	out, err := runner.Run(ctx, name, args...)
	if err != nil {
		return fallback
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return fallback
	}
	return out
}

func gitDirty(ctx context.Context, runner CommandRunner, repoRoot string) bool {
	_, err := runner.Run(ctx, "git", "-C", repoRoot, "diff", "--quiet")
	return err != nil
}

func kubectlGlobalArgs(cfg Config, args ...string) []string {
	out := make([]string, 0, safeStringSliceCapacity(len(args), kubectlGlobalArgCapacity))
	if cfg.KubeContext != "" {
		out = append(out, "--context", cfg.KubeContext)
	}
	if cfg.Kubeconfig != "" {
		out = append(out, "--kubeconfig", cfg.Kubeconfig)
	}
	out = append(out, args...)
	return out
}

func logBundleContents(root string, logf func(string, ...any)) {
	if logf == nil {
		return
	}
	logf("Bundle contents:")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return fmt.Errorf("stat bundle entry %s: %w", path, statErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		logf("  %s (%d bytes)", rel, info.Size())
		return nil
	})
	if err != nil {
		logf("  <walk failed: %v>", err)
	}
}
