package writepath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

var errRaftClusterClosed = errors.New("raft cluster commit barrier is closed")

type RaftClusterCommitBarrier struct {
	requests  chan any
	done      chan struct{}
	closeOnce sync.Once
}

type raftClusterCommitRequest struct {
	record   DocumentRecord
	accepted chan uint64
	result   chan error
}

type raftClusterPendingProposal struct {
	command      []byte
	lastLeaderID uint64
	lastTerm     uint64
}

type raftClusterCancelRequest struct {
	id uint64
}

type raftClusterReadCancelRequest struct {
	id uint64
}

type raftClusterReadIndexRequest struct {
	accepted chan uint64
	result   chan error
}

type raftClusterControlRequest struct {
	run    func(*raftCluster) error
	result chan error
}

type raftClusterQueryRequest struct {
	read   func(*raftCluster) any
	result chan raftClusterQueryResult
}

type raftClusterQueryResult struct {
	value any
	err   error
}

type raftClusterCloseRequest struct {
	result chan struct{}
}

func NewRaftClusterCommitBarrier() (*RaftClusterCommitBarrier, error) {
	cluster, err := newRaftCluster([]uint64{1, 2, 3})
	if err != nil {
		return nil, err
	}

	barrier := &RaftClusterCommitBarrier{
		requests: make(chan any),
		done:     make(chan struct{}),
	}
	go barrier.run(cluster)
	return barrier, nil
}

func (b *RaftClusterCommitBarrier) CommitDocument(ctx context.Context, record DocumentRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	commitCtx, cancel := context.WithTimeout(ctx, raftCommitTimeout)
	defer cancel()

	accepted := make(chan uint64, 1)
	result := make(chan error, 1)
	request := raftClusterCommitRequest{
		record:   record,
		accepted: accepted,
		result:   result,
	}

	select {
	case b.requests <- request:
	case <-b.done:
		return errRaftClusterClosed
	case <-commitCtx.Done():
		return commitCtx.Err()
	}

	var id uint64
	select {
	case id = <-accepted:
	case err := <-result:
		return err
	case <-b.done:
		return errRaftClusterClosed
	case <-commitCtx.Done():
		return commitCtx.Err()
	}

	select {
	case err := <-result:
		return err
	case <-b.done:
		return errRaftClusterClosed
	case <-commitCtx.Done():
		if id != 0 {
			b.tryCancel(id)
		}
		return commitCtx.Err()
	}
}

func (b *RaftClusterCommitBarrier) ReadFresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	readCtx, cancel := context.WithTimeout(ctx, raftCommitTimeout)
	defer cancel()

	accepted := make(chan uint64, 1)
	result := make(chan error, 1)
	request := raftClusterReadIndexRequest{
		accepted: accepted,
		result:   result,
	}

	select {
	case b.requests <- request:
	case <-b.done:
		return errRaftClusterClosed
	case <-readCtx.Done():
		return readCtx.Err()
	}

	var id uint64
	select {
	case id = <-accepted:
	case err := <-result:
		return err
	case <-b.done:
		return errRaftClusterClosed
	case <-readCtx.Done():
		return readCtx.Err()
	}

	select {
	case err := <-result:
		return err
	case <-b.done:
		return errRaftClusterClosed
	case <-readCtx.Done():
		if id != 0 {
			b.tryCancelRead(id)
		}
		return readCtx.Err()
	}
}

func (b *RaftClusterCommitBarrier) Close() error {
	var err error
	b.closeOnce.Do(func() {
		result := make(chan struct{})
		select {
		case b.requests <- raftClusterCloseRequest{result: result}:
			<-result
		case <-b.done:
		}
		<-b.done
	})
	return err
}

func (b *RaftClusterCommitBarrier) LeaderID() uint64 {
	value, err := b.query(func(cluster *raftCluster) any {
		return cluster.leaderID()
	})
	if err != nil {
		return 0
	}
	leaderID, _ := value.(uint64)
	return leaderID
}

