package shard

import (
	"strings"
	"testing"
)

func TestRaftSnapshotManifestRoundTripsForOwnMember(t *testing.T) {
	s := &Shard{shardID: 7, memberHostname: "scrapd-0", memberID: "member-a"}

	data, err := s.raftSnapshotData()
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}
	if err := s.restoreRaftSnapshot(data); err != nil {
		t.Fatalf("restoreRaftSnapshot of own manifest: %v", err)
	}
}

func TestRestoreRaftSnapshotFailsClosedForForeignMember(t *testing.T) {
	leader := &Shard{shardID: 7, memberHostname: "scrapd-1", memberID: "member-b"}
	data, err := leader.raftSnapshotData()
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}

	follower := &Shard{shardID: 7, memberHostname: "scrapd-0", memberID: "member-a"}
	err = follower.restoreRaftSnapshot(data)
	if err == nil {
		t.Fatal("restoreRaftSnapshot accepted a foreign manifest, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "re-seed") {
		t.Fatalf("restoreRaftSnapshot error = %v, want re-seed guidance", err)
	}
}

func TestRestoreRaftSnapshotRejectsWrongShardAndEmptyManifest(t *testing.T) {
	other := &Shard{shardID: 9, memberHostname: "scrapd-0", memberID: "member-a"}
	data, err := other.raftSnapshotData()
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}

	s := &Shard{shardID: 7, memberHostname: "scrapd-0", memberID: "member-a"}
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
