package raft

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type discardTransport struct{}

func (discardTransport) Send(_ []raftpb.Message) {}

func TestRestartWithSnapshotNewerThanHardStateCommit(t *testing.T) {
	dataDir := t.TempDir()
	walDir := filepath.Join(dataDir, "wal")
	snapDir := filepath.Join(dataDir, "snap")
	if err := os.MkdirAll(walDir, 0o750); err != nil {
		t.Fatalf("mkdir wal: %v", err)
	}
	if err := os.MkdirAll(snapDir, 0o750); err != nil {
		t.Fatalf("mkdir snap: %v", err)
	}

	logger := zap.NewNop()
	snapshot := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 10,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2, 3},
			},
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
	if err := w.Save(raftpb.HardState{Term: 2, Vote: 1, Commit: 5}, nil); err != nil {
		t.Fatalf("save hard state: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	node, err := Open(Config{
		ID:        1,
		Peers:     map[uint64]string{1: "", 2: "", 3: ""},
		DataDir:   dataDir,
		Transport: discardTransport{},
		Apply: func(_ []raftpb.Entry, _ uint64) error {
			return nil
		},
		Restore: func(data []byte) error {
			if string(data) != "snapshot" {
				t.Fatalf("restore data = %q, want snapshot", string(data))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	node.Stop()
}

func TestWALSnapshotFromReadySnapshotIncludesConfState(t *testing.T) {
	snapshot := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 12,
			Term:  34,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2, 3},
			},
		},
	}

	walSnap := walSnapshotFromReadySnapshot(snapshot)
	if walSnap.Index != snapshot.Metadata.Index || walSnap.Term != snapshot.Metadata.Term {
		t.Fatalf("wal snapshot metadata = %d/%d, want %d/%d",
			walSnap.Index,
			walSnap.Term,
			snapshot.Metadata.Index,
			snapshot.Metadata.Term,
		)
	}
	if walSnap.ConfState == nil {
		t.Fatal("wal snapshot ConfState is nil")
	}
	if !slices.Equal(walSnap.ConfState.Voters, []uint64{1, 2, 3}) {
		t.Fatalf("wal snapshot voters = %v, want [1 2 3]", walSnap.ConfState.Voters)
	}
}

func TestRestartReplaysCommittedEntriesWithoutSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	writeRestartWAL(t, dataDir, restartReplayEntries(t), raftpb.HardState{Term: 2, Vote: 1, Commit: 4})

	applied := make(chan []raftpb.Entry, 1)
	node, err := Open(Config{
		ID:        1,
		Peers:     map[uint64]string{1: "", 2: "", 3: ""},
		DataDir:   dataDir,
		Transport: discardTransport{},
		Apply: func(entries []raftpb.Entry, replayUntil uint64) error {
			if replayUntil != 0 {
				t.Errorf("replayUntil = %d, want 0", replayUntil)
			}
			applied <- append([]raftpb.Entry(nil), entries...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer node.Stop()

	requireRestartReplay(t, applied)
}

func restartReplayEntries(t *testing.T) []raftpb.Entry {
	t.Helper()
	return []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
		{Index: 2, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 2)},
		{Index: 3, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 3)},
		{Index: 4, Term: 2, Type: raftpb.EntryNormal, Data: []byte("committed-after-crash")},
	}
}

func writeRestartWAL(t *testing.T, dataDir string, entries []raftpb.Entry, hardState raftpb.HardState) {
	t.Helper()
	walDir := filepath.Join(dataDir, "wal")
	snapDir := filepath.Join(dataDir, "snap")
	if err := os.MkdirAll(walDir, 0o750); err != nil {
		t.Fatalf("mkdir wal: %v", err)
	}
	if err := os.MkdirAll(snapDir, 0o750); err != nil {
		t.Fatalf("mkdir snap: %v", err)
	}

	w, err := wal.Create(zap.NewNop(), walDir, nil)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	if err := w.Save(hardState, entries); err != nil {
		t.Fatalf("save wal: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
}

func requireRestartReplay(t *testing.T, applied <-chan []raftpb.Entry) {
	t.Helper()
	select {
	case got := <-applied:
		if len(got) != 4 {
			t.Fatalf("applied entries = %d, want 4", len(got))
		}
		normal := got[len(got)-1]
		if normal.Index != 4 || string(normal.Data) != "committed-after-crash" {
			t.Fatalf("last applied entry = index %d data %q, want committed index 4", normal.Index, string(normal.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not replay committed entries")
	}
}

func TestDeriveCommittedConfStateAppliesVoters(t *testing.T) {
	entries := []raftpb.Entry{
		{Index: 1, Type: raftpb.EntryNormal},
		{Index: 2, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
		{Index: 3, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 2)},
		{Index: 4, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
		{Index: 5, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeRemoveNode, 1)},
		{Index: 6, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeUpdateNode, 2)},
		{Index: 7, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddLearnerNode, 3)},
		{Index: 8, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 4)},
	}

	confState := deriveCommittedConfState(entries, 7)
	if confState == nil {
		t.Fatal("confState is nil")
	}
	if !slices.Equal(confState.Voters, []uint64{2}) {
		t.Fatalf("voters = %v, want [2]", confState.Voters)
	}
}

func TestDeriveCommittedConfStateSkipsWithoutCommit(t *testing.T) {
	entries := []raftpb.Entry{
		{Index: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, raftpb.ConfChangeAddNode, 1)},
	}

	if confState := deriveCommittedConfState(entries, 0); confState != nil {
		t.Fatalf("confState = %+v, want nil", confState)
	}
}

func TestDeriveCommittedConfStateSkipsWithoutConfChange(t *testing.T) {
	entries := []raftpb.Entry{{Index: 1, Type: raftpb.EntryNormal}}

	if confState := deriveCommittedConfState(entries, 1); confState != nil {
		t.Fatalf("confState = %+v, want nil", confState)
	}
}

func mustConfChangeData(t *testing.T, typ raftpb.ConfChangeType, nodeID uint64) []byte {
	t.Helper()
	data, err := (&raftpb.ConfChange{Type: typ, NodeID: nodeID}).Marshal()
	if err != nil {
		t.Fatalf("marshal conf change: %v", err)
	}
	return data
}
