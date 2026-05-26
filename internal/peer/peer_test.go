package peer_test

import (
	"context"
	"net"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/peer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startPeerServer(t *testing.T, blocksDir string) (string, *peer.Server) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	s := peer.NewServer(blocksDir)
	gs := grpc.NewServer()
	peer.RegisterServer(gs, s)
	go gs.Serve(lis)
	t.Cleanup(func() {
		gs.GracefulStop()
		s.Close()
	})

	return lis.Addr().String(), s
}

func TestReplicateDocumentSinglePeer(t *testing.T) {
	dir := t.TempDir()
	addr, _ := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

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
	addr1, _ := startPeerServer(t, dir1)
	addr2, _ := startPeerServer(t, dir2)

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
	addr, _ := startPeerServer(t, dir)

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
