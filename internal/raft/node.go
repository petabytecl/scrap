package raft

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
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
// passed on every call, so the applied path is correct from the very first invocation,
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

type Node struct {
	cfg       Config
	logger    *slog.Logger
	node      raft.Node
	storage   durableStorage
	wal       *wal.WAL
	snap      *snap.Snapshotter
	transport Transport

	appliedIndex      uint64
	snapshotIndex     uint64
	replayCommitIndex uint64
	commitIndex       uint64

	leaderID atomic.Uint64
	stateMu  sync.RWMutex
	readMu   sync.Mutex
	readMap  map[string]chan uint64
	readSeq  atomic.Uint64

	stopc chan struct{}
	donec chan struct{}
}

//nolint:gocognit,cyclop // initialization function validating multiple config fields and bootstrapping WAL/snapshot
func Open(cfg Config) (*Node, error) {
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
	if !walExists && len(cfg.Peers) == 0 {
		return nil, errors.New("raft: at least one peer is required to bootstrap a new node")
	}

	n := &Node{
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

func (n *Node) startNode(lg *zap.Logger, walDir string) error {
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

func (n *Node) restartNode(lg *zap.Logger, walDir string) error {
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

	if err := n.rebuildStorageFromWAL(snapshot, hardState, entries); err != nil {
		// Release the WAL file locks so a retried Open does not fail on them.
		_ = w.Close()
		n.wal = nil
		return err
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

func (n *Node) rebuildStorageFromWAL(snapshot *raftpb.Snapshot, hardState raftpb.HardState, entries []raftpb.Entry) error {
	baseStorage := raft.NewMemoryStorage()
	n.storage = baseStorage

	if snapshot != nil {
		if err := n.restoreSnapshotToStorage(snapshot, &hardState); err != nil {
			return err
		}
	}

	if err := n.storage.SetHardState(hardState); err != nil {
		return fmt.Errorf("raft: set hard state: %w", err)
	}
	if snapshot == nil {
		if cs := n.restartConfState(entries, hardState.Commit); cs != nil {
			n.storage = &initialConfStateStorage{
				MemoryStorage: baseStorage,
				confState:     *cs,
			}
		}
	}
	// Replay watermark = the durably applied index (the loaded snapshot's index, set
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
	return nil
}

func (n *Node) restoreSnapshotToStorage(snapshot *raftpb.Snapshot, hardState *raftpb.HardState) error {
	if err := n.storage.ApplySnapshot(*snapshot); err != nil {
		return fmt.Errorf("raft: apply snapshot to storage: %w", err)
	}
	n.snapshotIndex = snapshot.Metadata.Index
	n.appliedIndex = snapshot.Metadata.Index
	if hardState.Commit < snapshot.Metadata.Index {
		hardState.Commit = snapshot.Metadata.Index
	}

	if n.cfg.Restore != nil {
		if err := n.cfg.Restore(snapshot.Data); err != nil {
			return fmt.Errorf("raft: restore snapshot: %w", err)
		}
	}
	return nil
}

// restartConfState picks the voter set for a snapshot-less restart. When no
// committed conf state exists in the WAL (crash before the bootstrap
// ConfChange entries committed), it falls back to the static peer
// configuration: restarting with an empty voter set would leave the node
// permanently unable to campaign, and membership is static (there is no
// ProposeConfChange path), so cfg.Peers is authoritative.
func (n *Node) restartConfState(entries []raftpb.Entry, commit uint64) *raftpb.ConfState {
	if cs := deriveCommittedConfState(entries, commit); cs != nil {
		return cs
	}
	if commit == 0 {
		return bootstrapConfStateFromPeers(n.cfg.Peers)
	}
	return nil
}

func bootstrapConfStateFromPeers(peers map[uint64]string) *raftpb.ConfState {
	if len(peers) == 0 {
		return nil
	}
	voters := make([]uint64, 0, len(peers))
	for id := range peers {
		voters = append(voters, id)
	}
	slices.Sort(voters)
	return &raftpb.ConfState{Voters: voters}
}

type durableStorage interface {
	raft.Storage
	SetHardState(raftpb.HardState) error
	ApplySnapshot(raftpb.Snapshot) error
	Append([]raftpb.Entry) error
}

type initialConfStateStorage struct {
	*raft.MemoryStorage
	confState raftpb.ConfState
}

func (s *initialConfStateStorage) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	hardState, _, err := s.MemoryStorage.InitialState()
	return hardState, s.confState, err
}

func deriveCommittedConfState(entries []raftpb.Entry, commit uint64) *raftpb.ConfState {
	if commit == 0 {
		return nil
	}
	return deriveConfState(entries, nil, commit)
}

func (n *Node) run() {
	defer close(n.donec)
	ticker := time.NewTicker(n.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.stateMu.Lock()
			n.node.Tick()
			n.drainReadyLocked()
			n.stateMu.Unlock()

		case rd := <-n.node.Ready():
			n.processReady(rd)

		case <-n.stopc:
			n.node.Stop()
			_ = n.wal.Close() // best-effort close on shutdown
			return
		}
	}
}

func (n *Node) processReady(rd raft.Ready) {
	n.stateMu.Lock()
	defer n.stateMu.Unlock()

	n.processReadyLocked(rd)
}

func (n *Node) drainReadyLocked() {
	for {
		select {
		case rd := <-n.node.Ready():
			n.processReadyLocked(rd)
		default:
			return
		}
	}
}

//nolint:cyclop // Ready handling must keep the WAL, storage, transport, apply, and publish order explicit.
func (n *Node) processReadyLocked(rd raft.Ready) {
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
		if err := n.wal.SaveSnapshot(walSnapshotFromReadySnapshot(rd.Snapshot)); err != nil {
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

	if rd.SoftState != nil {
		n.leaderID.Store(rd.SoftState.Lead)
	}

	if len(rd.CommittedEntries) > 0 {
		if err := n.cfg.Apply(rd.CommittedEntries, n.replayCommitIndex); err != nil {
			panic(fmt.Sprintf("raft: apply: %v", err))
		}
		n.applyCommittedConfChangesLocked(rd.CommittedEntries)
		atomic.StoreUint64(&n.appliedIndex, rd.CommittedEntries[len(rd.CommittedEntries)-1].Index)
	}

	n.publishReadStates(rd.ReadStates)

	n.node.Advance()
}

// applyCommittedConfChangesLocked feeds committed membership entries to the
// raft state machine, as the etcd raft contract requires. The application
// apply path skips ConfChange entries, so without this the node's live voter
// tracker never reflects committed membership (e.g. after a restart that
// found no committed conf state in the WAL).
func (n *Node) applyCommittedConfChangesLocked(entries []raftpb.Entry) {
	for _, e := range entries {
		if e.Type != raftpb.EntryConfChange {
			continue
		}
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(e.Data); err != nil {
			continue
		}
		n.node.ApplyConfChange(cc)
	}
}

func walSnapshotFromReadySnapshot(snapshot raftpb.Snapshot) walpb.Snapshot {
	return walpb.Snapshot{
		Index:     snapshot.Metadata.Index,
		Term:      snapshot.Metadata.Term,
		ConfState: &snapshot.Metadata.ConfState,
	}
}

func (n *Node) publishReadStates(states []raft.ReadState) {
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

func (n *Node) Propose(ctx context.Context, data []byte) error {
	return n.node.Propose(ctx, data)
}

func (n *Node) ReadIndex(ctx context.Context) (uint64, error) {
	rctx := n.nextReadContext()
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

// nextReadContext returns a process-unique ReadIndex request key. A
// wall-clock key can collide when two reads land on the same nanosecond,
// overwriting the first waiter's channel and stranding it until its deadline.
func (n *Node) nextReadContext() string {
	return fmt.Sprintf("ri-%d-%d", n.cfg.ID, n.readSeq.Add(1))
}

func (n *Node) Step(ctx context.Context, msg raftpb.Message) error {
	return n.node.Step(ctx, msg)
}

func (n *Node) WithStableLeadership(fn func() error) error {
	n.stateMu.Lock()
	defer n.stateMu.Unlock()

	n.drainReadyLocked()
	n.publishCurrentLeaderLocked()
	return fn()
}

func (n *Node) publishCurrentLeaderLocked() {
	status := n.node.Status()
	n.leaderID.Store(status.SoftState.Lead)
}

func (n *Node) IsLeader() bool {
	return n.leaderID.Load() == n.cfg.ID
}

func (n *Node) LeaderID() uint64 {
	return n.leaderID.Load()
}

func (n *Node) AppliedIndex() uint64 {
	return atomic.LoadUint64(&n.appliedIndex)
}

// CommitIndex returns the highest Raft log index known to be committed. Apply lag
// (commit - applied) is the canonical Raft saturation signal on the USE dashboard.
func (n *Node) CommitIndex() uint64 {
	return atomic.LoadUint64(&n.commitIndex)
}

func deriveConfState(entries []raftpb.Entry, snapshot *raftpb.Snapshot, commit uint64) *raftpb.ConfState {
	if snapshot != nil {
		return new(snapshot.Metadata.ConfState)
	}

	voters := deriveVoters(entries, commit)
	if len(voters) == 0 {
		return nil
	}
	return &raftpb.ConfState{Voters: voters}
}

func deriveVoters(entries []raftpb.Entry, commit uint64) []uint64 {
	var voters []uint64
	for _, e := range entries {
		cc, ok := committedConfChange(e, commit)
		if !ok {
			continue
		}
		voters = applyConfChangeToVoters(voters, cc)
	}
	return voters
}

func committedConfChange(e raftpb.Entry, commit uint64) (raftpb.ConfChange, bool) {
	if commit > 0 && e.Index > commit {
		return raftpb.ConfChange{}, false
	}
	if e.Type != raftpb.EntryConfChange {
		return raftpb.ConfChange{}, false
	}
	var cc raftpb.ConfChange
	if err := cc.Unmarshal(e.Data); err != nil {
		return raftpb.ConfChange{}, false
	}
	return cc, true
}

func applyConfChangeToVoters(voters []uint64, cc raftpb.ConfChange) []uint64 {
	switch cc.Type {
	case raftpb.ConfChangeAddNode:
		return appendUnique(voters, cc.NodeID)
	case raftpb.ConfChangeRemoveNode:
		return removeID(voters, cc.NodeID)
	case raftpb.ConfChangeUpdateNode, raftpb.ConfChangeAddLearnerNode:
		return voters
	default:
		return voters
	}
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

func (n *Node) Stop() {
	select {
	case n.stopc <- struct{}{}:
	case <-n.donec:
		return
	}
	<-n.donec
}
