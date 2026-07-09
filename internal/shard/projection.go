package shard

// Projection helpers: keep Document metadata lookup and projection mutation
// close to the Pebble Projection authority on Shard.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/scrub"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) scrubProjectionResult(scrubID string, entryIndex uint64) scrub.Result {
	s.mu.Lock()
	s.idx.SetAppliedIndex(entryIndex)
	_, hash, err := s.idx.StreamingHash()
	s.mu.Unlock()

	result := scrub.Result{
		ScrubID:      scrubID,
		AppliedIndex: entryIndex,
	}
	if err == nil {
		result.SHA256 = hash
	}
	return result
}

func (s *Shard) applyCommitDocument(doc *scrapv1.CommitDocument, entryIndex uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// ADR 0001 / ADR 0033: Raft apply must remain deterministic even when local
	// Document bytes lag replication. Byte readiness is tracked and enforced on
	// read serving / leadership eligibility (see ensureCommitDocumentBytesReadyLocked),
	// not by failing apply (which panics the Raft Ready loop).

	state, err := s.inspectCommitProjectionState(doc)
	if err != nil {
		return err
	}
	handled, err := s.handleExistingCommitProjection(doc, state)
	if handled || err != nil {
		return err
	}

	if len(doc.Sha256) != sha256.Size {
		return fmt.Errorf("%w: commit document SHA-256 length %d, want %d", storeapi.ErrDataLoss, len(doc.Sha256), sha256.Size)
	}
	// ADR 0034 / H-03: preflight Transaction cardinality before mutating .idx so
	// a MaxUint16 overflow cannot leave half-applied physical state.
	if !state.targetCounted {
		if err := preflightProjectionDocCount(s.idx, doc.TransactionId); err != nil {
			return err
		}
	}
	var sha [32]byte
	copy(sha[:], doc.Sha256)
	createdAt := time.UnixMicro(doc.CreatedAtUs)

	if err := s.appendDocumentIndexEntry(doc, block.IndexEntry{
		TransactionID:      doc.TransactionId,
		DocName:            doc.DocumentName,
		ContentType:        doc.ContentType,
		CreatedAt:          createdAt,
		FirstFrameOff:      safeUint64ToInt64(doc.FirstFrameOff),
		FrameCount:         doc.FrameCount,
		TotalBytes:         doc.TotalBytes,
		SHA256:             sha,
		EncryptionEnvelope: append([]byte(nil), doc.EncryptionEnvelope...),
	}, entryIndex, state.targetExists); err != nil {
		return err
	}

	if !state.targetCounted {
		if err := addProjectionDocument(s.idx, doc.TransactionId, doc.BlockId); err != nil {
			return err
		}
	}
	s.cleanupCommittedOpenlogPreps(doc)

	return nil
}

func (s *Shard) handleExistingCommitProjection(doc *scrapv1.CommitDocument, state commitProjectionState) (bool, error) {
	if !state.targetExists {
		return false, nil
	}
	if state.targetCounted {
		s.cleanupCommittedOpenlogPreps(doc)
		if commitDocumentMatchesExisting(doc, state.existing) {
			return true, nil
		}
		return true, duplicateDocumentConflictError()
	}
	if state.existingBlockID != doc.BlockId || !commitDocumentMatchesExisting(doc, state.existing) {
		return true, corruptDuplicateMetadataError()
	}
	return false, nil
}

type commitProjectionState struct {
	targetExists    bool
	targetCounted   bool
	existing        block.IndexEntry
	existingBlockID uint64
}

func (s *Shard) inspectCommitProjectionState(doc *scrapv1.CommitDocument) (commitProjectionState, error) {
	entry, exists, err := s.commitProjectionEntry(doc.TransactionId)
	if err != nil || !exists {
		return commitProjectionState{}, err
	}
	return s.inspectCommitProjectionEntry(doc, entry)
}

func (s *Shard) commitProjectionEntry(txID string) (index.Entry, bool, error) {
	if s.idx == nil {
		return index.Entry{}, false, fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	entry, err := s.idx.Get(txID)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			return index.Entry{}, false, nil
		}
		return index.Entry{}, false, fmt.Errorf("%w: projection transaction %s: %w", storeapi.ErrDataLoss, txID, err)
	}
	if len(entry.BlockIDs) == 0 {
		return index.Entry{}, false, fmt.Errorf("%w: projection transaction %s has no block IDs", storeapi.ErrDataLoss, txID)
	}
	return entry, true, nil
}

func (s *Shard) inspectCommitProjectionEntry(doc *scrapv1.CommitDocument, entry index.Entry) (commitProjectionState, error) {
	resolved := 0
	targetBlockProjected := false
	state := commitProjectionState{}
	for _, blockID := range entry.BlockIDs {
		if blockID == doc.BlockId {
			targetBlockProjected = true
		}
		entries, err := s.blockIndexEntriesForApply(blockID, doc.TransactionId)
		if err != nil {
			return commitProjectionState{}, err
		}
		state, resolved, err = inspectCommitProjectionBlock(doc, entry.DocCount, blockID, resolved, state, entries)
		if err != nil {
			return commitProjectionState{}, err
		}
	}

	if state.targetExists {
		return state, nil
	}
	return commitProjectionState{
		targetCounted: targetBlockProjected && int(entry.DocCount) > resolved,
	}, nil
}

