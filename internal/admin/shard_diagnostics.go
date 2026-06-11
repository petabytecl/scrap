package admin

import "context"

const (
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
	ShardID             uint64   `json:"shard_id"`
	Membership          string   `json:"membership,omitempty"`
	Routes              []string `json:"routes,omitempty"`
	State               string   `json:"state,omitempty"`
	Health              string   `json:"health,omitempty"`
	Readiness           string   `json:"readiness,omitempty"`
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

func cloneShardDiagnostics(in ShardDiagnostics) ShardDiagnostics {
	out := in
	out.Shards = make([]ShardDiagnostic, len(in.Shards))
	for i, shard := range in.Shards {
		shard.Routes = append([]string(nil), shard.Routes...)
		out.Shards[i] = shard
	}
	return out
}
