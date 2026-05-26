package block_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func TestIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	now := time.Now().Truncate(time.Microsecond)

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	entries := []block.IndexEntry{
		{
			TransactionID:  "tx-001",
			DocName:        "invoice.xml",
			ContentType:    "application/xml",
			CreatedAt:      now,
			FirstFrameOff:  32,
			FrameCount:     1,
			TotalBytes:     1024,
			SHA256Checksum: "aabbccdd",
		},
		{
			TransactionID:  "tx-001",
			DocName:        "receipt.pdf",
			ContentType:    "application/pdf",
			CreatedAt:      now,
			FirstFrameOff:  1074,
			FrameCount:     3,
			TotalBytes:     196608,
			SHA256Checksum: "11223344",
		},
	}

	for _, e := range entries {
		if err := iw.Append(e); err != nil {
			t.Fatalf("Append %s: %v", e.DocName, err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	got, err := ir.Find("tx-001", "invoice.xml")
	if err != nil {
		t.Fatalf("Find invoice.xml: %v", err)
	}
	if got.TotalBytes != 1024 {
		t.Fatalf("TotalBytes: got %d, want 1024", got.TotalBytes)
	}
	if got.ContentType != "application/xml" {
		t.Fatalf("ContentType: got %q, want application/xml", got.ContentType)
	}
	if got.SHA256Checksum != "aabbccdd" {
		t.Fatalf("SHA256Checksum: got %q, want aabbccdd", got.SHA256Checksum)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, now)
	}
}

func TestIndexFindNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-001",
		DocName:       "a.xml",
		ContentType:   "text/xml",
		CreatedAt:     time.Now(),
		FirstFrameOff: 32,
		FrameCount:    1,
		TotalBytes:    100,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	_, err = ir.Find("tx-001", "nonexistent.xml")
	if err == nil {
		t.Fatal("expected error for missing document")
	}
}

func TestIndexAllEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	for i := range 100 {
		if err := iw.Append(block.IndexEntry{
			TransactionID: "tx-bulk",
			DocName:       fmt.Sprintf("doc-%03d.xml", i),
			ContentType:   "text/xml",
			CreatedAt:     time.Now(),
			FirstFrameOff: int64(i * 1000),
			FrameCount:    1,
			TotalBytes:    500,
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer ir.Close()

	all := ir.FindByTransaction("tx-bulk")
	if len(all) != 100 {
		t.Fatalf("FindByTransaction: got %d entries, want 100", len(all))
	}

	for i, e := range all {
		want := fmt.Sprintf("doc-%03d.xml", i)
		if e.DocName != want {
			t.Fatalf("entry %d: got %q, want %q", i, e.DocName, want)
		}
	}
}

func TestIndexCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	if err := os.WriteFile(path, []byte("garbage"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := block.OpenIndexReader(path)
	if err == nil {
		t.Fatal("expected error for corrupt idx file")
	}
}
