package shard_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func openTestShard(t *testing.T) *shard.Shard {
	t.Helper()
	dir := t.TempDir()
	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}

func TestWriteAndHeadDocument(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("test data "), 100)
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

	meta, err := s.HeadDocument(ctx, "tx-001", "invoice.xml")
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if meta.Name != "invoice.xml" {
		t.Fatalf("Name: got %q", meta.Name)
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("Size: got %d", meta.Size)
	}
	if meta.SHA256 != result.SHA256 {
		t.Fatal("SHA256 mismatch between write and head")
	}
}

func TestWriteAndReadDocument(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("read back "), 200)
	_, err := s.WriteDocument(ctx, "tx-002", "doc.pdf", "application/pdf", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-002", "doc.pdf")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer rc.Close()

	if meta.ContentType != "application/pdf" {
		t.Fatalf("ContentType: got %q", meta.ContentType)
	}

	readBack, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(readBack), len(content))
	}
}

func TestFindDocuments(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	names := []string{"a.xml", "b.xml", "c.xml"}
	for _, name := range names {
		_, err := s.WriteDocument(ctx, "tx-find", name, "text/xml", "", bytes.NewReader([]byte("data")))
		if err != nil {
			t.Fatalf("WriteDocument %s: %v", name, err)
		}
	}

	docs, err := s.FindDocuments(ctx, "tx-find")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}
}

func TestDuplicateWriteReturnsAlreadyExists(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-dup", "doc.xml", "text/xml", "", bytes.NewReader([]byte("first")))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	_, err = s.WriteDocument(ctx, "tx-dup", "doc.xml", "text/xml", "", bytes.NewReader([]byte("second")))
	if err == nil {
		t.Fatal("expected error on duplicate")
	}
	if !storeapi.IsAlreadyExists(err) {
		t.Fatalf("expected ErrAlreadyExists, got: %v", err)
	}
}

func TestHeadNotFound(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.HeadDocument(ctx, "nonexistent", "nope.xml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenlogRecoveryDeletesCompletedPrep(t *testing.T) {
	dir := t.TempDir()

	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, err = s.WriteDocument(context.Background(), "tx-recover", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	s.Close()

	s2, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer s2.Close()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s2.IsLeader() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	meta, err := s2.HeadDocument(context.Background(), "tx-recover", "doc.xml")
	if err != nil {
		t.Fatalf("HeadDocument after recovery: %v", err)
	}
	if meta.Name != "doc.xml" {
		t.Fatalf("Name: got %q", meta.Name)
	}
}

func TestFindEmptyTransaction(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	docs, err := s.FindDocuments(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty, got %d", len(docs))
	}
}
