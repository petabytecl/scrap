package peer

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	principalMismatch := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "peer",
		Roles: security.NewRoleSet(security.RolePeerMember),
	})
	principalMismatch = security.ContextWithPeerIdentity(principalMismatch, security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	if _, err := srv.ForwardRaft(principalMismatch, &scrapv1.ForwardRaftRequest{}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ForwardRaft principal mismatch = %v, want permission denied", err)
	}
	if got := authz.AuthorizationStatus(); got != security.AuthorizationStatusMismatch {
		t.Fatalf("authorization status = %q, want mismatch", got)
	}

	crossCell := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-b",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})
	if _, err := srv.ForwardRaft(crossCell, &scrapv1.ForwardRaftRequest{}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ForwardRaft cross-cell = %v, want permission denied", err)
	}
	if router.calls != 0 {
		t.Fatalf("router calls = %d, want 0", router.calls)
	}

	missingIdentity := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "peer",
		Roles: security.NewRoleSet(security.RolePeerMember),
	})
	if _, err := srv.ForwardRaft(missingIdentity, &scrapv1.ForwardRaftRequest{}); !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("ForwardRaft missing identity = %v, want unauthenticated", err)
	}
	if router.calls != 0 {
		t.Fatalf("router calls after missing identity = %d, want 0", router.calls)
	}

	otherMember := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	}
	allowed := peerAuthContext(security.NewRoleSet(security.RolePeerMember), otherMember)
	if _, err := srv.ForwardRaft(allowed, &scrapv1.ForwardRaftRequest{Message: marshalRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft same-cell peer: %v", err)
	}
	if router.calls != 1 {
		t.Fatalf("router calls after allowed peer = %d, want 1", router.calls)
	}
}

func TestPeerServerBindsRaftMessageSender(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	srv := NewServer(t.TempDir(),
		WithAuthorizer(authz, expected),
		WithPeerRaftIDs(map[string]uint64{"scrapd-1": 2}),
	)
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)

	caller := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	}
	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), caller)

	spoofed := marshalRaftMessageFrom(t, 99)
	if _, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: spoofed}); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("ForwardRaft spoofed From = %v, want permission denied", err)
	}
	if router.calls != 0 {
		t.Fatalf("router calls after spoof = %d, want 0", router.calls)
	}

	okMsg := marshalRaftMessageFrom(t, 2)
	if _, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: okMsg}); err != nil {
		t.Fatalf("ForwardRaft bound sender: %v", err)
	}
	if router.calls != 1 {
		t.Fatalf("router calls after bound sender = %d, want 1", router.calls)
	}
}

func TestPeerServerDeniesUnauthorizedShardBeforeRaftRoute(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuthorizedShards(7))
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	_, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{
		ShardId: 8,
		Message: marshalRaftMessage(t),
	})
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ForwardRaft wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if router.calls != 0 {
		t.Fatalf("router calls after wrong Shard = %d, want 0", router.calls)
	}

	_, err = srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{
		ShardId: 7,
		Message: marshalRaftMessage(t),
	})
	if err != nil {
		t.Fatalf("ForwardRaft allowed Shard: %v", err)
	}
	if router.calls != 1 {
		t.Fatalf("router calls after allowed Shard = %d, want 1", router.calls)
	}
}

func TestPeerServerDeniesUnauthorizedShardBeforeRaftStreamRoute(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuthorizedShards(7))
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
		t.Fatalf("router calls after wrong Shard = %d, want 0", router.calls)
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

func TestPeerServerDeniesUnauthorizedShardBeforeReplicationSink(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := &recordingReplicationSink{}
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithReplicationSink(sink), WithAuthorizedShards(7))
	defer func() { _ = srv.Close() }()

	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ReplicateDocumentRequest{{
			Part: &scrapv1.ReplicateDocumentRequest_Init{
				Init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
					init.ShardId = 8
					init.BlockId = 1
				}),
			},
		}},
	}
	err := srv.ReplicateDocument(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ReplicateDocument wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if sink.calls != 0 {
		t.Fatalf("replication sink calls after wrong Shard = %d, want 0", sink.calls)
	}
}

