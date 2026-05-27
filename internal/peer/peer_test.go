package peer_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/scrub"
)

func startPeerServer(t *testing.T, blocksDir string, opts ...peer.ServerOption) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	s := peer.NewServer(blocksDir, opts...)
	gs := grpc.NewServer()
	peer.RegisterServer(gs, s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.GracefulStop()
		s.Close()
	})

	return lis.Addr().String()
}

func TestReplicateDocumentSinglePeer(t *testing.T) {
	dir := t.TempDir()
	addr := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.ReplicateDocument(context.Background())
	if err != nil {
		t.Fatalf("ReplicateDocument: %v", err)
	}

	if err := stream.Send(&scrapv1.ReplicateDocumentRequest{
		Part: &scrapv1.ReplicateDocumentRequest_Init{
			Init: &scrapv1.ReplicateDocumentInit{
				TransactionId: "tx-peer-001",
				DocumentName:  "doc.xml",
				ContentType:   "text/xml",
				BlockId:       1,
				TotalBytes:    11,
			},
		},
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}

	if err := stream.Send(&scrapv1.ReplicateDocumentRequest{
		Part: &scrapv1.ReplicateDocumentRequest_ChunkData{
			ChunkData: []byte("hello world"),
		},
	}); err != nil {
		t.Fatalf("Send chunk: %v", err)
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	if len(resp.GetSha256()) != 32 {
		t.Fatalf("SHA256 should be 32 bytes, got %d", len(resp.GetSha256()))
	}
}

func TestFanOutQuorum(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	addr1 := startPeerServer(t, dir1)
	addr2 := startPeerServer(t, dir2)

	c := peer.NewClient()
	defer c.Close()

	init := &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-fan-001",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		TotalBytes:    4,
	}

	results := c.FanOut(context.Background(), []string{addr1, addr2}, init, [][]byte{[]byte("data")})

	successCount := 0
	for _, r := range results {
		if r.Err == nil {
			successCount++
		}
	}

	if !peer.QuorumMet(3, successCount) {
		t.Fatalf("quorum not met: %d successful peers out of 3 voters", successCount)
	}
}

func TestQuorumMetCalculation(t *testing.T) {
	if !peer.QuorumMet(3, 1) {
		t.Fatal("3 voters, leader + 1 peer = 2/3 should be quorum")
	}
	if !peer.QuorumMet(3, 2) {
		t.Fatal("3 voters, leader + 2 peers = 3/3 should be quorum")
	}
	if peer.QuorumMet(3, 0) {
		t.Fatal("3 voters, leader only = 1/3 should NOT be quorum")
	}
	if !peer.QuorumMet(5, 2) {
		t.Fatal("5 voters, leader + 2 = 3/5 should be quorum")
	}
	if peer.QuorumMet(5, 1) {
		t.Fatal("5 voters, leader + 1 = 2/5 should NOT be quorum")
	}
}

func TestFanOutWithDeadPeer(t *testing.T) {
	dir := t.TempDir()
	addr := startPeerServer(t, dir)

	c := peer.NewClient()
	defer c.Close()

	init := &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-dead-001",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       2,
		TotalBytes:    4,
	}

	results := c.FanOut(context.Background(),
		[]string{addr, "127.0.0.1:1"},
		init, [][]byte{[]byte("data")})

	successCount := 0
	for _, r := range results {
		if r.Err == nil {
			successCount++
		}
	}

	if successCount != 1 {
		t.Fatalf("expected 1 success (dead peer should fail), got %d", successCount)
	}

	if !peer.QuorumMet(3, successCount) {
		t.Fatal("3 voters with 1 successful peer should still be quorum")
	}
}

type mockScrubCache struct {
	result *scrub.Result
}

func (m *mockScrubCache) GetScrubResult(scrubID string) (scrub.Result, bool) {
	if m.result == nil || m.result.ScrubID != scrubID {
		return scrub.Result{}, false
	}
	return *m.result, true
}

func TestConsistencyCheckReturnsCache(t *testing.T) {
	dir := t.TempDir()
	hash := [32]byte{0xaa, 0xbb, 0xcc}
	cache := &mockScrubCache{result: &scrub.Result{
		ScrubID:      "scrub-100",
		AppliedIndex: 42,
		SHA256:       hash,
	}}

	addr := startPeerServer(t, dir, peer.WithScrubCache(cache))

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.ConsistencyCheck(context.Background(), &scrapv1.ConsistencyCheckRequest{
		ScrubId: "scrub-100",
	})
	if err != nil {
		t.Fatalf("ConsistencyCheck: %v", err)
	}
	if resp.ScrubId != "scrub-100" {
		t.Fatalf("ScrubId: got %q", resp.ScrubId)
	}
	if resp.AppliedIndex != 42 {
		t.Fatalf("AppliedIndex: got %d", resp.AppliedIndex)
	}
	if len(resp.Sha256) != 32 {
		t.Fatalf("SHA256 length: got %d", len(resp.Sha256))
	}
	if resp.Sha256[0] != 0xaa || resp.Sha256[1] != 0xbb || resp.Sha256[2] != 0xcc {
		t.Fatalf("SHA256 mismatch: got %x", resp.Sha256)
	}
}

type mockRebuildHandler struct {
	called     bool
	inProgress bool
	err        error
}

func (m *mockRebuildHandler) TriggerRebuild(_ context.Context) (bool, error) {
	m.called = true
	return m.inProgress, m.err
}

func TestRequestIndexRebuild_TriggersRebuild(t *testing.T) {
	dir := t.TempDir()
	handler := &mockRebuildHandler{}
	addr := startPeerServer(t, dir, peer.WithRebuildHandler(handler))

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.RequestIndexRebuild(context.Background(), &scrapv1.RequestIndexRebuildRequest{
		ScrubId: "scrub-rebuild-1",
	})
	if err != nil {
		t.Fatalf("RequestIndexRebuild: %v", err)
	}
	if !handler.called {
		t.Fatal("expected rebuild handler to be called")
	}
	if resp.AlreadyInProgress {
		t.Fatal("expected already_in_progress=false")
	}
}

func TestRequestIndexRebuild_AlreadyInProgress(t *testing.T) {
	dir := t.TempDir()
	handler := &mockRebuildHandler{inProgress: true}
	addr := startPeerServer(t, dir, peer.WithRebuildHandler(handler))

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	resp, err := client.RequestIndexRebuild(context.Background(), &scrapv1.RequestIndexRebuildRequest{
		ScrubId: "scrub-rebuild-2",
	})
	if err != nil {
		t.Fatalf("RequestIndexRebuild: %v", err)
	}
	if !resp.AlreadyInProgress {
		t.Fatal("expected already_in_progress=true")
	}
}

func TestRequestIndexRebuild_NotConfigured(t *testing.T) {
	dir := t.TempDir()
	addr := startPeerServer(t, dir) // no rebuild handler

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	_, err = client.RequestIndexRebuild(context.Background(), &scrapv1.RequestIndexRebuildRequest{
		ScrubId: "scrub-rebuild-3",
	})
	if err == nil {
		t.Fatal("expected error when rebuild handler not configured")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FAILED_PRECONDITION, got %v", st.Code())
	}
}

func TestConsistencyCheckNotFound(t *testing.T) {
	dir := t.TempDir()
	cache := &mockScrubCache{result: nil}

	addr := startPeerServer(t, dir, peer.WithScrubCache(cache))

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	_, err = client.ConsistencyCheck(context.Background(), &scrapv1.ConsistencyCheckRequest{
		ScrubId: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for missing scrub_id")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("expected NOT_FOUND, got %v", st.Code())
	}
}
