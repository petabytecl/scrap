package block_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestBlockWriterHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewBlockWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if len(data) < block.BlockHeaderSize {
		t.Fatalf("block too small: %d bytes", len(data))
	}
	if block.BlockHeaderSize != 32 {
		t.Fatalf("BlockHeaderSize: got %d, want 32", block.BlockHeaderSize)
	}

	magic := string(data[0:4])
	if magic != "SCRP" {
		t.Fatalf("magic: got %q, want SCRP", magic)
	}

	version := binary.LittleEndian.Uint16(data[4:6])
	if version != 1 {
		t.Fatalf("version: got %d, want 1", version)
	}
}

func TestBlockWriterAppendDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.blk")

	w, err := block.NewBlockWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
	}

	doc := bytes.Repeat([]byte("A"), 1024)
	result, err := w.AppendDocument("tx-001", "invoice.xml", "application/xml", bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}

	if result.Size != 1024 {
		t.Fatalf("Size: got %d, want 1024", result.Size)
	}
	if result.SHA256Checksum == "" {
		t.Fatal("SHA256Checksum should not be empty")
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

	w, err := block.NewBlockWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
	}

	for i := 0; i < 5; i++ {
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

	w, err := block.NewBlockWriter(path, 1, 100)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
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
