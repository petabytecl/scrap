package raftmeta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/observe"
)

type Authority struct {
	mu            sync.Mutex
	dir           string
	shardID       string
	log           *Log
	store         *metastore.Store
	members       []Member
	localMemberID string
	freshness     FreshnessChecker
	snapshotIndex uint64
	appliedIndex  uint64

	proposals         chan *authorityProposal
	proposalsMu       sync.Mutex
	proposalsClosed   bool
	proposalsStopped  bool
	proposalSenders   sync.WaitGroup
	stopProposals     chan struct{}
	proposalsDone     chan struct{}
	closeOnce         sync.Once
	metadataBatchWait time.Duration
	metadataBatchMax  int
	failureErr        error
}

type AuthorityOptions struct {
	Members          []Member
	LocalMemberID    string
	FreshnessChecker FreshnessChecker
}

const (
	defaultAuthorityProposalQueue = 1024
	defaultMetadataBatchWait      = 0
	defaultMetadataBatchMax       = 64
)

type authorityProposal struct {
	ctx     context.Context
	prepare func() (preparedAuthorityCommand, error)
	done    chan authorityProposalResult
	once    sync.Once
}

type preparedAuthorityCommand struct {
	proposal *authorityProposal
	command  *metastorev1.ShardCommand
	result   any
	after    func() (any, error)
}

type authorityProposalResult struct {
	value any
	err   error
}

func OpenAuthority(dir, shardID string, store *metastore.Store) (*Authority, error) {
	return OpenAuthorityWithOptions(dir, shardID, store, AuthorityOptions{})
}

func OpenAuthorityWithOptions(dir, shardID string, store *metastore.Store, options AuthorityOptions) (*Authority, error) {
	if shardID == "" {
		return nil, errors.New("raftmeta: shard id is required")
	}
	if store == nil {
		return nil, errors.New("raftmeta: metastore is required")
	}
	log, err := OpenLog(dir)
	if err != nil {
		return nil, err
	}
	authority := &Authority{
		dir:       dir,
		shardID:   shardID,
		log:       log,
		store:     store,
		members:   normalizeMembers(options.Members),
		freshness: options.FreshnessChecker,

		proposals:         make(chan *authorityProposal, defaultAuthorityProposalQueue),
		stopProposals:     make(chan struct{}),
		proposalsDone:     make(chan struct{}),
		metadataBatchWait: defaultMetadataBatchWait,
		metadataBatchMax:  defaultMetadataBatchMax,
	}
	if err := authority.installLatestSnapshot(); err != nil {
		_ = log.Close()
		return nil, err
	}
	if err := authority.replay(); err != nil {
		_ = log.Close()
		return nil, err
	}
	authority.localMemberID = options.LocalMemberID
	if authority.localMemberID == "" {
		authority.localMemberID = defaultLocalMemberID(authority.members)
	}
	if authority.freshness == nil {
		authority.freshness = singleVoterFreshnessChecker{}
	}
	go authority.runProposalWorker()
	return authority, nil
}

func (a *Authority) Close() error {
	if a == nil || a.log == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.proposalsMu.Lock()
		a.closeProposalsLocked()
		a.proposalsMu.Unlock()
		a.proposalSenders.Wait()
		<-a.proposalsDone
		a.failPendingProposals(errors.New("raftmeta: authority is closed"))
	})
	return a.log.Close()
}

func (a *Authority) AppliedIndex() uint64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appliedIndex
}

// CheckHealth reports fail-closed authority state that requires operator restart.
func (a *Authority) CheckHealth() error {
	if a == nil {
		return errors.New("raftmeta: authority is closed")
	}
	a.mu.Lock()
	failure := a.failureErr
	a.mu.Unlock()
	if failure != nil {
		return fmt.Errorf("raftmeta: authority is fail-closed; restart required: %w", failure)
	}
	if a.log == nil {
		return errors.New("raftmeta: command log is closed")
	}
	return a.log.CheckHealth()
}

func (a *Authority) EnsureWriteReady(ctx context.Context) error {
	return a.requireWriteQuorum(ctx)
}

func (a *Authority) ReadFresh(ctx context.Context) error {
	return a.requireReadIndex(ctx)
}

func (a *Authority) LeaseReadFresh(context.Context) error {
	return ErrLeaseReadDisabled
}

