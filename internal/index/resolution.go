package index

import (
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/block"
)

var (
	ErrDocumentNotFound = errors.New("index: document not found")
	ErrCorrupt          = errors.New("index: projection resolution corrupt")
)

type BlockIndexPath func(blockID uint64) string

type ResolvedDocument struct {
	block.IndexEntry
	BlockID uint64
}

type Resolver struct {
	projection     *Index
	blockIndexPath BlockIndexPath
}

func NewResolver(projection *Index, blockIndexPath BlockIndexPath) Resolver {
	return Resolver{
		projection:     projection,
		blockIndexPath: blockIndexPath,
	}
}

func (r Resolver) ResolveDocument(txID, docName string) (ResolvedDocument, error) {
	entry, err := r.transactionEntry(txID)
	if err != nil {
		return ResolvedDocument{}, err
	}

	for _, blockID := range entry.BlockIDs {
		entries, err := r.blockEntries(txID, blockID)
		if err != nil {
			return ResolvedDocument{}, err
		}
		for _, e := range entries {
			if e.DocName == docName {
				return ResolvedDocument{IndexEntry: e, BlockID: blockID}, nil
			}
		}
	}

	return ResolvedDocument{}, fmt.Errorf("%w: %s/%s", ErrDocumentNotFound, txID, docName)
}

func (r Resolver) ListDocuments(txID string) ([]ResolvedDocument, error) {
	entry, err := r.transactionEntry(txID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	docs := make([]ResolvedDocument, 0, entry.DocCount)
	for _, blockID := range entry.BlockIDs {
		entries, err := r.blockEntries(txID, blockID)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			docs = append(docs, ResolvedDocument{IndexEntry: e, BlockID: blockID})
		}
	}
	if len(docs) != int(entry.DocCount) {
		return nil, fmt.Errorf("%w: transaction %s doc count mismatch: projection=%d resolved=%d", ErrCorrupt, txID, entry.DocCount, len(docs))
	}
	return docs, nil
}

func (r Resolver) ContainsDocument(txID, docName string) (bool, error) {
	_, err := r.ResolveDocument(txID, docName)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrDocumentNotFound) {
		return false, nil
	}
	return false, err
}

func (r Resolver) ContainsDocumentLenient(txID, docName string) (bool, error) {
	entry, err := r.transactionEntry(txID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	for _, blockID := range entry.BlockIDs {
		exists, err := r.blockContainsDocumentLenient(txID, docName, blockID)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func (r Resolver) transactionEntry(txID string) (Entry, error) {
	if r.projection == nil {
		return Entry{}, fmt.Errorf("%w: projection is nil", ErrCorrupt)
	}
	entry, err := r.projection.Get(txID)
	if err == nil && len(entry.BlockIDs) == 0 {
		return Entry{}, fmt.Errorf("%w: transaction %s has no block IDs", ErrCorrupt, txID)
	}
	if err == nil {
		return entry, nil
	}
	if errors.Is(err, ErrNotFound) {
		return Entry{}, err
	}
	return Entry{}, fmt.Errorf("%w: transaction %s: %w", ErrCorrupt, txID, err)
}

func (r Resolver) blockEntries(txID string, blockID uint64) ([]block.IndexEntry, error) {
	if r.blockIndexPath == nil {
		return nil, fmt.Errorf("%w: block index path adapter is nil", ErrCorrupt)
	}
	path := r.blockIndexPath(blockID)
	ir, err := block.OpenIndexReader(path)
	if err != nil {
		return nil, fmt.Errorf("%w: block %d: %w", ErrCorrupt, blockID, err)
	}
	defer func() { _ = ir.Close() }()

	entries := ir.FindByTransaction(txID)
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: transaction %s missing from block %d index", ErrCorrupt, txID, blockID)
	}
	return entries, nil
}

func (r Resolver) blockContainsDocumentLenient(txID, docName string, blockID uint64) (bool, error) {
	if r.blockIndexPath == nil {
		return false, fmt.Errorf("%w: block index path adapter is nil", ErrCorrupt)
	}
	path := r.blockIndexPath(blockID)
	ir, err := block.OpenIndexReader(path)
	if err != nil {
		return false, nil
	}
	defer func() { _ = ir.Close() }()

	_, err = ir.Find(txID, docName)
	return err == nil, nil
}
