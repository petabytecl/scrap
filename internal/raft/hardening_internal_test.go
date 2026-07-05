package raft

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

// confChangeRecorderNode stubs the raft.Node methods processReadyLocked
// touches so the test can observe ApplyConfChange calls.
type confChangeRecorderNode struct {
	raft.Node
	confChanges []raftpb.ConfChangeV2
	advanced    int
}

func (n *confChangeRecorderNode) ApplyConfChange(cc raftpb.ConfChangeI) *raftpb.ConfState {
	n.confChanges = append(n.confChanges, cc.AsV2())
	return &raftpb.ConfState{}
}

func (n *confChangeRecorderNode) Advance() { n.advanced++ }

func TestProcessReadyAppliesCommittedConfChanges(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	if err := os.MkdirAll(walDir, 0o750); err != nil {
		t.Fatalf("mkdir wal: %v", err)
	}
	w, err := wal.Create(zap.NewNop(), walDir, nil)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	defer func() { _ = w.Close() }()

	stub := &confChangeRecorderNode{}
	var appliedEntries []raftpb.Entry
	n := &Node{
		cfg: Config{Apply: func(entries []raftpb.Entry, _ uint64) error {
			appliedEntries = append(appliedEntries, entries...)
			return nil
		}},
		node:      stub,
		storage:   raft.NewMemoryStorage(),
		wal:       w,
		transport: discardTransport{},
		readMap:   make(map[string]chan uint64),
	}

	n.processReady(raft.Ready{CommittedEntries: []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 7)},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: []byte("cmd")},
	}})

	if got := len(stub.confChanges); got != 1 {
		t.Fatalf("ApplyConfChange calls = %d, want 1", got)
	}
	changes := stub.confChanges[0].Changes
	if len(changes) != 1 || changes[0].NodeID != 7 {
		t.Fatalf("applied conf change = %+v, want single AddNode 7", changes)
	}
	if got := len(appliedEntries); got != 2 {
		t.Fatalf("entries passed to Apply = %d, want 2", got)
	}
	if stub.advanced != 1 {
		t.Fatalf("Advance calls = %d, want 1", stub.advanced)
	}
}

func TestRestartBeforeFirstCommitFallsBackToConfiguredPeers(t *testing.T) {
	dataDir := t.TempDir()
	// Simulate a crash after StartNode saved its bootstrap ConfChange entries
	// to the WAL but before anything committed (HardState is still empty).
	writeRestartWAL(t, dataDir,
		[]raftpb.Entry{
			{Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
		},
		raftpb.HardState{},
	)

	node, err := Open(Config{
		ID:           1,
		Peers:        map[uint64]string{1: ""},
		DataDir:      dataDir,
		TickInterval: 10 * time.Millisecond,
		Transport:    discardTransport{},
		Apply:        func(_ []raftpb.Entry, _ uint64) error { return nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer node.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if node.IsLeader() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("node restarted before first commit never campaigned; want conf state fallback from configured peers")
}

func TestOpenRejectsFreshStartWithoutPeers(t *testing.T) {
	_, err := Open(Config{
		ID:        1,
		DataDir:   t.TempDir(),
		Transport: discardTransport{},
		Apply:     func(_ []raftpb.Entry, _ uint64) error { return nil },
	})
	if err == nil {
		t.Fatal("Open with no peers on a fresh start succeeded, want error")
	}
}

func TestFailedRestartReleasesWAL(t *testing.T) {
	dataDir := t.TempDir()
	walDir := filepath.Join(dataDir, "wal")
	snapDir := filepath.Join(dataDir, "snap")
	for _, d := range []string{walDir, snapDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	logger := zap.NewNop()
	snapshot := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index:     10,
			Term:      2,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
		Data: []byte("snapshot"),
	}
	if err := snap.New(logger, snapDir).SaveSnap(snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	w, err := wal.Create(logger, walDir, nil)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	if err := w.SaveSnapshot(walpb.Snapshot{
		Index:     snapshot.Metadata.Index,
		Term:      snapshot.Metadata.Term,
		ConfState: &snapshot.Metadata.ConfState,
	}); err != nil {
		t.Fatalf("save wal snapshot: %v", err)
	}
	if err := w.Save(raftpb.HardState{Term: 2, Vote: 1, Commit: 10}, nil); err != nil {
		t.Fatalf("save hard state: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	cfg := Config{
		ID:           1,
		Peers:        map[uint64]string{1: ""},
		DataDir:      dataDir,
		TickInterval: 10 * time.Millisecond,
		Transport:    discardTransport{},
		Apply:        func(_ []raftpb.Entry, _ uint64) error { return nil },
	}

	failCfg := cfg
	failCfg.Restore = func(_ []byte) error { return errors.New("restore failed") }
	if _, err := Open(failCfg); err == nil {
		t.Fatal("Open with failing Restore succeeded, want error")
	}

	node, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open after failed restart: %v (WAL lock not released?)", err)
	}
	node.Stop()
}

func TestNextReadContextIsUnique(t *testing.T) {
	n := &Node{cfg: Config{ID: 1}}
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		key := n.nextReadContext()
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate ReadIndex request context %q", key)
		}
		seen[key] = struct{}{}
	}
}
