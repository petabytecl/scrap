package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestResolverResolveDocument(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-001", "a.xml", 40),
		resolverEntry("tx-001", "b.xml", 96),
	})
	if err := idx.Put("tx-001", 1, 2, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	doc, err := resolver.ResolveDocument("tx-001", "b.xml")
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if doc.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", doc.BlockID)
	}
	if doc.DocName != "b.xml" {
		t.Fatalf("DocName = %q, want b.xml", doc.DocName)
	}
}

func TestResolverListDocumentsReturnsWriteOrderAcrossBlocks(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-order", "a.xml", 40),
		resolverEntry("tx-order", "b.xml", 96),
	})
	writeResolverIndex(t, blocksDir, 2, []block.IndexEntry{
		resolverEntry("tx-order", "c.xml", 40),
	})
	if err := idx.Put("tx-order", 1, 3, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.AddBlockID("tx-order", 2); err != nil {
		t.Fatalf("AddBlockID: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	docs, err := resolver.ListDocuments("tx-order")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	got := docNames(docs)
	want := []string{"a.xml", "b.xml", "c.xml"}
	if len(got) != len(want) {
		t.Fatalf("len(docs) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("doc %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolverListDocumentsMissingTransactionIsEmpty(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))

	docs, err := resolver.ListDocuments("missing")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("len(docs) = %d, want 0", len(docs))
	}
}

func TestResolverListDocumentsFailsClosedOnDocCountDrift(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-drift", "a.xml", 40),
	})
	if err := idx.Put("tx-drift", 1, 2, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	_, err := resolver.ListDocuments("tx-drift")
	if !errors.Is(err, index.ErrCorrupt) {
		t.Fatalf("ListDocuments error = %v, want ErrCorrupt", err)
	}
}

func TestResolverResolveDocumentNotFound(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-001", "a.xml", 40),
	})
	if err := idx.Put("tx-001", 1, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	_, err := resolver.ResolveDocument("tx-001", "missing.xml")
	if !errors.Is(err, index.ErrDocumentNotFound) {
		t.Fatalf("ResolveDocument error = %v, want ErrDocumentNotFound", err)
	}
}

func TestResolverFailsClosedOnCorruptVisibleIndex(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	if err := idx.Put("tx-corrupt", 1, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(blocksDir, 1), []byte("bad index"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt index: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	if _, err := resolver.ListDocuments("tx-corrupt"); !errors.Is(err, index.ErrCorrupt) {
		t.Fatalf("ListDocuments error = %v, want ErrCorrupt", err)
	}
	if exists, err := resolver.ContainsDocument("tx-corrupt", "a.xml"); !errors.Is(err, index.ErrCorrupt) || exists {
		t.Fatalf("ContainsDocument = (%v, %v), want (false, ErrCorrupt)", exists, err)
	}
}

func TestResolverContainsDocumentLenientTreatsUnreadableBlockAsAbsent(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	if err := idx.Put("tx-torn", 1, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(blocksDir, 1), []byte("bad index"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt index: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	exists, err := resolver.ContainsDocumentLenient("tx-torn", "a.xml")
	if err != nil {
		t.Fatalf("ContainsDocumentLenient: %v", err)
	}
	if exists {
		t.Fatal("ContainsDocumentLenient = true, want false")
	}
}

func TestResolverFailsClosedWhenVisibleBlockHasNoTransactionEntries(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-other", "a.xml", 40),
	})
	if err := idx.Put("tx-missing", 1, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	_, err := resolver.ResolveDocument("tx-missing", "a.xml")
	if !errors.Is(err, index.ErrCorrupt) {
		t.Fatalf("ResolveDocument error = %v, want ErrCorrupt", err)
	}
}

func TestResolverContainsDocumentLenientTreatsMissingTransactionEntriesAsAbsent(t *testing.T) {
	idx, blocksDir := openResolverTestProjection(t)
	writeResolverIndex(t, blocksDir, 1, []block.IndexEntry{
		resolverEntry("tx-other", "a.xml", 40),
	})
	if err := idx.Put("tx-missing", 1, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := index.NewResolver(idx, resolverIndexPath(blocksDir))
	exists, err := resolver.ContainsDocumentLenient("tx-missing", "a.xml")
	if err != nil {
		t.Fatalf("ContainsDocumentLenient: %v", err)
	}
	if exists {
		t.Fatal("ContainsDocumentLenient = true, want false")
	}
}

func openResolverTestProjection(t *testing.T) (*index.Index, string) {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("MkdirAll blocks: %v", err)
	}
	return idx, blocksDir
}

func writeResolverIndex(t *testing.T, blocksDir string, blockID uint64, entries []block.IndexEntry) {
	t.Helper()
	iw, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for _, entry := range entries {
		if err := iw.Append(entry); err != nil {
			t.Fatalf("Append %s: %v", entry.DocName, err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func resolverEntry(txID, docName string, offset int64) block.IndexEntry {
	return block.IndexEntry{
		TransactionID: txID,
		DocName:       docName,
		ContentType:   "text/xml",
		CreatedAt:     time.UnixMicro(1716700000000000),
		FirstFrameOff: offset,
		FrameCount:    1,
		TotalBytes:    12,
		SHA256:        [32]byte{0x01, 0x02, 0x03},
	}
}

func resolverIndexPath(blocksDir string) index.BlockIndexPath {
	return func(blockID uint64) string {
		return block.IdxFilePath(blocksDir, blockID)
	}
}

func docNames(docs []index.ResolvedDocument) []string {
	names := make([]string, 0, len(docs))
	for _, doc := range docs {
		names = append(names, doc.DocName)
	}
	return names
}
