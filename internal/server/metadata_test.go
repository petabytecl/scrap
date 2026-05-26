package server_test

import (
	"bytes"
	"context"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func writeDoc(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, contentType string, data []byte) {
	t.Helper()
	ctx := context.Background()

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

	for i := 0; i < len(data); i += 4096 {
		end := min(i+4096, len(data))
		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			Part: &scrapv1.WriteDocumentRequest_ChunkData{
				ChunkData: data[i:end],
			},
		}); err != nil {
			t.Fatalf("Send chunk: %v", err)
		}
	}

	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
}

func TestGRPCHeadDocument(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("head test "), 50)
	writeDoc(t, client, "tx-head", "doc.xml", "application/xml", content)

	resp, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-head",
		DocumentName:  "doc.xml",
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}

	if resp.GetName() != "doc.xml" {
		t.Fatalf("Name: got %q, want doc.xml", resp.GetName())
	}
	if resp.GetContentType() != "application/xml" {
		t.Fatalf("ContentType: got %q", resp.GetContentType())
	}
	if resp.GetSize() != int64(len(content)) {
		t.Fatalf("Size: got %d, want %d", resp.GetSize(), len(content))
	}
	if len(resp.GetSha256Checksum()) != 64 {
		t.Fatalf("SHA256 should be 64 hex chars, got %d", len(resp.GetSha256Checksum()))
	}
	if resp.GetCreatedAt() == nil {
		t.Fatal("CreatedAt should be set")
	}
}

func TestGRPCHeadDocumentNotFound(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	_, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "nonexistent",
		DocumentName:  "nope.xml",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.NotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestGRPCFindDocuments(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	for i := range 5 {
		name := string(rune('a'+i)) + ".xml"
		writeDoc(t, client, "tx-find", name, "text/xml", bytes.Repeat([]byte{byte('A' + i)}, 256))
	}

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}

	if len(resp.GetDocuments()) != 5 {
		t.Fatalf("got %d docs, want 5", len(resp.GetDocuments()))
	}

	for _, doc := range resp.GetDocuments() {
		if doc.GetName() == "" {
			t.Fatal("document name should not be empty")
		}
		if doc.GetContentType() != "text/xml" {
			t.Fatalf("ContentType: got %q", doc.GetContentType())
		}
		if doc.GetSize() != 256 {
			t.Fatalf("Size: got %d, want 256", doc.GetSize())
		}
		if len(doc.GetSha256Checksum()) != 64 {
			t.Fatalf("SHA256 should be 64 hex chars")
		}
	}
}

func TestGRPCFindDocumentsEmptyTransaction(t *testing.T) {
	client := startTestServer(t)
	ctx := context.Background()

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "nonexistent",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}

	if len(resp.GetDocuments()) != 0 {
		t.Fatalf("expected empty list, got %d docs", len(resp.GetDocuments()))
	}
}

func TestGRPCDuplicateWriteAlreadyExists(t *testing.T) {
	client := startTestServer(t)

	writeDoc(t, client, "tx-dup-grpc", "a.xml", "text/xml", []byte("data"))

	ctx := context.Background()
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	_ = stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-dup-grpc",
				DocumentName:  "a.xml",
				ContentType:   "text/xml",
			},
		},
	})
	_ = stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{
			ChunkData: []byte("data"),
		},
	})
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("expected error on duplicate")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.AlreadyExists {
		t.Fatalf("expected ALREADY_EXISTS, got %v", err)
	}
}
