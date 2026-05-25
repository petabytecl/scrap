package api

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/appstatus"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/storageapp"
)

func TestToGRPCErrorMapsApplicationStatusWithDetails(t *testing.T) {
	retryAt := time.Unix(100, 0).UTC()
	retryDelay := 5 * time.Second
	document := identity.Document{TenantID: "tenant-1", TransactionID: "tx-1", DocumentName: "doc-1"}
	err := ToGRPCError(appstatus.New(
		appstatus.CodeDataLoss,
		"document bytes failed integrity verification",
		appstatus.WithDetails(
			storageapp.IntegrityFailureDetail{
				Identity:         document,
				AttemptedSources: []string{"local", "backend"},
				EvidenceID:       "evidence-1",
			},
			storageapp.RestorePendingDetail{
				Identity:         document,
				AffectedBlockIDs: []string{"block-1"},
				RestoreState:     "restore_pending",
				RestoreQueued:    true,
				RetryHint: storageapp.RetryHint{
					Retryable:  true,
					RetryAt:    &retryAt,
					RetryDelay: &retryDelay,
					Reason:     "restore_pending",
				},
			},
			storageapp.CryptoUnavailableDetail{
				Identity:  document,
				KeyScope:  "backend",
				RetryHint: storageapp.RetryHint{Retryable: true, Reason: "crypto_unavailable"},
			},
			storageapp.UnsafeCapacityDetail{
				CapacityProfileID: "local-non-production",
				RequiredBytes:     10,
				AvailableBytes:    5,
				Warnings:          []string{"SCRAP_LOCAL_CAPACITY_GUARD"},
			},
		),
	))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.DataLoss {
		t.Fatalf("code = %s, want %s", st.Code(), codes.DataLoss)
	}
	details := st.Details()
	requireMappedIntegrityDetail(t, details)
	requireMappedRestoreDetail(t, details, retryAt, retryDelay)
	requireMappedCryptoDetail(t, details)
	requireMappedCapacityDetail(t, details)
}

func requireMappedIntegrityDetail(t *testing.T, details []any) {
	t.Helper()
	integrity := requireProtoDetail[scrapv1.IntegrityFailureDetail](t, details)
	if integrity == nil || integrity.GetEvidenceId() != "evidence-1" || integrity.GetIdentity().GetTenantId() != "tenant-1" {
		t.Fatalf("integrity detail = %#v, want mapped identity and evidence", integrity)
	}
	if got := integrity.GetAttemptedSources(); len(got) != 2 || got[0] != "local" || got[1] != "backend" {
		t.Fatalf("attempted sources = %#v, want local/backend", got)
	}
}

func requireMappedRestoreDetail(t *testing.T, details []any, retryAt time.Time, retryDelay time.Duration) {
	t.Helper()
	restore := requireProtoDetail[scrapv1.RestorePendingDetail](t, details)
	if restore == nil || restore.GetAffectedBlockIds()[0] != "block-1" || !restore.GetRestoreQueued() {
		t.Fatalf("restore detail = %#v, want queued block restore", restore)
	}
	if restore.GetRetryHint().GetRetryAt().AsTime() != retryAt {
		t.Fatalf("restore retry_at = %v, want %v", restore.GetRetryHint().GetRetryAt().AsTime(), retryAt)
	}
	if restore.GetRetryHint().GetRetryDelay().AsDuration() != retryDelay {
		t.Fatalf("restore retry_delay = %v, want %v", restore.GetRetryHint().GetRetryDelay().AsDuration(), retryDelay)
	}
}

func requireMappedCryptoDetail(t *testing.T, details []any) {
	t.Helper()
	crypto := requireProtoDetail[scrapv1.CryptoUnavailableDetail](t, details)
	if crypto == nil || crypto.GetKeyScope() != "backend" || crypto.GetRetryHint().GetReason() != "crypto_unavailable" {
		t.Fatalf("crypto detail = %#v, want backend crypto retry", crypto)
	}
}

func requireMappedCapacityDetail(t *testing.T, details []any) {
	t.Helper()
	capacity := requireProtoDetail[scrapv1.UnsafeCapacityDetail](t, details)
	if capacity == nil || capacity.GetCapacityProfileId() != "local-non-production" || capacity.GetRequiredBytes() != 10 {
		t.Fatalf("capacity detail = %#v, want local capacity detail", capacity)
	}
}

func requireProtoDetail[T any](t *testing.T, details []any) *T {
	t.Helper()
	for _, detail := range details {
		if typed, ok := detail.(*T); ok {
			return typed
		}
	}
	t.Fatalf("details = %#v, want %T", details, new(T))
	return nil
}

func TestToGRPCErrorIgnoresUnsupportedApplicationStatusDetails(t *testing.T) {
	err := ToGRPCError(appstatus.New(
		appstatus.CodeUnavailable,
		"temporarily unavailable",
		appstatus.WithDetails(struct{ unsupported bool }{unsupported: true}),
	))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a gRPC status: %v", err)
	}
	if details := st.Details(); len(details) != 0 {
		t.Fatalf("details = %#v, want unsupported detail omitted", details)
	}
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
