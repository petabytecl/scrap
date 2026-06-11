package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestOpenShardSetClosesEarlierShardWhenLaterOpenFails(t *testing.T) {
	var closed []uint64
	openCfg := shardSetOpenConfig{
		cfg: Config{DataDir: t.TempDir()},
		topology: startupTopology{
			LocalShardIDs: []uint64{7, 9},
		},
	}

	_, err := openShardSetWithOpener(openCfg, func(_ shardSetOpenConfig, shardID uint64, _ string) (openedLocalShard, error) {
		if shardID == 9 {
			return openedLocalShard{}, errors.New("injected open failure")
		}
		id := shardID
		return openedLocalShard{
			close: func() error {
				closed = append(closed, id)
				return nil
			},
		}, nil
	})
	if err == nil {
		t.Fatal("openShardSetWithOpener succeeded, want later Shard open failure")
	}
	if !strings.Contains(err.Error(), "open Shard 9") {
		t.Fatalf("openShardSetWithOpener error = %v, want Shard 9 context", err)
	}
	if got, want := closed, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("closed Shards = %v, want %v", got, want)
	}
}

func TestShardSetReplicationSinkDispatchesByShardID(t *testing.T) {
	targets := fakeReplicationTargets{
		7: &recordingReplicationTarget{sha: []byte("shard-7")},
		9: &recordingReplicationTarget{sha: []byte("shard-9")},
	}
	sink := shardSetReplicationSink{shards: targets}

	got, err := sink.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{ShardId: 7}, strings.NewReader("seven"))
	if err != nil {
		t.Fatalf("AppendReplicatedDocument Shard 7: %v", err)
	}
	if string(got) != "shard-7" || targets[7].body != "seven" {
		t.Fatalf("Shard 7 dispatch got sha/body %q/%q, want shard-7/seven", string(got), targets[7].body)
	}

	got, err = sink.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{ShardId: 9}, strings.NewReader("nine"))
	if err != nil {
		t.Fatalf("AppendReplicatedDocument Shard 9: %v", err)
	}
	if string(got) != "shard-9" || targets[9].body != "nine" {
		t.Fatalf("Shard 9 dispatch got sha/body %q/%q, want shard-9/nine", string(got), targets[9].body)
	}

	_, err = sink.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{ShardId: 11}, strings.NewReader("missing"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unknown Shard error = %v (%s), want FAILED_PRECONDITION", err, status.Code(err))
	}
}

func TestShardSetRaftRouterDispatchesByShardID(t *testing.T) {
	targets := fakeRaftTargets{
		7: &recordingRaftTarget{},
		9: &recordingRaftTarget{},
	}
	router := shardSetRaftRouter{shards: targets}

	if err := router.RouteRaftMessage(context.Background(), 7, raftpb.Message{To: 7}); err != nil {
		t.Fatalf("RouteRaftMessage Shard 7: %v", err)
	}
	if targets[7].last.To != 7 {
		t.Fatalf("Shard 7 target got message To=%d, want 7", targets[7].last.To)
	}

	if err := router.RouteRaftMessage(context.Background(), 9, raftpb.Message{To: 9}); err != nil {
		t.Fatalf("RouteRaftMessage Shard 9: %v", err)
	}
	if targets[9].last.To != 9 {
		t.Fatalf("Shard 9 target got message To=%d, want 9", targets[9].last.To)
	}

	err := router.RouteRaftMessage(context.Background(), 11, raftpb.Message{To: 11})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unknown Shard error = %v (%s), want FAILED_PRECONDITION", err, status.Code(err))
	}
}

func TestShardSetBlockDirResolverDispatchesByShardID(t *testing.T) {
	set := &shardSet{
		ids:       []uint64{7, 9},
		blockDirs: map[uint64]string{7: "/redacted/shard-7/blocks", 9: "/redacted/shard-9/blocks"},
	}

	if got, ok := set.BlockDirForShard(7); !ok || got != "/redacted/shard-7/blocks" {
		t.Fatalf("BlockDirForShard(7) = %q/%v, want shard 7 blocks", got, ok)
	}
	if got, ok := set.BlockDirForShard(9); !ok || got != "/redacted/shard-9/blocks" {
		t.Fatalf("BlockDirForShard(9) = %q/%v, want shard 9 blocks", got, ok)
	}
	if got, ok := set.BlockDirForShard(11); ok || got != "" {
		t.Fatalf("BlockDirForShard(11) = %q/%v, want missing", got, ok)
	}
}

type fakeReplicationTargets map[uint64]*recordingReplicationTarget

func (t fakeReplicationTargets) replicationTarget(shardID uint64) (replicationTarget, bool) {
	target, ok := t[shardID]
	return target, ok
}

type recordingReplicationTarget struct {
	sha  []byte
	body string
}

func (t *recordingReplicationTarget) AppendReplicatedDocument(_ context.Context, _ *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	t.body = string(data)
	return append([]byte(nil), t.sha...), nil
}

type fakeRaftTargets map[uint64]*recordingRaftTarget

func (t fakeRaftTargets) raftTarget(shardID uint64) (raftTarget, bool) {
	target, ok := t[shardID]
	return target, ok
}

type recordingRaftTarget struct {
	last raftpb.Message
}

func (t *recordingRaftTarget) RaftStep(_ context.Context, msg raftpb.Message) error {
	t.last = msg
	return nil
}
