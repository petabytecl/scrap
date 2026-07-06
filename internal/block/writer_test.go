package block_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestBlockWriterHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test reads file it just created in a temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if len(data) < block.HeaderSize {
		t.Fatalf("block too small: %d bytes", len(data))
	}
	if block.HeaderSize != 40 {
		t.Fatalf("HeaderSize: got %d, want 40", block.HeaderSize)
	}

	magic := string(data[0:4])
	if magic != "SCRP" {
		t.Fatalf("magic: got %q, want SCRP", magic)
	}

	version := binary.LittleEndian.Uint16(data[4:6])
	if version != 1 {
		t.Fatalf("version: got %d, want 1", version)
	}

	headerLen := binary.LittleEndian.Uint16(data[6:8])
	if headerLen != 40 {
		t.Fatalf("header_len: got %d, want 40", headerLen)
	}

	tab := crc32.MakeTable(crc32.Castagnoli)
	headerCRC := binary.LittleEndian.Uint32(data[36:40])
	expectedCRC := crc32.Checksum(data[0:36], tab)
	if headerCRC != expectedCRC {
		t.Fatalf("header CRC mismatch: got %08x, want %08x", headerCRC, expectedCRC)
	}
}

func TestBlockWriterAppendDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	doc := bytes.Repeat([]byte("A"), 1024)
	result, err := w.AppendDocument("tx-001", "invoice.xml", "application/xml", bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}

	if result.Size != 1024 {
		t.Fatalf("Size: got %d, want 1024", result.Size)
	}
	if result.SHA256 == [32]byte{} {
		t.Fatal("SHA256 should not be empty")
	}
	if result.FrameCount == 0 {
		t.Fatal("FrameCount should be > 0")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBlockWriterMultipleDocuments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	for i := range 5 {
		doc := bytes.Repeat([]byte{byte('A' + i)}, 512)
		_, err := w.AppendDocument("tx-001", "doc-"+string(rune('a'+i)), "text/plain", bytes.NewReader(doc))
		if err != nil {
			t.Fatalf("AppendDocument %d: %v", i, err)
		}
	}

	if w.DocCount() != 5 {
		t.Fatalf("DocCount: got %d, want 5", w.DocCount())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBlockWriterMultiFrameDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	doc := bytes.Repeat([]byte("X"), block.MaxFramePayload*3+100)
	result, err := w.AppendDocument("tx-002", "large.pdf", "application/pdf", bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}

	if result.FrameCount != 4 {
		t.Fatalf("FrameCount: got %d, want 4 (3 full + 1 partial)", result.FrameCount)
	}
	if result.Size != int64(block.MaxFramePayload*3+100) {
		t.Fatalf("Size: got %d, want %d", result.Size, block.MaxFramePayload*3+100)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBlockWriterExactMultipleFramePayload(t *testing.T) {
	tests := []struct {
		name   string
		frames int
	}{
		{name: "single frame", frames: 1},
		{name: "two frames", frames: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appendExactMultipleDocument(t, tt.frames)
		})
	}
}

func appendExactMultipleDocument(t *testing.T, frames int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	doc := bytes.Repeat([]byte("E"), block.MaxFramePayload*frames)
	result, err := w.AppendDocument("tx-003", "exact.bin", "application/octet-stream", bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if int(result.FrameCount) != frames {
		t.Fatalf("FrameCount: got %d, want %d", result.FrameCount, frames)
	}
	if result.Size != int64(block.MaxFramePayload*frames) {
		t.Fatalf("Size: got %d, want %d", result.Size, block.MaxFramePayload*frames)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The final frame must carry a last-frame flag or the block can never be
	// reopened for appending.
	w2, err := block.OpenWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("OpenWriter after exact-multiple document: %v", err)
	}
	if w2.DocCount() != 1 {
		t.Fatalf("DocCount after reopen: got %d, want 1", w2.DocCount())
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close reopened writer: %v", err)
	}
}

func TestBlockWriterRejectsEmptyDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	if _, err := w.AppendDocument("tx-004", "empty.bin", "text/plain", bytes.NewReader(nil)); err == nil {
		t.Fatal("AppendDocument with empty body succeeded, want error")
	}
	if _, err := w.AppendDocumentFrames("tx-004", "empty.bin", "text/plain", block.DocumentFrames{}); err == nil {
		t.Fatal("AppendDocumentFrames with no payloads succeeded, want error")
	}

	// A rejected empty document must not consume a doc_seq: the next append
	// must produce a block that reopens cleanly.
	if _, err := w.AppendDocument("tx-004", "ok.bin", "text/plain", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("AppendDocument after rejected empty document: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	w2, err := block.OpenWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close reopened writer: %v", err)
	}
}

func TestBlockWriterTruncateBeyondEndRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.Truncate(w.Offset() + 1); err == nil {
		t.Fatal("Truncate beyond end succeeded, want error")
	}
}

func appendTruncateTestDoc(t *testing.T, w *block.Writer, name string, fill byte) {
	t.Helper()
	body := bytes.NewReader(bytes.Repeat([]byte{fill}, 128))
	if _, err := w.AppendDocument("tx", name, "text/plain", body); err != nil {
		t.Fatalf("AppendDocument %s: %v", name, err)
	}
}

func TestBlockWriterTruncateRollsBackDocCounters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Commit document A at doc_seq 0.
	appendTruncateTestDoc(t, w, "a", 'A')

	// Append document B, then abort it by truncating back to the boundary, as
	// the leader/peer write-abort paths do on a rejected write.
	boundary := w.Offset()
	appendTruncateTestDoc(t, w, "b", 'B')
	if err := w.Truncate(boundary); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if w.DocCount() != 1 {
		t.Fatalf("DocCount after truncate: got %d, want 1", w.DocCount())
	}

	// The next committed document must reuse the doc_seq freed by the abort so
	// its frame DocSeq stays contiguous with its .idx position; a leaked
	// counter would write document C at doc_seq 2 over position 1.
	appendTruncateTestDoc(t, w, "c", 'C')
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// OpenWriter re-scans the Frames and rejects any doc_seq gap, so a leaked
	// counter fails this reopen.
	reopened, err := block.OpenWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("OpenWriter after truncate+append: %v", err)
	}
	if reopened.DocCount() != 2 {
		t.Fatalf("reopened DocCount: got %d, want 2", reopened.DocCount())
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close reopened: %v", err)
	}
}

func TestOpenWriterRejectsInvalidFrameFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // test-owned temp file.
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if err := block.WriteFrame(f, block.FrameHeader{
		DocSeq:   0,
		FrameSeq: 0,
		Flags:    0,
	}, []byte("bad flags")); err != nil {
		_ = f.Close()
		t.Fatalf("WriteFrame: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close corrupt writer: %v", err)
	}

	if _, err := block.OpenWriter(path, 1, 100); err == nil {
		t.Fatal("OpenWriter succeeded, want invalid frame flags error")
	}
}
