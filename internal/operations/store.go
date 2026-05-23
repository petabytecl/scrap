package operations

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/closeutil"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
)

var (
	ErrNotFound = errors.New("operations: not found")
	ErrInvalid  = errors.New("operations: invalid operation")
	ErrConflict = errors.New("operations: conflict")
)

var protoMarshal = proto.MarshalOptions{Deterministic: true}

type Store struct {
	db *pebble.DB
}

type ListFilter struct {
	States        []adminv1.OperationState
	OperationType string
}

type RecoveryResult struct {
	Scanned           int
	Queued            int
	Requeued          int
	FailedUnsupported int
	Terminal          int
	Ignored           int
}

type recoveryUpdateKind int

const (
	recoveryUpdateRequeued recoveryUpdateKind = iota + 1
	recoveryUpdateFailedUnsupported
)

type recoveryUpdate struct {
	expected  *adminv1.Operation
	recovered *adminv1.Operation
	kind      recoveryUpdateKind
}

func Open(dir string) (*Store, error) {
	db, err := pebble.Open(filepath.Join(dir, "operations"), &pebble.Options{})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) Put(operation *adminv1.Operation) error {
	if err := validateOperation(operation); err != nil {
		return err
	}
	value, err := protoMarshal.Marshal(operation)
	if err != nil {
		return err
	}
	key := operationKey(operation.GetOperationId())
	if existing, ok, err := s.get(key); err != nil {
		return err
	} else if ok && bytes.Equal(existing, value) {
		return nil
	}
	return s.db.Set(key, value, pebble.Sync)
}

func (s *Store) Create(operation *adminv1.Operation) (*adminv1.Operation, error) {
	if err := validateOperation(operation); err != nil {
		return nil, err
	}
	value, err := protoMarshal.Marshal(operation)
	if err != nil {
		return nil, err
	}
	key := operationKey(operation.GetOperationId())
	if existing, ok, err := s.get(key); err != nil {
		return nil, err
	} else if ok {
		if bytes.Equal(existing, value) {
			return unmarshalOperation(existing)
		}
		return nil, fmt.Errorf("%w: operation already exists with different metadata", ErrConflict)
	}
	if err := s.db.Set(key, value, pebble.Sync); err != nil {
		return nil, err
	}
	return operation, nil
}

func (s *Store) Get(operationID string) (*adminv1.Operation, error) {
	value, ok, err := s.get(operationKey(operationID))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return unmarshalOperation(value)
}

func (s *Store) PutPlan(plan *adminv1.OperationPlan) error {
	if err := validateOperationPlan(plan); err != nil {
		return err
	}
	value, err := protoMarshal.Marshal(plan)
	if err != nil {
		return err
	}
	key := operationPlanKey(plan.GetOperationPlanId())
	if existing, ok, err := s.get(key); err != nil {
		return err
	} else if ok {
		if bytes.Equal(existing, value) {
			return nil
		}
		return fmt.Errorf("%w: operation plan already exists with different metadata", ErrConflict)
	}
	return s.db.Set(key, value, pebble.Sync)
}

func (s *Store) GetPlan(operationPlanID string) (*adminv1.OperationPlan, error) {
	value, ok, err := s.get(operationPlanKey(operationPlanID))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return unmarshalOperationPlan(value)
}

