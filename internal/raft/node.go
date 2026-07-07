package raft

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/fileutil"
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

	// defaultSnapshotCatchUpEntries is how many entries stay in MemoryStorage
	// behind a new snapshot so a slightly lagging follower catches up via
	// normal log replication instead of an install-snapshot.
	defaultSnapshotCatchUpEntries = 5000

	// maxKeptObsoleteFiles bounds each class of obsolete on-disk file
	// (snapshots, broken/db artifacts, released WAL segments) retained after
	// a purge; older files are purged best-effort.
	maxKeptObsoleteFiles = 5
)

// ApplyFunc applies committed entries to the state machine. replayUntil is the
// startup replay watermark: entries with Index <= replayUntil were already applied
// before a restart (re-delivered replay) and must not emit live trace spans. It is
// passed on every call, so the applied path is correct from the very first invocation,
// even though Open starts the run loop before returning the node.
type ApplyFunc func(entries []raftpb.Entry, replayUntil uint64) error

// SnapshotFunc produces the snapshot manifest for the given applied index. The
// index lets the application durably record how far its out-of-log state has
// advanced, so a later Restore can detect a partially-restored DataDir.
type SnapshotFunc func(appliedIndex uint64) (data []byte, err error)

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
	ElectionTick    int
	HeartbeatTick   int
	MaxSizePerMsg   uint64
	MaxInflightMsgs int

	// SnapshotCatchUpEntries is the trailing log window retained in
	// MemoryStorage when the log compacts behind a new snapshot.
	SnapshotCatchUpEntries uint64
}

// walLog is the subset of *wal.WAL the node uses after Open. It is an
// interface so ordering-sensitive persistence paths can be verified against
// a recording fake (the etcd contract: an incoming snapshot must be durable
// before the WAL saves a HardState that references it).
type walLog interface {
	Save(st raftpb.HardState, ents []raftpb.Entry) error
	SaveSnapshot(e walpb.Snapshot) error
	ReleaseLockTo(index uint64) error
	Close() error
}

// snapshotStore is the subset of *snap.Snapshotter the node uses.
type snapshotStore interface {
	SaveSnap(snapshot raftpb.Snapshot) error
	LoadNewestAvailable(walSnaps []walpb.Snapshot) (*raftpb.Snapshot, error)
}

