package operations

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestStorePutGetAndReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
	operation := sampleOperation("op-1", "restore", adminv1.OperationState_OPERATION_STATE_PLANNED)
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	testutil.RequireNoErrorf(t, store.Close(), "close store")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	got, err := reopened.Get("op-1")
	testutil.RequireNoErrorf(t, err, "get operation")
	if got.GetOperationId() != operation.GetOperationId() ||
		got.GetOperationType() != operation.GetOperationType() ||
		got.GetState() != operation.GetState() {
		t.Fatalf("operation = %#v, want %#v", got, operation)
	}
}

func TestStorePutIsIdempotentAndUpdatesState(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_QUEUED)
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	testutil.RequireNoErrorf(t, store.Put(operation), "idempotent put")
	operation.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	operation.StartedAt = timestamppb.New(time.Unix(200, 0).UTC())
	testutil.RequireNoErrorf(t, store.Put(operation), "update operation")
	got, err := store.Get("op-1")
	testutil.RequireNoErrorf(t, err, "get operation")
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_RUNNING || got.GetStartedAt() == nil {
		t.Fatalf("updated operation = %#v, want running with started_at", got)
	}
}

func TestStoreCreateIsIdempotentAndRejectsConflicts(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_QUEUED)
	created, err := store.Create(operation)
	testutil.RequireNoErrorf(t, err, "create operation")
	if created.GetOperationId() != "op-1" {
		t.Fatalf("created operation = %#v, want op-1", created)
	}
	replayed, err := store.Create(operation)
	testutil.RequireNoErrorf(t, err, "idempotent create")
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
	testutil.RequireNoErrorf(t, err, "list operations")
	if len(got) != 1 || got[0].GetOperationId() != "op-1" {
		t.Fatalf("operations = %#v, want only op-1", got)
	}
}

func TestStorePutPlanGetPlanAndReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
	plan := samplePlan("hash-1")
	testutil.RequireNoErrorf(t, store.PutPlan(plan), "put plan")
	testutil.RequireNoErrorf(t, store.Close(), "close store")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	got, err := reopened.GetPlan("plan-1")
	testutil.RequireNoErrorf(t, err, "get plan")
	if got.GetOperationPlanId() != "plan-1" || got.GetPlanHash() != "hash-1" || len(got.GetTargets()) != 1 {
		t.Fatalf("plan = %#v, want persisted plan", got)
	}
}

func TestStorePutPlanIsImmutable(t *testing.T) {
	store := openTestStore(t)
	plan := samplePlan("hash-1")
	testutil.RequireNoErrorf(t, store.PutPlan(plan), "put plan")
	testutil.RequireNoErrorf(t, store.PutPlan(plan), "idempotent put plan")
	conflict := samplePlan("hash-2")
	if err := store.PutPlan(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestStoreCancelMarksNonTerminalOperationCanceled(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	finishedAt := time.Unix(300, 0).UTC()
	canceled, err := store.Cancel("op-1", finishedAt)
	testutil.RequireNoErrorf(t, err, "cancel operation")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	got, err := store.Cancel("op-1", time.Unix(300, 0).UTC())
	testutil.RequireNoErrorf(t, err, "cancel operation")
	if got.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		!got.GetFinishedAt().AsTime().Equal(finishedAt) {
		t.Fatalf("terminal operation changed to %#v", got)
	}
}

func TestStoreRecoverInterruptedKeepsQueuedRetryEvidenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	testutil.RequireNoErrorf(t, store.Close(), "close store")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	result, err := reopened.RecoverInterrupted(time.Unix(200, 0).UTC(), map[string]bool{"repair": true})
	testutil.RequireNoErrorf(t, err, "recover interrupted")
	if result.Scanned != 1 || result.Queued != 1 || result.Requeued != 0 || result.FailedUnsupported != 0 {
		t.Fatalf("recovery result = %#v, want queued operation left unchanged", result)
	}
	got, err := reopened.Get("op-1")
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := store.RecoverInterrupted(time.Unix(200, 0).UTC(), map[string]bool{"repair": true})
	testutil.RequireNoErrorf(t, err, "recover interrupted")
	testutil.RequireEqualf(t, result.Scanned, 1, "recovery scanned count")
	testutil.RequireEqualf(t, result.Requeued, 1, "recovery requeued count")
	testutil.RequireEqualf(t, result.FailedUnsupported, 0, "recovery failed unsupported count")
	got, err := store.Get("op-1")
	testutil.RequireNoErrorf(t, err, "get operation")
	requireRecoveredRepairOperation(t, got, startedAt)
}

func requireRecoveredRepairOperation(t *testing.T, operation *adminv1.Operation, startedAt time.Time) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "operation state")
	testutil.RequireTruef(t, operation.GetStartedAt().AsTime().Equal(startedAt), "started_at = %s, want %s", operation.GetStartedAt().AsTime(), startedAt)
	testutil.RequireNilf(t, operation.GetFinishedAt(), "finished_at")
	testutil.RequireEqualf(t, operation.GetProgress().GetMessage(), "retrying peer repair", "progress message")
	testutil.RequireEqualf(t, operation.GetProgress().GetCounters()["retry_attempt"], "2", "retry counter")
	testutil.RequireEqualf(t, operation.GetProgress().GetCounters()["blocks_seen"], "7", "blocks seen counter")
	testutil.RequireEqualf(t, operation.GetLastError().GetCode(), "SCRAP_REPAIR_RETRYABLE", "last error code")
	testutil.RequireEqualf(t, operation.GetMetadata()["audit_correlation_id"], "audit-1", "audit correlation id")
	testutil.RequireTruef(t, hasOperationWarningCode(operation.GetWarnings(), "SCRAP_REPAIR_SOURCE_SLOW"), "source slow warning missing")
	testutil.RequireTruef(t, hasOperationWarningCode(operation.GetWarnings(), "SCRAP_OPERATION_RESTART_REQUEUED"), "restart requeued warning missing")
}

