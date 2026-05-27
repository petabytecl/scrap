package shard_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
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

func TestAppendReplicatedDocument_CorrectOffsetAfterPriorAppend(t *testing.T) {
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
	_, err = s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-second",
		DocumentName:  "b.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		StartOffset:   40,
		FrameCount:    1,
		TotalBytes:    int64(len(second)),
		Sha256:        secondSum[:],
	}, bytes.NewReader(second))
	if err == nil {
		t.Fatal("expected error: StartOffset=40 should not match after first append")
	}
	if !strings.Contains(err.Error(), "replica offset") {
		t.Fatalf("expected offset mismatch error, got: %v", err)
	}
}
