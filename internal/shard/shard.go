package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/scrub"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/ulid"
)

const (
	DefaultBlockSealSize  = 64 * 1024 * 1024
	DefaultBootstrapGrace = 60 * time.Second
)

type Config struct {
	DataDir        string
	ShardID        uint64
	RaftID         uint64
	Peers          map[uint64]string
	BlockSealSize  int64
	TickInterval   time.Duration
	BootstrapGrace time.Duration
	Scrub          scrub.ScrubConfig
	Transport      scrapraft.Transport

	ConsistencyChecker scrub.ConsistencyChecker
	ScrubMetrics       scrub.ScrubMetrics
	PeerAddrs          []string
}

type Shard struct {
	dataDir        string
	blocksDir      string
	openlogDir     string
	shardID        uint64
	peers          map[uint64]string
	idx            *index.Index
	raft           *scrapraft.RaftNode
	blockSealSize  int64
	raftStartedAt  time.Time
	bootstrapGrace time.Duration

	mu          sync.Mutex
	blockWriter *block.BlockWriter
	idxWriter   *block.IndexWriter
	nextBlockID uint64

	proposalMu     sync.Mutex
	proposals      map[string]chan error
	scrubProposals map[string]chan scrub.Result

	scrubMu     sync.RWMutex
	scrubResult *scrub.Result

	scrubber    *scrub.LightScrubber
	scrubCancel context.CancelFunc
}

func (c *Config) applyDefaults() {
	if c.BlockSealSize <= 0 {
		c.BlockSealSize = DefaultBlockSealSize
	}
	if c.BootstrapGrace == 0 {
		c.BootstrapGrace = DefaultBootstrapGrace
	}
}

func Open(cfg Config) (*Shard, error) {
	cfg.applyDefaults()

	blocksDir := filepath.Join(cfg.DataDir, "blocks")
	pebbleDir := filepath.Join(cfg.DataDir, "pebble")
	openlogDir := filepath.Join(cfg.DataDir, "openlog")
	raftDir := filepath.Join(cfg.DataDir, "raft")

	for _, d := range []string{blocksDir, pebbleDir, openlogDir, raftDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("shard: mkdir %s: %w", d, err)
		}
	}

	idx, err := index.Open(pebbleDir)
	if err != nil {
		return nil, fmt.Errorf("shard: open index: %w", err)
	}

	nextID, err := scanMaxBlockID(blocksDir)
	if err != nil {
		_ = idx.Close()
		return nil, err
	}

	s := &Shard{
		dataDir:        cfg.DataDir,
		blocksDir:      blocksDir,
		openlogDir:     openlogDir,
		shardID:        cfg.ShardID,
		peers:          cfg.Peers,
		idx:            idx,
		blockSealSize:  cfg.BlockSealSize,
		nextBlockID:    nextID,
		proposals:      make(map[string]chan error),
		scrubProposals: make(map[string]chan scrub.Result),
		raftStartedAt:  time.Now(),
		bootstrapGrace: cfg.BootstrapGrace,
	}

	if err := s.recoverOpenlog(); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("shard: openlog recovery: %w", err)
	}

	if err := s.openNewBlock(); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("shard: open block: %w", err)
	}

	transport := cfg.Transport
	if transport == nil {
		transport = &noopTransport{}
	}
	raftNode, err := scrapraft.Open(scrapraft.Config{
		ID:           cfg.RaftID,
		Peers:        cfg.Peers,
		DataDir:      raftDir,
		TickInterval: cfg.TickInterval,
		Transport:    transport,
		Apply:        s.applyEntries,
	})
	if err != nil {
		s.closeBlockAndIdx()
		_ = idx.Close()
		return nil, fmt.Errorf("shard: open raft: %w", err)
	}
	s.raft = raftNode

	s.startScrubber(cfg)

	return s, nil
}

func (s *Shard) startScrubber(cfg Config) {
	if !cfg.Scrub.Enabled || cfg.ConsistencyChecker == nil || cfg.ScrubMetrics == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scrubCancel = cancel
	s.scrubber = scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           s,
		ConsistencyChecker: cfg.ConsistencyChecker,
		LeaderChecker:      s,
		Metrics:            cfg.ScrubMetrics,
		PeerAddrs:          cfg.PeerAddrs,
		Interval:           cfg.Scrub.LightScrubInterval,
		Jitter:             cfg.Scrub.Jitter,
	})
	s.scrubber.Start(ctx)
}

//nolint:cyclop // orchestration function managing seal check, prep file, block append, raft propose, and apply
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

	writeID := ulid.New().String()
	blockID := s.blockWriter.BlockID()
	startOffset := s.blockWriter.Offset()

	if startOffset < 0 {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: negative start offset %d", startOffset)
	}
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
				FirstFrameOff:  uint64(max(0, result.FirstFrameOffset)),
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

	_ = os.Remove(s.prepPath(writeID)) // best-effort cleanup of prep file after commit

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
		return nil, storeapi.DocumentMeta{}, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
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
			return nil, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
		}
		entries := ir.FindByTransaction(txID)
		_ = ir.Close() // best-effort; data already read

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

