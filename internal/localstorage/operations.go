package localstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageformat"
)

type OperationRunResult struct {
	Scanned   int
	Skipped   int
	Pending   int
	Succeeded int
	Failed    int
}

var errDrainUnsafe = errors.New("storage member is not safe to drain")

const (
	operationLaneMetadata         = "scrap.operation_lane"
	backendLaneMetadata           = "scrap.backend_lane"
	restoreTriggerMetadata        = "scrap.restore_trigger"
	restoreTriggerRead            = "read"
	operationLaneInteractive      = "interactive-restore"
	operationLanePlannedPrewarm   = "planned-prewarm"
	operationLaneRestoreFallback  = "restore"
	operationEventRestoreQueued   = "restore_queued"
	operationEventRestoreComplete = "restore_completed"
	operationEventPrewarmComplete = "prewarm_completed"
)

func (a *Application) RunQueuedOperationsOnce(ctx context.Context, store *operations.Store) (OperationRunResult, error) {
	var result OperationRunResult
	if store == nil {
		return result, errors.New("localstorage: operation store is not configured")
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
		succeeded, supported, err := a.runQueuedOperation(ctx, store, operation)
		if err != nil {
			return result, err
		}
		if !supported {
			result.Skipped++
			continue
		}
		if err := recordQueuedOperationOutcome(store, operation, succeeded, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *Application) runQueuedOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, bool, error) {
	switch operation.GetOperationType() {
	case "tombstone":
		return runSupportedOperation(a.runTombstoneOperation(ctx, store, operation))
	case "restore", "prewarm":
		return runSupportedOperation(a.runRestoreOperation(ctx, store, operation))
	case "rewrap":
		return runSupportedOperation(a.runRewrapOperation(ctx, store, operation))
	case "repair":
		return runSupportedOperation(a.runRepairOperation(ctx, store, operation))
	case "scrub":
		return runSupportedOperation(a.runScrubOperation(ctx, store, operation))
	case "drain":
		return runSupportedOperation(a.runDrainOperation(ctx, store, operation))
	case "capacity-override":
		return runSupportedOperation(a.runCapacityOverrideOperation(ctx, store, operation))
	case "metadata-restore":
		return runSupportedOperation(a.runMetadataRestoreOperation(ctx, store, operation))
	case "copy-verify":
		return runSupportedOperation(a.runCopyVerifyOperation(ctx, store, operation))
	case "dr-drill":
		return runSupportedOperation(a.runDRDrillOperation(ctx, store, operation))
	default:
		return false, false, nil
	}
}

func runSupportedOperation(succeeded bool, err error) (bool, bool, error) {
	return succeeded, true, err
}

func recordQueuedOperationOutcome(store *operations.Store, operation *adminv1.Operation, succeeded bool, result *OperationRunResult) error {
	if succeeded {
		result.Succeeded++
		return nil
	}
	queued, err := isOperationQueued(store, operation.GetOperationId())
	if err != nil {
		return err
	}
	if queued {
		result.Pending++
		return nil
	}
	result.Failed++
	return nil
}

func (a *Application) RecoverInterruptedOperations(ctx context.Context, store *operations.Store) (operations.RecoveryResult, error) {
	var result operations.RecoveryResult
	if store == nil {
		return result, errors.New("localstorage: operation store is not configured")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return store.RecoverInterrupted(a.now(), supportedOperationTypes())
}

func supportedOperationTypes() map[string]bool {
	return map[string]bool{
		"tombstone":         true,
		"restore":           true,
		"prewarm":           true,
		"rewrap":            true,
		"repair":            true,
		"scrub":             true,
		"drain":             true,
		"capacity-override": true,
		"metadata-restore":  true,
		"copy-verify":       true,
		"dr-drill":          true,
	}
}

func isOperationQueued(store *operations.Store, operationID string) (bool, error) {
	current, err := store.Get(operationID)
	if err != nil {
		return false, err
	}
	return current.GetState() == adminv1.OperationState_OPERATION_STATE_QUEUED, nil
}

func (a *Application) beginOperationAttempt(operation *adminv1.Operation, message string) (*adminv1.Operation, time.Time) {
	now := a.now()
	running := cloneOperation(operation)
	running.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	running.StartedAt = timestamppb.New(now)
	running.FinishedAt = nil
	running.LastError = nil
	running.Progress = &adminv1.OperationProgress{Message: message}
	return running, now
}

func (a *Application) runTombstoneOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, now := a.beginOperationAttempt(operation, "running tombstone operation")
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

	targets, err := operationWorkUnits("tombstone targets", len(finished.GetTargets()))
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     targets,
		WorkUnitsCompleted: targets,
		Message:            "tombstone operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
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
	running, now := a.beginOperationAttempt(operation, "running scrub operation")
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
	documentsScanned, err := safeconv.IntToUint64("documents scanned", summary.DocumentsScanned)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     documentsScanned,
		WorkUnitsCompleted: documentsScanned,
		Message:            message,
		Counters: map[string]string{
			"documents_scanned":   strconv.Itoa(summary.DocumentsScanned),
			"repair_queued":       strconv.Itoa(summary.RepairQueued),
			"skipped_unavailable": strconv.Itoa(summary.SkippedUnavailable),
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runRewrapOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, _ := a.beginOperationAttempt(operation, "running rewrap operation")
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

	workUnits, err := safeconv.IntToUint64("rewrap work units", rewrapped+skipped)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     workUnits,
		WorkUnitsCompleted: workUnits,
		Message:            "rewrap operation succeeded",
		Counters: map[string]string{
			"envelopes_rewrapped": strconv.Itoa(rewrapped),
			"envelopes_skipped":   strconv.Itoa(skipped),
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
		return 0, 0, errors.New("localstorage: backend store is not configured")
	}
	mutable, ok := a.backendStore.(backend.MutableStore)
	if !ok {
		return 0, 0, errors.New("localstorage: backend store does not support mutable envelope objects")
	}
	if a.envelopeTransit == nil {
		return 0, 0, fmt.Errorf("%w: transit client is required for rewrap", cryptoenv.ErrUnavailable)
	}
	destinationKeyID := operation.GetMetadata()["scrap.destination_key_id"]
	if destinationKeyID == "" {
		destinationKeyID = operation.GetMetadata()["destination_key_id"]
	}
	if destinationKeyID == "" {
		return 0, 0, errors.New("localstorage: rewrap destination key id is required")
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

func (a *Application) rewrapBlockEnvelope(ctx context.Context, store backend.MutableStore, blockID, destinationKeyID string, rewrappedAt time.Time) (bool, error) {
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
	if bytes.Equal(payload, data.Bytes()) {
		return false, nil
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
		Metadata:      sanitizeOperationAuditMetadata(operation.GetMetadata()),
	}
}

func (a *Application) runRepairOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, now := a.beginOperationAttempt(operation, "running repair operation")
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

	workUnitsCompleted, err := safeconv.IntToUint64("repair work units", workUnits)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     workUnitsCompleted,
		WorkUnitsCompleted: workUnitsCompleted,
		Message:            "repair operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runCapacityOverrideOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	running, _ := a.beginOperationAttempt(operation, "running capacity-override operation")
	if err := store.Put(running); err != nil {
		return false, err
	}

	finished := cloneOperation(running)
	finished.FinishedAt = timestamppb.New(a.now())
	capacityProfileID := finished.GetMetadata()["scrap.capacity_profile_id"]
	expiresAt := finished.GetMetadata()["scrap.capacity_override_expires_at"]
	reason := finished.GetMetadata()["scrap.capacity_override_reason"]
	if capacityProfileID == "" || expiresAt == "" || reason == "" {
		finished.State = adminv1.OperationState_OPERATION_STATE_FAILED
		finished.Progress = &adminv1.OperationProgress{Message: "capacity-override operation failed"}
		finished.LastError = &adminv1.OperationError{
			Code:    "SCRAP_CAPACITY_OVERRIDE_INVALID",
			Message: "capacity override requires capacity profile, expiry, and reason metadata",
		}
		if putErr := store.Put(finished); putErr != nil {
			return false, putErr
		}
		return false, nil
	}

	message := "capacity-override operation recorded"
	if operation.GetDryRun() {
		message = "capacity-override dry-run succeeded"
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Warnings = append(finished.Warnings, &adminv1.OperationWarning{
		Code:    "SCRAP_CAPACITY_OVERRIDE_RECORDED_ONLY",
		Message: "capacity override is recorded as operation evidence and does not force production write ACK mode",
	})
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     1,
		WorkUnitsCompleted: 1,
		Message:            message,
		Counters: map[string]string{
			"capacity_profile_id": capacityProfileID,
			"expires_at":          expiresAt,
			"reason":              reason,
		},
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runCopyVerifyOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, _ := a.beginOperationAttempt(operation, "running copy-verify operation")
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
	verifiedObjects, err := operationWorkUnits("verified objects", checkpoint.VerifiedObjects)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     verifiedObjects,
		WorkUnitsCompleted: verifiedObjects,
		Message:            message,
		Counters:           checkpointEvidenceCounters(checkpoint),
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runMetadataRestoreOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, _ := a.beginOperationAttempt(operation, "running metadata-restore operation")
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
	workUnits, err := operationWorkUnits("metadata restore work units", restore.Documents, restore.Transactions, restore.UploadIntents)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     workUnits,
		WorkUnitsCompleted: workUnits,
		Message:            message,
		Counters:           restoreEvidenceCounters(restore),
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) runDRDrillOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, _ := a.beginOperationAttempt(operation, "running dr-drill operation")
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
	workUnits, err := operationWorkUnits("dr drill work units", drill.Documents, drill.Transactions, drill.UploadIntents)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     workUnits,
		WorkUnitsCompleted: workUnits,
		Message:            message,
		Counters:           restoreEvidenceCounters(drill),
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) verifyCurrentCheckpoint(ctx context.Context) (published.CheckpointVerification, error) {
	if a.backendStore == nil {
		return published.CheckpointVerification{}, errors.New("localstorage: backend store is not configured")
	}
	return published.VerifyCurrentCheckpoint(ctx, a.backendStore, localPublishedCellID)
}

func checkpointEvidenceCounters(checkpoint published.CheckpointVerification) map[string]string {
	return map[string]string{
		"generation":                strconv.FormatUint(checkpoint.Pointer.GetGeneration(), 10),
		"manifest_id":               checkpoint.Manifest.GetManifestId(),
		"recovery_report_kind":      recoveryEvidenceReportKind,
		"rpo_promise":               recoveryNoFormalPromise,
		"rto_promise":               recoveryNoFormalPromise,
		"verified_artifacts":        strconv.Itoa(checkpoint.VerifiedArtifacts),
		"verified_block_objects":    strconv.Itoa(checkpoint.VerifiedBlockObjects),
		"verified_envelope_objects": strconv.Itoa(checkpoint.VerifiedEnvelopeObjects),
		"verified_index_objects":    strconv.Itoa(checkpoint.VerifiedIndexObjects),
		"verified_objects":          strconv.Itoa(checkpoint.VerifiedObjects),
		"verified_required_objects": strconv.Itoa(checkpoint.VerifiedRequiredObjects),
	}
}

func restoreEvidenceCounters(restore MetadataRestoreResult) map[string]string {
	return map[string]string{
		"blocks_restored":           strconv.Itoa(restore.BlocksRestored),
		"documents":                 strconv.Itoa(restore.Documents),
		"generation":                strconv.FormatUint(restore.Generation, 10),
		"manifest_id":               restore.ManifestID,
		"recovery_report_kind":      restore.ReportKind,
		"rpo_promise":               restore.RPOPromise,
		"rto_promise":               restore.RTOPromise,
		"snapshots":                 strconv.Itoa(restore.Snapshots),
		"tombstones":                strconv.Itoa(restore.Tombstones),
		"transactions":              strconv.Itoa(restore.Transactions),
		"upload_intents":            strconv.Itoa(restore.UploadIntents),
		"verified":                  strconv.Itoa(restore.Verified),
		"verified_artifacts":        strconv.Itoa(restore.VerifiedArtifacts),
		"verified_block_objects":    strconv.Itoa(restore.VerifiedBlockObjects),
		"verified_envelope_objects": strconv.Itoa(restore.VerifiedEnvelopeObjects),
		"verified_index_objects":    strconv.Itoa(restore.VerifiedIndexObjects),
		"verified_required_objects": strconv.Itoa(restore.VerifiedRequiredObjects),
	}
}

func (a *Application) runDrainOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, _ := a.beginOperationAttempt(operation, "running drain operation")
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

	workUnitsCompleted, err := operationWorkUnits("drain work units", workUnits)
	if err != nil {
		return false, err
	}
	finished.State = adminv1.OperationState_OPERATION_STATE_SUCCEEDED
	finished.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     workUnitsCompleted,
		WorkUnitsCompleted: workUnitsCompleted,
		Message:            "drain operation succeeded",
	}
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(operationCompletedAuditEvent(finished)); err != nil {
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
		return "", errors.New("localstorage: drain operation requires exactly one storage member target")
	}
	target := operation.GetTargets()[0]
	if target == nil {
		return "", errors.New("localstorage: drain operation requires a storage member target")
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
	collector := newDocumentCollector()
	if len(operation.GetTargets()) == 0 {
		if err := a.addAllDocuments(collector); err != nil {
			return nil, err
		}
		return collector.documents, nil
	}
	for _, target := range operation.GetTargets() {
		if err := a.addScrubTarget(collector, target); err != nil {
			return nil, err
		}
	}
	return collector.documents, nil
}

type documentCollector struct {
	documents []metastore.Document
	seen      map[identity.Document]bool
}

func newDocumentCollector() *documentCollector {
	return &documentCollector{seen: make(map[identity.Document]bool)}
}

func (c *documentCollector) add(document metastore.Document) {
	if c.seen[document.Identity] {
		return
	}
	c.seen[document.Identity] = true
	c.documents = append(c.documents, document)
}

func (a *Application) addAllDocuments(collector *documentCollector) error {
	all, err := a.metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return err
	}
	for _, document := range all {
		collector.add(document)
	}
	return nil
}

func (a *Application) addScrubTarget(collector *documentCollector, target *adminv1.Target) error {
	switch typed := target.GetTarget().(type) {
	case *adminv1.Target_Document:
		document, err := a.metadata.HeadDocument(adminDocumentIdentity(typed.Document))
		if err != nil {
			return err
		}
		collector.add(document)
		return nil
	case *adminv1.Target_Transaction:
		return a.addScrubTransactionTarget(collector, typed.Transaction)
	case *adminv1.Target_Block:
		return a.addScrubBlockTarget(collector, typed.Block)
	case *adminv1.Target_Shard:
		return a.addLocalScrubScope(collector, typed.Shard.GetShardId())
	case *adminv1.Target_StorageMember:
		return a.addLocalScrubScope(collector, typed.StorageMember.GetStorageMemberId())
	default:
		return fmt.Errorf("localstorage: unsupported scrub target %T", target.GetTarget())
	}
}

func (a *Application) addScrubTransactionTarget(collector *documentCollector, target *adminv1.TransactionTarget) error {
	docs, err := a.metadata.FindDocuments(identity.Transaction{
		TenantID:      target.GetTenantId(),
		TransactionID: target.GetTransactionId(),
	}, metastore.DocumentFilter{})
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return metastore.ErrNotFound
	}
	for _, document := range docs {
		collector.add(document)
	}
	return nil
}

