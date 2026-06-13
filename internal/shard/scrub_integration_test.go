package shard_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"

	"github.com/petabytecl/scrap/internal/shard"
)

type shardTransport struct {
	mu     sync.RWMutex
	shards map[uint64]*shard.Shard
}

func newShardTransport() *shardTransport {
	return &shardTransport{shards: make(map[uint64]*shard.Shard)}
}

func (t *shardTransport) Register(id uint64, s *shard.Shard) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shards[id] = s
}

func (t *shardTransport) Send(msgs []raftpb.Message) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, m := range msgs {
		if target, ok := t.shards[m.To]; ok {
			_ = target.RaftStep(context.Background(), m)
		}
	}
}

type shardCluster struct {
	shards    []*shard.Shard
	transport *shardTransport
}

func openShardCluster(t *testing.T) *shardCluster {
	t.Helper()
	transport := newShardTransport()

	peers := map[uint64]string{
		1: "localhost:9091",
		2: "localhost:9092",
		3: "localhost:9093",
	}

	sc := &shardCluster{transport: transport}

	for i := range 3 {
		id := uint64(i + 1)
		dir := t.TempDir()

		s, err := shard.Open(shard.Config{
			DataDir:      dir,
			ShardID:      0,
			RaftID:       id,
			Peers:        peers,
			TickInterval: 10 * time.Millisecond,
			Transport:    transport,
		})
		if err != nil {
			t.Fatalf("Open shard %d: %v", id, err)
		}
		sc.shards = append(sc.shards, s)
		transport.Register(id, s)
	}

	t.Cleanup(func() {
		for _, s := range sc.shards {
			_ = s.Close()
		}
	})

	return sc
}

func (sc *shardCluster) waitForLeader(t *testing.T) *shard.Shard {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range sc.shards {
			if s.IsLeader() {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected")
	return nil
}

func TestLightScrubIntegration_AllMatch(t *testing.T) {
	sc := openShardCluster(t)
	leader := sc.waitForLeader(t)
	ctx := context.Background()

	_, err := leader.WriteDocument(ctx, "tx-scrub-int", "doc.xml", "text/xml", "", bytes.NewReader([]byte("integrity")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	result, err := leader.ProposeConsistencyCheck(ctx, "scrub-int-001")
	if err != nil {
		t.Fatalf("ProposeConsistencyCheck: %v", err)
	}
	if result.SHA256 == [32]byte{} {
		t.Fatal("leader hash should not be zero")
	}

	time.Sleep(200 * time.Millisecond)

	for i, s := range sc.shards {
		cached, ok := s.GetScrubResult("scrub-int-001")
		if !ok {
			t.Fatalf("shard %d: no cached result", i+1)
		}
		if cached.SHA256 != result.SHA256 {
			t.Fatalf("shard %d: hash mismatch: %x vs leader %x", i+1, cached.SHA256, result.SHA256)
		}
	}
}

func TestLightScrubIntegration_DetectsMismatch(t *testing.T) {
	sc := openShardCluster(t)
	leader := sc.waitForLeader(t)
	ctx := context.Background()

	_, err := leader.WriteDocument(ctx, "tx-corrupt", "doc.xml", "text/xml", "", bytes.NewReader([]byte("original")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Corrupt one non-leader shard's projection by writing extra data directly.
	corruptIdx := -1
	for i, s := range sc.shards {
		if !s.IsLeader() {
			s.CorruptProjectionForTest("tx-extra-corrupt", 999, 1, false)
			corruptIdx = i
			break
		}
	}
	if corruptIdx == -1 {
		t.Fatal("could not find non-leader shard to corrupt")
	}

	_, err = leader.ProposeConsistencyCheck(ctx, "scrub-corrupt-001")
	if err != nil {
		t.Fatalf("ProposeConsistencyCheck: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	leaderResult, ok := leader.GetScrubResult("scrub-corrupt-001")
	if !ok {
		t.Fatal("leader has no cached result")
	}

	corruptResult, ok := sc.shards[corruptIdx].GetScrubResult("scrub-corrupt-001")
	if !ok {
		t.Fatalf("corrupt shard %d has no cached result", corruptIdx+1)
	}

	if corruptResult.SHA256 == leaderResult.SHA256 {
		t.Fatal("corrupt shard should have different hash from leader")
	}
}