func inspectCommitProjectionBlock(
	doc *scrapv1.CommitDocument,
	docCount uint16,
	blockID uint64,
	resolved int,
	state commitProjectionState,
	entries []block.IndexEntry,
) (commitProjectionState, int, error) {
	for _, blockDoc := range entries {
		resolved++
		if blockDoc.DocName != doc.DocumentName {
			continue
		}
		if state.targetExists {
			return commitProjectionState{}, resolved, corruptDuplicateMetadataError()
		}
		state = commitProjectionState{
			targetExists:    true,
			targetCounted:   resolved <= int(docCount),
			existing:        blockDoc,
			existingBlockID: blockID,
		}
	}
	return state, resolved, nil
}

func (s *Shard) blockIndexEntriesForApply(blockID uint64, txID string) ([]block.IndexEntry, error) {
	ir, err := block.OpenIndexReader(s.idxPath(blockID))
	if err != nil {
		if !errors.Is(err, block.ErrIdxCorrupt) {
			// Only a genuinely absent index means "no entries". Any other
			// failure (EIO, EACCES) must fail the apply-side conflict check
			// closed instead of silently bypassing duplicate detection.
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("shard: open historical read idx: %w", err)
		}
		if err := block.RepairIndexTail(s.idxPath(blockID)); err != nil {
			return nil, fmt.Errorf("shard: repair historical read idx: %w", err)
		}
		ir, err = block.OpenIndexReader(s.idxPath(blockID))
		if err != nil {
			return nil, fmt.Errorf("shard: open repaired historical read idx: %w", err)
		}
	}
	defer func() { _ = ir.Close() }()
	return ir.FindByTransaction(txID), nil
}

func (s *Shard) appendDocumentIndexEntry(doc *scrapv1.CommitDocument, entry block.IndexEntry, entryIndex uint64, alreadyIndexed bool) error {
	idxW := s.idxWriterForBlock(doc.BlockId)
	if idxW != nil {
		// Re-applying a committed entry already written to the open Block's index
		// (a crash between the .idx append and the projection count) must not
		// append a duplicate: a duplicate makes ListDocuments report ErrCorrupt
		// and deep scrub misattribute the Block. The sealed-Block branch below
		// makes the same check via blockIndexContainsDocumentRepairingTail.
		if alreadyIndexed {
			return nil
		}
		if err := idxW.Append(entry); err != nil {
			return fmt.Errorf("shard: apply write idx: %w", err)
		}
		return nil
	}
	uploadGeneration := uploadGenerationFromApplyIndex(entryIndex, doc.GetCreatedAtUs())

	contains, err := s.blockIndexContainsDocumentRepairingTail(doc.BlockId, doc.TransactionId, doc.DocumentName)
	if err != nil {
		return err
	}
	if contains {
		return s.requeueBlockUploadAfterIndexAppend(doc.BlockId, uploadGeneration)
	}

	idxW, err = block.OpenIndexWriter(s.idxPath(doc.BlockId))
	if err != nil {
		return fmt.Errorf("shard: open historical write idx: %w", err)
	}
	if err := idxW.Append(entry); err != nil {
		_ = idxW.Close()
		return fmt.Errorf("shard: append historical write idx: %w", err)
	}
	if err := idxW.Close(); err != nil {
		return fmt.Errorf("shard: close historical write idx: %w", err)
	}
	if err := s.requeueBlockUploadAfterIndexAppend(doc.BlockId, uploadGeneration); err != nil {
		return err
	}
	return nil
}

func (s *Shard) blockIndexContainsDocumentRepairingTail(blockID uint64, txID, docName string) (bool, error) {
	contains, err := s.blockIndexContainsDocument(blockID, txID, docName)
	if err == nil || !errors.Is(err, block.ErrIdxCorrupt) {
		return contains, err
	}
	if err := block.RepairIndexTail(s.idxPath(blockID)); err != nil {
		return false, fmt.Errorf("shard: repair historical write idx: %w", err)
	}
	return s.blockIndexContainsDocument(blockID, txID, docName)
}

func (s *Shard) requeueBlockUploadAfterIndexAppend(blockID uint64, uploadGeneration int64) error {
	if !s.upload.Enabled {
		return nil
	}
	sealedSize, err := s.sealedSizeForUploadRequeueLocked(blockID)
	if err != nil {
		return err
	}
	if err := s.idx.PutPendingUpload(index.PendingUpload{
		BlockID:          blockID,
		ShardID:          s.shardID,
		SealedSizeBytes:  sealedSize,
		SealedAtUs:       uploadGeneration,
		UploadGeneration: uploadGeneration,
	}); err != nil {
		return fmt.Errorf("shard: requeue historical block upload: %w", err)
	}
	if err := s.refreshUploadPressureLocked(); err != nil {
		return err
	}
	s.uploads.Notify()
	return nil
}

