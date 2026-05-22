package raftmeta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
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
	authority, err := OpenAuthority(filepath.Join(dir, "raftmeta"), "tenant-txn", metadata)
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