func (a *Authority) CreateSnapshot(ctx context.Context) (SnapshotInfo, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.requireCleanApplyStateLocked("create snapshot"); err != nil {
		return SnapshotInfo{}, err
	}
	snapshot, err := a.buildSnapshotLocked(a.appliedIndex)
	if err != nil {
		return SnapshotInfo{}, err
	}
	if err := writeSnapshotFile(a.dir, snapshot); err != nil {
		return SnapshotInfo{}, err
	}
	a.snapshotIndex = snapshot.GetLastIndex()
	return snapshotInfo(a.dir, snapshot), nil
}

func (a *Authority) CompactLog(ctx context.Context, throughIndex uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.requireCleanApplyStateLocked("compact log"); err != nil {
		return err
	}
	if throughIndex == 0 {
		return nil
	}
	if a.snapshotIndex < throughIndex {
		snapshot, err := readSnapshotFile(a.dir)
		if errors.Is(err, errSnapshotNotFound) {
			snapshot = nil
		} else if err != nil {
			return err
		}
		if snapshot != nil {
			a.snapshotIndex = snapshot.GetLastIndex()
		}
	}
	if a.snapshotIndex < throughIndex {
		return fmt.Errorf("raftmeta: snapshot through index %d is required before compacting through index %d", a.snapshotIndex, throughIndex)
	}
	return a.log.Compact(throughIndex)
}

func (a *Authority) CommitDocument(ctx context.Context, document metastore.Document, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_CommitDocument{
			CommitDocument: &metastorev1.CommitDocumentCommand{
				Document: metastore.DocumentRecord(document),
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		if err := a.ensureNoConflictingDocument(document); err != nil {
			return preparedAuthorityCommand{}, err
		}
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) CompleteTransaction(ctx context.Context, transaction identity.Transaction, completedAt time.Time, tags map[string]string, commandID string) (metastore.Transaction, error) {
	if err := ctx.Err(); err != nil {
		return metastore.Transaction{}, err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return metastore.Transaction{}, err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(completedAt),
		Command: &metastorev1.ShardCommand_CompleteTransaction{
			CompleteTransaction: &metastorev1.CompleteTransactionCommand{
				TenantId:      transaction.TenantID,
				TransactionId: transaction.TransactionID,
				CompletedAt:   timestamppb.New(completedAt),
				Tags:          cloneTags(tags),
			},
		},
	}
	result, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		current, err := a.store.GetTransaction(transaction)
		if err != nil {
			return preparedAuthorityCommand{}, err
		}
		if current.State == metastore.TransactionStateCompleted {
			return preparedAuthorityCommand{result: current}, nil
		}
		if current.State != metastore.TransactionStateOpen {
			return preparedAuthorityCommand{}, fmt.Errorf("%w: transaction %s/%s is not open", metastore.ErrTransactionClosed, transaction.TenantID, transaction.TransactionID)
		}
		completed := completedTransactionResult(current, completedAt, tags)
		return preparedAuthorityCommand{
			command: command,
			result:  completed,
			after: func() (any, error) {
				return a.store.GetTransaction(transaction)
			},
		}, nil
	})
	if err != nil {
		return metastore.Transaction{}, err
	}
	completed, ok := result.(metastore.Transaction)
	if !ok {
		return metastore.Transaction{}, errors.New("raftmeta: complete transaction returned invalid result")
	}
	return completed, nil
}

