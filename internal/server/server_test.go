package server_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

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

func TestGRPCWriteRejectsInvalidMetadata(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	tests := []struct {
		name string
		init *scrapv1.WriteDocumentInit
	}{
		{
			name: "control transaction",
			init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-\n1",
				DocumentName:  "doc.xml",
				ContentType:   "text/xml",
			},
		},
		{
			name: "oversized document name",
			init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-1",
				DocumentName:  string(bytes.Repeat([]byte("d"), 513)),
				ContentType:   "text/xml",
			},
		},
		{
			name: "missing content type",
			init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-1",
				DocumentName:  "doc.xml",
			},
		},
		{
			name: "oversized tenant",
			init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-1",
				DocumentName:  "doc.xml",
				ContentType:   "text/xml",
				TenantId:      string(bytes.Repeat([]byte("t"), 257)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeDocumentWithMessages(ctx, client, []*scrapv1.WriteDocumentRequest{
				{Part: &scrapv1.WriteDocumentRequest_Init{Init: tt.init}},
				{Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("payload")}},
			})
			assertStatusCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGRPCWriteRejectsDuplicateInitMidStream(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	init := &scrapv1.WriteDocumentInit{
		TransactionId: "tx-dup-init",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
	}
	err := writeDocumentWithMessages(ctx, client, []*scrapv1.WriteDocumentRequest{
		{Part: &scrapv1.WriteDocumentRequest_Init{Init: init}},
		{Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("pay")}},
		{Part: &scrapv1.WriteDocumentRequest_Init{Init: init}},
		{Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("load")}},
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestGRPCWriteRejectsZeroByteDocument(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	err := writeDocumentWithMessages(ctx, client, []*scrapv1.WriteDocumentRequest{
		{Part: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			TransactionId: "tx-empty",
			DocumentName:  "empty.xml",
			ContentType:   "text/xml",
		}}},
	})
	assertStatusCode(t, err, codes.InvalidArgument)
}

func TestGRPCWriteRejectsOversizedChunk(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	err := writeDocumentWithMessages(ctx, client, []*scrapv1.WriteDocumentRequest{
		{Part: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			TransactionId: "tx-large-chunk",
			DocumentName:  "large.bin",
			ContentType:   "application/octet-stream",
		}}},
		{Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: bytes.Repeat([]byte("x"), 1<<20+1)}},
	})
	assertStatusCode(t, err, codes.ResourceExhausted)
}

func TestGRPCReadRejectsInvalidIdentity(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	_, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-\n1",
		DocumentName:  "doc.xml",
	})
	assertStatusCode(t, err, codes.InvalidArgument)

	_, err = client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-1",
		TenantId:      string(bytes.Repeat([]byte("t"), 257)),
	})
	assertStatusCode(t, err, codes.InvalidArgument)
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

func writeDocumentWithMessages(ctx context.Context, client scrapv1.DocumentServiceClient, messages []*scrapv1.WriteDocumentRequest) error {
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return fmt.Errorf("WriteDocument: %w", err)
	}
	for _, msg := range messages {
		if err := stream.Send(msg); err != nil {
			if errors.Is(err, io.EOF) {
				_, err = stream.CloseAndRecv()
				if err != nil {
					return fmt.Errorf("close write document stream: %w", err)
				}
				return nil
			}
			return fmt.Errorf("send write document message: %w", err)
		}
	}
	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("close write document stream: %w", err)
	}
	return nil
}

func assertStatusCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	if got := status.Code(err); got != want {
		t.Fatalf("status.Code = %s, want %s (err=%v)", got, want, err)
	}
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
