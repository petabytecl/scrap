package cmd

import (
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestPublicRPCLimiterRejectsWritesPastDocumentedCap(t *testing.T) {
	limiter := newPublicRPCLimiter()
	var releases []func()
	for range storeapi.MaxConcurrentWrites {
		release, err := limiter.acquire(scrapv1.DocumentService_WriteDocument_FullMethodName)
		if err != nil {
			t.Fatalf("acquire write below cap: %v", err)
		}
		releases = append(releases, release)
	}
	_, err := limiter.acquire(scrapv1.DocumentService_WriteDocument_FullMethodName)
	assertResourceExhaustedReason(t, err, storeapi.ResourceExhaustedReasonConcurrentWrites)

	releases[0]()
	release, err := limiter.acquire(scrapv1.DocumentService_WriteDocument_FullMethodName)
	if err != nil {
		t.Fatalf("acquire write after release: %v", err)
	}
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestPublicRPCLimiterRejectsReadsPastDocumentedCap(t *testing.T) {
	limiter := newPublicRPCLimiter()
	var releases []func()
	for range storeapi.MaxConcurrentReads {
		release, err := limiter.acquire(scrapv1.DocumentService_ReadDocument_FullMethodName)
		if err != nil {
			t.Fatalf("acquire read below cap: %v", err)
		}
		releases = append(releases, release)
	}
	_, err := limiter.acquire(scrapv1.DocumentService_HeadDocument_FullMethodName)
	assertResourceExhaustedReason(t, err, storeapi.ResourceExhaustedReasonConcurrentReads)

	for _, release := range releases {
		release()
	}
}

func TestPublicGRPCTransportLimitDescription(t *testing.T) {
	if got := publicGRPCLimitDescription(); got == "" {
		t.Fatal("limit description should not be empty")
	}
}

func assertResourceExhaustedReason(t *testing.T, err error, want string) {
	t.Helper()

	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("status.Code = %s, want ResourceExhausted (err=%v)", status.Code(err), err)
	}
	st := status.Convert(err)
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.GetReason() == want {
			return
		}
	}
	t.Fatalf("resource exhausted reason not found; want %q in details %v", want, st.Details())
}
