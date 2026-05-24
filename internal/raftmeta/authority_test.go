package raftmeta

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/observe"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestAuthorityRebuildsProjectionFromCommandLog(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	completedAt := time.Unix(200, 0).UTC()

	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	_, err := authority.CompleteTransaction(context.Background(), identity.Transaction{
		TenantID:      document.Identity.TenantID,
		TransactionID: document.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "test"}, "cmd-2")
	testutil.RequireNoErrorf(t, err, "complete transaction")
	testutil.RequireNoErrorf(t, authority.RecordUploadIntent(context.Background(), metastore.UploadIntent{
		BlockID:           document.Location.BlockID,
		BackendObjectKey:  "objects/block-1.blk",
		IndexObjectKey:    "objects/block-1.idx",
		EnvelopeObjectKey: "objects/block-1.env",
	}, "cmd-3", time.Unix(300, 0).UTC()), "record upload intent")
	testutil.RequireNoErrorf(t, authority.UpdateUploadIntentState(context.Background(), document.Location.BlockID, metastore.UploadStateUploaded, "", "cmd-4", time.Unix(350, 0).UTC()), "update upload intent state")
	testutil.RequireNoErrorf(t, authority.UpdateDocumentRestoreState(context.Background(), document.Identity, metastore.RestoreStateCold, "cooled", "cmd-5", time.Unix(400, 0).UTC()), "update restore state")
	testutil.RequireNoErrorf(t, authority.RecordRepairState(context.Background(), metastore.RepairState{
		Identity:    document.Identity,
		PhysicalRef: "member-a/block-1/64",
		IncidentID:  "incident-1",
		Quarantined: true,
	}, "cmd-6", time.Unix(500, 0).UTC()), "record repair state")
	tombstonedAt := time.Unix(600, 0).UTC()
	testutil.RequireNoErrorf(t, authority.TombstoneDocument(context.Background(), document.Identity, tombstonedAt, "operation-1", "cmd-7"), "tombstone document")
	closeTestAuthority(t, authority, metadata)

	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")
	rebuiltMetadata := openTestMetadata(t, dir)
	rebuiltAuthority := openTestAuthority(t, dir, rebuiltMetadata)
	defer closeTestAuthority(t, rebuiltAuthority, rebuiltMetadata)

	head, err := rebuiltMetadata.HeadDocument(document.Identity)
	testutil.RequireNoErrorf(t, err, "head rebuilt document")
	requireRebuiltDocument(t, head, document, tombstonedAt)
	transaction, err := rebuiltMetadata.GetTransaction(identity.Transaction{
		TenantID:      document.Identity.TenantID,
		TransactionID: document.Identity.TransactionID,
	})
	testutil.RequireNoErrorf(t, err, "get rebuilt transaction")
	requireRebuiltTransaction(t, transaction, completedAt)
	intent, err := rebuiltMetadata.GetUploadIntent(document.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get rebuilt upload intent")
	requireRebuiltUploadIntent(t, intent)
	repair, err := rebuiltMetadata.GetRepairState(document.Identity, "incident-1")
	testutil.RequireNoErrorf(t, err, "get rebuilt repair state")
	requireRebuiltRepairState(t, repair)
}

func TestAuthorityCompleteTransactionRetryDoesNotAppend(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	completedAt := time.Unix(200, 0).UTC()

	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	first, err := authority.CompleteTransaction(context.Background(), identity.Transaction{
		TenantID:      document.Identity.TenantID,
		TransactionID: document.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "first"}, "cmd-2")
	testutil.RequireNoErrorf(t, err, "complete transaction")
	firstIndex := authority.AppliedIndex()

	retry, err := authority.CompleteTransaction(context.Background(), first.Identity, completedAt.Add(time.Hour), map[string]string{"closed_by": "retry"}, "cmd-3")
	testutil.RequireNoErrorf(t, err, "retry complete transaction")

	testutil.RequireEqualf(t, authority.AppliedIndex(), firstIndex, "applied index after retry")
	testutil.RequireDeepEqualf(t, retry.Tags, first.Tags, "retry tags")
	if retry.CompletedAt == nil || !retry.CompletedAt.Equal(completedAt) {
		t.Fatalf("retry completed_at = %v, want original %v", retry.CompletedAt, completedAt)
	}
}

