package shard_test

// Crash-injection regression for #463: a Document whose CommitDocument was
// quorum-committed but not yet applied when the Member crashed leaves its
// prep on disk and nothing in the projection. Openlog recovery must not
// truncate its bytes before raft replay applies the committed entry — the
// old single-pass recovery destroyed the committed Frames and replay then
// indexed them past EOF (permanent DATA_LOSS on a single-member Cell).

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/storage/wal"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestCommittedButUnappliedDocumentSurvivesOpenlogRecovery(t *testing.T) {
	const (
		shardID = uint64(7)
		blockID = uint64(1)
		txID    = "tx-victim"
		docName = "doc.xml"
	)
	content := []byte("committed on quorum, not yet applied")
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")

	appendResult, blockSize := writeCrashedBlock(t, blocksDir, shardID, blockID, txID, docName, content)
	writeCrashedPrep(t, dataDir, txID, docName, blockID, appendResult.FirstFrameOffset)
	writeCommittedWAL(t, dataDir, &scrapv1.CommitDocument{
		TransactionId: txID,
		DocumentName:  docName,
		ContentType:   "text/xml",
		BlockId:       blockID,
		FirstFrameOff: uint64(appendResult.FirstFrameOffset), //nolint:gosec // offset >= HeaderSize, never negative
		FrameCount:    appendResult.FrameCount,
		TotalBytes:    appendResult.Size,
		Sha256:        appendResult.SHA256[:],
		CreatedAtUs:   time.Now().UTC().UnixMicro(),
	})

	s, err := shard.Open(shard.Config{
		DataDir:      dataDir,
		ShardID:      shardID,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open after commit/apply crash: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	waitShardLeader(t, s)

	// The committed bytes must have survived recovery: replay applied the
	// CommitDocument, so the Document is visible and readable.
	rc, _, err := s.ReadDocument(t.Context(), txID, docName)
	if err != nil {
		t.Fatalf("ReadDocument after recovery = %v; committed-but-unapplied Document was destroyed", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content = %q, want %q", got, content)
	}

	if info, err := os.Stat(block.FilePath(blocksDir, blockID)); err != nil || info.Size() != blockSize {
		t.Fatalf("Block size after recovery = %v (err=%v), want untruncated %d", info, err, blockSize)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "openlog", crashedPrepName+".prep")); !os.IsNotExist(err) {
		t.Fatalf("prep stat = %v, want removed after replay resolved it", err)
	}
}

const crashedPrepName = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

// writeCrashedBlock crafts the Block exactly as the crashed write left it:
// header plus the Document's Frames, fsynced, with a header-only .idx (the
// .idx entry is written at apply time, which never ran).
func writeCrashedBlock(
	t *testing.T, blocksDir string, shardID, blockID uint64, txID, docName string, content []byte,
) (block.AppendResult, int64) {
	t.Helper()
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks: %v", err)
	}
	bw, err := block.NewWriter(block.FilePath(blocksDir, blockID), shardID, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	appendResult, err := bw.AppendDocument(txID, docName, "text/xml", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	size := bw.Offset()
	if err := bw.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}
	iw, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}
	return appendResult, size
}

func writeCrashedPrep(t *testing.T, dataDir, txID, docName string, blockID uint64, startOffset int64) {
	t.Helper()
	openlogDir := filepath.Join(dataDir, "openlog")
	if err := os.MkdirAll(openlogDir, 0o750); err != nil {
		t.Fatalf("mkdir openlog: %v", err)
	}
	data, err := proto.Marshal(&scrapv1.OpenlogEntry{
		TransactionId: txID,
		DocumentName:  docName,
		BlockId:       blockID,
		StartOffset:   uint64(startOffset), //nolint:gosec // offset >= HeaderSize, never negative
		ContentType:   "text/xml",
	})
	if err != nil {
		t.Fatalf("marshal prep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(openlogDir, crashedPrepName+".prep"), data, 0o600); err != nil {
		t.Fatalf("write prep: %v", err)
	}
}

// writeCommittedWAL crafts the raft WAL of the crash moment: the bootstrap
// ConfChange and the CommitDocument entry are committed (HardState.Commit
// covers them) but the apply never ran before the crash.
func writeCommittedWAL(t *testing.T, dataDir string, doc *scrapv1.CommitDocument) {
	t.Helper()
	walDir := filepath.Join(dataDir, "raft", "wal")
	if err := os.MkdirAll(walDir, 0o750); err != nil {
		t.Fatalf("mkdir wal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "raft", "snap"), 0o750); err != nil {
		t.Fatalf("mkdir snap: %v", err)
	}

	confChange, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}).Marshal()
	if err != nil {
		t.Fatalf("marshal conf change: %v", err)
	}
	command, err := proto.Marshal(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{CommitDoc: doc},
	})
	if err != nil {
		t.Fatalf("marshal raft command: %v", err)
	}

	w, err := wal.Create(zap.NewNop(), walDir, nil)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: confChange},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: command},
	}
	if err := w.Save(raftpb.HardState{Term: 1, Vote: 1, Commit: 2}, entries); err != nil {
		t.Fatalf("save wal: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
}

func waitShardLeader(t *testing.T, s *shard.Shard) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
}
