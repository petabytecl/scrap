package block_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

//nolint:unparam // idxPath is created as a side-effect; callers only need blkPath and entry
func writeSingleDocBlock(t *testing.T, dir string, data []byte) (blkPath, idxPath string, entry block.IndexEntry) {
	t.Helper()
	blkPath = filepath.Join(dir, "test.blk")
	idxPath = filepath.Join(dir, "test.idx")

	bw, err := block.NewBlockWriter(blkPath, 1, 100)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	result, err := bw.AppendDocument("tx", "doc.bin", "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}

	entry = block.IndexEntry{
		TransactionID: "tx",
		DocName:       "doc.bin",
		ContentType:   "application/octet-stream",
		CreatedAt:     time.Now(),
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}
	if err := iw.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}
	return blkPath, idxPath, entry
}

func TestTwoPassReadCorrect(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("good"), 1024)
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	rc, err := block.ReadDocumentTwoPass(blkPath, entry)
	if err != nil {
		t.Fatalf("ReadDocumentTwoPass: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = rc.Close()

	if !bytes.Equal(got, data) {
		t.Fatalf("content mismatch: got %d bytes", len(got))
	}
}

func TestTwoPassCorruptPayload(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("A"), 512)
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	raw, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	raw[block.BlockHeaderSize+block.FrameHeaderSize+10] ^= 0xFF
	if err := os.WriteFile(blkPath, raw, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = block.ReadDocumentTwoPass(blkPath, entry)
	if err == nil {
		t.Fatal("expected error on corrupt payload")
	}
}

func TestTwoPassCorruptSHA256(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("B"), 512)
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	entry.SHA256[0] ^= 0xFF

	_, err := block.ReadDocumentTwoPass(blkPath, entry)
	if err == nil {
		t.Fatal("expected SHA-256 mismatch")
	}
}

func TestTwoPassTruncatedBlock(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("C"), 512)
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	raw, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(blkPath, raw[:block.BlockHeaderSize+block.FrameHeaderSize+5], 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = block.ReadDocumentTwoPass(blkPath, entry)
	if err == nil {
		t.Fatal("expected error on truncated block")
	}
}

func TestTwoPassMultiFrame(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("D"), block.MaxFramePayload*3+100)
	blkPath, _, entry := writeSingleDocBlock(t, dir, data)

	rc, err := block.ReadDocumentTwoPass(blkPath, entry)
	if err != nil {
		t.Fatalf("ReadDocumentTwoPass: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = rc.Close()

	if !bytes.Equal(got, data) {
		t.Fatalf("multi-frame content mismatch: got %d bytes, want %d", len(got), len(data))
	}
}
