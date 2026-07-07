package raft

// Lagging-follower install-snapshot harness (#462 / #471): a restartable
// 3-node in-process cluster with a fault-injectable transport and small
// snapshot thresholds, so a follower can be driven past the leader's
// compaction window and through a real install-snapshot. These are the paths
// that run when a Member falls behind — the moment a consensus bug turns
// routine lag into a bricked voter.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// faultableTransport delivers messages in-process and supports fault
// injection: nodes deregister while stopped (messages to them drop, like a
// crashed pod), and an optional intercept can observe or drop messages. A
// dropped message is reported to the sender's StatusReporter, matching the
// production transport contract (#462).
type faultableTransport struct {
	mu    sync.RWMutex
	nodes map[uint64]*Node
	// reporters holds each node's StatusReporter, registered through the
	// per-node ReporterSink handle like production's per-shard transport.
	reporters map[uint64]StatusReporter
	// intercept, when set, runs per message before delivery; returning false
	// drops the message.
	intercept func(msg raftpb.Message) bool
}

func newFaultableTransport() *faultableTransport {
	return &faultableTransport{
		nodes:     make(map[uint64]*Node),
		reporters: make(map[uint64]StatusReporter),
	}
}

func (t *faultableTransport) register(id uint64, n *Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes[id] = n
}

func (t *faultableTransport) deregister(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.nodes, id)
}

func (t *faultableTransport) setIntercept(fn func(msg raftpb.Message) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.intercept = fn
}

// forNode returns the per-node transport handle a Node is opened with; it
// implements ReporterSink so Open can register the node's StatusReporter.
func (t *faultableTransport) forNode(id uint64) *nodeTransport {
	return &nodeTransport{shared: t, self: id}
}

type nodeTransport struct {
	shared *faultableTransport
	self   uint64
}

func (nt *nodeTransport) Send(msgs []raftpb.Message) {
	nt.shared.send(msgs)
}

func (nt *nodeTransport) SetStatusReporter(r StatusReporter) {
	nt.shared.mu.Lock()
	defer nt.shared.mu.Unlock()
	nt.shared.reporters[nt.self] = r
}

func (t *faultableTransport) send(msgs []raftpb.Message) {
	t.mu.RLock()
	intercept := t.intercept
	t.mu.RUnlock()

	for _, m := range msgs {
		if intercept != nil && !intercept(m) {
			t.reportDropped(m)
			continue
		}
		t.mu.RLock()
		target := t.nodes[m.To]
		t.mu.RUnlock()
		if target != nil {
			_ = target.Step(context.Background(), m) // best-effort in-process delivery
		}
	}
}

// reportDropped reports an intercept-dropped message to the sender's
// reporter, as the production transport does for its drop paths.
func (t *faultableTransport) reportDropped(m raftpb.Message) {
	t.mu.RLock()
	reporter := t.reporters[m.From]
	t.mu.RUnlock()
	if reporter == nil {
		return
	}
	reporter.ReportUnreachable(m.To)
	if m.Type == raftpb.MsgSnap {
		reporter.ReportSnapshotFailure(m.To)
	}
}

const catchupClusterSize = 3

// clusterIndex maps a 1-based raft node ID to its slot in the fixed-size
// cluster arrays; IDs are always 1..catchupClusterSize in these tests.
func clusterIndex(id uint64) int {
	return int(id%(catchupClusterSize+1)) - 1
}

// catchupCluster is a restartable 3-node cluster. Nodes keep their DataDirs
// across stop/start, so stopping a node models a pod crash and starting it
// again models the restart that must recover from local disk.
type catchupCluster struct {
	t         *testing.T
	transport *faultableTransport
	dirs      [catchupClusterSize]string
	peers     map[uint64]string

	mu       sync.Mutex
	nodes    [catchupClusterSize]*Node
	applied  [catchupClusterSize]uint64 // highest applied entry index per node
	restores [catchupClusterSize]atomic.Uint64
}

