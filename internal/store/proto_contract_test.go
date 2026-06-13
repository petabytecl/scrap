package store_test

import (
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestProtoWriteDocumentOneofInit(t *testing.T) {
	req := &scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId:  "tx-001",
				DocumentName:   "invoice.xml",
				ContentType:    "application/xml",
				IdempotencyKey: "idem-001",
				TenantId:       "tenant-001",
			},
		},
	}
	init := req.GetInit()
	if init == nil {
		t.Fatal("Init should not be nil")
	}
	if init.GetTransactionId() != "tx-001" {
		t.Fatalf("TransactionId: got %q", init.GetTransactionId())
	}
}

func TestProtoWriteDocumentOneofChunk(t *testing.T) {
	req := &scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{
			ChunkData: []byte("test data"),
		},
	}
	if req.GetInit() != nil {
		t.Fatal("Init should be nil for chunk message")
	}
	if len(req.GetChunkData()) == 0 {
		t.Fatal("ChunkData should not be empty")
	}
}

func TestProtoReadDocumentOneofMeta(t *testing.T) {
	resp := &scrapv1.ReadDocumentResponse{
		Part: &scrapv1.ReadDocumentResponse_Meta{
			Meta: &scrapv1.ReadDocumentMeta{
				ContentType:    "application/xml",
				Size:           2048,
				Sha256Checksum: "aabbccdd",
			},
		},
	}
	meta := resp.GetMeta()
	if meta == nil {
		t.Fatal("Meta should not be nil")
	}
	if meta.GetContentType() != "application/xml" {
		t.Fatalf("ContentType: got %q", meta.GetContentType())
	}
}

func TestProtoReadDocumentOneofChunk(t *testing.T) {
	resp := &scrapv1.ReadDocumentResponse{
		Part: &scrapv1.ReadDocumentResponse_ChunkData{
			ChunkData: []byte("bytes"),
		},
	}
	if resp.GetMeta() != nil {
		t.Fatal("Meta should be nil for chunk message")
	}
	if len(resp.GetChunkData()) == 0 {
		t.Fatal("ChunkData should not be empty")
	}
}

func TestProtoFindDocumentsResponseFields(t *testing.T) {
	meta := &scrapv1.DocumentMeta{
		Name:           "invoice.xml",
		ContentType:    "application/xml",
		Size:           1024,
		Sha256Checksum: "abc",
	}
	resp := &scrapv1.FindDocumentsResponse{
		Documents: []*scrapv1.DocumentMeta{meta},
	}
	if len(resp.GetDocuments()) != 1 {
		t.Fatal("should have 1 document")
	}
	if resp.GetDocuments()[0].GetName() == "" {
		t.Fatal("document Name should be set")
	}
}