func TestAuthorityCompleteTransactionRejectsClosedTransactionWithoutAppending(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	timeoutAt := time.Unix(200, 0).UTC()
	transaction := metastore.Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State:     metastore.TransactionStateTimedOut,
		CreatedAt: time.Unix(100, 0).UTC(),
		TimeoutAt: &timeoutAt,
		Tags:      map[string]string{"reason": "timeout"},
	}
	installAuthorityTransactionSnapshot(t, metadata, transaction)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	beforeIndex := authority.AppliedIndex()

	_, err := authority.CompleteTransaction(context.Background(), transaction.Identity, timeoutAt.Add(time.Minute), nil, "cmd-1")
	if !errors.Is(err, metastore.ErrTransactionClosed) {
		t.Fatalf("complete closed transaction error = %v, want %v", err, metastore.ErrTransactionClosed)
	}
	testutil.RequireEqualf(t, authority.AppliedIndex(), beforeIndex, "applied index after rejected completion")
}

func TestAuthorityRejectsConflictingCommitWithoutPoisoningLog(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	conflict := authorityTestDocument("invoice.xml", []byte{2})
	if err := authority.CommitDocument(context.Background(), conflict, "cmd-2", time.Unix(101, 0).UTC()); !errors.Is(err, metastore.ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, metastore.ErrConflict)
	}
	entries, err := authority.log.Replay()
	testutil.RequireNoErrorf(t, err, "replay log")
	if len(entries) != 1 || entries[0].Command.GetCommandId() != "cmd-1" {
		t.Fatalf("entries = %#v, want only first command", entries)
	}
	closeTestAuthority(t, authority, metadata)

	reopenedMetadata := openTestMetadata(t, dir)
	reopenedAuthority := openTestAuthority(t, dir, reopenedMetadata)
	defer closeTestAuthority(t, reopenedAuthority, reopenedMetadata)
	head, err := reopenedMetadata.HeadDocument(document.Identity)
	testutil.RequireNoErrorf(t, err, "head document after reopen")
	if head.Length != document.Length {
		t.Fatalf("head length = %d, want %d", head.Length, document.Length)
	}
}

func TestAuthorityConcurrentCommitsShareCommandLogSync(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	authority.metadataBatchWait = 50 * time.Millisecond
	authority.metadataBatchMax = 16
	var syncCalls atomic.Int32
	authority.log.syncLogFile = func() error {
		syncCalls.Add(1)
		return authority.log.file.Sync()
	}

	documents := []metastore.Document{
		authorityTestDocument("invoice-1.xml", []byte{1}),
		authorityTestDocument("invoice-2.xml", []byte{2}),
		authorityTestDocument("invoice-3.xml", []byte{3}),
		authorityTestDocument("invoice-4.xml", []byte{4}),
	}
	for index := range documents {
		documents[index].Location.BlockID = "block-batch-" + documents[index].Identity.DocumentName
	}
	start := make(chan struct{})
	errs := make([]error, len(documents))
	var wg sync.WaitGroup
	wg.Add(len(documents))
	for index, document := range documents {
		go func() {
			defer wg.Done()
			<-start
			errs[index] = authority.CommitDocument(context.Background(), document, "cmd-batch-"+document.Identity.DocumentName, time.Unix(100, 0).UTC())
		}()
	}
	close(start)
	wg.Wait()
	for index, err := range errs {
		testutil.RequireNoErrorf(t, err, "commit %d", index)
	}
	if got := syncCalls.Load(); got != 1 {
		t.Fatalf("sync calls = %d, want 1", got)
	}
	if got := authority.AppliedIndex(); got != uint64(len(documents)) {
		t.Fatalf("applied index = %d, want %d", got, len(documents))
	}
	for _, document := range documents {
		_, err := metadata.HeadDocument(document.Identity)
		testutil.RequireNoErrorf(t, err, "head committed document %s", document.Identity.DocumentName)
	}
	entries, err := authority.log.Replay()
	testutil.RequireNoErrorf(t, err, "replay log")
	if len(entries) != len(documents) {
		t.Fatalf("entries = %d, want %d", len(entries), len(documents))
	}
	for index, entry := range entries {
		want := uint64(index + 1)
		if entry.Index != want {
			t.Fatalf("entry %d index = %d, want %d", index, entry.Index, want)
		}
	}
}

