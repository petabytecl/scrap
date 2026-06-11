package scrapctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	minMetricFields = 2
	podReadyType    = "Ready"
	metricFalse     = "0"
	metricTrue      = "1"
)

type Health struct {
	Status                  string            `json:"status"`
	SecurityMode            string            `json:"security_mode,omitempty"`
	ProductionReadyStatus   string            `json:"production_readiness_status,omitempty"`
	ProductionReadyReason   string            `json:"production_readiness_reason,omitempty"`
	UploadPressure          string            `json:"upload_pressure"`
	UploadPressureLevel     int               `json:"upload_pressure_level"`
	UploadPendingBytes      int64             `json:"upload_pending_bytes"`
	UploadPendingBlocks     int               `json:"upload_pending_blocks"`
	EvictionPressure        string            `json:"eviction_pressure"`
	EvictedBlocks           int               `json:"evicted_blocks"`
	EvictedBytes            int64             `json:"evicted_bytes"`
	HotCleanupNeededBlocks  int               `json:"hot_cleanup_needed_blocks"`
	MetadataLossBlocks      int               `json:"metadata_loss_blocks"`
	UnexpectedLossBlocks    int               `json:"unexpected_loss_blocks"`
	QuarantinedBlocks       int               `json:"quarantined_blocks"`
	RestoreFailedBlocks     int               `json:"restore_failed_blocks"`
	RestoreFailuresByReason map[string]int    `json:"restore_failures_by_reason,omitempty"`
	ShardDiagnostics        *ShardDiagnostics `json:"shard_diagnostics,omitempty"`
}