func TestPeerServerDeniesUnauthorizedShardBeforeLocalReplicationFiles(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	dir := t.TempDir()
	srv := NewServer(dir, WithAuthorizer(authz, expected), WithAuthorizedShards(7))
	defer func() { _ = srv.Close() }()

	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ReplicateDocumentRequest{{
			Part: &scrapv1.ReplicateDocumentRequest_Init{
				Init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
					init.ShardId = 8
					init.BlockId = 1
					init.TotalBytes = 6
					init.FrameCount = 1
					init.DocumentName = "invoice.xml"
				}),
			},
		}, {
			Part: &scrapv1.ReplicateDocumentRequest_ChunkData{
				ChunkData: []byte("secret"),
			},
		}},
	}
	err := srv.ReplicateDocument(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ReplicateDocument wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read block dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("local block dir entries after wrong Shard = %v, want none", entries)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.writers) != 0 {
		t.Fatalf("local block writers after wrong Shard = %d, want 0", len(srv.writers))
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
		CellID:         "cell-b",
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

func TestPeerServerDeniesUnauthorizedShardBeforeBlockTransfer(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	resolver := &recordingBlockDirResolver{}
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuthorizedShards(7), WithBlockDirResolver(resolver))
	defer func() { _ = srv.Close() }()

	stream := &transferBlockStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
	}
	err := srv.TransferBlock(&scrapv1.TransferBlockRequest{ShardId: 8, BlockId: 1}, stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("TransferBlock wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if stream.sends != 0 {
		t.Fatalf("transfer sends after wrong Shard = %d, want 0", stream.sends)
	}
	if resolver.calls != 0 {
		t.Fatalf("block dir resolver calls after wrong Shard = %d, want 0", resolver.calls)
	}
}

func TestPeerServerAllowsMultipleAuthorizedShards(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := &recordingReplicationSink{}
	resolver := &recordingBlockDirResolver{}
	srv := NewServer(
		t.TempDir(),
		WithAuthorizer(authz, expected),
		WithReplicationSink(sink),
		WithBlockDirResolver(resolver),
		WithAuthorizedShards(7, 9),
	)
	defer func() { _ = srv.Close() }()

	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	assertMultipleShardForwardRaftAllowed(ctx, t, srv)
	assertMultipleShardForwardRaftStreamAllowed(ctx, t, srv)
	assertMultipleShardReplicationAllowed(ctx, t, srv, sink)
	assertMultipleShardTransferBlockReachesResolver(ctx, t, srv, resolver)
}

func assertMultipleShardForwardRaftAllowed(ctx context.Context, t *testing.T, srv *Server) {
	t.Helper()
	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	for _, shardID := range []uint64{7, 9} {
		if _, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{
			ShardId: shardID,
			Message: marshalRaftMessage(t),
		}); err != nil {
			t.Fatalf("ForwardRaft Shard %d: %v", shardID, err)
		}
	}
	if got, want := router.shardIDs, []uint64{7, 9}; !slices.Equal(got, want) {
		t.Fatalf("routed Shards = %v, want %v", got, want)
	}
}