func (b *RaftClusterCommitBarrier) AppliedCount() int {
	value, err := b.query(func(cluster *raftCluster) any {
		return len(cluster.committed)
	})
	if err != nil {
		return 0
	}
	applied, _ := value.(int)
	return applied
}

func (b *RaftClusterCommitBarrier) AppliedCountOn(nodeID uint64) int {
	value, err := b.query(func(cluster *raftCluster) any {
		return cluster.appliedOn[nodeID]
	})
	if err != nil {
		return 0
	}
	applied, _ := value.(int)
	return applied
}

func (b *RaftClusterCommitBarrier) CommittedDocument(tenantID, transactionID, documentName string) (DocumentRecord, bool) {
	value, err := b.query(func(cluster *raftCluster) any {
		record, ok := cluster.records[committedDocumentKey(tenantID, transactionID, documentName)]
		return committedDocumentQueryResult{
			record: record,
			ok:     ok,
		}
	})
	if err != nil {
		return DocumentRecord{}, false
	}
	result, _ := value.(committedDocumentQueryResult)
	return result.record, result.ok
}

func (b *RaftClusterCommitBarrier) IsolateNode(nodeID uint64) error {
	return b.control(func(cluster *raftCluster) error {
		if !cluster.hasNode(nodeID) {
			return fmt.Errorf("raft node %d does not exist", nodeID)
		}
		cluster.isolated[nodeID] = true
		return nil
	})
}

func (b *RaftClusterCommitBarrier) HealNode(nodeID uint64) error {
	return b.control(func(cluster *raftCluster) error {
		if !cluster.hasNode(nodeID) {
			return fmt.Errorf("raft node %d does not exist", nodeID)
		}
		delete(cluster.isolated, nodeID)
		return nil
	})
}

func (b *RaftClusterCommitBarrier) DropMessagesBetween(a, c uint64) error {
	return b.control(func(cluster *raftCluster) error {
		if !cluster.hasNode(a) || !cluster.hasNode(c) {
			return fmt.Errorf("raft message drop references unknown nodes %d and %d", a, c)
		}
		cluster.drops[raftMessagePair{from: a, to: c}] = true
		cluster.drops[raftMessagePair{from: c, to: a}] = true
		return nil
	})
}

func (b *RaftClusterCommitBarrier) AllowMessagesBetween(a, c uint64) error {
	return b.control(func(cluster *raftCluster) error {
		if !cluster.hasNode(a) || !cluster.hasNode(c) {
			return fmt.Errorf("raft message allow references unknown nodes %d and %d", a, c)
		}
		delete(cluster.drops, raftMessagePair{from: a, to: c})
		delete(cluster.drops, raftMessagePair{from: c, to: a})
		return nil
	})
}

func (b *RaftClusterCommitBarrier) CampaignNode(nodeID uint64) error {
	return b.control(func(cluster *raftCluster) error {
		if err := cluster.campaign(nodeID); err != nil {
			return err
		}
		return cluster.pumpUntil(func() bool {
			return cluster.leaderID() == nodeID
		})
	})
}

func (b *RaftClusterCommitBarrier) WaitForAppliedOn(nodeID uint64, want int) error {
	deadline := time.Now().Add(raftCommitTimeout)
	for time.Now().Before(deadline) {
		if b.AppliedCountOn(nodeID) >= want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("raft node %d applied %d commands, want at least %d", nodeID, b.AppliedCountOn(nodeID), want)
}

func (b *RaftClusterCommitBarrier) control(run func(*raftCluster) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), raftCommitTimeout)
	defer cancel()

	result := make(chan error, 1)
	request := raftClusterControlRequest{
		run:    run,
		result: result,
	}
	select {
	case b.requests <- request:
	case <-b.done:
		return errRaftClusterClosed
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-result:
		return err
	case <-b.done:
		return errRaftClusterClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *RaftClusterCommitBarrier) query(read func(*raftCluster) any) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), raftCommitTimeout)
	defer cancel()

	result := make(chan raftClusterQueryResult, 1)
	request := raftClusterQueryRequest{
		read:   read,
		result: result,
	}
	select {
	case b.requests <- request:
	case <-b.done:
		return nil, errRaftClusterClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case response := <-result:
		return response.value, response.err
	case <-b.done:
		return nil, errRaftClusterClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *RaftClusterCommitBarrier) tryCancel(id uint64) {
	select {
	case b.requests <- raftClusterCancelRequest{id: id}:
	case <-b.done:
	default:
	}
}

