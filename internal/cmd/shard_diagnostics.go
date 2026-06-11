package cmd

import (
	"context"
	"errors"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	shardDiagnosticsStateNotLocal     = "not_local"
	shardDiagnosticsHealthOK          = "ok"
	shardDiagnosticsHealthDegraded    = "degraded"
	shardDiagnosticsReadinessReady    = "ready"
	shardDiagnosticsReadinessNotReady = "not_ready"
	shardDiagnosticsPeerConfigured    = "configured"
	shardDiagnosticsReasonRebuilding  = "rebuilding"
	shardDiagnosticsReasonNoLeader    = "no_leader"
	shardDiagnosticsReasonReadiness   = "readiness_failed"
	shardDiagnosticsReasonContext     = "context_canceled"
	shardDiagnosticsReasonSnapshot    = "snapshot_unavailable"
)

var errShardDiagnosticsUnavailable = errors.New("shard diagnostics unavailable")

type shardDiagnosticsTarget interface {
	CheckReadiness(context.Context) error
	IsLeader() bool
	LeaderID() uint64
	UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int)
	EvictionHealthSnapshot(context.Context) (eviction.HealthSnapshot, error)
}

type appShardDiagnosticsProvider struct {
	cellID    string
	member    scrapdMemberIdentity
	topology  startupTopology
	shards    *shardSet
	peerCount int
}

func newAppShardDiagnosticsProvider(
	cfg Config,
	member scrapdMemberIdentity,
	topology startupTopology,
	shards *shardSet,
	peers map[uint64]string,
) admin.ShardDiagnosticsProvider {
	return appShardDiagnosticsProvider{
		cellID:    cfg.CellID,
		member:    member,
		topology:  topology,
		shards:    shards,
		peerCount: len(peers),
	}
}

func (p appShardDiagnosticsProvider) ShardDiagnosticsSnapshot(ctx context.Context) (admin.ShardDiagnostics, error) {
	if p.shards == nil {
		return admin.ShardDiagnostics{}, errShardDiagnosticsUnavailable
	}
	diagnostics := admin.ShardDiagnostics{
		Status:         admin.ShardDiagnosticsStatusOK,
		CellID:         p.cellID,
		MemberHostname: p.member.MemberHostname,
		MemberID:       p.member.MemberID,
	}
	for _, status := range p.shards.StartupStatus(p.topology) {
		shardDiag := admin.ShardDiagnostic{
			ShardID:    status.ShardID,
			Membership: status.Membership,
			Routes:     append([]string(nil), status.Routes...),
			State:      status.State,
			Health:     shardDiagnosticsStateNotLocal,
			Readiness:  shardDiagnosticsStateNotLocal,
			PeerCount:  p.peerCount,
			PeerHealth: shardDiagnosticsPeerConfigured,
		}
		if status.FailureCategory != "" {
			shardDiag.FailureReason = status.FailureCategory
		}
		if target, ok := p.shards.diagnosticsTarget(status.ShardID); ok {
			applyLiveShardDiagnostics(ctx, target, &shardDiag)
		}
		if shardDiag.Health != shardDiagnosticsHealthOK {
			diagnostics.Status = admin.ShardDiagnosticsStatusDegraded
		}
		diagnostics.Shards = append(diagnostics.Shards, shardDiag)
	}
	return diagnostics, nil
}

func (s *shardSet) diagnosticsTarget(shardID uint64) (shardDiagnosticsTarget, bool) {
	if s == nil {
		return nil, false
	}
	target, ok := s.shards[shardID]
	return target, ok
}

func applyLiveShardDiagnostics(ctx context.Context, target shardDiagnosticsTarget, diag *admin.ShardDiagnostic) {
	diag.Health = shardDiagnosticsHealthOK
	diag.Readiness = shardDiagnosticsReadinessReady
	if err := target.CheckReadiness(ctx); err != nil {
		markShardDiagnosticDegraded(diag, readinessFailureReason(err))
	}
	diag.IsLeader = target.IsLeader()
	diag.LeaderID = target.LeaderID()
	level, levelName, pendingBytes, pendingBlocks := target.UploadPressureSnapshot()
	diag.UploadPressureLevel = level
	diag.UploadPressure = levelName
	diag.UploadPendingBytes = pendingBytes
	diag.UploadPendingBlocks = pendingBlocks
	if diag.UploadPressure == "" {
		diag.UploadPressure = shardDiagnosticsHealthOK
	}
	if diag.UploadPressure != shardDiagnosticsHealthOK {
		markShardDiagnosticDegraded(diag, diag.UploadPressure)
	}
	evictionHealth, err := target.EvictionHealthSnapshot(ctx)
	if err != nil {
		diag.EvictionPressure = eviction.HealthPressureDegraded
		markShardDiagnosticDegraded(diag, shardDiagnosticsReasonSnapshot)
		return
	}
	diag.EvictionPressure = evictionHealth.Pressure
	if diag.EvictionPressure == "" {
		diag.EvictionPressure = eviction.HealthPressureOK
	}
	diag.RestoreFailedBlocks = evictionHealth.RestoreFailedBlocks
	if diag.EvictionPressure != eviction.HealthPressureOK {
		markShardDiagnosticDegraded(diag, diag.EvictionPressure)
	}
}

func markShardDiagnosticDegraded(diag *admin.ShardDiagnostic, reason string) {
	diag.Health = shardDiagnosticsHealthDegraded
	diag.Readiness = shardDiagnosticsReadinessNotReady
	if diag.FailureReason == "" {
		diag.FailureReason = reason
	}
}

func readinessFailureReason(err error) string {
	switch {
	case errors.Is(err, storeapi.ErrRebuilding):
		return shardDiagnosticsReasonRebuilding
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return shardDiagnosticsReasonContext
	case err != nil:
		return shardDiagnosticsReasonReadiness
	default:
		return shardDiagnosticsReasonNoLeader
	}
}
