package writepath

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestStoreRaftCommitBarrierControlsMetadataVisibility(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft barrier")
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
	testutil.RequireNoErrorf(t, err, "new raft barrier")
	testutil.RequireNoErrorf(t, barrier.Close(), "close raft barrier")

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

func TestDurableRaftCommitBarrierReplaysCommittedRecordsAndContinuesWrites(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raft")
	storeDir := filepath.Join(dir, "store")

	barrier, err := NewDurableRaftCommitBarrier(raftDir)
	testutil.RequireNoErrorf(t, err, "new durable raft barrier")
	store := openStoreAtWithOptions(t, storeDir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})

	firstData := []byte("durable raft replay record")
	first := writeTestDocument(t, store, "tx-017", "durable-first.xml", firstData)
	closeTestStore(t, store)
	testutil.RequireNoErrorf(t, barrier.Close(), "close durable raft barrier")

	replayed, err := NewDurableRaftCommitBarrier(raftDir)
	testutil.RequireNoErrorf(t, err, "reopen durable raft barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, replayed.Close(), "close replayed durable raft barrier")
	})

	committed, ok := replayed.CommittedDocument("tenant-a", "tx-017", "durable-first.xml")
	if !ok {
		t.Fatal("replayed barrier did not restore committed document")
	}
	assertCommittedRecordMatchesWrite(t, committed, first, firstData)

	reopenedStore := openStoreAtWithOptions(t, storeDir, StoreOptions{
		MetadataCommitBarrier: replayed,
	})
	secondData := []byte("write after durable raft replay")
	second := writeTestDocument(t, reopenedStore, "tx-017", "durable-second.xml", secondData)
	assertReadDocument(t, reopenedStore, "tenant-a", "tx-017", "durable-second.xml", secondData)

	committed, ok = replayed.CommittedDocument("tenant-a", "tx-017", "durable-second.xml")
	if !ok {
		t.Fatal("replayed barrier did not commit second document")
	}
	assertCommittedRecordMatchesWrite(t, committed, second, secondData)
	if got := replayed.AppliedCount(); got != 2 {
		t.Fatalf("durable raft applied count = %d, want 2", got)
	}
}

func TestStoreProjectionCanRebuildFromDurableRaftLogAfterPebbleLoss(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raft")
	storeDir := filepath.Join(dir, "store")

	barrier, err := NewDurableRaftCommitBarrier(raftDir)
	testutil.RequireNoErrorf(t, err, "new durable raft barrier")
	store := openStoreAtWithOptions(t, storeDir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})

	data := []byte("rebuild visible projection from raft")
	writeTestDocument(t, store, "tx-018", "projection-rebuild.xml", data)
	closeTestStore(t, store)
	testutil.RequireNoErrorf(t, barrier.Close(), "close durable raft barrier")

	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(storeDir, "metadata")), "remove pebble metadata projection")

	replayed, err := NewDurableRaftCommitBarrier(raftDir)
	testutil.RequireNoErrorf(t, err, "reopen durable raft barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, replayed.Close(), "close replayed durable raft barrier")
	})

	rebuildingStore := openStoreAtWithOptions(t, storeDir, StoreOptions{
		MetadataCommitBarrier: replayed,
	})
	testutil.RequireNoErrorf(t, rebuildingStore.RebuildMetadataProjection(replayed.CommittedDocuments()), "rebuild metadata projection")
	assertReadDocument(t, rebuildingStore, "tenant-a", "tx-018", "projection-rebuild.xml", data)
}

func TestDurableRaftCommitBarrierRejectsCorruptLogRecord(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raft")
	storeDir := filepath.Join(dir, "store")

	barrier, err := NewDurableRaftCommitBarrier(raftDir)
	testutil.RequireNoErrorf(t, err, "new durable raft barrier")
	store := openStoreAtWithOptions(t, storeDir, StoreOptions{
		MetadataCommitBarrier: barrier,
	})
	writeTestDocument(t, store, "tx-019", "corrupt-raft-log.xml", []byte("log checksum detects corruption"))
	closeTestStore(t, store)
	testutil.RequireNoErrorf(t, barrier.Close(), "close durable raft barrier")

	path := filepath.Join(raftDir, raftDiskLogFile)
	logBytes, err := os.ReadFile(path)
	testutil.RequireNoErrorf(t, err, "read raft log")
	corrupted := bytes.Replace(logBytes, []byte(`"crc32c":"`), []byte(`"crc32c":"00000000`), 1)
	if bytes.Equal(corrupted, logBytes) {
		t.Fatal("test did not corrupt raft log checksum")
	}
	testutil.RequireNoErrorf(t, os.WriteFile(path, corrupted, 0o600), "write corrupted raft log")

	if _, err := NewDurableRaftCommitBarrier(raftDir); err == nil {
		t.Fatal("opened durable raft barrier with corrupted log record")
	}
}