func (b *RaftClusterCommitBarrier) tryCancelRead(id uint64) {
	select {
	case b.requests <- raftClusterReadCancelRequest{id: id}:
	case <-b.done:
	default:
	}
}

func (b *RaftClusterCommitBarrier) run(cluster *raftCluster) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	defer close(b.done)

	for {
		cluster.proposePending()
		if cluster.pump() {
			continue
		}

		select {
		case request := <-b.requests:
			switch request := request.(type) {
			case raftClusterCommitRequest:
				cluster.handleCommit(request)
			case raftClusterCancelRequest:
				cluster.cancel(request.id)
			case raftClusterReadIndexRequest:
				cluster.handleReadIndex(request)
			case raftClusterReadCancelRequest:
				cluster.cancelRead(request.id)
			case raftClusterControlRequest:
				request.result <- request.run(cluster)
			case raftClusterQueryRequest:
				value := request.read(cluster)
				request.result <- raftClusterQueryResult{value: value}
			case raftClusterCloseRequest:
				cluster.closeWithError(errRaftClusterClosed)
				close(request.result)
				return
			}
		case <-ticker.C:
			cluster.tick()
		}
	}
}

type raftCluster struct {
	ids         []uint64
	nodes       map[uint64]*raftClusterNode
	isolated    map[uint64]bool
	drops       map[raftMessagePair]bool
	nextID      uint64
	nextReadID  uint64
	waiters     map[uint64]chan error
	readWaiters map[uint64]chan error
	appliedBy   map[uint64]map[uint64]struct{}
	appliedOn   map[uint64]int
	committed   map[uint64]struct{}
	records     map[string]DocumentRecord
	pending     map[uint64]raftClusterPendingProposal
	err         error
}

type raftClusterNode struct {
	id      uint64
	raw     *raft.RawNode
	storage *raft.MemoryStorage
}

type raftMessagePair struct {
	from uint64
	to   uint64
}

type committedDocumentQueryResult struct {
	record DocumentRecord
	ok     bool
}

func newRaftCluster(ids []uint64) (*raftCluster, error) {
	peers := make([]raft.Peer, 0, len(ids))
	for _, id := range ids {
		peers = append(peers, raft.Peer{ID: id})
	}

	cluster := &raftCluster{
		ids:         append([]uint64(nil), ids...),
		nodes:       make(map[uint64]*raftClusterNode),
		isolated:    make(map[uint64]bool),
		drops:       make(map[raftMessagePair]bool),
		waiters:     make(map[uint64]chan error),
		readWaiters: make(map[uint64]chan error),
		appliedBy:   make(map[uint64]map[uint64]struct{}),
		appliedOn:   make(map[uint64]int),
		committed:   make(map[uint64]struct{}),
		records:     make(map[string]DocumentRecord),
		pending:     make(map[uint64]raftClusterPendingProposal),
	}

	for _, id := range ids {
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
			Logger:                    raftNopLogger{},
		})
		if err != nil {
			return nil, err
		}
		if err := raw.Bootstrap(peers); err != nil {
			return nil, err
		}
		cluster.nodes[id] = &raftClusterNode{
			id:      id,
			raw:     raw,
			storage: storage,
		}
	}

	if err := cluster.pumpUntilIdle(); err != nil {
		return nil, err
	}
	if err := cluster.campaign(ids[0]); err != nil {
		return nil, err
	}
	if err := cluster.pumpUntil(func() bool {
		return cluster.leaderID() == ids[0]
	}); err != nil {
		return nil, err
	}
	return cluster, nil
}

