package operations

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrNotFound = errors.New("operations: not found")
	ErrInvalid  = errors.New("operations: invalid operation")
)

var protoMarshal = proto.MarshalOptions{Deterministic: true}

type Store struct {
	db *pebble.DB
}

type ListFilter struct {
	States        []adminv1.OperationState
	OperationType string
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

func (s *Store) List(filter ListFilter) ([]*adminv1.Operation, error) {
	prefix := operationPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

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

func (s *Store) get(key []byte) ([]byte, bool, error) {
	value, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

func operationPrefix() []byte {
	return []byte("operation\x00")
}

func operationKey(operationID string) []byte {
	return append(operationPrefix(), []byte(operationID)...)
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
