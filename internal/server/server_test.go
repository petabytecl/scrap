package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/spike"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startTestServer(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()

	dir := t.TempDir()
	s, err := spike.Open(dir)
	if err != nil {
		t.Fatalf("spike.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s)
	go gs.Serve(lis)
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func TestGRPCWriteAndRead(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("grpc test data "), 100)

	stream, err := client.WriteDocument(ctx)
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		TransactionId: "tx-grpc-001",
		DocumentName:  "test.xml",
		ContentType:   "text/xml",
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}

	chunkSize := 1024
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			ChunkData: content[i:end],
		}); err != nil {
			t.Fatalf("Send chunk: %v", err)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	if resp.GetSha256Checksum() == "" {
		t.Fatal("checksum should not be empty")
	}
	if resp.GetSize() != int64(len(content)) {
		t.Fatalf("size: got %d, want %d", resp.GetSize(), len(content))
	}

	readStream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-grpc-001",
		DocumentName:  "test.xml",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	var readContent []byte
	for {
		msg, err := readStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		readContent = append(readContent, msg.GetChunkData()...)
	}

	if !bytes.Equal(readContent, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(readContent), len(content))
	}
}
