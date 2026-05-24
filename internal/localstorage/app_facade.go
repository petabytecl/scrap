package localstorage

import (
	"context"
	"time"

	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/storageapp"
)

func (a *Application) WriteDocument(ctx context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (storageapp.WriteDocumentResult, error) {
	return a.transactions.WriteDocument(ctx, init, chunks)
}

func (a *documentApplication) WriteDocument(ctx context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (storageapp.WriteDocumentResult, error) {
	return a.transactions.WriteDocument(ctx, init, chunks)
}

func (a *Application) HeadDocument(ctx context.Context, req storageapp.HeadDocumentRequest) (storageapp.DocumentMetadata, error) {
	return a.documents.HeadDocument(ctx, req)
}

func (a *Application) ReadDocument(ctx context.Context, req storageapp.ReadDocumentRequest, sender storageapp.ReadDocumentSender) error {
	return a.documents.ReadDocument(ctx, req, sender)
}

func (a *Application) FindDocuments(ctx context.Context, req storageapp.FindDocumentsRequest) (storageapp.FindDocumentsResult, error) {
	return a.documents.FindDocuments(ctx, req)
}

func (a *Application) CompleteTransaction(ctx context.Context, req storageapp.CompleteTransactionRequest) (storageapp.TransactionState, error) {
	return a.transactions.CompleteTransaction(ctx, req)
}

func (a *Application) GetTransaction(ctx context.Context, req storageapp.GetTransactionRequest) (storageapp.TransactionState, error) {
	return a.transactions.GetTransaction(ctx, req)
}

func (a *Application) RunQueuedOperationsOnce(ctx context.Context, store *operations.Store) (OperationRunResult, error) {
	return a.operationExecutor.RunQueuedOperationsOnce(ctx, store)
}

func (a *Application) RecoverInterruptedOperations(ctx context.Context, store *operations.Store) (operations.RecoveryResult, error) {
	return a.operationExecutor.RecoverInterruptedOperations(ctx, store)
}

func (a *Application) replaceBlockFromBackend(ctx context.Context, blockID string) error {
	return a.operationExecutor.replaceBlockFromBackend(ctx, blockID)
}

func (a *Application) repairDocumentFromVerifiedPeer(ctx context.Context, document metastore.Document, now time.Time) (bool, error) {
	return a.operationExecutor.repairDocumentFromVerifiedPeer(ctx, document, now)
}
