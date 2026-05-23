package raftmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

func TestRaftFaultHarnessTransportRestartAndReadIndex(t *testing.T) {
	h := newRaftFaultHarness(t, 1, 2, 3)

	h.proposeDocument(t, harnessDocument{DocumentID: "invoice.xml", BlockID: "block-1"})
	h.waitAppliedOn(t, 2, 1)
	h.restartNode(t, 2)
	h.proposeDocument(t, harnessDocument{DocumentID: "after-restart.xml", BlockID: "block-2"})
	h.waitAppliedOn(t, 2, 2)

	leader := h.leaderID()
	h.delayBetween(leader, 2)
	h.proposeDocument(t, harnessDocument{DocumentID: "reordered.xml", BlockID: "block-3"})
	h.flushDelayedReverse(t)
	h.waitAppliedOn(t, 2, 3)

	if err := h.readIndex(); err != nil {
		t.Fatalf("read index with quorum: %v", err)
	}
	h.isolate(leader)
	h.dropBetween(2, 3)
	h.tickN(20)
	if err := h.readIndex(); err == nil {
		t.Fatal("read index succeeded while leader was isolated from quorum")
	}
	h.heal(leader)
	h.allowBetween(2, 3)
	h.waitAnyLeader(t)
	if err := h.readIndex(); err != nil {
		t.Fatalf("read index after healed leader change: %v", err)
	}
}

func TestRaftFaultHarnessJointConsensusMembershipChange(t *testing.T) {
	h := newRaftFaultHarness(t, 1, 2, 3)

	h.enterJointReplacement(t, 3, 4)
	joint := h.confState(1)
	if !containsUint64(joint.VotersOutgoing, 3) || !containsUint64(joint.Voters, 4) {
		t.Fatalf("joint conf state = %#v, want outgoing voter 3 and incoming voter 4", joint)
	}
	h.leaveJoint(t)
	final := h.confState(1)
	if len(final.VotersOutgoing) != 0 || containsUint64(final.Voters, 3) || !containsUint64(final.Voters, 4) {
		t.Fatalf("final conf state = %#v, want voter set 1,2,4 with no outgoing voters", final)
	}
}

func TestRaftFaultHarnessRechecksByteEligibilityAfterSnapshotInstall(t *testing.T) {
	h := newRaftFaultHarness(t, 1, 2, 3)
	h.isolate(3)
	h.proposeDocument(t, harnessDocument{DocumentID: "snapshotted.xml", BlockID: "block-snapshot"})
	if h.canServe(3, "snapshotted.xml") {
		t.Fatal("isolated node served bytes before receiving metadata")
	}

	h.installSnapshot(t, h.leaderID(), 3)
	if !h.hasDocument(3, "snapshotted.xml") {
		t.Fatal("snapshot install did not rebuild metadata projection on target")
	}
	if h.canServe(3, "snapshotted.xml") {
		t.Fatal("snapshot install served bytes before local byte refs were verified")
	}
	h.addByteRef(3, "block-snapshot")
	h.recheckByteEligibility(3)
	if !h.canServe(3, "snapshotted.xml") {
		t.Fatal("verified local byte ref did not make snapshotted document byte-serving eligible")
	}
}

type raftFaultHarness struct {
	t       *testing.T
	ids     []uint64
	nodes   map[uint64]*raftHarnessNode
	drops   map[raftHarnessPair]bool
	delays  map[raftHarnessPair]bool
	delayed []pb.Message
}

type raftHarnessNode struct {
	id           uint64
	raw          *raft.RawNode
	storage      *raft.MemoryStorage
	appliedIndex uint64
	confState    pb.ConfState
	documents    map[string]harnessDocument
	byteRefs     map[string]bool
	eligible     map[string]bool
}

type raftHarnessPair struct {
	from uint64
	to   uint64
}

type harnessDocument struct {
	DocumentID string
	BlockID    string
}

type harnessSnapshot struct {
	Documents []harnessDocument `json:"documents"`
}

func newRaftFaultHarness(t *testing.T, ids ...uint64) *raftFaultHarness {
	t.Helper()
	peers := make([]raft.Peer, 0, len(ids))
	for _, id := range ids {
		peers = append(peers, raft.Peer{ID: id})
	}
	h := &raftFaultHarness{
		t:      t,
		ids:    append([]uint64(nil), ids...),
		nodes:  make(map[uint64]*raftHarnessNode),
		drops:  make(map[raftHarnessPair]bool),
		delays: make(map[raftHarnessPair]bool),
	}
	for _, id := range ids {
		h.nodes[id] = h.newNode(t, id)
		if err := h.nodes[id].raw.Bootstrap(peers); err != nil {
			t.Fatalf("bootstrap node %d: %v", id, err)
		}
	}
	h.pumpUntilIdle(t)
	if err := h.nodes[ids[0]].raw.Campaign(); err != nil {
		t.Fatalf("campaign node %d: %v", ids[0], err)
	}
	h.waitLeader(t, ids[0])
	return h
}

