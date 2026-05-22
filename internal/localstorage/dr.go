package localstorage

import (
	"bytes"
	"context"
	"errors"

	"github.com/petabytecl/scrap/internal/backend"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/published"
)

func (a *Application) GetRecoveryReadiness(ctx context.Context) (*adminv1.RecoveryReadiness, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warnings := make([]*adminv1.OperationWarning, 0, 2)
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

	pointer, err := a.readCurrentMetadataPointer(ctx)
	switch {
	case err == nil:
		readiness.LatestRestorableCheckpointAt = pointer.GetPublishedAt()
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_DR_RESTORE_IMPLEMENTATION_MISSING",
			Message: "published metadata checkpoint exists, but local metadata restore and copy verification are not implemented",
		})
	case errors.Is(err, backend.ErrNotFound):
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

func (a *Application) readCurrentMetadataPointer(ctx context.Context) (*publishedv1.CurrentPointer, error) {
	key, err := published.CurrentPointerObjectKey(localPublishedCellID)
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, key, backend.Range{}, &data); err != nil {
		return nil, err
	}
	return published.UnmarshalCurrentPointer(data.Bytes())
}
