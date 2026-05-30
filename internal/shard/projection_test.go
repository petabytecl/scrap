package shard

import (
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestApplyCommitDocumentToleratesProjectionAheadOfBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}
	if err := idx.Put("tx-replay", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-replay",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	})
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
}
