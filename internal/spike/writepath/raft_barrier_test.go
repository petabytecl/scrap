package writepath

import (
	"sync"
	"testing"
	"time"
)

func TestStoreRaftCommitBarrierControlsMetadataVisibility(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftCommitBarrier()
	if err != nil {
		t.Fatalf("new raft barrier: %v", err)
	}
	t.Cleanup(func() {
		if err := barrier.Close(); err != nil {
			t.Fatalf("close raft barrier: %v", err)
		}
	})

	store := openStoreAtWithOptions(t, dir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})

	data := []byte("visible after raft commit")
	result := writeTestDocument(t, store, "tx-015", "raft-committed.xml", data)
	if barrier.AppliedCount() != 1 {
		t.Fatalf("applied raft commands = %d, want 1", barrier.AppliedCount())
	}
	committed, ok := barrier.CommittedDocument("tenant-a", "tx-015", "raft-committed.xml")
	if !ok {
		t.Fatal("raft barrier did not retain committed document record")
	}
	assertCommittedRecordMatchesWrite(t, committed, result, data)
	assertReadDocument(t, store, "tenant-a", "tx-015", "raft-committed.xml", data)
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertReadDocument(t, reopened, "tenant-a", "tx-015", "raft-committed.xml", data)
}

func TestStoreClosedRaftCommitBarrierLeavesDocumentInvisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftCommitBarrier()
	if err != nil {
		t.Fatalf("new raft barrier: %v", err)
	}
	if err := barrier.Close(); err != nil {
		t.Fatalf("close raft barrier: %v", err)
	}

	store := openStoreAtWithOptions(t, dir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})

	_, err = store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-016",
		DocumentName:   "raft-closed.xml",
		ExpectedLength: 3,
	}, chunkSource([][]byte{[]byte("ack")}))
	if err == nil {
		t.Fatal("write succeeded with closed raft barrier")
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertNotFound(t, reopened, "tenant-a", "tx-016", "raft-closed.xml")
}

func TestStoreRaftClusterBarrierKeepsDocumentInvisibleUntilQuorum(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	if err != nil {
		t.Fatalf("new raft cluster barrier: %v", err)
	}
	t.Cleanup(func() {
		if err := barrier.Close(); err != nil {
			t.Fatalf("close raft cluster barrier: %v", err)
		}
	})

	leader := barrier.LeaderID()
	followers := clusterFollowers(leader)
	for _, follower := range followers {
		if err := barrier.IsolateNode(follower); err != nil {
			t.Fatalf("isolate follower %d: %v", follower, err)
		}
	}

	var afterOpenLog sync.Once
	reachedBarrier := make(chan struct{})
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		MetadataCommitBarrier: barrier,
		Faults: StoreFaults{
			AfterOpenLogSync: func(DocumentRecord) error {
				afterOpenLog.Do(func() {
					close(reachedBarrier)
				})
				return nil
			},
		},
	})

	data := []byte("not visible before raft quorum")
	writeResult := make(chan error, 1)
	go func() {
		_, err := store.WriteDocument(&WriteDocumentMetadata{
			TenantID:       "tenant-a",
			TransactionID:  "tx-017",
			DocumentName:   "cluster-quorum.xml",
			ExpectedLength: int64(len(data)),
		}, chunkSource([][]byte{data}))
		writeResult <- err
	}()

	select {
	case <-reachedBarrier:
	case err := <-writeResult:
		t.Fatalf("write completed before reaching raft barrier: %v", err)
	case <-time.After(time.Second):
		t.Fatal("write did not reach raft barrier")
	}

	select {
	case err := <-writeResult:
		t.Fatalf("write completed without quorum: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	assertNotFound(t, store, "tenant-a", "tx-017", "cluster-quorum.xml")

	if err := barrier.HealNode(followers[0]); err != nil {
		t.Fatalf("heal follower %d: %v", followers[0], err)
	}
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatalf("write after quorum restored: %v", err)
		}
	case <-time.After(raftCommitTimeout):
		t.Fatal("write did not complete after quorum was restored")
	}
	assertReadDocument(t, store, "tenant-a", "tx-017", "cluster-quorum.xml", data)
}