type ShardDiagnostics struct {
	Status         string            `json:"status,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	CellID         string            `json:"cell_id,omitempty"`
	MemberHostname string            `json:"member_hostname,omitempty"`
	MemberID       string            `json:"member_id,omitempty"`
	Shards         []ShardDiagnostic `json:"shards,omitempty"`
}

type ShardDiagnostic struct {
	ShardID             uint64   `json:"shard_id"`
	Membership          string   `json:"membership,omitempty"`
	Routes              []string `json:"routes,omitempty"`
	State               string   `json:"state,omitempty"`
	Health              string   `json:"health,omitempty"`
	Readiness           string   `json:"readiness,omitempty"`
	LeaderState         string   `json:"leader_state,omitempty"`
	IsLeader            bool     `json:"is_leader"`
	LeaderID            uint64   `json:"leader_id,omitempty"`
	PeerCount           int      `json:"peer_count"`
	PeerHealth          string   `json:"peer_health,omitempty"`
	UploadPressure      string   `json:"upload_pressure,omitempty"`
	UploadPressureLevel int      `json:"upload_pressure_level,omitempty"`
	UploadPendingBytes  int64    `json:"upload_pending_bytes,omitempty"`
	UploadPendingBlocks int      `json:"upload_pending_blocks,omitempty"`
	EvictionPressure    string   `json:"eviction_pressure,omitempty"`
	RestoreFailedBlocks int      `json:"restore_failed_blocks,omitempty"`
	FailureReason       string   `json:"failure_reason,omitempty"`
}

type Peer struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
}

type podList struct {
	Items []pod `json:"items"`
}

type pod struct {
	Metadata podMetadata `json:"metadata"`
	Status   podStatus   `json:"status"`
}

type podMetadata struct {
	Name string `json:"name"`
}

type podStatus struct {
	Conditions []podCondition `json:"conditions"`
}

type podCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func runStatus(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseCommon("status", args)
	if err != nil {
		return err
	}
	cctx, cancel := commandContext(context.Background(), opts.timeout)
	defer cancel()
	health, err := fetchHealth(cctx, opts, deps)
	if err != nil {
		return err
	}
	return writeByFormat(stdout, opts.output, health)
}

func runUploadPressure(args []string, stdout io.Writer, deps Deps) error {
	return runStatus(args, stdout, deps)
}

func runPeers(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseCommon("peers", args)
	if err != nil {
		return err
	}
	cctx, cancel := commandContext(context.Background(), opts.timeout)
	defer cancel()
	out, err := deps.Runner.Run(cctx, opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "pods", "-l", "app=scrap", "-o", "json")...)
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	peers, err := parsePeers(out)
	if err != nil {
		return fmt.Errorf("parse peers: %w", err)
	}
	return writeByFormat(stdout, opts.output, peers)
}

func runLeader(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseCommon("leader", args)
	if err != nil {
		return err
	}
	leader, err := fetchLeaderStatus(context.Background(), opts, deps)
	if err != nil {
		return err
	}
	return writeByFormat(stdout, opts.output, leader)
}

func fetchLeaderStatus(ctx context.Context, opts commonOptions, deps Deps) (LeaderStatus, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.metricsURL)
	if err != nil {
		return LeaderStatus{}, err
	}
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, opts.metricsURL, nil)
	if err != nil {
		return LeaderStatus{}, fmt.Errorf("build metrics request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return LeaderStatus{}, fmt.Errorf("GET metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LeaderStatus{}, fmt.Errorf("GET metrics status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return LeaderStatus{}, fmt.Errorf("read metrics: %w", err)
	}
	leader, err := parseLeaderMetrics(string(body))
	if err != nil {
		return LeaderStatus{}, err
	}
	return leader, nil
}

func fetchHealthCheck(ctx context.Context, opts commonOptions, deps Deps) (*Health, Check) {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	health, err := fetchHealth(cctx, opts, deps)
	if err != nil {
		return nil, Check{Name: "admin.health", Status: "fail", Reason: err.Error()}
	}
	if health.Status != "ok" {
		return &health, Check{Name: "admin.health", Status: "fail", Reason: "status=" + health.Status}
	}
	return &health, Check{Name: "admin.health", Status: "ok"}
}

func fetchHealth(ctx context.Context, opts commonOptions, deps Deps) (Health, error) {
	deps, err := withHTTPClientTLS(deps, opts, opts.adminURL)
	if err != nil {
		return Health{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(opts.adminURL, "/")+"/healthz", nil)
	if err != nil {
		return Health{}, fmt.Errorf("build health request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return Health{}, fmt.Errorf("GET healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Health{}, fmt.Errorf("GET healthz status: %d", resp.StatusCode)
	}
	var health Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return Health{}, fmt.Errorf("decode healthz: %w", err)
	}
	return health, nil
}

func parsePeers(out string) ([]Peer, error) {
	var pods podList
	if err := json.Unmarshal([]byte(out), &pods); err != nil {
		return nil, err
	}
	peers := make([]Peer, 0, len(pods.Items))
	for _, pod := range pods.Items {
		peers = append(peers, Peer{Name: pod.Metadata.Name, Ready: podReady(pod)})
	}
	return peers, nil
}

func podReady(pod pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == podReadyType {
			return strings.EqualFold(condition.Status, "true")
		}
	}
	return false
}

type LeaderStatus struct {
	LeaderID int64 `json:"leader_id"`
	IsLeader bool  `json:"is_leader"`
}

func parseLeaderMetrics(metrics string) (LeaderStatus, error) {
	var status LeaderStatus
	var sawIsLeader, sawLeaderID bool
	for _, line := range strings.Split(metrics, "\n") {
		name, value, ok := metricNameAndValue(line)
		if !ok {
			continue
		}
		switch name {
		case "scrap_raft_is_leader", "scrap.raft.is_leader":
			isLeader, err := parseMetricBool(value)
			if err != nil {
				return LeaderStatus{}, fmt.Errorf("parse is leader metric: %w", err)
			}
			status.IsLeader = isLeader
			sawIsLeader = true
		case "scrap_raft_leader_id", "scrap.raft.leader_id":
			leaderID, err := strconv.ParseInt(strings.TrimSuffix(value, ".0"), 10, 64)
			if err != nil {
				return LeaderStatus{}, fmt.Errorf("parse leader id metric: %w", err)
			}
			status.LeaderID = leaderID
			sawLeaderID = true
		}
	}
	if !sawIsLeader || !sawLeaderID {
		return LeaderStatus{}, fmt.Errorf("leader metrics missing: is_leader=%t leader_id=%t", sawIsLeader, sawLeaderID)
	}
	return status, nil
}

func parseMetricBool(value string) (bool, error) {
	normalized := strings.TrimSuffix(value, ".0")
	switch normalized {
	case metricFalse:
		return false, nil
	case metricTrue:
		return true, nil
	default:
		return false, fmt.Errorf("got %q, want 0 or 1", value)
	}
}

func metricNameAndValue(line string) (string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < minMetricFields {
		return "", "", false
	}
	name := fields[0]
	if labelsStart := strings.IndexByte(name, '{'); labelsStart >= 0 {
		name = name[:labelsStart]
	}
	return name, fields[len(fields)-1], true
}

func writeByFormat(stdout io.Writer, output string, value any) error {
	if output == "json" {
		return writeJSON(stdout, value)
	}
	if health, ok := value.(Health); ok {
		return writeHealthText(stdout, health)
	}
	_, err := fmt.Fprintf(stdout, "%+v\n", value)
	if err != nil {
		return fmt.Errorf("write text output: %w", err)
	}
	return nil
}
