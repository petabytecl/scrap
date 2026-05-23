package raftmeta

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestAuthorityRebuildsProjectionFromCommandLog(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	completedAt := time.Unix(200, 0).UTC()

	if err := authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("commit document: %v", err)
	}
	if _, err := authority.CompleteTransaction(context.Background(), identity.Transaction{
		TenantID:      document.Identity.TenantID,
		TransactionID: document.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "test"}, "cmd-2"); err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	if err := authority.RecordUploadIntent(context.Background(), metastore.UploadIntent{
		BlockID:           document.Location.BlockID,
		BackendObjectKey:  "objects/block-1.blk",
		IndexObjectKey:    "objects/block-1.idx",
		EnvelopeObjectKey: "objects/block-1.env",
	}, "cmd-3", time.Unix(300, 0).UTC()); err != nil {
		t.Fatalf("record upload intent: %v", err)
	}
	if err := authority.UpdateUploadIntentState(context.Background(), document.Location.BlockID, metastore.UploadStateUploaded, "", "cmd-4", time.Unix(350, 0).UTC()); err != nil {
		t.Fatalf("update upload intent state: %v", err)
	}
	if err := authority.UpdateDocumentRestoreState(context.Background(), document.Identity, metastore.RestoreStateCold, "cooled", "cmd-5", time.Unix(400, 0).UTC()); err != nil {
		t.Fatalf("update restore state: %v", err)
	}
	if err := authority.RecordRepairState(context.Background(), metastore.RepairState{
		Identity:    document.Identity,
		PhysicalRef: "member-a/block-1/64",
		IncidentID:  "incident-1",
		Quarantined: true,
	}, "cmd-6", time.Unix(500, 0).UTC()); err != nil {
		t.Fatalf("record repair state: %v", err)
	}
	tombstonedAt := time.Unix(600, 0).UTC()
	if err := authority.TombstoneDocument(context.Background(), document.Identity, tombstonedAt, "operation-1", "cmd-7"); err != nil {
		t.Fatalf("tombstone document: %v", err)
	}
	closeTestAuthority(t, authority, metadata)

	if err := os.RemoveAll(filepath.Join(dir, "metadata")); err != nil {
		t.Fatalf("remove metadata projection: %v", err)
	}
	rebuiltMetadata := openTestMetadata(t, dir)
	rebuiltAuthority := openTestAuthority(t, dir, rebuiltMetadata)
	defer closeTestAuthority(t, rebuiltAuthority, rebuiltMetadata)

	head, err := rebuiltMetadata.HeadDocument(document.Identity)
	if err != nil {
		t.Fatalf("head rebuilt document: %v", err)
	}
	if head.Identity != document.Identity || head.Length != document.Length {
		t.Fatalf("rebuilt document = %#v, want %#v", head, document)
	}
	if head.RestoreState != metastore.RestoreStateCold || head.LifecycleState != metastore.LifecycleStateTombstoned {
		t.Fatalf("rebuilt restore/lifecycle state = %d/%d, want cold/tombstoned", head.RestoreState, head.LifecycleState)
	}
	if head.TombstonedAt == nil || !head.TombstonedAt.Equal(tombstonedAt) || head.TombstoneOperationID != "operation-1" {
		t.Fatalf("rebuilt tombstone metadata = %#v", head)
	}
	transaction, err := rebuiltMetadata.GetTransaction(identity.Transaction{
		TenantID:      document.Identity.TenantID,
		TransactionID: document.Identity.TransactionID,
	})
	if err != nil {
		t.Fatalf("get rebuilt transaction: %v", err)
	}
	if transaction.CompletedAt == nil || !transaction.CompletedAt.Equal(completedAt) {
		t.Fatalf("rebuilt completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	}
	if transaction.Tags["closed_by"] != "test" {
		t.Fatalf("rebuilt tags = %#v, want closed_by=test", transaction.Tags)
	}
	intent, err := rebuiltMetadata.GetUploadIntent(document.Location.BlockID)
	if err != nil {
		t.Fatalf("get rebuilt upload intent: %v", err)
	}
	if intent.BackendObjectKey != "objects/block-1.blk" || intent.State != metastore.UploadStateUploaded {
		t.Fatalf("rebuilt upload intent = %#v, want uploaded objects/block-1.blk", intent)
	}
	repair, err := rebuiltMetadata.GetRepairState(document.Identity, "incident-1")
	if err != nil {
		t.Fatalf("get rebuilt repair state: %v", err)
	}
	if !repair.Quarantined || repair.PhysicalRef != "member-a/block-1/64" {
		t.Fatalf("rebuilt repair state = %#v, want quarantined member-a ref", repair)
	}
}

func TestAuthorityRejectsConflictingCommitWithoutPoisoningLog(t *testing.T) {
	dir := t.TempDir()
	metadata := openTestMetadata(t, dir)
	authority := openTestAuthority(t, dir, metadata)
	document := authorityTestDocument("invoice.xml", []byte{1})
	if err := authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("commit document: %v", err)
	}
	conflict := authorityTestDocument("invoice.xml", []byte{2})
	if err := authority.CommitDocument(context.Background(), conflict, "cmd-2", time.Unix(101, 0).UTC()); !errors.Is(err, metastore.ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, metastore.ErrConflict)
	}
	entries, err := authority.log.Replay()
	if err != nil {
		t.Fatalf("replay log: %v", err)
	}
	if len(entries) != 1 || entries[0].Command.GetCommandId() != "cmd-1" {
		t.Fatalf("entries = %#v, want only first command", entries)
	}
	closeTestAuthority(t, authority, metadata)

	reopenedMetadata := openTestMetadata(t, dir)
	reopenedAuthority := openTestAuthority(t, dir, reopenedMetadata)
	defer closeTestAuthority(t, reopenedAuthority, reopenedMetadata)
	head, err := reopenedMetadata.HeadDocument(document.Identity)
	if err != nil {
		t.Fatalf("head document after reopen: %v", err)
	}
	if head.Length != document.Length {
		t.Fatalf("head length = %d, want %d", head.Length, document.Length)
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
	if err != nil {
		t.Fatalf("open blockstore: %v", err)
	}
	record, err := blocks.Append(context.Background(), bytes.NewReader(privateBytes))
	if err != nil {
		_ = blocks.Close()
		t.Fatalf("append private bytes: %v", err)
	}
	if err := blocks.Close(); err != nil {
		t.Fatalf("close blockstore: %v", err)
	}
	document.Length = uint64(len(privateBytes))
	document.LogicalSHA256 = record.LogicalSHA256
	document.StoredSHA256 = record.LogicalSHA256
	document.Location = record
	if err := authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("commit document: %v", err)
	}
	if err := authority.RecordUploadIntent(context.Background(), metastore.UploadIntent{
		BlockID:           document.Location.BlockID,
		BackendObjectKey:  "objects/block-1.blk",
		IndexObjectKey:    "objects/block-1.idx",
		EnvelopeObjectKey: "objects/block-1.env",
	}, "cmd-2", time.Unix(200, 0).UTC()); err != nil {
		t.Fatalf("record upload intent: %v", err)
	}
	if err := authority.UpdateUploadIntentState(context.Background(), document.Location.BlockID, metastore.UploadStateUploaded, "", "cmd-3", time.Unix(250, 0).UTC()); err != nil {
		t.Fatalf("update upload intent: %v", err)
	}
	if err := authority.UpdateDocumentRestoreState(context.Background(), document.Identity, metastore.RestoreStateCold, "snapshot-test", "cmd-4", time.Unix(300, 0).UTC()); err != nil {
		t.Fatalf("update restore state: %v", err)
	}
	if err := authority.RecordRepairState(context.Background(), metastore.RepairState{
		Identity:    document.Identity,
		PhysicalRef: "member-a/block-1/64",
		IncidentID:  "incident-1",
		Quarantined: true,
	}, "cmd-5", time.Unix(350, 0).UTC()); err != nil {
		t.Fatalf("record repair state: %v", err)
	}
	info, err := authority.CreateSnapshot(context.Background())
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if info.LastIndex != 5 || info.Documents != 1 || info.Transactions != 1 || info.UploadJobs != 1 || info.Repairs != 1 || info.Members != 2 {
		t.Fatalf("snapshot info = %#v, want metadata/job/repair/membership state through index 5", info)
	}
	snapshot, err := readSnapshotFile(raftDir)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.GetUploadIntents()[0].GetEnvelopeObjectKey() != "objects/block-1.env" {
		t.Fatalf("snapshot upload intent = %#v, want envelope object key", snapshot.GetUploadIntents()[0])
	}
	if snapshot.GetDocuments()[0].GetRestoreState() != metastorev1.RestoreState_RESTORE_STATE_COLD {
		t.Fatalf("snapshot restore state = %s, want cold", snapshot.GetDocuments()[0].GetRestoreState())
	}
	if len(snapshot.GetMembership().GetMembers()) != 2 {
		t.Fatalf("snapshot membership = %#v, want two members", snapshot.GetMembership())
	}
	rawSnapshot, err := os.ReadFile(snapshotPath(raftDir))
	if err != nil {
		t.Fatalf("read raw snapshot: %v", err)
	}
	if bytes.Contains(rawSnapshot, privateBytes) {
		t.Fatal("snapshot contains document byte payload")
	}

	tailDocument := authorityTestDocument("tail.xml", []byte{2})
	tailDocument.Identity.TransactionID = "txn-tail"
	tailDocument.Location.BlockID = "block-2"
	if err := authority.CommitDocument(context.Background(), tailDocument, "cmd-6", time.Unix(400, 0).UTC()); err != nil {
		t.Fatalf("commit tail document: %v", err)
	}
	if err := authority.CompactLog(context.Background(), info.LastIndex); err != nil {
		t.Fatalf("compact log: %v", err)
	}
	closeTestAuthority(t, authority, metadata)

	if err := os.RemoveAll(filepath.Join(dir, "metadata")); err != nil {
		t.Fatalf("remove metadata projection: %v", err)
	}
	rebuiltMetadata := openTestMetadata(t, dir)
	rebuiltAuthority := openTestAuthorityWithOptions(t, raftDir, rebuiltMetadata, AuthorityOptions{})
	defer closeTestAuthority(t, rebuiltAuthority, rebuiltMetadata)
	head, err := rebuiltMetadata.HeadDocument(document.Identity)
	if err != nil {
		t.Fatalf("head snapshot document: %v", err)
	}
	if head.RestoreState != metastore.RestoreStateCold {
		t.Fatalf("snapshot document restore state = %d, want cold", head.RestoreState)
	}
	if _, err := rebuiltMetadata.GetUploadIntent(document.Location.BlockID); err != nil {
		t.Fatalf("get snapshot upload intent: %v", err)
	}
	if _, err := rebuiltMetadata.GetRepairState(document.Identity, "incident-1"); err != nil {
		t.Fatalf("get snapshot repair state: %v", err)
	}
	if _, err := rebuiltMetadata.HeadDocument(tailDocument.Identity); err != nil {
		t.Fatalf("head tail document: %v", err)
	}
}

