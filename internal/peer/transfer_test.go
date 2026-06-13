package peer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/scrub"
)

func TestTransferBlockStreamsFileContents(t *testing.T) {
	dir := t.TempDir()
	seedTransferBlock(t, dir)
	addr := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(context.Background(), &scrapv1.TransferBlockRequest{
		ShardId: 0,
		BlockId: 1,
	})
	if err != nil {
		t.Fatalf("TransferBlock: %v", err)
	}

	meta := recvAndVerifyMeta(t, stream)
	received := recvAllChunks(t, stream)

	expectedTotal := meta.GetBlockSize() + meta.GetIdxSize()
	if int64(len(received)) != expectedTotal {
		t.Fatalf("received %d bytes, expected %d (blk=%d + idx=%d)",
			len(received), expectedTotal, meta.GetBlockSize(), meta.GetIdxSize())
	}
}

func TestTransferBlockUsesResolverForRequestShard(t *testing.T) {
	root := t.TempDir()
	shard7Dir := filepath.Join(root, "shard-7")
	shard9Dir := filepath.Join(root, "shard-9")
	for _, dir := range []string{shard7Dir, shard9Dir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	seedTransferBlockForShard(t, shard7Dir, 7, bytes.Repeat([]byte("payload shard 7 "), 32))
	seedTransferBlockForShard(t, shard9Dir, 9, bytes.Repeat([]byte("payload shard 9 "), 32))
	addr := startPeerServer(t, filepath.Join(root, "unused"),
		peer.WithBlockDirResolver(staticBlockDirResolver{
			7: shard7Dir,
			9: shard9Dir,
		}),
	)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(context.Background(), &scrapv1.TransferBlockRequest{
		ShardId: 9,
		BlockId: 1,
	})
	if err != nil {
		t.Fatalf("TransferBlock: %v", err)
	}
	_ = recvAndVerifyMeta(t, stream)
	received := recvAllChunks(t, stream)

	if !bytes.Contains(received, []byte("payload shard 9")) {
		t.Fatalf("TransferBlock did not stream Shard 9 payload")
	}
	if bytes.Contains(received, []byte("payload shard 7")) {
		t.Fatalf("TransferBlock streamed payload from wrong Shard directory")
	}
}

// seedTransferBlock creates a block file and index file in dir for block ID 1.
func seedTransferBlock(t *testing.T, dir string) {
	t.Helper()
	seedTransferBlockForShard(t, dir, 0, bytes.Repeat([]byte("transfer test "), 100))
}

func seedTransferBlockForShard(t *testing.T, dir string, shardID uint64, content []byte) {
	t.Helper()

	bw, err := block.NewWriter(dir+"/0000000000000001.blk", shardID, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	result, err := bw.AppendDocument("tx-transfer", "doc.xml", "text/xml", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	_ = bw.Close()

	iw, err := block.NewIndexWriter(dir + "/0000000000000001.idx")
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-transfer",
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}); err != nil {
		t.Fatalf("iw.Append: %v", err)
	}
	_ = iw.Close()
}

type staticBlockDirResolver map[uint64]string

func (r staticBlockDirResolver) BlockDirForShard(shardID uint64) (string, bool) {
	dir, ok := r[shardID]
	return dir, ok
}

func recvAndVerifyMeta(t *testing.T, stream grpc.ServerStreamingClient[scrapv1.TransferBlockResponse]) *scrapv1.TransferBlockMeta {
	t.Helper()

	firstMsg, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv meta: %v", err)
	}
	meta := firstMsg.GetMeta()
	if meta == nil {
		t.Fatal("first message should be meta")
	}
	if meta.GetBlockId() != 1 {
		t.Fatalf("BlockId: got %d", meta.GetBlockId())
	}
	if meta.GetBlockSize() <= 0 {
		t.Fatalf("BlockSize should be > 0, got %d", meta.GetBlockSize())
	}
	if meta.GetIdxSize() <= 0 {
		t.Fatalf("IdxSize should be > 0, got %d", meta.GetIdxSize())
	}
	return meta
}

func recvAllChunks(t *testing.T, stream grpc.ServerStreamingClient[scrapv1.TransferBlockResponse]) []byte {
	t.Helper()

	var received []byte
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		received = append(received, msg.GetChunkData()...)
	}
	return received
}

