package metastore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/petabytecl/scrap/internal/closeutil"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
)

var (
	ErrNotFound                 = errors.New("metastore: not found")
	ErrConflict                 = errors.New("metastore: conflict")
	ErrInvalidRecord            = errors.New("metastore: invalid record")
	ErrTransactionClosed        = errors.New("metastore: transaction is closed")
	ErrUnsupportedSchemaVersion = errors.New("metastore: unsupported schema version")
)

const (
	maxKeyByte          = 0xff
	unixNanoSortSignBit = uint64(1) << 63
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
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	if err := s.putDocument(batch, document); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) putDocument(batch *pebble.Batch, document Document) error {
	document = normalizeDocumentDefaults(document)
	value, err := marshalDocument(document)
	if err != nil {
		return err
	}
	docKey := documentKey(document.Identity)
	if stored, err := s.documentAlreadyStored(docKey, value); err != nil || stored {
		return err
	}
	transaction, err := s.transactionForDocument(document)
	if err != nil {
		return err
	}
	if transaction.State != TransactionStateOpen {
		return fmt.Errorf("%w: transaction %s/%s is not open", ErrTransactionClosed, transaction.Identity.TenantID, transaction.Identity.TransactionID)
	}
	transaction = incrementTransactionDocumentCount(transaction, document.DocumentClass)
	transactionValue, err := marshalTransaction(transaction)
	if err != nil {
		return err
	}
	return s.putDocumentIndexes(batch, document, docKey, value, transactionValue, transaction)
}

func incrementTransactionDocumentCount(transaction Transaction, class DocumentClass) Transaction {
	transaction.DocumentCount++
	switch class {
	case DocumentClassPermanent:
		transaction.PermanentDocumentCount++
	case DocumentClassEphemeral:
		transaction.EphemeralDocumentCount++
	}
	return transaction
}

func (s *Store) putDocumentIndexes(batch *pebble.Batch, document Document, docKey, value, transactionValue []byte, transaction Transaction) error {
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
	return nil
}

func (s *Store) documentAlreadyStored(docKey, value []byte) (bool, error) {
	existingValue, ok, err := s.get(docKey)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if bytes.Equal(existingValue, value) {
		return true, nil
	}
	return false, fmt.Errorf("%w: document already exists with different metadata", ErrConflict)
}

