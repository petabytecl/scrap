package peer_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/peer"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
)

type grpcTestCluster struct {
	nodes     []*scrapraft.Node
	servers   []*grpc.Server
	transport *peer.SharedTransport
}

//nolint:gocognit // integration test helper wiring 3-node gRPC cluster
func startGRPCCluster(t *testing.T) *grpcTestCluster {
	t.Helper()

	addrs := make(map[uint64]string, 3)
	listeners := make([]net.Listener, 3)
	lc := net.ListenConfig{}

	for i := range 3 {
		lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen %d: %v", i, err)
		}
		listeners[i] = lis
		addrs[uint64(i+1)] = lis.Addr().String()
	}

	transport := peer.NewSharedTransport(addrs)
	tc := &grpcTestCluster{transport: transport}

	var applied [3]struct {
		mu    sync.Mutex
		count int
	}

	for i := range 3 {
		id := uint64(i + 1)
		idx := i

		peerSrv := peer.NewServer(t.TempDir())
		gs := grpc.NewServer()
		peer.RegisterServer(gs, peerSrv)
		tc.servers = append(tc.servers, gs)
		go func() { _ = gs.Serve(listeners[idx]) }()

		dir := t.TempDir()
		shardTransport := transport.ForShard(0, addrs)

		node, err := scrapraft.Open(scrapraft.Config{
			ID:           id,
			Peers:        addrs,
			DataDir:      dir,
			TickInterval: 10 * time.Millisecond,
			Transport:    shardTransport,
			Apply: func(entries []raftpb.Entry, _ uint64) error {
				applied[idx].mu.Lock()
				defer applied[idx].mu.Unlock()
				for _, e := range entries {
					if e.Type == raftpb.EntryNormal && len(e.Data) > 0 {
						applied[idx].count++
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Open node %d: %v", id, err)
		}
		tc.nodes = append(tc.nodes, node)

		peerSrv.SetRaftRouter(peer.RaftRouterFunc(func(ctx context.Context, _ uint64, msg raftpb.Message) error {
			return node.Step(ctx, msg)
		}))
	}

	t.Cleanup(func() {
		transport.Close()
		for _, n := range tc.nodes {
			n.Stop()
		}
		for _, s := range tc.servers {
			s.GracefulStop()
		}
	})

	return tc
}

func (tc *grpcTestCluster) waitForLeader(t *testing.T) *scrapraft.Node {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range tc.nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected via gRPC transport")
	return nil
}

func TestGRPCTransport_LeaderElection(t *testing.T) {
	tc := startGRPCCluster(t)
	leader := tc.waitForLeader(t)

	leaderCount := 0
	for _, n := range tc.nodes {
		if n.IsLeader() {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaderCount)
	}
	if leader.LeaderID() == 0 {
		t.Fatal("leader ID should not be 0")
	}
}

func TestGRPCTransport_ProposeAndApply(t *testing.T) {
	tc := startGRPCCluster(t)
	leader := tc.waitForLeader(t)

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId: "tx-grpc-001",
				DocumentName:  "doc.xml",
				BlockId:       1,
				TotalBytes:    42,
				Sha256:        make([]byte, 32),
			},
		},
	}
	data, err := proto.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := leader.Propose(ctx, data); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// Wait for all 3 nodes to apply
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Check applied via Raft's AppliedIndex
		allApplied := true
		for _, n := range tc.nodes {
			if n.AppliedIndex() < 2 {
				allApplied = false
				break
			}
		}
		if allApplied {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i, n := range tc.nodes {
		t.Logf("node %d: appliedIndex=%d", i+1, n.AppliedIndex())
	}
	t.Fatal("not all nodes applied the entry via gRPC transport")
}

//nolint:gocognit // integration test with peer kill + quorum verification
func TestGRPCTransport_DeadPeerTolerance(t *testing.T) {
	tc := startGRPCCluster(t)
	leader := tc.waitForLeader(t)

	// Kill one non-leader server
	killedIdx := -1
	for i, n := range tc.nodes {
		if !n.IsLeader() {
			tc.servers[i].Stop()
			killedIdx = i
			break
		}
	}
	if killedIdx == -1 {
		t.Fatal("could not find non-leader to kill")
	}

	// Propose should still work — quorum of 2/3 is met
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := leader.Propose(ctx, []byte("after-kill")); err != nil {
		t.Fatalf("Propose after kill: %v", err)
	}

	// Verify the 2 surviving nodes apply
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		applied := 0
		for i, n := range tc.nodes {
			if i == killedIdx {
				continue
			}
			if n.AppliedIndex() >= 2 {
				applied++
			}
		}
		if applied >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("surviving nodes did not apply after peer death")
}
