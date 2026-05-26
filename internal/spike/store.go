package spike

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const DefaultBlockSealSize = 64 * 1024 * 1024

type Config struct {
	BlockSealSize int64
}

type Store struct {
	dataDir       string
	blocksDir     string
	idx           *index.Index
	blockSealSize int64

	mu          sync.Mutex
	blockWriter *block.BlockWriter
	idxWriter   *block.IndexWriter
	nextBlockID uint64
	docs        map[string]bool
}

func Open(dataDir string) (*Store, error) {
	return OpenWithConfig(dataDir, Config{BlockSealSize: DefaultBlockSealSize})
}

func OpenWithConfig(dataDir string, cfg Config) (*Store, error) {
	blocksDir := filepath.Join(dataDir, "blocks")
	pebbleDir := filepath.Join(dataDir, "pebble")

	for _, d := range []string{blocksDir, pebbleDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("spike: mkdir %s: %w", d, err)
		}
	}

	idx, err := index.Open(pebbleDir)
	if err != nil {
		return nil, fmt.Errorf("spike: open index: %w", err)
	}

	sealSize := cfg.BlockSealSize
	if sealSize <= 0 {
		sealSize = DefaultBlockSealSize
	}

	nextID, err := scanMaxBlockID(blocksDir)
	if err != nil {
		idx.Close()
		return nil, err
	}

	s := &Store{
		dataDir:       dataDir,
		blocksDir:     blocksDir,
		idx:           idx,
		blockSealSize: sealSize,
		nextBlockID:   nextID,
		docs:          make(map[string]bool),
	}

	if err := s.openNewBlock(); err != nil {
		idx.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) WriteDocument(_ context.Context, txID, docName, contentType, _ string, body io.Reader) (storeapi.WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := txID + "\x00" + docName
	if s.docs[key] {
		return storeapi.WriteResult{}, fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, txID, docName)
	}

	if s.blockWriter.Offset() > block.BlockHeaderSize && s.blockWriter.Offset() >= s.blockSealSize {
		if err := s.sealAndOpenNew(); err != nil {
			return storeapi.WriteResult{}, fmt.Errorf("spike: seal block: %w", err)
		}
	}

	now := time.Now()

	result, err := s.blockWriter.AppendDocument(txID, docName, contentType, body)
	if err != nil {
		return storeapi.WriteResult{}, fmt.Errorf("spike: append document: %w", err)
	}

	if err := s.idxWriter.Append(block.IndexEntry{
		TransactionID: txID,
		DocName:       docName,
		ContentType:   contentType,
		CreatedAt:     now,
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}); err != nil {
		return storeapi.WriteResult{}, fmt.Errorf("spike: write idx: %w", err)
	}

	blockID := s.blockWriter.BlockID()
	existing, err := s.idx.Get(txID)
	if err != nil {
		if err := s.idx.Put(txID, blockID, 1, false); err != nil {
			return storeapi.WriteResult{}, fmt.Errorf("spike: put index: %w", err)
		}
	} else {
		lastBlockID := existing.BlockIDs[len(existing.BlockIDs)-1]
		if blockID != lastBlockID {
			if err := s.idx.AddBlockID(txID, blockID); err != nil {
				return storeapi.WriteResult{}, fmt.Errorf("spike: add block id: %w", err)
			}
		}
		if err := s.idx.IncrementDocCount(txID); err != nil {
			return storeapi.WriteResult{}, fmt.Errorf("spike: increment doc count: %w", err)
		}
	}

	s.docs[key] = true

	if s.blockWriter.Offset() >= s.blockSealSize {
		if err := s.sealAndOpenNew(); err != nil {
			return storeapi.WriteResult{}, fmt.Errorf("spike: seal after large doc: %w", err)
		}
	}

	return storeapi.WriteResult{
		SHA256:    result.SHA256,
		Size:      result.Size,
		CreatedAt: now,
	}, nil
}