func TestAuthorityDoesNotExposeMetadataBeforeCommandSync(t *testing.T) {
	observe.SetRaftQueueDepth(0)
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	authority.metadataBatchWait = 0
	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	var syncCalls atomic.Int32
	authority.log.syncLogFile = func() error {
		if syncCalls.Add(1) == 1 {
			close(syncStarted)
			<-releaseSync
		}
		return authority.log.file.Sync()
	}
	document := authorityTestDocument("pending-sync.xml", []byte{1})

	done := make(chan error, 1)
	go func() {
		done <- authority.CommitDocument(context.Background(), document, "cmd-pending-sync", time.Unix(100, 0).UTC())
	}()
	<-syncStarted
	if _, err := metadata.HeadDocument(document.Identity); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("head before durable sync error = %v, want %v", err, metastore.ErrNotFound)
	}
	requireScrapedMetric(t, "scrap_raft_queue_depth 1")
	close(releaseSync)
	testutil.RequireNoErrorf(t, <-done, "commit after sync")
	_, err := metadata.HeadDocument(document.Identity)
	testutil.RequireNoErrorf(t, err, "head after durable sync")
	requireScrapedMetric(t, "scrap_raft_queue_depth 0")
}

func TestAuthoritySyncErrorFailsBatchAndSubsequentCommands(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	authority.metadataBatchWait = 50 * time.Millisecond
	syncErr := errors.New("metadata sync failed")
	authority.log.syncLogFile = func() error {
		return syncErr
	}

	documents := []metastore.Document{
		authorityTestDocument("failed-1.xml", []byte{1}),
		authorityTestDocument("failed-2.xml", []byte{2}),
	}
	start := make(chan struct{})
	errs := make([]error, len(documents))
	var wg sync.WaitGroup
	wg.Add(len(documents))
	for index, document := range documents {
		go func() {
			defer wg.Done()
			<-start
			errs[index] = authority.CommitDocument(context.Background(), document, "cmd-failed-"+document.Identity.DocumentName, time.Unix(100, 0).UTC())
		}()
	}
	close(start)
	wg.Wait()
	for index, err := range errs {
		if !errors.Is(err, syncErr) {
			t.Fatalf("commit %d error = %v, want %v", index, err, syncErr)
		}
	}
	_, err := metadata.HeadDocument(documents[0].Identity)
	if !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("failed document metadata error = %v, want %v", err, metastore.ErrNotFound)
	}
	recovered := authorityTestDocument("after-failure.xml", []byte{3})
	err = authority.CommitDocument(context.Background(), recovered, "cmd-after-failure", time.Unix(200, 0).UTC())
	if !errors.Is(err, syncErr) {
		t.Fatalf("commit after sync failure error = %v, want %v", err, syncErr)
	}
}

