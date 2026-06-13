package evidencebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const manifestSchemaVersion = "scrap.evidence.bundle/v1"

type bundleManifest struct {
	SchemaVersion        string                  `json:"schema_version"`
	BundleName           string                  `json:"bundle_name"`
	Scenario             string                  `json:"scenario"`
	GeneratedAt          string                  `json:"generated_at"`
	PrivacyStatus        string                  `json:"privacy_status"`
	PrivacyFindingsCount int                     `json:"privacy_findings_count"`
	Provenance           manifestProvenance      `json:"provenance"`
	Environment          manifestEnvironment     `json:"environment"`
	RunParameters        manifestRunParameters   `json:"run_parameters"`
	Commands             []manifestCommand       `json:"commands"`
	Artifacts            []manifestArtifact      `json:"artifacts"`
	Evidence             []manifestEvidenceState `json:"evidence"`
}

type manifestProvenance struct {
	GitSHA      string `json:"git_sha"`
	GitSHAShort string `json:"git_sha_short"`
	GitDirty    bool   `json:"git_dirty"`
	Image       string `json:"image"`
	Replicas    int    `json:"replicas"`
}

type manifestEnvironment struct {
	Namespace   string `json:"namespace"`
	KubeContext string `json:"kube_context,omitempty"`
	Cluster     string `json:"cluster"`
}

type manifestRunParameters struct {
	Workers                  int    `json:"workers"`
	Duration                 string `json:"duration"`
	DocSizeBytes             int    `json:"doc_size_bytes"`
	StressAddr               string `json:"stress_addr"`
	SecurityReportConfigured bool   `json:"security_report_configured"`
	SecurityReportPresent    bool   `json:"security_report_present"`
}

type manifestCommand struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Artifact    string `json:"artifact"`
	Passive     bool   `json:"passive"`
	Description string `json:"description"`
}

type manifestArtifact struct {
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}

type manifestEvidenceState struct {
	Area       string `json:"area"`
	Status     string `json:"status"`
	Artifact   string `json:"artifact,omitempty"`
	Reason     string `json:"reason"`
	Owner      string `json:"owner"`
	Mitigation string `json:"mitigation,omitempty"`
}

func writeManifest(cfg Config, state generationState, gate Gate, privacy privacyScanReport) error {
	run, err := readRunConfig(state.paths.root)
	if err != nil {
		return err
	}
	artifacts, err := collectManifestArtifacts(state.paths.root)
	if err != nil {
		return err
	}
	manifest := bundleManifest{
		SchemaVersion:        manifestSchemaVersion,
		BundleName:           filepath.Base(state.paths.root),
		Scenario:             cfg.Scenario,
		GeneratedAt:          run.Timestamp,
		PrivacyStatus:        privacy.Status,
		PrivacyFindingsCount: privacy.FindingsCount,
		Provenance: manifestProvenance{
			GitSHA:      run.GitSHA,
			GitSHAShort: run.GitSHAShort,
			GitDirty:    run.GitDirty,
			Image:       run.Image,
			Replicas:    run.Replicas,
		},
		Environment: manifestEnvironment{
			Namespace:   cfg.Namespace,
			KubeContext: cfg.KubeContext,
			Cluster:     run.Cluster,
		},
		RunParameters: manifestRunParameters{
			Workers:                  run.Workers,
			Duration:                 run.Duration,
			DocSizeBytes:             run.DocSizeBytes,
			StressAddr:               run.StressAddr,
			SecurityReportConfigured: run.SecurityReportConfigured,
			SecurityReportPresent:    artifactHasEvidence(state.paths.root, "security/e2e-report.json"),
		},
		Commands:  manifestCommands(cfg),
		Artifacts: artifacts,
		Evidence:  manifestEvidence(cfg, state, gate, privacy),
	}
	return writeJSONFile(filepath.Join(state.paths.root, "manifest.json"), manifest)
}

func readRunConfig(root string) (runConfig, error) {
	data, err := os.ReadFile(filepath.Join(root, "config.json")) //nolint:gosec // config path is inside the generated bundle.
	if err != nil {
		return runConfig{}, fmt.Errorf("read run config for manifest: %w", err)
	}
	var run runConfig
	if err := json.Unmarshal(data, &run); err != nil {
		return runConfig{}, fmt.Errorf("parse run config for manifest: %w", err)
	}
	return run, nil
}

