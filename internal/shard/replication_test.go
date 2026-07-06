package shard_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestAppendReplicatedDocumentWritesExpectedBytes(t *testing.T) {
	s := openTestShard(t)
	data := []byte("replicated document")
	sum := sha256.Sum256(data)

	sha, err := s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-replicated",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		StartOffset:   40,
		FrameCount:    1,
		TotalBytes:    int64(len(data)),
		Sha256:        sum[:],
	}, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("AppendReplicatedDocument: %v", err)
	}
	if !bytes.Equal(sha, sum[:]) {
		t.Fatalf("sha: got %x, want %x", sha, sum)
	}
}

func TestAppendReplicatedDocument_RejectsWrongStartOffset(t *testing.T) {
	s := openTestShard(t)
	data := []byte("should not be written")
	sum := sha256.Sum256(data)

	_, err := s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-bad-offset",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		StartOffset:   9999,
		FrameCount:    1,
		TotalBytes:    int64(len(data)),
		Sha256:        sum[:],
	}, bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error for wrong StartOffset")
	}
	if !strings.Contains(err.Error(), "replica offset") {
		t.Fatalf("expected offset mismatch error, got: %v", err)
	}
}

// A replica that accepted an append the leader then aborted holds an
// uncommitted overhang: the next replicated append arrives at the leader's
// rolled-back offset and must reclaim the overhang instead of stranding the
// replica until restart recovery.
func TestAppendReplicatedDocument_RollsBackAbortedOverhang(t *testing.T) {
	s := openTestShard(t)

	first := []byte("first document")
	firstSum := sha256.Sum256(first)
	sha, err := s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-first",
		DocumentName:  "a.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		StartOffset:   40,
		FrameCount:    1,
		TotalBytes:    int64(len(first)),
		Sha256:        firstSum[:],
	}, bytes.NewReader(first))
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	if !bytes.Equal(sha, firstSum[:]) {
		t.Fatalf("first sha mismatch")
	}

	second := []byte("second document")
	secondSum := sha256.Sum256(second)
	sha, err = s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-second",
		DocumentName:  "b.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		StartOffset:   40,
		FrameCount:    1,
		TotalBytes:    int64(len(second)),
		Sha256:        secondSum[:],
	}, bytes.NewReader(second))
	if err != nil {
		t.Fatalf("append at rolled-back offset: %v", err)
	}
	if !bytes.Equal(sha, secondSum[:]) {
		t.Fatalf("second sha mismatch")
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	gotDocSeqs := readBlockDocSeqs(t, block.FilePath(blocksDir, 1))
	if want := []uint32{0}; !slices.Equal(gotDocSeqs, want) {
		t.Fatalf("doc sequences = %v, want %v (overhang reclaimed, second doc only)", gotDocSeqs, want)
	}
}

func TestAppendReplicatedDocument_ReopensEmptyFutureBlockAfterRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := openReplicationTestShardInDir(t, dataDir)

	first := []byte("first replicated document")
	if _, err := s.WriteDocument(ctx, "tx-first", "a.xml", "text/xml", "", bytes.NewReader(first)); err != nil {
		t.Fatalf("first write: %v", err)
	}
	closeReplicationTestShard(t, s)

	restarted := openReplicationTestShardInDir(t, dataDir)
	defer closeReplicationTestShard(t, restarted)
	if got := restarted.CurrentBlockIDForTest(); got != 2 {
		t.Fatalf("current block after restart = %d, want 2", got)
	}

	second := []byte("second replicated document")
	secondSum := sha256.Sum256(second)
	secondOffset := testOffsetAfterSingleFrame(t, first)
	if _, err := restarted.AppendReplicatedDocument(ctx, replicatedInit("tx-second", "b.xml", 1, secondOffset, secondSum[:], len(second)), bytes.NewReader(second)); err != nil {
		t.Fatalf("second append after restart: %v", err)
	}
	if got := restarted.CurrentBlockIDForTest(); got != 1 {
		t.Fatalf("current block after rewind = %d, want 1", got)
	}

	blocksDir := filepath.Join(dataDir, "blocks")
	if _, err := os.Stat(block.FilePath(blocksDir, 2)); !os.IsNotExist(err) {
		t.Fatalf("future block 2 should have been removed, stat err=%v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 2)); !os.IsNotExist(err) {
		t.Fatalf("future block 2 index should have been removed, stat err=%v", err)
	}
	gotDocSeqs := readBlockDocSeqs(t, block.FilePath(blocksDir, 1))
	wantDocSeqs := []uint32{0, 1}
	if !slices.Equal(gotDocSeqs, wantDocSeqs) {
		t.Fatalf("doc sequences = %v, want %v", gotDocSeqs, wantDocSeqs)
	}
}