func (a *Application) addScrubBlockTarget(collector *documentCollector, target *adminv1.BlockTarget) error {
	if target.GetShardId() != "local" {
		return metastore.ErrNotFound
	}
	docs, err := a.metadata.ListBlockDocuments(target.GetBlockId())
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return metastore.ErrNotFound
	}
	for _, document := range docs {
		collector.add(document)
	}
	return nil
}

func (a *Application) addLocalScrubScope(collector *documentCollector, shardOrMemberID string) error {
	if shardOrMemberID != "local" {
		return metastore.ErrNotFound
	}
	return a.addAllDocuments(collector)
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
	collector := newRepairStateCollector()
	for _, target := range operation.GetTargets() {
		if err := a.addRepairTarget(collector, states, target); err != nil {
			return nil, err
		}
	}
	if len(collector.repairs) == 0 {
		return nil, metastore.ErrNotFound
	}
	return collector.repairs, nil
}

type repairStateCollector struct {
	repairs []metastore.RepairState
	seen    map[string]bool
}

func newRepairStateCollector() *repairStateCollector {
	return &repairStateCollector{seen: make(map[string]bool)}
}

func (c *repairStateCollector) add(state metastore.RepairState) {
	if !state.Quarantined || !isLocalRepairState(state) {
		return
	}
	key := state.Identity.TenantID + "\x00" + state.Identity.TransactionID + "\x00" + state.Identity.DocumentName + "\x00" + state.IncidentID
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.repairs = append(c.repairs, state)
}

