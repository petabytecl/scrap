package server

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
)

func TestDocumentServerAuditsAndRateLimitsPublicReads(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePublic, Limit: 1, Window: time.Minute},
		},
	})
	store := &recordingStore{}
	srv := &documentServer{
		store:       store,
		telemetry:   noopTelemetry{},
		logger:      slog.New(slog.DiscardHandler),
		authorizer:  authz,
		auditSink:   sink,
		rateLimiter: limiter,
	}
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleDocumentReader),
	})

	if _, err := srv.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{TransactionId: "tx-raw", DocumentName: "doc-raw"}); err != nil {
		t.Fatalf("HeadDocument first call: %v", err)
	}
	_, err := srv.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{TransactionId: "tx-raw", DocumentName: "doc-raw"})
	if !errors.Is(err, security.ErrRateLimited) || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("HeadDocument second call = %v (%s), want rate limited", err, status.Code(err))
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	if events[0].Result != audit.ResultAllowed || events[1].Result != audit.ResultRateLimited {
		t.Fatalf("audit results = %q, %q", events[0].Result, events[1].Result)
	}
	if events[0].Target != audit.TargetDocument || events[1].Reason != audit.ReasonRateLimited {
		t.Fatalf("unexpected audit events: %+v", events)
	}
}