func collectManifestArtifacts(root string) ([]manifestArtifact, error) {
	var artifacts []manifestArtifact
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("rel manifest path %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" {
			return nil
		}
		artifact, err := manifestArtifactForPath(path, rel)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk bundle artifacts for manifest: %w", err)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts, nil
}

func manifestArtifactForPath(path, rel string) (manifestArtifact, error) {
	file, err := os.Open(path) //nolint:gosec // path is generated by walking the bundle directory.
	if err != nil {
		return manifestArtifact{}, fmt.Errorf("open artifact %s: %w", rel, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return manifestArtifact{}, fmt.Errorf("hash artifact %s: %w", rel, err)
	}
	return manifestArtifact{
		Path:        rel,
		SizeBytes:   size,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: manifestContentType(rel),
	}, nil
}

func manifestContentType(rel string) string {
	if filepath.Ext(rel) == ".json" {
		return "application/json"
	}
	return "text/plain"
}

func manifestCommands(cfg Config) []manifestCommand {
	return []manifestCommand{
		{Name: "stress", Command: stressCommand(cfg), Artifact: "stress-results.json", Passive: true, Description: "current-run workload evidence"},
		{Name: "status", Command: "scrapctl status --output=json", Artifact: "logs/evidence-probe-health.json", Passive: true, Description: "status evidence captured through the admin health source used by status"},
		{Name: "leader", Command: "scrapctl leader --output=json", Artifact: "metrics/raft_is_leader.json", Passive: true, Description: "leader evidence captured through the Raft leadership metric"},
		{Name: "peers", Command: "scrapctl peers --output=json", Artifact: "", Passive: false, Description: "passive peer command collection is not implemented; manifest records concern"},
		{Name: "upload_pressure", Command: "scrapctl upload-pressure --output=json", Artifact: "metrics/upload_pressure.json", Passive: true, Description: "upload pressure evidence captured through the release metric snapshot"},
		{Name: "admin_health_probe", Command: "GET /healthz with X-Scrap-Evidence-Marker", Artifact: "logs/evidence-probe-health.json", Passive: true, Description: "status, security mode, Shard, scanner, and quarantine health snapshot"},
		{Name: "metrics", Command: "Prometheus instant queries via configured Mimir proxy", Artifact: "metrics/", Passive: true, Description: "RPC, upload, eviction, scanner, security, Raft, and runtime metric snapshots"},
		{Name: "traces", Command: "Tempo search for service.name=scrapd", Artifact: "traces/scrapd.json", Passive: true, Description: "redacted trace-count evidence"},
		{Name: "logs", Command: "Loki query for generated evidence marker", Artifact: "logs/scrapd.json", Passive: true, Description: "current-run log marker evidence"},
		{Name: "profiles", Command: "Pyroscope render queries", Artifact: "profiles/", Passive: true, Description: "CPU and heap profile evidence"},
		{Name: "security_report", Command: "copy configured security evidence report", Artifact: "security/e2e-report.json", Passive: true, Description: "production-security rehearsal summary"},
		{Name: "eviction_status", Command: "GET /admin/eviction/plans/<redacted-plan-id>", Artifact: "eviction/", Passive: true, Description: evictionCommandDescription(cfg)},
		{Name: "fault", Command: "scrapctl fault ...", Artifact: "", Passive: false, Description: "fault commands are intentionally not run by passive release collection"},
		{Name: "quarantine", Command: "scrapctl quarantine evidence --output=json", Artifact: "logs/evidence-probe-health.json", Passive: true, Description: "quarantine status evidence is captured from admin health when present"},
		{Name: "openbao", Command: "scrapctl openbao bootstrap --evidence-path=<bundle-relative>", Artifact: "security/e2e-report.json", Passive: true, Description: "OpenBao readiness is aggregated from security report evidence"},
		{Name: "privacy_scan", Command: "scan generated bundle artifacts for forbidden evidence shapes", Artifact: "privacy-scan.json", Passive: true, Description: "release-sensitive leak scan"},
	}
}

func stressCommand(cfg Config) string {
	return manifestCommandName(cfg.GoCommand) +
		" run ./test/stress" +
		" -addr=" + cfg.StressAddr +
		" -scenario=" + cfg.Scenario +
		" -workers=" + strconv.Itoa(cfg.Workers) +
		" -duration=" + cfg.Duration +
		" -doc-size=" + strconv.Itoa(cfg.DocSizeBytes)
}

func manifestCommandName(command string) string {
	if filepath.IsAbs(command) {
		base := filepath.Base(command)
		if base != "" {
			return base
		}
		return "<absolute-command>"
	}
	return command
}

func evictionCommandDescription(cfg Config) string {
	if strings.TrimSpace(cfg.EvictionPlanID) == "" {
		return "eviction plan not configured; manifest records concern"
	}
	return "eviction campaign status and validation evidence"
}

func manifestEvidence(cfg Config, state generationState, gate Gate, privacy privacyScanReport) []manifestEvidenceState {
	root := state.paths.root
	return []manifestEvidenceState{
		fileEvidence(root, "status", "logs/evidence-probe-health.json", "admin health/status snapshot captured"),
		fileEvidence(root, "leaders", "metrics/raft_is_leader.json", "Raft leader metric captured"),
		concernEvidence("peers", "Story 6.5 / Tier 2 gate", "passive bundle does not invoke scrapctl peers yet", "Link Tier 2 peer evidence or add safe passive peer collection."),
		fileEvidence(root, "upload_pressure", "metrics/upload_pressure.json", "upload pressure metric captured"),
		concernEvidence("faults", "release owner", "fault commands are intentionally non-passive and not run by release bundle", "Link controlled non-production fault drill evidence."),
		evictionEvidence(root, cfg),
		fileEvidence(root, "shard_health", "metrics/raft_is_leader.json", "Shard leadership metric captured"),
		fileEvidenceAll(root, "scanner_quarantine", []string{"metrics/avscan_lag_blocks.json", "logs/evidence-probe-health.json"}, "scanner lag metric and admin health captured"),
		gateEvidenceAll("openbao_readiness", "security/e2e-report.json", "release owner / Epic 4", gate, "security and OpenBao readiness evidence recorded", "security_mode_recorded", "encryption_outcomes_recorded", "rewrap_outcomes_recorded"),
		gateEvidence("telemetry", "metrics/rpc_requests.json", "release owner / Story 6.5", gate, "metrics_captured", "required current-run metrics captured"),
		gateEvidence("traces", "traces/scrapd.json", "release owner / Story 6.5", gate, "traces_captured", "redacted trace evidence captured"),
		gateEvidence("logs", "logs/scrapd.json", "release owner / Story 6.5", gate, "logs_captured", "current-run log marker captured"),
		gateEvidenceAll("profiles", "profiles/", "release owner / Story 6.5", gate, "CPU and heap profile evidence captured", "cpu_profile_captured", "heap_profile_captured"),
		gateEvidenceAll("security", "security/e2e-report.json", "release owner / Epic 4", gate, "security report, authz, audit, encryption, rewrap, and Phase 5 proof recorded", "security_mode_recorded", "authorization_denials_recorded", "audit_samples_recorded", "encryption_outcomes_recorded", "rewrap_outcomes_recorded", "phase5_gate_recorded"),
		privacyEvidence(state, privacy),
	}
}

func fileEvidence(root, area, artifact, passReason string) manifestEvidenceState {
	if artifactHasEvidence(root, artifact) {
		return manifestEvidenceState{Area: area, Status: "PASS", Artifact: artifact, Reason: passReason, Owner: "release owner"}
	}
	return manifestEvidenceState{Area: area, Status: "FAIL", Artifact: artifact, Reason: "required artifact missing or empty", Owner: "release owner", Mitigation: "rerun bundle after fixing artifact generation"}
}

func fileEvidenceAll(root, area string, artifacts []string, passReason string) manifestEvidenceState {
	var missing []string
	for _, artifact := range artifacts {
		if !artifactHasEvidence(root, artifact) {
			missing = append(missing, artifact)
		}
	}
	if len(missing) == 0 {
		return manifestEvidenceState{Area: area, Status: "PASS", Artifact: strings.Join(artifacts, ","), Reason: passReason, Owner: "release owner"}
	}
	return manifestEvidenceState{Area: area, Status: "FAIL", Artifact: strings.Join(artifacts, ","), Reason: "required artifact missing or empty: " + strings.Join(missing, ","), Owner: "release owner", Mitigation: "rerun bundle after fixing artifact generation"}
}

func concernEvidence(area, owner, reason, mitigation string) manifestEvidenceState {
	return manifestEvidenceState{Area: area, Status: "CONCERNS", Reason: reason, Owner: owner, Mitigation: mitigation}
}

func evictionEvidence(root string, cfg Config) manifestEvidenceState {
	if strings.TrimSpace(cfg.EvictionPlanID) == "" {
		return concernEvidence("eviction_restore", "release owner / Epic 3", "eviction plan id not configured for this bundle", "Run bundle with --eviction-plan-id when release proof requires an eviction campaign.")
	}
	if artifactHasEvidence(root, "eviction/status.json") {
		return manifestEvidenceState{Area: "eviction_restore", Status: "PASS", Artifact: "eviction/status.json", Reason: "eviction campaign evidence captured", Owner: "release owner / Epic 3"}
	}
	return manifestEvidenceState{Area: "eviction_restore", Status: "FAIL", Artifact: "eviction/status.json", Reason: "eviction campaign status missing or empty", Owner: "release owner / Epic 3", Mitigation: "rerun bundle with a valid --eviction-plan-id after fixing eviction evidence collection"}
}

func gateEvidence(area, artifact, owner string, gate Gate, checkName, passReason string) manifestEvidenceState {
	for _, check := range gate.Checks {
		if check.Name != checkName {
			continue
		}
		if check.Pass {
			return manifestEvidenceState{Area: area, Status: "PASS", Artifact: artifact, Reason: passReason, Owner: owner}
		}
		return manifestEvidenceState{Area: area, Status: "FAIL", Artifact: artifact, Reason: check.Reason, Owner: owner, Mitigation: "fix failing gate evidence and rerun bundle"}
	}
	return manifestEvidenceState{Area: area, Status: "FAIL", Artifact: artifact, Reason: "gate check missing: " + checkName, Owner: owner, Mitigation: "restore expected gate check"}
}

func gateEvidenceAll(area, artifact, owner string, gate Gate, passReason string, checkNames ...string) manifestEvidenceState {
	checks := make(map[string]GateCheck, len(gate.Checks))
	for _, check := range gate.Checks {
		checks[check.Name] = check
	}
	var failures []string
	for _, name := range checkNames {
		check, ok := checks[name]
		if !ok {
			failures = append(failures, name+" missing")
			continue
		}
		if !check.Pass {
			failures = append(failures, name+": "+check.Reason)
		}
	}
	if len(failures) == 0 {
		return manifestEvidenceState{Area: area, Status: "PASS", Artifact: artifact, Reason: passReason, Owner: owner}
	}
	return manifestEvidenceState{Area: area, Status: "FAIL", Artifact: artifact, Reason: strings.Join(failures, "; "), Owner: owner, Mitigation: "fix failing gate evidence and rerun bundle"}
}

func privacyEvidence(state generationState, privacy privacyScanReport) manifestEvidenceState {
	if privacy.Status == privacyStatusPass {
		return manifestEvidenceState{Area: "redaction_proof", Status: "PASS", Artifact: "privacy-scan.json", Reason: state.signals.privacyScanReason, Owner: "release owner"}
	}
	return manifestEvidenceState{Area: "redaction_proof", Status: "FAIL", Artifact: "privacy-scan.json", Reason: state.signals.privacyScanReason, Owner: "release owner", Mitigation: "remove or redact forbidden findings and rerun bundle"}
}

func artifactHasEvidence(root, artifact string) bool {
	path := filepath.Join(root, filepath.FromSlash(artifact))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	data, err := os.ReadFile(path) //nolint:gosec // artifact path is bundle-relative and selected by the bundle manifest.
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return false
	}
	return jsonArtifactHasEvidence(data)
}

func jsonArtifactHasEvidence(data []byte) bool {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return true
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) > 0
	case map[string]any:
		if len(typed) == 0 {
			return false
		}
		if errorValue, ok := typed["error"].(string); ok && errorValue != "" {
			return false
		}
		if status, ok := typed["status"].(string); ok && strings.EqualFold(status, "error") {
			return false
		}
	}
	return true
}
