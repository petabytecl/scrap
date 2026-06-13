package shard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestShardRestartPreservesCoreGatewayBehaviors(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	payload := []byte("restart evidence payload")
	otherPayload := []byte("restart evidence companion")

	firstShard := openRestartEvidenceShard(t, dataDir)
	first, err := firstShard.WriteDocument(ctx, "tx-shard-restart", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := firstShard.WriteDocument(ctx, "tx-shard-restart", "other.xml", "text/xml", "", bytes.NewReader(otherPayload)); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}
	exact, err := firstShard.WriteDocument(ctx, "tx-shard-restart", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("exact replay before restart: %v", err)
	}
	assertRestartReplayResult(t, exact, first)
	assertRestartDocumentContent(ctx, t, firstShard, "tx-shard-restart", "doc.xml", payload)
	assertRestartDocumentContent(ctx, t, firstShard, "tx-shard-restart", "other.xml", otherPayload)
	assertFindDocumentCount(ctx, t, firstShard, "tx-shard-restart", 2)

	if err := firstShard.Close(); err != nil {
		t.Fatalf("Close first shard: %v", err)
	}

	reopened := openRestartEvidenceShard(t, dataDir)
	defer func() { _ = reopened.Close() }()
	assertHeadDocumentSize(ctx, t, reopened, "tx-shard-restart", "doc.xml", int64(len(payload)))
	assertHeadDocumentSize(ctx, t, reopened, "tx-shard-restart", "other.xml", int64(len(otherPayload)))
	assertRestartDocumentContent(ctx, t, reopened, "tx-shard-restart", "doc.xml", payload)
	assertRestartDocumentContent(ctx, t, reopened, "tx-shard-restart", "other.xml", otherPayload)
	assertFindDocumentCount(ctx, t, reopened, "tx-shard-restart", 2)

	replayed, err := reopened.WriteDocument(ctx, "tx-shard-restart", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("exact replay after restart: %v", err)
	}
	assertRestartReplayResult(t, replayed, first)

	_, err = reopened.WriteDocument(ctx, "tx-shard-restart", "doc.xml", "text/xml", "", bytes.NewReader([]byte("conflict")))
	if !storeapi.IsAlreadyExists(err) {
		t.Fatalf("conflicting replay after restart error = %v, want ErrAlreadyExists", err)
	}
	assertRestartDocumentContent(ctx, t, reopened, "tx-shard-restart", "doc.xml", payload)
	assertRestartDocumentContent(ctx, t, reopened, "tx-shard-restart", "other.xml", otherPayload)
}

func TestProjectionRebuildRemovesStaleProjectionEntries(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := []byte("survives rebuild")

	if _, err := s.WriteDocument(ctx, "tx-rebuild-valid", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument valid doc: %v", err)
	}
	s.CorruptProjectionForTest("tx-rebuild-stale", 999, 1, true)

	triggerRebuildAndWait(ctx, t, s)

	docs, err := s.FindDocuments(ctx, "tx-rebuild-stale")
	if err != nil {
		t.Fatalf("FindDocuments stale transaction after rebuild: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("FindDocuments stale transaction count = %d, want 0", len(docs))
	}
	assertHeadDocumentSize(ctx, t, s, "tx-rebuild-valid", "doc.xml", int64(len(payload)))
	assertRestartDocumentContent(ctx, t, s, "tx-rebuild-valid", "doc.xml", payload)
	assertFindDocumentCount(ctx, t, s, "tx-rebuild-valid", 1)
}

func TestProjectionRebuildWithCorruptVisibleIndexFailsClosed(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-rebuild-corrupt", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	idxPath := block.IdxFilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	if err := os.WriteFile(idxPath, []byte("not a valid block index"), 0o600); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	triggerRebuildAndWait(ctx, t, s)

	if _, err := s.HeadDocument(ctx, "tx-rebuild-corrupt", "doc.xml"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument after corrupt rebuild error = %v, want ErrDataLoss", err)
	}
	if _, err := s.FindDocuments(ctx, "tx-rebuild-corrupt"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("FindDocuments after corrupt rebuild error = %v, want ErrDataLoss", err)
	}
	if _, _, err := s.ReadDocument(ctx, "tx-rebuild-corrupt", "doc.xml"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument after corrupt rebuild error = %v, want ErrDataLoss", err)
	}
}

func openRestartEvidenceShard(t *testing.T, dataDir string) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:      dataDir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	waitForLeader(t, s)
	return s
}

func assertRestartReplayResult(t *testing.T, got, want storeapi.WriteResult) {
	t.Helper()

	if got.SHA256 != want.SHA256 {
		t.Fatal("exact replay SHA256 should match original write result")
	}
	if got.Size != want.Size {
		t.Fatalf("exact replay size = %d, want %d", got.Size, want.Size)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("exact replay CreatedAt = %s, want %s", got.CreatedAt, want.CreatedAt)
	}
}

func assertRestartDocumentContent(ctx context.Context, t *testing.T, s *shard.Shard, txID, docName string, want []byte) {
	t.Helper()

	rc, _, err := s.ReadDocument(ctx, txID, docName)
	if err != nil {
		t.Fatalf("ReadDocument %s/%s: %v", txID, docName, err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll %s/%s: %v", txID, docName, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadDocument %s/%s payload = %q, want %q", txID, docName, got, want)
	}
}
