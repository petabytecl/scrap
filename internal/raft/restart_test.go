package raft

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	raftpb "go.etcd.io/raft/v3/raftpb"
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
