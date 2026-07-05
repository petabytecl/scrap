package block_test

import (
	"errors"
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
			TransactionID:      "tx-001",
			DocName:            "invoice.xml",
			ContentType:        "application/xml",
			CreatedAt:          now,
			FirstFrameOff:      40,
			FrameCount:         1,
			TotalBytes:         1024,
			SHA256:             sha,
			EncryptionEnvelope: []byte(`{"version":1,"wrapped_data_key":"vault:v1:test"}`),
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
	if string(got.EncryptionEnvelope) != `{"version":1,"wrapped_data_key":"vault:v1:test"}` {
		t.Fatalf("EncryptionEnvelope: got %q", got.EncryptionEnvelope)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, now)
	}
}

func TestReplaceDocumentEnvelopeRewritesOnlyTargetEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")
	now := time.Now().Truncate(time.Microsecond)

	writeBlockIndexEntries(t, path,
		block.IndexEntry{
			TransactionID:      "tx-001",
			DocName:            "invoice.xml",
			ContentType:        "application/xml",
			CreatedAt:          now,
			FirstFrameOff:      40,
			FrameCount:         1,
			TotalBytes:         1024,
			SHA256:             [32]byte{0xAA},
			EncryptionEnvelope: []byte(`{"version":1,"wrapped_data_key":"vault:v1:old"}`),
		},
		block.IndexEntry{
			TransactionID:      "tx-001",
			DocName:            "receipt.pdf",
			ContentType:        "application/pdf",
			CreatedAt:          now,
			FirstFrameOff:      1074,
			FrameCount:         3,
			TotalBytes:         2048,
			SHA256:             [32]byte{0xBB},
			EncryptionEnvelope: []byte(`{"version":1,"wrapped_data_key":"vault:v1:keep"}`),
		},
	)

	updated, changed, err := block.ReplaceDocumentEnvelope(path, "tx-001", "invoice.xml", []byte(`{"version":1,"wrapped_data_key":"vault:v2:new"}`))
	if err != nil {
		t.Fatalf("ReplaceDocumentEnvelope: %v", err)
	}
	if !changed {
		t.Fatal("ReplaceDocumentEnvelope changed = false, want true")
	}
	if string(updated.EncryptionEnvelope) != `{"version":1,"wrapped_data_key":"vault:v2:new"}` {
		t.Fatalf("updated envelope = %q", updated.EncryptionEnvelope)
	}
	if updated.ContentType != "application/xml" || updated.TotalBytes != 1024 || updated.SHA256 != [32]byte{0xAA} {
		t.Fatalf("updated metadata drifted: %+v", updated)
	}

	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()
	kept, err := ir.Find("tx-001", "receipt.pdf")
	if err != nil {
		t.Fatalf("Find receipt.pdf: %v", err)
	}
	if string(kept.EncryptionEnvelope) != `{"version":1,"wrapped_data_key":"vault:v1:keep"}` {
		t.Fatalf("non-target envelope = %q", kept.EncryptionEnvelope)
	}
}

func TestReplaceDocumentEnvelopeNoopsWhenEnvelopeMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")
	envelope := []byte(`{"version":1,"wrapped_data_key":"vault:v1:same"}`)
	writeBlockIndexEntries(t, path, block.IndexEntry{
		TransactionID:      "tx-001",
		DocName:            "invoice.xml",
		ContentType:        "application/xml",
		CreatedAt:          time.Now().Truncate(time.Microsecond),
		FirstFrameOff:      40,
		FrameCount:         1,
		TotalBytes:         1024,
		SHA256:             [32]byte{0xAA},
		EncryptionEnvelope: envelope,
	})

	updated, changed, err := block.ReplaceDocumentEnvelope(path, "tx-001", "invoice.xml", envelope)
	if err != nil {
		t.Fatalf("ReplaceDocumentEnvelope: %v", err)
	}
	if changed {
		t.Fatal("ReplaceDocumentEnvelope changed = true, want false")
	}
	if string(updated.EncryptionEnvelope) != string(envelope) {
		t.Fatalf("updated envelope = %q, want %q", updated.EncryptionEnvelope, envelope)
	}
}

func TestReplaceDocumentEnvelopeMissingDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")
	writeBlockIndexEntries(t, path, block.IndexEntry{
		TransactionID: "tx-001",
		DocName:       "invoice.xml",
		ContentType:   "application/xml",
		CreatedAt:     time.Now().Truncate(time.Microsecond),
		FirstFrameOff: 40,
		FrameCount:    1,
		TotalBytes:    1024,
		SHA256:        [32]byte{0xAA},
	})

	_, _, err := block.ReplaceDocumentEnvelope(path, "tx-001", "missing.xml", []byte(`{"version":1}`))
	if !errors.Is(err, block.ErrDocNotFound) {
		t.Fatalf("ReplaceDocumentEnvelope error = %v, want ErrDocNotFound", err)
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

func TestRepairIndexTailTruncatesPartialAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	writeBlockIndexEntries(t, path, block.IndexEntry{TransactionID: "tx-001", DocName: "a.xml"})
	appendRawIndexTail(t, path, []byte{0x03, 0x00})
	requireUnreadableIndex(t, path)

	if err := block.RepairIndexTail(path); err != nil {
		t.Fatalf("RepairIndexTail: %v", err)
	}
	appendBlockIndexEntry(t, path, block.IndexEntry{TransactionID: "tx-001", DocName: "b.xml"})
	requireBlockIndexDocCount(t, path, "tx-001", 2)
}

func appendBlockIndexEntry(t *testing.T, path string, entry block.IndexEntry) {
	t.Helper()
	iw, err := block.OpenIndexWriter(path)
	if err != nil {
		t.Fatalf("OpenIndexWriter: %v", err)
	}
	if err := iw.Append(entry); err != nil {
		t.Fatalf("Append index entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index writer: %v", err)
	}
}

func writeBlockIndexEntries(t *testing.T, path string, entries ...block.IndexEntry) {
	t.Helper()
	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for _, entry := range entries {
		if err := iw.Append(entry); err != nil {
			t.Fatalf("Append index entry: %v", err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index writer: %v", err)
	}
}

func requireBlockIndexDocCount(t *testing.T, path, txID string, want int) {
	t.Helper()
	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()
	all := ir.FindByTransaction(txID)
	if len(all) != want {
		t.Fatalf("FindByTransaction: got %d entries, want %d", len(all), want)
	}
}

func requireUnreadableIndex(t *testing.T, path string) {
	t.Helper()
	if _, err := block.OpenIndexReader(path); err == nil {
		t.Fatal("expected torn tail to make index unreadable")
	}
}

func appendRawIndexTail(t *testing.T, path string, tail []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("OpenFile append: %v", err)
	}
	if _, err := f.Write(tail); err != nil {
		_ = f.Close()
		t.Fatalf("Write torn tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close torn tail: %v", err)
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
	if err := block.RepairIndexTail(path); err == nil {
		t.Fatal("expected repair to reject committed entry CRC corruption")
	}
}

func TestIndexOversizedEntryLenMidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A corrupt length prefix whose claimed payload still fits in the file is
	// mid-file corruption: it must fail hard without allocating the claim.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // test-owned temp file.
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	corrupt := make([]byte, 4+300008)
	corrupt[0] = 0xE0 // entry_len = 300000, above the sanity cap
	corrupt[1] = 0x93
	corrupt[2] = 0x04
	if _, err := f.Write(corrupt); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close corrupt file: %v", err)
	}

	if _, err := block.OpenIndexReader(path); !errors.Is(err, block.ErrIdxCorrupt) {
		t.Fatalf("OpenIndexReader error: got %v, want ErrIdxCorrupt", err)
	}
	if err := block.RepairIndexTail(path); err == nil {
		t.Fatal("RepairIndexTail repaired mid-file corruption, want error")
	}
}

func TestIndexOversizedEntryLenTornTailRepairable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	entry := block.IndexEntry{
		TransactionID: "tx-torn",
		DocName:       "doc.bin",
		ContentType:   "application/octet-stream",
		CreatedAt:     time.Now(),
		FrameCount:    1,
		TotalBytes:    4,
		SHA256:        [32]byte{1},
	}
	if err := iw.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A garbage length prefix at EOF with no payload behind it is a torn
	// tail and must stay repairable, without a giant allocation.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // test-owned temp file.
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close torn file: %v", err)
	}

	if err := block.RepairIndexTail(path); err != nil {
		t.Fatalf("RepairIndexTail: %v", err)
	}
	ir, err := block.OpenIndexReader(path)
	if err != nil {
		t.Fatalf("OpenIndexReader after repair: %v", err)
	}
	defer func() { _ = ir.Close() }()
	if got := len(ir.Entries()); got != 1 {
		t.Fatalf("entries after repair: got %d, want 1", got)
	}
}

func TestIndexAppendRejectsOversizedEnvelope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	defer func() { _ = iw.Close() }()

	err = iw.Append(block.IndexEntry{
		TransactionID:      "tx-big",
		DocName:            "doc.bin",
		ContentType:        "application/octet-stream",
		CreatedAt:          time.Now(),
		FrameCount:         1,
		TotalBytes:         1,
		SHA256:             [32]byte{1},
		EncryptionEnvelope: make([]byte, 65536),
	})
	if err == nil {
		t.Fatal("Append with 64 KiB envelope succeeded, want error")
	}
}
