package raftmeta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Authority struct {
	mu      sync.Mutex
	shardID string
	log     *Log
	store   *metastore.Store
}

func OpenAuthority(dir string, shardID string, store *metastore.Store) (*Authority, error) {
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
	authority := &Authority{shardID: shardID, log: log, store: store}
	if err := authority.replay(); err != nil {
		_ = log.Close()
		return nil, err
	}
	return authority, nil
}

func (a *Authority) Close() error {
	if a == nil || a.log == nil {
		return nil
	}
	return a.log.Close()
}

func (a *Authority) CommitDocument(ctx context.Context, document metastore.Document, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) CompleteTransaction(ctx context.Context, transaction identity.Transaction, completedAt time.Time, tags map[string]string, commandID string) (metastore.Transaction, error) {
	if err := ctx.Err(); err != nil {
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
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.store.GetTransaction(transaction); err != nil {
		return metastore.Transaction{}, err
	}
	if err := a.appendAndApply(command); err != nil {
		return metastore.Transaction{}, err
	}
	return a.store.GetTransaction(transaction)
}

func (a *Authority) RecordUploadIntent(ctx context.Context, intent metastore.UploadIntent, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) UpdateDocumentRestoreState(ctx context.Context, doc identity.Document, state metastore.RestoreState, reason string, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) UpdateTransactionRestoreState(ctx context.Context, transaction identity.Transaction, state metastore.RestoreState, reason string, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) RecordRepairState(ctx context.Context, state metastore.RepairState, commandID string, proposedAt time.Time) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) TombstoneDocument(ctx context.Context, doc identity.Document, tombstonedAt time.Time, operationID string, commandID string) error {
	if err := ctx.Err(); err != nil {
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
	return a.appendAndApply(command)
}

func (a *Authority) replay() error {
	entries, err := a.log.Replay()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := a.store.ApplyShardCommand(entry.Command); err != nil {
			return fmt.Errorf("raftmeta: apply command at index %d: %w", entry.Index, err)
		}
	}
	return nil
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

func (a *Authority) appendAndApply(command *metastorev1.ShardCommand) error {
	entry, err := a.log.Append(command)
	if err != nil {
		return err
	}
	if err := a.store.ApplyShardCommand(entry.Command); err != nil {
		return err
	}
	return nil
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
