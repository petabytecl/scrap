package raft

// Crash-window regression tests for the install-snapshot persistence
// contract (#462): persist order inside one Ready, recovery with orphaned
// snap files, and a rejected foreign snapshot leaving the disk clean.

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type recordingWAL struct {
	calls *[]string
}

func (w recordingWAL) Save(_ raftpb.HardState, _ []raftpb.Entry) error {
	*w.calls = append(*w.calls, "wal.Save")
	return nil
}

func (w recordingWAL) SaveSnapshot(_ walpb.Snapshot) error {
	*w.calls = append(*w.calls, "wal.SaveSnapshot")
	return nil
}

func (w recordingWAL) ReleaseLockTo(_ uint64) error {
	*w.calls = append(*w.calls, "wal.ReleaseLockTo")
	return nil
}

func (w recordingWAL) Close() error { return nil }

type recordingSnapStore struct {
	calls *[]string
}

func (s recordingSnapStore) SaveSnap(_ raftpb.Snapshot) error {
	*s.calls = append(*s.calls, "snap.SaveSnap")
	return nil
}

func (s recordingSnapStore) LoadNewestAvailable(_ []walpb.Snapshot) (*raftpb.Snapshot, error) {
	return nil, snap.ErrNoSnapshot
}

func installSnapshotReady(index uint64) raft.Ready {
	return raft.Ready{
		HardState: raftpb.HardState{Term: 2, Vote: 1, Commit: index},
		Snapshot: raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index:     index,
				Term:      2,
				ConfState: raftpb.ConfState{Voters: []uint64{1}},
			},
			Data: []byte(snapshotTestManifest),
		},
	}
}

// The etcd contract: within one Ready, an incoming snapshot must be durable
// (snap file, then WAL snapshot record) BEFORE wal.Save persists the
// HardState referencing it, and the application validates (Restore) before
// anything is persisted. The old order saved the WAL first — a crash between
// the two bricked the member (#462).
func TestProcessReadyPersistsIncomingSnapshotBeforeWALSave(t *testing.T) {
	var calls []string
	n := &Node{
		cfg: Config{
			Apply: func(_ []raftpb.Entry, _ uint64) error { return nil },
			Restore: func(_ []byte) error {
				calls = append(calls, "restore")
				return nil
			},
		},
		node:      &confChangeRecorderNode{},
		storage:   raft.NewMemoryStorage(),
		wal:       recordingWAL{calls: &calls},
		snap:      recordingSnapStore{calls: &calls},
		transport: discardTransport{},
		readMap:   make(map[string]chan uint64),
	}

	n.processReady(installSnapshotReady(10))

	want := []string{"restore", "snap.SaveSnap", "wal.SaveSnapshot", "wal.ReleaseLockTo", "wal.Save"}
	if len(calls) != len(want) {
		t.Fatalf("persistence calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("persistence call %d = %q, want %q (full order %v)", i, calls[i], want[i], calls)
		}
	}
	if got := n.AppliedIndex(); got != 10 {
		t.Fatalf("AppliedIndex after install-snapshot = %d, want 10", got)
	}
}

// A crash between snap.SaveSnap and wal.SaveSnapshot leaves the newest .snap
// file orphaned (no WAL record). Recovery must skip it and boot from the
// last WAL-recorded snapshot; the old snap.Load loader trusted the newest
// file and failed wal.Open with ErrSnapshotNotFound forever (#462).
func TestRestartSkipsOrphanedNewerSnapFile(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64
	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 40)
	waitForCondition(t, "snapshot file", func() bool {
		return len(snapDirFiles(t, dataDir)) > 0
	})
	node.Stop()

	orphan := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index:     lastApplied.Load() + 100,
			Term:      99,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
		Data: []byte("orphaned snap file"),
	}
	if err := snap.New(zap.NewNop(), filepath.Join(dataDir, "snap")).SaveSnap(orphan); err != nil {
		t.Fatalf("plant orphaned snapshot: %v", err)
	}

	reopened, err := Open(Config{
		ID:           1,
		Peers:        map[uint64]string{1: ""},
		DataDir:      dataDir,
		Transport:    discardTransport{},
		TickInterval: 5 * time.Millisecond,
		Apply:        func(_ []raftpb.Entry, _ uint64) error { return nil },
		Snapshot:     func(uint64) ([]byte, error) { return []byte(snapshotTestManifest), nil },
		Restore: func(data []byte) error {
			if string(data) == "orphaned snap file" {
				return errors.New("restored the orphaned snapshot; recovery must skip it")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Open with orphaned newer .snap: %v", err)
	}
	reopened.Stop()
}

// A foreign install-snapshot the application rejects must fail BEFORE any of
// it is persisted: the panic is still fatal (silent divergence is never
// acceptable), but the member restarts at its previous state. The old order
// persisted first, so the rejection recurred from disk at every startup —
// a bricked voter with no re-seed path (#462).
func TestRejectedInstallSnapshotLeavesDiskCleanAndRestartable(t *testing.T) {
	dataDir := t.TempDir()
	walDir := filepath.Join(dataDir, "wal")
	snapDir := filepath.Join(dataDir, "snap")
	for _, d := range []string{walDir, snapDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	w, err := wal.Create(zap.NewNop(), walDir, nil)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
		{Index: 2, Term: 2, Type: raftpb.EntryNormal, Data: []byte("committed-1")},
		{Index: 3, Term: 2, Type: raftpb.EntryNormal, Data: []byte("committed-2")},
	}
	if err := w.Save(raftpb.HardState{Term: 2, Vote: 1, Commit: 3}, entries); err != nil {
		t.Fatalf("save wal: %v", err)
	}

	rejectRestore := func(_ []byte) error { return errors.New("foreign manifest; re-seed required") }
	n := &Node{
		cfg: Config{
			Apply:   func(_ []raftpb.Entry, _ uint64) error { return nil },
			Restore: rejectRestore,
		},
		node:      &confChangeRecorderNode{},
		storage:   raft.NewMemoryStorage(),
		wal:       w,
		snap:      snap.New(zap.NewNop(), snapDir),
		transport: discardTransport{},
		readMap:   make(map[string]chan uint64),
	}

	if !panics(func() { n.processReady(installSnapshotReady(10)) }) {
		t.Fatal("rejected install-snapshot did not panic; divergence must be fatal")
	}

	// Nothing of the rejected snapshot may be durable.
	if files := snapDirFiles(t, dataDir); len(files) != 0 {
		t.Fatalf("snap files after rejection = %v, want none", files)
	}
	_ = w.Close() // the crash releases the WAL locks; simulate it

	// The member restarts at its previous state — even with the same
	// rejecting Restore hook (the old order bricked here).
	reopened, err := Open(Config{
		ID:        1,
		Peers:     map[uint64]string{1: ""},
		DataDir:   dataDir,
		Transport: discardTransport{},
		Apply:     func(_ []raftpb.Entry, _ uint64) error { return nil },
		Restore:   rejectRestore,
	})
	if err != nil {
		t.Fatalf("Open after rejected install-snapshot: %v", err)
	}
	defer reopened.Stop()
	if got := reopened.CommitIndex(); got != 3 {
		t.Fatalf("CommitIndex after restart = %d, want 3 (pre-snapshot state)", got)
	}
}

func panics(fn func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	fn()
	return false
}
