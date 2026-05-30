package block_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func TestIndexRoundTrip(t *testing.T) { //nolint:cyclop // test function with exhaustive assertions
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	now := time.Now().Truncate(time.Microsecond)

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	sha := [32]byte{0xAA, 0xBB, 0xCC}
	entries := []block.IndexEntry{
		{
			TransactionID: "tx-001",
			DocName:       "invoice.xml",
			ContentType:   "application/xml",
			CreatedAt:     now,
			FirstFrameOff: 40,
			FrameCount:    1,
			TotalBytes:    1024,
			SHA256:        sha,
		},
		{
			TransactionID: "tx-001",
			DocName:       "receipt.pdf",
			ContentType:   "application/pdf",
			CreatedAt:     now,
			FirstFrameOff: 1074,
			FrameCount:    3,
			TotalBytes:    196608,
			SHA256:        [32]byte{0x11, 0x22},
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
	defer func() { _ = ir.Close() }()

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
	if got.SHA256 != sha {
		t.Fatalf("SHA256 mismatch")
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
		FirstFrameOff: 40,
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
	defer func() { _ = ir.Close() }()

	_, err = ir.Find("tx-001", "nonexistent.xml")
	if err == nil {
		t.Fatal("expected error for missing document")
	}
}

func TestOpenIndexWriterAppendsExistingIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-001", DocName: "a.xml"}); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close first writer: %v", err)
	}

	iw, err = block.OpenIndexWriter(path)
	if err != nil {
		t.Fatalf("OpenIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-001", DocName: "b.xml"}); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close second writer: %v", err)
	}

	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()
	for _, docName := range []string{"a.xml", "b.xml"} {
		if _, err := ir.Find("tx-001", docName); err != nil {
			t.Fatalf("Find %s: %v", docName, err)
		}
	}
}

func TestOpenIndexWriterMissingFile(t *testing.T) {
	_, err := block.OpenIndexWriter(filepath.Join(t.TempDir(), "missing.idx"))
	if err == nil {
		t.Fatal("expected error for missing index")
	}
}

func TestOpenIndexWriterRejectsBadHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.idx")
	if err := os.WriteFile(path, []byte("not an index"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := block.OpenIndexWriter(path)
	if err == nil {
		t.Fatal("expected error for bad index header")
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
	defer func() { _ = ir.Close() }()

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

func TestIndexCorruptHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	if err := os.WriteFile(path, []byte("garbage_data"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := block.OpenIndexReader(path)
	if err == nil {
		t.Fatal("expected error for corrupt idx file")
	}
}

func TestIndexCorruptEntryCRC(t *testing.T) {
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
		FirstFrameOff: 40,
		FrameCount:    1,
		TotalBytes:    100,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data[20] ^= 0xFF                                        // corrupt entry payload
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = block.OpenIndexReader(path)
	if err == nil {
		t.Fatal("expected CRC error for corrupt entry")
	}
}
