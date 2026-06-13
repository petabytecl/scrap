package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestDocumentServerDeniesUnauthorizedOperationsBeforeStore(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	readerCtx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "reader",
		Roles: security.NewRoleSet(security.RoleDocumentReader),
	})
	writerCtx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "writer",
		Roles: security.NewRoleSet(security.RoleDocumentWriter),
	})

	store := &recordingStore{}
	srv := &documentServer{
		store:      store,
		telemetry:  noopTelemetry{},
		logger:     slog.New(slog.DiscardHandler),
		authorizer: authz,
	}

	if _, err := srv.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{TransactionId: "tx", DocumentName: "doc"}); !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("HeadDocument without principal = %v, want unauthenticated", err)
	}
	if _, err := srv.HeadDocument(writerCtx, &scrapv1.HeadDocumentRequest{TransactionId: "tx", DocumentName: "doc"}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("HeadDocument with writer = %v, want permission denied", err)
	}
	if _, err := srv.FindDocuments(writerCtx, &scrapv1.FindDocumentsRequest{TransactionId: "tx"}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("FindDocuments with writer = %v, want permission denied", err)
	}
	readStream := &readDocumentStream{ctx: writerCtx}
	if err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{TransactionId: "tx", DocumentName: "doc"}, readStream); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ReadDocument with writer = %v, want permission denied", err)
	}
	writeStream := &writeDocumentStream{ctx: readerCtx}
	if err := srv.WriteDocument(writeStream); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("WriteDocument with reader = %v, want permission denied", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
	if _, err := srv.HeadDocument(readerCtx, &scrapv1.HeadDocumentRequest{TransactionId: "tx", DocumentName: "doc"}); err != nil {
		t.Fatalf("HeadDocument with reader: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls after authorized read = %d, want 1", store.calls)
	}
}

type recordingStore struct {
	calls int
}

func (s *recordingStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	s.calls++
	return storeapi.WriteResult{}, nil
}

func (s *recordingStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	s.calls++
	return storeapi.DocumentMeta{Name: "doc", CreatedAt: time.Unix(0, 0)}, nil
}

func (s *recordingStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.calls++
	return io.NopCloser(nil), storeapi.DocumentMeta{}, nil
}

func (s *recordingStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	s.calls++
	return nil, nil
}

type writeDocumentStream struct {
	ctx context.Context
	scrapv1.DocumentService_WriteDocumentServer
}

func (s *writeDocumentStream) Context() context.Context {
	return s.ctx
}

type readDocumentStream struct {
	ctx context.Context
	scrapv1.DocumentService_ReadDocumentServer
}

func (s *readDocumentStream) Context() context.Context {
	return s.ctx
}
