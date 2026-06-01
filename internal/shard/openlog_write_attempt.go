package shard

import (
	"fmt"
	"os"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/ulid"
)

type openlogWriteAttemptConfig struct {
	txID           string
	docName        string
	contentType    string
	idempotencyKey string
	blockID        uint64
	startOffset    int64
}

type openlogWriteAttempt struct {
	shard           *Shard
	writeID         string
	entry           *scrapv1.OpenlogEntry
	prepCleanupDone bool
}

func (s *Shard) beginOpenlogWriteAttempt(cfg openlogWriteAttemptConfig) (*openlogWriteAttempt, error) {
	if cfg.startOffset < 0 {
		return nil, fmt.Errorf("shard: negative start offset %d", cfg.startOffset)
	}

	attempt := &openlogWriteAttempt{
		shard:   s,
		writeID: ulid.New().String(),
		entry: &scrapv1.OpenlogEntry{
			TransactionId:  cfg.txID,
			DocumentName:   cfg.docName,
			BlockId:        cfg.blockID,
			StartOffset:    uint64(cfg.startOffset),
			ContentType:    cfg.contentType,
			IdempotencyKey: cfg.idempotencyKey,
		},
	}
	if err := s.writePrepFile(attempt.writeID, attempt.entry); err != nil {
		return nil, fmt.Errorf("shard: write prep: %w", err)
	}

	return attempt, nil
}

func (a *openlogWriteAttempt) cleanupOnAbort() {
	if a.prepCleanupDone {
		return
	}
	_ = os.Remove(a.prepPath())
	a.prepCleanupDone = true
}

func (a *openlogWriteAttempt) complete() {
	a.prepCleanupDone = true
	_ = os.Remove(a.prepPath())
}

func (a *openlogWriteAttempt) prepEntry() *scrapv1.OpenlogEntry {
	return a.entry
}

func (a *openlogWriteAttempt) commitCommand(result block.AppendResult, createdAt time.Time) *scrapv1.RaftCommand {
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId:  a.entry.TransactionId,
				DocumentName:   a.entry.DocumentName,
				ContentType:    a.entry.ContentType,
				IdempotencyKey: a.entry.IdempotencyKey,
				BlockId:        a.entry.BlockId,
				FirstFrameOff:  uint64(max(0, result.FirstFrameOffset)),
				FrameCount:     result.FrameCount,
				TotalBytes:     result.Size,
				Sha256:         result.SHA256[:],
				CreatedAtUs:    createdAt.UnixMicro(),
			},
		},
	}
}

func (a *openlogWriteAttempt) prepPath() string {
	return a.shard.prepPath(a.writeID)
}