func (h *raftFaultHarness) newNode(t *testing.T, id uint64) *raftHarnessNode {
	t.Helper()
	storage := raft.NewMemoryStorage()
	raw, err := raft.NewRawNode(&raft.Config{
		ID:                        id,
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   storage,
		MaxSizePerMsg:             1024 * 1024,
		MaxInflightMsgs:           16,
		MaxUncommittedEntriesSize: 64 * 1024 * 1024,
		CheckQuorum:               true,
		ReadOnlyOption:            raft.ReadOnlySafe,
		Logger:                    raftNopLogger{},
	})
	if err != nil {
		t.Fatalf("new raw node %d: %v", id, err)
	}
	return &raftHarnessNode{
		id:        id,
		raw:       raw,
		storage:   storage,
		documents: make(map[string]harnessDocument),
		byteRefs:  make(map[string]bool),
		eligible:  make(map[string]bool),
	}
}

func (h *raftFaultHarness) proposeDocument(t *testing.T, document harnessDocument) {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document command: %v", err)
	}
	leader := h.leaderID()
	if leader == 0 {
		t.Fatal("no raft leader")
	}
	if err := h.nodes[leader].raw.Propose(data); err != nil {
		t.Fatalf("propose document: %v", err)
	}
	h.pumpUntil(t, func() bool {
		applied := 0
		for _, node := range h.nodes {
			if _, ok := node.documents[document.DocumentID]; ok {
				applied++
			}
		}
		return applied >= h.quorum()
	})
}

func (h *raftFaultHarness) readIndex() error {
	leader := h.leaderID()
	if leader == 0 {
		return errors.New("raft harness has no current leader")
	}
	const ctx = "fresh-read"
	h.nodes[leader].raw.ReadIndex([]byte(ctx))
	for step := 0; step < 1000; step++ {
		if h.pump(func(state raft.ReadState) bool {
			return string(state.RequestCtx) == ctx
		}) {
			return nil
		}
		h.tick()
	}
	return errors.New("raft harness read index was not confirmed")
}

func (h *raftFaultHarness) enterJointReplacement(t *testing.T, remove, add uint64) {
	t.Helper()
	change := pb.ConfChangeV2{
		Transition: pb.ConfChangeTransitionJointExplicit,
		Changes: []pb.ConfChangeSingle{
			{Type: pb.ConfChangeRemoveNode, NodeID: remove},
			{Type: pb.ConfChangeAddNode, NodeID: add},
		},
	}
	h.proposeConfChange(t, change, func(state pb.ConfState) bool {
		return containsUint64(state.VotersOutgoing, remove) && containsUint64(state.Voters, add)
	})
}

func (h *raftFaultHarness) leaveJoint(t *testing.T) {
	t.Helper()
	h.proposeConfChange(t, pb.ConfChangeV2{}, func(state pb.ConfState) bool {
		return len(state.VotersOutgoing) == 0
	})
}

func (h *raftFaultHarness) proposeConfChange(t *testing.T, change pb.ConfChangeV2, done func(pb.ConfState) bool) {
	t.Helper()
	leader := h.leaderID()
	if leader == 0 {
		t.Fatal("no raft leader")
	}
	if err := h.nodes[leader].raw.ProposeConfChange(change); err != nil {
		t.Fatalf("propose conf change: %v", err)
	}
	h.pumpUntil(t, func() bool {
		return done(h.nodes[leader].confState)
	})
}

func (h *raftFaultHarness) installSnapshot(t *testing.T, sourceID, targetID uint64) {
	t.Helper()
	source := h.nodes[sourceID]
	target := h.nodes[targetID]
	if source == nil || target == nil {
		t.Fatalf("snapshot install source=%d target=%d must exist", sourceID, targetID)
	}
	payload, err := json.Marshal(harnessSnapshot{Documents: sortedHarnessDocuments(source.documents)})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	snapshot, err := source.storage.CreateSnapshot(source.appliedIndex, &source.confState, payload)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if err := target.storage.ApplySnapshot(snapshot); errors.Is(err, raft.ErrSnapOutOfDate) {
		return
	} else if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	target.applySnapshot(t, snapshot)
}

func (h *raftFaultHarness) restartNode(t *testing.T, id uint64) {
	t.Helper()
	node := h.nodes[id]
	if node == nil {
		t.Fatalf("restart missing node %d", id)
	}
	raw, err := raft.NewRawNode(&raft.Config{
		ID:                        id,
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   node.storage,
		MaxSizePerMsg:             1024 * 1024,
		MaxInflightMsgs:           16,
		MaxUncommittedEntriesSize: 64 * 1024 * 1024,
		CheckQuorum:               true,
		ReadOnlyOption:            raft.ReadOnlySafe,
		Logger:                    raftNopLogger{},
	})
	if err != nil {
		t.Fatalf("restart raw node %d: %v", id, err)
	}
	node.raw = raw
	h.pumpUntilIdle(t)
}

