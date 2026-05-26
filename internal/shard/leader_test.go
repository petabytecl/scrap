package shard_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestNonLeaderWriteReturnsNotLeaderError(t *testing.T) {
	dir := t.TempDir()
	s, err := shard.Open(shard.Config{
		DataDir: dir,
		ShardID: 0,
		RaftID:  2,
		Peers: map[uint64]string{
			1: "localhost:9091",
			2: "localhost:9092",
			3: "localhost:9093",
		},
		TickInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = s.WriteDocument(ctx, "tx-001", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error from non-leader")
	}

	var nle *storeapi.NotLeaderError
	if !isNotLeaderError(err, &nle) {
		t.Fatalf("expected NotLeaderError, got: %T %v", err, err)
	}
}

func TestNonLeaderHeadReturnsNotLeaderError(t *testing.T) {
	dir := t.TempDir()
	s, err := shard.Open(shard.Config{
		DataDir: dir,
		ShardID: 0,
		RaftID:  2,
		Peers: map[uint64]string{
			1: "localhost:9091",
			2: "localhost:9092",
			3: "localhost:9093",
		},
		TickInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	time.Sleep(100 * time.Millisecond)

	_, err = s.HeadDocument(context.Background(), "tx-001", "doc.xml")
	if err == nil {
		t.Fatal("expected error from non-leader head")
	}

	var nle *storeapi.NotLeaderError
	if !isNotLeaderError(err, &nle) {
		t.Fatalf("expected NotLeaderError, got: %T %v", err, err)
	}
}

func isNotLeaderError(err error, target **storeapi.NotLeaderError) bool {
	for err != nil {
		if nle, ok := err.(*storeapi.NotLeaderError); ok {
			*target = nle
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}
