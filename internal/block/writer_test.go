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
