package server

import (
	"context"
	"io"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentQuarantinedReturnsFailedPreconditionWithoutSend(t *testing.T) {
	const leaked = "scanner signature should not reach clients"
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return nil, storeapi.DocumentMeta{}, storeapi.NewPrecondition(
				storeapi.PreconditionReasonContentQuarantined,
				leaked,
			)
		},
	}
	stream := &recordingReadDocumentStream{ctx: context.Background()}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-quarantined",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.FailedPrecondition, err)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("ReadDocument sent %d responses for quarantined Document, want 0", len(stream.sent))
	}
	st := status.Convert(err)
	if st.Message() != storeapi.ErrFailedPrecondition.Error() {
		t.Fatalf("status message = %q, want %q", st.Message(), storeapi.ErrFailedPrecondition.Error())
	}
	info := errorInfoDetailFromStatus(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.PreconditionReasonContentQuarantined {
		t.Fatalf("reason = %q, want %q", info.GetReason(), storeapi.PreconditionReasonContentQuarantined)
	}
}

func TestReadDocumentReaderPreconditionReturnsFailedPrecondition(t *testing.T) {
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return readErrorCloser{
				err: storeapi.NewPrecondition(storeapi.PreconditionReasonContentQuarantined, "content quarantined"),
			}, readCanceledMeta(), nil
		},
	}
	stream := &recordingReadDocumentStream{ctx: context.Background()}
	srv := &documentServer{
		store:     store,
		telemetry: noopTelemetry{},
	}

	err := srv.ReadDocument(&scrapv1.ReadDocumentRequest{
		TransactionId: "tx-reader-quarantined",
		DocumentName:  "doc.xml",
	}, stream)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.FailedPrecondition, err)
	}
	if len(stream.sent) != 1 || stream.sent[0].GetMeta() == nil {
		t.Fatalf("ReadDocument sent = %+v, want metadata only before reader precondition", stream.sent)
	}
}

func errorInfoDetailFromStatus(st *status.Status) *errdetails.ErrorInfo {
	for _, detail := range st.Details() {
		if d, ok := detail.(*errdetails.ErrorInfo); ok {
			return d
		}
	}
	return nil
}
