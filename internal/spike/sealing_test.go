package spike_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/spike"
)

func TestBlockSealAtThreshold(t *testing.T) {
	dir := t.TempDir()
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: 64 * 1024})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	for i := range 3 {
		doc := bytes.Repeat([]byte{byte('A' + i)}, 30*1024)
		_, err := s.WriteDocument(ctx, "tx-seal", fmt.Sprintf("doc-%d.xml", i), "text/xml", "", bytes.NewReader(doc))
		if err != nil {
			t.Fatalf("WriteDocument %d: %v", i, err)
		}
	}

	blks, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	if len(blks) < 2 {
		t.Fatalf("expected at least 2 block files after sealing, got %d", len(blks))
	}
}

func TestMultiBlockTransactionFindDocuments(t *testing.T) {
	dir := t.TempDir()
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: 64 * 1024})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	for i := range 4 {
		doc := bytes.Repeat([]byte{byte('A' + i)}, 30*1024)
		_, err := s.WriteDocument(ctx, "tx-cross", fmt.Sprintf("doc-%d.xml", i), "text/xml", "", bytes.NewReader(doc))
		if err != nil {
			t.Fatalf("WriteDocument %d: %v", i, err)
		}
	}

	docs, err := s.FindDocuments(ctx, "tx-cross")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 4 {
		t.Fatalf("FindDocuments: got %d docs, want 4", len(docs))
	}
}

func TestLargeDocumentExceedsSealThreshold(t *testing.T) {
	dir := t.TempDir()
	sealSize := int64(64 * 1024)
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: sealSize})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	largeDoc := bytes.Repeat([]byte("X"), int(sealSize*2))
	result, err := s.WriteDocument(ctx, "tx-large", "big.bin", "application/octet-stream", "", bytes.NewReader(largeDoc))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if result.Size != sealSize*2 {
		t.Fatalf("Size: got %d, want %d", result.Size, sealSize*2)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-large", "big.bin")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	got, _ := readAll(rc)
	if !bytes.Equal(got, largeDoc) {
		t.Fatalf("content mismatch: got %d bytes", len(got))
	}

	smallDoc := []byte("after seal")
	_, err = s.WriteDocument(ctx, "tx-after", "small.txt", "text/plain", "", bytes.NewReader(smallDoc))
	if err != nil {
		t.Fatalf("WriteDocument after seal: %v", err)
	}

	blks, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	if len(blks) < 2 {
		t.Fatalf("expected at least 2 blocks (large doc seals), got %d", len(blks))
	}
}

func TestRestartAllocatesNextBlockID(t *testing.T) {
	dir := t.TempDir()
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: 64 * 1024 * 1024})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, err = s.WriteDocument(context.Background(), "tx-restart", "a.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	blksBeforeClose, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	s.Close()

	s2, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: 64 * 1024 * 1024})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer s2.Close()

	blksAfterRestart, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	if len(blksAfterRestart) <= len(blksBeforeClose) {
		t.Fatalf("restart should open a new block: had %d, now %d", len(blksBeforeClose), len(blksAfterRestart))
	}

	rc, _, err := s2.ReadDocument(context.Background(), "tx-restart", "a.xml")
	if err != nil {
		t.Fatalf("ReadDocument after restart: %v", err)
	}
	got, _ := readAll(rc)
	if string(got) != "data" {
		t.Fatalf("content after restart: got %q", got)
	}
}

func TestBlockFilenamesAreFixedWidthHex(t *testing.T) {
	dir := t.TempDir()
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: 64 * 1024 * 1024})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	_, err = s.WriteDocument(context.Background(), "tx-hex", "a.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "blocks"))
	for _, e := range entries {
		name := e.Name()
		if len(name) != 20 { // 16 hex chars + ".blk" or ".idx"
			t.Fatalf("filename not fixed-width: %q", name)
		}
	}
}

func readAll(rc interface {
	Read([]byte) (int, error)
	Close() error
},
) ([]byte, error) {
	defer rc.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(rc)
	return buf.Bytes(), err
}