func (c *raftCluster) handleCommit(request raftClusterCommitRequest) {
	if c.err != nil {
		request.accepted <- 0
		request.result <- c.err
		return
	}

	c.nextID++
	id := c.nextID
	request.accepted <- id
	c.waiters[id] = request.result

	command, err := json.Marshal(raftCommitCommand{
		ID:     id,
		Record: request.record,
	})
	if err != nil {
		c.complete(id, err)
		return
	}

	c.pending[id] = raftClusterPendingProposal{command: command}
	c.proposePending()
}

func (c *raftCluster) proposePending() {
	if c.err != nil || len(c.pending) == 0 {
		return
	}

	leaderID, term := c.leaderStatus()
	if leaderID == 0 {
		return
	}

	for id, pending := range c.pending {
		if pending.lastLeaderID == leaderID && pending.lastTerm == term {
			continue
		}
		if err := c.nodes[leaderID].raw.Propose(pending.command); err != nil {
			c.complete(id, err)
			continue
		}
		pending.lastLeaderID = leaderID
		pending.lastTerm = term
		c.pending[id] = pending
	}
}

func (c *raftCluster) handleReadIndex(request raftClusterReadIndexRequest) {
	if c.err != nil {
		request.accepted <- 0
		request.result <- c.err
		return
	}

	c.nextReadID++
	id := c.nextReadID
	request.accepted <- id
	c.readWaiters[id] = request.result

	leaderID := c.leaderID()
	if leaderID == 0 {
		c.completeRead(id, errors.New("raft cluster has no current leader"))
		return
	}
	c.nodes[leaderID].raw.ReadIndex([]byte(strconv.FormatUint(id, 10)))
}

func (c *raftCluster) pumpUntilIdle() error {
	for step := 0; step < 1000; step++ {
		if !c.pump() {
			return c.err
		}
	}
	return errors.New("raft cluster did not become idle")
}

func (c *raftCluster) pumpUntil(done func() bool) error {
	for step := 0; step < 10000; step++ {
		if done() {
			return nil
		}
		if !c.pump() {
			c.tick()
		}
		if c.err != nil {
			return c.err
		}
	}
	return errors.New("raft cluster condition was not reached")
}

func (c *raftCluster) pump() bool {
	if c.err != nil {
		return false
	}

	progressed := false
	for _, id := range c.ids {
		node := c.nodes[id]
		for node.raw.HasReady() {
			ready := node.raw.Ready()
			messages, err := c.processReady(node, ready)
			if err != nil {
				c.fail(err)
				return false
			}
			node.raw.Advance(ready)
			progressed = true
			for _, message := range messages {
				if c.deliver(message) {
					progressed = true
				}
			}
		}
	}
	return progressed
}

func (c *raftCluster) processReady(node *raftClusterNode, ready raft.Ready) ([]pb.Message, error) {
	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := node.storage.ApplySnapshot(ready.Snapshot); err != nil {
			return nil, err
		}
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		if err := node.storage.SetHardState(ready.HardState); err != nil {
			return nil, err
		}
	}
	if err := node.storage.Append(ready.Entries); err != nil {
		return nil, err
	}

	for _, entry := range ready.CommittedEntries {
		switch entry.Type {
		case pb.EntryNormal:
			c.applyNormalEntry(node.id, entry)
		case pb.EntryConfChange:
			var change pb.ConfChange
			if err := change.Unmarshal(entry.Data); err != nil {
				return nil, err
			}
			node.raw.ApplyConfChange(change)
		}
	}
	for _, readState := range ready.ReadStates {
		id, err := strconv.ParseUint(string(readState.RequestCtx), 10, 64)
		if err != nil {
			continue
		}
		c.completeRead(id, nil)
	}

	return ready.Messages, nil
}

