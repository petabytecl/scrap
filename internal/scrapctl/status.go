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
	minPeerFields   = 2
	minMetricFields = 2
)

type Health struct {
	Status              string `json:"status"`
	UploadPressure      string `json:"upload_pressure"`
	UploadPressureLevel int    `json:"upload_pressure_level"`
	UploadPendingBytes  int64  `json:"upload_pending_bytes"`
	UploadPendingBlocks int    `json:"upload_pending_blocks"`
}

type Peer struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
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
	out, err := deps.Runner.Run(cctx, opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "pods", "-l", "app=scrap", "-o", "jsonpath={range .items[*]}{.metadata.name}{\" \"}{.status.containerStatuses[0].ready}{\"\\n\"}{end}")...)
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	peers := parsePeers(out)
	return writeByFormat(stdout, opts.output, peers)
}

func runLeader(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseCommon("leader", args)
	if err != nil {
		return err
	}
	cctx, cancel := commandContext(context.Background(), opts.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, opts.metricsURL, nil)
	if err != nil {
		return fmt.Errorf("build metrics request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET metrics status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read metrics: %w", err)
	}
	leader := parseLeaderMetrics(string(body))
	return writeByFormat(stdout, opts.output, leader)
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

func parsePeers(out string) []Peer {
	var peers []Peer
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < minPeerFields {
			continue
		}
		peers = append(peers, Peer{Name: fields[0], Ready: fields[1] == "True" || fields[1] == "true"})
	}
	return peers
}

type LeaderStatus struct {
	LeaderID int64 `json:"leader_id"`
	IsLeader bool  `json:"is_leader"`
}

func parseLeaderMetrics(metrics string) LeaderStatus {
	var status LeaderStatus
	for _, line := range strings.Split(metrics, "\n") {
		name, value, ok := metricNameAndValue(line)
		if !ok {
			continue
		}
		switch name {
		case "scrap_raft_is_leader", "scrap.raft.is_leader":
			status.IsLeader = value == "1" || value == "1.0"
		case "scrap_raft_leader_id", "scrap.raft.leader_id":
			if leaderID, err := strconv.ParseInt(strings.TrimSuffix(value, ".0"), 10, 64); err == nil {
				status.LeaderID = leaderID
			}
		}
	}
	return status
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
	_, err := fmt.Fprintf(stdout, "%+v\n", value)
	if err != nil {
		return fmt.Errorf("write text output: %w", err)
	}
	return nil
}
