package shard

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/index"
)

// persistProjectionAppliedIndex durably records the applied index in the Pebble
// Projection. It fails (deferring the snapshot) when the projection is being
// rebuilt, rather than skipping the fsync and leaving a stale watermark.
func (s *Shard) persistProjectionAppliedIndex(appliedIndex uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return errors.New("shard: projection unavailable (rebuild in progress)")
	}
	return s.idx.PersistAppliedIndex(appliedIndex)
}

// projectionAppliedIndex returns the durably-persisted projection applied index
// and whether the projection is currently available.
func (s *Shard) projectionAppliedIndex() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return 0, false
	}
	return s.idx.AppliedIndex(), true
}

// copyContentSafetyInto copies the live projection's content-quarantine records
// and scanner watermark into dst (a rebuild-in-progress projection). These have
// no Block-file source, so a projection rebuild must carry them forward or it
// silently rolls back the content-safety controls.
func (s *Shard) copyContentSafetyInto(dst *index.Index) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx == nil {
		return errors.New("shard: projection unavailable (rebuild in progress)")
	}
	if err := s.idx.ForEachContentQuarantine(func(q index.ContentQuarantine) error {
		return dst.PutContentQuarantine(q)
	}); err != nil {
		return err
	}
	watermark, err := s.idx.GetScannerWatermark()
	if errors.Is(err, index.ErrScannerWatermarkNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return dst.PutScannerWatermark(watermark)
}

const (
	// raftSnapshotManifestVersion is written by this build. Version 1 omitted the
	// durable applied index; version 2 records it so restore can detect a
	// projection rolled back below the snapshot (partial DataDir restore).
	raftSnapshotManifestVersion    = 2
	raftSnapshotManifestMinVersion = 1
)

// raftSnapshotManifest is the Raft snapshot payload. Shard state is durably
// applied outside the Raft log (pebble.Sync index writes and fsynced Block
// files), so a snapshot does not carry state — it records which Member's
// durable local state the snapshot stands for and how far that durable state
// has advanced. Restore accepts only a manifest this Member created itself
// (restart replay); a manifest received from another Member means this node
// fell behind the compaction window and must fail closed rather than silently
// diverge.
type raftSnapshotManifest struct {
	Version        int    `json:"version"`
	ShardID        uint64 `json:"shard_id"`
	MemberHostname string `json:"member_hostname"`
	MemberID       string `json:"member_id"`
	CreatedAtUs    int64  `json:"created_at_us"`
	// AppliedIndex is the raft index the projection content durably covered when
	// the snapshot was taken (version 2+).
	AppliedIndex uint64 `json:"applied_index,omitempty"`
}

func (s *Shard) raftSnapshotData(appliedIndex uint64) ([]byte, error) {
	// Durably record how far the projection content has advanced before the
	// snapshot is persisted. A crash between this fsync and the raft snapshot
	// only leaves the watermark ahead of the raft snapshot, which restore treats
	// as covered; the watermark must never trail the content it certifies.
	if err := s.persistProjectionAppliedIndex(appliedIndex); err != nil {
		return nil, err
	}
	manifest := raftSnapshotManifest{
		Version:        raftSnapshotManifestVersion,
		ShardID:        s.shardID,
		MemberHostname: s.memberHostname,
		MemberID:       s.memberID,
		CreatedAtUs:    time.Now().UTC().UnixMicro(),
		AppliedIndex:   appliedIndex,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("shard: marshal raft snapshot manifest: %w", err)
	}
	return data, nil
}

func (s *Shard) restoreRaftSnapshot(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("shard %d: raft snapshot has no manifest", s.shardID)
	}
	var manifest raftSnapshotManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("shard %d: parse raft snapshot manifest: %w", s.shardID, err)
	}
	if manifest.Version < raftSnapshotManifestMinVersion || manifest.Version > raftSnapshotManifestVersion {
		return fmt.Errorf("shard %d: unsupported raft snapshot manifest version %d", s.shardID, manifest.Version)
	}
	if manifest.ShardID != s.shardID {
		return fmt.Errorf("shard %d: raft snapshot manifest is for shard %d", s.shardID, manifest.ShardID)
	}
	if manifest.MemberHostname == s.memberHostname && manifest.MemberID == s.memberID {
		return s.verifySelfSnapshotDurableState(manifest)
	}
	return fmt.Errorf(
		"shard %d: member %s/%s fell behind the raft retention window (install-snapshot from %s/%s); "+
			"re-seed this member's shard state (replica repair) before rejoining",
		s.shardID, s.memberHostname, s.memberID, manifest.MemberHostname, manifest.MemberID,
	)
}

// verifySelfSnapshotDurableState accepts a self-created snapshot only when the
// projection's durably-persisted applied index still covers the snapshot's
// index. A partial DataDir restore that leaves the raft snapshot/WAL newer than
// the projection self-matches on Member identity but has a projection applied
// index below the snapshot's; accepting it would leave the entries between the
// two unreplayed while the Shard rejoins as healthy. Version 1 manifests carry
// no applied index (AppliedIndex 0) and keep the legacy accept-on-identity
// behavior.
func (s *Shard) verifySelfSnapshotDurableState(manifest raftSnapshotManifest) error {
	durable, ok := s.projectionAppliedIndex()
	if !ok {
		return fmt.Errorf("shard %d: projection unavailable while restoring raft snapshot", s.shardID)
	}
	if durable < manifest.AppliedIndex {
		return fmt.Errorf(
			"shard %d: projection applied index %d is behind self snapshot index %d "+
				"(partial DataDir restore); re-seed this member's shard state (replica repair) before rejoining",
			s.shardID, durable, manifest.AppliedIndex,
		)
	}
	return nil
}