func (h *raftFaultHarness) isolate(id uint64) {
	for _, peer := range h.ids {
		if peer == id {
			continue
		}
		h.dropBetween(id, peer)
	}
}

func (h *raftFaultHarness) heal(id uint64) {
	for _, peer := range h.ids {
		if peer == id {
			continue
		}
		h.allowBetween(id, peer)
	}
}

func (h *raftFaultHarness) dropBetween(a, b uint64) {
	h.drops[raftHarnessPair{from: a, to: b}] = true
	h.drops[raftHarnessPair{from: b, to: a}] = true
}

func (h *raftFaultHarness) allowBetween(a, b uint64) {
	delete(h.drops, raftHarnessPair{from: a, to: b})
	delete(h.drops, raftHarnessPair{from: b, to: a})
}

func (h *raftFaultHarness) delayBetween(a, b uint64) {
	h.delays[raftHarnessPair{from: a, to: b}] = true
	h.delays[raftHarnessPair{from: b, to: a}] = true
}

func (h *raftFaultHarness) flushDelayedReverse(t *testing.T) {
	t.Helper()
	for pair := range h.delays {
		delete(h.delays, pair)
	}
	for i := len(h.delayed) - 1; i >= 0; i-- {
		h.deliver(h.delayed[i])
	}
	h.delayed = nil
	h.pumpUntilIdle(t)
}

func (h *raftFaultHarness) waitLeader(t *testing.T, id uint64) {
	t.Helper()
	h.pumpUntil(t, func() bool {
		return h.leaderID() == id
	})
}

func (h *raftFaultHarness) waitAnyLeader(t *testing.T) {
	t.Helper()
	h.pumpUntil(t, func() bool {
		return h.leaderID() != 0
	})
}

func (h *raftFaultHarness) waitAppliedOn(t *testing.T, id uint64, count int) {
	t.Helper()
	h.pumpUntil(t, func() bool {
		return len(h.nodes[id].documents) >= count
	})
}

func (h *raftFaultHarness) pumpUntilIdle(t *testing.T) {
	t.Helper()
	for step := 0; step < 1000; step++ {
		if !h.pump(nil) {
			return
		}
	}
	t.Fatal("raft harness did not become idle")
}

func (h *raftFaultHarness) pumpUntil(t *testing.T, done func() bool) {
	t.Helper()
	for step := 0; step < 10000; step++ {
		if done() {
			return
		}
		if !h.pump(nil) {
			h.tick()
		}
	}
	t.Fatal("raft harness condition was not reached")
}

func (h *raftFaultHarness) pump(readDone func(raft.ReadState) bool) bool {
	progressed := false
	for _, id := range h.ids {
		node := h.nodes[id]
		if node == nil {
			continue
		}
		for node.raw.HasReady() {
			ready := node.raw.Ready()
			readMatched := h.processReady(node, ready, readDone)
			node.raw.Advance(ready)
			if readMatched {
				return true
			}
			progressed = true
		}
	}
	return progressed
}

func (h *raftFaultHarness) processReady(node *raftHarnessNode, ready raft.Ready, readDone func(raft.ReadState) bool) bool {
	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := node.storage.ApplySnapshot(ready.Snapshot); err != nil {
			h.t.Fatalf("apply ready snapshot on %d: %v", node.id, err)
		}
		node.applySnapshot(h.t, ready.Snapshot)
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		if err := node.storage.SetHardState(ready.HardState); err != nil {
			h.t.Fatalf("set hard state on %d: %v", node.id, err)
		}
	}
	if err := node.storage.Append(ready.Entries); err != nil {
		h.t.Fatalf("append entries on %d: %v", node.id, err)
	}
	for _, entry := range ready.CommittedEntries {
		node.appliedIndex = entry.Index
		switch entry.Type {
		case pb.EntryNormal:
			node.applyDocument(h.t, entry)
		case pb.EntryConfChangeV2:
			var change pb.ConfChangeV2
			if err := change.Unmarshal(entry.Data); err != nil {
				h.t.Fatalf("unmarshal conf change v2: %v", err)
			}
			node.confState = *node.raw.ApplyConfChange(change)
		case pb.EntryConfChange:
			var change pb.ConfChange
			if err := change.Unmarshal(entry.Data); err != nil {
				h.t.Fatalf("unmarshal conf change: %v", err)
			}
			node.confState = *node.raw.ApplyConfChange(change)
		}
	}
	for _, message := range ready.Messages {
		h.deliver(message)
	}
	readMatched := false
	for _, readState := range ready.ReadStates {
		if readDone != nil && readDone(readState) {
			readMatched = true
		}
	}
	return readMatched
}