func startCatchupCluster(t *testing.T) *catchupCluster {
	t.Helper()
	base := t.TempDir()
	c := &catchupCluster{
		t:         t,
		transport: newFaultableTransport(),
		peers: map[uint64]string{
			1: "localhost:9091",
			2: "localhost:9092",
			3: "localhost:9093",
		},
	}
	for i := range catchupClusterSize {
		c.dirs[i] = filepath.Join(base, fmt.Sprintf("node-%d", i+1))
		c.start(t, uint64(i+1))
	}
	t.Cleanup(func() {
		for i := range catchupClusterSize {
			c.stop(uint64(i + 1))
		}
	})
	return c
}

// start opens node id from its existing DataDir with snapshot thresholds low
// enough that a modest proposal burst compacts a stopped follower out of the
// log-replication window.
func (c *catchupCluster) start(t *testing.T, id uint64) {
	t.Helper()
	idx := clusterIndex(id)
	node, err := Open(Config{
		ID:                     id,
		Peers:                  c.peers,
		DataDir:                c.dirs[idx],
		TickInterval:           5 * time.Millisecond,
		Transport:              c.transport.forNode(id),
		MaxSnapCount:           8,
		SnapshotCatchUpEntries: 4,
		Apply: func(entries []raftpb.Entry, _ uint64) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			for _, e := range entries {
				if e.Index > c.applied[idx] {
					c.applied[idx] = e.Index
				}
			}
			return nil
		},
		Snapshot: func(uint64) ([]byte, error) {
			return []byte(snapshotTestManifest), nil
		},
		Restore: func(data []byte) error {
			if string(data) != snapshotTestManifest {
				return fmt.Errorf("unexpected snapshot manifest %q", string(data))
			}
			c.restores[idx].Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Open node %d: %v", id, err)
	}
	c.mu.Lock()
	c.nodes[idx] = node
	c.mu.Unlock()
	c.transport.register(id, node)
}

// stop crashes node id: it deregisters from the transport (in-flight messages
// drop) and stops the node. The DataDir survives for a later start.
func (c *catchupCluster) stop(id uint64) {
	idx := clusterIndex(id)
	c.mu.Lock()
	node := c.nodes[idx]
	c.nodes[idx] = nil
	c.mu.Unlock()
	if node == nil {
		return
	}
	c.transport.deregister(id)
	node.Stop()
}

func (c *catchupCluster) node(id uint64) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[id-1]
}

func (c *catchupCluster) appliedIndex(id uint64) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applied[id-1]
}

func (c *catchupCluster) restoreCount(id uint64) uint64 {
	return c.restores[id-1].Load()
}

// waitForClusterLeader returns the current leader once one is elected among
// the running nodes.
func (c *catchupCluster) waitForClusterLeader(t *testing.T) *Node {
	t.Helper()
	var leader *Node
	waitForCondition(t, "cluster leader election", func() bool {
		for id := uint64(1); id <= catchupClusterSize; id++ {
			if n := c.node(id); n != nil && n.IsLeader() {
				leader = n
				return true
			}
		}
		return false
	})
	return leader
}

// followerID returns a running node that is not the leader.
func (c *catchupCluster) followerID(t *testing.T, leader *Node) uint64 {
	t.Helper()
	for id := uint64(1); id <= catchupClusterSize; id++ {
		if n := c.node(id); n != nil && n != leader {
			return id
		}
	}
	t.Fatal("no running follower found")
	return 0
}

// proposeAndWaitQuorum proposes count entries through the current leader
// (following leadership moves) and waits until every RUNNING node applied
// them.
func (c *catchupCluster) proposeAndWaitQuorum(t *testing.T, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range count {
		c.proposeOnce(ctx, t, i)
	}
	leader := c.waitForClusterLeader(t)
	target := leader.CommitIndex()
	waitForCondition(t, "quorum applied proposals", func() bool {
		for id := uint64(1); id <= catchupClusterSize; id++ {
			if c.node(id) == nil {
				continue
			}
			if c.appliedIndex(id) < target {
				return false
			}
		}
		return true
	})
}

// proposeOnce proposes one entry via the current leader, retrying across
// leadership changes.
func (c *catchupCluster) proposeOnce(ctx context.Context, t *testing.T, seq int) {
	t.Helper()
	data := fmt.Sprintf("catchup-entry-%d", seq)
	for {
		leader := c.waitForClusterLeader(t)
		proposeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := leader.Propose(proposeCtx, []byte(data))
		cancel()
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("propose %d: %v", seq, ctx.Err())
		}
	}
}

