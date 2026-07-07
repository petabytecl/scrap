package raft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

const snapshotTestManifest = `{"member":"member-a"}`

func TestNodeSnapshotsAndCompactsAfterThreshold(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied atomic.Uint64
	var snapshotCalls atomic.Uint64

	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()

	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 40)

	waitForCondition(t, "snapshot file", func() bool {
		return len(snapDirFiles(t, dataDir)) > 0
	})
	if snapshotCalls.Load() == 0 {
		t.Fatal("Snapshot func was never called")
	}

	node.Stop()
	firstIndex, err := node.storage.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if firstIndex <= 1 {
		t.Fatalf("FirstIndex = %d, want compacted log (> 1)", firstIndex)
	}
	if count := len(snapDirFiles(t, dataDir)); count > maxKeptObsoleteFiles+1 {
		t.Fatalf("snap files = %d, want purge to keep at most %d", count, maxKeptObsoleteFiles+1)
	}
}

func TestNodeRestartsFromOwnSnapshotAndReplaysOnlyTail(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied atomic.Uint64
	var snapshotCalls atomic.Uint64

	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 40)
	waitForCondition(t, "snapshot file", func() bool {
		return len(snapDirFiles(t, dataDir)) > 0
	})
	node.Stop()

	var restored atomic.Bool
	var replayUntilSeen atomic.Uint64
	restarted, err := Open(Config{
		ID:           1,
		Peers:        map[uint64]string{1: ""},
		DataDir:      dataDir,
		Transport:    discardTransport{},
		TickInterval: 5 * time.Millisecond,
		Apply: func(entries []raftpb.Entry, replayUntil uint64) error {
			replayUntilSeen.Store(replayUntil)
			if len(entries) > 0 {
				lastApplied.Store(entries[len(entries)-1].Index)
			}
			return nil
		},
		Snapshot: func(uint64) ([]byte, error) { return []byte(snapshotTestManifest), nil },
		Restore: func(data []byte) error {
			if string(data) != snapshotTestManifest {
				t.Errorf("Restore data = %q, want manifest", string(data))
			}
			restored.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer restarted.Stop()

	if !restored.Load() {
		t.Fatal("Restore was not invoked on restart with a snapshot present")
	}
	if restarted.AppliedIndex() == 0 {
		t.Fatal("restarted node applied index = 0, want snapshot index")
	}
}

func TestNodeSnapshotDataFailureDefersSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied atomic.Uint64

	node, err := Open(Config{
		ID:                     1,
		Peers:                  map[uint64]string{1: ""},
		DataDir:                dataDir,
		Transport:              discardTransport{},
		TickInterval:           5 * time.Millisecond,
		MaxSnapCount:           8,
		SnapshotCatchUpEntries: 4,
		Apply: func(entries []raftpb.Entry, _ uint64) error {
			if len(entries) > 0 {
				lastApplied.Store(entries[len(entries)-1].Index)
			}
			return nil
		},
		Snapshot: func(uint64) ([]byte, error) { return nil, os.ErrDeadlineExceeded },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer node.Stop()

	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 20)

	// The node keeps running and never persists a snapshot.
	if files := snapDirFiles(t, dataDir); len(files) != 0 {
		t.Fatalf("snap files = %v, want none when Snapshot fails", files)
	}
	firstIndex, err := node.storage.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if firstIndex != 1 {
		t.Fatalf("FirstIndex = %d, want uncompacted log when Snapshot fails", firstIndex)
	}
}

func openSnapshotTestNode(t *testing.T, dataDir string, lastApplied, snapshotCalls *atomic.Uint64) *Node {
	t.Helper()
	node, err := Open(Config{
		ID:                     1,
		Peers:                  map[uint64]string{1: ""},
		DataDir:                dataDir,
		Transport:              discardTransport{},
		TickInterval:           5 * time.Millisecond,
		MaxSnapCount:           8,
		SnapshotCatchUpEntries: 4,
		Apply: func(entries []raftpb.Entry, _ uint64) error {
			if len(entries) > 0 {
				lastApplied.Store(entries[len(entries)-1].Index)
			}
			return nil
		},
		Snapshot: func(uint64) ([]byte, error) {
			snapshotCalls.Add(1)
			return []byte(snapshotTestManifest), nil
		},
		Restore: func([]byte) error { return nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return node
}

func waitForLeader(t *testing.T, node *Node) {
	t.Helper()
	waitForCondition(t, "leader election", node.IsLeader)
}

func proposeUntilApplied(t *testing.T, node *Node, lastApplied *atomic.Uint64, proposals int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range proposals {
		if err := node.Propose(ctx, []byte("snapshot-test-entry")); err != nil {
			t.Fatalf("Propose %d: %v", i, err)
		}
	}
	target := node.CommitIndex()
	waitForCondition(t, "proposals applied", func() bool {
		return lastApplied.Load() >= target
	})
}

func waitForCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func snapDirFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "snap"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read snap dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".snap") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func writeStaleSnapArtifacts(t *testing.T, dataDir string, count int) {
	t.Helper()
	snapDir := filepath.Join(dataDir, "snap")
	if err := os.MkdirAll(snapDir, 0o750); err != nil {
		t.Fatalf("mkdir snap dir: %v", err)
	}
	for i := range count {
		for _, suffix := range []string{".snap.broken", ".snap.db"} {
			name := filepath.Join(snapDir, fmt.Sprintf("%016x-%016x%s", i, i, suffix))
			if err := os.WriteFile(name, []byte("stale"), 0o600); err != nil {
				t.Fatalf("write stale artifact: %v", err)
			}
		}
	}
}

func snapDirFilesWithSuffix(t *testing.T, dataDir, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "snap"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read snap dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), suffix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// Regression for #457: .snap.broken files are produced by the snapshot
// loader at restart, so they must be reclaimed at startup, not only when the
// next snapshot happens to be created.
func TestNodeStartupPurgesStaleSnapshotArtifacts(t *testing.T) {
	dataDir := t.TempDir()
	writeStaleSnapArtifacts(t, dataDir, maxKeptObsoleteFiles+3)

	var lastApplied, snapshotCalls atomic.Uint64
	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()

	for _, suffix := range []string{".snap.broken", ".snap.db"} {
		if got := len(snapDirFilesWithSuffix(t, dataDir, suffix)); got != maxKeptObsoleteFiles {
			t.Fatalf("%s files after startup = %d, want %d", suffix, got, maxKeptObsoleteFiles)
		}
	}
}

// Regression for #457: the snapshot-creation purge must cover .snap.broken
// and .snap.db artifacts, not only .snap files.
func TestNodeSnapshotPurgeCoversBrokenAndDBArtifacts(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64

	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()
	waitForLeader(t, node)

	// Artifacts appear after startup; only the snapshot-time purge can see them.
	writeStaleSnapArtifacts(t, dataDir, maxKeptObsoleteFiles+3)
	proposeUntilApplied(t, node, &lastApplied, 40)
	waitForCondition(t, "snapshot file", func() bool {
		return len(snapDirFiles(t, dataDir)) > 0
	})

	waitForCondition(t, "stale artifact purge", func() bool {
		return len(snapDirFilesWithSuffix(t, dataDir, ".snap.broken")) == maxKeptObsoleteFiles &&
			len(snapDirFilesWithSuffix(t, dataDir, ".snap.db")) == maxKeptObsoleteFiles
	})
}

// Regression for the #487 review finding: the startup purge must never
// delete .snap files — well-formed orphan snapshots newer than the
// WAL-backed one (SaveSnap-then-crash leftovers) would otherwise crowd the
// WAL-backed file out of the keep window and strand the next restart.
func TestNodeStartupPurgeKeepsAllSnapFiles(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64

	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 40)
	waitForCondition(t, "snapshot file", func() bool {
		return len(snapDirFiles(t, dataDir)) > 0
	})
	node.Stop()
	walBacked := snapDirFiles(t, dataDir)

	// Fabricate SaveSnap-then-crash orphans: well-formed snapshot files at
	// higher indexes with no WAL snapshot record, more than the keep window.
	snapshotter := snap.New(zap.NewNop(), filepath.Join(dataDir, "snap"))
	for i := range maxKeptObsoleteFiles + 2 {
		orphan := raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: uint64(i) + 1_000_000,
				Term:  99,
			},
			Data: []byte(snapshotTestManifest),
		}
		if err := snapshotter.SaveSnap(orphan); err != nil {
			t.Fatalf("SaveSnap orphan: %v", err)
		}
	}
	before := len(snapDirFiles(t, dataDir))

	restarted := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer restarted.Stop()

	if after := len(snapDirFiles(t, dataDir)); after != before {
		t.Fatalf("snap files after restart = %d, want %d (startup purge must not touch .snap)", after, before)
	}
	for _, name := range walBacked {
		if _, err := os.Stat(filepath.Join(dataDir, "snap", name)); err != nil {
			t.Fatalf("WAL-backed snapshot %s missing after restart: %v", name, err)
		}
	}
}
