package localstorage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type OperationRunResult struct {
	Scanned   int
	Skipped   int
	Succeeded int
	Failed    int
}

func (a *Application) RunQueuedOperationsOnce(ctx context.Context, store *operations.Store) (OperationRunResult, error) {
	var result OperationRunResult
	if store == nil {
		return result, fmt.Errorf("localstorage: operation store is not configured")
	}
	queued, err := store.List(operations.ListFilter{
		States: []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_QUEUED},
	})
	if err != nil {
		return result, err
	}
	for _, operation := range queued {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		var succeeded bool
		switch operation.GetOperationType() {
		case "tombstone":
			succeeded, err = a.runTombstoneOperation(ctx, store, operation)
		case "restore", "prewarm":
			succeeded, err = a.runRestoreOperation(ctx, store, operation)
		default:
			result.Skipped++
			continue
		}
		if err != nil {
			return result, err
		}
		if succeeded {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func (a *Application) runTombstoneOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running tombstone operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	err := a.applyTombstoneOperation(ctx, running, now)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "tombstone operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_TOMBSTONE_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(len(finished.GetTargets())),
		WorkUnitsCompleted: uint64(len(finished.GetTargets())),
		Message:            "tombstone operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runRestoreOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running " + operation.GetOperationType() + " operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	workUnits, err := a.applyRestoreOperation(ctx, running, now)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: operation.GetOperationType() + " operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_" + operationErrorPrefix(operation.GetOperationType()) + "_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(workUnits),
		WorkUnitsCompleted: uint64(workUnits),
		Message:            operation.GetOperationType() + " operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) applyRestoreOperation(ctx context.Context, operation *adminv1.Operation, now time.Time) (int, error) {
	if operation.GetDryRun() {
		return len(operation.GetTargets()), nil
	}
	blockIDs, documents, err := a.restoreTargets(operation)
	if err != nil {
		return 0, err
	}
	for blockID := range blockIDs {
		if err := a.restoreBlockFromBackend(ctx, blockID); err != nil {
			return 0, err
		}
	}
	for _, doc := range documents {
		if err := a.authority.UpdateDocumentRestoreState(
			ctx,
			doc,
			metastore.RestoreStateHot,
			operation.GetOperationType()+" operation restored local bytes",
			stableCommandID(operation.GetOperationType()+"-operation", operation.GetOperationId(), doc.TenantID, doc.TransactionID, doc.DocumentName),
			now,
		); err != nil {
			return 0, err
		}
	}
	return len(blockIDs), nil
}

func (a *Application) restoreTargets(operation *adminv1.Operation) (map[string]bool, []identity.Document, error) {
	blockIDs := make(map[string]bool)
	documentsByKey := make(map[identity.Document]bool)
	var documents []identity.Document
	addDocument := func(document metastore.Document) {
		blockIDs[document.Location.BlockID] = true
		if !documentsByKey[document.Identity] {
			documentsByKey[document.Identity] = true
			documents = append(documents, document.Identity)
		}
	}

	for _, target := range operation.GetTargets() {
		switch typed := target.GetTarget().(type) {
		case *adminv1.Target_Document:
			document, err := a.metadata.HeadDocument(adminDocumentIdentity(typed.Document))
			if err != nil {
				return nil, nil, err
			}
			addDocument(document)
		case *adminv1.Target_Transaction:
			docs, err := a.metadata.FindDocuments(identity.Transaction{
				TenantID:      typed.Transaction.GetTenantId(),
				TransactionID: typed.Transaction.GetTransactionId(),
			}, metastore.DocumentFilter{})
			if err != nil {
				return nil, nil, err
			}
			if len(docs) == 0 {
				return nil, nil, metastore.ErrNotFound
			}
			for _, document := range docs {
				addDocument(document)
			}
		case *adminv1.Target_Block:
			if typed.Block.GetShardId() != "local" {
				return nil, nil, metastore.ErrNotFound
			}
			docs, err := a.metadata.ListBlockDocuments(typed.Block.GetBlockId())
			if err != nil {
				return nil, nil, err
			}
			if len(docs) == 0 {
				return nil, nil, metastore.ErrNotFound
			}
			for _, document := range docs {
				addDocument(document)
			}
		default:
			return nil, nil, fmt.Errorf("localstorage: unsupported %s target %T", operation.GetOperationType(), target.GetTarget())
		}
	}
	return blockIDs, documents, nil
}

func (a *Application) restoreBlockFromBackend(ctx context.Context, blockID string) error {
	if a.backendStore == nil {
		return fmt.Errorf("localstorage: backend store is not configured")
	}
	intent, err := a.metadata.GetUploadIntent(blockID)
	if err != nil {
		return err
	}
	if intent.State != metastore.UploadStateUploaded {
		return fmt.Errorf("localstorage: block %s is not uploaded", blockID)
	}
	object, err := a.backendStore.HeadObject(ctx, intent.BackendObjectKey)
	if err != nil {
		return err
	}
	installed, err := a.blocks.EnsureSealedBlock(ctx, blockID, object.Length, object.SHA256)
	if err != nil {
		return err
	}
	if installed {
		return nil
	}
	reader, writer := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := a.backendStore.ReadObjectRange(ctx, intent.BackendObjectKey, backend.Range{}, writer)
		_ = writer.CloseWithError(err)
		errc <- err
	}()
	installErr := a.blocks.InstallSealedBlock(ctx, blockID, object.Length, object.SHA256, reader)
	_ = reader.Close()
	readErr := <-errc
	if installErr != nil {
		return installErr
	}
	return readErr
}

