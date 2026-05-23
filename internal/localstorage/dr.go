package localstorage

import (
	"context"
	"errors"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/published"
)

func (a *Application) GetRecoveryReadiness(ctx context.Context) (*adminv1.RecoveryReadiness, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warnings := make([]*adminv1.OperationWarning, 0, 3)
	readiness := &adminv1.RecoveryReadiness{Ready: false}
	if a.backendStore == nil {
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_METADATA_EXPORT_MISSING",
			Message: "local non-production mode has no published metadata checkpoint or tail to restore",
		})
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_BACKEND_NOT_CONFIGURED",
			Message: "local non-production mode has no backend store configured for recovery artifacts",
		})
		readiness.Warnings = warnings
		return readiness, nil
	}

	checkpoint, err := published.VerifyCurrentCheckpoint(ctx, a.backendStore, localPublishedCellID)
	switch {
	case err == nil:
		readiness.Ready = true
		readiness.LatestRestorableCheckpointAt = checkpoint.Pointer.GetPublishedAt()
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_NON_PRODUCTION_MODE",
			Message: "local non-production recovery artifacts are ready, but production readiness gates are separate",
		})
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_MEASURED_EVIDENCE_ONLY",
			Message: "recovery reports contain measured verification evidence and do not promise formal RTO or RPO",
		})
	case errors.Is(err, published.ErrCurrentPointerNotFound):
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_METADATA_EXPORT_MISSING",
			Message: "local non-production mode has no published metadata checkpoint or tail to restore",
		})
	default:
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_METADATA_EXPORT_INVALID",
			Message: "local non-production mode has an unreadable published metadata checkpoint: " + err.Error(),
		})
	}
	readiness.Warnings = warnings
	return readiness, nil
}
