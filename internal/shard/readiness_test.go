package shard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestCheckReadinessWithinGracePeriod(t *testing.T) {
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
		TickInterval:   50 * time.Millisecond,
		BootstrapGrace: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	time.Sleep(100 * time.Millisecond)

	if err := s.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness should succeed during grace period: %v", err)
	}
}

func TestCheckReadinessFailsAfterGraceExpires(t *testing.T) {
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
		TickInterval:   50 * time.Millisecond,
		BootstrapGrace: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	time.Sleep(50 * time.Millisecond)

	if err := s.CheckReadiness(context.Background()); err == nil {
		t.Fatal("CheckReadiness should fail after grace period expires with no leader")
	}
}

func TestCheckReadinessSucceedsWithLeader(t *testing.T) {
	s := openTestShard(t)

	if err := s.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness with leader: %v", err)
	}
}

func TestCheckReadinessFailsWhileRebuilding(t *testing.T) {
	s := openTestShard(t)
	s.SetRebuildingForTest(true)
	defer s.SetRebuildingForTest(false)

	err := s.CheckReadiness(context.Background())
	if !errors.Is(err, storeapi.ErrRebuilding) {
		t.Fatalf("CheckReadiness error: got %v, want ErrRebuilding", err)
	}
}
