package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestFindDocumentsRejectsInvalidLookupBeforeStore(t *testing.T) {
	store := &findDocumentsStore{}
	srv := newFindDocumentsTestServer(store)

	_, err := srv.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-1",
		TenantId:      invalidTenantID(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("FindDocuments status = %s, want %s (err=%v)", status.Code(err), codes.InvalidArgument, err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestFindDocumentsCanceledContextReturnsBeforeStore(t *testing.T) {
	store := &findDocumentsStore{}
	srv := newFindDocumentsTestServer(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := srv.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-canceled",
	})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("FindDocuments status = %s, want %s (err=%v)", status.Code(err), codes.Canceled, err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestFindDocumentsStoreContextErrorPreservesCanceledStatus(t *testing.T) {
	store := &findDocumentsStore{err: context.Canceled}
	srv := newFindDocumentsTestServer(store)

	_, err := srv.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-store-canceled",
	})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("FindDocuments status = %s, want %s (err=%v)", status.Code(err), codes.Canceled, err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}

func TestFindDocumentsStoreDeadlineErrorPreservesDeadlineExceededStatus(t *testing.T) {
	store := &findDocumentsStore{err: context.DeadlineExceeded}
	srv := newFindDocumentsTestServer(store)

	_, err := srv.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-store-deadline",
	})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("FindDocuments status = %s, want %s (err=%v)", status.Code(err), codes.DeadlineExceeded, err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}

func newFindDocumentsTestServer(store storeapi.Store) *documentServer {
	return &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
		logger:    slog.New(slog.DiscardHandler),
	}
}

func invalidTenantID() string {
	return string(make([]byte, storeapi.MaxTenantIDBytes+1))
}

type findDocumentsStore struct {
	calls int
	err   error
}

func (s *findDocumentsStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, errors.New("unexpected WriteDocument call")
}

func (s *findDocumentsStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, errors.New("unexpected HeadDocument call")
}

func (s *findDocumentsStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, errors.New("unexpected ReadDocument call")
}

func (s *findDocumentsStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}
