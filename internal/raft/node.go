package raft

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	raftpb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"

	"github.com/petabytecl/scrap/internal/logbridge"
)

const (
	defaultTickInterval  = 100 * time.Millisecond
	defaultMaxSizePerMsg = 1024 * 1024 // 1 MiB
	defaultMaxInflight   = 256
	defaultMaxSnapCount  = 10000
	defaultElectionTick  = 10
	defaultHeartbeatTick = 1
)

// ApplyFunc applies committed entries to the state machine. replayUntil is the
// startup replay watermark: entries with Index <= replayUntil were already applied
// before a restart (re-delivered replay) and must not emit live trace spans. It is
// passed on every call so the apply path is correct from the very first invocation,
// even though Open starts the run loop before returning the node.
type ApplyFunc func(entries []raftpb.Entry, replayUntil uint64) error

type SnapshotFunc func() (data []byte, err error)

type RestoreFunc func(data []byte) error

type Config struct {
	ID           uint64
	Peers        map[uint64]string
	DataDir      string
	Apply        ApplyFunc
	Snapshot     SnapshotFunc
	Restore      RestoreFunc
	Transport    Transport
	TickInterval time.Duration
	Logger       *slog.Logger

	MaxSnapCount    uint64
	MaxWALSize      int64
	ElectionTick    int
	HeartbeatTick   int
	MaxSizePerMsg   uint64
	MaxInflightMsgs int
}

type RaftNode struct {
	cfg       Config
	logger    *slog.Logger
	node      raft.Node
	storage   *raft.MemoryStorage
	wal       *wal.WAL
	snap      *snap.Snapshotter
	transport Transport

	appliedIndex      uint64
	snapshotIndex     uint64
	replayCommitIndex uint64
	commitIndex       uint64

	leaderID atomic.Uint64
	readMu   sync.Mutex
	readMap  map[string]chan uint64

	stopc chan struct{}
	donec chan struct{}
}

