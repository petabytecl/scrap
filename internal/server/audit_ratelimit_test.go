package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	limiter := mustNewRateLimiter(t, security.RateLimitPolicy{
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

func TestDocumentServerAuditsDeniedPublicOperationsWithoutRawIdentifierLeaks(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	store := &recordingStore{}
	srv := &documentServer{
		store:      store,
		telemetry:  noopTelemetry{},
		logger:     slog.New(slog.DiscardHandler),
		authorizer: authz,
		auditSink:  sink,
	}
	writerCtx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-writer",
		Roles: security.NewRoleSet(security.RoleDocumentWriter),
	})
	readerCtx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-reader",
		Roles: security.NewRoleSet(security.RoleDocumentReader),
	})

	cases := publicDeniedAuditCases(writerCtx, readerCtx, srv)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || status.Code(err) != tc.code {
				t.Fatalf("call error = %v (%s), want %s", err, status.Code(err), tc.code)
			}
		})
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
	events := sink.Events()
	assertPublicDeniedAuditEvents(t, events, cases)
	assertPublicDeniedAuditNoLeaks(t, events)
}

type publicDeniedAuditCase struct {
	name      string
	call      func() error
	operation string
	reason    string
	code      codes.Code
}

func publicDeniedAuditCases(writerCtx, readerCtx context.Context, srv *documentServer) []publicDeniedAuditCase {
	const rawTx = "tx-secret-raw"
	const rawDoc = "invoice-secret.pdf"
	return []publicDeniedAuditCase{
		{
			name: "HeadDocument missing principal",
			call: func() error {
				_, err := srv.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{TransactionId: rawTx, DocumentName: rawDoc})
				return err
			},
			operation: audit.OperationHeadDocument,
			reason:    audit.ReasonUnauthenticated,
			code:      codes.Unauthenticated,
		},
		{
			name: "HeadDocument wrong role",
			call: func() error {
				_, err := srv.HeadDocument(writerCtx, &scrapv1.HeadDocumentRequest{TransactionId: rawTx, DocumentName: rawDoc})
				return err
			},
			operation: audit.OperationHeadDocument,
			reason:    audit.ReasonMissingRole,
			code:      codes.PermissionDenied,
		},
		{
			name: "FindDocuments wrong role",
			call: func() error {
				_, err := srv.FindDocuments(writerCtx, &scrapv1.FindDocumentsRequest{TransactionId: rawTx})
				return err
			},
			operation: audit.OperationFindDocuments,
			reason:    audit.ReasonMissingRole,
			code:      codes.PermissionDenied,
		},
		{
			name: "FindDocuments missing principal",
			call: func() error {
				_, err := srv.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{TransactionId: rawTx})
				return err
			},
			operation: audit.OperationFindDocuments,
			reason:    audit.ReasonUnauthenticated,
			code:      codes.Unauthenticated,
		},
		{
			name: "ReadDocument wrong role",
			call: func() error {
				return srv.ReadDocument(&scrapv1.ReadDocumentRequest{TransactionId: rawTx, DocumentName: rawDoc}, &readDocumentStream{ctx: writerCtx})
			},
			operation: audit.OperationReadDocument,
			reason:    audit.ReasonMissingRole,
			code:      codes.PermissionDenied,
		},
		{
			name: "ReadDocument missing principal",
			call: func() error {
				return srv.ReadDocument(&scrapv1.ReadDocumentRequest{TransactionId: rawTx, DocumentName: rawDoc}, &readDocumentStream{ctx: context.Background()})
			},
			operation: audit.OperationReadDocument,
			reason:    audit.ReasonUnauthenticated,
			code:      codes.Unauthenticated,
		},
		{
			name: "WriteDocument wrong role",
			call: func() error {
				return srv.WriteDocument(&writeDocumentStream{ctx: readerCtx})
			},
			operation: audit.OperationWriteDocument,
			reason:    audit.ReasonMissingRole,
			code:      codes.PermissionDenied,
		},
		{
			name: "WriteDocument missing principal",
			call: func() error {
				return srv.WriteDocument(&writeDocumentStream{ctx: context.Background()})
			},
			operation: audit.OperationWriteDocument,
			reason:    audit.ReasonUnauthenticated,
			code:      codes.Unauthenticated,
		},
	}
}

func assertPublicDeniedAuditEvents(t *testing.T, events []audit.Event, cases []publicDeniedAuditCase) {
	t.Helper()
	if len(events) != len(cases) {
		t.Fatalf("audit events = %d, want %d: %+v", len(events), len(cases), events)
	}
	for i, tc := range cases {
		event := events[i]
		if event.Surface != audit.SurfacePublic ||
			event.Operation != tc.operation ||
			event.Target != audit.TargetDocument ||
			event.Result != audit.ResultDenied ||
			event.Reason != tc.reason {
			t.Fatalf("event %d = %+v, want %s denied %s", i, event, tc.operation, tc.reason)
		}
	}
}

func assertPublicDeniedAuditNoLeaks(t *testing.T, events []audit.Event) {
	t.Helper()
	rendered := fmt.Sprintf("%+v", events)
	for _, forbidden := range []string{"tx-secret-raw", "invoice-secret.pdf", "member-writer", "member-reader"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("public denial audit leaked %q in %+v", forbidden, events)
		}
	}
}

func mustNewRateLimiter(t *testing.T, policy security.RateLimitPolicy) *security.RateLimiter {
	t.Helper()
	limiter, err := security.NewRateLimiter(policy)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	return limiter
}