// waitLeaderCompactedPast waits until the leader's in-memory log no longer
// contains index, so a follower at that index needs an install-snapshot.
func waitLeaderCompactedPast(t *testing.T, c *catchupCluster, index uint64) {
	t.Helper()
	waitForCondition(t, "leader log compaction past follower index", func() bool {
		leader := c.waitForClusterLeader(t)
		first, err := leader.storage.FirstIndex()
		if err != nil {
			return false
		}
		return first > index+1
	})
}

// A dropped MsgSnap parks the follower's Progress in StateSnapshot on the
// leader with nothing to un-park it — the transport must report the failure
// so raft retries the snapshot (#462). This drops the FIRST MsgSnap and
// asserts the follower still catches up via the reported failure + retry;
// without the StatusReporter plumbing the leader waits forever.
func TestDroppedSnapshotMessageIsReportedAndRetried(t *testing.T) {
	c := startCatchupCluster(t)
	leader := c.waitForClusterLeader(t)
	c.proposeAndWaitQuorum(t, 3)

	followerID := c.followerID(t, leader)
	followerIndex := c.appliedIndex(followerID)
	c.stop(followerID)

	c.proposeAndWaitQuorum(t, 40)
	waitLeaderCompactedPast(t, c, followerIndex)

	var snapsDropped atomic.Uint64
	c.transport.setIntercept(func(m raftpb.Message) bool {
		if m.Type == raftpb.MsgSnap && snapsDropped.CompareAndSwap(0, 1) {
			return false // drop exactly the first snapshot message
		}
		return true
	})

	c.start(t, followerID)
	leader = c.waitForClusterLeader(t)
	catchupTarget := leader.CommitIndex()
	waitForCondition(t, "follower catches up after dropped MsgSnap", func() bool {
		return c.node(followerID).AppliedIndex() >= catchupTarget
	})
	if snapsDropped.Load() == 0 {
		t.Fatal("no MsgSnap was dropped; the test did not exercise the report-and-retry path")
	}
	if c.restoreCount(followerID) == 0 {
		t.Fatal("follower caught up without install-snapshot; want a retried MsgSnap")
	}
}

// TestLaggingFollowerCatchesUpViaInstallSnapshot drives the full lifecycle:
// a follower crashes, the cluster commits past the compaction window, and the
// restarted follower must be caught up by a real install-snapshot (observed
// via the Restore hook), then keep applying live traffic and survive another
// restart.
func TestLaggingFollowerCatchesUpViaInstallSnapshot(t *testing.T) {
	c := startCatchupCluster(t)
	leader := c.waitForClusterLeader(t)
	c.proposeAndWaitQuorum(t, 3)

	followerID := c.followerID(t, leader)
	followerIndex := c.appliedIndex(followerID)
	c.stop(followerID)

	// Drive the running majority far past MaxSnapCount(8) +
	// SnapshotCatchUpEntries(4) so the stopped follower falls out of the
	// log window and the leader must install a snapshot.
	c.proposeAndWaitQuorum(t, 40)
	waitLeaderCompactedPast(t, c, followerIndex)

	c.start(t, followerID)
	leader = c.waitForClusterLeader(t)
	catchupTarget := leader.CommitIndex()
	waitForCondition(t, "restarted follower catches up", func() bool {
		return c.node(followerID).AppliedIndex() >= catchupTarget
	})
	if c.restoreCount(followerID) == 0 {
		t.Fatal("follower caught up without a Restore call; want install-snapshot (not log replay)")
	}

	// The caught-up follower keeps applying live traffic.
	c.proposeAndWaitQuorum(t, 3)

	// And a restart from its own disk state stays healthy.
	c.stop(followerID)
	c.start(t, followerID)
	leader = c.waitForClusterLeader(t)
	restartTarget := leader.CommitIndex()
	waitForCondition(t, "follower healthy after post-snapshot restart", func() bool {
		return c.node(followerID).AppliedIndex() >= restartTarget
	})
}