func TestAuthoritySnapshotCompactionReplaysSnapshotAndTail(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raftmeta")
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthorityWithOptions(t, raftDir, metadata, AuthorityOptions{
		Members: []Member{
			{RaftID: 1, MemberID: "member-a", Role: metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER},
			{RaftID: 2, MemberID: "member-b", Role: metastorev1.MembershipRole_MEMBERSHIP_ROLE_LEARNER},
		},
	})
	document := authorityTestDocument("invoice.xml", []byte{1})
	privateBytes := []byte("actual document body must not enter metadata snapshot")
	blocks, err := blockstore.Open(filepath.Join(dir, "blocks"))
	testutil.RequireNoErrorf(t, err, "open blockstore")
	record, err := blocks.Append(context.Background(), bytes.NewReader(privateBytes))
	requireNoBlockstoreErrorBeforeClose(t, blocks, err, "append private bytes")
	testutil.RequireNoErrorf(t, blocks.Close(), "close blockstore")
	document.Length = uint64(len(privateBytes))
	document.LogicalSHA256 = record.LogicalSHA256
	document.StoredSHA256 = record.LogicalSHA256
	document.Location = record
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	testutil.RequireNoErrorf(t, authority.RecordUploadIntent(context.Background(), metastore.UploadIntent{
		BlockID:           document.Location.BlockID,
		BackendObjectKey:  "objects/block-1.blk",
		IndexObjectKey:    "objects/block-1.idx",
		EnvelopeObjectKey: "objects/block-1.env",
	}, "cmd-2", time.Unix(200, 0).UTC()), "record upload intent")
	testutil.RequireNoErrorf(t, authority.UpdateUploadIntentState(context.Background(), document.Location.BlockID, metastore.UploadStateUploaded, "", "cmd-3", time.Unix(250, 0).UTC()), "update upload intent")
	testutil.RequireNoErrorf(t, authority.UpdateDocumentRestoreState(context.Background(), document.Identity, metastore.RestoreStateCold, "snapshot-test", "cmd-4", time.Unix(300, 0).UTC()), "update restore state")
	testutil.RequireNoErrorf(t, authority.RecordRepairState(context.Background(), metastore.RepairState{
		Identity:    document.Identity,
		PhysicalRef: "member-a/block-1/64",
		IncidentID:  "incident-1",
		Quarantined: true,
	}, "cmd-5", time.Unix(350, 0).UTC()), "record repair state")
	info, err := authority.CreateSnapshot(context.Background())
	testutil.RequireNoErrorf(t, err, "create snapshot")
	requireSnapshotInfo(t, info)
	snapshot, err := readSnapshotFile(raftDir)
	testutil.RequireNoErrorf(t, err, "read snapshot")
	requireMetadataSnapshot(t, snapshot)
	rawSnapshot, err := os.ReadFile(snapshotPath(raftDir))
	testutil.RequireNoErrorf(t, err, "read raw snapshot")
	testutil.RequireFalsef(t, bytes.Contains(rawSnapshot, privateBytes), "snapshot contains document byte payload")

	tailDocument := authorityTestDocument("tail.xml", []byte{2})
	tailDocument.Identity.TransactionID = "txn-tail"
	tailDocument.Location.BlockID = "block-2"
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), tailDocument, "cmd-6", time.Unix(400, 0).UTC()), "commit tail document")
	testutil.RequireNoErrorf(t, authority.CompactLog(context.Background(), info.LastIndex), "compact log")
	closeTestAuthority(t, authority, metadata)

	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")
	rebuiltMetadata := openTestMetadata(t, dir)
	rebuiltAuthority := openTestAuthorityWithOptions(t, raftDir, rebuiltMetadata, AuthorityOptions{})
	defer closeTestAuthority(t, rebuiltAuthority, rebuiltMetadata)
	head, err := rebuiltMetadata.HeadDocument(document.Identity)
	testutil.RequireNoErrorf(t, err, "head snapshot document")
	requireSnapshotAndTailRebuilt(t, rebuiltMetadata, head, document, tailDocument)
}

func requireRebuiltDocument(t *testing.T, got, want metastore.Document, tombstonedAt time.Time) {
	t.Helper()
	testutil.RequireEqualf(t, want.Identity, got.Identity, "rebuilt document identity")
	testutil.RequireEqualf(t, want.Length, got.Length, "rebuilt document length")
	testutil.RequireEqualf(t, metastore.RestoreStateCold, got.RestoreState, "rebuilt restore state")
	testutil.RequireEqualf(t, metastore.LifecycleStateTombstoned, got.LifecycleState, "rebuilt lifecycle state")
	testutil.RequireNotNilf(t, got.TombstonedAt, "rebuilt tombstone timestamp")
	testutil.RequireTruef(t, got.TombstonedAt.Equal(tombstonedAt), "rebuilt tombstone timestamp = %v, want %v", got.TombstonedAt, tombstonedAt)
	testutil.RequireEqualf(t, "operation-1", got.TombstoneOperationID, "rebuilt tombstone operation")
}

