package api

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/appstatus"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
)

func TestToGRPCErrorMapsApplicationStatusWithDetails(t *testing.T) {
	err := ToGRPCError(appstatus.New(
		appstatus.CodeDataLoss,
		"document bytes failed integrity verification",
		appstatus.WithDetails(&scrapv1.IntegrityFailureDetail{EvidenceId: "evidence-1"}),
	))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.DataLoss {
		t.Fatalf("code = %s, want %s", st.Code(), codes.DataLoss)
	}
	for _, detail := range st.Details() {
		if integrity, ok := detail.(*scrapv1.IntegrityFailureDetail); ok && integrity.GetEvidenceId() == "evidence-1" {
			return
		}
	}
	t.Fatalf("details = %#v, want integrity failure detail", st.Details())
}