func (s *Shard) CheckReadiness(_ context.Context) error {
	if s.raft.LeaderID() != 0 {
		return nil
	}
	if time.Since(s.raftStartedAt) < s.bootstrapGrace {
		return nil
	}
	return errors.New("shard: no leader elected")
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

func (s *Shard) RaftStep(ctx context.Context, msg raftpb.Message) error {
	return s.raft.Step(ctx, msg)
}

func (s *Shard) Close() error {
	if s.scrubCancel != nil {
		s.scrubCancel()
	}
	if s.scrubber != nil {
		s.scrubber.Stop()
	}
	s.raft.Stop()

	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.idxWriter != nil {
		firstErr = s.idxWriter.Close()
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

		switch c := cmd.Command.(type) {
		case *scrapv1.RaftCommand_CommitDoc:
			doc := c.CommitDoc
			key := doc.TransactionId + "\x00" + doc.DocumentName
			applyErr := s.applyCommitDocument(doc)

			s.proposalMu.Lock()
			if ch, ok := s.proposals[key]; ok {
				ch <- applyErr
				delete(s.proposals, key)
			}
			s.proposalMu.Unlock()

		case *scrapv1.RaftCommand_ConsistencyCheck:
			s.idx.SetAppliedIndex(e.Index)
			result := s.applyConsistencyCheck(c.ConsistencyCheck, e.Index)

			s.proposalMu.Lock()
			if ch, ok := s.scrubProposals[result.ScrubID]; ok {
				ch <- result
				delete(s.scrubProposals, result.ScrubID)
			}
			s.proposalMu.Unlock()
		}
	}
	return nil
}

func (s *Shard) applyConsistencyCheck(cc *scrapv1.RequestConsistencyCheck, entryIndex uint64) scrub.Result {
	_, hash, err := s.idx.StreamingHash()
	result := scrub.Result{
		ScrubID:      cc.ScrubId,
		AppliedIndex: entryIndex,
	}
	if err == nil {
		result.SHA256 = hash
	}

	s.scrubMu.Lock()
	s.scrubResult = &result
	s.scrubMu.Unlock()

	return result
}

func (s *Shard) ProposeConsistencyCheck(ctx context.Context, scrubID string) (scrub.Result, error) {
	if err := s.requireLeader(); err != nil {
		return scrub.Result{}, err
	}

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConsistencyCheck{
			ConsistencyCheck: &scrapv1.RequestConsistencyCheck{
				ScrubId:       scrubID,
				RequestedAtUs: time.Now().UnixMicro(),
			},
		},
	}

	data, err := proto.Marshal(cmd)
	if err != nil {
		return scrub.Result{}, fmt.Errorf("shard: marshal consistency check: %w", err)
	}

	doneCh := make(chan scrub.Result, 1)
	s.proposalMu.Lock()
	s.scrubProposals[scrubID] = doneCh
	s.proposalMu.Unlock()

	if err := s.raft.Propose(ctx, data); err != nil {
		s.proposalMu.Lock()
		delete(s.scrubProposals, scrubID)
		s.proposalMu.Unlock()
		return scrub.Result{}, fmt.Errorf("shard: propose consistency check: %w", err)
	}

	select {
	case result := <-doneCh:
		return result, nil
	case <-ctx.Done():
		s.proposalMu.Lock()
		delete(s.scrubProposals, scrubID)
		s.proposalMu.Unlock()
		return scrub.Result{}, ctx.Err()
	}
}

func (s *Shard) GetScrubResult(scrubID string) (scrub.Result, bool) {
	s.scrubMu.RLock()
	defer s.scrubMu.RUnlock()

	if s.scrubResult == nil || s.scrubResult.ScrubID != scrubID {
		return scrub.Result{}, false
	}
	return *s.scrubResult, true
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
		_ = ir.Close() // best-effort; data already read
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
	f, err := os.Create(path) //nolint:gosec // path constructed from known openlogDir + ULID
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // best-effort close after write failure
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // best-effort close after sync failure
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
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
		if err != nil {
			return fmt.Errorf("shard: read prep %s: %w", name, err)
		}

		entry := &scrapv1.OpenlogEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			return fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
		}

		if s.docExistsInPebble(entry.TransactionId, entry.DocumentName) {
			_ = os.Remove(path) // best-effort cleanup of already-committed prep
			continue
		}

		blkPath := s.blockPath(entry.BlockId)
		if err := truncateFile(blkPath, safeUint64ToInt64(entry.StartOffset)); err != nil {
			return fmt.Errorf("shard: truncate block %d: %w", entry.BlockId, err)
		}
		_ = os.Remove(path) // best-effort cleanup after truncation
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
		_ = bw.Close() // best-effort cleanup on index writer failure
		return err
	}
	s.blockWriter = bw
	s.idxWriter = iw
	return nil
}

func (s *Shard) closeBlockAndIdx() {
	if s.idxWriter != nil {
		_ = s.idxWriter.Close() // best-effort cleanup
	}
	if s.blockWriter != nil {
		_ = s.blockWriter.Close() // best-effort cleanup
	}
}

func (s *Shard) blockPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", id))
}

func (s *Shard) idxPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", id))
}

func truncateFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644) //nolint:gosec // path constructed from known blocksDir + block ID
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	truncErr := f.Truncate(size)
	if closeErr := f.Close(); closeErr != nil && truncErr == nil {
		return closeErr
	}
	return truncErr
}

func syncDir(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is the shard's own openlogDir
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	if closeErr := f.Close(); closeErr != nil && syncErr == nil {
		return closeErr
	}
	return syncErr
}

// safeUint64ToInt64 converts a uint64 to int64, clamping to math.MaxInt64 on overflow.
func safeUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
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

func (s *Shard) CorruptProjectionForTest(txID string, blockID uint64, docCount uint16, completed bool) {
	_ = s.idx.Put(txID, blockID, docCount, completed)
}

type noopTransport struct{}

func (t *noopTransport) Send(_ []raftpb.Message) {}

// Ensure Shard satisfies store.Store and scrub interfaces at compile time.
var (
	_ storeapi.Store      = (*Shard)(nil)
	_ scrub.ResultCache   = (*Shard)(nil)
	_ scrub.Proposer      = (*Shard)(nil)
	_ scrub.LeaderChecker = (*Shard)(nil)
)

// Suppress unused import.
var _ = bytes.NewReader