func requireRebuiltTransaction(t *testing.T, transaction metastore.Transaction, completedAt time.Time) {
	t.Helper()
	testutil.RequireNotNilf(t, transaction.CompletedAt, "rebuilt transaction completion timestamp")
	testutil.RequireTruef(t, transaction.CompletedAt.Equal(completedAt), "rebuilt completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	testutil.RequireEqualf(t, "test", transaction.Tags["closed_by"], "rebuilt closed_by tag")
}

func requireRebuiltUploadIntent(t *testing.T, intent metastore.UploadIntent) {
	t.Helper()
	testutil.RequireEqualf(t, "objects/block-1.blk", intent.BackendObjectKey, "rebuilt backend object key")
	testutil.RequireEqualf(t, metastore.UploadStateUploaded, intent.State, "rebuilt upload state")
}

func requireRebuiltRepairState(t *testing.T, repair metastore.RepairState) {
	t.Helper()
	testutil.RequireTruef(t, repair.Quarantined, "rebuilt repair state should be quarantined")
	testutil.RequireEqualf(t, "member-a/block-1/64", repair.PhysicalRef, "rebuilt repair physical ref")
}

func requireNoBlockstoreErrorBeforeClose(t *testing.T, blocks *blockstore.Store, err error, operation string) {
	t.Helper()
	if err == nil {
		return
	}
	_ = blocks.Close()
	t.Fatalf("%s: %v", operation, err)
}

func requireSnapshotInfo(t *testing.T, info SnapshotInfo) {
	t.Helper()
	testutil.RequireEqualf(t, uint64(5), info.LastIndex, "snapshot last index")
	testutil.RequireEqualf(t, 1, info.Documents, "snapshot document count")
	testutil.RequireEqualf(t, 1, info.Transactions, "snapshot transaction count")
	testutil.RequireEqualf(t, 1, info.UploadJobs, "snapshot upload job count")
	testutil.RequireEqualf(t, 1, info.Repairs, "snapshot repair count")
	testutil.RequireEqualf(t, 2, info.Members, "snapshot member count")
	testutil.RequireEqualf(t, 5, info.Commands, "snapshot command receipt count")
}

func requireMetadataSnapshot(t *testing.T, snapshot *metastorev1.ShardSnapshot) {
	t.Helper()
	testutil.RequireEqualf(t, "objects/block-1.env", snapshot.GetUploadIntents()[0].GetEnvelopeObjectKey(), "snapshot envelope object key")
	testutil.RequireEqualf(t, metastorev1.RestoreState_RESTORE_STATE_COLD, snapshot.GetDocuments()[0].GetRestoreState(), "snapshot restore state")
	testutil.RequireEqualf(t, 2, len(snapshot.GetMembership().GetMembers()), "snapshot member count")
	testutil.RequireEqualf(t, 5, len(snapshot.GetCommandReceipts()), "snapshot command receipt count")
}

func requireSnapshotAndTailRebuilt(
	t *testing.T,
	metadata *metastore.Store,
	head metastore.Document,
	snapshotDocument metastore.Document,
	tailDocument metastore.Document,
) {
	t.Helper()
	testutil.RequireEqualf(t, metastore.RestoreStateCold, head.RestoreState, "snapshot document restore state")
	_, err := metadata.GetUploadIntent(snapshotDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get snapshot upload intent")
	_, err = metadata.GetRepairState(snapshotDocument.Identity, "incident-1")
	testutil.RequireNoErrorf(t, err, "get snapshot repair state")
	_, err = metadata.HeadDocument(tailDocument.Identity)
	testutil.RequireNoErrorf(t, err, "head tail document")
}

func TestAuthorityRejectsCompactedLogWithoutSnapshot(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raftmeta")
	log, err := OpenLog(raftDir)
	testutil.RequireNoErrorf(t, err, "open log")
	if _, err := log.Append(sampleCommand("cmd-1", "invoice-1.xml")); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if _, err := log.Append(sampleCommand("cmd-2", "invoice-2.xml")); err != nil {
		t.Fatalf("append second: %v", err)
	}
	testutil.RequireNoErrorf(t, log.Compact(1), "compact log")
	testutil.RequireNoErrorf(t, log.Close(), "close log")

	metadata := openTestMetadata(t, dir)
	defer func() {
		testutil.RequireNoErrorf(t, metadata.Close(), "close metadata")
	}()
	if _, err := OpenAuthority(raftDir, "tenant-txn", metadata); err == nil {
		t.Fatal("opened compacted log without a snapshot covering the missing prefix")
	}
}

func TestAuthorityRejectsCompactionWithoutCoveredSnapshot(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	if err := authority.CompactLog(context.Background(), authority.AppliedIndex()); err == nil {
		t.Fatal("compacted log without a snapshot covering the compacted index")
	}
	entries, err := authority.log.Replay()
	testutil.RequireNoErrorf(t, err, "replay log")
	if len(entries) != 1 || entries[0].Index != 1 {
		t.Fatalf("entries after rejected compaction = %#v, want original command", entries)
	}
}

func TestAuthorityReadIndexFailsClosedDuringPartition(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	freshness := newControlledFreshness(true, true)
	authority := openTestAuthorityWithOptions(t, filepath.Join(dir, "raftmeta"), metadata, AuthorityOptions{
		LocalMemberID:    "member-a",
		Members:          threeVoterMembers(),
		FreshnessChecker: freshness,
	})
	defer closeTestAuthority(t, authority, metadata)

	document := authorityTestDocument("invoice.xml", []byte{1})
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit document")
	freshness.set(true, false)
	if err := authority.ReadFresh(context.Background()); !errors.Is(err, ErrReadFreshnessUnavailable) {
		t.Fatalf("partitioned read freshness error = %v, want %v", err, ErrReadFreshnessUnavailable)
	}
	tail := authorityTestDocument("tail.xml", []byte{2})
	tail.Identity.TransactionID = "txn-tail"
	tail.Location.BlockID = "block-tail"
	if err := authority.CommitDocument(context.Background(), tail, "cmd-2", time.Unix(200, 0).UTC()); !errors.Is(err, ErrQuorumUnavailable) {
		t.Fatalf("partitioned write error = %v, want %v", err, ErrQuorumUnavailable)
	}
	if _, err := metadata.HeadDocument(tail.Identity); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("partitioned write document error = %v, want %v", err, metastore.ErrNotFound)
	}
	entries, err := authority.log.Replay()
	testutil.RequireNoErrorf(t, err, "replay log")
	if len(entries) != 1 || entries[0].Command.GetCommandId() != "cmd-1" {
		t.Fatalf("entries after partition = %#v, want only first committed command", entries)
	}
	readChecks, _ := freshness.checks()
	if len(readChecks) != 1 || readChecks[0].AppliedIndex != 1 {
		t.Fatalf("read freshness checks = %#v, want applied index 1", readChecks)
	}
}

func TestAuthorityFencesStaleLeaderAfterLeaderChange(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	freshness := newControlledFreshness(true, true)
	authority := openTestAuthorityWithOptions(t, filepath.Join(dir, "raftmeta"), metadata, AuthorityOptions{
		LocalMemberID:    "member-a",
		Members:          threeVoterMembers(),
		FreshnessChecker: freshness,
	})
	defer closeTestAuthority(t, authority, metadata)

	document := authorityTestDocument("leader.xml", []byte{1})
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()), "commit as leader")
	freshness.set(false, true)
	if err := authority.ReadFresh(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale leader read error = %v, want %v", err, ErrNotLeader)
	}
	staleWrite := authorityTestDocument("stale.xml", []byte{2})
	staleWrite.Location.BlockID = "block-stale"
	if err := authority.CommitDocument(context.Background(), staleWrite, "cmd-2", time.Unix(200, 0).UTC()); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale leader write error = %v, want %v", err, ErrNotLeader)
	}
	if _, err := metadata.HeadDocument(staleWrite.Identity); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("stale leader document error = %v, want %v", err, metastore.ErrNotFound)
	}

	freshness.set(true, true)
	recovered := authorityTestDocument("recovered.xml", []byte{3})
	recovered.Identity.TransactionID = "txn-recovered"
	recovered.Location.BlockID = "block-recovered"
	testutil.RequireNoErrorf(t, authority.CommitDocument(context.Background(), recovered, "cmd-3", time.Unix(300, 0).UTC()), "commit after leadership recovery")
	testutil.RequireNoErrorf(t, authority.ReadFresh(context.Background()), "read after leadership recovery")
	entries, err := authority.log.Replay()
	testutil.RequireNoErrorf(t, err, "replay log")
	if len(entries) != 2 || entries[1].Command.GetCommandId() != "cmd-3" {
		t.Fatalf("entries after leader change = %#v, want cmd-1 and cmd-3 only", entries)
	}
}

