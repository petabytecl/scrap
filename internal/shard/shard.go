package shard

import (
	"bytes"
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

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/oklog/ulid/v2"
	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

const DefaultBlockSealSize = 64 * 1024 * 1024

type Config struct {
	DataDir       string
	ShardID       uint64
	RaftID        uint64
	Peers         map[uint64]string
	BlockSealSize int64
	TickInterval  time.Duration
}

type Shard struct {
	dataDir       string
	blocksDir     string
	openlogDir    string
	shardID       uint64
	peers         map[uint64]string
	idx           *index.Index
	raft          *scrapraft.RaftNode
	blockSealSize int64

	mu          sync.Mutex
	blockWriter *block.BlockWriter
	idxWriter   *block.IndexWriter
	nextBlockID uint64

	proposalMu sync.Mutex
	proposals  map[string]chan error
}

func Open(cfg Config) (*Shard, error) {
	blocksDir := filepath.Join(cfg.DataDir, "blocks")
	pebbleDir := filepath.Join(cfg.DataDir, "pebble")
	openlogDir := filepath.Join(cfg.DataDir, "openlog")
	raftDir := filepath.Join(cfg.DataDir, "raft")

	for _, d := range []string{blocksDir, pebbleDir, openlogDir, raftDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("shard: mkdir %s: %w", d, err)
		}
	}

	idx, err := index.Open(pebbleDir)
	if err != nil {
		return nil, fmt.Errorf("shard: open index: %w", err)
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

	s := &Shard{
		dataDir:       cfg.DataDir,
		blocksDir:     blocksDir,
		openlogDir:    openlogDir,
		shardID:       cfg.ShardID,
		peers:         cfg.Peers,
		idx:           idx,
		blockSealSize: sealSize,
		nextBlockID:   nextID,
		proposals:     make(map[string]chan error),
	}

	if err := s.recoverOpenlog(); err != nil {
		idx.Close()
		return nil, fmt.Errorf("shard: openlog recovery: %w", err)
	}

	if err := s.openNewBlock(); err != nil {
		idx.Close()
		return nil, fmt.Errorf("shard: open block: %w", err)
	}

	tickInterval := cfg.TickInterval
	if tickInterval == 0 {
		tickInterval = 100 * time.Millisecond
	}

	transport := &noopTransport{}
	raftNode, err := scrapraft.Open(scrapraft.Config{
		ID:           cfg.RaftID,
		Peers:        cfg.Peers,
		DataDir:      raftDir,
		TickInterval: tickInterval,
		Transport:    transport,
		Apply:        s.applyEntries,
	})
	if err != nil {
		s.closeBlockAndIdx()
		idx.Close()
		return nil, fmt.Errorf("shard: open raft: %w", err)
	}
	s.raft = raftNode

	return s, nil
}

func (s *Shard) WriteDocument(ctx context.Context, txID, docName, contentType, idempotencyKey string, body io.Reader) (storeapi.WriteResult, error) {
	if err := s.requireLeader(); err != nil {
		return storeapi.WriteResult{}, err
	}

	s.mu.Lock()

	key := txID + "\x00" + docName
	if s.docExistsInPebble(txID, docName) {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, txID, docName)
	}

	if s.blockWriter.Offset() > block.BlockHeaderSize && s.blockWriter.Offset() >= s.blockSealSize {
		if err := s.sealAndOpenNew(); err != nil {
			s.mu.Unlock()
			return storeapi.WriteResult{}, fmt.Errorf("shard: seal block: %w", err)
		}
	}

	writeID := ulid.Make().String()
	blockID := s.blockWriter.BlockID()
	startOffset := s.blockWriter.Offset()

	prepEntry := &scrapv1.OpenlogEntry{
		TransactionId:  txID,
		DocumentName:   docName,
		BlockId:        blockID,
		StartOffset:    uint64(startOffset),
		ContentType:    contentType,
		IdempotencyKey: idempotencyKey,
	}
	if err := s.writePrepFile(writeID, prepEntry); err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: write prep: %w", err)
	}

	now := time.Now()

	result, err := s.blockWriter.AppendDocument(txID, docName, contentType, body)
	if err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: append document: %w", err)
	}

	s.mu.Unlock()

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId:  txID,
				DocumentName:   docName,
				ContentType:    contentType,
				IdempotencyKey: idempotencyKey,
				BlockId:        blockID,
				FirstFrameOff:  uint64(result.FirstFrameOffset),
				FrameCount:     result.FrameCount,
				TotalBytes:     result.Size,
				Sha256:         result.SHA256[:],
				CreatedAtUs:    now.UnixMicro(),
			},
		},
	}

	data, err := proto.Marshal(cmd)
	if err != nil {
		return storeapi.WriteResult{}, fmt.Errorf("shard: marshal command: %w", err)
	}

	doneCh := make(chan error, 1)
	s.proposalMu.Lock()
	s.proposals[key] = doneCh
	s.proposalMu.Unlock()

	if err := s.raft.Propose(ctx, data); err != nil {
		s.proposalMu.Lock()
		delete(s.proposals, key)
		s.proposalMu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: propose: %w", err)
	}

	select {
	case applyErr := <-doneCh:
		if applyErr != nil {
			return storeapi.WriteResult{}, applyErr
		}
	case <-ctx.Done():
		return storeapi.WriteResult{}, ctx.Err()
	}

	os.Remove(s.prepPath(writeID))

	return storeapi.WriteResult{
		SHA256:    result.SHA256,
		Size:      result.Size,
		CreatedAt: now,
	}, nil
}

func (s *Shard) HeadDocument(ctx context.Context, txID, docName string) (storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return storeapi.DocumentMeta{}, err
	}

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

