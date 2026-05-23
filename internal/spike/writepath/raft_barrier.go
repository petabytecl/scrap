package writepath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

const raftCommitTimeout = 5 * time.Second

type RaftCommitBarrier struct {
	node    raft.Node
	storage raftMutableStorage
	diskLog *raftDiskLog

	mu         sync.Mutex
	nextID     uint64
	applied    int
	closed     bool
	stopped    bool
	waiters    map[uint64]chan error
	appliedIDs map[uint64]struct{}
	records    map[string]DocumentRecord

	stop chan struct{}
	done chan struct{}
}

type raftCommitCommand struct {
	ID     uint64
	Record DocumentRecord
}

func NewRaftCommitBarrier() (*RaftCommitBarrier, error) {
	return newRaftCommitBarrier(nil)
}

func NewDurableRaftCommitBarrier(dir string) (*RaftCommitBarrier, error) {
	diskLog, err := openRaftDiskLog(dir)
	if err != nil {
		return nil, err
	}
	barrier, err := newRaftCommitBarrier(diskLog)
	if err != nil {
		_ = diskLog.Close()
		return nil, err
	}
	return barrier, nil
}

func newRaftCommitBarrier(diskLog *raftDiskLog) (*RaftCommitBarrier, error) {
	storage := raftMutableStorage(newRaftStorageWithConfState(1))
	if diskLog != nil {
		storage = diskLog.storage
	}
	cfg := &raft.Config{
		ID:                        1,
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   storage,
		MaxSizePerMsg:             1024 * 1024,
		MaxInflightMsgs:           16,
		MaxUncommittedEntriesSize: 64 * 1024 * 1024,
		Logger:                    raftNopLogger{},
	}
	var node raft.Node
	if diskLog != nil && diskLog.hasEntries {
		node = raft.RestartNode(cfg)
	} else {
		node = raft.StartNode(cfg, []raft.Peer{{ID: 1}})
	}
	barrier := &RaftCommitBarrier{
		node:       node,
		storage:    storage,
		diskLog:    diskLog,
		waiters:    make(map[uint64]chan error),
		appliedIDs: make(map[uint64]struct{}),
		records:    make(map[string]DocumentRecord),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	if diskLog != nil {
		barrier.nextID = diskLog.nextID
		barrier.applied = diskLog.applied
		for id := range diskLog.appliedIDs {
			barrier.appliedIDs[id] = struct{}{}
		}
		for key, record := range diskLog.records {
			barrier.records[key] = record
		}
	}
	go barrier.run()

	ctx, cancel := context.WithTimeout(context.Background(), raftCommitTimeout)
	defer cancel()
	if err := node.Campaign(ctx); err != nil {
		_ = barrier.Close()
		return nil, err
	}
	if err := barrier.waitForLeader(ctx); err != nil {
		_ = barrier.Close()
		return nil, err
	}
	return barrier, nil
}

func (b *RaftCommitBarrier) CommitDocument(ctx context.Context, record DocumentRecord) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("raft commit barrier is closed")
	}
	b.nextID++
	id := b.nextID
	waiter := make(chan error, 1)
	b.waiters[id] = waiter
	b.mu.Unlock()

	command, err := json.Marshal(raftCommitCommand{
		ID:     id,
		Record: record,
	})
	if err != nil {
		b.removeWaiter(id)
		return err
	}

	proposeCtx, cancel := context.WithTimeout(ctx, raftCommitTimeout)
	defer cancel()
	if err := b.node.Propose(proposeCtx, command); err != nil {
		b.removeWaiter(id)
		return err
	}

	select {
	case err := <-waiter:
		return err
	case <-proposeCtx.Done():
		b.removeWaiter(id)
		return proposeCtx.Err()
	}
}

func (b *RaftCommitBarrier) AppliedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.applied
}

func (b *RaftCommitBarrier) CommittedDocument(tenantID, transactionID, documentName string) (DocumentRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.records[committedDocumentKey(tenantID, transactionID, documentName)]
	return record, ok
}

func (b *RaftCommitBarrier) CommittedDocuments() []DocumentRecord {
	b.mu.Lock()
	defer b.mu.Unlock()

	keys := make([]string, 0, len(b.records))
	for key := range b.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	records := make([]DocumentRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, b.records[key])
	}
	return records
}

func (b *RaftCommitBarrier) Close() error {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.stopped = true
	for id, waiter := range b.waiters {
		waiter <- errors.New("raft commit barrier is closed")
		close(waiter)
		delete(b.waiters, id)
	}
	b.mu.Unlock()

	close(b.stop)
	b.node.Stop()
	<-b.done
	if b.diskLog != nil {
		return b.diskLog.Close()
	}
	return nil
}

func (b *RaftCommitBarrier) run() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	defer close(b.done)

	for {
		select {
		case <-ticker.C:
			b.node.Tick()
		case ready := <-b.node.Ready():
			if err := b.processReady(ready); err != nil {
				b.fail(err)
			}
			b.node.Advance()
		case <-b.stop:
			return
		}
	}
}

func (b *RaftCommitBarrier) processReady(ready raft.Ready) error {
	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := b.storage.ApplySnapshot(ready.Snapshot); err != nil {
			return err
		}
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		if err := b.storage.SetHardState(ready.HardState); err != nil {
			return err
		}
	}
	if err := b.storage.Append(ready.Entries); err != nil {
		return err
	}
	if b.diskLog != nil {
		if err := b.diskLog.SaveReady(ready); err != nil {
			return err
		}
	}

	for _, entry := range ready.CommittedEntries {
		switch entry.Type {
		case pb.EntryNormal:
			if len(entry.Data) == 0 {
				continue
			}
			var command raftCommitCommand
			if err := json.Unmarshal(entry.Data, &command); err != nil {
				continue
			}
			b.apply(command)
			b.complete(command.ID, nil)
		case pb.EntryConfChange:
			var change pb.ConfChange
			if err := change.Unmarshal(entry.Data); err == nil {
				b.node.ApplyConfChange(change)
			}
		}
	}
	return nil
}

func (b *RaftCommitBarrier) waitForLeader(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if b.node.Status().Lead == 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("raft leader election timed out: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (b *RaftCommitBarrier) apply(command raftCommitCommand) {
	if command.ID == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.appliedIDs[command.ID]; ok {
		return
	}
	b.appliedIDs[command.ID] = struct{}{}
	b.applied++
	if command.ID > b.nextID {
		b.nextID = command.ID
	}
	b.records[committedDocumentRecordKey(command.Record)] = command.Record
}

func (b *RaftCommitBarrier) complete(id uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter, ok := b.waiters[id]
	if !ok {
		return
	}
	waiter <- err
	close(waiter)
	delete(b.waiters, id)
}

func (b *RaftCommitBarrier) removeWaiter(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiters, id)
}

func (b *RaftCommitBarrier) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, waiter := range b.waiters {
		waiter <- err
		close(waiter)
		delete(b.waiters, id)
	}
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

func committedDocumentRecordKey(record DocumentRecord) string {
	return committedDocumentKey(record.TenantID, record.TransactionID, record.DocumentName)
}

func committedDocumentKey(tenantID, transactionID, documentName string) string {
	return string(documentKey(tenantID, transactionID, documentName))
}
