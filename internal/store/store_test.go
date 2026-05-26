package store_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/store"
)

type stubStore struct{}

func (s *stubStore) WriteDocument(_ context.Context, _, _, _, _ string, _ io.Reader) (store.WriteResult, error) {
	return store.WriteResult{}, nil
}

func (s *stubStore) HeadDocument(_ context.Context, _, _ string) (store.DocumentMeta, error) {
	return store.DocumentMeta{}, nil
}

func (s *stubStore) ReadDocument(_ context.Context, _, _ string) (io.ReadCloser, store.DocumentMeta, error) {
	return nil, store.DocumentMeta{}, nil
}

func (s *stubStore) FindDocuments(_ context.Context, _ string) ([]store.DocumentMeta, error) {
	return nil, nil
}

var _ store.Store = (*stubStore)(nil)

func TestStoreInterfaceCompliance(t *testing.T) {
	var s store.Store = &stubStore{}
	if s == nil {
		t.Fatal("stubStore should satisfy Store interface")
	}
}

func TestWriteResultFields(t *testing.T) {
	r := store.WriteResult{
		SHA256Checksum: "abc123",
		Size:           1024,
		CreatedAt:      time.Now(),
	}
	if r.SHA256Checksum == "" {
		t.Fatal("SHA256Checksum should be set")
	}
	if r.Size != 1024 {
		t.Fatal("Size should be 1024")
	}
	if r.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}

func TestDocumentMetaFields(t *testing.T) {
	m := store.DocumentMeta{
		Name:           "invoice.xml",
		ContentType:    "application/xml",
		Size:           2048,
		SHA256Checksum: "def456",
		CreatedAt:      time.Now(),
	}
	if m.Name == "" {
		t.Fatal("Name should be set")
	}
	if m.ContentType == "" {
		t.Fatal("ContentType should be set")
	}
	if m.Size != 2048 {
		t.Fatal("Size should be 2048")
	}
	if m.SHA256Checksum == "" {
		t.Fatal("SHA256Checksum should be set")
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}