func (s *Store) HeadDocument(_ context.Context, txID, docName string) (storeapi.DocumentMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.findDocEntry(txID, docName)
	if err != nil {
		return storeapi.DocumentMeta{}, err
	}

	return storeapi.DocumentMeta{
		Name:        entry.DocName,
		ContentType: entry.ContentType,
		Size:        entry.TotalBytes,
		SHA256:      entry.SHA256,
		CreatedAt:   entry.CreatedAt,
	}, nil
}

func (s *Store) ReadDocument(_ context.Context, txID, docName string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.findDocEntry(txID, docName)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}

	blkPath := s.blockPath(entry.blockID)
	rc, err := block.ReadDocument(blkPath, entry.IndexEntry)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, fmt.Errorf("%w: %v", storeapi.ErrDataLoss, err)
	}

	meta := storeapi.DocumentMeta{
		Name:        entry.DocName,
		ContentType: entry.ContentType,
		Size:        entry.TotalBytes,
		SHA256:      entry.SHA256,
		CreatedAt:   entry.CreatedAt,
	}

	return rc, meta, nil
}

func (s *Store) FindDocuments(_ context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return nil, nil
	}

	var docs []storeapi.DocumentMeta
	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", storeapi.ErrDataLoss, err)
		}
		entries := ir.FindByTransaction(txID)
		ir.Close()

		for _, e := range entries {
			docs = append(docs, storeapi.DocumentMeta{
				Name:        e.DocName,
				ContentType: e.ContentType,
				Size:        e.TotalBytes,
				SHA256:      e.SHA256,
				CreatedAt:   e.CreatedAt,
			})
		}
	}

	return docs, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.idxWriter != nil {
		if err := s.idxWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.blockWriter != nil {
		if err := s.blockWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.idx.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

type docWithBlock struct {
	block.IndexEntry
	blockID uint64
}

func (s *Store) findDocEntry(txID, docName string) (docWithBlock, error) {
	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return docWithBlock{}, fmt.Errorf("%w: %s", storeapi.ErrTxNotFound, txID)
	}

	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return docWithBlock{}, fmt.Errorf("%w: %v", storeapi.ErrDataLoss, err)
		}

		entry, err := ir.Find(txID, docName)
		ir.Close()
		if err == nil {
			return docWithBlock{IndexEntry: entry, blockID: bid}, nil
		}
	}

	return docWithBlock{}, fmt.Errorf("%w: %s/%s", storeapi.ErrNotFound, txID, docName)
}

func (s *Store) sealAndOpenNew() error {
	if err := s.idxWriter.Close(); err != nil {
		return fmt.Errorf("spike: close idx: %w", err)
	}
	if err := s.blockWriter.Close(); err != nil {
		return fmt.Errorf("spike: close block: %w", err)
	}
	return s.openNewBlock()
}

func (s *Store) openNewBlock() error {
	id := s.nextBlockID
	s.nextBlockID++

	blkPath := s.blockPath(id)
	bw, err := block.NewBlockWriter(blkPath, 0, id)
	if err != nil {
		return fmt.Errorf("spike: new block: %w", err)
	}

	idxPath := s.idxPath(id)
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		bw.Close()
		return fmt.Errorf("spike: new index: %w", err)
	}

	s.blockWriter = bw
	s.idxWriter = iw
	return nil
}

func (s *Store) blockPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", id))
}

func (s *Store) idxPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", id))
}

func scanMaxBlockID(blocksDir string) (uint64, error) {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("spike: read blocks dir: %w", err)
	}

	var maxID uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".blk") {
			continue
		}
		hexPart := strings.TrimSuffix(name, ".blk")
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			return 0, fmt.Errorf("spike: malformed block filename: %s", name)
		}
		if id > maxID {
			maxID = id
		}
	}

	_ = sort.Search // keep import for future use
	return maxID + 1, nil
}
