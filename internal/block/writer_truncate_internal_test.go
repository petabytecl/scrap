package block

import (
	"bytes"
	"path/filepath"
	"testing"
)

// #471: Truncate's post-scan desync arms. Truncating into the middle of a
// frame leaves a torn tail that scanWriterState must reject, so the writer
// never resumes appending at a mid-frame offset.
func TestTruncateIntoFrameMiddleFailsClosed(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "00000000000000c8.blk")
	w, err := NewWriter(blkPath, 1, 200)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.AppendDocument("tx-trunc", "a.txt", "text/plain", bytes.NewReader(bytes.Repeat([]byte("A"), 512))); err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}

	if err := w.Truncate(w.Offset() - 7); err == nil {
		t.Fatal("Truncate into the middle of a frame succeeded, want scan failure")
	}
}

func TestTruncateBoundsRejected(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "00000000000000c9.blk")
	w, err := NewWriter(blkPath, 1, 201)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.Truncate(int64(HeaderSize) - 1); err == nil {
		t.Fatal("Truncate before header succeeded")
	}
	if err := w.Truncate(w.Offset() + 1); err == nil {
		t.Fatal("Truncate beyond end succeeded")
	}
}
