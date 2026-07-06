package store_test

import (
	"context"
	"io"

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

// Compile-time interface compliance check.
var _ store.Store = (*stubStore)(nil)
