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
}

type AuthorityOptions struct {
	Members          []Member
	LocalMemberID    string
	FreshnessChecker FreshnessChecker
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
	return authority, nil
}

func (a *Authority) Close() error {
	if a == nil || a.log == nil {
		return nil
	}
	return a.log.Close()
}

func (a *Authority) AppliedIndex() uint64 {
	if a == nil || a.log == nil {
		return 0
	}
	return a.log.LastIndex()
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
	snapshot, err := a.buildSnapshotLocked(a.log.LastIndex())
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
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureNoConflictingDocument(document); err != nil {
		return err
	}
	return a.appendAndApplyLocked(command)
}

func (a *Authority) CompleteTransaction(ctx context.Context, transaction identity.Transaction, completedAt time.Time, tags map[string]string, commandID string) (metastore.Transaction, error) {
	if err := ctx.Err(); err != nil {
		return metastore.Transaction{}, err
	}
	if err := a.requireWriteQuorum(ctx); err != nil {
		return metastore.Transaction{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.store.GetTransaction(transaction)
	if err != nil {
		return metastore.Transaction{}, err
	}
	if current.State == metastore.TransactionStateCompleted {
		return current, nil
	}
	if current.State != metastore.TransactionStateOpen {
		return metastore.Transaction{}, fmt.Errorf("%w: transaction %s/%s is not open", metastore.ErrTransactionClosed, transaction.TenantID, transaction.TransactionID)
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
	if err := a.appendAndApplyLocked(command); err != nil {
		return metastore.Transaction{}, err
	}
	return a.store.GetTransaction(transaction)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAndApplyLocked(command)
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
			return fmt.Errorf("raftmeta: apply command at index %d: %w", entry.Index, err)
		}
		expectedIndex++
	}
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

func (a *Authority) appendAndApplyLocked(command *metastorev1.ShardCommand) error {
	observe.SetRaftQueueDepth(1)
	defer observe.SetRaftQueueDepth(0)

	entry, err := a.log.Append(command)
	if err != nil {
		return err
	}
	if err := a.store.ApplyShardCommand(entry.Command); err != nil {
		return err
	}
	return nil
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
		AppliedIndex:  a.log.LastIndex(),
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
