package peer

import (
	"context"
	"errors"
	"io"
	"testing"

	"go.etcd.io/raft/v3/raftpb"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
)

func TestPeerServerDeniesUnauthorizedBeforeRaftRoute(t *testing.T) {
	expected := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-0",
		MemberID:       "member-a",
	}
	authz := security.NewStaticAuthorizer()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected))
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)

	noRole := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "peer",
		Roles: security.NewRoleSet(security.RoleDocumentReader),
	})
	noRole = security.ContextWithPeerIdentity(noRole, expected)

	if _, err := srv.ForwardRaft(noRole, &scrapv1.ForwardRaftRequest{}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ForwardRaft no role = %v, want permission denied", err)
	}

	mismatch := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "peer",
		Roles: security.NewRoleSet(security.RolePeerMember),
	})
	mismatch = security.ContextWithPeerIdentity(mismatch, security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	if _, err := srv.ForwardRaft(mismatch, &scrapv1.ForwardRaftRequest{}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ForwardRaft mismatch = %v, want permission denied", err)
	}
	if router.calls != 0 {
		t.Fatalf("router calls = %d, want 0", router.calls)
	}
}

func TestPeerServerDeniesUnauthorizedBeforeReplicationSink(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := &recordingReplicationSink{}
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithReplicationSink(sink))
	defer func() { _ = srv.Close() }()

	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RoleDocumentReader), expected),
	}
	if err := srv.ReplicateDocument(stream); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ReplicateDocument no peer role = %v, want permission denied", err)
	}
	if sink.calls != 0 {
		t.Fatalf("replication sink calls = %d, want 0", sink.calls)
	}
}

func TestPeerServerDeniesUnauthorizedBeforeRebuildAndScrub(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	rebuild := &recordingRebuildHandler{}
	cache := &recordingScrubCache{}
	srv := NewServer(
		t.TempDir(),
		WithAuthorizer(authz, expected),
		WithRebuildHandler(rebuild),
		WithScrubCache(cache),
	)
	defer func() { _ = srv.Close() }()

	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-2",
		MemberID:       "member-b",
	})
	if _, err := srv.RequestIndexRebuild(ctx, &scrapv1.RequestIndexRebuildRequest{}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("RequestIndexRebuild mismatch = %v, want permission denied", err)
	}
	if _, err := srv.ConsistencyCheck(ctx, &scrapv1.ConsistencyCheckRequest{ScrubId: "scrub-1"}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ConsistencyCheck mismatch = %v, want permission denied", err)
	}
	if rebuild.calls != 0 {
		t.Fatalf("rebuild calls = %d, want 0", rebuild.calls)
	}
	if cache.calls != 0 {
		t.Fatalf("scrub cache calls = %d, want 0", cache.calls)
	}
}

func TestPeerServerDeniesUnauthorizedBeforeBlockTransfer(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected))
	defer func() { _ = srv.Close() }()

	stream := &transferBlockStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RoleDocumentReader), expected),
	}
	err := srv.TransferBlock(&scrapv1.TransferBlockRequest{BlockId: 1}, stream)
	if !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("TransferBlock no peer role = %v, want permission denied", err)
	}
	if stream.sends != 0 {
		t.Fatalf("transfer sends = %d, want 0", stream.sends)
	}
}

func peerAuthExpectedIdentity() security.PeerIdentityConfig {
	return security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-0",
		MemberID:       "member-a",
	}
}

func peerAuthContext(roles security.RoleSet, identity security.PeerIdentityConfig) context.Context {
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "peer",
		Roles: roles,
	})
	return security.ContextWithPeerIdentity(ctx, identity)
}

type recordingRaftRouter struct {
	calls int
}

func (r *recordingRaftRouter) RouteRaftMessage(context.Context, uint64, raftpb.Message) error {
	r.calls++
	return nil
}

type recordingReplicationSink struct {
	calls int
}

func (s *recordingReplicationSink) AppendReplicatedDocument(context.Context, *scrapv1.ReplicateDocumentInit, io.Reader) ([]byte, error) {
	s.calls++
	return nil, nil
}

type recordingRebuildHandler struct {
	calls int
}

func (h *recordingRebuildHandler) TriggerRebuild(context.Context) (bool, error) {
	h.calls++
	return false, nil
}

type recordingScrubCache struct {
	calls int
}

func (c *recordingScrubCache) GetScrubResult(string) (scrub.Result, bool) {
	c.calls++
	return scrub.Result{}, false
}

type replicateDocumentStream struct {
	ctx context.Context
	scrapv1.PeerService_ReplicateDocumentServer
}

func (s *replicateDocumentStream) Context() context.Context {
	return s.ctx
}

type transferBlockStream struct {
	ctx   context.Context
	sends int
	scrapv1.PeerService_TransferBlockServer
}

func (s *transferBlockStream) Context() context.Context {
	return s.ctx
}

func (s *transferBlockStream) Send(*scrapv1.TransferBlockResponse) error {
	s.sends++
	return nil
}
