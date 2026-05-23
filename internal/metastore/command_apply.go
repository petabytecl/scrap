package metastore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Store) ApplyShardCommand(command *metastorev1.ShardCommand) error {
	receipt, err := commandReceipt(command)
	if err != nil {
		return err
	}
	existing, err := s.GetCommandReceipt(receipt.CommandID)
	if err == nil {
		if existing.CommandSHA256 == receipt.CommandSHA256 {
			return nil
		}
		return fmt.Errorf("%w: command id already applied with different payload", ErrConflict)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	switch typed := command.GetCommand().(type) {
	case *metastorev1.ShardCommand_CommitDocument:
		err = s.applyCommitDocument(batch, typed.CommitDocument)
	case *metastorev1.ShardCommand_CompleteTransaction:
		err = s.applyCompleteTransaction(batch, typed.CompleteTransaction)
	case *metastorev1.ShardCommand_RecordUploadIntent:
		err = s.applyRecordUploadIntent(batch, typed.RecordUploadIntent, command.GetProposedAt())
	case *metastorev1.ShardCommand_UpdateUploadIntentState:
		err = s.applyUpdateUploadIntentState(batch, typed.UpdateUploadIntentState, command.GetProposedAt())
	case *metastorev1.ShardCommand_UpdateRestoreState:
		err = s.applyUpdateRestoreState(batch, typed.UpdateRestoreState)
	case *metastorev1.ShardCommand_RecordRepairState:
		err = s.applyRecordRepairState(batch, typed.RecordRepairState, command.GetProposedAt())
	case *metastorev1.ShardCommand_TombstoneDocument:
		err = s.applyTombstoneDocument(batch, typed.TombstoneDocument)
	default:
		return fmt.Errorf("metastore: unsupported shard command %T", command.GetCommand())
	}
	if err != nil {
		return err
	}
	if err := s.recordCommandReceipt(batch, receipt); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func commandReceipt(command *metastorev1.ShardCommand) (CommandReceipt, error) {
	if _, err := MarshalShardCommand(command); err != nil {
		return CommandReceipt{}, err
	}
	stable := proto.Clone(command).(*metastorev1.ShardCommand)
	stable.ProposedAt = nil
	data, err := protoMarshal.Marshal(stable)
	if err != nil {
		return CommandReceipt{}, err
	}
	return CommandReceipt{
		CommandID:     command.GetCommandId(),
		CommandSHA256: sha256.Sum256(data),
	}, nil
}

func (s *Store) applyCommitDocument(batch *pebble.Batch, command *metastorev1.CommitDocumentCommand) error {
	if command == nil || command.GetDocument() == nil {
		return fmt.Errorf("metastore: commit document command requires document")
	}
	if err := validateSchemaVersion("document", command.GetDocument().GetSchemaVersion()); err != nil {
		return err
	}
	return s.putDocument(batch, documentFromProto(command.GetDocument()))
}

func (s *Store) applyCompleteTransaction(batch *pebble.Batch, command *metastorev1.CompleteTransactionCommand) error {
	if command == nil {
		return fmt.Errorf("metastore: complete transaction command is required")
	}
	completedAt := command.GetCompletedAt()
	if completedAt == nil {
		return fmt.Errorf("metastore: complete transaction command requires completed_at")
	}
	_, err := s.completeTransaction(batch, identity.Transaction{
		TenantID:      command.GetTenantId(),
		TransactionID: command.GetTransactionId(),
	}, completedAt.AsTime(), cloneTags(command.GetTags()))
	return err
}

func (s *Store) applyRecordUploadIntent(batch *pebble.Batch, command *metastorev1.RecordUploadIntentCommand, proposedAt *timestamppb.Timestamp) error {
	if command == nil {
		return fmt.Errorf("metastore: record upload intent command is required")
	}
	updatedAt := time.Time{}
	if proposedAt != nil {
		updatedAt = proposedAt.AsTime()
	}
	return s.recordUploadIntent(batch, UploadIntent{
		BlockID:           command.GetBlockId(),
		BackendObjectKey:  command.GetBackendObjectKey(),
		IndexObjectKey:    command.GetIndexObjectKey(),
		EnvelopeObjectKey: command.GetEnvelopeObjectKey(),
		State:             UploadStatePending,
		UpdatedAt:         updatedAt,
	})
}

func (s *Store) applyUpdateUploadIntentState(batch *pebble.Batch, command *metastorev1.UpdateUploadIntentStateCommand, proposedAt *timestamppb.Timestamp) error {
	if command == nil {
		return fmt.Errorf("metastore: update upload intent state command is required")
	}
	updatedAt := time.Time{}
	if proposedAt != nil {
		updatedAt = proposedAt.AsTime()
	}
	_, err := s.updateUploadIntentState(
		batch,
		command.GetBlockId(),
		UploadState(command.GetState()),
		command.GetLastError(),
		updatedAt,
	)
	return err
}

func (s *Store) applyUpdateRestoreState(batch *pebble.Batch, command *metastorev1.UpdateRestoreStateCommand) error {
	if command == nil {
		return fmt.Errorf("metastore: update restore state command is required")
	}
	state := RestoreState(command.GetRestoreState())
	if _, err := availabilityFromRestoreState(state); err != nil {
		return err
	}
	if command.DocumentName != nil {
		_, err := s.updateDocumentRestoreState(batch, identity.Document{
			TenantID:      command.GetTenantId(),
			TransactionID: command.GetTransactionId(),
			DocumentName:  command.GetDocumentName(),
		}, state)
		return err
	}
	_, err := s.updateTransactionRestoreState(batch, identity.Transaction{
		TenantID:      command.GetTenantId(),
		TransactionID: command.GetTransactionId(),
	}, state)
	return err
}

func (s *Store) applyRecordRepairState(batch *pebble.Batch, command *metastorev1.RecordRepairStateCommand, proposedAt *timestamppb.Timestamp) error {
	if command == nil {
		return fmt.Errorf("metastore: record repair state command is required")
	}
	updatedAt := time.Time{}
	if proposedAt != nil {
		updatedAt = proposedAt.AsTime()
	}
	return s.recordRepairState(batch, RepairState{
		Identity: identity.Document{
			TenantID:      command.GetTenantId(),
			TransactionID: command.GetTransactionId(),
			DocumentName:  command.GetDocumentName(),
		},
		PhysicalRef: command.GetPhysicalRef(),
		IncidentID:  command.GetIncidentId(),
		Quarantined: command.GetQuarantined(),
		UpdatedAt:   updatedAt,
	})
}

func (s *Store) applyTombstoneDocument(batch *pebble.Batch, command *metastorev1.TombstoneDocumentCommand) error {
	if command == nil {
		return fmt.Errorf("metastore: tombstone document command is required")
	}
	if command.GetTombstonedAt() == nil {
		return fmt.Errorf("metastore: tombstone document command requires tombstoned_at")
	}
	_, err := s.tombstoneDocument(batch, identity.Document{
		TenantID:      command.GetTenantId(),
		TransactionID: command.GetTransactionId(),
		DocumentName:  command.GetDocumentName(),
	}, command.GetTombstonedAt().AsTime(), command.GetOperationId())
	return err
}