func TestAuthorityLeaseReadsRemainDisabled(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	defer closeTestAuthority(t, authority, metadata)

	if err := authority.LeaseReadFresh(context.Background()); !errors.Is(err, ErrLeaseReadDisabled) {
		t.Fatalf("lease read error = %v, want %v", err, ErrLeaseReadDisabled)
	}
}

func openTestMetadata(t *testing.T, dir string) *metastore.Store {
	t.Helper()
	metadata, err := metastore.Open(dir)
	testutil.RequireNoErrorf(t, err, "open metadata")
	return metadata
}

func installAuthorityTransactionSnapshot(t *testing.T, metadata *metastore.Store, transaction metastore.Transaction) {
	t.Helper()
	err := metadata.ApplyShardSnapshot(&metastorev1.ShardSnapshot{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		Membership: &metastorev1.MembershipState{
			Members: []*metastorev1.MembershipMember{
				{
					RaftId:   1,
					MemberId: "member-a",
					Role:     metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER,
				},
			},
		},
		Transactions: []*metastorev1.TransactionRecord{metastore.TransactionRecord(transaction)},
	})
	testutil.RequireNoErrorf(t, err, "install transaction snapshot")
}

type controlledFreshness struct {
	mu          sync.Mutex
	leader      bool
	quorum      bool
	readChecks  []FreshnessCheck
	writeChecks []FreshnessCheck
}

