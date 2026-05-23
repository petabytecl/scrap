package localstorage

import (
	"context"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (a *Application) GetRepairQueue(ctx context.Context, shardID string) ([]*adminv1.RepairQueueItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if shardID != "" && shardID != "local" {
		return nil, nil
	}
	states, err := a.metadata.ListRepairStates()
	if err != nil {
		return nil, err
	}
	items := make([]*adminv1.RepairQueueItem, 0, len(states))
	for _, state := range states {
		if !state.Quarantined {
			continue
		}
		items = append(items, repairQueueItem(state))
	}
	return items, nil
}

func repairQueueItem(state metastore.RepairState) *adminv1.RepairQueueItem {
	return &adminv1.RepairQueueItem{
		Target: &adminv1.Target{
			Target: &adminv1.Target_Document{
				Document: &adminv1.DocumentTarget{
					TenantId:      state.Identity.TenantID,
					TransactionId: state.Identity.TransactionID,
					DocumentName:  state.Identity.DocumentName,
				},
			},
		},
		Reason:     repairReason(state),
		DetectedAt: timestamppb.New(state.UpdatedAt),
	}
}

func repairReason(state metastore.RepairState) string {
	if state.Quarantined {
		return "quarantined byte source " + state.PhysicalRef
	}
	return "repair required for byte source " + state.PhysicalRef
}