func (a *Application) addRepairTarget(collector *repairStateCollector, states []metastore.RepairState, target *adminv1.Target) error {
	switch typed := target.GetTarget().(type) {
	case *adminv1.Target_Document:
		addDocumentRepairStates(collector, states, adminDocumentIdentity(typed.Document))
		return nil
	case *adminv1.Target_Block:
		return a.addBlockRepairStates(collector, states, typed.Block)
	case *adminv1.Target_Shard:
		return addLocalRepairScope(collector, states, typed.Shard.GetShardId())
	case *adminv1.Target_StorageMember:
		return addLocalRepairScope(collector, states, typed.StorageMember.GetStorageMemberId())
	default:
		return fmt.Errorf("localstorage: unsupported repair target %T", target.GetTarget())
	}
}

func addDocumentRepairStates(collector *repairStateCollector, states []metastore.RepairState, doc identity.Document) {
	for _, state := range states {
		if state.Identity == doc {
			collector.add(state)
		}
	}
}

func (a *Application) addBlockRepairStates(collector *repairStateCollector, states []metastore.RepairState, target *adminv1.BlockTarget) error {
	if target.GetShardId() != "local" {
		return metastore.ErrNotFound
	}
	for _, state := range states {
		if !state.Quarantined {
			continue
		}
		document, err := a.metadata.HeadDocument(state.Identity)
		if err != nil {
			return err
		}
		if document.Location.BlockID == target.GetBlockId() {
			collector.add(state)
		}
	}
	return nil
}

