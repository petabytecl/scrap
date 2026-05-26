package server_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestNotLeaderHeadDocumentReturnsUnavailableWithHint(t *testing.T) {
	client := startNotLeaderServer(t, "scrapd-2.scrap-headless.ns.svc:9090")

	_, err := client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-001",
		DocumentName:  "doc.xml",
	})
	if err == nil {
		t.Fatal("expected error from non-leader")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("expected UNAVAILABLE, got %v", st.Code())
	}

	var hint *scrapv1.LeaderHint
	for _, detail := range st.Details() {
		if h, ok := detail.(*scrapv1.LeaderHint); ok {
			hint = h
			break
		}
	}
	if hint == nil {
		t.Fatal("expected LeaderHint in status details")
	}
	if hint.GetLeaderAddr() != "scrapd-2.scrap-headless.ns.svc:9090" {
		t.Fatalf("LeaderAddr: got %q", hint.GetLeaderAddr())
	}
}