type Node struct {
	cfg       Config
	logger    *slog.Logger
	node      raft.Node
	storage   durableStorage
	wal       walLog
	snap      snapshotStore
	transport Transport

	appliedIndex      uint64
	snapshotIndex     uint64
	replayCommitIndex uint64
	commitIndex       uint64
	confState         raftpb.ConfState

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
	if cfg.SnapshotCatchUpEntries == 0 {
		cfg.SnapshotCatchUpEntries = defaultSnapshotCatchUpEntries
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

	if sink, ok := cfg.Transport.(ReporterSink); ok {
		sink.SetStatusReporter(n)
	}

	// Startup purge: stale artifacts from before the last shutdown (and
	// .snap.broken files the loader just renamed aside) must not wait for
	// the next snapshot creation to be reclaimed (#457).
	n.purgeObsoleteFiles()

	go n.run()
	return n, nil
}

// ReportUnreachable reports to raft that the peer could not be reached.
// Safe to call from any goroutine (raft node methods are channel-based) and
// a no-op after Stop.
func (n *Node) ReportUnreachable(id uint64) {
	n.node.ReportUnreachable(id)
}

// ReportSnapshotFailure reports that a MsgSnap to the peer was dropped or
// failed, so the leader leaves StateSnapshot and retries.
func (n *Node) ReportSnapshotFailure(id uint64) {
	n.node.ReportSnapshot(id, raft.SnapshotFailure)
}

// ReportSnapshotSuccess reports that a MsgSnap was handed to the peer.
func (n *Node) ReportSnapshotSuccess(id uint64) {
	n.node.ReportSnapshot(id, raft.SnapshotFinish)
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
	// Trust only snapshots the WAL has a record for. The snap file is written
	// before the WAL snapshot record, so a crash between the two leaves an
	// orphaned newest .snap; naively loading it (snap.Load) would make
	// wal.Open fail with ErrSnapshotNotFound on every restart (#462).
	walSnaps, err := wal.ValidSnapshotEntries(lg, walDir)
	if err != nil {
		return fmt.Errorf("raft: read WAL snapshot records: %w", err)
	}
	snapshot, err := n.snap.LoadNewestAvailable(walSnaps)
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
		n.confState = snapshot.Metadata.ConfState
	}

	if err := n.storage.SetHardState(hardState); err != nil {
		return fmt.Errorf("raft: set hard state: %w", err)
	}
	if snapshot == nil {
		if cs := n.restartConfState(entries, hardState.Commit); cs != nil {
			n.confState = *cs
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
	CreateSnapshot(index uint64, cs *raftpb.ConfState, data []byte) (raftpb.Snapshot, error)
	Compact(compactIndex uint64) error
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

func (n *Node) processReadyLocked(rd raft.Ready) {
	// An incoming install-snapshot must be durable BEFORE the WAL saves a
	// HardState whose Commit references it (etcd contract). A crash between
	// wal.Save and the snapshot persist would otherwise leave the WAL
	// pointing at a snapshot that never hit disk — an unbootable member
	// (#462).
	if !raft.IsEmptySnap(rd.Snapshot) {
		n.persistIncomingSnapshotLocked(rd.Snapshot)
	}

	if err := n.wal.Save(rd.HardState, rd.Entries); err != nil {
		panic(fmt.Sprintf("raft: WAL save: %v", err))
	}

	if !raft.IsEmptyHardState(rd.HardState) {
		atomic.StoreUint64(&n.commitIndex, rd.HardState.Commit)
	}

	if !raft.IsEmptySnap(rd.Snapshot) {
		n.adoptIncomingSnapshotLocked(rd.Snapshot)
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

	n.maybeTriggerSnapshotLocked()

	n.node.Advance()
}

// persistIncomingSnapshotLocked validates and durably persists an
// install-snapshot delivered by the leader to a follower whose log fell
// behind the compaction window. The application Restore hook runs FIRST: it
// decides whether local state can satisfy the snapshot, and a rejection is
// fatal (continuing would silently diverge) — but because nothing has been
// persisted yet, the member restarts at its previous state instead of
// crash-looping on a durably adopted foreign snapshot (#462). Restore may
// therefore be invoked again for the same snapshot after a crash or retry;
// it must be idempotent. Persist order after validation follows the etcd
// contract: snap file first, then the WAL snapshot record, then release WAL
// locks.
func (n *Node) persistIncomingSnapshotLocked(snapshot raftpb.Snapshot) {
	if n.cfg.Restore != nil {
		if err := n.cfg.Restore(snapshot.Data); err != nil {
			panic(fmt.Sprintf("raft: restore snapshot at index %d: %v", snapshot.Metadata.Index, err))
		}
	}
	if err := n.snap.SaveSnap(snapshot); err != nil {
		panic(fmt.Sprintf("raft: save snapshot: %v", err))
	}
	if err := n.wal.SaveSnapshot(walSnapshotFromReadySnapshot(snapshot)); err != nil {
		panic(fmt.Sprintf("raft: WAL save snapshot: %v", err))
	}
	if err := n.wal.ReleaseLockTo(snapshot.Metadata.Index); err != nil {
		panic(fmt.Sprintf("raft: WAL release to snapshot: %v", err))
	}
}

// adoptIncomingSnapshotLocked switches the in-memory raft state over to a
// persisted install-snapshot. Runs after the WAL saved the Ready's HardState.
func (n *Node) adoptIncomingSnapshotLocked(snapshot raftpb.Snapshot) {
	if err := n.storage.ApplySnapshot(snapshot); err != nil {
		panic(fmt.Sprintf("raft: storage apply snapshot: %v", err))
	}
	n.confState = snapshot.Metadata.ConfState
	n.snapshotIndex = snapshot.Metadata.Index
	atomic.StoreUint64(&n.appliedIndex, snapshot.Metadata.Index)
	if commit := atomic.LoadUint64(&n.commitIndex); commit < snapshot.Metadata.Index {
		atomic.StoreUint64(&n.commitIndex, snapshot.Metadata.Index)
	}
}

// maybeTriggerSnapshotLocked snapshots the state machine and compacts the
// in-memory log once MaxSnapCount entries applied since the last snapshot.
// The application state is durably applied outside the Raft log, so the
// snapshot data is whatever cfg.Snapshot returns (a manifest); failing to
// produce it only defers the snapshot, but failing to persist one is fatal
// like any other WAL/snapshot durability failure.
func (n *Node) maybeTriggerSnapshotLocked() {
	if n.cfg.Snapshot == nil {
		return
	}
	applied := atomic.LoadUint64(&n.appliedIndex)
	if applied <= n.snapshotIndex || applied-n.snapshotIndex < n.cfg.MaxSnapCount {
		return
	}

	data, err := n.cfg.Snapshot(applied)
	if err != nil {
		n.logger.Error("raft: snapshot data unavailable, deferring snapshot", "error", err)
		return
	}
	var cs *raftpb.ConfState
	if len(n.confState.Voters) > 0 {
		cs = &n.confState
	}
	snapshot, err := n.storage.CreateSnapshot(applied, cs, data)
	if err != nil {
		n.logger.Error("raft: create snapshot", "applied_index", applied, "error", err)
		return
	}
	n.commitSnapshotLocked(snapshot, applied)
}

func (n *Node) commitSnapshotLocked(snapshot raftpb.Snapshot, applied uint64) {
	if err := n.persistSnapshotLocked(snapshot); err != nil {
		panic(fmt.Sprintf("raft: persist snapshot: %v", err))
	}

	compactIndex := uint64(1)
	if applied > n.cfg.SnapshotCatchUpEntries {
		compactIndex = applied - n.cfg.SnapshotCatchUpEntries
	}
	if err := n.storage.Compact(compactIndex); err != nil && !errors.Is(err, raft.ErrCompacted) {
		panic(fmt.Sprintf("raft: compact log to %d: %v", compactIndex, err))
	}
	n.snapshotIndex = applied
	n.logger.Info("raft: snapshot created",
		"snapshot_index", applied,
		"compact_index", compactIndex,
	)
	n.purgeObsoleteFiles()
}

// persistSnapshotLocked follows the etcd ordering: snapshot file first so a
// WAL snapshot record never points at a missing file, then the WAL record,
// then release WAL locks up to the snapshot index so old segments can purge.
func (n *Node) persistSnapshotLocked(snapshot raftpb.Snapshot) error {
	if err := n.snap.SaveSnap(snapshot); err != nil {
		return fmt.Errorf("save snapshot file: %w", err)
	}
	if err := n.wal.SaveSnapshot(walSnapshotFromReadySnapshot(snapshot)); err != nil {
		return fmt.Errorf("save WAL snapshot record: %w", err)
	}
	if err := n.wal.ReleaseLockTo(snapshot.Metadata.Index); err != nil {
		return fmt.Errorf("release WAL locks: %w", err)
	}
	return nil
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
		if cs := n.node.ApplyConfChange(cc); cs != nil {
			n.confState = *cs
		}
	}
}

// purgeObsoleteFiles removes snapshot and WAL files made obsolete by a new
// snapshot, keeping the newest few of each. WAL segments are only removed
// when their file lock is free, which is exactly the set ReleaseLockTo
// released. Purging is best-effort: failures only delay reclamation.
// It runs at startup and after each snapshot creation: .snap.broken files
// are produced by the snapshot loader at restart (a corrupt .snap is renamed
// aside), so a snapshot-creation-only purge would let them accumulate across
// restarts (#457).
func (n *Node) purgeObsoleteFiles() {
	snapDir := n.cfg.DataDir + "/snap"
	n.purgeOldestFiles(snapDir, ".snap", nil)
	// Neither .snap.broken (corrupt snapshots renamed aside by the loader)
	// nor .snap.db (etcd snapshotter payload files this node never writes)
	// are ever re-read; bound their accumulation with the same policy.
	n.purgeOldestFiles(snapDir, ".snap.broken", nil)
	n.purgeOldestFiles(snapDir, ".snap.db", nil)
	n.purgeOldestFiles(n.cfg.DataDir+"/wal", ".wal", func(path string) bool {
		lock, err := fileutil.TryLockFile(path, os.O_WRONLY, fileutil.PrivateFileMode)
		if err != nil {
			return false
		}
		_ = lock.Close()
		return true
	})
}

func (n *Node) purgeOldestFiles(dir, suffix string, removable func(string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		n.logger.Warn("raft: purge read dir", "dir", dir, "error", err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			names = append(names, entry.Name())
		}
	}
	// File names embed zero-padded hex sequence/index pairs, so lexical order
	// is creation order.
	slices.Sort(names)
	for _, name := range names[:max(0, len(names)-maxKeptObsoleteFiles)] {
		path := dir + "/" + name
		if removable != nil && !removable(path) {
			// Still locked by the live WAL: this and everything newer is needed.
			return
		}
		if err := os.Remove(path); err != nil {
			n.logger.Warn("raft: purge file", "path", path, "error", err)
			return
		}
		n.logger.Info("raft: purged obsolete file", "path", path)
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