func TestAuthorityRejectsCompactedLogWithoutSnapshot(t *testing.T) {
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "raftmeta")
	log, err := OpenLog(raftDir)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := log.Append(sampleCommand("cmd-1", "invoice-1.xml")); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if _, err := log.Append(sampleCommand("cmd-2", "invoice-2.xml")); err != nil {
		t.Fatalf("append second: %v", err)
	}
	if err := log.Compact(1); err != nil {
		t.Fatalf("compact log: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	metadata := openTestMetadata(t, dir)
	defer func() {
		if err := metadata.Close(); err != nil {
			t.Fatalf("close metadata: %v", err)
		}
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
	if err := authority.CommitDocument(context.Background(), document, "cmd-1", time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("commit document: %v", err)
	}
	if err := authority.CompactLog(context.Background(), authority.AppliedIndex()); err == nil {
		t.Fatal("compacted log without a snapshot covering the compacted index")
	}
	entries, err := authority.log.Replay()
	if err != nil {
		t.Fatalf("replay log: %v", err)
	}
	if len(entries) != 1 || entries[0].Index != 1 {
		t.Fatalf("entries after rejected compaction = %#v, want original command", entries)
	}
}

func openTestMetadata(t *testing.T, dir string) *metastore.Store {
	t.Helper()
	metadata, err := metastore.Open(dir)
	if err != nil {
		t.Fatalf("open metadata: %v", err)
	}
	return metadata
}

func openTestAuthority(t *testing.T, dir string, metadata *metastore.Store) *Authority {
	t.Helper()
	return openTestAuthorityWithOptions(t, filepath.Join(dir, "raftmeta"), metadata, AuthorityOptions{})
}

func openTestAuthorityWithOptions(t *testing.T, raftDir string, metadata *metastore.Store, options AuthorityOptions) *Authority {
	t.Helper()
	authority, err := OpenAuthorityWithOptions(raftDir, "tenant-txn", metadata, options)
	if err != nil {
		t.Fatalf("open authority: %v", err)
	}
	return authority
}

func closeTestAuthority(t *testing.T, authority *Authority, metadata *metastore.Store) {
	t.Helper()
	if err := authority.Close(); err != nil {
		t.Fatalf("close authority: %v", err)
	}
	if err := metadata.Close(); err != nil {
		t.Fatalf("close metadata: %v", err)
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