func TestStoreRecoverInterruptedFailsUnsupportedRunningOperation(t *testing.T) {
	store := openTestStore(t)
	operation := sampleOperation("op-1", "legacy-maintenance", adminv1.OperationState_OPERATION_STATE_RUNNING)
	operation.StartedAt = timestamppb.New(time.Unix(150, 0).UTC())
	operation.Progress = &adminv1.OperationProgress{Message: "legacy operation running"}
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	finishedAt := time.Unix(200, 0).UTC()
	result, err := store.RecoverInterrupted(finishedAt, map[string]bool{"repair": true})
	testutil.RequireNoErrorf(t, err, "recover interrupted")
	if result.Scanned != 1 || result.Requeued != 0 || result.FailedUnsupported != 1 {
		t.Fatalf("recovery result = %#v, want unsupported running operation failed", result)
	}
	got, err := store.Get("op-1")
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, err, "recover interrupted")
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
	testutil.RequireNoErrorf(t, err, "open store")
	event := sampleAuditEvent("operation_started:op-1", "operation_started")
	testutil.RequireNoErrorf(t, store.AppendAuditEvent(event), "append audit event")
	testutil.RequireNoErrorf(t, store.AppendAuditEvent(event), "idempotent append audit event")
	testutil.RequireNoErrorf(t, store.Close(), "close store")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	events, err := reopened.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	if len(events) != 1 || !proto.Equal(events[0], event) {
		t.Fatalf("events = %#v, want persisted audit event %#v", events, event)
	}
}

func TestStoreAppendAuditEventRejectsConflictingEventID(t *testing.T) {
	store := openTestStore(t)
	event := sampleAuditEvent("operation_started:op-1", "operation_started")
	testutil.RequireNoErrorf(t, store.AppendAuditEvent(event), "append audit event")
	conflict := sampleAuditEvent("operation_started:op-1", "operation_started")
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
		"missing id":    sampleAuditEvent("", "operation_started"),
		"missing type":  sampleAuditEvent("operation_started:op-1", ""),
		"missing actor": sampleAuditEvent("operation_started:op-1", "operation_started"),
		"missing time":  sampleAuditEvent("operation_started:op-1", "operation_started"),
		"invalid time":  sampleAuditEvent("operation_started:op-1", "operation_started"),
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
	testutil.RequireNoErrorf(t, err, "open store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close store")
	})
	return store
}

func sampleAuditEvent(eventID, eventType string) *adminv1.AuditEvent {
	return &adminv1.AuditEvent{
		EventId:       eventID,
		EventType:     eventType,
		OperationId:   "op-1",
		OperationType: "repair",
		ActorIdentity: "test-operator",
		OccurredAt:    timestamppb.New(time.Unix(500, 0).UTC()),
		Targets:       samplePlan("hash-1").GetTargets(),
		Metadata:      map[string]string{"ticket": "INC-1"},
	}
}

func sampleOperation(operationID, operationType string, state adminv1.OperationState) *adminv1.Operation {
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

func samplePlan(planHash string) *adminv1.OperationPlan {
	return &adminv1.OperationPlan{
		OperationPlanId: "plan-1",
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