func (h *raftFaultHarness) deliver(message pb.Message) {
	if message.To == 0 {
		return
	}
	pair := raftHarnessPair{from: message.From, to: message.To}
	if h.drops[pair] {
		return
	}
	if h.delays[pair] {
		h.delayed = append(h.delayed, message)
		return
	}
	node := h.nodes[message.To]
	if node == nil {
		return
	}
	if err := node.raw.Step(message); err != nil {
		if errors.Is(err, raft.ErrStepLocalMsg) || errors.Is(err, raft.ErrStepPeerNotFound) {
			return
		}
		h.t.Fatalf("step message %s from %d to %d: %v", message.Type, message.From, message.To, err)
	}
}

func (h *raftFaultHarness) tick() {
	for _, node := range h.nodes {
		node.raw.Tick()
	}
}

func (h *raftFaultHarness) tickN(count int) {
	for i := 0; i < count; i++ {
		h.tick()
		h.pump(nil)
	}
}

func (h *raftFaultHarness) leaderID() uint64 {
	var leader uint64
	var term uint64
	for _, id := range h.ids {
		node := h.nodes[id]
		if node == nil {
			continue
		}
		status := node.raw.Status()
		if status.RaftState != raft.StateLeader {
			continue
		}
		if status.Term >= term {
			leader = id
			term = status.Term
		}
	}
	return leader
}

func (h *raftFaultHarness) quorum() int {
	return len(h.ids)/2 + 1
}

func (h *raftFaultHarness) confState(id uint64) pb.ConfState {
	return h.nodes[id].confState
}

func (h *raftFaultHarness) hasDocument(nodeID uint64, documentID string) bool {
	_, ok := h.nodes[nodeID].documents[documentID]
	return ok
}

func (h *raftFaultHarness) addByteRef(nodeID uint64, blockID string) {
	h.nodes[nodeID].byteRefs[blockID] = true
}

func (h *raftFaultHarness) canServe(nodeID uint64, documentID string) bool {
	return h.nodes[nodeID].eligible[documentID]
}

func (h *raftFaultHarness) recheckByteEligibility(nodeID uint64) {
	h.nodes[nodeID].recheckByteEligibility()
}

func (n *raftHarnessNode) applyDocument(t *testing.T, entry pb.Entry) {
	t.Helper()
	if len(entry.Data) == 0 {
		return
	}
	var document harnessDocument
	if err := json.Unmarshal(entry.Data, &document); err != nil {
		t.Fatalf("unmarshal document command: %v", err)
	}
	n.documents[document.DocumentID] = document
	n.recheckByteEligibility()
}

func (n *raftHarnessNode) applySnapshot(t *testing.T, snapshot pb.Snapshot) {
	t.Helper()
	var payload harnessSnapshot
	if err := json.Unmarshal(snapshot.Data, &payload); err != nil {
		t.Fatalf("unmarshal snapshot payload: %v", err)
	}
	n.documents = make(map[string]harnessDocument, len(payload.Documents))
	n.eligible = make(map[string]bool, len(payload.Documents))
	for _, document := range payload.Documents {
		n.documents[document.DocumentID] = document
	}
	n.confState = snapshot.Metadata.ConfState
	n.appliedIndex = snapshot.Metadata.Index
	n.recheckByteEligibility()
}

func (n *raftHarnessNode) recheckByteEligibility() {
	for documentID, document := range n.documents {
		n.eligible[documentID] = n.byteRefs[document.BlockID]
	}
}

func sortedHarnessDocuments(documents map[string]harnessDocument) []harnessDocument {
	out := make([]harnessDocument, 0, len(documents))
	for _, document := range documents {
		out = append(out, document)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DocumentID < out[j].DocumentID
	})
	return out
}

func containsUint64(values []uint64, value uint64) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type raftNopLogger struct{}

func (raftNopLogger) Debug(...any)              {}
func (raftNopLogger) Debugf(string, ...any)     {}
func (raftNopLogger) Error(...any)              {}
func (raftNopLogger) Errorf(string, ...any)     {}
func (raftNopLogger) Info(...any)               {}
func (raftNopLogger) Infof(string, ...any)      {}
func (raftNopLogger) Warning(...any)            {}
func (raftNopLogger) Warningf(string, ...any)   {}
func (raftNopLogger) Fatal(v ...any)            { panic(fmt.Sprint(v...)) }
func (raftNopLogger) Fatalf(f string, v ...any) { panic(fmt.Sprintf(f, v...)) }
func (raftNopLogger) Panic(v ...any)            { panic(fmt.Sprint(v...)) }
func (raftNopLogger) Panicf(f string, v ...any) { panic(fmt.Sprintf(f, v...)) }