func operationErrorPrefix(operationType string) string {
	switch operationType {
	case "prewarm":
		return "PREWARM"
	default:
		return "RESTORE"
	}
}

func (a *Application) applyTombstoneOperation(ctx context.Context, operation *adminv1.Operation, tombstonedAt time.Time) error {
	if operation.GetDryRun() {
		return nil
	}
	for _, target := range operation.GetTargets() {
		switch typed := target.GetTarget().(type) {
		case *adminv1.Target_Document:
			if err := a.tombstoneDocument(ctx, adminDocumentIdentity(typed.Document), tombstonedAt, operation.GetOperationId()); err != nil {
				return err
			}
		case *adminv1.Target_Transaction:
			docs, err := a.metadata.FindDocuments(identity.Transaction{
				TenantID:      typed.Transaction.GetTenantId(),
				TransactionID: typed.Transaction.GetTransactionId(),
			}, metastore.DocumentFilter{})
			if err != nil {
				return err
			}
			if len(docs) == 0 {
				return metastore.ErrNotFound
			}
			for _, doc := range docs {
				if err := a.tombstoneDocument(ctx, doc.Identity, tombstonedAt, operation.GetOperationId()); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("localstorage: unsupported tombstone target %T", target.GetTarget())
		}
	}
	return nil
}

func (a *Application) tombstoneDocument(ctx context.Context, doc identity.Document, tombstonedAt time.Time, operationID string) error {
	return a.authority.TombstoneDocument(
		ctx,
		doc,
		tombstonedAt,
		operationID,
		stableCommandID("tombstone-operation", operationID, doc.TenantID, doc.TransactionID, doc.DocumentName),
	)
}

func adminDocumentIdentity(doc *adminv1.DocumentTarget) identity.Document {
	if doc == nil {
		return identity.Document{}
	}
	return identity.Document{
		TenantID:      doc.GetTenantId(),
		TransactionID: doc.GetTransactionId(),
		DocumentName:  doc.GetDocumentName(),
	}
}

func cloneOperation(operation *adminv1.Operation) *adminv1.Operation {
	if operation == nil {
		return nil
	}
	return proto.Clone(operation).(*adminv1.Operation)
}