func TestAppendReplicatedDocument_RejectsNonEmptyFutureBlock(t *testing.T) {
	ctx := context.Background()
	s := openTestShard(t)

	first := []byte("first replicated document")
	if _, err := s.WriteDocument(ctx, "tx-first", "a.xml", "text/xml", "", bytes.NewReader(first)); err != nil {
		t.Fatalf("first write: %v", err)
	}

	future := []byte("future replicated document")
	futureSum := sha256.Sum256(future)
	if _, err := s.AppendReplicatedDocument(ctx, replicatedInit("tx-future", "future.xml", 2, block.HeaderSize, futureSum[:], len(future)), bytes.NewReader(future)); err != nil {
		t.Fatalf("future append: %v", err)
	}

	second := []byte("second replicated document")
	secondSum := sha256.Sum256(second)
	secondOffset := testOffsetAfterSingleFrame(t, first)
	_, err := s.AppendReplicatedDocument(ctx, replicatedInit("tx-second", "b.xml", 1, secondOffset, secondSum[:], len(second)), bytes.NewReader(second))
	if err == nil {
		t.Fatal("expected error for non-empty future block")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected non-empty future block error, got: %v", err)
	}
}

func TestAppendReplicatedDocument_RepairsBehindCurrentBlockFromPeer(t *testing.T) {
	ctx := context.Background()
	first := []byte("first replicated document")
	second := []byte("second document recovered from leader")
	third := []byte("third replicated document")
	repairBlock, repairIndex, thirdOffset := replicaRepairSourceBlock(t, [][]byte{first, second}, [][]byte{third})
	transferer := &replicaRepairTransferer{
		blockData: repairBlock,
		idxData:   repairIndex,
	}
	s := openReplicaRepairTestShard(t, transferer)

	firstSum := sha256.Sum256(first)
	if _, err := s.AppendReplicatedDocument(ctx, replicatedInit("tx-first", "a.xml", 1, block.HeaderSize, firstSum[:], len(first)), bytes.NewReader(first)); err != nil {
		t.Fatalf("first append: %v", err)
	}

	thirdSum := sha256.Sum256(third)
	if _, err := s.AppendReplicatedDocument(ctx, replicatedInit("tx-third", "c.xml", 1, thirdOffset, thirdSum[:], len(third)), bytes.NewReader(third)); err != nil {
		t.Fatalf("third append after repair: %v", err)
	}
	if got, want := transferer.calls, []string{"leader:9091"}; !slices.Equal(got, want) {
		t.Fatalf("transfer calls = %v, want %v", got, want)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	gotDocSeqs := readBlockDocSeqs(t, block.FilePath(blocksDir, 1))
	wantDocSeqs := []uint32{0, 1, 2}
	if !slices.Equal(gotDocSeqs, wantDocSeqs) {
		t.Fatalf("doc sequences = %v, want %v", gotDocSeqs, wantDocSeqs)
	}
}

type replicaRepairTransferer struct {
	blockData []byte
	idxData   []byte
	calls     []string
}

func (t *replicaRepairTransferer) TransferBlock(_ context.Context, addr string, _, _ uint64) ([]byte, []byte, error) {
	t.calls = append(t.calls, addr)
	return append([]byte(nil), t.blockData...), append([]byte(nil), t.idxData...), nil
}

func replicaRepairSourceBlock(t *testing.T, indexedDocs, trailingDocs [][]byte) ([]byte, []byte, uint64) {
	t.Helper()

	blocksDir := t.TempDir()
	blockPath := block.FilePath(blocksDir, 1)
	idxPath := block.IdxFilePath(blocksDir, 1)
	blockWriter, err := block.NewWriter(blockPath, 0, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	idxWriter, err := block.NewIndexWriter(idxPath)
	if err != nil {
		_ = blockWriter.Close()
		t.Fatalf("NewIndexWriter: %v", err)
	}

	repairOffset := appendIndexedReplicaRepairSourceDocs(t, blockWriter, idxWriter, indexedDocs)
	appendTrailingReplicaRepairSourceDocs(t, blockWriter, idxWriter, trailingDocs)
	if err := idxWriter.Close(); err != nil {
		_ = blockWriter.Close()
		t.Fatalf("Close index: %v", err)
	}
	if err := blockWriter.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}

	blockData, err := os.ReadFile(blockPath) //nolint:gosec // test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read block: %v", err)
	}
	idxData, err := os.ReadFile(idxPath) //nolint:gosec // test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	return blockData, idxData, repairOffset
}

