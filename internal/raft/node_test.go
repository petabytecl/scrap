package raft_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
)

type inProcessTransport struct {
	mu    sync.RWMutex
	nodes map[uint64]*scrapraft.Node
}

func newInProcessTransport() *inProcessTransport {
	return &inProcessTransport{nodes: make(map[uint64]*scrapraft.Node)}
}

func (t *inProcessTransport) Register(id uint64, n *scrapraft.Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes[id] = n
}

func (t *inProcessTransport) Send(msgs []raftpb.Message) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, m := range msgs {
		if target, ok := t.nodes[m.To]; ok {
			_ = target.Step(context.Background(), m) // best-effort in-process delivery
		}
	}
}

type testCluster struct {
	nodes     []*scrapraft.Node
	transport *inProcessTransport
	applied   [3][]raftpb.Entry
	mu        [3]sync.Mutex
}

func startTestCluster(t *testing.T) *testCluster {
	t.Helper()
	transport := newInProcessTransport()

	peers := map[uint64]string{
		1: "localhost:9091",
		2: "localhost:9092",
		3: "localhost:9093",
	}

	tc := &testCluster{transport: transport}

	for i := range 3 {
		id := uint64(i + 1)
		idx := i
		dir := t.TempDir()

		node, err := scrapraft.Open(scrapraft.Config{
			ID:           id,
			Peers:        peers,
			DataDir:      dir,
			TickInterval: 10 * time.Millisecond,
			Transport:    transport,
			Apply: func(entries []raftpb.Entry, _ uint64) error {
				tc.mu[idx].Lock()
				defer tc.mu[idx].Unlock()
				for _, e := range entries {
					if e.Type == raftpb.EntryNormal && len(e.Data) > 0 {
						tc.applied[idx] = append(tc.applied[idx], e)
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Open node %d: %v", id, err)
		}
		tc.nodes = append(tc.nodes, node)
		transport.Register(id, node)
	}

	t.Cleanup(func() {
		for _, n := range tc.nodes {
			n.Stop()
		}
	})

	return tc
}

const leaderElectionTimeout = 5 * time.Second

func (tc *testCluster) waitForLeader(t *testing.T) uint64 {
	t.Helper()
	deadline := time.Now().Add(leaderElectionTimeout)
	for time.Now().Before(deadline) {
		for _, n := range tc.nodes {
			if n.IsLeader() {
				return n.LeaderID()
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return 0
}

func (tc *testCluster) leader() *scrapraft.Node {
	for _, n := range tc.nodes {
		if n.IsLeader() {
			return n
		}
	}
	return nil
}

func (tc *testCluster) appliedCount(idx int) int {
	tc.mu[idx].Lock()
	defer tc.mu[idx].Unlock()
	return len(tc.applied[idx])
}

func TestThreeNodeElection(t *testing.T) {
	tc := startTestCluster(t)

	leaderID := tc.waitForLeader(t)
	if leaderID == 0 {
		t.Fatal("leader ID should not be 0")
	}

	leaderCount := 0
	for _, n := range tc.nodes {
		if n.IsLeader() {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaderCount)
	}
}

//nolint:gocognit,cyclop // integration test verifying proposal replication across a 3-node cluster
func TestProposeAndApply(t *testing.T) {
	tc := startTestCluster(t)
	tc.waitForLeader(t)

	leader := tc.leader()
	if leader == nil {
		t.Fatal("no leader found")
	}

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId: "tx-raft-test",
				DocumentName:  "doc.xml",
				BlockId:       1,
				TotalBytes:    1024,
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

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allApplied := true
		for i := range 3 {
			if tc.appliedCount(i) < 1 {
				allApplied = false
				break
			}
		}
		if allApplied {
			for i := range 3 {
				tc.mu[i].Lock()
				entry := tc.applied[i][0]
				tc.mu[i].Unlock()

				decoded := &scrapv1.RaftCommand{}
				if err := proto.Unmarshal(entry.Data, decoded); err != nil {
					t.Fatalf("node %d: Unmarshal: %v", i+1, err)
				}
				doc := decoded.GetCommitDoc()
				if doc == nil {
					t.Fatalf("node %d: CommitDoc is nil", i+1)
				}
				if doc.GetTransactionId() != "tx-raft-test" {
					t.Fatalf("node %d: TransactionId: got %q", i+1, doc.GetTransactionId())
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("not all nodes applied the entry within timeout")
}

//nolint:gocognit,cyclop // integration test verifying leader failover and re-election
func TestLeaderFailoverReElection(t *testing.T) {
	tc := startTestCluster(t)
	oldLeaderID := tc.waitForLeader(t)

	tc.nodes[oldLeaderID-1].Stop()
	tc.transport.mu.Lock()
	delete(tc.transport.nodes, oldLeaderID)
	tc.transport.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	var newLeaderID uint64
	for time.Now().Before(deadline) {
		for _, n := range tc.nodes {
			lid := n.LeaderID()
			if lid != 0 && lid != oldLeaderID && n.IsLeader() {
				newLeaderID = lid
				break
			}
		}
		if newLeaderID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if newLeaderID == 0 {
		t.Fatal("no new leader elected after stopping old leader")
	}
	if newLeaderID == oldLeaderID {
		t.Fatal("new leader should differ from old leader")
	}

	newLeader := tc.nodes[newLeaderID-1]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := newLeader.Propose(ctx, []byte("post-failover")); err != nil {
		t.Fatalf("Propose on new leader: %v", err)
	}

	var applied atomic.Int32
	dl2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl2) {
		applied.Store(0)
		for i := range 3 {
			if uint64(i+1) == oldLeaderID {
				continue
			}
			if tc.appliedCount(i) > 0 {
				applied.Add(1)
			}
		}
		if applied.Load() >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("remaining nodes did not apply after failover (applied: %d)", applied.Load())
}

func TestMultipleProposals(t *testing.T) {
	tc := startTestCluster(t)
	tc.waitForLeader(t)

	leader := tc.leader()
	ctx := context.Background()

	for i := range 5 {
		data := []byte(fmt.Sprintf("proposal-%d", i))
		if err := leader.Propose(ctx, data); err != nil {
			t.Fatalf("Propose %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for i := range 3 {
			if tc.appliedCount(i) < 5 {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := range 3 {
		t.Logf("node %d applied %d entries", i+1, tc.appliedCount(i))
	}
	t.Fatal("not all 5 proposals applied on all nodes")
}
