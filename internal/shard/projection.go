package shard

// Projection helpers: keep Document metadata lookup and projection mutation
// close to the Pebble Projection authority on Shard.

import (
	"errors"
	"fmt"
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

	if s.docExistsInPebble(doc.TransactionId, doc.DocumentName) {
		return fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, doc.TransactionId, doc.DocumentName)
	}

	var sha [32]byte
	copy(sha[:], doc.Sha256)
	createdAt := time.UnixMicro(doc.CreatedAtUs)

	idxW := s.idxWriterForBlock(doc.BlockId)
	if idxW != nil {
		if err := idxW.Append(block.IndexEntry{
			TransactionID: doc.TransactionId,
			DocName:       doc.DocumentName,
			ContentType:   doc.ContentType,
			CreatedAt:     createdAt,
			FirstFrameOff: safeUint64ToInt64(doc.FirstFrameOff),
			FrameCount:    doc.FrameCount,
			TotalBytes:    doc.TotalBytes,
			SHA256:        sha,
		}); err != nil {
			return fmt.Errorf("shard: apply write idx: %w", err)
		}
	}

	if err := addProjectionDocument(s.idx, doc.TransactionId, doc.BlockId); err != nil {
		return err
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

func (s *Shard) docExistsInPebble(txID, docName string) bool {
	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return false
	}
	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			continue
		}
		_, err = ir.Find(txID, docName)
		_ = ir.Close() // best-effort; data already read
		if err == nil {
			return true
		}
	}
	return false
}

type docWithBlock struct {
	block.IndexEntry
	blockID uint64
}

func (s *Shard) findDocEntry(txID, docName string) (docWithBlock, error) {
	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return docWithBlock{}, fmt.Errorf("%w: %s", storeapi.ErrTxNotFound, txID)
	}

	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return docWithBlock{}, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
		}
		entry, err := ir.Find(txID, docName)
		_ = ir.Close() // best-effort; data already read
		if err == nil {
			return docWithBlock{IndexEntry: entry, blockID: bid}, nil
		}
	}

	return docWithBlock{}, fmt.Errorf("%w: %s/%s", storeapi.ErrNotFound, txID, docName)
}

func (s *Shard) CorruptProjectionForTest(txID string, blockID uint64, docCount uint16, completed bool) {
	_ = s.idx.Put(txID, blockID, docCount, completed)
}
