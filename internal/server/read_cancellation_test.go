package server

import (
	"bytes"
	"context"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentCanceledContextReturnsBeforeStoreOrSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &readCanceledStore{}
	stream := &recordingReadDocumentStream{ctx: ctx}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-canceled",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.Canceled, err)
	}
	if store.readCalls != 0 {
		t.Fatalf("ReadDocument store calls = %d, want 0", store.readCalls)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("ReadDocument sent %d responses after cancellation, want 0", len(stream.sent))
	}
}

func TestReadDocumentCancellationDuringStoreReturnsCanceledWithoutSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storeEntered := make(chan struct{})
	store := &readCanceledStore{
		read: func(ctx context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			close(storeEntered)
			<-ctx.Done()
			return nil, storeapi.DocumentMeta{}, ctx.Err()
		},
	}
	stream := &recordingReadDocumentStream{ctx: ctx}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.ReadDocument(&scrapv1.ReadDocumentRequest{
			TransactionId: "tx-cancel-during-store",
			DocumentName:  "doc.xml",
		}, stream)
	}()

	<-storeEntered
	cancel()

	err := <-done
	if status.Code(err) != codes.Canceled {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.Canceled, err)
	}
	if store.readCalls != 1 {
		t.Fatalf("ReadDocument store calls = %d, want 1", store.readCalls)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("ReadDocument sent %d responses after store cancellation, want 0", len(stream.sent))
	}
}

func TestReadDocumentReaderContextErrorPreservesCanceledStatus(t *testing.T) {
	ctx := context.Background()
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return readErrorCloser{err: context.Canceled}, readCanceledMeta(), nil
		},
	}
	stream := &recordingReadDocumentStream{ctx: ctx}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-reader-canceled",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.Canceled, err)
	}
	if len(stream.sent) != 1 || stream.sent[0].GetMeta() == nil {
		t.Fatalf("ReadDocument sent = %+v, want metadata before reader cancellation", stream.sent)
	}
}

func TestReadDocumentReaderPartialDataLossSendsNoPartialBytes(t *testing.T) {
	ctx := context.Background()
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return readPartialErrorCloser{
				data: []byte("partial"),
				err:  storeapi.ErrDataLoss,
			}, readCanceledMeta(), nil
		},
	}
	stream := &recordingReadDocumentStream{ctx: ctx}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-reader-partial-error",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.DataLoss, err)
	}
	if len(stream.sent) != 1 || stream.sent[0].GetMeta() == nil {
		t.Fatalf("ReadDocument sent = %+v, want metadata only before reader data loss", stream.sent)
	}
}

func TestReadDocumentReaderRebuildingErrorReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return readErrorCloser{err: storeapi.ErrRebuilding}, readCanceledMeta(), nil
		},
	}
	stream := &recordingReadDocumentStream{ctx: ctx}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-reader-rebuilding",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.Unavailable, err)
	}
}

func TestReadDocumentSendStatusErrorIsNotRemappedToInternal(t *testing.T) {
	ctx := context.Background()
	store := &readCanceledStore{}
	stream := &recordingReadDocumentStream{
		ctx:     ctx,
		sendErr: status.Error(codes.Unavailable, "transport unavailable"),
	}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-send-unavailable",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.Unavailable, err)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("ReadDocument successful sends = %d, want 0", len(stream.sent))
	}
}

type readCanceledStore struct {
	readCalls int
	read      func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error)
}

func (s *readCanceledStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, storeapi.ErrInvalidArgument
}

func (s *readCanceledStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s *readCanceledStore) ReadDocument(ctx context.Context, _, _ string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.readCalls++
	if s.read != nil {
		return s.read(ctx)
	}
	return io.NopCloser(bytes.NewReader([]byte("payload"))), readCanceledMeta(), nil
}

func readCanceledMeta() storeapi.DocumentMeta {
	return storeapi.DocumentMeta{
		Name:        "doc.xml",
		ContentType: "text/xml",
		Size:        int64(len("payload")),
	}
}

func (s *readCanceledStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

type readErrorCloser struct {
	err error
}

func (r readErrorCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r readErrorCloser) Close() error {
	return nil
}

type readPartialErrorCloser struct {
	data []byte
	err  error
}

func (r readPartialErrorCloser) Read(dst []byte) (int, error) {
	return copy(dst, r.data), r.err
}

func (r readPartialErrorCloser) Close() error {
	return nil
}

type recordingReadDocumentStream struct {
	grpc.ServerStream
	ctx     context.Context
	sendErr error
	sent    []*scrapv1.ReadDocumentResponse
}

func (s *recordingReadDocumentStream) Context() context.Context {
	return s.ctx
}

func (s *recordingReadDocumentStream) Send(resp *scrapv1.ReadDocumentResponse) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, resp)
	return nil
}