func TestTransferBlockNotFound(t *testing.T) {
	dir := t.TempDir()
	addr := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(context.Background(), &scrapv1.TransferBlockRequest{
		ShardId: 0,
		BlockId: 999,
	})
	if err != nil {
		t.Fatalf("TransferBlock: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for nonexistent block")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		t.Fatalf("expected NOT_FOUND, got: %v", err)
	}
}

func TestTransferBlockRejectsShardHeaderMismatch(t *testing.T) {
	dir := t.TempDir()
	seedTransferBlock(t, dir)
	addr := startPeerServer(t, dir)

	err := transferBlockFirstRecvErrorForShard(t, addr, 1, 1)
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.DataLoss || !strings.Contains(st.Message(), "block header verification failed") {
		t.Fatalf("TransferBlock Shard mismatch error = %v, want DATA_LOSS header verification failure", err)
	}
}

func TestTransferBlockDistinguishesLocalLossStates(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*testing.T, string)
		wantCode codes.Code
		wantText string
	}{
		{
			name: "metadata loss",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				seedTransferBlock(t, dir)
				if err := os.Remove(block.IdxFilePath(dir, 1)); err != nil {
					t.Fatalf("remove index: %v", err)
				}
			},
			wantCode: codes.DataLoss,
			wantText: "block_metadata_loss",
		},
		{
			name: "unexpected data loss",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				seedTransferBlock(t, dir)
				if err := os.Remove(block.FilePath(dir, 1)); err != nil {
					t.Fatalf("remove block: %v", err)
				}
			},
			wantCode: codes.DataLoss,
			wantText: "block_unexpected_loss",
		},
		{
			name: "quarantined",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				seedTransferBlock(t, dir)
				if err := block.Quarantine(block.FilePath(dir, 1)); err != nil {
					t.Fatalf("Quarantine: %v", err)
				}
			},
			wantCode: codes.DataLoss,
			wantText: "block_quarantined",
		},
		{
			name: "evicted metadata loss",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				seedTransferBlock(t, dir)
				writeTransferEvictionMarker(t, dir, 1)
				if err := os.Remove(block.FilePath(dir, 1)); err != nil {
					t.Fatalf("remove block: %v", err)
				}
				if err := os.Remove(block.IdxFilePath(dir, 1)); err != nil {
					t.Fatalf("remove index: %v", err)
				}
			},
			wantCode: codes.DataLoss,
			wantText: "block_metadata_loss",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.mutate(t, dir)
			addr := startPeerServer(t, dir)
			err := transferBlockFirstRecvError(t, addr, 1)
			st, ok := status.FromError(err)
			if !ok || st.Code() != tt.wantCode || !strings.Contains(st.Message(), tt.wantText) {
				t.Fatalf("TransferBlock error = %v, want %s containing %q", err, tt.wantCode, tt.wantText)
			}
		})
	}
}

func TestTransferBlockEvictedReturnsFailedPrecondition(t *testing.T) {
	dir := t.TempDir()
	seedTransferBlock(t, dir)
	writeTransferEvictionMarker(t, dir, 1)
	if err := os.Remove(block.FilePath(dir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}
	addr := startPeerServer(t, dir)

	c := peer.NewClient()
	defer c.Close()
	_, _, err := c.TransferBlock(context.Background(), addr, 0, 1)
	if !errors.Is(err, scrub.ErrPeerBlockEvicted) {
		t.Fatalf("TransferBlock error = %v, want ErrPeerBlockEvicted", err)
	}
}

func transferBlockFirstRecvError(t *testing.T, addr string, blockID uint64) error {
	t.Helper()
	return transferBlockFirstRecvErrorForShard(t, addr, 0, blockID)
}

func transferBlockFirstRecvErrorForShard(t *testing.T, addr string, shardID, blockID uint64) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(context.Background(), &scrapv1.TransferBlockRequest{
		ShardId: shardID,
		BlockId: blockID,
	})
	if err != nil {
		return fmt.Errorf("TransferBlock: %w", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected TransferBlock Recv error")
	}
	return err
}

func writeTransferEvictionMarker(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	if err := localblock.WriteEvictionMarker(dir, localblock.EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0/1.blk",
		SizeBytes:       123,
		ValidationToken: "etag",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         localblock.EvictionTriggerOperatorRequested,
		Reason:          localblock.EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}