//nolint:gocognit,cyclop // initialization function validating multiple config fields and bootstrapping WAL/snapshot
func Open(cfg Config) (*RaftNode, error) {
	if cfg.ID == 0 {
		return nil, errors.New("raft: node ID must be non-zero")
	}
	if cfg.Apply == nil {
		return nil, errors.New("raft: Apply function is required")
	}
	if cfg.Transport == nil {
		return nil, errors.New("raft: Transport is required")
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = defaultTickInterval
	}
	if cfg.ElectionTick == 0 {
		cfg.ElectionTick = defaultElectionTick
	}
	if cfg.HeartbeatTick == 0 {
		cfg.HeartbeatTick = defaultHeartbeatTick
	}
	if cfg.MaxSizePerMsg == 0 {
		cfg.MaxSizePerMsg = defaultMaxSizePerMsg
	}
	if cfg.MaxInflightMsgs == 0 {
		cfg.MaxInflightMsgs = defaultMaxInflight
	}
	if cfg.MaxSnapCount == 0 {
		cfg.MaxSnapCount = defaultMaxSnapCount
	}

	walDir := cfg.DataDir + "/wal"
	snapDir := cfg.DataDir + "/snap"

	for _, d := range []string{walDir, snapDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("raft: mkdir %s: %w", d, err)
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	lg := logbridge.NewZapLogger(logger)
	snapshotter := snap.New(lg, snapDir)

	walExists := wal.Exist(walDir)

	n := &RaftNode{
		cfg:       cfg,
		logger:    logger,
		snap:      snapshotter,
		transport: cfg.Transport,
		readMap:   make(map[string]chan uint64),
		stopc:     make(chan struct{}),
		donec:     make(chan struct{}),
	}

	if walExists {
		if err := n.restartNode(lg, walDir); err != nil {
			return nil, err
		}
	} else {
		if err := n.startNode(lg, walDir); err != nil {
			return nil, err
		}
	}

	go n.run()
	return n, nil
}

func (n *RaftNode) startNode(lg *zap.Logger, walDir string) error {
	w, err := wal.Create(lg, walDir, nil)
	if err != nil {
		return fmt.Errorf("raft: create WAL: %w", err)
	}
	n.wal = w

	var peers []raft.Peer
	for id := range n.cfg.Peers {
		peers = append(peers, raft.Peer{ID: id})
	}

	n.storage = raft.NewMemoryStorage()
	c := &raft.Config{
		ID:              n.cfg.ID,
		ElectionTick:    n.cfg.ElectionTick,
		HeartbeatTick:   n.cfg.HeartbeatTick,
		Storage:         n.storage,
		MaxSizePerMsg:   n.cfg.MaxSizePerMsg,
		MaxInflightMsgs: n.cfg.MaxInflightMsgs,
		Logger:          logbridge.NewRaftLogger(n.logger),
	}

	n.node = raft.StartNode(c, peers)
	return nil
}

//nolint:gocognit,cyclop // restart orchestrates snapshot load, WAL replay, and storage reconstruction
func (n *RaftNode) restartNode(lg *zap.Logger, walDir string) error {
	snapshot, err := n.snap.Load()
	if err != nil && !errors.Is(err, snap.ErrNoSnapshot) {
		return fmt.Errorf("raft: load snapshot: %w", err)
	}

	var walSnap walpb.Snapshot
	if snapshot != nil {
		walSnap.Index = snapshot.Metadata.Index
		walSnap.Term = snapshot.Metadata.Term
	}

	w, err := wal.Open(lg, walDir, walSnap)
	if err != nil {
		return fmt.Errorf("raft: open WAL: %w", err)
	}

	_, hardState, entries, err := w.ReadAll()
	if err != nil {
		_ = w.Close() // best-effort close on read failure
		return fmt.Errorf("raft: read WAL: %w", err)
	}
	n.wal = w

	n.storage = raft.NewMemoryStorage()

	if snapshot != nil {
		if err := n.storage.ApplySnapshot(*snapshot); err != nil {
			return fmt.Errorf("raft: apply snapshot to storage: %w", err)
		}
		n.snapshotIndex = snapshot.Metadata.Index
		n.appliedIndex = snapshot.Metadata.Index

		if n.cfg.Restore != nil {
			if err := n.cfg.Restore(snapshot.Data); err != nil {
				return fmt.Errorf("raft: restore snapshot: %w", err)
			}
		}
	}

	if err := n.storage.SetHardState(hardState); err != nil {
		return fmt.Errorf("raft: set hard state: %w", err)
	}
	// Replay watermark = the durably-applied index (the loaded snapshot's index, set
	// above as n.appliedIndex, or 0 with no snapshot) — NOT hardState.Commit. Entries
	// committed but not yet applied before a crash are re-delivered and applied for the
	// first time after restart, so they must emit live spans; only entries known to
	// have applied are suppressed as replay. Basing this on Commit would leave a gap in
	// crash-recovery apply evidence (ADR 0013 §3).
	n.replayCommitIndex = n.appliedIndex
	atomic.StoreUint64(&n.commitIndex, hardState.Commit)
	if err := n.storage.Append(entries); err != nil {
		return fmt.Errorf("raft: append entries: %w", err)
	}

	cs := deriveConfState(entries, snapshot)
	if cs != nil {
		if err := n.storage.ApplySnapshot(raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index:     hardState.Commit,
				Term:      hardState.Term,
				ConfState: *cs,
			},
		}); err != nil {
			return fmt.Errorf("raft: apply derived conf state: %w", err)
		}
	}

	c := &raft.Config{
		ID:              n.cfg.ID,
		ElectionTick:    n.cfg.ElectionTick,
		HeartbeatTick:   n.cfg.HeartbeatTick,
		Storage:         n.storage,
		MaxSizePerMsg:   n.cfg.MaxSizePerMsg,
		MaxInflightMsgs: n.cfg.MaxInflightMsgs,
		Applied:         n.appliedIndex,
		Logger:          logbridge.NewRaftLogger(n.logger),
	}

	n.node = raft.RestartNode(c)
	return nil
}

