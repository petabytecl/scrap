package shard_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestFindDocumentsReturnsTransactionScopedMetadataInWriteOrder(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	firstPayload := []byte("first scoped payload")
	first, err := s.WriteDocument(ctx, "tx-find-scope", "b.xml", "text/xml", "", bytes.NewReader(firstPayload))
	if err != nil {
		t.Fatalf("WriteDocument first: %v", err)
	}
	secondPayload := []byte(`{"second":true}`)
	second, err := s.WriteDocument(ctx, "tx-find-scope", "a.json", "application/json", "", bytes.NewReader(secondPayload))
	if err != nil {
		t.Fatalf("WriteDocument second: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-find-other", "b.xml", "text/xml", "", bytes.NewReader([]byte("other transaction"))); err != nil {
		t.Fatalf("WriteDocument other Transaction: %v", err)
	}

	docs, err := s.FindDocuments(ctx, "tx-find-scope")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("FindDocuments returned %d Documents, want 2: %+v", len(docs), docs)
	}
	assertStoreMetaMatchesWrite(t, docs[0], "b.xml", "text/xml", first, firstPayload)
	assertStoreMetaMatchesWrite(t, docs[1], "a.json", "application/json", second, secondPayload)
}

func TestFindDocumentsEmptyTransactionReturnsEmptyList(t *testing.T) {
	s := openTestShard(t)

	docs, err := s.FindDocuments(context.Background(), "tx-find-empty")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("FindDocuments returned %d Documents, want empty list: %+v", len(docs), docs)
	}
}

func TestFindDocumentsCorruptIndexFailsClosed(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-find-corrupt", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	idxPath := block.IdxFilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	if err := os.WriteFile(idxPath, []byte("bad index"), 0o600); err != nil {
		t.Fatalf("corrupt idx: %v", err)
	}

	if _, err := s.FindDocuments(ctx, "tx-find-corrupt"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("FindDocuments error = %v, want ErrDataLoss", err)
	}
}

func TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingDiscoveryBackend(backend.NewFS(t.TempDir()))
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("metadata only find "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.countingGetBackend.Backend, content)
	countingBackend.resetCalls()

	docs, err := s.FindDocuments(ctx, "tx-restore")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("FindDocuments returned %d Documents, want 1: %+v", len(docs), docs)
	}
	if docs[0].Name != "doc-1.bin" {
		t.Fatalf("FindDocuments Document name = %q, want doc-1.bin", docs[0].Name)
	}
	if docs[0].Size != int64(len(content)) {
		t.Fatalf("FindDocuments Document size = %d, want %d", docs[0].Size, len(content))
	}
	if got := countingBackend.getCalls.Load(); got != 0 {
		t.Fatalf("Backend GetObject calls = %d, want 0", got)
	}
	if got := countingBackend.headCalls.Load(); got != 0 {
		t.Fatalf("Backend HeadObject calls = %d, want 0", got)
	}
	if got := countingBackend.listCalls.Load(); got != 0 {
		t.Fatalf("Backend ListObjects calls = %d, want 0", got)
	}
}

type countingDiscoveryBackend struct {
	*countingGetBackend
	headCalls atomic.Int32
	listCalls atomic.Int32
}

func newCountingDiscoveryBackend(base backend.Backend) *countingDiscoveryBackend {
	return &countingDiscoveryBackend{countingGetBackend: newCountingGetBackend(base)}
}

func (b *countingDiscoveryBackend) HeadObject(ctx context.Context, key string) (backend.ObjectMeta, error) {
	b.headCalls.Add(1)
	return b.countingGetBackend.Backend.HeadObject(ctx, key)
}

func (b *countingDiscoveryBackend) ListObjects(ctx context.Context, prefix string, opts backend.ListOpts) (backend.ObjectIterator, error) {
	b.listCalls.Add(1)
	return b.countingGetBackend.Backend.ListObjects(ctx, prefix, opts)
}

func (b *countingDiscoveryBackend) resetCalls() {
	b.getCalls.Store(0)
	b.headCalls.Store(0)
	b.listCalls.Store(0)
}
