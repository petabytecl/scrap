package admin

import (
	"context"
	"strings"
)

const (
	shardDiagnosticLabelMaxBytes = 128
	shardDiagnosticRedacted      = "redacted"

	// ShardDiagnosticsStatusOK means Shard diagnostics were collected successfully.
	ShardDiagnosticsStatusOK = "ok"
	// ShardDiagnosticsStatusDegraded means Shard diagnostics are present but incomplete.
	ShardDiagnosticsStatusDegraded = "degraded"
	// ShardDiagnosticsReasonSnapshotUnavailable is the bounded reason for provider failures.
	ShardDiagnosticsReasonSnapshotUnavailable = "snapshot_unavailable"
)

// ShardDiagnosticsProvider supplies read-only Cell, Member, and Shard diagnostics.
type ShardDiagnosticsProvider interface {
	ShardDiagnosticsSnapshot(context.Context) (ShardDiagnostics, error)
}

// ShardDiagnostics is the admin HTTP status payload for Shard-aware diagnostics.
type ShardDiagnostics struct {
	Status         string            `json:"status,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	CellID         string            `json:"cell_id,omitempty"`
	MemberHostname string            `json:"member_hostname,omitempty"`
	MemberID       string            `json:"member_id,omitempty"`
	Shards         []ShardDiagnostic `json:"shards,omitempty"`
}

// ShardDiagnostic reports bounded read-only state for one Shard.
type ShardDiagnostic struct {
	ShardID               uint64   `json:"shard_id"`
	Membership            string   `json:"membership,omitempty"`
	Routes                []string `json:"routes,omitempty"`
	State                 string   `json:"state,omitempty"`
	Health                string   `json:"health,omitempty"`
	Readiness             string   `json:"readiness,omitempty"`
	LeaderState           string   `json:"leader_state,omitempty"`
	IsLeader              bool     `json:"is_leader"`
	LeaderID              uint64   `json:"leader_id,omitempty"`
	PeerCount             int      `json:"peer_count"`
	PeerHealth            string   `json:"peer_health,omitempty"`
	UploadPressure        string   `json:"upload_pressure,omitempty"`
	UploadPressureLevel   int      `json:"upload_pressure_level,omitempty"`
	UploadPendingBytes    int64    `json:"upload_pending_bytes,omitempty"`
	UploadPendingBlocks   int      `json:"upload_pending_blocks,omitempty"`
	EvictionPressure      string   `json:"eviction_pressure,omitempty"`
	RestoreFailedBlocks   int      `json:"restore_failed_blocks,omitempty"`
	ScannerStatus         string   `json:"scanner_status,omitempty"`
	ScannerLagBlocks      int      `json:"scanner_lag_blocks,omitempty"`
	ScannerInFlightBlocks int      `json:"scanner_in_flight_blocks,omitempty"`
	ScannerLastReason     string   `json:"scanner_last_reason,omitempty"`
	ScannerScannedBlocks  uint64   `json:"scanner_scanned_blocks,omitempty"`
	ScannerFailedBlocks   uint64   `json:"scanner_failed_blocks,omitempty"`
	FailureReason         string   `json:"failure_reason,omitempty"`
}

func cloneShardDiagnostics(in ShardDiagnostics) ShardDiagnostics {
	out := in
	out.Status = boundedShardDiagnosticsStatus(out.Status)
	out.Reason = boundedShardDiagnosticLabel(out.Reason)
	out.CellID = boundedShardDiagnosticLabel(out.CellID)
	out.MemberHostname = boundedShardDiagnosticLabel(out.MemberHostname)
	out.MemberID = boundedShardDiagnosticLabel(out.MemberID)
	out.Shards = make([]ShardDiagnostic, len(in.Shards))
	for i, shard := range in.Shards {
		shard.Membership = boundedShardDiagnosticLabel(shard.Membership)
		shard.Routes = boundedShardDiagnosticLabels(shard.Routes)
		shard.State = boundedShardDiagnosticLabel(shard.State)
		shard.Health = boundedShardDiagnosticLabel(shard.Health)
		shard.Readiness = boundedShardDiagnosticLabel(shard.Readiness)
		shard.LeaderState = boundedShardDiagnosticLabel(shard.LeaderState)
		shard.PeerHealth = boundedShardDiagnosticLabel(shard.PeerHealth)
		shard.UploadPressure = boundedShardDiagnosticLabel(shard.UploadPressure)
		shard.EvictionPressure = boundedShardDiagnosticLabel(shard.EvictionPressure)
		shard.ScannerStatus = boundedShardDiagnosticLabel(shard.ScannerStatus)
		shard.ScannerLastReason = boundedShardDiagnosticLabel(shard.ScannerLastReason)
		shard.FailureReason = boundedShardDiagnosticLabel(shard.FailureReason)
		out.Shards[i] = shard
	}
	return out
}

func boundedShardDiagnosticsStatus(status string) string {
	switch status {
	case "", ShardDiagnosticsStatusOK, ShardDiagnosticsStatusDegraded:
		return status
	default:
		return ShardDiagnosticsStatusDegraded
	}
}

func boundedShardDiagnosticLabels(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = boundedShardDiagnosticLabel(value)
	}
	return out
}

func boundedShardDiagnosticLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if shardDiagnosticLabelLooksSensitive(value) {
		return shardDiagnosticRedacted
	}
	if len(value) > shardDiagnosticLabelMaxBytes {
		return shardDiagnosticRedacted
	}
	for _, r := range value {
		if shardDiagnosticLabelRuneAllowed(r) {
			continue
		}
		return shardDiagnosticRedacted
	}
	return value
}

func shardDiagnosticLabelLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"secret", "private", "token", "password", "key"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shardDiagnosticLabelRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		strings.ContainsRune("_.-", r)
}
