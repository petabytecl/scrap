package raft

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// #471: ReadIndex and WithStableLeadership host the linearizable-read
// contract and were at 0% coverage.

func TestReadIndexReturnsCommittedIndexOnLeader(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64
	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()
	waitForLeader(t, node)
	proposeUntilApplied(t, node, &lastApplied, 3)

	// Capture the floor before the call: the background run loop can commit
	// further entries while ReadIndex resolves, so comparing against a commit
	// index read afterwards is racy.
	commitFloor := node.CommitIndex()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	idx, err := node.ReadIndex(ctx)
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if idx < commitFloor {
		t.Fatalf("ReadIndex = %d, want at least the pre-call commit index %d", idx, commitFloor)
	}

	node.readMu.Lock()
	waiters := len(node.readMap)
	node.readMu.Unlock()
	if waiters != 0 {
		t.Fatalf("read waiters after success = %d, want 0", waiters)
	}
}

func TestReadIndexCanceledContextCleansUpWaiter(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64
	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()
	waitForLeader(t, node)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.ReadIndex(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadIndex on canceled context = %v, want context.Canceled", err)
	}

	node.readMu.Lock()
	waiters := len(node.readMap)
	node.readMu.Unlock()
	if waiters != 0 {
		t.Fatalf("read waiters after cancellation = %d, want 0 (leaked waiter)", waiters)
	}
}

func TestWithStableLeadershipPublishesLeaderAndRunsFn(t *testing.T) {
	dataDir := t.TempDir()
	var lastApplied, snapshotCalls atomic.Uint64
	node := openSnapshotTestNode(t, dataDir, &lastApplied, &snapshotCalls)
	defer node.Stop()
	waitForLeader(t, node)

	ran := false
	if err := node.WithStableLeadership(func() error {
		ran = true
		if !node.IsLeader() {
			t.Error("IsLeader inside WithStableLeadership = false, want true on a single-voter leader")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithStableLeadership: %v", err)
	}
	if !ran {
		t.Fatal("WithStableLeadership did not run fn")
	}

	wantErr := errors.New("fn failed")
	if err := node.WithStableLeadership(func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("WithStableLeadership error = %v, want fn error propagated", err)
	}
}