func TestStoreRaftClusterBarrierCommitsWithOneDroppedFollowerLink(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	if err != nil {
		t.Fatalf("new raft cluster barrier: %v", err)
	}
	t.Cleanup(func() {
		if err := barrier.Close(); err != nil {
			t.Fatalf("close raft cluster barrier: %v", err)
		}
	})

	leader := barrier.LeaderID()
	droppedFollower := clusterFollowers(leader)[0]
	if err := barrier.DropMessagesBetween(leader, droppedFollower); err != nil {
		t.Fatalf("drop leader/follower messages: %v", err)
	}

	store := openStoreAtWithOptions(t, dir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})
	data := []byte("visible with one dropped follower link")
	result := writeTestDocument(t, store, "tx-018", "cluster-dropped-follower.xml", data)

	if got := barrier.AppliedCount(); got != 1 {
		t.Fatalf("cluster applied commands = %d, want 1", got)
	}
	committed, ok := barrier.CommittedDocument("tenant-a", "tx-018", "cluster-dropped-follower.xml")
	if !ok {
		t.Fatal("raft cluster barrier did not retain committed document record")
	}
	assertCommittedRecordMatchesWrite(t, committed, result, data)
	if got := barrier.AppliedCountOn(droppedFollower); got != 0 {
		t.Fatalf("dropped follower applied commands = %d, want 0 before heal", got)
	}
	assertReadDocument(t, store, "tenant-a", "tx-018", "cluster-dropped-follower.xml", data)

	if err := barrier.AllowMessagesBetween(leader, droppedFollower); err != nil {
		t.Fatalf("allow leader/follower messages: %v", err)
	}
	if err := barrier.WaitForAppliedOn(droppedFollower, 1); err != nil {
		t.Fatalf("dropped follower did not catch up after heal: %v", err)
	}
}

func TestStoreRaftClusterBarrierCommitsAfterLeaderChange(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	if err != nil {
		t.Fatalf("new raft cluster barrier: %v", err)
	}
	t.Cleanup(func() {
		if err := barrier.Close(); err != nil {
			t.Fatalf("close raft cluster barrier: %v", err)
		}
	})

	oldLeader := barrier.LeaderID()
	newLeaderCandidate := clusterFollowers(oldLeader)[0]
	if err := barrier.IsolateNode(oldLeader); err != nil {
		t.Fatalf("isolate old leader %d: %v", oldLeader, err)
	}
	if err := barrier.CampaignNode(newLeaderCandidate); err != nil {
		t.Fatalf("campaign node %d: %v", newLeaderCandidate, err)
	}
	newLeader := barrier.LeaderID()
	if newLeader == 0 || newLeader == oldLeader {
		t.Fatalf("leader after campaign = %d, want a new leader instead of %d", newLeader, oldLeader)
	}

	store := openStoreAtWithOptions(t, dir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})
	data := []byte("committed after raft leader change")
	writeTestDocument(t, store, "tx-019", "cluster-leader-change.xml", data)
	assertReadDocument(t, store, "tenant-a", "tx-019", "cluster-leader-change.xml", data)

	if err := barrier.HealNode(oldLeader); err != nil {
		t.Fatalf("heal old leader %d: %v", oldLeader, err)
	}
	if err := barrier.WaitForAppliedOn(oldLeader, 1); err != nil {
		t.Fatalf("old leader did not catch up after heal: %v", err)
	}
}

func clusterFollowers(leader uint64) []uint64 {
	followers := make([]uint64, 0, 2)
	for _, id := range []uint64{1, 2, 3} {
		if id != leader {
			followers = append(followers, id)
		}
	}
	return followers
}

func assertCommittedRecordMatchesWrite(t *testing.T, record DocumentRecord, result *WriteDocumentResult, data []byte) {
	t.Helper()

	if record.TenantID != result.TenantID ||
		record.TransactionID != result.TransactionID ||
		record.DocumentName != result.DocumentName {
		t.Fatalf("committed identity = (%s, %s, %s), want (%s, %s, %s)",
			record.TenantID, record.TransactionID, record.DocumentName,
			result.TenantID, result.TransactionID, result.DocumentName)
	}
	if record.ByteLength != int64(len(data)) || record.ByteLength != result.ByteLength {
		t.Fatalf("committed length = %d, result length = %d, want %d", record.ByteLength, result.ByteLength, len(data))
	}
	if record.LogicalSHA256 != sha256Hex(data) || record.LogicalSHA256 != result.LogicalSHA256 {
		t.Fatalf("committed sha = %s, result sha = %s, want %s", record.LogicalSHA256, result.LogicalSHA256, sha256Hex(data))
	}
	if record.BlockID != result.BlockID ||
		record.StoredOffset != result.StoredOffset ||
		record.StoredLength != result.StoredLength {
		t.Fatalf("committed physical ref = (%s, %d, %d), want (%s, %d, %d)",
			record.BlockID, record.StoredOffset, record.StoredLength,
			result.BlockID, result.StoredOffset, result.StoredLength)
	}
	if len(record.Frames) == 0 {
		t.Fatal("committed record has no frame checksums")
	}
}
