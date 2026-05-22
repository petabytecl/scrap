package localstorage

import (
	"context"
	"fmt"
	"time"

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
		if operation.GetOperationType() != "tombstone" {
			result.Skipped++
			continue
		}
		succeeded, err := a.runTombstoneOperation(ctx, store, operation)
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
