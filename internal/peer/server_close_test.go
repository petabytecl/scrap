package peer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

// TestServerCloseFlushesAndClosesWriters exercises the graceful-shutdown path:
// a just-completed replicate receive leaves an open block/index writer, and
// Close must flush and close them. It also asserts Close is idempotent.
func TestServerCloseFlushesAndClosesWriters(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(dir)

	const blockID uint64 = 1
	bs, err := s.getOrCreateBlock(blockID, 7)
	if err != nil {
		t.Fatalf("getOrCreateBlock: %v", err)
	}

	res, err := bs.writer.AppendDocument("tx-1", "doc-1", "text/plain", strings.NewReader("payload-bytes"))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := bs.idxWriter.Append(block.IndexEntry{
		TransactionID: "tx-1",
		DocName:       "doc-1",
		ContentType:   "text/plain",
		CreatedAt:     time.UnixMicro(0),
		FirstFrameOff: res.FirstFrameOffset,
		FrameCount:    res.FrameCount,
		TotalBytes:    res.Size,
		SHA256:        res.SHA256,
	}); err != nil {
		t.Fatalf("idx Append: %v", err)
	}

	// Hold a reference so we can prove the writer is closed after Close.
	w := bs.writer

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 1) The block writer is closed: further appends are rejected.
	if _, err := w.AppendDocument("tx-1", "doc-2", "text/plain", strings.NewReader("x")); err == nil {
		t.Fatal("expected AppendDocument to fail after Close, got nil")
	}

	// 2) The index was flushed and is a valid, readable index on disk.
	idxPath := filepath.Join(dir, fmt.Sprintf("%016x.idx", blockID))
	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader after Close: %v", err)
	}
	defer func() { _ = ir.Close() }()
	if _, err := ir.Find("tx-1", "doc-1"); err != nil {
		t.Fatalf("index entry not found after Close: %v", err)
	}

	// 3) Close is idempotent: a second call is a no-op and does not panic.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