func (a *Authority) RecordUploadIntent(ctx context.Context, intent metastore.UploadIntent, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_RecordUploadIntent{
			RecordUploadIntent: &metastorev1.RecordUploadIntentCommand{
				BlockId:           intent.BlockID,
				BackendObjectKey:  intent.BackendObjectKey,
				IndexObjectKey:    intent.IndexObjectKey,
				EnvelopeObjectKey: intent.EnvelopeObjectKey,
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) UpdateUploadIntentState(ctx context.Context, blockID string, state metastore.UploadState, lastError, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_UpdateUploadIntentState{
			UpdateUploadIntentState: &metastorev1.UpdateUploadIntentStateCommand{
				BlockId: blockID,
				State:   metastorev1.UploadState(state),
			},
		},
	}
	if lastError != "" {
		command.GetUpdateUploadIntentState().LastError = &lastError
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) UpdateDocumentRestoreState(ctx context.Context, doc identity.Document, state metastore.RestoreState, reason, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	documentName := doc.DocumentName
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_UpdateRestoreState{
			UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
				TenantId:      doc.TenantID,
				TransactionId: doc.TransactionID,
				DocumentName:  &documentName,
				RestoreState:  metastorev1.RestoreState(state),
				Reason:        reason,
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) UpdateTransactionRestoreState(ctx context.Context, transaction identity.Transaction, state metastore.RestoreState, reason, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_UpdateRestoreState{
			UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
				TenantId:      transaction.TenantID,
				TransactionId: transaction.TransactionID,
				RestoreState:  metastorev1.RestoreState(state),
				Reason:        reason,
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) RecordRepairState(ctx context.Context, state metastore.RepairState, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_RecordRepairState{
			RecordRepairState: &metastorev1.RecordRepairStateCommand{
				TenantId:      state.Identity.TenantID,
				TransactionId: state.Identity.TransactionID,
				DocumentName:  state.Identity.DocumentName,
				PhysicalRef:   state.PhysicalRef,
				IncidentId:    state.IncidentID,
				Quarantined:   state.Quarantined,
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) TombstoneDocument(ctx context.Context, doc identity.Document, tombstonedAt time.Time, operationID, commandID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return err
	}
	command := &metastorev1.ShardCommand{
		SchemaVersion: metastore.CurrentSchemaVersion,
		ShardId:       a.shardID,
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(tombstonedAt),
		Command: &metastorev1.ShardCommand_TombstoneDocument{
			TombstoneDocument: &metastorev1.TombstoneDocumentCommand{
				TenantId:      doc.TenantID,
				TransactionId: doc.TransactionID,
				DocumentName:  doc.DocumentName,
				TombstonedAt:  timestamppb.New(tombstonedAt),
				OperationId:   operationID,
			},
		},
	}
	_, err := a.submitProposal(ctx, func() (preparedAuthorityCommand, error) {
		return preparedAuthorityCommand{command: command}, nil
	})
	return err
}

func (a *Authority) replay() error {
	entries, err := a.log.Replay()
	if err != nil {
		return err
	}
	expectedIndex := a.snapshotIndex + 1
	for _, entry := range entries {
		if entry.Index <= a.snapshotIndex {
			continue
		}
		if entry.Index != expectedIndex {
			return fmt.Errorf("raftmeta: command index %d after snapshot index %d; want %d", entry.Index, a.snapshotIndex, expectedIndex)
		}
		if err := a.store.ApplyShardCommand(entry.Command); err != nil {
			if isDeterministicCommandRejection(err) {
				expectedIndex++
				continue
			}
			return fmt.Errorf("raftmeta: apply command at index %d: %w", entry.Index, err)
		}
		expectedIndex++
	}
	a.appliedIndex = expectedIndex - 1
	return nil
}

func (a *Authority) installLatestSnapshot() error {
	snapshot, err := readSnapshotFile(a.dir)
	if errors.Is(err, errSnapshotNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if snapshot.GetShardId() != a.shardID {
		return fmt.Errorf("raftmeta: snapshot shard %q does not match authority shard %q", snapshot.GetShardId(), a.shardID)
	}
	if err := a.store.ApplyShardSnapshot(snapshot); err != nil {
		return err
	}
	a.snapshotIndex = snapshot.GetLastIndex()
	a.appliedIndex = a.snapshotIndex
	a.log.EnsureNextIndex(a.snapshotIndex + 1)
	a.members = normalizeMembers(membershipFromProto(snapshot.GetMembership()))
	return nil
}

func (a *Authority) buildSnapshotLocked(lastIndex uint64) (*metastorev1.ShardSnapshot, error) {
	source, err := a.loadSnapshotSource()
	if err != nil {
		return nil, err
	}
	return a.buildSnapshotFromSource(lastIndex, source), nil
}

type snapshotSource struct {
	documents       []metastore.Document
	transactions    []metastore.Transaction
	uploadIntents   []metastore.UploadIntent
	repairStates    []metastore.RepairState
	commandReceipts []metastore.CommandReceipt
}

func (a *Authority) loadSnapshotSource() (snapshotSource, error) {
	documents, err := a.store.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return snapshotSource{}, err
	}
	sortDocuments(documents)
	transactions, err := a.store.ListTransactions()
	if err != nil {
		return snapshotSource{}, err
	}
	sortTransactions(transactions)
	uploadIntents, err := a.store.ListUploadIntents()
	if err != nil {
		return snapshotSource{}, err
	}
	sort.Slice(uploadIntents, func(i, j int) bool {
		return uploadIntents[i].BlockID < uploadIntents[j].BlockID
	})
	repairStates, err := a.store.ListRepairStates()
	if err != nil {
		return snapshotSource{}, err
	}
	sortRepairStates(repairStates)
	commandReceipts, err := a.store.ListCommandReceipts()
	if err != nil {
		return snapshotSource{}, err
	}
	sort.Slice(commandReceipts, func(i, j int) bool {
		return commandReceipts[i].CommandID < commandReceipts[j].CommandID
	})
	return snapshotSource{
		documents:       documents,
		transactions:    transactions,
		uploadIntents:   uploadIntents,
		repairStates:    repairStates,
		commandReceipts: commandReceipts,
	}, nil
}

func (a *Authority) buildSnapshotFromSource(lastIndex uint64, source snapshotSource) *metastorev1.ShardSnapshot {
	snapshot := &metastorev1.ShardSnapshot{
		SchemaVersion:   metastore.CurrentSchemaVersion,
		ShardId:         a.shardID,
		LastIndex:       lastIndex,
		Documents:       make([]*metastorev1.DocumentRecord, 0, len(source.documents)),
		Transactions:    make([]*metastorev1.TransactionRecord, 0, len(source.transactions)),
		UploadIntents:   make([]*metastorev1.UploadIntentRecord, 0, len(source.uploadIntents)),
		RepairStates:    make([]*metastorev1.RepairStateRecord, 0, len(source.repairStates)),
		CommandReceipts: make([]*metastorev1.CommandReceiptRecord, 0, len(source.commandReceipts)),
		Membership:      membershipToProto(a.members),
	}
	for _, document := range source.documents {
		record := metastore.DocumentRecord(document)
		record.ShardId = a.shardID
		record.CommittedIndex = lastIndex
		snapshot.Documents = append(snapshot.Documents, record)
	}
	for _, transaction := range source.transactions {
		record := metastore.TransactionRecord(transaction)
		record.ShardId = a.shardID
		record.CommittedIndex = lastIndex
		snapshot.Transactions = append(snapshot.Transactions, record)
	}
	for _, intent := range source.uploadIntents {
		record := metastore.UploadIntentRecord(intent)
		record.ShardId = a.shardID
		record.CommittedIndex = lastIndex
		snapshot.UploadIntents = append(snapshot.UploadIntents, record)
	}
	for _, state := range source.repairStates {
		record := metastore.RepairStateRecord(state)
		record.ShardId = a.shardID
		record.CommittedIndex = lastIndex
		snapshot.RepairStates = append(snapshot.RepairStates, record)
	}
	for _, receipt := range source.commandReceipts {
		record := metastore.CommandReceiptRecord(receipt)
		record.ShardId = a.shardID
		record.CommittedIndex = lastIndex
		snapshot.CommandReceipts = append(snapshot.CommandReceipts, record)
	}
	return snapshot
}

func sortDocuments(documents []metastore.Document) {
	sort.Slice(documents, func(i, j int) bool {
		left := documents[i].Identity
		right := documents[j].Identity
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		if left.TransactionID != right.TransactionID {
			return left.TransactionID < right.TransactionID
		}
		return left.DocumentName < right.DocumentName
	})
}

func sortTransactions(transactions []metastore.Transaction) {
	sort.Slice(transactions, func(i, j int) bool {
		if transactions[i].Identity.TenantID != transactions[j].Identity.TenantID {
			return transactions[i].Identity.TenantID < transactions[j].Identity.TenantID
		}
		return transactions[i].Identity.TransactionID < transactions[j].Identity.TransactionID
	})
}

func sortRepairStates(repairStates []metastore.RepairState) {
	sort.Slice(repairStates, func(i, j int) bool {
		left := repairStates[i]
		right := repairStates[j]
		if left.Identity.TenantID != right.Identity.TenantID {
			return left.Identity.TenantID < right.Identity.TenantID
		}
		if left.Identity.TransactionID != right.Identity.TransactionID {
			return left.Identity.TransactionID < right.Identity.TransactionID
		}
		if left.Identity.DocumentName != right.Identity.DocumentName {
			return left.Identity.DocumentName < right.Identity.DocumentName
		}
		return left.IncidentID < right.IncidentID
	})
}

func (a *Authority) ensureNoConflictingDocument(document metastore.Document) error {
	existing, err := a.store.HeadDocument(document.Identity)
	if errors.Is(err, metastore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	existingData, err := metastore.MarshalDocument(existing)
	if err != nil {
		return err
	}
	newData, err := metastore.MarshalDocument(document)
	if err != nil {
		return err
	}
	if bytes.Equal(existingData, newData) {
		return nil
	}
	return fmt.Errorf("%w: document already exists with different metadata", metastore.ErrConflict)
}

func (a *Authority) submitProposal(ctx context.Context, prepare func() (preparedAuthorityCommand, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	proposal := &authorityProposal{
		ctx:     ctx,
		prepare: prepare,
		done:    make(chan authorityProposalResult, 1),
	}

	a.proposalsMu.Lock()
	if a.proposalsClosed {
		a.proposalsMu.Unlock()
		return nil, a.proposalClosedError()
	}
	a.proposalSenders.Add(1)
	a.proposalsMu.Unlock()

	var enqueueErr error
	sent := false
	select {
	case <-ctx.Done():
		enqueueErr = ctx.Err()
	case <-a.stopProposals:
		enqueueErr = a.proposalClosedError()
	case a.proposals <- proposal:
		sent = true
	}
	a.proposalSenders.Done()
	if !sent {
		return nil, enqueueErr
	}

	observe.IncrementRaftQueueDepth()
	defer observe.DecrementRaftQueueDepth()

	result := <-proposal.done
	return result.value, result.err
}

func (a *Authority) runProposalWorker() {
	var active []*authorityProposal
	defer close(a.proposalsDone)
	defer func() {
		if recovered := recover(); recovered != nil {
			a.failProposalWorkerPanic(active, recovered)
		}
	}()
	for {
		proposal, ok := a.receiveProposal()
		if !ok {
			a.failPendingProposals(errors.New("raftmeta: authority is closed"))
			return
		}
		active = a.collectProposalBatch(proposal)
		a.processProposalBatch(active)
		active = nil
	}
}

func (a *Authority) closeProposalsLocked() {
	a.proposalsClosed = true
	if !a.proposalsStopped {
		close(a.stopProposals)
		a.proposalsStopped = true
	}
}

func (a *Authority) failProposalWorkerPanic(active []*authorityProposal, recovered any) {
	err := fmt.Errorf("raftmeta: proposal worker panic: %v", recovered)
	a.mu.Lock()
	failure := a.recordFailureLocked(err)
	a.mu.Unlock()

	a.proposalsMu.Lock()
	a.closeProposalsLocked()
	a.proposalsMu.Unlock()
	a.proposalSenders.Wait()

	replyProposalBatch(active, nil, failure)
	a.failPendingProposals(failure)
}

func (a *Authority) proposalClosedError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failureErr != nil {
		return a.failureErr
	}
	return errors.New("raftmeta: authority is closed")
}

func (a *Authority) receiveProposal() (*authorityProposal, bool) {
	select {
	case <-a.stopProposals:
		return nil, false
	case proposal := <-a.proposals:
		return proposal, true
	}
}

func (a *Authority) collectProposalBatch(first *authorityProposal) []*authorityProposal {
	batch := []*authorityProposal{first}
	maxBatch := a.metadataBatchMax
	if maxBatch < 1 {
		maxBatch = 1
	}
	if a.metadataBatchWait <= 0 {
		return a.drainReadyProposals(batch, maxBatch)
	}
	timer := time.NewTimer(a.metadataBatchWait)
	defer timer.Stop()
	for len(batch) < maxBatch {
		select {
		case proposal := <-a.proposals:
			batch = append(batch, proposal)
		case <-timer.C:
			return batch
		case <-a.stopProposals:
			return batch
		}
	}
	return batch
}

func (a *Authority) drainReadyProposals(batch []*authorityProposal, maxBatch int) []*authorityProposal {
	for len(batch) < maxBatch {
		select {
		case proposal := <-a.proposals:
			batch = append(batch, proposal)
		default:
			return batch
		}
	}
	return batch
}

func (a *Authority) processProposalBatch(batch []*authorityProposal) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.failureErr != nil {
		replyProposalBatch(batch, nil, a.failureErr)
		return
	}
	prepared := a.prepareProposalBatch(batch)
	if len(prepared) == 0 {
		return
	}
	commands := make([]*metastorev1.ShardCommand, 0, len(prepared))
	for _, command := range prepared {
		commands = append(commands, command.command)
	}
	entries, err := a.log.AppendBatch(commands)
	if err != nil {
		failure := a.recordFailureLocked(err)
		replyPreparedBatch(prepared, 0, failure)
		return
	}
	a.applyPreparedEntries(prepared, entries)
}

func (a *Authority) prepareProposalBatch(batch []*authorityProposal) []preparedAuthorityCommand {
	prepared := make([]preparedAuthorityCommand, 0, len(batch))
	for _, proposal := range batch {
		if err := proposal.ctx.Err(); err != nil {
			proposal.reply(nil, err)
			continue
		}
		command, err := proposal.prepare()
		if err != nil {
			proposal.reply(nil, err)
			continue
		}
		command.proposal = proposal
		if command.command == nil {
			proposal.reply(command.result, nil)
			continue
		}
		prepared = append(prepared, command)
	}
	return prepared
}

func (a *Authority) applyPreparedEntries(prepared []preparedAuthorityCommand, entries []Entry) {
	for index, entry := range entries {
		if err := a.store.ApplyShardCommand(entry.Command); err != nil {
			if isDeterministicCommandRejection(err) {
				a.appliedIndex = entry.Index
				prepared[index].proposal.reply(nil, err)
				continue
			}
			failure := a.recordFailureLocked(fmt.Errorf("raftmeta: apply command at index %d: %w", entry.Index, err))
			replyPreparedBatch(prepared, index, failure)
			return
		}
		a.appliedIndex = entry.Index
		value, err := prepared[index].afterApply()
		prepared[index].proposal.reply(value, err)
	}
}

func (command preparedAuthorityCommand) afterApply() (any, error) {
	if command.after == nil {
		return command.result, nil
	}
	value, err := command.after()
	if err != nil && command.result != nil {
		return command.result, nil
	}
	return value, err
}

func completedTransactionResult(current metastore.Transaction, completedAt time.Time, tags map[string]string) metastore.Transaction {
	completedAtCopy := completedAt
	current.State = metastore.TransactionStateCompleted
	current.CompletedAt = &completedAtCopy
	current.Tags = cloneTags(tags)
	return current
}

func (a *Authority) recordFailureLocked(err error) error {
	if a.failureErr == nil {
		a.failureErr = err
	}
	return a.failureErr
}

func (a *Authority) requireCleanApplyStateLocked(operation string) error {
	if a.failureErr == nil {
		return nil
	}
	return fmt.Errorf("raftmeta: authority is fail-closed; %s refused; restart required: %w", operation, a.failureErr)
}

// These errors are deterministic no-op command outcomes, not local durability failures.
func isDeterministicCommandRejection(err error) bool {
	return errors.Is(err, metastore.ErrConflict) ||
		errors.Is(err, metastore.ErrTransactionClosed) ||
		errors.Is(err, metastore.ErrNotFound)
}

func replyPreparedBatch(prepared []preparedAuthorityCommand, start int, err error) {
	for _, command := range prepared[start:] {
		command.proposal.reply(nil, err)
	}
}

func replyProposalBatch(batch []*authorityProposal, value any, err error) {
	for _, proposal := range batch {
		proposal.reply(value, err)
	}
}

func (a *Authority) failPendingProposals(err error) {
	for {
		select {
		case proposal := <-a.proposals:
			proposal.reply(nil, err)
		default:
			return
		}
	}
}

func (p *authorityProposal) reply(value any, err error) {
	p.once.Do(func() {
		p.done <- authorityProposalResult{value: value, err: err}
	})
}

func (a *Authority) requireWriteQuorum(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	checker, check := a.freshnessSnapshot()
	return checker.RequireWriteQuorum(ctx, check)
}

func (a *Authority) requireReadIndex(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	checker, check := a.freshnessSnapshot()
	return checker.RequireReadIndex(ctx, check)
}

func (a *Authority) freshnessSnapshot() (FreshnessChecker, FreshnessCheck) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.freshness, a.freshnessCheckLocked()
}

func (a *Authority) freshnessCheckLocked() FreshnessCheck {
	return FreshnessCheck{
		ShardID:       a.shardID,
		LocalMemberID: a.localMemberID,
		AppliedIndex:  a.appliedIndex,
		Members:       append([]Member(nil), a.members...),
	}
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}
