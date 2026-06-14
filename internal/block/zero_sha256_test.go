package block_test

import (
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestP0ReadDocumentTwoPassRejectsAllZeroSHA256(t *testing.T) {
	dir := t.TempDir()
	blkPath, _, entry := writeSingleDocBlock(t, dir, []byte("sha256 must be present"))

	entry.SHA256 = [32]byte{}
	rc, err := block.ReadDocumentTwoPass(blkPath, entry)
	if err == nil {
		if rc != nil {
			_ = rc.Close()
		}
		t.Fatal("ReadDocumentTwoPass succeeded with all-zero SHA-256")
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocumentTwoPass returned reader with all-zero SHA-256")
	}
	if !errors.Is(err, block.ErrSHA256Mismatch) {
		t.Fatalf("ReadDocumentTwoPass error = %v, want ErrSHA256Mismatch", err)
	}
}

func TestP0VerifyBlockReportsDocSHA256ForAllZeroPlaintextIndexEntry(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	entries := readZeroSHA256TestIndexEntries(t, idxPath)
	if len(entries) == 0 {
		t.Fatal("expected at least one index entry")
	}
	entries[0].SHA256 = [32]byte{}
	rewriteZeroSHA256TestIndex(t, idxPath, entries)

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if !zeroSHA256TestHasCorruption(result.CorruptFrames, block.CorruptionDocSHA256) {
		t.Fatalf("expected doc_sha256 corruption for all-zero plaintext SHA-256, got %v", result.CorruptFrames)
	}
}

func readZeroSHA256TestIndexEntries(t *testing.T, idxPath string) []block.IndexEntry {
	t.Helper()

	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	entries := append([]block.IndexEntry(nil), ir.Entries()...)
	if err := ir.Close(); err != nil {
		t.Fatalf("Close index reader: %v", err)
	}
	return entries
}

func rewriteZeroSHA256TestIndex(t *testing.T, idxPath string, entries []block.IndexEntry) {
	t.Helper()

	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for _, entry := range entries {
		if err := iw.Append(entry); err != nil {
			_ = iw.Close()
			t.Fatalf("Append rewritten index entry: %v", err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close rewritten index: %v", err)
	}
}

func zeroSHA256TestHasCorruption(frames []block.CorruptFrame, want block.CorruptionType) bool {
	for _, frame := range frames {
		if frame.Type == want {
			return true
		}
	}
	return false
}
