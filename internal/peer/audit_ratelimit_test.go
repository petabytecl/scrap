package peer

import (
	"errors"
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

func mustMarshalRaftForRateLimit(t *testing.T) []byte {
	t.Helper()
	data, err := (&raftpb.Message{}).Marshal()
	if err != nil {
		t.Fatalf("marshal raft message: %v", err)
	}
	return data
}