func assertMultipleShardForwardRaftStreamAllowed(ctx context.Context, t *testing.T, srv *Server) {
	t.Helper()
	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	err := srv.ForwardRaftStream(&forwardRaftStream{
		ctx: ctx,
		requests: []*scrapv1.ForwardRaftStreamRequest{{
			ShardId: 9,
			Message: marshalRaftMessage(t),
		}},
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ForwardRaftStream allowed Shard = %v, want EOF after routing", err)
	}
	if got, want := router.shardIDs, []uint64{9}; !slices.Equal(got, want) {
		t.Fatalf("stream routed Shards = %v, want %v", got, want)
	}
}

func assertMultipleShardReplicationAllowed(ctx context.Context, t *testing.T, srv *Server, sink *recordingReplicationSink) {
	t.Helper()
	for _, shardID := range []uint64{7, 9} {
		err := srv.ReplicateDocument(&replicateDocumentStream{
			ctx: ctx,
			requests: []*scrapv1.ReplicateDocumentRequest{{
				Part: &scrapv1.ReplicateDocumentRequest_Init{
					Init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
						init.ShardId = shardID
						init.BlockId = shardID
						init.TotalBytes = 7
					}),
				},
			}, {
				Part: &scrapv1.ReplicateDocumentRequest_ChunkData{
					ChunkData: []byte("payload"),
				},
			}},
		})
		if err != nil {
			t.Fatalf("ReplicateDocument allowed Shard %d: %v", shardID, err)
		}
	}
	if got, want := sink.shardIDs, []uint64{7, 9}; !slices.Equal(got, want) {
		t.Fatalf("replicated Shards = %v, want %v", got, want)
	}
}

func assertMultipleShardTransferBlockReachesResolver(ctx context.Context, t *testing.T, srv *Server, resolver *recordingBlockDirResolver) {
	t.Helper()
	for _, shardID := range []uint64{7, 9} {
		stream := &transferBlockStream{ctx: ctx}
		err := srv.TransferBlock(&scrapv1.TransferBlockRequest{ShardId: shardID, BlockId: 1}, stream)
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("TransferBlock allowed Shard %d = %v (%s), want failed precondition after resolver", shardID, err, status.Code(err))
		}
		if stream.sends != 0 {
			t.Fatalf("TransferBlock allowed Shard %d sends = %d, want 0 before local data", shardID, stream.sends)
		}
	}
	if got, want := resolver.shardIDs, []uint64{7, 9}; !slices.Equal(got, want) {
		t.Fatalf("resolved block Shards = %v, want %v", got, want)
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
		ID:    security.PeerIdentityPrincipalID(identity),
		Roles: roles,
	})
	return security.ContextWithPeerIdentity(ctx, identity)
}

func marshalRaftMessage(t *testing.T) []byte {
	t.Helper()
	return marshalRaftMessageFrom(t, 0)
}

func marshalRaftMessageFrom(t *testing.T, from uint64) []byte {
	t.Helper()
	data, err := (&raftpb.Message{From: from}).Marshal()
	if err != nil {
		t.Fatalf("marshal raft message: %v", err)
	}
	return data
}

type recordingRaftRouter struct {
	calls    int
	shardIDs []uint64
}

func (r *recordingRaftRouter) RouteRaftMessage(_ context.Context, shardID uint64, _ raftpb.Message) error {
	r.calls++
	r.shardIDs = append(r.shardIDs, shardID)
	return nil
}

type recordingBlockDirResolver struct {
	calls    int
	shardIDs []uint64
	dir      string
	ok       bool
}

func (r *recordingBlockDirResolver) BlockDirForShard(shardID uint64) (string, bool) {
	r.calls++
	r.shardIDs = append(r.shardIDs, shardID)
	return r.dir, r.ok
}

type recordingReplicationSink struct {
	calls    int
	shardIDs []uint64
	err      error
}

func (s *recordingReplicationSink) AppendReplicatedDocument(_ context.Context, init *scrapv1.ReplicateDocumentInit, _ io.Reader) ([]byte, error) {
	s.calls++
	s.shardIDs = append(s.shardIDs, init.GetShardId())
	return nil, s.err
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
	ctx      context.Context
	requests []*scrapv1.ReplicateDocumentRequest
	next     int
	closed   bool
	scrapv1.PeerService_ReplicateDocumentServer
}

func (s *replicateDocumentStream) Context() context.Context {
	return s.ctx
}

func (s *replicateDocumentStream) Recv() (*scrapv1.ReplicateDocumentRequest, error) {
	if s.next >= len(s.requests) {
		return nil, io.EOF
	}
	req := s.requests[s.next]
	s.next++
	return req, nil
}

func (s *replicateDocumentStream) SendAndClose(*scrapv1.ReplicateDocumentResponse) error {
	s.closed = true
	return nil
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
