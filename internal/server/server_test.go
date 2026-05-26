package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/spike"
)

func startTestServer(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()

	dir := t.TempDir()
	s, err := spike.Open(dir)
	if err != nil {
		t.Fatalf("spike.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func TestGRPCWriteAndRead(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("grpc test data "), 100)

	resp := writeDocument(ctx, t, client, "tx-grpc-001", "test.xml", "text/xml", content)
	verifyWriteResponse(t, resp, content)
	readContent := readDocument(ctx, t, client, "tx-grpc-001", "test.xml")

	if !bytes.Equal(readContent, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(readContent), len(content))
	}
}

const writeChunkSize = 1024

func writeDocument(ctx context.Context, t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, contentType string, content []byte) *scrapv1.WriteDocumentResponse {
	t.Helper()

	stream, err := client.WriteDocument(ctx)
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId: txID,
				DocumentName:  docName,
				ContentType:   contentType,
			},
		},
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}

	for i := 0; i < len(content); i += writeChunkSize {
		end := min(i+writeChunkSize, len(content))
		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			Part: &scrapv1.WriteDocumentRequest_ChunkData{
				ChunkData: content[i:end],
			},
		}); err != nil {
			t.Fatalf("Send chunk: %v", err)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	return resp
}

func verifyWriteResponse(t *testing.T, resp *scrapv1.WriteDocumentResponse, content []byte) {
	t.Helper()

	if resp.GetSha256Checksum() == "" {
		t.Fatal("checksum should not be empty")
	}

	const hexSHA256Len = 64
	if len(resp.GetSha256Checksum()) != hexSHA256Len {
		t.Fatalf("checksum should be %d hex chars, got %d", hexSHA256Len, len(resp.GetSha256Checksum()))
	}
	if resp.GetSize() != int64(len(content)) {
		t.Fatalf("size: got %d, want %d", resp.GetSize(), len(content))
	}
}

func readDocument(ctx context.Context, t *testing.T, client scrapv1.DocumentServiceClient, txID, docName string) []byte {
	t.Helper()

	readStream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: txID,
		DocumentName:  docName,
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	firstMsg, err := readStream.Recv()
	if err != nil {
		t.Fatalf("Recv first: %v", err)
	}
	meta := firstMsg.GetMeta()
	if meta == nil {
		t.Fatal("first message should be meta")
	}
	if meta.GetContentType() != "text/xml" {
		t.Fatalf("ContentType: got %q", meta.GetContentType())
	}

	const hexSHA256Len = 64
	if len(meta.GetSha256Checksum()) != hexSHA256Len {
		t.Fatalf("meta checksum should be %d hex chars, got %d", hexSHA256Len, len(meta.GetSha256Checksum()))
	}

	var readContent []byte
	for {
		msg, err := readStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		readContent = append(readContent, msg.GetChunkData()...)
	}
	return readContent
}
