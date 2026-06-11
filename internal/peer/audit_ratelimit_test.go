package peer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
)

func TestPeerServerAuditsAndRateLimitsPeerOperations(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePeer, Limit: 1, Window: time.Minute},
		},
	})
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuditSink(sink), WithRateLimiter(limiter))
	defer func() { _ = srv.Close() }()
	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	if _, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: marshalRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft first call: %v", err)
	}
	_, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: mustMarshalRaftForRateLimit(t)})
	if !errors.Is(err, security.ErrRateLimited) || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ForwardRaft second call = %v (%s), want rate limited", err, status.Code(err))
	}
	if router.calls != 1 {
		t.Fatalf("router calls = %d, want 1", router.calls)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationForwardRaft || events[1].Result != audit.ResultRateLimited {
		t.Fatalf("unexpected audit events: %+v", events)
	}
}

func TestPeerServerAuditsUnauthorizedReplicationShardWithoutAllowedEvent(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := NewServer(
		t.TempDir(),
		WithAuthorizer(authz, expected),
		WithAuditSink(sink),
		WithReplicationSink(&recordingReplicationSink{}),
		WithAuthorizedShards(7),
	)
	defer func() { _ = srv.Close() }()

	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ReplicateDocumentRequest{{
			Part: &scrapv1.ReplicateDocumentRequest_Init{
				Init: &scrapv1.ReplicateDocumentInit{
					ShardId: 8,
					BlockId: 1,
				},
			},
		}},
	}

	err := srv.ReplicateDocument(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ReplicateDocument wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationReplicateDocument || events[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func TestPeerServerAuditsUnauthorizedStreamShardWithoutAllowedEvent(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuditSink(sink), WithAuthorizedShards(7))
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	stream := &forwardRaftStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ForwardRaftStreamRequest{{
			ShardId: 8,
			Message: marshalRaftMessage(t),
		}},
	}

	err := srv.ForwardRaftStream(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ForwardRaftStream wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if router.calls != 0 {
		t.Fatalf("router calls = %d, want 0", router.calls)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationForwardRaftStream || events[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func TestPeerServerAuditsWrongShardDenialsWithoutRawIdentifierLeaks(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	caller := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "secret-peer-host",
		MemberID:       "member-secret-raw",
	}
	for _, tt := range wrongShardAuditCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			authz := security.NewStaticAuthorizer()
			sink := audit.NewMemorySink()
			srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuditSink(sink), WithAuthorizedShards(7))
			defer func() { _ = srv.Close() }()
			if tt.configure != nil {
				tt.configure(srv)
			}

			err := tt.call(srv, peerAuthContext(security.NewRoleSet(security.RolePeerMember), caller))
			assertWrongShardAuditDenial(t, tt, sink.Events(), err, caller)
		})
	}
}

type wrongShardAuditCase struct {
	name      string
	operation string
	configure func(*Server)
	call      func(*Server, context.Context) error
}

func wrongShardAuditCases(t *testing.T) []wrongShardAuditCase {
	t.Helper()
	return []wrongShardAuditCase{
		{
			name:      "ForwardRaft",
			operation: audit.OperationForwardRaft,
			configure: func(s *Server) { s.SetRaftRouter(&recordingRaftRouter{}) },
			call: func(s *Server, ctx context.Context) error {
				_, err := s.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 8, Message: marshalRaftMessage(t)})
				return err
			},
		},
		{
			name:      "ForwardRaftStream",
			operation: audit.OperationForwardRaftStream,
			configure: func(s *Server) { s.SetRaftRouter(&recordingRaftRouter{}) },
			call: func(s *Server, ctx context.Context) error {
				return s.ForwardRaftStream(&forwardRaftStream{
					ctx: ctx,
					requests: []*scrapv1.ForwardRaftStreamRequest{{
						ShardId: 8,
						Message: marshalRaftMessage(t),
					}},
				})
			},
		},
		{
			name:      "ReplicateDocument",
			operation: audit.OperationReplicateDocument,
			configure: func(s *Server) { s.replicationSink = &recordingReplicationSink{} },
			call: func(s *Server, ctx context.Context) error {
				return s.ReplicateDocument(&replicateDocumentStream{
					ctx: ctx,
					requests: []*scrapv1.ReplicateDocumentRequest{{
						Part: &scrapv1.ReplicateDocumentRequest_Init{
							Init: &scrapv1.ReplicateDocumentInit{
								ShardId:       8,
								BlockId:       1,
								TransactionId: "tx-secret-route",
								DocumentName:  "invoice-secret.xml",
							},
						},
					}},
				})
			},
		},
		{
			name:      "TransferBlock",
			operation: audit.OperationTransferBlock,
			call: func(s *Server, ctx context.Context) error {
				return s.TransferBlock(&scrapv1.TransferBlockRequest{ShardId: 8, BlockId: 1}, &transferBlockStream{ctx: ctx})
			},
		},
	}
}

func assertWrongShardAuditDenial(t *testing.T, tt wrongShardAuditCase, events []audit.Event, err error, caller security.PeerIdentityConfig) {
	t.Helper()
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%s wrong Shard = %v (%s), want permission denied", tt.name, err, status.Code(err))
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != tt.operation || events[0].Result != audit.ResultDenied || events[0].Reason != audit.ReasonPermissionDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
	rendered := fmt.Sprintf("%+v %v", events[0], err)
	for _, forbidden := range wrongShardAuditForbiddenValues(caller) {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("%s denial evidence leaked %q in %q", tt.name, forbidden, rendered)
		}
	}
}

func wrongShardAuditForbiddenValues(caller security.PeerIdentityConfig) []string {
	return []string{
		caller.MemberHostname,
		caller.MemberID,
		security.PeerIdentityPrincipalID(caller),
		"tx-secret-route",
		"invoice-secret.xml",
		"/tmp/secret",
		"backend-key-secret",
	}
}

func mustMarshalRaftForRateLimit(t *testing.T) []byte {
	t.Helper()
	data, err := (&raftpb.Message{}).Marshal()
	if err != nil {
		t.Fatalf("marshal raft message: %v", err)
	}
	return data
}
