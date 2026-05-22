package localstorage

import (
	"context"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
)

func (a *Application) GetRecoveryReadiness(ctx context.Context) (*adminv1.RecoveryReadiness, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warnings := []*adminv1.OperationWarning{
		{
			Code:    "SCRAP_DR_METADATA_EXPORT_MISSING",
			Message: "local non-production mode has no published metadata checkpoint or tail to restore",
		},
	}
	if a.backendStore == nil {
		warnings = append(warnings, &adminv1.OperationWarning{
			Code:    "SCRAP_BACKEND_NOT_CONFIGURED",
			Message: "local non-production mode has no backend store configured for recovery artifacts",
		})
	}
	return &adminv1.RecoveryReadiness{
		Ready:    false,
		Warnings: warnings,
	}, nil
}
