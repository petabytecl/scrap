package api

import (
	"errors"
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

func TestToGRPCErrorPassThroughAndCodeMapping(t *testing.T) {
	if ToGRPCError(nil) != nil {
		t.Fatal("nil error did not map to nil")
	}
	statusErr := status.Error(codes.PermissionDenied, "denied")
	if !errors.Is(ToGRPCError(statusErr), statusErr) {
		t.Fatal("existing gRPC status was not passed through")
	}
	plainErr := errors.New("plain")
	if !errors.Is(ToGRPCError(plainErr), plainErr) {
		t.Fatal("plain error was not passed through")
	}

	for _, tc := range []struct {
		code appstatus.Code
		want codes.Code
	}{
		{code: appstatus.CodeInvalidArgument, want: codes.InvalidArgument},
		{code: appstatus.CodeNotFound, want: codes.NotFound},
		{code: appstatus.CodeAlreadyExists, want: codes.AlreadyExists},
		{code: appstatus.CodeFailedPrecondition, want: codes.FailedPrecondition},
		{code: appstatus.CodeResourceExhausted, want: codes.ResourceExhausted},
		{code: appstatus.CodeUnavailable, want: codes.Unavailable},
		{code: appstatus.CodeDataLoss, want: codes.DataLoss},
		{code: appstatus.CodeInternal, want: codes.Internal},
		{code: appstatus.Code(99), want: codes.Unknown},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			st, ok := status.FromError(ToGRPCError(appstatus.New(tc.code, "mapped")))
			if !ok {
				t.Fatal("mapped error is not gRPC status")
			}
			if st.Code() != tc.want {
				t.Fatalf("code = %s, want %s", st.Code(), tc.want)
			}
		})
	}
}
