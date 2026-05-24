package localstorage

import (
	"context"

	"github.com/petabytecl/scrap/internal/storageapp"
)

type TransactionCoordinator interface {
	WriteDocument(context.Context, storageapp.WriteDocumentInit, storageapp.ChunkReader) (storageapp.WriteDocumentResult, error)
	CompleteTransaction(context.Context, storageapp.CompleteTransactionRequest) (storageapp.TransactionState, error)
	GetTransaction(context.Context, storageapp.GetTransactionRequest) (storageapp.TransactionState, error)
}

type documentApplication struct {
	*Application
}

type transactionCoordinator struct {
	*Application
}