func addLocalRepairScope(collector *repairStateCollector, states []metastore.RepairState, shardOrMemberID string) error {
	if shardOrMemberID != "local" {
		return metastore.ErrNotFound
	}
	for _, state := range states {
		collector.add(state)
	}
	return nil
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
		repaired, err := a.repairDocumentFromReplica(ctx, document, prepared, replica, now)
		if err != nil {
			return false, err
		}
		if repaired {
			return true, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (a *Application) repairDocumentFromReplica(
	ctx context.Context,
	document metastore.Document,
	prepared replication.PreparedDocument,
	replica blockstore.ReplicaRef,
	now time.Time,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	source := a.peerRepairSources[replica.MemberID]
	if source == nil {
		return false, nil
	}
	quarantined, err := a.peerRepairSourceQuarantined(document, replica)
	if err != nil || quarantined {
		return false, err
	}
	data, err := a.readVerifiedPeerReplica(ctx, document, prepared, replica, source, now)
	if err != nil || data == nil {
		return false, err
	}
	if err := a.blocks.InstallVerifiedRange(ctx, document.Location, document.StoredSHA256, bytes.NewReader(data)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) readVerifiedPeerReplica(
	ctx context.Context,
	document metastore.Document,
	prepared replication.PreparedDocument,
	replica blockstore.ReplicaRef,
	source PeerRepairSource,
	now time.Time,
) ([]byte, error) {
	var data bytes.Buffer
	err := source.ReadReplica(ctx, replica, &data)
	if err == nil {
		err = replication.ValidatePreparedBytes(prepared, data.Bytes())
	}
	if err == nil {
		return data.Bytes(), nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if !isPeerIntegrityFailure(err) {
		return nil, nil
	}
	if err := a.recordPeerRepairState(ctx, document, replica, true, now); err != nil {
		return nil, err
	}
	return nil, nil
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
		return errors.New("localstorage: backend store is not configured")
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
	_, err = a.restoreBlockFromBackend(ctx, blockID)
	return err
}

type restoreSummary struct {
	BlocksRestored  int
	BlocksSkipped   int
	BlocksPending   int
	DocumentsMarked int
}

func (a *Application) runRestoreOperation(ctx context.Context, store *operations.Store, operation *adminv1.Operation) (bool, error) {
	running, now := a.beginOperationAttempt(operation, "running "+operation.GetOperationType()+" operation")
	running.Metadata = restoreOperationMetadata(running)
	if err := store.Put(running); err != nil {
		return false, err
	}

	summary, err := a.applyRestoreOperation(ctx, running, now)
	if errors.Is(err, backend.ErrRestorePending) {
		pending := cloneOperation(running)
		pending.State = adminv1.OperationState_OPERATION_STATE_QUEUED
		progress, progressErr := restoreOperationProgress(pending, summary, operation.GetOperationType()+" operation pending backend restore")
		if progressErr != nil {
			return false, progressErr
		}
		pending.Progress = progress
		pending.LastError = nil
		if putErr := store.Put(pending); putErr != nil {
			return false, putErr
		}
		return false, nil
	}
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
	progress, err := restoreOperationProgress(finished, summary, operation.GetOperationType()+" operation succeeded")
	if err != nil {
		return false, err
	}
	finished.Progress = progress
	if err := store.Put(finished); err != nil {
		return false, err
	}
	if err := store.AppendAuditEvent(restoreCompletedAuditEvent(finished)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Application) applyRestoreOperation(ctx context.Context, operation *adminv1.Operation, now time.Time) (restoreSummary, error) {
	var summary restoreSummary
	if operation.GetDryRun() {
		summary.BlocksSkipped = len(operation.GetTargets())
		return summary, nil
	}
	blockIDs, documents, err := a.restoreTargets(operation)
	if err != nil {
		return summary, err
	}
	for blockID := range blockIDs {
		restored, err := a.restoreBlockFromBackend(ctx, blockID)
		if errors.Is(err, backend.ErrRestorePending) {
			summary.BlocksPending++
			continue
		}
		if err != nil {
			return summary, err
		}
		if restored {
			summary.BlocksRestored++
		} else {
			summary.BlocksSkipped++
		}
	}
	if summary.BlocksPending > 0 {
		for _, doc := range documents {
			if err := a.authority.UpdateDocumentRestoreState(
				ctx,
				doc,
				metastore.RestoreStateRestorePending,
				operation.GetOperationType()+" operation waiting on backend restore",
				stableCommandID(operation.GetOperationType()+"-pending-operation", operation.GetOperationId(), doc.TenantID, doc.TransactionID, doc.DocumentName),
				now,
			); err != nil {
				return summary, err
			}
			summary.DocumentsMarked++
		}
		return summary, backend.ErrRestorePending
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
			return summary, err
		}
		summary.DocumentsMarked++
	}
	return summary, nil
}

func (a *Application) restoreTargets(operation *adminv1.Operation) (map[string]bool, []identity.Document, error) {
	collector := restoreTargetCollector{
		blockIDs:       make(map[string]bool),
		documentsByKey: make(map[identity.Document]bool),
	}

	for _, target := range operation.GetTargets() {
		if err := collector.addTarget(a, target, operation.GetOperationType()); err != nil {
			return nil, nil, err
		}
	}
	return collector.blockIDs, collector.documents, nil
}

type restoreTargetCollector struct {
	blockIDs       map[string]bool
	documentsByKey map[identity.Document]bool
	documents      []identity.Document
}

func (c *restoreTargetCollector) addTarget(a *Application, target *adminv1.Target, operationType string) error {
	switch typed := target.GetTarget().(type) {
	case *adminv1.Target_Document:
		return c.addDocumentTarget(a, typed.Document)
	case *adminv1.Target_Transaction:
		return c.addTransactionTarget(a, typed.Transaction)
	case *adminv1.Target_Block:
		return c.addBlockTarget(a, typed.Block)
	default:
		return fmt.Errorf("localstorage: unsupported %s target %T", operationType, target.GetTarget())
	}
}

func (c *restoreTargetCollector) addDocumentTarget(a *Application, target *adminv1.DocumentTarget) error {
	document, err := a.metadata.HeadDocument(adminDocumentIdentity(target))
	if err != nil {
		return err
	}
	c.addDocument(document)
	return nil
}

func (c *restoreTargetCollector) addTransactionTarget(a *Application, target *adminv1.TransactionTarget) error {
	docs, err := a.metadata.FindDocuments(identity.Transaction{
		TenantID:      target.GetTenantId(),
		TransactionID: target.GetTransactionId(),
	}, metastore.DocumentFilter{})
	if err != nil {
		return err
	}
	return c.addDocuments(docs)
}

func (c *restoreTargetCollector) addBlockTarget(a *Application, target *adminv1.BlockTarget) error {
	if target.GetShardId() != "local" {
		return metastore.ErrNotFound
	}
	docs, err := a.metadata.ListBlockDocuments(target.GetBlockId())
	if err != nil {
		return err
	}
	return c.addDocuments(docs)
}

func (c *restoreTargetCollector) addDocuments(documents []metastore.Document) error {
	if len(documents) == 0 {
		return metastore.ErrNotFound
	}
	for _, document := range documents {
		c.addDocument(document)
	}
	return nil
}

func (c *restoreTargetCollector) addDocument(document metastore.Document) {
	c.blockIDs[document.Location.BlockID] = true
	if c.documentsByKey[document.Identity] {
		return
	}
	c.documentsByKey[document.Identity] = true
	c.documents = append(c.documents, document.Identity)
}

func (a *Application) restoreBlockFromBackend(ctx context.Context, blockID string) (bool, error) {
	if a.backendStore == nil {
		return false, errors.New("localstorage: backend store is not configured")
	}
	intent, err := a.metadata.GetUploadIntent(blockID)
	if err != nil {
		return false, err
	}
	if intent.State != metastore.UploadStateUploaded {
		return false, fmt.Errorf("localstorage: block %s is not uploaded", blockID)
	}
	if err := a.verifyBackendEnvelope(ctx, intent); err != nil {
		return false, err
	}
	object, err := a.backendStore.HeadObject(ctx, intent.BackendObjectKey)
	if err != nil {
		return false, err
	}
	installed, err := a.blocks.EnsureSealedBlock(ctx, blockID, object.Length, object.SHA256)
	if err != nil {
		return false, err
	}
	if installed {
		return false, nil
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
		return false, installErr
	}
	if readErr != nil {
		return false, readErr
	}
	return true, nil
}

func restoreOperationMetadata(operation *adminv1.Operation) map[string]string {
	metadata := cloneTags(operation.GetMetadata())
	if metadata == nil {
		metadata = make(map[string]string)
	}
	if metadata[operationLaneMetadata] == "" {
		metadata[operationLaneMetadata] = defaultRestoreOperationLane(operation.GetOperationType())
	}
	if metadata[backendLaneMetadata] == "" {
		metadata[backendLaneMetadata] = string(backend.LaneRestore)
	}
	return metadata
}

func defaultRestoreOperationLane(operationType string) string {
	switch operationType {
	case "prewarm":
		return operationLanePlannedPrewarm
	case "restore":
		return operationLaneInteractive
	default:
		return operationLaneRestoreFallback
	}
}

func restoreOperationProgress(operation *adminv1.Operation, summary restoreSummary, message string) (*adminv1.OperationProgress, error) {
	total, err := operationWorkUnits("restore work units", summary.BlocksRestored, summary.BlocksSkipped, summary.BlocksPending)
	if err != nil {
		return nil, err
	}
	completed, err := operationWorkUnits("completed restore work units", summary.BlocksRestored, summary.BlocksSkipped)
	if err != nil {
		return nil, err
	}
	return &adminv1.OperationProgress{
		WorkUnitsTotal:     total,
		WorkUnitsCompleted: completed,
		Message:            message,
		Counters: map[string]string{
			"blocks_restored":  strconv.Itoa(summary.BlocksRestored),
			"blocks_skipped":   strconv.Itoa(summary.BlocksSkipped),
			"blocks_pending":   strconv.Itoa(summary.BlocksPending),
			"documents_marked": strconv.Itoa(summary.DocumentsMarked),
			"operation_lane":   operation.GetMetadata()[operationLaneMetadata],
			"backend_lane":     operation.GetMetadata()[backendLaneMetadata],
		},
	}, nil
}

func operationWorkUnits(name string, values ...int) (uint64, error) {
	var total uint64
	for _, value := range values {
		converted, err := safeconv.IntToUint64(name, value)
		if err != nil {
			return 0, err
		}
		next := total + converted
		if next < total {
			return 0, fmt.Errorf("%s overflows uint64", name)
		}
		total = next
	}
	return total, nil
}

func restoreCompletedAuditEvent(operation *adminv1.Operation) *adminv1.AuditEvent {
	eventType := operationEventRestoreComplete
	if operation.GetOperationType() == "prewarm" {
		eventType = operationEventPrewarmComplete
	}
	return &adminv1.AuditEvent{
		EventId:       stableCommandID("audit-"+eventType, operation.GetOperationId()),
		EventType:     eventType,
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: operation.GetRequestedByIdentity(),
		OccurredAt:    operation.GetFinishedAt(),
		Targets:       cloneOperationTargets(operation.GetTargets()),
		Metadata:      sanitizeOperationAuditMetadata(operation.GetMetadata()),
	}
}

func operationCompletedAuditEvent(operation *adminv1.Operation) *adminv1.AuditEvent {
	eventType := strings.ReplaceAll(operation.GetOperationType(), "-", "_") + "_completed"
	return &adminv1.AuditEvent{
		EventId:       stableCommandID("audit-"+eventType, operation.GetOperationId()),
		EventType:     eventType,
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: operation.GetRequestedByIdentity(),
		OccurredAt:    operation.GetFinishedAt(),
		Targets:       cloneOperationTargets(operation.GetTargets()),
		Metadata:      sanitizeOperationAuditMetadata(operation.GetMetadata()),
	}
}

func sanitizeOperationAuditMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if operationAuditMetadataKeyIsSensitive(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func operationAuditMetadataKeyIsSensitive(key string) bool {
	normalized := strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
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
