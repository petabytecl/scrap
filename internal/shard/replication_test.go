package shard_test

import (
	"bytes"
	"context"
	"crypto/sha256"
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
