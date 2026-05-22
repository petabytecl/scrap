package metastore

import (
	"fmt"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
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