//nolint:gocognit,cyclop // main event loop processing ticks, ready states, and shutdown
func (n *RaftNode) run() {
	defer close(n.donec)
	ticker := time.NewTicker(n.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.node.Tick()

		case rd := <-n.node.Ready():
			if err := n.wal.Save(rd.HardState, rd.Entries); err != nil {
				panic(fmt.Sprintf("raft: WAL save: %v", err))
			}

			if !raft.IsEmptyHardState(rd.HardState) {
				atomic.StoreUint64(&n.commitIndex, rd.HardState.Commit)
			}

			if !raft.IsEmptySnap(rd.Snapshot) {
				if err := n.snap.SaveSnap(rd.Snapshot); err != nil {
					panic(fmt.Sprintf("raft: save snapshot: %v", err))
				}
				if err := n.wal.SaveSnapshot(walpb.Snapshot{
					Index: rd.Snapshot.Metadata.Index,
					Term:  rd.Snapshot.Metadata.Term,
				}); err != nil {
					panic(fmt.Sprintf("raft: WAL save snapshot: %v", err))
				}
				if err := n.storage.ApplySnapshot(rd.Snapshot); err != nil {
					panic(fmt.Sprintf("raft: storage apply snapshot: %v", err))
				}
			}

			if err := n.storage.Append(rd.Entries); err != nil {
				panic(fmt.Sprintf("raft: storage append: %v", err))
			}

			n.transport.Send(rd.Messages)

			if len(rd.CommittedEntries) > 0 {
				if err := n.cfg.Apply(rd.CommittedEntries, n.replayCommitIndex); err != nil {
					panic(fmt.Sprintf("raft: apply: %v", err))
				}
				atomic.StoreUint64(&n.appliedIndex, rd.CommittedEntries[len(rd.CommittedEntries)-1].Index)
			}

			if rd.SoftState != nil {
				n.leaderID.Store(rd.SoftState.Lead)
			}

			n.publishReadStates(rd.ReadStates)

			n.node.Advance()

		case <-n.stopc:
			n.node.Stop()
			_ = n.wal.Close() // best-effort close on shutdown
			return
		}
	}
}

func (n *RaftNode) publishReadStates(states []raft.ReadState) {
	n.readMu.Lock()
	defer n.readMu.Unlock()

	for _, rs := range states {
		key := string(rs.RequestCtx)
		if ch, ok := n.readMap[key]; ok {
			ch <- rs.Index
			delete(n.readMap, key)
		}
	}
}

func (n *RaftNode) Propose(ctx context.Context, data []byte) error {
	return n.node.Propose(ctx, data)
}

func (n *RaftNode) ReadIndex(ctx context.Context) (uint64, error) {
	rctx := fmt.Sprintf("ri-%d-%d", n.cfg.ID, time.Now().UnixNano())
	ch := make(chan uint64, 1)

	n.readMu.Lock()
	n.readMap[rctx] = ch
	n.readMu.Unlock()

	if err := n.node.ReadIndex(ctx, []byte(rctx)); err != nil {
		n.readMu.Lock()
		delete(n.readMap, rctx)
		n.readMu.Unlock()
		return 0, err
	}

	select {
	case idx := <-ch:
		return idx, nil
	case <-ctx.Done():
		n.readMu.Lock()
		delete(n.readMap, rctx)
		n.readMu.Unlock()
		return 0, ctx.Err()
	}
}

func (n *RaftNode) Step(ctx context.Context, msg raftpb.Message) error {
	return n.node.Step(ctx, msg)
}

func (n *RaftNode) IsLeader() bool {
	return n.leaderID.Load() == n.cfg.ID
}

func (n *RaftNode) LeaderID() uint64 {
	return n.leaderID.Load()
}

func (n *RaftNode) AppliedIndex() uint64 {
	return atomic.LoadUint64(&n.appliedIndex)
}

// CommitIndex returns the highest Raft log index known to be committed. Apply lag
// (commit - applied) is the canonical Raft saturation signal on the USE dashboard.
func (n *RaftNode) CommitIndex() uint64 {
	return atomic.LoadUint64(&n.commitIndex)
}

func deriveConfState(entries []raftpb.Entry, snapshot *raftpb.Snapshot) *raftpb.ConfState {
	if snapshot != nil {
		cs := snapshot.Metadata.ConfState
		return &cs
	}

	var voters []uint64
	for _, e := range entries {
		if e.Type != raftpb.EntryConfChange {
			continue
		}
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(e.Data); err != nil {
			continue
		}
		switch cc.Type {
		case raftpb.ConfChangeAddNode:
			voters = appendUnique(voters, cc.NodeID)
		case raftpb.ConfChangeRemoveNode:
			voters = removeID(voters, cc.NodeID)
		case raftpb.ConfChangeUpdateNode, raftpb.ConfChangeAddLearnerNode:
			// no voter-set changes needed for updates or learner additions
		}
	}

	if len(voters) == 0 {
		return nil
	}
	return &raftpb.ConfState{Voters: voters}
}

func appendUnique(ids []uint64, id uint64) []uint64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeID(ids []uint64, id uint64) []uint64 {
	result := ids[:0]
	for _, existing := range ids {
		if existing != id {
			result = append(result, existing)
		}
	}
	return result
}

func (n *RaftNode) Stop() {
	select {
	case n.stopc <- struct{}{}:
	case <-n.donec:
		return
	}
	<-n.donec
}
