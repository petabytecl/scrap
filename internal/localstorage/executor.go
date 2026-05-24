package localstorage

import (
	"context"

	"github.com/petabytecl/scrap/internal/operations"
)

type OperationExecution interface {
	RunQueuedOperationsOnce(context.Context, *operations.Store) (OperationRunResult, error)
	RecoverInterruptedOperations(context.Context, *operations.Store) (operations.RecoveryResult, error)
}

type OperationExecutor struct {
	*Application
}