func (s *Store) List(filter ListFilter) ([]*adminv1.Operation, error) {
	prefix := operationPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(iter)

	stateFilter := make(map[adminv1.OperationState]bool, len(filter.States))
	for _, state := range filter.States {
		stateFilter[state] = true
	}
	var operations []*adminv1.Operation
	for valid := iter.First(); valid; valid = iter.Next() {
		operation, err := unmarshalOperation(iter.Value())
		if err != nil {
			return nil, err
		}
		if filter.OperationType != "" && operation.GetOperationType() != filter.OperationType {
			continue
		}
		if len(stateFilter) > 0 && !stateFilter[operation.GetState()] {
			continue
		}
		operations = append(operations, operation)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return operations, nil
}

func (s *Store) Cancel(operationID string, finishedAt time.Time) (*adminv1.Operation, error) {
	operation, err := s.Get(operationID)
	if err != nil {
		return nil, err
	}
	if isTerminal(operation.GetState()) {
		return operation, nil
	}
	operation.State = adminv1.OperationState_OPERATION_STATE_CANCELED
	operation.FinishedAt = timestamppb.New(finishedAt)
	if err := s.Put(operation); err != nil {
		return nil, err
	}
	return operation, nil
}

func (s *Store) RecoverInterrupted(now time.Time, supportedTypes map[string]bool) (RecoveryResult, error) {
	var result RecoveryResult
	all, err := s.List(ListFilter{})
	if err != nil {
		return result, err
	}
	var candidates []recoveryUpdate
	for _, operation := range all {
		result.Scanned++
		switch operation.GetState() {
		case adminv1.OperationState_OPERATION_STATE_QUEUED:
			result.Queued++
		case adminv1.OperationState_OPERATION_STATE_RUNNING:
			recovered := cloneOperation(operation)
			if supportedTypes[recovered.GetOperationType()] {
				recovered.State = adminv1.OperationState_OPERATION_STATE_QUEUED
				recovered.FinishedAt = nil
				appendRecoveryWarning(recovered, &adminv1.OperationWarning{
					Code:    "SCRAP_OPERATION_RESTART_REQUEUED",
					Message: "operation was running during process restart and was requeued for idempotent retry",
				})
				candidates = append(candidates, recoveryUpdate{
					expected:  operation,
					recovered: recovered,
					kind:      recoveryUpdateRequeued,
				})
				continue
			}
			recovered.State = adminv1.OperationState_OPERATION_STATE_FAILED
			recovered.FinishedAt = timestamppb.New(now)
			appendRecoveryWarning(recovered, &adminv1.OperationWarning{
				Code:    "SCRAP_OPERATION_RECOVERY_UNSUPPORTED",
				Message: "operation was running during process restart but this binary cannot resume its type",
			})
			recovered.LastError = &adminv1.OperationError{
				Code:    "SCRAP_OPERATION_RECOVERY_UNSUPPORTED",
				Message: fmt.Sprintf("operation type %q cannot be resumed after restart", recovered.GetOperationType()),
			}
			candidates = append(candidates, recoveryUpdate{
				expected:  operation,
				recovered: recovered,
				kind:      recoveryUpdateFailedUnsupported,
			})
		default:
			if isTerminal(operation.GetState()) {
				result.Terminal++
				continue
			}
			result.Ignored++
		}
	}
	verified, skipped, err := s.verifyRecoveryUpdates(candidates)
	if err != nil {
		return result, err
	}
	result.Ignored += skipped
	updates := make([]*adminv1.Operation, 0, len(verified))
	for _, candidate := range verified {
		updates = append(updates, candidate.recovered)
		switch candidate.kind {
		case recoveryUpdateRequeued:
			result.Requeued++
		case recoveryUpdateFailedUnsupported:
			result.FailedUnsupported++
		}
	}
	if err := s.putBatch(updates); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) AppendAuditEvent(event *adminv1.AuditEvent) error {
	if err := validateAuditEvent(event); err != nil {
		return err
	}
	value, err := protoMarshal.Marshal(event)
	if err != nil {
		return err
	}
	key := auditEventKey(event.GetEventId())
	if existing, ok, err := s.get(key); err != nil {
		return err
	} else if ok {
		if bytes.Equal(existing, value) {
			return nil
		}
		return fmt.Errorf("%w: audit event already exists with different metadata", ErrConflict)
	}
	return s.db.Set(key, value, pebble.Sync)
}

func (s *Store) ListAuditEvents() ([]*adminv1.AuditEvent, error) {
	prefix := auditEventPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(iter)

	var events []*adminv1.AuditEvent
	for valid := iter.First(); valid; valid = iter.Next() {
		event, err := unmarshalAuditEvent(iter.Value())
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return events, nil
}

func validateOperation(operation *adminv1.Operation) error {
	if operation == nil {
		return fmt.Errorf("%w: operation is required", ErrInvalid)
	}
	if operation.GetOperationId() == "" {
		return fmt.Errorf("%w: operation_id is required", ErrInvalid)
	}
	if operation.GetOperationType() == "" {
		return fmt.Errorf("%w: operation_type is required", ErrInvalid)
	}
	if operation.GetState() == adminv1.OperationState_OPERATION_STATE_UNSPECIFIED {
		return fmt.Errorf("%w: state is required", ErrInvalid)
	}
	return nil
}

func validateAuditEvent(event *adminv1.AuditEvent) error {
	if event == nil {
		return fmt.Errorf("%w: audit event is required", ErrInvalid)
	}
	if event.GetEventId() == "" {
		return fmt.Errorf("%w: audit event_id is required", ErrInvalid)
	}
	if event.GetEventType() == "" {
		return fmt.Errorf("%w: audit event_type is required", ErrInvalid)
	}
	if event.GetActorIdentity() == "" {
		return fmt.Errorf("%w: audit actor_identity is required", ErrInvalid)
	}
	if event.GetOccurredAt() == nil {
		return fmt.Errorf("%w: audit occurred_at is required", ErrInvalid)
	}
	if err := event.GetOccurredAt().CheckValid(); err != nil {
		return fmt.Errorf("%w: audit occurred_at is invalid", ErrInvalid)
	}
	return nil
}

func validateOperationPlan(plan *adminv1.OperationPlan) error {
	if plan == nil {
		return fmt.Errorf("%w: operation plan is required", ErrInvalid)
	}
	if plan.GetOperationPlanId() == "" {
		return fmt.Errorf("%w: operation_plan_id is required", ErrInvalid)
	}
	if plan.GetPlanHash() == "" {
		return fmt.Errorf("%w: plan_hash is required", ErrInvalid)
	}
	if plan.GetExpiresAt() == nil {
		return fmt.Errorf("%w: expires_at is required", ErrInvalid)
	}
	if err := plan.GetExpiresAt().CheckValid(); err != nil {
		return fmt.Errorf("%w: expires_at is invalid", ErrInvalid)
	}
	if len(plan.GetTargets()) == 0 {
		return fmt.Errorf("%w: at least one target is required", ErrInvalid)
	}
	return nil
}

func isTerminal(state adminv1.OperationState) bool {
	switch state {
	case adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
		adminv1.OperationState_OPERATION_STATE_FAILED,
		adminv1.OperationState_OPERATION_STATE_CANCELED,
		adminv1.OperationState_OPERATION_STATE_EXPIRED:
		return true
	default:
		return false
	}
}

func cloneOperation(operation *adminv1.Operation) *adminv1.Operation {
	if operation == nil {
		return nil
	}
	return proto.Clone(operation).(*adminv1.Operation)
}

func appendRecoveryWarning(operation *adminv1.Operation, warning *adminv1.OperationWarning) {
	for _, existing := range operation.GetWarnings() {
		if existing.GetCode() == warning.GetCode() {
			return
		}
	}
	operation.Warnings = append(operation.Warnings, warning)
}

func unmarshalOperation(data []byte) (*adminv1.Operation, error) {
	var operation adminv1.Operation
	if err := proto.Unmarshal(data, &operation); err != nil {
		return nil, err
	}
	if err := validateOperation(&operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func unmarshalOperationPlan(data []byte) (*adminv1.OperationPlan, error) {
	var plan adminv1.OperationPlan
	if err := proto.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if err := validateOperationPlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func unmarshalAuditEvent(data []byte) (*adminv1.AuditEvent, error) {
	var event adminv1.AuditEvent
	if err := proto.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if err := validateAuditEvent(&event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *Store) get(key []byte) ([]byte, bool, error) {
	value, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer closeutil.Ignore(closer)
	return append([]byte(nil), value...), true, nil
}

func (s *Store) verifyRecoveryUpdates(candidates []recoveryUpdate) ([]recoveryUpdate, int, error) {
	if len(candidates) == 0 {
		return nil, 0, nil
	}
	verified := make([]recoveryUpdate, 0, len(candidates))
	skipped := 0
	for _, candidate := range candidates {
		latest, err := s.Get(candidate.expected.GetOperationId())
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				skipped++
				continue
			}
			return nil, skipped, err
		}
		if latest.GetState() != adminv1.OperationState_OPERATION_STATE_RUNNING || !proto.Equal(latest, candidate.expected) {
			skipped++
			continue
		}
		verified = append(verified, candidate)
	}
	return verified, skipped, nil
}

func (s *Store) putBatch(operations []*adminv1.Operation) error {
	if len(operations) == 0 {
		return nil
	}
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	for _, operation := range operations {
		if err := validateOperation(operation); err != nil {
			return err
		}
		value, err := protoMarshal.Marshal(operation)
		if err != nil {
			return err
		}
		if err := batch.Set(operationKey(operation.GetOperationId()), value, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func operationPrefix() []byte {
	return []byte("operation\x00")
}

func operationKey(operationID string) []byte {
	return append(operationPrefix(), []byte(operationID)...)
}

func operationPlanPrefix() []byte {
	return []byte("operation_plan\x00")
}

func operationPlanKey(operationPlanID string) []byte {
	return append(operationPlanPrefix(), []byte(operationPlanID)...)
}

func auditEventPrefix() []byte {
	return []byte("audit_event\x00")
}

func auditEventKey(eventID string) []byte {
	return append(auditEventPrefix(), []byte(eventID)...)
}

func prefixUpperBound(prefix []byte) []byte {
	out := append([]byte(nil), prefix...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != 0xff {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}