func TestStoreRaftClusterBarrierKeepsDocumentInvisibleUntilQuorum(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
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
		testutil.RequireNoErrorf(t, err, "write after quorum restored")
	case <-time.After(raftCommitTimeout):
		t.Fatal("write did not complete after quorum was restored")
	}
	assertReadDocument(t, store, "tenant-a", "tx-017", "cluster-quorum.xml", data)
}

func TestStoreRaftClusterBarrierRetriesCommitAfterNoLeader(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
	})

	leader := barrier.LeaderID()
	followers := clusterFollowers(leader)
	for _, follower := range followers {
		testutil.RequireNoErrorf(t, barrier.IsolateNode(follower), "isolate follower")
	}
	waitForRaftClusterNoLeader(t, barrier)

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

	data := []byte("visible after raft quorum returns")
	writeResult := make(chan error, 1)
	go func() {
		_, err := store.WriteDocument(&WriteDocumentMetadata{
			TenantID:       "tenant-a",
			TransactionID:  "tx-017",
			DocumentName:   "cluster-no-leader-retry.xml",
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
		t.Fatalf("write completed without restored quorum: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	assertNotFound(t, store, "tenant-a", "tx-017", "cluster-no-leader-retry.xml")

	testutil.RequireNoErrorf(t, barrier.HealNode(followers[0]), "heal one follower")
	select {
	case err := <-writeResult:
		testutil.RequireNoErrorf(t, err, "write after quorum restored")
	case <-time.After(raftCommitTimeout):
		t.Fatal("write did not complete after quorum was restored")
	}
	assertReadDocument(t, store, "tenant-a", "tx-017", "cluster-no-leader-retry.xml", data)
}

func TestStoreRaftClusterBarrierCommitsWithOneDroppedFollowerLink(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
	})

	leader := barrier.LeaderID()
	droppedFollower := clusterFollowers(leader)[0]
	testutil.RequireNoErrorf(t, barrier.DropMessagesBetween(leader, droppedFollower), "drop leader/follower messages")

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

	testutil.RequireNoErrorf(t, barrier.AllowMessagesBetween(leader, droppedFollower), "allow leader/follower messages")
	testutil.RequireNoErrorf(t, barrier.WaitForAppliedOn(droppedFollower, 1), "dropped follower did not catch up after heal")
}

func TestStoreRaftClusterBarrierCommitsAfterLeaderChange(t *testing.T) {
	dir := t.TempDir()
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
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
	testutil.RequireNoErrorf(t, barrier.WaitForAppliedOn(oldLeader, 1), "old leader did not catch up after heal")
}

func TestRaftClusterReadIndexRequiresCurrentQuorum(t *testing.T) {
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
	})

	testutil.RequireNoErrorf(t, barrier.ReadFresh(context.Background()), "read index with healthy quorum")

	leader := barrier.LeaderID()
	for _, follower := range clusterFollowers(leader) {
		if err := barrier.IsolateNode(follower); err != nil {
			t.Fatalf("isolate follower %d: %v", follower, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if err := barrier.ReadFresh(ctx); err == nil {
		t.Fatal("read index succeeded without quorum")
	}

	testutil.RequireNoErrorf(t, barrier.HealNode(clusterFollowers(leader)[0]), "heal one follower")
	testutil.RequireNoErrorf(t, barrier.ReadFresh(context.Background()), "read index after quorum restored")
}

func TestRaftClusterRetriesPendingProposalAfterLeaderTransferDropsIt(t *testing.T) {
	cluster, err := newRaftCluster([]uint64{1, 2, 3})
	testutil.RequireNoErrorf(t, err, "new raft cluster")

	leader := cluster.leaderID()
	transferee := clusterFollowers(leader)[0]
	result := make(chan error, 1)
	command, err := json.Marshal(raftCommitCommand{
		ID: 1,
		Record: DocumentRecord{
			TenantID:      "tenant-a",
			TransactionID: "tx-transfer",
			DocumentName:  "proposal-transfer.xml",
			ByteLength:    12,
		},
	})
	testutil.RequireNoErrorf(t, err, "marshal raft commit command")

	cluster.nextID = 1
	cluster.waiters[1] = result
	cluster.pending[1] = raftClusterPendingProposal{command: command}
	_, term := cluster.leaderStatus()

	cluster.nodes[leader].raw.TransferLeader(transferee)
	cluster.proposePending()
	pending, ok := cluster.pending[1]
	if !ok {
		t.Fatal("proposal completed instead of remaining pending after leader transfer drop")
	}
	if pending.lastLeaderID != leader || pending.lastTerm != term {
		t.Fatalf("pending retry marker = leader %d term %d, want leader %d term %d",
			pending.lastLeaderID, pending.lastTerm, leader, term)
	}

	deadline := time.Now().Add(raftCommitTimeout)
	for time.Now().Before(deadline) {
		cluster.proposePending()
		if cluster.pump() {
			select {
			case err := <-result:
				testutil.RequireNoErrorf(t, err, "proposal after leader transfer")
				if _, ok := cluster.records[committedDocumentKey("tenant-a", "tx-transfer", "proposal-transfer.xml")]; !ok {
					t.Fatal("proposal committed without retaining document record")
				}
				return
			default:
			}
			continue
		}
		cluster.tick()
	}
	t.Fatal("proposal did not complete after leader transfer")
}

func TestRaftClusterCloseUnblocksInFlightProposal(t *testing.T) {
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")

	leader := barrier.LeaderID()
	for _, follower := range clusterFollowers(leader) {
		testutil.RequireNoErrorf(t, barrier.IsolateNode(follower), "isolate follower")
	}

	result := make(chan error, 1)
	go func() {
		result <- barrier.CommitDocument(context.Background(), DocumentRecord{
			TenantID:      "tenant-a",
			TransactionID: "tx-close",
			DocumentName:  "inflight-close.xml",
			ByteLength:    5,
		})
	}()

	waitForRaftClusterInFlightProposal(t, barrier)
	testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
	select {
	case err := <-result:
		if !errors.Is(err, errRaftClusterClosed) {
			t.Fatalf("commit error = %v, want %v", err, errRaftClusterClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight proposal did not unblock after close")
	}
}

func TestRaftClusterRejectsUnknownControlNodes(t *testing.T) {
	barrier, err := NewRaftClusterCommitBarrier()
	testutil.RequireNoErrorf(t, err, "new raft cluster barrier")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, barrier.Close(), "close raft cluster barrier")
	})

	if err := barrier.IsolateNode(99); err == nil {
		t.Fatal("isolate accepted an unknown node")
	}
	if err := barrier.HealNode(99); err == nil {
		t.Fatal("heal accepted an unknown node")
	}
	if err := barrier.DropMessagesBetween(1, 99); err == nil {
		t.Fatal("drop accepted an unknown node")
	}
	if err := barrier.AllowMessagesBetween(1, 99); err == nil {
		t.Fatal("allow accepted an unknown node")
	}
	if err := barrier.CampaignNode(99); err == nil {
		t.Fatal("campaign accepted an unknown node")
	}
}

func TestRaftClusterIgnoresUndeliverableMessagesAndBadEntries(t *testing.T) {
	cluster, err := newRaftCluster([]uint64{1, 2, 3})
	testutil.RequireNoErrorf(t, err, "new raft cluster")

	if cluster.deliver(pb.Message{From: 1, To: 0}) {
		t.Fatal("message to node zero was delivered")
	}
	if cluster.deliver(pb.Message{From: 1, To: 99}) {
		t.Fatal("message to unknown node was delivered")
	}

	cluster.applyNormalEntry(1, pb.Entry{Data: []byte("{bad json")})
	cluster.applyNormalEntry(1, pb.Entry{Data: mustMarshalRaftCommand(t, raftCommitCommand{})})
	if cluster.appliedOn[1] != 0 {
		t.Fatalf("applied commands = %d, want 0 for malformed entries", cluster.appliedOn[1])
	}
}

func waitForRaftClusterNoLeader(t *testing.T, barrier *RaftClusterCommitBarrier) {
	t.Helper()

	deadline := time.Now().Add(raftCommitTimeout)
	for time.Now().Before(deadline) {
		if barrier.LeaderID() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("raft cluster still had a leader after quorum loss")
}

func waitForRaftClusterInFlightProposal(t *testing.T, barrier *RaftClusterCommitBarrier) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, err := barrier.query(func(cluster *raftCluster) any {
			return len(cluster.pending) + len(cluster.waiters)
		})
		testutil.RequireNoErrorf(t, err, "query raft cluster pending proposals")
		if inFlight, ok := value.(int); ok && inFlight > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("raft cluster did not retain an in-flight proposal")
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

func mustMarshalRaftCommand(t *testing.T, command raftCommitCommand) []byte {
	t.Helper()

	data, err := json.Marshal(command)
	testutil.RequireNoErrorf(t, err, "marshal raft command")
	return data
}
