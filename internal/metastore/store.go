package metastore

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/petabytecl/scrap/internal/identity"
)

var (
	ErrNotFound                 = errors.New("metastore: not found")
	ErrConflict                 = errors.New("metastore: conflict")
	ErrInvalidRecord            = errors.New("metastore: invalid record")
	ErrUnsupportedSchemaVersion = errors.New("metastore: unsupported schema version")
)

type Store struct {
	db *pebble.DB
}

func Open(dir string) (*Store, error) {
	db, err := pebble.Open(filepath.Join(dir, "metadata"), &pebble.Options{})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) PutDocument(document Document) error {
	document = normalizeDocumentDefaults(document)
	value, err := marshalDocument(document)
	if err != nil {
		return err
	}
	docKey := documentKey(document.Identity)
	if existingValue, ok, err := s.get(docKey); err != nil {
		return err
	} else if ok {
		if bytes.Equal(existingValue, value) {
			return nil
		}
		return fmt.Errorf("%w: document already exists with different metadata", ErrConflict)
	}

	transaction, existed, err := s.getTransaction(document.Identity.TenantID, document.Identity.TransactionID)
	if err != nil {
		return err
	}
	if !existed {
		transaction = Transaction{
			Identity: identity.Transaction{
				TenantID:      document.Identity.TenantID,
				TransactionID: document.Identity.TransactionID,
			},
			State:     TransactionStateOpen,
			CreatedAt: document.CreatedAt,
		}
	}
	transaction.DocumentCount++
	switch document.DocumentClass {
	case DocumentClassPermanent:
		transaction.PermanentDocumentCount++
	case DocumentClassEphemeral:
		transaction.EphemeralDocumentCount++
	}

	transactionValue, err := marshalTransaction(transaction)
	if err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(docKey, value, nil); err != nil {
		return err
	}
	if err := batch.Set(transactionDocumentKey(document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(blockDocumentKey(document.Location.BlockID, document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(transactionKey(transaction.Identity), transactionValue, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) HeadDocument(doc identity.Document) (Document, error) {
	value, ok, err := s.get(documentKey(doc))
	if err != nil {
		return Document{}, err
	}
	if !ok {
		return Document{}, ErrNotFound
	}
	return unmarshalDocument(value)
}

func (s *Store) ListDocuments(filter DocumentFilter) ([]Document, error) {
	prefix := documentPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var documents []Document
	for valid := iter.First(); valid; valid = iter.Next() {
		document, err := unmarshalDocument(iter.Value())
		if err != nil {
			return nil, err
		}
		if matchesFilter(document, filter) {
			documents = append(documents, document)
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return documents, nil
}

func (s *Store) FindDocuments(transaction identity.Transaction, filter DocumentFilter) ([]Document, error) {
	prefix := transactionDocumentsPrefix(transaction)
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var documents []Document
	for valid := iter.First(); valid; valid = iter.Next() {
		document, err := unmarshalDocument(iter.Value())
		if err != nil {
			return nil, err
		}
		if matchesFilter(document, filter) {
			documents = append(documents, document)
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return documents, nil
}

func (s *Store) CompleteTransaction(transaction identity.Transaction, completedAt time.Time, tags map[string]string) (Transaction, error) {
	current, ok, err := s.getTransaction(transaction.TenantID, transaction.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	if !ok {
		return Transaction{}, ErrNotFound
	}
	current.State = TransactionStateCompleted
	current.CompletedAt = &completedAt
	current.Tags = cloneTags(tags)
	value, err := marshalTransaction(current)
	if err != nil {
		return Transaction{}, err
	}
	if err := s.db.Set(transactionKey(current.Identity), value, pebble.Sync); err != nil {
		return Transaction{}, err
	}
	return current, nil
}

func (s *Store) RecordUploadIntent(intent UploadIntent) error {
	if intent.State == 0 {
		intent.State = UploadStatePending
	}
	if err := validateUploadIntentState(intent.State); err != nil {
		return err
	}
	if intent.UpdatedAt.IsZero() {
		intent.UpdatedAt = time.Now().UTC()
	}
	value, err := marshalUploadIntent(intent)
	if err != nil {
		return err
	}
	key := uploadIntentKey(intent.BlockID)
	if existingValue, ok, err := s.get(key); err != nil {
		return err
	} else if ok {
		existing, err := unmarshalUploadIntent(existingValue)
		if err != nil {
			return err
		}
		if sameUploadIntentDestination(existing, intent) {
			return nil
		}
		return fmt.Errorf("%w: upload intent already exists with different metadata", ErrConflict)
	}
	return s.db.Set(key, value, pebble.Sync)
}

func (s *Store) UpdateUploadIntentState(blockID string, state UploadState, lastError string, updatedAt time.Time) (UploadIntent, error) {
	if err := validateUploadIntentState(state); err != nil {
		return UploadIntent{}, err
	}
	intent, err := s.GetUploadIntent(blockID)
	if err != nil {
		return UploadIntent{}, err
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	intent.State = state
	intent.LastError = lastError
	intent.HasLastError = lastError != ""
	intent.UpdatedAt = updatedAt
	value, err := marshalUploadIntent(intent)
	if err != nil {
		return UploadIntent{}, err
	}
	if err := s.db.Set(uploadIntentKey(intent.BlockID), value, pebble.Sync); err != nil {
		return UploadIntent{}, err
	}
	return intent, nil
}

func (s *Store) UpdateDocumentRestoreState(doc identity.Document, state RestoreState) (Document, error) {
	availability, err := availabilityFromRestoreState(state)
	if err != nil {
		return Document{}, err
	}
	document, err := s.HeadDocument(doc)
	if err != nil {
		return Document{}, err
	}
	document.RestoreState = state
	document.Availability = availability
	if err := s.replaceDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) UpdateTransactionRestoreState(transaction identity.Transaction, state RestoreState) ([]Document, error) {
	availability, err := availabilityFromRestoreState(state)
	if err != nil {
		return nil, err
	}
	documents, err := s.FindDocuments(transaction, DocumentFilter{})
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, ErrNotFound
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	for i := range documents {
		documents[i].RestoreState = state
		documents[i].Availability = availability
		value, err := marshalDocument(documents[i])
		if err != nil {
			return nil, err
		}
		if err := batch.Set(documentKey(documents[i].Identity), value, nil); err != nil {
			return nil, err
		}
		if err := batch.Set(transactionDocumentKey(documents[i].Identity), value, nil); err != nil {
			return nil, err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return nil, err
	}
	return documents, nil
}

func (s *Store) TombstoneDocument(doc identity.Document, tombstonedAt time.Time, operationID string) (Document, error) {
	if tombstonedAt.IsZero() {
		return Document{}, fmt.Errorf("metastore: tombstone requires tombstoned_at")
	}
	if operationID == "" {
		return Document{}, fmt.Errorf("metastore: tombstone requires operation_id")
	}
	document, err := s.HeadDocument(doc)
	if err != nil {
		return Document{}, err
	}
	document.LifecycleState = LifecycleStateTombstoned
	document.TombstonedAt = &tombstonedAt
	document.TombstoneOperationID = operationID
	document.HasTombstoneOperationID = operationID != ""
	if err := s.replaceDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) RecordRepairState(state RepairState) error {
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	value, err := marshalRepairState(state)
	if err != nil {
		return err
	}
	return s.db.Set(repairStateKey(state.Identity, state.IncidentID), value, pebble.Sync)
}

func (s *Store) GetRepairState(doc identity.Document, incidentID string) (RepairState, error) {
	value, ok, err := s.get(repairStateKey(doc, incidentID))
	if err != nil {
		return RepairState{}, err
	}
	if !ok {
		return RepairState{}, ErrNotFound
	}
	return unmarshalRepairState(value)
}

func (s *Store) ListRepairStates() ([]RepairState, error) {
	prefix := repairStatesPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var states []RepairState
	for valid := iter.First(); valid; valid = iter.Next() {
		state, err := unmarshalRepairState(iter.Value())
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return states, nil
}

func (s *Store) GetUploadIntent(blockID string) (UploadIntent, error) {
	value, ok, err := s.get(uploadIntentKey(blockID))
	if err != nil {
		return UploadIntent{}, err
	}
	if !ok {
		return UploadIntent{}, ErrNotFound
	}
	return unmarshalUploadIntent(value)
}

func (s *Store) ListUploadIntents() ([]UploadIntent, error) {
	prefix := uploadIntentPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var intents []UploadIntent
	for valid := iter.First(); valid; valid = iter.Next() {
		intent, err := unmarshalUploadIntent(iter.Value())
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return intents, nil
}

func (s *Store) ListBlockDocuments(blockID string) ([]Document, error) {
	prefix := blockDocumentsPrefix(blockID)
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var documents []Document
	for valid := iter.First(); valid; valid = iter.Next() {
		document, err := unmarshalDocument(iter.Value())
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return documents, nil
}

func (s *Store) GetTransaction(transaction identity.Transaction) (Transaction, error) {
	current, ok, err := s.getTransaction(transaction.TenantID, transaction.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	if !ok {
		return Transaction{}, ErrNotFound
	}
	return current, nil
}

func (s *Store) ListTransactions() ([]Transaction, error) {
	prefix := transactionPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var transactions []Transaction
	for valid := iter.First(); valid; valid = iter.Next() {
		transaction, err := unmarshalTransaction(iter.Value())
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, transaction)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return transactions, nil
}

func (s *Store) replaceDocument(document Document) error {
	value, err := marshalDocument(document)
	if err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(documentKey(document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(transactionDocumentKey(document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(blockDocumentKey(document.Location.BlockID, document.Identity), value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) getTransaction(tenantID string, transactionID string) (Transaction, bool, error) {
	transaction := identity.Transaction{TenantID: tenantID, TransactionID: transactionID}
	value, ok, err := s.get(transactionKey(transaction))
	if err != nil || !ok {
		return Transaction{}, ok, err
	}
	decoded, err := unmarshalTransaction(value)
	return decoded, true, err
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

func matchesFilter(document Document, filter DocumentFilter) bool {
	if filter.HasDocumentNameExact && document.Identity.DocumentName != filter.DocumentNameExact {
		return false
	}
	if filter.HasDocumentNamePrefix && !strings.HasPrefix(document.Identity.DocumentName, filter.DocumentNamePrefix) {
		return false
	}
	if filter.HasDocumentClass && document.DocumentClass != filter.DocumentClass {
		return false
	}
	if filter.HasContentType && (!document.HasContentType || document.ContentType != filter.ContentType) {
		return false
	}
	if filter.HasWorkflowStage && (!document.HasWorkflowStage || document.WorkflowStage != filter.WorkflowStage) {
		return false
	}
	if filter.HasCreatedByService && document.CreatedByService != filter.CreatedByService {
		return false
	}
	if filter.CreatedAfter != nil && document.CreatedAt.Before(*filter.CreatedAfter) {
		return false
	}
	if filter.CreatedBefore != nil && document.CreatedAt.After(*filter.CreatedBefore) {
		return false
	}
	for key, value := range filter.Tags {
		if document.Tags[key] != value {
			return false
		}
	}
	return true
}

func documentKey(doc identity.Document) []byte {
	return []byte("document\x00" + doc.TenantID + "\x00" + doc.TransactionID + "\x00" + doc.DocumentName)
}

func documentPrefix() []byte {
	return []byte("document\x00")
}

func transactionPrefix() []byte {
	return []byte("transaction\x00")
}

func transactionKey(transaction identity.Transaction) []byte {
	return append(transactionPrefix(), []byte(transaction.TenantID+"\x00"+transaction.TransactionID)...)
}

func transactionDocumentsPrefix(transaction identity.Transaction) []byte {
	return []byte("document_by_transaction\x00" + transaction.TenantID + "\x00" + transaction.TransactionID + "\x00")
}

func transactionDocumentKey(doc identity.Document) []byte {
	return append(transactionDocumentsPrefix(identity.Transaction{
		TenantID:      doc.TenantID,
		TransactionID: doc.TransactionID,
	}), []byte(doc.DocumentName)...)
}

func blockDocumentsPrefix(blockID string) []byte {
	return []byte("document_by_block\x00" + blockID + "\x00")
}

func blockDocumentKey(blockID string, doc identity.Document) []byte {
	return append(blockDocumentsPrefix(blockID), []byte(doc.TenantID+"\x00"+doc.TransactionID+"\x00"+doc.DocumentName)...)
}

func uploadIntentPrefix() []byte {
	return []byte("upload_intent\x00")
}

func uploadIntentKey(blockID string) []byte {
	return append(uploadIntentPrefix(), []byte(blockID)...)
}

func repairStatesPrefix() []byte {
	return []byte("repair_state\x00")
}

func repairStatePrefix(doc identity.Document) []byte {
	return append(repairStatesPrefix(), []byte(doc.TenantID+"\x00"+doc.TransactionID+"\x00"+doc.DocumentName+"\x00")...)
}

func repairStateKey(doc identity.Document, incidentID string) []byte {
	return append(repairStatePrefix(doc), []byte(incidentID)...)
}

func validateUploadIntentState(state UploadState) error {
	switch state {
	case UploadStatePending, UploadStateUploaded, UploadStateFailed:
		return nil
	default:
		return fmt.Errorf("metastore: unsupported upload intent state %d", state)
	}
}

func sameUploadIntentDestination(left UploadIntent, right UploadIntent) bool {
	return left.BlockID == right.BlockID &&
		left.BackendObjectKey == right.BackendObjectKey &&
		left.IndexObjectKey == right.IndexObjectKey &&
		left.EnvelopeObjectKey == right.EnvelopeObjectKey
}

func availabilityFromRestoreState(state RestoreState) (Availability, error) {
	switch state {
	case RestoreStateHot:
		return AvailabilityHot, nil
	case RestoreStateCold:
		return AvailabilityCold, nil
	case RestoreStateRestorePending:
		return AvailabilityRestorePending, nil
	case RestoreStateCryptoUnavailable:
		return AvailabilityCryptoUnavailable, nil
	default:
		return 0, fmt.Errorf("metastore: unsupported restore state %d", state)
	}
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
