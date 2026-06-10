package spike_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/petabytecl/scrap/internal/spike"
	"github.com/petabytecl/scrap/internal/store"
)

var _ store.Store = (*spike.Store)(nil)

func openTestStore(t *testing.T) *spike.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := spike.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWriteAndRead(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("invoice data "), 100)
	result, err := s.WriteDocument(ctx, "tx-001", "invoice.xml", "application/xml", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	if result.Size != int64(len(content)) {
		t.Fatalf("Size: got %d, want %d", result.Size, len(content))
	}
	if result.SHA256 == [32]byte{} {
		t.Fatal("SHA256 should not be zero")
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-001", "invoice.xml")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if meta.SHA256 != result.SHA256 {
		t.Fatal("SHA256 mismatch between write and read")
	}
	if meta.ContentType != "application/xml" {
		t.Fatalf("ContentType: got %q, want application/xml", meta.ContentType)
	}
}

func TestDuplicateWrite(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	content := []byte("first write")
	_, err := s.WriteDocument(ctx, "tx-dup", "a.xml", "text/xml", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	_, err = s.WriteDocument(ctx, "tx-dup", "a.xml", "text/xml", "", bytes.NewReader(content))
	if err == nil {
		t.Fatal("expected error on duplicate write")
	}
}

func TestWriteRejectsInvalidMetadataAndZeroByteDocument(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-\ninvalid", "a.xml", "text/xml", "", bytes.NewReader([]byte("payload")))
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("invalid metadata error = %v, want ErrInvalidArgument", err)
	}

	_, err = s.WriteDocument(ctx, "tx-empty", "a.xml", "text/xml", "", bytes.NewReader(nil))
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("empty document error = %v, want ErrInvalidArgument", err)
	}
}

func TestReadNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, _, err := s.ReadDocument(ctx, "nonexistent", "nope.xml")
	if err == nil {
		t.Fatal("expected error for missing document")
	}
}

func TestMultiDocTransaction(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		name := string(rune('a'+i)) + ".xml"
		content := bytes.Repeat([]byte{byte('A' + i)}, 256)
		_, err := s.WriteDocument(ctx, "tx-multi", name, "text/xml", "", bytes.NewReader(content))
		if err != nil {
			t.Fatalf("WriteDocument %s: %v", name, err)
		}
	}

	docs, err := s.FindDocuments(ctx, "tx-multi")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 5 {
		t.Fatalf("FindDocuments: got %d docs, want 5", len(docs))
	}
}
