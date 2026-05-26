package integration_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/spike"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func startIntegrationServer(t *testing.T, sealSize int64) (scrapv1.DocumentServiceClient, string) {
	t.Helper()

	dir := t.TempDir()
	if sealSize <= 0 {
		sealSize = 64 * 1024 * 1024
	}
	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: sealSize})
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

	return scrapv1.NewDocumentServiceClient(conn), dir
}

func writeDoc(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, ct string, data []byte) *scrapv1.WriteDocumentResponse {
	t.Helper()
	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId: txID,
				DocumentName:  docName,
				ContentType:   ct,
			},
		},
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}

	for i := 0; i < len(data); i += 32 * 1024 {
		end := min(i+32*1024, len(data))
		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			Part: &scrapv1.WriteDocumentRequest_ChunkData{
				ChunkData: data[i:end],
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

func readDoc(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName string) []byte {
	t.Helper()
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: txID,
		DocumentName:  docName,
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	var content []byte
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content = append(content, msg.GetChunkData()...)
	}
	return content
}

func TestBasicWriteRead(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)
	content := bytes.Repeat([]byte("basic test "), 1024)
	resp := writeDoc(t, client, "tx-basic", "doc.xml", "text/xml", content)

	if len(resp.GetSha256Checksum()) != 64 {
		t.Fatalf("checksum not 64 hex chars: %d", len(resp.GetSha256Checksum()))
	}

	got := readDoc(t, client, "tx-basic", "doc.xml")
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(content))
	}
}

func TestLargeDocument128MiB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 128 MiB test in short mode")
	}

	client, _ := startIntegrationServer(t, 256*1024*1024)
	content := make([]byte, 128*1024*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}

	resp := writeDoc(t, client, "tx-large", "huge.bin", "application/octet-stream", content)
	if resp.GetSize() != int64(len(content)) {
		t.Fatalf("size: got %d, want %d", resp.GetSize(), len(content))
	}

	got := readDoc(t, client, "tx-large", "huge.bin")
	if !bytes.Equal(got, content) {
		t.Fatalf("128 MiB content mismatch")
	}
}

func TestMultiDocTransaction(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)
	ctx := context.Background()

	for i := range 5 {
		name := string(rune('a'+i)) + ".xml"
		writeDoc(t, client, "tx-multi", name, "text/xml", bytes.Repeat([]byte{byte('A' + i)}, 256))
	}

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{TransactionId: "tx-multi"})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 5 {
		t.Fatalf("got %d docs, want 5", len(resp.GetDocuments()))
	}
}

func TestMultiBlockTransactionOrdering(t *testing.T) {
	client, _ := startIntegrationServer(t, 64*1024)
	ctx := context.Background()

	for i := range 4 {
		name := string(rune('a'+i)) + ".xml"
		writeDoc(t, client, "tx-order", name, "text/xml", bytes.Repeat([]byte{byte('A' + i)}, 30*1024))
	}

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{TransactionId: "tx-order"})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 4 {
		t.Fatalf("got %d docs, want 4", len(resp.GetDocuments()))
	}
}

func TestHeadDocumentMetadata(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)
	ctx := context.Background()

	content := bytes.Repeat([]byte("head"), 100)
	writeDoc(t, client, "tx-head", "meta.xml", "application/xml", content)

	resp, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-head",
		DocumentName:  "meta.xml",
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}

	if resp.GetSize() != int64(len(content)) {
		t.Fatalf("size: got %d, want %d", resp.GetSize(), len(content))
	}
	if resp.GetContentType() != "application/xml" {
		t.Fatalf("content_type: got %q", resp.GetContentType())
	}
	if len(resp.GetSha256Checksum()) != 64 {
		t.Fatal("sha256 not 64 hex chars")
	}
	if resp.GetCreatedAt() == nil {
		t.Fatal("created_at should be set")
	}
}

