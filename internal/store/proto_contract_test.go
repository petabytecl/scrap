package store_test

import (
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestProtoWriteDocumentRequestFields(t *testing.T) {
	req := &scrapv1.WriteDocumentRequest{
		TransactionId:  "tx-001",
		DocumentName:   "invoice.xml",
		ContentType:    "application/xml",
		IdempotencyKey: "idem-001",
		ChunkData:      []byte("test"),
	}
	if req.GetTransactionId() == "" {
		t.Fatal("TransactionId should be set")
	}
	if req.GetDocumentName() == "" {
		t.Fatal("DocumentName should be set")
	}
	if req.GetContentType() == "" {
		t.Fatal("ContentType should be set")
	}
	if req.GetIdempotencyKey() == "" {
		t.Fatal("IdempotencyKey should be set")
	}
}

func TestProtoWriteDocumentResponseFields(t *testing.T) {
	resp := &scrapv1.WriteDocumentResponse{
		Sha256Checksum: "abc123",
		Size:           1024,
	}
	if resp.GetSha256Checksum() == "" {
		t.Fatal("Sha256Checksum should be set")
	}
	if resp.GetSize() != 1024 {
		t.Fatal("Size should be 1024")
	}
}

func TestProtoHeadDocumentResponseFields(t *testing.T) {
	resp := &scrapv1.HeadDocumentResponse{
		ContentType:    "application/xml",
		Size:           2048,
		Sha256Checksum: "def456",
	}
	if resp.GetContentType() == "" {
		t.Fatal("ContentType should be set")
	}
	if resp.GetSize() != 2048 {
		t.Fatal("Size should be 2048")
	}
	if resp.GetSha256Checksum() == "" {
		t.Fatal("Sha256Checksum should be set")
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