func (s *Shard) ReadDocument(ctx context.Context, txID, docName string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}

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

func (s *Shard) FindDocuments(ctx context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return nil, err
	}

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

func (s *Shard) IsLeader() bool {
	return s.raft.IsLeader()
}

func (s *Shard) requireLeader() error {
	if s.raft.IsLeader() {
		return nil
	}
	return s.notLeaderError()
}

func (s *Shard) requireLeaderRead(ctx context.Context) error {
	if !s.raft.IsLeader() {
		return s.notLeaderError()
	}
	_, err := s.raft.ReadIndex(ctx)
	if err != nil {
		return fmt.Errorf("shard: read index: %w", err)
	}
	return nil
}

func (s *Shard) notLeaderError() error {
	leaderID := s.raft.LeaderID()
	var leaderAddr string
	if leaderID != 0 {
		if addr, ok := s.peers[leaderID]; ok {
			leaderAddr = addr
		}
	}
	return &storeapi.NotLeaderError{LeaderAddr: leaderAddr}
}

func (s *Shard) Close() error {
	s.raft.Stop()

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

func (s *Shard) applyEntries(entries []raftpb.Entry) error {
	for _, e := range entries {
		if e.Type != raftpb.EntryNormal || len(e.Data) == 0 {
			continue
		}

		cmd := &scrapv1.RaftCommand{}
		if err := proto.Unmarshal(e.Data, cmd); err != nil {
			return fmt.Errorf("shard: unmarshal raft command: %w", err)
		}

		doc := cmd.GetCommitDoc()
		if doc == nil {
			continue
		}

		key := doc.TransactionId + "\x00" + doc.DocumentName
		applyErr := s.applyCommitDocument(doc)

		s.proposalMu.Lock()
		if ch, ok := s.proposals[key]; ok {
			ch <- applyErr
			delete(s.proposals, key)
		}
		s.proposalMu.Unlock()
	}
	return nil
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
			FirstFrameOff: int64(doc.FirstFrameOff),
			FrameCount:    doc.FrameCount,
			TotalBytes:    doc.TotalBytes,
			SHA256:        sha,
		}); err != nil {
			return fmt.Errorf("shard: apply write idx: %w", err)
		}
	}

	blockID := doc.BlockId
	existing, err := s.idx.Get(doc.TransactionId)
	if err != nil {
		if err := s.idx.Put(doc.TransactionId, blockID, 1, false); err != nil {
			return fmt.Errorf("shard: put index: %w", err)
		}
	} else {
		lastBlockID := existing.BlockIDs[len(existing.BlockIDs)-1]
		if blockID != lastBlockID {
			if err := s.idx.AddBlockID(doc.TransactionId, blockID); err != nil {
				return fmt.Errorf("shard: add block id: %w", err)
			}
		}
		if err := s.idx.IncrementDocCount(doc.TransactionId); err != nil {
			return fmt.Errorf("shard: increment doc count: %w", err)
		}
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
		ir.Close()
		if err == nil {
			return true
		}
	}
	return false
}

func (s *Shard) writePrepFile(writeID string, entry *scrapv1.OpenlogEntry) error {
	data, err := proto.Marshal(entry)
	if err != nil {
		return err
	}
	path := s.prepPath(writeID)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(s.openlogDir)
}

func (s *Shard) prepPath(writeID string) string {
	return filepath.Join(s.openlogDir, writeID+".prep")
}

func (s *Shard) recoverOpenlog() error {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var prepFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".prep") {
			prepFiles = append(prepFiles, e.Name())
		}
	}
	sort.Strings(prepFiles)

	for _, name := range prepFiles {
		path := filepath.Join(s.openlogDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("shard: read prep %s: %w", name, err)
		}

		entry := &scrapv1.OpenlogEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			return fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
		}

		if s.docExistsInPebble(entry.TransactionId, entry.DocumentName) {
			os.Remove(path)
			continue
		}

		blkPath := s.blockPath(entry.BlockId)
		if err := truncateFile(blkPath, int64(entry.StartOffset)); err != nil {
			return fmt.Errorf("shard: truncate block %d: %w", entry.BlockId, err)
		}
		os.Remove(path)
	}

	return nil
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

func (s *Shard) sealAndOpenNew() error {
	if err := s.idxWriter.Close(); err != nil {
		return err
	}
	if err := s.blockWriter.Close(); err != nil {
		return err
	}
	return s.openNewBlock()
}

func (s *Shard) openNewBlock() error {
	id := s.nextBlockID
	s.nextBlockID++

	bw, err := block.NewBlockWriter(s.blockPath(id), s.shardID, id)
	if err != nil {
		return err
	}
	iw, err := block.NewIndexWriter(s.idxPath(id))
	if err != nil {
		bw.Close()
		return err
	}
	s.blockWriter = bw
	s.idxWriter = iw
	return nil
}

func (s *Shard) closeBlockAndIdx() {
	if s.idxWriter != nil {
		s.idxWriter.Close()
	}
	if s.blockWriter != nil {
		s.blockWriter.Close()
	}
}

func (s *Shard) blockPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", id))
}

func (s *Shard) idxPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", id))
}

func truncateFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func scanMaxBlockID(blocksDir string) (uint64, error) {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("shard: read blocks dir: %w", err)
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
			return 0, fmt.Errorf("shard: malformed block filename: %s", name)
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID + 1, nil
}

type noopTransport struct{}

func (t *noopTransport) Send(_ []raftpb.Message) {}

// Ensure Shard satisfies store.Store at compile time.
var _ storeapi.Store = (*Shard)(nil)

// Suppress unused import.
var _ = bytes.NewReader
