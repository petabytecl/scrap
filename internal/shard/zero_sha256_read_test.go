package shard_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestP0ReadDocumentVisibleAllZeroSHA256FailsClosedWithoutReader(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("visible zero sha256 "), 16)

	if _, err := s.WriteDocument(ctx, "tx-zero-sha256", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	idxPath := block.IdxFilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	rewriteZeroSHA256ShardIndexEntry(t, idxPath, "tx-zero-sha256", "doc.xml")

	rc, meta, err := s.ReadDocument(ctx, "tx-zero-sha256", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with all-zero SHA-256 metadata")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on all-zero SHA-256 metadata", meta)
	}
}

func rewriteZeroSHA256ShardIndexEntry(t *testing.T, idxPath, txID, docName string) {
	t.Helper()

	entries := readZeroSHA256ShardIndexEntries(t, idxPath)
	found := false
	for i := range entries {
		if entries[i].TransactionID == txID && entries[i].DocName == docName {
			entries[i].SHA256 = [32]byte{}
			found = true
		}
	}
	if !found {
		t.Fatalf("index entry %s/%s not found", txID, docName)
	}
	rewriteZeroSHA256ShardIndex(t, idxPath, entries)
}

func readZeroSHA256ShardIndexEntries(t *testing.T, idxPath string) []block.IndexEntry {
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

func rewriteZeroSHA256ShardIndex(t *testing.T, idxPath string, entries []block.IndexEntry) {
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
