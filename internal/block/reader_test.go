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

func writeTestBlock(t *testing.T, dir string) (blkPath, idxPath string) {
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

	docs := []struct {
		txID, name, ct string
		data           []byte
	}{
		{"tx-001", "small.xml", "text/xml", bytes.Repeat([]byte("A"), 512)},
		{"tx-001", "medium.pdf", "application/pdf", bytes.Repeat([]byte("B"), block.MaxFramePayload+100)},
	}

	for _, d := range docs {
		result, err := bw.AppendDocument(d.txID, d.name, d.ct, bytes.NewReader(d.data))
		if err != nil {
			t.Fatalf("AppendDocument %s: %v", d.name, err)
		}
		if err := iw.Append(block.IndexEntry{
			TransactionID:  d.txID,
			DocName:        d.name,
			ContentType:    d.ct,
			CreatedAt:      time.Now(),
			FirstFrameOff:  result.FirstFrameOffset,
			FrameCount:     result.FrameCount,
			TotalBytes:     result.Size,
			SHA256Checksum: result.SHA256Checksum,
		}); err != nil {
			t.Fatalf("Append index %s: %v", d.name, err)
		}
	}

	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}

	return blkPath, idxPath
}

func TestBlockReaderSmallDoc(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeTestBlock(t, dir)

	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	entry, err := ir.Find("tx-001", "small.xml")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	rc, err := block.ReadDocument(blkPath, entry)
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	want := bytes.Repeat([]byte("A"), 512)
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestBlockReaderMultiFrameDoc(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeTestBlock(t, dir)

	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	entry, err := ir.Find("tx-001", "medium.pdf")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	rc, err := block.ReadDocument(blkPath, entry)
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	want := bytes.Repeat([]byte("B"), block.MaxFramePayload+100)
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestBlockReaderCorruptPayload(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeTestBlock(t, dir)

	data, err := os.ReadFile(blkPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data[block.BlockHeaderSize+block.FrameHeaderSize+5] ^= 0xFF
	if err := os.WriteFile(blkPath, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	entry, _ := ir.Find("tx-001", "small.xml")
	_, err = block.ReadDocument(blkPath, entry)
	if err == nil {
		t.Fatal("expected error on corrupt block")
	}
}
