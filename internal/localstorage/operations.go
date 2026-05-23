package localstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/storageformat"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type OperationRunResult struct {
	Scanned   int
	Skipped   int
	Succeeded int
	Failed    int
}

var errDrainUnsafe = errors.New("storage member is not safe to drain")

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
		case "rewrap":
			succeeded, err = a.runRewrapOperation(ctx, store, operation)
		case "repair":
			succeeded, err = a.runRepairOperation(ctx, store, operation)
		case "scrub":
			succeeded, err = a.runScrubOperation(ctx, store, operation)
		case "drain":
			succeeded, err = a.runDrainOperation(ctx, store, operation)
		case "metadata-restore":
			succeeded, err = a.runMetadataRestoreOperation(ctx, store, operation)
		case "copy-verify":
			succeeded, err = a.runCopyVerifyOperation(ctx, store, operation)
		case "dr-drill":
			succeeded, err = a.runDRDrillOperation(ctx, store, operation)
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

type scrubSummary struct {
	DocumentsScanned   int
	RepairQueued       int
	SkippedUnavailable int
}

func (a *Application) runScrubOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running scrub operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	summary, err := a.applyScrubOperation(ctx, running, now)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "scrub operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_SCRUB_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	message := "scrub operation succeeded"
	if operation.GetDryRun() {
		message = "scrub dry-run succeeded"
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(summary.DocumentsScanned),
		WorkUnitsCompleted: uint64(summary.DocumentsScanned),
		Message:            message,
		Counters: map[string]string{
			"documents_scanned":   fmt.Sprintf("%d", summary.DocumentsScanned),
			"repair_queued":       fmt.Sprintf("%d", summary.RepairQueued),
			"skipped_unavailable": fmt.Sprintf("%d", summary.SkippedUnavailable),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runRewrapOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running rewrap operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	rewrapped, skipped, err := a.applyRewrapOperation(ctx, running)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "rewrap operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_REWRAP_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(rewrapped + skipped),
		WorkUnitsCompleted: uint64(rewrapped + skipped),
		Message:            "rewrap operation succeeded",
		Counters: map[string]string{
			"envelopes_rewrapped": fmt.Sprintf("%d", rewrapped),
			"envelopes_skipped":   fmt.Sprintf("%d", skipped),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(rewrapAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) applyRewrapOperation(ctx context.Context, operation *adminv1.Operation) (int, int, error) {
	if operation.GetDryRun() {
		return len(operation.GetTargets()), 0, nil
	}
	if a.backendStore == nil {
		return 0, 0, fmt.Errorf("localstorage: backend store is not configured")
	}
	mutable, ok := a.backendStore.(backend.MutableStore)
	if !ok {
		return 0, 0, fmt.Errorf("localstorage: backend store does not support mutable envelope objects")
	}
	if a.envelopeTransit == nil {
		return 0, 0, fmt.Errorf("%w: transit client is required for rewrap", cryptoenv.ErrUnavailable)
	}
	destinationKeyID := operation.GetMetadata()["scrap.destination_key_id"]
	if destinationKeyID == "" {
		destinationKeyID = operation.GetMetadata()["destination_key_id"]
	}
	if destinationKeyID == "" {
		return 0, 0, fmt.Errorf("localstorage: rewrap destination key id is required")
	}
	blockIDs, _, err := a.restoreTargets(operation)
	if err != nil {
		return 0, 0, err
	}
	rewrapped := 0
	skipped := 0
	rewrappedAt := operation.GetRequestedAt().AsTime()
	if rewrappedAt.IsZero() {
		rewrappedAt = a.now()
	}
	for blockID := range blockIDs {
		changed, err := a.rewrapBlockEnvelope(ctx, mutable, blockID, destinationKeyID, rewrappedAt)
		if err != nil {
			return rewrapped, skipped, err
		}
		if changed {
			rewrapped++
		} else {
			skipped++
		}
	}
	return rewrapped, skipped, nil
}

func (a *Application) rewrapBlockEnvelope(ctx context.Context, store backend.MutableStore, blockID string, destinationKeyID string, rewrappedAt time.Time) (bool, error) {
	intent, err := a.metadata.GetUploadIntent(blockID)
	if err != nil {
		return false, err
	}
	if intent.EnvelopeObjectKey == "" {
		return false, nil
	}
	var data bytes.Buffer
	if err := store.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &data); err != nil {
		return false, err
	}
	envelope, err := storageformat.UnmarshalEnvelopeRecord(data.Bytes())
	if err != nil {
		return false, fmt.Errorf("%w: backend envelope %s is invalid: %w", backend.ErrChecksumMismatch, intent.EnvelopeObjectKey, err)
	}
	if envelope.GetAeadAlgorithm() == "none" {
		return false, nil
	}
	rewrapped, err := cryptoenv.RewrapEnvelopeRecord(ctx, a.envelopeTransit, envelope, destinationKeyID, rewrappedAt)
	if err != nil {
		return false, err
	}
	payload, err := storageformat.MarshalEnvelopeRecord(rewrapped)
	if err != nil {
		return false, err
	}
	if _, err := store.PutMutableObject(ctx, intent.EnvelopeObjectKey, bytes.NewReader(payload)); err != nil {
		return false, err
	}
	return true, nil
}

func rewrapAuditEvent(operation *adminv1.Operation) *adminv1.AuditEvent {
	occurredAt := operation.GetRequestedAt()
	if occurredAt == nil {
		occurredAt = operation.GetFinishedAt()
	}
	return &adminv1.AuditEvent{
		EventId:       stableCommandID("audit-rewrap-completed", operation.GetOperationId()),
		EventType:     "rewrap_completed",
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: operation.GetRequestedByIdentity(),
		OccurredAt:    occurredAt,
		Targets:       cloneOperationTargets(operation.GetTargets()),
		Metadata:      cloneTags(operation.GetMetadata()),
	}
}

func (a *Application) runRepairOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running repair operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	workUnits, err := a.applyRepairOperation(ctx, running, now)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "repair operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_REPAIR_FAILED",
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
		Message:            "repair operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runDisasterRecoveryOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running " + operation.GetOperationType() + " operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	readiness, err := a.GetRecoveryReadiness(ctx)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if readiness != nil {
		finished.Warnings = append(finished.Warnings, cloneWarnings(readiness.GetWarnings())...)
	}
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: operation.GetOperationType() + " operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_DR_READINESS_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}
	workUnits := len(finished.GetTargets())
	if operation.GetDryRun() {
		finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
		finished.Progress = &adminv1.OperationProgress{
			WorkUnitsTotal:     uint64(workUnits),
			WorkUnitsCompleted: uint64(workUnits),
			Message:            operation.GetOperationType() + " dry-run succeeded",
		}
		if err := store.Put(finished); err != nil {
			return false, err
		}
		return true, nil
	}
	if !readiness.GetReady() {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: operation.GetOperationType() + " operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_DR_NOT_READY",
			Message: "recovery readiness gates are not satisfied",
		}
		if err := store.Put(finished); err != nil {
			return false, err
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

func (a *Application) runCopyVerifyOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running copy-verify operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	checkpoint, err := a.verifyCurrentCheckpoint(ctx)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "copy-verify operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_DR_COPY_VERIFY_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	message := "copy-verify operation succeeded"
	if operation.GetDryRun() {
		message = "copy-verify dry-run succeeded"
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(checkpoint.VerifiedObjects),
		WorkUnitsCompleted: uint64(checkpoint.VerifiedObjects),
		Message:            message,
		Counters: map[string]string{
			"generation":       fmt.Sprintf("%d", checkpoint.Pointer.GetGeneration()),
			"manifest_id":      checkpoint.Manifest.GetManifestId(),
			"verified_objects": fmt.Sprintf("%d", checkpoint.VerifiedObjects),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runMetadataRestoreOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running metadata-restore operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	restore, err := a.RestorePublishedMetadataCheckpoint(ctx, !operation.GetDryRun())
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "metadata-restore operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_DR_METADATA_RESTORE_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	message := "metadata-restore operation succeeded"
	if operation.GetDryRun() {
		message = "metadata-restore dry-run succeeded"
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(restore.Documents + restore.Transactions + restore.UploadIntents),
		WorkUnitsCompleted: uint64(restore.Documents + restore.Transactions + restore.UploadIntents),
		Message:            message,
		Counters: map[string]string{
			"blocks_restored": fmt.Sprintf("%d", restore.BlocksRestored),
			"documents":       fmt.Sprintf("%d", restore.Documents),
			"snapshots":       fmt.Sprintf("%d", restore.Snapshots),
			"tombstones":      fmt.Sprintf("%d", restore.Tombstones),
			"transactions":    fmt.Sprintf("%d", restore.Transactions),
			"upload_intents":  fmt.Sprintf("%d", restore.UploadIntents),
			"verified":        fmt.Sprintf("%d", restore.Verified),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runDRDrillOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running dr-drill operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	drill, err := a.RunDRDrill(ctx, !operation.GetDryRun())
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "dr-drill operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_DR_DRILL_FAILED",
			Message: err.Error(),
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	message := "dr-drill operation succeeded"
	if operation.GetDryRun() {
		message = "dr-drill dry-run succeeded"
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     uint64(drill.Documents + drill.Transactions + drill.UploadIntents),
		WorkUnitsCompleted: uint64(drill.Documents + drill.Transactions + drill.UploadIntents),
		Message:            message,
		Counters: map[string]string{
			"blocks_restored": fmt.Sprintf("%d", drill.BlocksRestored),
			"documents":       fmt.Sprintf("%d", drill.Documents),
			"snapshots":       fmt.Sprintf("%d", drill.Snapshots),
			"tombstones":      fmt.Sprintf("%d", drill.Tombstones),
			"transactions":    fmt.Sprintf("%d", drill.Transactions),
			"upload_intents":  fmt.Sprintf("%d", drill.UploadIntents),
			"verified":        fmt.Sprintf("%d", drill.Verified),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) verifyCurrentCheckpoint(ctx context.Context) (published.CheckpointVerification, error) {
	if a.backendStore == nil {
		return published.CheckpointVerification{}, fmt.Errorf("localstorage: backend store is not configured")
	}
	return published.VerifyCurrentCheckpoint(ctx, a.backendStore, localPublishedCellID)
}

func (a *Application) runDrainOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running := cloneOperation(operation)
	now := a.now()
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.Progress = &adminv1.OperationProgress{Message: "running drain operation"}
	if err := store.Put(running); err != nil {
		return false, err
	}

	workUnits, warnings, err := a.applyDrainOperation(ctx, running)
	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	finished.Warnings = append(finished.Warnings, warnings...)
	if err != nil {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "drain operation failed"}
		code := "SCRAP_DRAIN_FAILED"
		if errors.Is(err, errDrainUnsafe) {
			code = "SCRAP_DRAIN_UNSAFE"
		}
		finished.LastError = &adminv1.OperationError{
			Code:    code,
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
		Message:            "drain operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) applyDrainOperation(ctx context.Context, operation *adminv1.Operation) (int, []*adminv1.OperationWarning, error) {
	memberID, err := drainOperationMember(operation)
	if err != nil {
		return 0, nil, err
	}
	safety, err := a.GetEvictionSafety(ctx, memberID)
	if err != nil {
		return 0, nil, err
	}
	warnings := cloneWarnings(safety.GetWarnings())
	if operation.GetDryRun() {
		return 1, warnings, nil
	}
	if !safety.GetSafeToEvict() {
		return 1, warnings, fmt.Errorf("%w: storage member %q has no safe eviction path", errDrainUnsafe, memberID)
	}
	if err := a.updateLocalMemberState(func(state *localMemberState) {
		state.Draining = true
		state.Cordoned = true
	}); err != nil {
		return 0, warnings, err
	}
	return 1, warnings, nil
}

func drainOperationMember(operation *adminv1.Operation) (string, error) {
	if len(operation.GetTargets()) != 1 {
		return "", fmt.Errorf("localstorage: drain operation requires exactly one storage member target")
	}
	target := operation.GetTargets()[0]
	if target == nil {
		return "", fmt.Errorf("localstorage: drain operation requires a storage member target")
	}
	typed, ok := target.GetTarget().(*adminv1.Target_StorageMember)
	if !ok {
		return "", fmt.Errorf("localstorage: unsupported drain target %T", target.GetTarget())
	}
	memberID := typed.StorageMember.GetStorageMemberId()
	if memberID != "local" {
		return "", metastore.ErrNotFound
	}
	return memberID, nil
}

func (a *Application) applyScrubOperation(ctx context.Context, operation *adminv1.Operation, now time.Time) (scrubSummary, error) {
	documents, err := a.scrubTargets(operation)
	if err != nil {
		return scrubSummary{}, err
	}
	summary := scrubSummary{DocumentsScanned: len(documents)}
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if document.RestoreState != metastore.RestoreStateHot || document.Availability != metastore.AvailabilityHot {
			summary.SkippedUnavailable++
			continue
		}
		length := document.Length
		err := a.blocks.VerifyRange(document.Location, 0, &length)
		if err == nil {
			continue
		}
		if !isIntegrityFailure(err) {
			return summary, err
		}
		summary.RepairQueued++
		if operation.GetDryRun() {
			continue
		}
		if err := a.recordDocumentRepairState(ctx, document, integrityEvidenceID(document), true, now); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func (a *Application) scrubTargets(operation *adminv1.Operation) ([]metastore.Document, error) {
	var documents []metastore.Document
	seen := make(map[identity.Document]bool)
	add := func(document metastore.Document) {
		if seen[document.Identity] {
			return
		}
		seen[document.Identity] = true
		documents = append(documents, document)
	}
	addAll := func() error {
		all, err := a.metadata.ListDocuments(metastore.DocumentFilter{})
		if err != nil {
			return err
		}
		for _, document := range all {
			add(document)
		}
		return nil
	}
	if len(operation.GetTargets()) == 0 {
		if err := addAll(); err != nil {
			return nil, err
		}
		return documents, nil
	}
	for _, target := range operation.GetTargets() {
		switch typed := target.GetTarget().(type) {
		case *adminv1.Target_Document:
			document, err := a.metadata.HeadDocument(adminDocumentIdentity(typed.Document))
			if err != nil {
				return nil, err
			}
			add(document)
		case *adminv1.Target_Transaction:
			docs, err := a.metadata.FindDocuments(identity.Transaction{
				TenantID:      typed.Transaction.GetTenantId(),
				TransactionID: typed.Transaction.GetTransactionId(),
			}, metastore.DocumentFilter{})
			if err != nil {
				return nil, err
			}
			if len(docs) == 0 {
				return nil, metastore.ErrNotFound
			}
			for _, document := range docs {
				add(document)
			}
		case *adminv1.Target_Block:
			if typed.Block.GetShardId() != "local" {
				return nil, metastore.ErrNotFound
			}
			docs, err := a.metadata.ListBlockDocuments(typed.Block.GetBlockId())
			if err != nil {
				return nil, err
			}
			if len(docs) == 0 {
				return nil, metastore.ErrNotFound
			}
			for _, document := range docs {
				add(document)
			}
		case *adminv1.Target_Shard:
			if typed.Shard.GetShardId() != "local" {
				return nil, metastore.ErrNotFound
			}
			if err := addAll(); err != nil {
				return nil, err
			}
		case *adminv1.Target_StorageMember:
			if typed.StorageMember.GetStorageMemberId() != "local" {
				return nil, metastore.ErrNotFound
			}
			if err := addAll(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("localstorage: unsupported scrub target %T", target.GetTarget())
		}
	}
	return documents, nil
}

func (a *Application) applyRepairOperation(ctx context.Context, operation *adminv1.Operation, now time.Time) (int, error) {
	if operation.GetDryRun() {
		return len(operation.GetTargets()), nil
	}
	repairs, err := a.repairTargets(operation)
	if err != nil {
		return 0, err
	}
	restoredBlocks := make(map[string]bool)
	for _, repair := range repairs {
		document, err := a.metadata.HeadDocument(repair.Identity)
		if err != nil {
			return 0, err
		}
		if !restoredBlocks[document.Location.BlockID] {
			repairedFromPeer, err := a.repairDocumentFromVerifiedPeer(ctx, document, now)
			if err != nil {
				return 0, err
			}
			if !repairedFromPeer {
				if err := a.replaceBlockFromBackend(ctx, document.Location.BlockID); err != nil {
					return 0, err
				}
				restoredBlocks[document.Location.BlockID] = true
			}
		}
		if err := a.recordDocumentRepairState(ctx, document, repair.IncidentID, false, now); err != nil {
			return 0, err
		}
	}
	return len(repairs), nil
}

func (a *Application) repairTargets(operation *adminv1.Operation) ([]metastore.RepairState, error) {
	states, err := a.metadata.ListRepairStates()
	if err != nil {
		return nil, err
	}
	var repairs []metastore.RepairState
	seen := make(map[string]bool)
	add := func(state metastore.RepairState) {
		if !state.Quarantined || !isLocalRepairState(state) {
			return
		}
		key := state.Identity.TenantID + "\x00" + state.Identity.TransactionID + "\x00" + state.Identity.DocumentName + "\x00" + state.IncidentID
		if seen[key] {
			return
		}
		seen[key] = true
		repairs = append(repairs, state)
	}
	for _, target := range operation.GetTargets() {
		switch typed := target.GetTarget().(type) {
		case *adminv1.Target_Document:
			doc := adminDocumentIdentity(typed.Document)
			for _, state := range states {
				if state.Identity == doc {
					add(state)
				}
			}
		case *adminv1.Target_Block:
			if typed.Block.GetShardId() != "local" {
				return nil, metastore.ErrNotFound
			}
			for _, state := range states {
				if !state.Quarantined {
					continue
				}
				document, err := a.metadata.HeadDocument(state.Identity)
				if err != nil {
					return nil, err
				}
				if document.Location.BlockID == typed.Block.GetBlockId() {
					add(state)
				}
			}
		case *adminv1.Target_Shard:
			if typed.Shard.GetShardId() != "local" {
				return nil, metastore.ErrNotFound
			}
			for _, state := range states {
				add(state)
			}
		case *adminv1.Target_StorageMember:
			if typed.StorageMember.GetStorageMemberId() != "local" {
				return nil, metastore.ErrNotFound
			}
			for _, state := range states {
				add(state)
			}
		default:
			return nil, fmt.Errorf("localstorage: unsupported repair target %T", target.GetTarget())
		}
	}
	if len(repairs) == 0 {
		return nil, metastore.ErrNotFound
	}
	return repairs, nil
}

func isLocalRepairState(state metastore.RepairState) bool {
	return strings.HasPrefix(state.PhysicalRef, "local/")
}

func (a *Application) repairDocumentFromVerifiedPeer(ctx context.Context, document metastore.Document, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(document.Location.Replicas) == 0 || len(a.peerRepairSources) == 0 {
		return false, nil
	}
	prepared := replication.PreparedDocumentFromMetadata(document)
	for _, replica := range document.Location.Replicas {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		source := a.peerRepairSources[replica.MemberID]
		if source == nil {
			continue
		}
		quarantined, err := a.peerRepairSourceQuarantined(document, replica)
		if err != nil {
			return false, err
		}
		if quarantined {
			continue
		}
		var data bytes.Buffer
		err = source.ReadReplica(ctx, replica, &data)
		if err == nil {
			err = replication.ValidatePreparedBytes(prepared, data.Bytes())
		}
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return false, contextErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false, err
			}
			if !isPeerIntegrityFailure(err) {
				continue
			}
			if recordErr := a.recordPeerRepairState(ctx, document, replica, true, now); recordErr != nil {
				return false, recordErr
			}
			continue
		}
		if err := a.blocks.InstallVerifiedRange(ctx, document.Location, document.StoredSHA256, bytes.NewReader(data.Bytes())); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (a *Application) peerRepairSourceQuarantined(document metastore.Document, replica blockstore.ReplicaRef) (bool, error) {
	state, err := a.metadata.GetRepairState(document.Identity, peerIntegrityEvidenceID(document, replica))
	if errors.Is(err, metastore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.Quarantined, nil
}

func isPeerIntegrityFailure(err error) bool {
	return isIntegrityFailure(err) || errors.Is(err, replication.ErrTransferMismatch)
}

func (a *Application) replaceBlockFromBackend(ctx context.Context, blockID string) error {
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
	if _, err := a.backendStore.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		return err
	}
	if err := os.Remove(a.blocks.BlockPath(blockID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(a.blocks.SealPath(blockID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return a.restoreBlockFromBackend(ctx, blockID)
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
	case "rewrap":
		return "REWRAP"
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

func cloneWarnings(warnings []*adminv1.OperationWarning) []*adminv1.OperationWarning {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]*adminv1.OperationWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, proto.Clone(warning).(*adminv1.OperationWarning))
	}
	return out
}

func cloneOperationTargets(targets []*adminv1.Target) []*adminv1.Target {
	if len(targets) == 0 {
		return nil
	}
	out := make([]*adminv1.Target, 0, len(targets))
	for _, target := range targets {
		out = append(out, proto.Clone(target).(*adminv1.Target))
	}
	return out
}
