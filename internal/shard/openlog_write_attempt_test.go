package shard

import (
	"crypto/sha256"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
)

func TestBeginOpenlogWriteAttemptWritesPrepEntry(t *testing.T) {
	t.Parallel()

	s := &Shard{openlogDir: t.TempDir()}
	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:           "tx-1",
		docName:        "doc.xml",
		contentType:    "text/xml",
		idempotencyKey: "idem-1",
		blockID:        7,
		startOffset:    40,
	})
	if err != nil {
		t.Fatalf("beginOpenlogWriteAttempt: %v", err)
	}

	data, err := os.ReadFile(attempt.prepPath())
	if err != nil {
		t.Fatalf("ReadFile prep: %v", err)
	}
	var got scrapv1.OpenlogEntry
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal prep: %v", err)
	}
	want := &scrapv1.OpenlogEntry{
		TransactionId:  "tx-1",
		DocumentName:   "doc.xml",
		BlockId:        7,
		StartOffset:    40,
		ContentType:    "text/xml",
		IdempotencyKey: "idem-1",
	}
	if !proto.Equal(&got, want) {
		t.Fatalf("prep entry mismatch\ngot:  %v\nwant: %v", &got, want)
	}
}

func TestOpenlogWriteAttemptCleanupOnAbortRemovesPrep(t *testing.T) {
	t.Parallel()

	s := &Shard{openlogDir: t.TempDir()}
	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:        "tx-1",
		docName:     "doc.xml",
		contentType: "text/xml",
		blockID:     7,
		startOffset: 40,
	})
	if err != nil {
		t.Fatalf("beginOpenlogWriteAttempt: %v", err)
	}

	attempt.cleanupOnAbort()

	if _, err := os.Stat(attempt.prepPath()); !os.IsNotExist(err) {
		t.Fatalf("prep file should be removed after abort cleanup, err=%v", err)
	}
}

func TestOpenlogWriteAttemptCompleteRemovesPrep(t *testing.T) {
	t.Parallel()

	s := &Shard{openlogDir: t.TempDir()}
	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:        "tx-1",
		docName:     "doc.xml",
		contentType: "text/xml",
		blockID:     7,
		startOffset: 40,
	})
	if err != nil {
		t.Fatalf("beginOpenlogWriteAttempt: %v", err)
	}

	attempt.complete()
	attempt.cleanupOnAbort()

	if _, err := os.Stat(attempt.prepPath()); !os.IsNotExist(err) {
		t.Fatalf("prep file should be removed after completion, err=%v", err)
	}
}

func TestOpenlogWriteAttemptCommitCommand(t *testing.T) {
	t.Parallel()

	s := &Shard{openlogDir: t.TempDir()}
	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:           "tx-1",
		docName:        "doc.xml",
		contentType:    "text/xml",
		idempotencyKey: "idem-1",
		blockID:        7,
		startOffset:    40,
	})
	if err != nil {
		t.Fatalf("beginOpenlogWriteAttempt: %v", err)
	}

	sum := sha256.Sum256([]byte("payload"))
	createdAt := time.UnixMicro(123456)
	cmd := attempt.commitCommand(block.AppendResult{
		SHA256:           sum,
		Size:             99,
		FrameCount:       3,
		FirstFrameOffset: 88,
	}, createdAt, []byte(`{"version":1}`))

	got := cmd.GetCommitDoc()
	want := &scrapv1.CommitDocument{
		TransactionId:      "tx-1",
		DocumentName:       "doc.xml",
		ContentType:        "text/xml",
		IdempotencyKey:     "idem-1",
		BlockId:            7,
		FirstFrameOff:      88,
		FrameCount:         3,
		TotalBytes:         99,
		Sha256:             sum[:],
		CreatedAtUs:        123456,
		EncryptionEnvelope: []byte(`{"version":1}`),
	}
	if !proto.Equal(got, want) {
		t.Fatalf("commit document mismatch\ngot:  %v\nwant: %v", got, want)
	}
}
