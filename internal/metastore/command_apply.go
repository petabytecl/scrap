package metastore

import (
	"fmt"
	"time"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Store) ApplyShardCommand(command *metastorev1.ShardCommand) error {
	if _, err := MarshalShardCommand(command); err != nil {
		return err
	}
	switch typed := command.GetCommand().(type) {
	case *metastorev1.ShardCommand_CommitDocument:
		return s.applyCommitDocument(typed.CommitDocument)
	case *metastorev1.ShardCommand_CompleteTransaction:
		return s.applyCompleteTransaction(typed.CompleteTransaction)
	case *metastorev1.ShardCommand_RecordUploadIntent:
		return s.applyRecordUploadIntent(typed.RecordUploadIntent, command.GetProposedAt())
	default:
		return fmt.Errorf("metastore: unsupported shard command %T", command.GetCommand())
	}
}

func (s *Store) applyCommitDocument(command *metastorev1.CommitDocumentCommand) error {
	if command == nil || command.GetDocument() == nil {
		return fmt.Errorf("metastore: commit document command requires document")
	}
	if err := validateSchemaVersion("document", command.GetDocument().GetSchemaVersion()); err != nil {
		return err
	}
	return s.PutDocument(documentFromProto(command.GetDocument()))
}

func (s *Store) applyCompleteTransaction(command *metastorev1.CompleteTransactionCommand) error {
	if command == nil {
		return fmt.Errorf("metastore: complete transaction command is required")
	}
	completedAt := command.GetCompletedAt()
	if completedAt == nil {
		return fmt.Errorf("metastore: complete transaction command requires completed_at")
	}
	_, err := s.CompleteTransaction(identity.Transaction{
		TenantID:      command.GetTenantId(),
		TransactionID: command.GetTransactionId(),
	}, completedAt.AsTime(), cloneTags(command.GetTags()))
	return err
}

func (s *Store) applyRecordUploadIntent(command *metastorev1.RecordUploadIntentCommand, proposedAt *timestamppb.Timestamp) error {
	if command == nil {
		return fmt.Errorf("metastore: record upload intent command is required")
	}
	updatedAt := time.Time{}
	if proposedAt != nil {
		updatedAt = proposedAt.AsTime()
	}
	return s.RecordUploadIntent(UploadIntent{
		BlockID:           command.GetBlockId(),
		BackendObjectKey:  command.GetBackendObjectKey(),
		IndexObjectKey:    command.GetIndexObjectKey(),
		EnvelopeObjectKey: command.GetEnvelopeObjectKey(),
		State:             UploadStatePending,
		UpdatedAt:         updatedAt,
	})
}
