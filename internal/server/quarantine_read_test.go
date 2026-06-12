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
	store := &readCanceledStore{
		read: func(context.Context) (io.ReadCloser, storeapi.DocumentMeta, error) {
			return nil, storeapi.DocumentMeta{}, storeapi.NewPrecondition(
				storeapi.PreconditionReasonContentQuarantined,
				"content quarantined",
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
	info := errorInfoDetailFromStatus(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.PreconditionReasonContentQuarantined {
		t.Fatalf("reason = %q, want %q", info.GetReason(), storeapi.PreconditionReasonContentQuarantined)
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