func (c *raftCluster) applyNormalEntry(nodeID uint64, entry pb.Entry) {
	if len(entry.Data) == 0 {
		return
	}

	var command raftCommitCommand
	if err := json.Unmarshal(entry.Data, &command); err != nil || command.ID == 0 {
		return
	}

	nodes := c.appliedBy[command.ID]
	if nodes == nil {
		nodes = make(map[uint64]struct{})
		c.appliedBy[command.ID] = nodes
	}
	if _, ok := nodes[nodeID]; ok {
		return
	}

	nodes[nodeID] = struct{}{}
	c.appliedOn[nodeID]++
	if len(nodes) < c.quorumSize() {
		return
	}
	if _, ok := c.committed[command.ID]; ok {
		return
	}
	c.committed[command.ID] = struct{}{}
	c.records[committedDocumentRecordKey(command.Record)] = command.Record
	c.complete(command.ID, nil)
}

func (c *raftCluster) deliver(message pb.Message) bool {
	if c.shouldDrop(message) {
		return false
	}
	node := c.nodes[message.To]
	if node == nil {
		return false
	}
	if err := node.raw.Step(message); err != nil {
		if errors.Is(err, raft.ErrStepLocalMsg) || errors.Is(err, raft.ErrStepPeerNotFound) {
			return false
		}
		c.fail(fmt.Errorf("raft step message %s from %d to %d: %w", message.Type, message.From, message.To, err))
		return false
	}
	return true
}

func (c *raftCluster) shouldDrop(message pb.Message) bool {
	if message.To == 0 {
		return true
	}
	if c.isolated[message.From] || c.isolated[message.To] {
		return true
	}
	return c.drops[raftMessagePair{from: message.From, to: message.To}]
}

func (c *raftCluster) tick() {
	for _, id := range c.ids {
		c.nodes[id].raw.Tick()
	}
}

func (c *raftCluster) campaign(nodeID uint64) error {
	if !c.hasNode(nodeID) {
		return fmt.Errorf("raft node %d does not exist", nodeID)
	}
	return c.nodes[nodeID].raw.Campaign()
}

func (c *raftCluster) leaderID() uint64 {
	leaderID, _ := c.leaderStatus()
	return leaderID
}

func (c *raftCluster) leaderStatus() (uint64, uint64) {
	var leaderID uint64
	var leaderTerm uint64
	for _, id := range c.ids {
		if c.isolated[id] {
			continue
		}
		status := c.nodes[id].raw.Status()
		if status.RaftState != raft.StateLeader {
			continue
		}
		if status.Term >= leaderTerm {
			leaderID = id
			leaderTerm = status.Term
		}
	}
	return leaderID, leaderTerm
}

func (c *raftCluster) quorumSize() int {
	return len(c.ids)/2 + 1
}

func (c *raftCluster) hasNode(nodeID uint64) bool {
	_, ok := c.nodes[nodeID]
	return ok
}

func (c *raftCluster) complete(id uint64, err error) {
	waiter, ok := c.waiters[id]
	if !ok {
		return
	}
	waiter <- err
	close(waiter)
	delete(c.waiters, id)
	delete(c.pending, id)
}

func (c *raftCluster) cancel(id uint64) {
	delete(c.waiters, id)
	delete(c.pending, id)
}

func (c *raftCluster) cancelRead(id uint64) {
	delete(c.readWaiters, id)
}

func (c *raftCluster) completeRead(id uint64, err error) {
	waiter, ok := c.readWaiters[id]
	if !ok {
		return
	}
	waiter <- err
	close(waiter)
	delete(c.readWaiters, id)
}

func (c *raftCluster) fail(err error) {
	if err == nil || c.err != nil {
		return
	}
	c.err = err
	c.closeWithError(err)
}

func (c *raftCluster) closeWithError(err error) {
	for id, waiter := range c.waiters {
		waiter <- err
		close(waiter)
		delete(c.waiters, id)
		delete(c.pending, id)
	}
	for id, waiter := range c.readWaiters {
		waiter <- err
		close(waiter)
		delete(c.readWaiters, id)
	}
}