func (s *Shard) sealedSizeForUploadRequeueLocked(blockID uint64) (int64, error) {
	info, err := os.Stat(s.blockPath(blockID))
	if err == nil {
		return info.Size(), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("shard: stat historical block for upload: %w", err)
	}
	if pending, pendingErr := s.idx.GetPendingUpload(blockID); pendingErr == nil {
		return pending.SealedSizeBytes, nil
	} else if !errors.Is(pendingErr, index.ErrPendingUploadNotFound) {
		return 0, fmt.Errorf("shard: get pending upload size for requeue: %w", pendingErr)
	}
	if confirmed, confirmedErr := s.idx.GetConfirmedUpload(blockID); confirmedErr == nil {
		return confirmed.SealedSizeBytes, nil
	} else if !errors.Is(confirmedErr, index.ErrConfirmedUploadNotFound) {
		return 0, fmt.Errorf("shard: get confirmed upload size for requeue: %w", confirmedErr)
	}
	return 0, fmt.Errorf("shard: stat historical block for upload: %w", err)
}

func (s *Shard) blockIndexContainsDocument(blockID uint64, txID, docName string) (bool, error) {
	ir, err := block.OpenIndexReader(s.idxPath(blockID))
	if err != nil {
		return false, fmt.Errorf("shard: open historical read idx: %w", err)
	}
	defer func() { _ = ir.Close() }()

	_, err = ir.Find(txID, docName)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, block.ErrDocNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("shard: read historical write idx: %w", err)
}

func preflightProjectionDocCount(projection *index.Index, txID string) error {
	existing, err := projection.Get(txID)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("shard: get index: %w", err)
	}
	if existing.DocCount == math.MaxUint16 {
		return fmt.Errorf("shard: increment doc count: index: doc count at maximum %d", math.MaxUint16)
	}
	return nil
}

func addProjectionDocument(projection *index.Index, txID string, blockID uint64) error {
	existing, err := projection.Get(txID)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			if err := projection.Put(txID, blockID, 1, false); err != nil {
				return fmt.Errorf("shard: put index: %w", err)
			}
			return nil
		}
		return fmt.Errorf("shard: get index: %w", err)
	}

	if len(existing.BlockIDs) == 0 || blockID != existing.BlockIDs[len(existing.BlockIDs)-1] {
		if err := projection.AddBlockID(txID, blockID); err != nil {
			return fmt.Errorf("shard: add block id: %w", err)
		}
	}
	if err := projection.IncrementDocCount(txID); err != nil {
		return fmt.Errorf("shard: increment doc count: %w", err)
	}
	return nil
}

func (s *Shard) idxWriterForBlock(blockID uint64) *block.IndexWriter {
	if s.blockWriter != nil && s.blockWriter.BlockID() == blockID {
		return s.idxWriter
	}
	return nil
}

func (s *Shard) documentVisibleInProjectionLenient(txID, docName string) (bool, error) {
	exists, err := s.projectionResolver().ContainsDocumentLenient(txID, docName)
	if err != nil {
		return false, mapProjectionResolutionError(txID, docName, err)
	}
	return exists, nil
}

type docWithBlock struct {
	block.IndexEntry
	blockID uint64
}

func (s *Shard) duplicateDocumentEntry(txID, docName string) (docWithBlock, bool, error) {
	docs, err := s.projectionResolver().ListDocuments(txID)
	if err != nil {
		return docWithBlock{}, false, mapProjectionResolutionError(txID, docName, err)
	}
	var existing docWithBlock
	found := false
	for _, doc := range docs {
		if doc.DocName == docName {
			if found {
				return docWithBlock{}, false, corruptDuplicateMetadataError()
			}
			existing = docWithBlock{IndexEntry: doc.IndexEntry, blockID: doc.BlockID}
			found = true
		}
	}
	return existing, found, nil
}

func (s *Shard) findDocEntry(txID, docName string) (docWithBlock, error) {
	doc, err := s.projectionResolver().ResolveDocument(txID, docName)
	if err != nil {
		return docWithBlock{}, mapProjectionResolutionError(txID, docName, err)
	}

	return docWithBlock{IndexEntry: doc.IndexEntry, blockID: doc.BlockID}, nil
}

func (s *Shard) projectionResolver() index.Resolver {
	return index.NewResolver(s.idx, s.idxPath)
}

func mapProjectionResolutionError(txID, docName string, err error) error {
	switch {
	case errors.Is(err, index.ErrNotFound):
		return fmt.Errorf("%w: %s: %w", storeapi.ErrTxNotFound, txID, err)
	case errors.Is(err, index.ErrDocumentNotFound):
		return fmt.Errorf("%w: %s/%s: %w", storeapi.ErrNotFound, txID, docName, err)
	default:
		return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	}
}

func (s *Shard) CorruptProjectionForTest(txID string, blockID uint64, docCount uint16, completed bool) {
	_ = s.idx.Put(txID, blockID, docCount, completed)
}
