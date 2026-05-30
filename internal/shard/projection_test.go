package shard

import (
	"os"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestApplyCommitDocumentToleratesProjectionAheadOfBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}
	if err := idx.Put("tx-replay", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-replay",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	})
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
}

func TestApplyCommitDocumentWritesHistoricalBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-historical",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	})
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}

	resolved, err := s.projectionResolver().ResolveDocument("tx-historical", "doc.xml")
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if resolved.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", resolved.BlockID)
	}
}

func TestApplyCommitDocumentWritesCurrentBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	blockWriter, err := block.NewWriter(block.FilePath(dir, 1), 1, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = blockWriter.Close() })
	idxWriter, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	t.Cleanup(func() { _ = idxWriter.Close() })

	s := &Shard{
		blocksDir:   dir,
		idx:         idx,
		blockWriter: blockWriter,
		idxWriter:   idxWriter,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-current",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	})
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
	if err := idxWriter.Close(); err != nil {
		t.Fatalf("Close idx writer: %v", err)
	}
	s.idxWriter = nil

	resolved, err := s.projectionResolver().ResolveDocument("tx-current", "doc.xml")
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if resolved.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", resolved.BlockID)
	}
}

func TestApplyCommitDocumentSkipsExistingHistoricalIndexEntry(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	path := block.IdxFilePath(dir, 1)
	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-existing", DocName: "doc.xml"}); err != nil {
		t.Fatalf("Append existing entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close existing index: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-existing",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	})
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("index size changed: got %d, want %d", after.Size(), before.Size())
	}
}
