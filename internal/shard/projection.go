package shard

// Projection helpers: keep Document metadata lookup and projection mutation
// close to the Pebble Projection authority on Shard.

import (
	"errors"
	"fmt"
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

func (s *Shard) applyCommitDocument(doc *scrapv1.CommitDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.inspectCommitProjectionState(doc)
	if err != nil {
		return err
	}
	if state.targetExists && state.targetCounted {
		return fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, doc.TransactionId, doc.DocumentName)
	}

	var sha [32]byte
	copy(sha[:], doc.Sha256)
	createdAt := time.UnixMicro(doc.CreatedAtUs)

	if err := s.appendDocumentIndexEntry(doc, block.IndexEntry{
		TransactionID: doc.TransactionId,
		DocName:       doc.DocumentName,
		ContentType:   doc.ContentType,
		CreatedAt:     createdAt,
		FirstFrameOff: safeUint64ToInt64(doc.FirstFrameOff),
		FrameCount:    doc.FrameCount,
		TotalBytes:    doc.TotalBytes,
		SHA256:        sha,
	}); err != nil {
		return err
	}

	if !state.targetCounted {
		if err := addProjectionDocument(s.idx, doc.TransactionId, doc.BlockId); err != nil {
			return err
		}
	}

	return nil
}

type commitProjectionState struct {
	targetExists  bool
	targetCounted bool
}

func (s *Shard) inspectCommitProjectionState(doc *scrapv1.CommitDocument) (commitProjectionState, error) {
	entry, exists, err := s.commitProjectionEntry(doc.TransactionId)
	if err != nil || !exists {
		return commitProjectionState{}, err
	}
	return s.inspectCommitProjectionEntry(doc, entry), nil
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

func (s *Shard) inspectCommitProjectionEntry(doc *scrapv1.CommitDocument, entry index.Entry) commitProjectionState {
	resolved := 0
	targetBlockProjected := false
	for _, blockID := range entry.BlockIDs {
		if blockID == doc.BlockId {
			targetBlockProjected = true
		}
		for _, blockDoc := range s.blockIndexEntriesLenient(blockID, doc.TransactionId) {
			resolved++
			if blockID == doc.BlockId && blockDoc.DocName == doc.DocumentName {
				return commitProjectionState{
					targetExists:  true,
					targetCounted: resolved <= int(entry.DocCount),
				}
			}
		}
	}

	return commitProjectionState{
		targetCounted: targetBlockProjected && int(entry.DocCount) > resolved,
	}
}

func (s *Shard) blockIndexEntriesLenient(blockID uint64, txID string) []block.IndexEntry {
	ir, err := block.OpenIndexReader(s.idxPath(blockID))
	if err != nil {
		return nil
	}
	defer func() { _ = ir.Close() }()
	return ir.FindByTransaction(txID)
}

func (s *Shard) appendDocumentIndexEntry(doc *scrapv1.CommitDocument, entry block.IndexEntry) error {
	idxW := s.idxWriterForBlock(doc.BlockId)
	if idxW != nil {
		if err := idxW.Append(entry); err != nil {
			return fmt.Errorf("shard: apply write idx: %w", err)
		}
		return nil
	}

	contains, err := s.blockIndexContainsDocument(doc.BlockId, doc.TransactionId, doc.DocumentName)
	if err != nil {
		return err
	}
	if contains {
		return nil
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
	if err := s.requeueBlockUploadAfterIndexAppend(doc.BlockId); err != nil {
		return err
	}
	return nil
}

func (s *Shard) requeueBlockUploadAfterIndexAppend(blockID uint64) error {
	if !s.upload.Enabled {
		return nil
	}
	info, err := os.Stat(s.blockPath(blockID))
	if err != nil {
		return fmt.Errorf("shard: stat historical block for upload: %w", err)
	}
	if err := s.idx.PutPendingUpload(index.PendingUpload{
		BlockID:         blockID,
		ShardID:         s.shardID,
		SealedSizeBytes: info.Size(),
		SealedAtUs:      time.Now().UnixMicro(),
	}); err != nil {
		return fmt.Errorf("shard: requeue historical block upload: %w", err)
	}
	if err := s.refreshUploadPressureLocked(); err != nil {
		return err
	}
	s.uploads.Notify()
	return nil
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

func (s *Shard) documentVisibleInProjection(txID, docName string) (bool, error) {
	exists, err := s.projectionResolver().ContainsDocument(txID, docName)
	if err != nil {
		return false, mapProjectionResolutionError(txID, docName, err)
	}
	return exists, nil
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