func TestCorruptBlockDataLoss(t *testing.T) {
	client, dir := startIntegrationServer(t, 0)

	writeDoc(t, client, "tx-corrupt", "doc.xml", "text/xml", bytes.Repeat([]byte("X"), 512))

	blks, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	if len(blks) == 0 {
		t.Fatal("no block files")
	}
	data, _ := os.ReadFile(blks[0])
	data[100] ^= 0xFF
	os.WriteFile(blks[0], data, 0644)

	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-corrupt",
		DocumentName:  "doc.xml",
	})
	if err != nil {
		t.Fatalf("ReadDocument call: %v", err)
	}

	var gotBytes int
	for {
		msg, err := stream.Recv()
		if err != nil {
			s, ok := status.FromError(err)
			if !ok || s.Code() != codes.DataLoss {
				t.Fatalf("expected DATA_LOSS, got: %v", err)
			}
			break
		}
		gotBytes += len(msg.GetChunkData())
	}
	if gotBytes > 0 {
		t.Fatalf("expected zero bytes on corruption, got %d", gotBytes)
	}
}

func TestBlockSealingLifecycle(t *testing.T) {
	client, dir := startIntegrationServer(t, 64*1024)

	for i := range 3 {
		name := string(rune('a'+i)) + ".bin"
		writeDoc(t, client, "tx-seal", name, "application/octet-stream", bytes.Repeat([]byte{byte(i)}, 30*1024))
	}

	blks, _ := filepath.Glob(filepath.Join(dir, "blocks", "*.blk"))
	if len(blks) < 2 {
		t.Fatalf("expected at least 2 block files, got %d", len(blks))
	}

	for i := range 3 {
		name := string(rune('a'+i)) + ".bin"
		got := readDoc(t, client, "tx-seal", name)
		if len(got) != 30*1024 {
			t.Fatalf("doc %d: got %d bytes, want %d", i, len(got), 30*1024)
		}
	}
}

func TestDuplicateWrite(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)

	writeDoc(t, client, "tx-dup", "a.xml", "text/xml", []byte("data"))

	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	_ = stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{TransactionId: "tx-dup", DocumentName: "a.xml", ContentType: "text/xml"},
		},
	})
	_ = stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("data")},
	})

	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("expected error on duplicate")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.AlreadyExists {
		t.Fatalf("expected ALREADY_EXISTS, got %v", err)
	}
}

func TestReadNotFound(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)

	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "nonexistent",
		DocumentName:  "nope.xml",
	})
	if err != nil {
		t.Fatalf("ReadDocument call: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.NotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestFindDocumentsEmpty(t *testing.T) {
	client, _ := startIntegrationServer(t, 0)

	resp, err := client.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "nonexistent",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 0 {
		t.Fatalf("expected empty list, got %d", len(resp.GetDocuments()))
	}
}

func TestRestartDurability(t *testing.T) {
	dir := t.TempDir()
	sealSize := int64(64 * 1024 * 1024)

	s, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: sealSize})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	gs := grpc.NewServer()
	server.Register(gs, s)
	go gs.Serve(lis)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	client := scrapv1.NewDocumentServiceClient(conn)

	content := bytes.Repeat([]byte("durable"), 100)
	writeDoc(t, client, "tx-durable", "persist.xml", "text/xml", content)

	conn.Close()
	gs.GracefulStop()
	s.Close()

	s2, err := spike.OpenWithConfig(dir, spike.Config{BlockSealSize: sealSize})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer s2.Close()

	lis2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	gs2 := grpc.NewServer()
	server.Register(gs2, s2)
	go gs2.Serve(lis2)
	defer gs2.GracefulStop()

	conn2, err := grpc.NewClient(lis2.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn2.Close()
	client2 := scrapv1.NewDocumentServiceClient(conn2)

	got := readDoc(t, client2, "tx-durable", "persist.xml")
	if !bytes.Equal(got, content) {
		t.Fatalf("content after restart mismatch: got %d bytes", len(got))
	}
}
