package peer_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestTransferBlockStreamsFileContents(t *testing.T) {
	dir := t.TempDir()

	bw, err := block.NewBlockWriter(dir+"/0000000000000001.blk", 0, 1)
	if err != nil {
		t.Fatalf("NewBlockWriter: %v", err)
	}
	content := bytes.Repeat([]byte("transfer test "), 100)
	result, err := bw.AppendDocument("tx-transfer", "doc.xml", "text/xml", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	bw.Close()

	iw, err := block.NewIndexWriter(dir + "/0000000000000001.idx")
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	iw.Append(block.IndexEntry{
		TransactionID: "tx-transfer",
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	})
	iw.Close()

	addr, _ := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	client := scrapv1.NewPeerServiceClient(conn)
	stream, err := client.TransferBlock(context.Background(), &scrapv1.TransferBlockRequest{
		ShardId: 0,
		BlockId: 1,
	})
	if err != nil {
		t.Fatalf("TransferBlock: %v", err)
	}

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

	var received []byte
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		received = append(received, msg.GetChunkData()...)
	}

	expectedTotal := meta.GetBlockSize() + meta.GetIdxSize()
	if int64(len(received)) != expectedTotal {
		t.Fatalf("received %d bytes, expected %d (blk=%d + idx=%d)",
			len(received), expectedTotal, meta.GetBlockSize(), meta.GetIdxSize())
	}
}

func TestTransferBlockNotFound(t *testing.T) {
	dir := t.TempDir()
	addr, _ := startPeerServer(t, dir)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

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
