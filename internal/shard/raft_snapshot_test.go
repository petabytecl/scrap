package shard

import (
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func newRaftSnapshotTestShard(t *testing.T, shardID uint64, host, member string) *Shard {
	t.Helper()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return &Shard{shardID: shardID, memberHostname: host, memberID: member, idx: idx}
}

func TestRaftSnapshotManifestRoundTripsForOwnMember(t *testing.T) {
	s := newRaftSnapshotTestShard(t, 7, "scrapd-0", "member-a")

	data, err := s.raftSnapshotData(42)
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}
	// The projection durably covers the snapshot index, so restore accepts it.
	if err := s.restoreRaftSnapshot(data); err != nil {
		t.Fatalf("restoreRaftSnapshot of own manifest: %v", err)
	}
}

func TestRestoreRaftSnapshotFailsClosedOnPartialRestore(t *testing.T) {
	s := newRaftSnapshotTestShard(t, 7, "scrapd-0", "member-a")

	data, err := s.raftSnapshotData(100)
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}

	// Simulate a partial DataDir restore: the raft snapshot/WAL stay at index 100
	// but the projection is rolled back below it.
	if err := s.idx.PersistAppliedIndex(50); err != nil {
		t.Fatalf("PersistAppliedIndex: %v", err)
	}

	err = s.restoreRaftSnapshot(data)
	if err == nil {
		t.Fatal("restoreRaftSnapshot accepted a self snapshot ahead of durable projection state")
	}
	if !strings.Contains(err.Error(), "partial DataDir restore") {
		t.Fatalf("restoreRaftSnapshot error = %v, want partial-restore guidance", err)
	}
}

func TestRestoreRaftSnapshotAcceptsLegacyV1Manifest(t *testing.T) {
	s := newRaftSnapshotTestShard(t, 7, "scrapd-0", "member-a")

	// Version 1 manifests carry no applied index; they keep the accept-on-identity
	// behavior so a snapshot written by an older build still restores.
	legacy := `{"version":1,"shard_id":7,"member_hostname":"scrapd-0","member_id":"member-a","created_at_us":1}`
	if err := s.restoreRaftSnapshot([]byte(legacy)); err != nil {
		t.Fatalf("restoreRaftSnapshot of legacy v1 manifest: %v", err)
	}
}

func TestRestoreRaftSnapshotFailsClosedForForeignMember(t *testing.T) {
	leader := newRaftSnapshotTestShard(t, 7, "scrapd-1", "member-b")
	data, err := leader.raftSnapshotData(10)
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}

	follower := newRaftSnapshotTestShard(t, 7, "scrapd-0", "member-a")
	err = follower.restoreRaftSnapshot(data)
	if err == nil {
		t.Fatal("restoreRaftSnapshot accepted a foreign manifest, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "re-seed") {
		t.Fatalf("restoreRaftSnapshot error = %v, want re-seed guidance", err)
	}
}

func TestRestoreRaftSnapshotRejectsWrongShardAndEmptyManifest(t *testing.T) {
	other := newRaftSnapshotTestShard(t, 9, "scrapd-0", "member-a")
	data, err := other.raftSnapshotData(5)
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}

	s := newRaftSnapshotTestShard(t, 7, "scrapd-0", "member-a")
	if err := s.restoreRaftSnapshot(data); err == nil {
		t.Fatal("restoreRaftSnapshot accepted a manifest for another shard")
	}
	if err := s.restoreRaftSnapshot(nil); err == nil {
		t.Fatal("restoreRaftSnapshot accepted an empty manifest")
	}
	if err := s.restoreRaftSnapshot([]byte("{")); err == nil {
		t.Fatal("restoreRaftSnapshot accepted a malformed manifest")
	}
}
