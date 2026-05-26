package store_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestRaftCommandOneofCommitDocument(t *testing.T) {
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId:  "tx-001",
				DocumentName:   "invoice.xml",
				ContentType:    "application/xml",
				IdempotencyKey: "idem-001",
				BlockId:        42,
				FirstFrameOff:  40,
				FrameCount:     3,
				TotalBytes:     16384,
				Sha256:         make([]byte, 32),
				CreatedAtUs:    1716700000000000,
			},
		},
	}

	doc := cmd.GetCommitDoc()
	if doc == nil {
		t.Fatal("CommitDoc should not be nil")
	}
	if doc.GetTransactionId() != "tx-001" {
		t.Fatalf("TransactionId: got %q", doc.GetTransactionId())
	}
	if doc.GetDocumentName() != "invoice.xml" {
		t.Fatalf("DocumentName: got %q", doc.GetDocumentName())
	}
	if doc.GetBlockId() != 42 {
		t.Fatalf("BlockId: got %d", doc.GetBlockId())
	}
	if doc.GetFirstFrameOff() != 40 {
		t.Fatalf("FirstFrameOff: got %d", doc.GetFirstFrameOff())
	}
	if doc.GetFrameCount() != 3 {
		t.Fatalf("FrameCount: got %d", doc.GetFrameCount())
	}
	if doc.GetTotalBytes() != 16384 {
		t.Fatalf("TotalBytes: got %d", doc.GetTotalBytes())
	}
	if len(doc.GetSha256()) != 32 {
		t.Fatalf("SHA256 should be 32 raw bytes, got %d", len(doc.GetSha256()))
	}
	if doc.GetCreatedAtUs() != 1716700000000000 {
		t.Fatalf("CreatedAtUs: got %d", doc.GetCreatedAtUs())
	}
}

func TestRaftCommandOneofNilArms(t *testing.T) {
	cmd := &scrapv1.RaftCommand{}
	if cmd.GetCommitDoc() != nil {
		t.Fatal("empty RaftCommand should have nil CommitDoc")
	}
}

func TestRaftCommandRoundTrip(t *testing.T) {
	original := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId: "tx-rt",
				DocumentName:  "doc.pdf",
				BlockId:       7,
				TotalBytes:    999,
				Sha256:        []byte("0123456789abcdef0123456789abcdef"),
			},
		},
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &scrapv1.RaftCommand{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	doc := decoded.GetCommitDoc()
	if doc == nil {
		t.Fatal("decoded CommitDoc should not be nil")
	}
	if doc.GetTransactionId() != "tx-rt" {
		t.Fatalf("TransactionId: got %q", doc.GetTransactionId())
	}
	if doc.GetBlockId() != 7 {
		t.Fatalf("BlockId: got %d", doc.GetBlockId())
	}
}

func TestOpenlogEntryRoundTrip(t *testing.T) {
	original := &scrapv1.OpenlogEntry{
		TransactionId:  "tx-prep",
		DocumentName:   "receipt.xml",
		BlockId:        5,
		StartOffset:    1024,
		ContentType:    "text/xml",
		IdempotencyKey: "idem-prep",
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &scrapv1.OpenlogEntry{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.GetTransactionId() != "tx-prep" {
		t.Fatalf("TransactionId: got %q", decoded.GetTransactionId())
	}
	if decoded.GetBlockId() != 5 {
		t.Fatalf("BlockId: got %d", decoded.GetBlockId())
	}
	if decoded.GetStartOffset() != 1024 {
		t.Fatalf("StartOffset: got %d", decoded.GetStartOffset())
	}
}

func TestCommitDocumentSize(t *testing.T) {
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId:  "tx-00000000-0000-0000-0000-000000000001",
				DocumentName:   "billing/2026/05/invoice-00001.xml",
				ContentType:    "application/xml",
				IdempotencyKey: "idem-00000000-0000-0000-0000-000000000001",
				BlockId:        999999,
				FirstFrameOff:  67108864,
				FrameCount:     256,
				TotalBytes:     1048576,
				Sha256:         make([]byte, 32),
				CreatedAtUs:    1716700000000000,
			},
		},
	}

	data, err := proto.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if len(data) > 500 {
		t.Fatalf("RaftCommand too large for log entry: %d bytes (target ~330)", len(data))
	}
}
