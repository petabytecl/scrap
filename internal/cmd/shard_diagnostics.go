package cmd

import (
	"context"
	"errors"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	shardDiagnosticsStateNotLocal     = "not_local"
	shardDiagnosticsHealthOK          = "ok"
	shardDiagnosticsHealthDegraded    = "degraded"
	shardDiagnosticsReadinessReady    = "ready"
	shardDiagnosticsReadinessNotReady = "not_ready"
	shardDiagnosticsLeaderLeader      = "leader"
	shardDiagnosticsLeaderFollower    = "follower"
	shardDiagnosticsLeaderUnknown     = "unknown"
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

type shardDiagnosticsSource interface {
	StartupStatus(startupTopology) []startupShardStatus
	diagnosticsTarget(shardID uint64) (shardDiagnosticsTarget, bool)
}

type appShardDiagnosticsProvider struct {
	cellID    string
	member    scrapdMemberIdentity
	topology  startupTopology
	shards    shardDiagnosticsSource
	peerCount int
}

func newAppShardDiagnosticsProvider(
	cfg Config,
	member scrapdMemberIdentity,
	topology startupTopology,
	shards *shardSet,
	peers map[uint64]string,
) admin.ShardDiagnosticsProvider {
	var source shardDiagnosticsSource
	if shards != nil {
		source = shards
	}
	return appShardDiagnosticsProvider{
		cellID:    cfg.CellID,
		member:    member,
		topology:  topology,
		shards:    source,
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
			ShardID:     status.ShardID,
			Membership:  status.Membership,
			Routes:      append([]string(nil), status.Routes...),
			State:       status.State,
			Health:      shardDiagnosticsStateNotLocal,
			Readiness:   shardDiagnosticsStateNotLocal,
			LeaderState: shardDiagnosticsStateNotLocal,
			PeerCount:   p.peerCount,
			PeerHealth:  shardDiagnosticsPeerConfigured,
		}
		if status.FailureCategory != "" {
			shardDiag.FailureReason = status.FailureCategory
		}
		if target, ok := p.shards.diagnosticsTarget(status.ShardID); ok {
			applyLiveShardDiagnostics(ctx, target, &shardDiag)
		}
		if shardDiagnosticDegradesAggregate(shardDiag) {
			diagnostics.Status = admin.ShardDiagnosticsStatusDegraded
		}
		diagnostics.Shards = append(diagnostics.Shards, shardDiag)
	}
	return diagnostics, nil
}

func shardDiagnosticDegradesAggregate(diag admin.ShardDiagnostic) bool {
	return diag.Membership == "local" && diag.Health != shardDiagnosticsHealthOK
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
		markShardDiagnosticNotReady(diag, readinessFailureReason(err))
	}
	diag.IsLeader = target.IsLeader()
	diag.LeaderID = target.LeaderID()
	switch {
	case diag.IsLeader:
		diag.LeaderState = shardDiagnosticsLeaderLeader
	case diag.LeaderID != 0:
		diag.LeaderState = shardDiagnosticsLeaderFollower
	default:
		diag.LeaderState = shardDiagnosticsLeaderUnknown
		markShardDiagnosticDegraded(diag, shardDiagnosticsReasonNoLeader)
	}
	level, levelName, pendingBytes, pendingBlocks := target.UploadPressureSnapshot()
	diag.UploadPressureLevel = level
	diag.UploadPressure = levelName
	diag.UploadPendingBytes = pendingBytes
	diag.UploadPendingBlocks = pendingBlocks
	if diag.UploadPressure == "" {
		diag.UploadPressure = shardDiagnosticsHealthOK
	}
	if level >= int(shard.UploadPressureLevelPressure) {
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
	if diag.FailureReason == "" {
		diag.FailureReason = reason
	}
}

func markShardDiagnosticNotReady(diag *admin.ShardDiagnostic, reason string) {
	markShardDiagnosticDegraded(diag, reason)
	diag.Readiness = shardDiagnosticsReadinessNotReady
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
