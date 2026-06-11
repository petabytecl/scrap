package shard_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestWriteDocumentExactReplayReturnsOriginalMetadata(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := []byte("original replay payload")

	first, err := s.WriteDocument(ctx, "tx-replay", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	second, err := s.WriteDocument(ctx, "tx-replay", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("exact replay WriteDocument: %v", err)
	}

	if second.SHA256 != first.SHA256 {
		t.Fatal("exact replay SHA256 should match original write result")
	}
	if second.Size != first.Size {
		t.Fatalf("exact replay size = %d, want %d", second.Size, first.Size)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("exact replay CreatedAt = %s, want original %s", second.CreatedAt, first.CreatedAt)
	}
	assertFindDocumentCount(ctx, t, s, "tx-replay", 1)
	assertReadDocumentContent(ctx, t, s, "tx-replay", payload)
}

func TestWriteDocumentConflictingReplayFailsWithoutMutation(t *testing.T) {
	tests := []struct {
		name           string
		txID           string
		replayContent  []byte
		replayType     string
		wantFirstBytes []byte
	}{
		{
			name:           "payload",
			txID:           "tx-conflict-payload",
			replayContent:  []byte("changed payload"),
			replayType:     "text/xml",
			wantFirstBytes: []byte("original payload"),
		},
		{
			name:           "content type",
			txID:           "tx-conflict-content-type",
			replayContent:  []byte("original payload"),
			replayType:     "application/xml",
			wantFirstBytes: []byte("original payload"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestShard(t)
			ctx := context.Background()

			first, err := s.WriteDocument(ctx, tt.txID, "doc.xml", "text/xml", "", bytes.NewReader(tt.wantFirstBytes))
			if err != nil {
				t.Fatalf("first WriteDocument: %v", err)
			}
			_, err = s.WriteDocument(ctx, tt.txID, "doc.xml", tt.replayType, "", bytes.NewReader(tt.replayContent))
			if !storeapi.IsAlreadyExists(err) {
				t.Fatalf("conflicting replay error = %v, want ErrAlreadyExists", err)
			}

			assertReplayConflictPreservesDocument(ctx, t, s, tt.txID, first, tt.wantFirstBytes)
		})
	}
}

func assertReplayConflictPreservesDocument(
	ctx context.Context,
	t *testing.T,
	s *shard.Shard,
	txID string,
	first storeapi.WriteResult,
	wantFirstBytes []byte,
) {
	t.Helper()
	meta, err := s.HeadDocument(ctx, txID, "doc.xml")
	if err != nil {
		t.Fatalf("HeadDocument after conflict: %v", err)
	}
	if meta.SHA256 != first.SHA256 {
		t.Fatal("conflict mutated SHA256")
	}
	if meta.Size != first.Size {
		t.Fatalf("conflict mutated size = %d, want %d", meta.Size, first.Size)
	}
	if meta.ContentType != "text/xml" {
		t.Fatalf("conflict mutated content type = %q, want text/xml", meta.ContentType)
	}
	assertFindDocumentCount(ctx, t, s, txID, 1)
	assertReadDocumentContent(ctx, t, s, txID, wantFirstBytes)
}

func TestWriteDocumentDuplicateWithCorruptCommittedStateFailsClosed(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	payload := []byte("committed before corruption")
	if _, err := s.WriteDocument(ctx, "tx-corrupt-replay", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	idxPath := block.IdxFilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	if err := os.WriteFile(idxPath, []byte("not a valid block index"), 0o600); err != nil {
		t.Fatalf("corrupt idx: %v", err)
	}

	replay := bytes.NewReader(payload)
	_, err := s.WriteDocument(ctx, "tx-corrupt-replay", "doc.xml", "text/xml", "", replay)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("duplicate corrupt-state error = %v, want ErrDataLoss", err)
	}
	if replay.Len() != 0 {
		t.Fatalf("duplicate corrupt-state body bytes remaining = %d, want 0", replay.Len())
	}
}