func (s *Store) transactionForDocument(document Document) (Transaction, error) {
	transaction, existed, err := s.getTransaction(document.Identity.TenantID, document.Identity.TransactionID)
	if err != nil || existed {
		return transaction, err
	}
	return Transaction{
		Identity: identity.Transaction{
			TenantID:      document.Identity.TenantID,
			TransactionID: document.Identity.TransactionID,
		},
		State:     TransactionStateOpen,
		CreatedAt: document.CreatedAt,
	}, nil
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
	defer closeutil.Ignore(iter)

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

func (s *Store) ApplyShardSnapshot(snapshot *metastorev1.ShardSnapshot) error {
	if _, err := MarshalShardSnapshot(snapshot); err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)

	if err := clearSnapshotBatch(batch); err != nil {
		return err
	}
	if err := applySnapshotDocuments(batch, snapshot.GetDocuments()); err != nil {
		return err
	}
	if err := applySnapshotTransactions(batch, snapshot.GetTransactions()); err != nil {
		return err
	}
	if err := applySnapshotUploadIntents(batch, snapshot.GetUploadIntents()); err != nil {
		return err
	}
	if err := applySnapshotRepairStates(batch, snapshot.GetRepairStates()); err != nil {
		return err
	}
	if err := applySnapshotCommandReceipts(batch, snapshot.GetCommandReceipts()); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func clearSnapshotBatch(batch *pebble.Batch) error {
	for _, prefix := range [][]byte{
		documentPrefix(),
		transactionDocumentsRootPrefix(),
		blockDocumentsRootPrefix(),
		transactionPrefix(),
		uploadIntentPrefix(),
		processableUploadIntentPrefix(),
		repairStatesPrefix(),
		commandReceiptPrefix(),
	} {
		if err := batch.DeleteRange(prefix, prefixUpperBound(prefix), nil); err != nil {
			return err
		}
	}
	return nil
}

func applySnapshotDocuments(batch *pebble.Batch, records []*metastorev1.DocumentRecord) error {
	for _, record := range records {
		document := documentFromProto(record)
		value, err := marshalDocument(document)
		if err != nil {
			return err
		}
		if err := batch.Set(documentKey(document.Identity), value, nil); err != nil {
			return err
		}
		if err := batch.Set(transactionDocumentKey(document.Identity), value, nil); err != nil {
			return err
		}
		if err := batch.Set(blockDocumentKey(document.Location.BlockID, document.Identity), value, nil); err != nil {
			return err
		}
	}
	return nil
}

func applySnapshotTransactions(batch *pebble.Batch, records []*metastorev1.TransactionRecord) error {
	for _, record := range records {
		transaction := transactionFromProto(record)
		value, err := marshalTransaction(transaction)
		if err != nil {
			return err
		}
		if err := batch.Set(transactionKey(transaction.Identity), value, nil); err != nil {
			return err
		}
	}
	return nil
}

func applySnapshotUploadIntents(batch *pebble.Batch, records []*metastorev1.UploadIntentRecord) error {
	for _, record := range records {
		intent := uploadIntentFromProto(record)
		value, err := marshalUploadIntent(intent)
		if err != nil {
			return err
		}
		if err := setUploadIntentIndexes(batch, intent, value); err != nil {
			return err
		}
	}
	return nil
}

func applySnapshotRepairStates(batch *pebble.Batch, records []*metastorev1.RepairStateRecord) error {
	for _, record := range records {
		state := repairStateFromProto(record)
		value, err := marshalRepairState(state)
		if err != nil {
			return err
		}
		if err := batch.Set(repairStateKey(state.Identity, state.IncidentID), value, nil); err != nil {
			return err
		}
	}
	return nil
}

func applySnapshotCommandReceipts(batch *pebble.Batch, records []*metastorev1.CommandReceiptRecord) error {
	for _, record := range records {
		receipt := commandReceiptFromProto(record)
		value, err := marshalCommandReceipt(receipt)
		if err != nil {
			return err
		}
		if err := batch.Set(commandReceiptKey(receipt.CommandID), value, nil); err != nil {
			return err
		}
	}
	return nil
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
	defer closeutil.Ignore(iter)

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
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	current, err := s.completeTransaction(batch, transaction, completedAt, tags)
	if err != nil {
		return Transaction{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return Transaction{}, err
	}
	return current, nil
}

func (s *Store) completeTransaction(batch *pebble.Batch, transaction identity.Transaction, completedAt time.Time, tags map[string]string) (Transaction, error) {
	current, ok, err := s.getTransaction(transaction.TenantID, transaction.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	if !ok {
		return Transaction{}, ErrNotFound
	}
	if current.State == TransactionStateCompleted {
		return current, nil
	}
	if current.State != TransactionStateOpen {
		return Transaction{}, fmt.Errorf("%w: transaction %s/%s is not open", ErrTransactionClosed, current.Identity.TenantID, current.Identity.TransactionID)
	}
	current.State = TransactionStateCompleted
	current.CompletedAt = &completedAt
	current.Tags = cloneTags(tags)
	value, err := marshalTransaction(current)
	if err != nil {
		return Transaction{}, err
	}
	if err := batch.Set(transactionKey(current.Identity), value, nil); err != nil {
		return Transaction{}, err
	}
	return current, nil
}

func (s *Store) RecordUploadIntent(intent UploadIntent) error {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	if err := s.recordUploadIntent(batch, intent); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) recordUploadIntent(batch *pebble.Batch, intent UploadIntent) error {
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
	return setUploadIntentIndexes(batch, intent, value)
}

func setUploadIntentIndexes(batch *pebble.Batch, intent UploadIntent, value []byte) error {
	if err := batch.Set(uploadIntentKey(intent.BlockID), value, nil); err != nil {
		return err
	}
	if shouldProcessUploadIntent(intent) {
		return batch.Set(processableUploadIntentKey(intent), value, nil)
	}
	return nil
}

func deleteProcessableUploadIntentIndex(batch *pebble.Batch, intent UploadIntent) error {
	if !shouldProcessUploadIntent(intent) {
		return nil
	}
	return batch.Delete(processableUploadIntentKey(intent), nil)
}

func (s *Store) UpdateUploadIntentState(blockID string, state UploadState, lastError string, updatedAt time.Time) (UploadIntent, error) {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	intent, err := s.updateUploadIntentState(batch, blockID, state, lastError, updatedAt)
	if err != nil {
		return UploadIntent{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return UploadIntent{}, err
	}
	return intent, nil
}

func (s *Store) updateUploadIntentState(batch *pebble.Batch, blockID string, state UploadState, lastError string, updatedAt time.Time) (UploadIntent, error) {
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
	if err := deleteProcessableUploadIntentIndex(batch, intent); err != nil {
		return UploadIntent{}, err
	}
	intent.State = state
	intent.LastError = lastError
	intent.HasLastError = lastError != ""
	intent.UpdatedAt = updatedAt
	value, err := marshalUploadIntent(intent)
	if err != nil {
		return UploadIntent{}, err
	}
	if err := setUploadIntentIndexes(batch, intent, value); err != nil {
		return UploadIntent{}, err
	}
	return intent, nil
}

func (s *Store) UpdateDocumentRestoreState(doc identity.Document, state RestoreState) (Document, error) {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	document, err := s.updateDocumentRestoreState(batch, doc, state)
	if err != nil {
		return Document{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) updateDocumentRestoreState(batch *pebble.Batch, doc identity.Document, state RestoreState) (Document, error) {
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
	if err := s.replaceDocument(batch, document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) UpdateTransactionRestoreState(transaction identity.Transaction, state RestoreState) ([]Document, error) {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	documents, err := s.updateTransactionRestoreState(batch, transaction, state)
	if err != nil {
		return nil, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return nil, err
	}
	return documents, nil
}

func (s *Store) updateTransactionRestoreState(batch *pebble.Batch, transaction identity.Transaction, state RestoreState) ([]Document, error) {
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
	return documents, nil
}

func (s *Store) TombstoneDocument(doc identity.Document, tombstonedAt time.Time, operationID string) (Document, error) {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	document, err := s.tombstoneDocument(batch, doc, tombstonedAt, operationID)
	if err != nil {
		return Document{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) tombstoneDocument(batch *pebble.Batch, doc identity.Document, tombstonedAt time.Time, operationID string) (Document, error) {
	if tombstonedAt.IsZero() {
		return Document{}, errors.New("metastore: tombstone requires tombstoned_at")
	}
	if operationID == "" {
		return Document{}, errors.New("metastore: tombstone requires operation_id")
	}
	document, err := s.HeadDocument(doc)
	if err != nil {
		return Document{}, err
	}
	document.LifecycleState = LifecycleStateTombstoned
	document.TombstonedAt = &tombstonedAt
	document.TombstoneOperationID = operationID
	document.HasTombstoneOperationID = operationID != ""
	if err := s.replaceDocument(batch, document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) RecordRepairState(state RepairState) error {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	if err := s.recordRepairState(batch, state); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) recordRepairState(batch *pebble.Batch, state RepairState) error {
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	value, err := marshalRepairState(state)
	if err != nil {
		return err
	}
	return batch.Set(repairStateKey(state.Identity, state.IncidentID), value, nil)
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
	defer closeutil.Ignore(iter)

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

func (s *Store) RecordCommandReceipt(receipt CommandReceipt) error {
	batch := s.db.NewBatch()
	defer closeutil.Ignore(batch)
	if err := s.recordCommandReceipt(batch, receipt); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) recordCommandReceipt(batch *pebble.Batch, receipt CommandReceipt) error {
	value, err := marshalCommandReceipt(receipt)
	if err != nil {
		return err
	}
	if existingValue, ok, err := s.get(commandReceiptKey(receipt.CommandID)); err != nil {
		return err
	} else if ok {
		existing, err := unmarshalCommandReceipt(existingValue)
		if err != nil {
			return err
		}
		if existing.CommandSHA256 == receipt.CommandSHA256 {
			return nil
		}
		return fmt.Errorf("%w: command id already applied with different payload", ErrConflict)
	}
	return batch.Set(commandReceiptKey(receipt.CommandID), value, nil)
}

func (s *Store) GetCommandReceipt(commandID string) (CommandReceipt, error) {
	value, ok, err := s.get(commandReceiptKey(commandID))
	if err != nil {
		return CommandReceipt{}, err
	}
	if !ok {
		return CommandReceipt{}, ErrNotFound
	}
	return unmarshalCommandReceipt(value)
}

func (s *Store) ListCommandReceipts() ([]CommandReceipt, error) {
	prefix := commandReceiptPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(iter)

	var receipts []CommandReceipt
	for valid := iter.First(); valid; valid = iter.Next() {
		receipt, err := unmarshalCommandReceipt(iter.Value())
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return receipts, nil
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
	return s.listUploadIntents(0)
}

func (s *Store) ListPendingUploadIntents(limit int) ([]UploadIntent, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: pending upload intent limit must be positive", ErrInvalidRecord)
	}
	return s.listProcessableUploadIntents(limit)
}

func (s *Store) listUploadIntents(limit int) ([]UploadIntent, error) {
	prefix := uploadIntentPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(iter)

	var intents []UploadIntent
	for valid := iter.First(); valid; valid = iter.Next() {
		intent, err := unmarshalUploadIntent(iter.Value())
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
		if limit > 0 && len(intents) >= limit {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return intents, nil
}

func (s *Store) listProcessableUploadIntents(limit int) ([]UploadIntent, error) {
	prefix := processableUploadIntentPrefix()
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(iter)

	intents := make([]UploadIntent, 0, limit)
	for valid := iter.First(); valid; valid = iter.Next() {
		intent, err := unmarshalUploadIntent(iter.Value())
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
		if len(intents) >= limit {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return intents, nil
}

func shouldProcessUploadIntent(intent UploadIntent) bool {
	return intent.State == UploadStatePending || intent.State == UploadStateFailed
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
	defer closeutil.Ignore(iter)

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
	defer closeutil.Ignore(iter)

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

func (s *Store) replaceDocument(batch *pebble.Batch, document Document) error {
	value, err := marshalDocument(document)
	if err != nil {
		return err
	}
	if err := batch.Set(documentKey(document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(transactionDocumentKey(document.Identity), value, nil); err != nil {
		return err
	}
	if err := batch.Set(blockDocumentKey(document.Location.BlockID, document.Identity), value, nil); err != nil {
		return err
	}
	return nil
}

func (s *Store) getTransaction(tenantID, transactionID string) (Transaction, bool, error) {
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
	defer closeutil.Ignore(closer)
	return append([]byte(nil), value...), true, nil
}

func matchesFilter(document Document, filter DocumentFilter) bool {
	return matchesIdentityFilter(document, filter) &&
		matchesAttributeFilter(document, filter) &&
		matchesTimeFilter(document, filter) &&
		matchesTagFilter(document, filter)
}

func matchesIdentityFilter(document Document, filter DocumentFilter) bool {
	if filter.HasDocumentNameExact && document.Identity.DocumentName != filter.DocumentNameExact {
		return false
	}
	if filter.HasDocumentNamePrefix && !strings.HasPrefix(document.Identity.DocumentName, filter.DocumentNamePrefix) {
		return false
	}
	return true
}

func matchesAttributeFilter(document Document, filter DocumentFilter) bool {
	if filter.HasDocumentClass && document.DocumentClass != filter.DocumentClass {
		return false
	}
	if !matchesOptionalString(document.HasContentType, document.ContentType, filter.HasContentType, filter.ContentType) {
		return false
	}
	if !matchesOptionalString(document.HasWorkflowStage, document.WorkflowStage, filter.HasWorkflowStage, filter.WorkflowStage) {
		return false
	}
	if filter.HasCreatedByService && document.CreatedByService != filter.CreatedByService {
		return false
	}
	return true
}

func matchesOptionalString(hasValue bool, value string, hasFilter bool, filter string) bool {
	return !hasFilter || (hasValue && value == filter)
}

func matchesTimeFilter(document Document, filter DocumentFilter) bool {
	if filter.CreatedAfter != nil && document.CreatedAt.Before(*filter.CreatedAfter) {
		return false
	}
	if filter.CreatedBefore != nil && document.CreatedAt.After(*filter.CreatedBefore) {
		return false
	}
	return true
}

func matchesTagFilter(document Document, filter DocumentFilter) bool {
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

func transactionDocumentsRootPrefix() []byte {
	return []byte("document_by_transaction\x00")
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

func blockDocumentsRootPrefix() []byte {
	return []byte("document_by_block\x00")
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

func processableUploadIntentPrefix() []byte {
	return []byte("upload_intent_processable\x00")
}

func processableUploadIntentKey(intent UploadIntent) []byte {
	prefix := processableUploadIntentPrefix()
	key := make([]byte, 0, len(prefix)+len(intent.BlockID)+9)
	key = append(key, prefix...)
	key = binary.BigEndian.AppendUint64(key, uploadIntentUpdatedAtSortKey(intent.UpdatedAt))
	key = append(key, 0)
	return append(key, []byte(intent.BlockID)...)
}

func uploadIntentUpdatedAtSortKey(updatedAt time.Time) uint64 {
	return uint64(updatedAt.UTC().UnixNano()) ^ unixNanoSortSignBit
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

func commandReceiptPrefix() []byte {
	return []byte("command_receipt\x00")
}

func commandReceiptKey(commandID string) []byte {
	return append(commandReceiptPrefix(), []byte(commandID)...)
}

func validateUploadIntentState(state UploadState) error {
	switch state {
	case UploadStatePending, UploadStateUploaded, UploadStateFailed:
		return nil
	default:
		return fmt.Errorf("metastore: unsupported upload intent state %d", state)
	}
}

func sameUploadIntentDestination(left, right UploadIntent) bool {
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
		if out[i] != maxKeyByte {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}