func newControlledFreshness(leader, quorum bool) *controlledFreshness {
	return &controlledFreshness{
		leader: leader,
		quorum: quorum,
	}
}

func (c *controlledFreshness) set(leader, quorum bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leader = leader
	c.quorum = quorum
}

func (c *controlledFreshness) RequireWriteQuorum(ctx context.Context, check FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeChecks = append(c.writeChecks, check)
	if !c.leader {
		return ErrNotLeader
	}
	if !c.quorum {
		return ErrQuorumUnavailable
	}
	return nil
}

func (c *controlledFreshness) RequireReadIndex(ctx context.Context, check FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readChecks = append(c.readChecks, check)
	if !c.leader {
		return ErrNotLeader
	}
	if !c.quorum {
		return ErrReadFreshnessUnavailable
	}
	return nil
}

func (c *controlledFreshness) checks() ([]FreshnessCheck, []FreshnessCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]FreshnessCheck(nil), c.readChecks...), append([]FreshnessCheck(nil), c.writeChecks...)
}

func threeVoterMembers() []Member {
	return []Member{
		{RaftID: 1, MemberID: "member-a", Role: metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER},
		{RaftID: 2, MemberID: "member-b", Role: metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER},
		{RaftID: 3, MemberID: "member-c", Role: metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER},
	}
}

func openTestAuthority(t *testing.T, dir string, metadata *metastore.Store) *Authority {
	t.Helper()
	return openTestAuthorityWithOptions(t, filepath.Join(dir, "raftmeta"), metadata, AuthorityOptions{})
}

func openTestAuthorityWithOptions(t *testing.T, raftDir string, metadata *metastore.Store, options AuthorityOptions) *Authority {
	t.Helper()
	authority, err := OpenAuthorityWithOptions(raftDir, "tenant-txn", metadata, options)
	testutil.RequireNoErrorf(t, err, "open authority")
	return authority
}

func closeTestAuthority(t *testing.T, authority *Authority, metadata *metastore.Store) {
	t.Helper()
	testutil.RequireNoErrorf(t, authority.Close(), "close authority")
	testutil.RequireNoErrorf(t, metadata.Close(), "close metadata")
}

func requireScrapedMetric(t *testing.T, want string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	observe.Handler().ServeHTTP(recorder, request)
	testutil.RequireEqualf(t, recorder.Code, http.StatusOK, "metrics status")
	if !strings.Contains(recorder.Body.String(), want) {
		t.Fatalf("metrics body missing %q:\n%s", want, recorder.Body.String())
	}
}

func authorityTestDocument(name string, fill []byte) metastore.Document {
	logical := [32]byte{}
	stored := [32]byte{}
	frame := [32]byte{}
	copy(logical[:], fill)
	copy(stored[:], fill)
	copy(frame[:], fill)
	now := time.Unix(100, 0).UTC()
	return metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  name,
		},
		DocumentClass:               metastore.DocumentClassPermanent,
		PriorityClass:               metastore.PriorityClassNormal,
		Length:                      42,
		LogicalSHA256:               logical,
		StoredSHA256:                stored,
		DocumentIdentityFingerprint: [16]byte{1, 2, 3},
		CreatedByService:            "billing-etl",
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                metastore.AvailabilityHot,
		LifecycleState:              metastore.LifecycleStateActive,
		Location: blockstore.Record{
			BlockID:       "block-1",
			StoredOffset:  64,
			StoredLength:  42,
			LogicalSHA256: logical,
			Frames: []blockstore.FrameRecord{
				{
					FrameOffset:   64,
					SegmentOffset: 64,
					SegmentLength: 42,
					SHA256:        frame,
				},
			},
		},
	}
}