func appendIndexedReplicaRepairSourceDocs(t *testing.T, blockWriter *block.Writer, idxWriter *block.IndexWriter, docs [][]byte) uint64 {
	t.Helper()

	offset := uint64(block.HeaderSize)
	for i, payload := range docs {
		result, err := blockWriter.AppendDocument("tx-source", "doc.xml", "text/xml", bytes.NewReader(payload))
		if err != nil {
			closeReplicaRepairSourceWriters(blockWriter, idxWriter)
			t.Fatalf("AppendDocument indexed %d: %v", i, err)
		}
		if result.FirstFrameOffset != int64(offset) {
			closeReplicaRepairSourceWriters(blockWriter, idxWriter)
			t.Fatalf("indexed offset %d = %d, want %d", i, result.FirstFrameOffset, offset)
		}
		if err := appendReplicaRepairSourceIndex(idxWriter, result); err != nil {
			closeReplicaRepairSourceWriters(blockWriter, idxWriter)
			t.Fatalf("Append index %d: %v", i, err)
		}
		offset += uint64(block.FrameHeaderSize + len(payload))
	}
	return offset
}

func appendReplicaRepairSourceIndex(idxWriter *block.IndexWriter, result block.AppendResult) error {
	return idxWriter.Append(block.IndexEntry{
		TransactionID: "tx-source",
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	})
}

func appendTrailingReplicaRepairSourceDocs(t *testing.T, blockWriter *block.Writer, idxWriter *block.IndexWriter, docs [][]byte) {
	t.Helper()

	for i, payload := range docs {
		if _, err := blockWriter.AppendDocument("tx-trailing", "doc.xml", "text/xml", bytes.NewReader(payload)); err != nil {
			closeReplicaRepairSourceWriters(blockWriter, idxWriter)
			t.Fatalf("AppendDocument trailing %d: %v", i, err)
		}
	}
}

func closeReplicaRepairSourceWriters(blockWriter *block.Writer, idxWriter *block.IndexWriter) {
	_ = idxWriter.Close()
	_ = blockWriter.Close()
}

func openReplicaRepairTestShard(t *testing.T, transferer *replicaRepairTransferer) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:         t.TempDir(),
		ShardID:         0,
		RaftID:          1,
		Peers:           map[uint64]string{1: "self:9091", 2: "leader:9091"},
		BlockTransferer: transferer,
		TickInterval:    10 * time.Millisecond,
		Replicator:      noopTestReplicator{},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func replicatedInit(txID, docName string, blockID, startOffset uint64, sha []byte, size int) *scrapv1.ReplicateDocumentInit {
	return &scrapv1.ReplicateDocumentInit{
		TransactionId: txID,
		DocumentName:  docName,
		ContentType:   "text/xml",
		BlockId:       blockID,
		StartOffset:   startOffset,
		FrameCount:    1,
		TotalBytes:    int64(size),
		Sha256:        sha,
	}
}

func testOffsetAfterSingleFrame(t *testing.T, payload []byte) uint64 {
	t.Helper()

	offset := block.HeaderSize + block.FrameHeaderSize + len(payload)
	if offset < block.HeaderSize {
		t.Fatalf("computed offset %d before Block header", offset)
	}
	return uint64(offset)
}

func openReplicationTestShardInDir(t *testing.T, dataDir string) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:      dataDir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}

func closeReplicationTestShard(t *testing.T, s *shard.Shard) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func readBlockDocSeqs(t *testing.T, path string) []uint32 {
	t.Helper()

	f, err := os.Open(path) //nolint:gosec // test path is under t.TempDir.
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(block.HeaderSize, io.SeekStart); err != nil {
		t.Fatalf("seek block frames: %v", err)
	}

	var seqs []uint32
	for {
		hdr, _, err := block.ReadFrame(f)
		if errors.Is(err, io.EOF) {
			return seqs
		}
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if hdr.FrameSeq == 0 {
			seqs = append(seqs, hdr.DocSeq)
		}
	}
}
