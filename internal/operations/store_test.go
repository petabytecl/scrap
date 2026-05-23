package operations

import (
	"errors"
	"testing"
	"time"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStorePutGetAndReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	operation := sampleOperation("op-1", "restore", adminv1.OperationState_OPERATION_STATE_PLANNED)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.Get("op-1")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.GetOperationId() != operation.GetOperationId() ||
		got.GetOperationType() != operation.GetOperationType() ||
		got.GetState() != operation.GetState() {
		t.Fatalf("operation = %#v, want %#v", got, operation)
	}
}

func TestStorePutIsIdempotentAndUpdatesState(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_QUEUED)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	if err := store.Put(operation); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	operation.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	operation.StartedAt = timestamppb.New(time.Unix(200, 0).UTC())
	if err := store.Put(operation); err != nil {
		t.Fatalf("update operation: %v", err)
	}
	got, err := store.Get("op-1")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_RUNNING || got.GetStartedAt() == nil {
		t.Fatalf("updated operation = %#v, want running with started_at", got)
	}
}

func TestStoreCreateIsIdempotentAndRejectsConflicts(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_QUEUED)
	created, err := store.Create(operation)
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	if created.GetOperationId() != "op-1" {
		t.Fatalf("created operation = %#v, want op-1", created)
	}
	replayed, err := store.Create(operation)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if replayed.GetOperationType() != operation.GetOperationType() {
		t.Fatalf("replayed operation = %#v, want same operation", replayed)
	}
	conflict := sampleOperation("op-1", "restore", adminv1.OperationState_OPERATION_STATE_QUEUED)
	if _, err := store.Create(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestStoreListFiltersByStateAndType(t *testing.T) {
	store := openTestStore(t)
	for _, operation := range []*adminv1.Operation{
		sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_RUNNING),
		sampleOperation("op-2", "repair", adminv1.OperationState_OPERATION_STATE_SUCCEEDED),
		sampleOperation("op-3", "restore", adminv1.OperationState_OPERATION_STATE_RUNNING),
	} {
		if err := store.Put(operation); err != nil {
			t.Fatalf("put operation %s: %v", operation.GetOperationId(), err)
		}
	}
	got, err := store.List(ListFilter{
		States:        []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_RUNNING},
		OperationType: "repair",
	})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	if len(got) != 1 || got[0].GetOperationId() != "op-1" {
		t.Fatalf("operations = %#v, want only op-1", got)
	}
}

func TestStorePutPlanGetPlanAndReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	plan := samplePlan("plan-1", "hash-1")
	if err := store.PutPlan(plan); err != nil {
		t.Fatalf("put plan: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetPlan("plan-1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.GetOperationPlanId() != "plan-1" || got.GetPlanHash() != "hash-1" || len(got.GetTargets()) != 1 {
		t.Fatalf("plan = %#v, want persisted plan", got)
	}
}

func TestStorePutPlanIsImmutable(t *testing.T) {
	store := openTestStore(t)
	plan := samplePlan("plan-1", "hash-1")
	if err := store.PutPlan(plan); err != nil {
		t.Fatalf("put plan: %v", err)
	}
	if err := store.PutPlan(plan); err != nil {
		t.Fatalf("idempotent put plan: %v", err)
	}
	conflict := samplePlan("plan-1", "hash-2")
	if err := store.PutPlan(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestStoreCancelMarksNonTerminalOperationCanceled(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	finishedAt := time.Unix(300, 0).UTC()
	canceled, err := store.Cancel("op-1", finishedAt)
	if err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	if canceled.GetState() != adminv1.OperationState_OPERATION_STATE_CANCELED ||
		canceled.GetFinishedAt() == nil ||
		!canceled.GetFinishedAt().AsTime().Equal(finishedAt) {
		t.Fatalf("canceled operation = %#v, want canceled at %v", canceled, finishedAt)
	}
}

func TestStoreCancelLeavesTerminalOperationUnchanged(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_SUCCEEDED)
	finishedAt := time.Unix(200, 0).UTC()
	operation.FinishedAt = timestamppb.New(finishedAt)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	got, err := store.Cancel("op-1", time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		!got.GetFinishedAt().AsTime().Equal(finishedAt) {
		t.Fatalf("terminal operation changed to %#v", got)
	}
}

func TestStoreRecoverInterruptedKeepsQueuedRetryEvidenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_QUEUED)
	operation.Progress = &adminv1.OperationProgress{
		Message: "queued retry",
		Counters: map[string]string{
			"retry_attempt": "2",
		},
	}
	operation.Warnings = []*adminv1.OperationWarning{
		{Code: "SCRAP_RETRY_WAITING", Message: "retry queued after transient failure"},
	}
	operation.LastError = &adminv1.OperationError{Code: "SCRAP_REPAIR_TRANSIENT", Message: "peer unavailable"}
	operation.Metadata["audit_correlation_id"] = "audit-1"
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	result, err := reopened.RecoverInterrupted(time.Unix(200, 0).UTC(), map[string]bool{"repair": true})
	if err != nil {
		t.Fatalf("recover interrupted: %v", err)
	}
	if result.Scanned != 1 || result.Queued != 1 || result.Requeued != 0 || result.FailedUnsupported != 0 {
		t.Fatalf("recovery result = %#v, want queued operation left unchanged", result)
	}
	got, err := reopened.Get("op-1")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if !proto.Equal(got, operation) {
		t.Fatalf("operation = %#v, want unchanged %#v", got, operation)
	}
}

func TestStoreRecoverInterruptedRequeuesRunningSupportedOperation(t *testing.T) {
	store := openTestStore(t)
	startedAt := time.Unix(150, 0).UTC()
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	operation.StartedAt = timestamppb.New(startedAt)
	operation.Progress = &adminv1.OperationProgress{
		WorkUnitsTotal:     3,
		WorkUnitsCompleted: 1,
		Message:            "retrying peer repair",
		Counters: map[string]string{
			"retry_attempt": "2",
			"blocks_seen":   "7",
		},
	}
	operation.Warnings = []*adminv1.OperationWarning{
		{Code: "SCRAP_REPAIR_SOURCE_SLOW", Message: "peer repair source was slow"},
	}
	operation.LastError = &adminv1.OperationError{Code: "SCRAP_REPAIR_RETRYABLE", Message: "transient peer read failure"}
	operation.Metadata["audit_correlation_id"] = "audit-1"
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := store.RecoverInterrupted(time.Unix(200, 0).UTC(), map[string]bool{"repair": true})
	if err != nil {
		t.Fatalf("recover interrupted: %v", err)
	}
	if result.Scanned != 1 || result.Requeued != 1 || result.FailedUnsupported != 0 {
		t.Fatalf("recovery result = %#v, want one requeued operation", result)
	}
	got, err := store.Get("op-1")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		!got.GetStartedAt().AsTime().Equal(startedAt) ||
		got.GetFinishedAt() != nil ||
		got.GetProgress().GetMessage() != "retrying peer repair" ||
		got.GetProgress().GetCounters()["retry_attempt"] != "2" ||
		got.GetProgress().GetCounters()["blocks_seen"] != "7" ||
		got.GetLastError().GetCode() != "SCRAP_REPAIR_RETRYABLE" ||
		got.GetMetadata()["audit_correlation_id"] != "audit-1" ||
		!hasOperationWarningCode(got.GetWarnings(), "SCRAP_REPAIR_SOURCE_SLOW") ||
		!hasOperationWarningCode(got.GetWarnings(), "SCRAP_OPERATION_RESTART_REQUEUED") {
		t.Fatalf("recovered operation = %#v, want queued retry with existing evidence preserved", got)
	}
}

func TestStoreRecoverInterruptedFailsUnsupportedRunningOperation(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "legacy-maintenance", adminv1.OperationState_OPERATION_STATE_RUNNING)
	operation.StartedAt = timestamppb.New(time.Unix(150, 0).UTC())
	operation.Progress = &adminv1.OperationProgress{Message: "legacy operation running"}
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	finishedAt := time.Unix(200, 0).UTC()
	result, err := store.RecoverInterrupted(finishedAt, map[string]bool{"repair": true})
	if err != nil {
		t.Fatalf("recover interrupted: %v", err)
	}
	if result.Scanned != 1 || result.Requeued != 0 || result.FailedUnsupported != 1 {
		t.Fatalf("recovery result = %#v, want unsupported running operation failed", result)
	}
	got, err := store.Get("op-1")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		!got.GetFinishedAt().AsTime().Equal(finishedAt) ||
		got.GetLastError().GetCode() != "SCRAP_OPERATION_RECOVERY_UNSUPPORTED" ||
		!hasOperationWarningCode(got.GetWarnings(), "SCRAP_OPERATION_RECOVERY_UNSUPPORTED") {
		t.Fatalf("recovered operation = %#v, want typed unsupported terminal failure", got)
	}
}

func TestStoreRecoverInterruptedLeavesTerminalStatesUnchanged(t *testing.T) {
	store := openTestStore(t)
	operations := []*adminv1.Operation{
		sampleOperation("op-succeeded", "repair", adminv1.OperationState_OPERATION_STATE_SUCCEEDED),
		sampleOperation("op-failed", "repair", adminv1.OperationState_OPERATION_STATE_FAILED),
		sampleOperation("op-canceled", "repair", adminv1.OperationState_OPERATION_STATE_CANCELED),
	}
	for _, operation := range operations {
		operation.FinishedAt = timestamppb.New(time.Unix(200, 0).UTC())
		if err := store.Put(operation); err != nil {
			t.Fatalf("put operation %s: %v", operation.GetOperationId(), err)
		}
	}

	result, err := store.RecoverInterrupted(time.Unix(300, 0).UTC(), map[string]bool{"repair": true})
	if err != nil {
		t.Fatalf("recover interrupted: %v", err)
	}
	if result.Scanned != 3 || result.Terminal != 3 || result.Requeued != 0 || result.FailedUnsupported != 0 {
		t.Fatalf("recovery result = %#v, want terminal operations left unchanged", result)
	}
	for _, operation := range operations {
		got, err := store.Get(operation.GetOperationId())
		if err != nil {
			t.Fatalf("get operation %s: %v", operation.GetOperationId(), err)
		}
		if !proto.Equal(got, operation) {
			t.Fatalf("operation %s = %#v, want unchanged %#v", operation.GetOperationId(), got, operation)
		}
	}
}

func TestStoreAppendAuditEventPersistsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	event := sampleAuditEvent("operation_started:op-1", "operation_started", "op-1")
	if err := store.AppendAuditEvent(event); err != nil {
		t.Fatalf("append audit event: %v", err)
	}
	if err := store.AppendAuditEvent(event); err != nil {
		t.Fatalf("idempotent append audit event: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	events, err := reopened.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 || !proto.Equal(events[0], event) {
		t.Fatalf("events = %#v, want persisted audit event %#v", events, event)
	}
}

func TestStoreAppendAuditEventRejectsConflictingEventID(t *testing.T) {
	store := openTestStore(t)
	event := sampleAuditEvent("operation_started:op-1", "operation_started", "op-1")
	if err := store.AppendAuditEvent(event); err != nil {
		t.Fatalf("append audit event: %v", err)
	}
	conflict := sampleAuditEvent("operation_started:op-1", "operation_started", "op-1")
	conflict.Metadata["ticket"] = "INC-2"
	if err := store.AppendAuditEvent(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestStoreRejectsInvalidOperations(t *testing.T) {
	store := openTestStore(t)
	tests := map[string]*adminv1.Operation{
		"nil":          nil,
		"missing id":   sampleOperation("", "repair", adminv1.OperationState_OPERATION_STATE_PLANNED),
		"missing type": sampleOperation("op-1", "", adminv1.OperationState_OPERATION_STATE_PLANNED),
		"missing state": sampleOperation(
			"op-1",
			"repair",
			adminv1.OperationState_OPERATION_STATE_UNSPECIFIED,
		),
	}
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			err := store.Put(operation)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestStoreRejectsInvalidAuditEvents(t *testing.T) {
	store := openTestStore(t)
	tests := map[string]*adminv1.AuditEvent{
		"nil":           nil,
		"missing id":    sampleAuditEvent("", "operation_started", "op-1"),
		"missing type":  sampleAuditEvent("operation_started:op-1", "", "op-1"),
		"missing actor": sampleAuditEvent("operation_started:op-1", "operation_started", "op-1"),
		"missing time":  sampleAuditEvent("operation_started:op-1", "operation_started", "op-1"),
		"invalid time":  sampleAuditEvent("operation_started:op-1", "operation_started", "op-1"),
	}
	tests["missing actor"].ActorIdentity = ""
	tests["missing time"].OccurredAt = nil
	tests["invalid time"].OccurredAt = timestamppb.New(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	for name, event := range tests {
		t.Run(name, func(t *testing.T) {
			err := store.AppendAuditEvent(event)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}

func sampleAuditEvent(eventID string, eventType string, operationID string) *adminv1.AuditEvent {
	return &adminv1.AuditEvent{
		EventId:       eventID,
		EventType:     eventType,
		OperationId:   operationID,
		OperationType: "repair",
		ActorIdentity: "test-operator",
		OccurredAt:    timestamppb.New(time.Unix(500, 0).UTC()),
		Targets:       samplePlan("plan-1", "hash-1").GetTargets(),
		Metadata:      map[string]string{"ticket": "INC-1"},
	}
}

func sampleOperation(operationID string, operationType string, state adminv1.OperationState) *adminv1.Operation {
	return &adminv1.Operation{
		OperationId:         operationID,
		OperationType:       operationType,
		State:               state,
		RequestedByIdentity: "test-operator",
		RequestedAt:         timestamppb.New(time.Unix(100, 0).UTC()),
		Metadata: map[string]string{
			"source": "test",
		},
	}
}

func samplePlan(operationPlanID string, planHash string) *adminv1.OperationPlan {
	return &adminv1.OperationPlan{
		OperationPlanId: operationPlanID,
		PlanHash:        planHash,
		ExpiresAt:       timestamppb.New(time.Unix(400, 0).UTC()),
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Document{
					Document: &adminv1.DocumentTarget{
						TenantId:      "tenant",
						TransactionId: "txn",
						DocumentName:  "invoice.xml",
					},
				},
			},
		},
	}
}

func hasOperationWarningCode(warnings []*adminv1.OperationWarning, code string) bool {
	for _, warning := range warnings {
		if warning.GetCode() == code {
			return true
		}
	}
	return false
}
