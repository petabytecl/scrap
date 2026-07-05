package shard

import (
	"encoding/json"
	"fmt"
	"time"
)

const raftSnapshotManifestVersion = 1

// raftSnapshotManifest is the Raft snapshot payload. Shard state is durably
// applied outside the Raft log (pebble.Sync index writes and fsynced Block
// files), so a snapshot does not carry state — it records which Member's
// durable local state the snapshot stands for. Restore accepts only a
// manifest this Member created itself (restart replay); a manifest received
// from another Member means this node fell behind the compaction window and
// must fail closed rather than silently diverge.
type raftSnapshotManifest struct {
	Version        int    `json:"version"`
	ShardID        uint64 `json:"shard_id"`
	MemberHostname string `json:"member_hostname"`
	MemberID       string `json:"member_id"`
	CreatedAtUs    int64  `json:"created_at_us"`
}

func (s *Shard) raftSnapshotData() ([]byte, error) {
	manifest := raftSnapshotManifest{
		Version:        raftSnapshotManifestVersion,
		ShardID:        s.shardID,
		MemberHostname: s.memberHostname,
		MemberID:       s.memberID,
		CreatedAtUs:    time.Now().UTC().UnixMicro(),
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
	if manifest.Version != raftSnapshotManifestVersion {
		return fmt.Errorf("shard %d: unsupported raft snapshot manifest version %d", s.shardID, manifest.Version)
	}
	if manifest.ShardID != s.shardID {
		return fmt.Errorf("shard %d: raft snapshot manifest is for shard %d", s.shardID, manifest.ShardID)
	}
	if manifest.MemberHostname == s.memberHostname && manifest.MemberID == s.memberID {
		// Self-created snapshot: every entry it covers was durably applied to
		// this Member's pebble index and Block files before the snapshot was
		// taken, so there is nothing to restore.
		return nil
	}
	return fmt.Errorf(
		"shard %d: member %s/%s fell behind the raft retention window (install-snapshot from %s/%s); "+
			"re-seed this member's shard state (replica repair) before rejoining",
		s.shardID, s.memberHostname, s.memberID, manifest.MemberHostname, manifest.MemberID,
	)
}
