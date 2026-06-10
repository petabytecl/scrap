package store_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestReplicateDocumentRequestOneofInit(t *testing.T) {
	req := &scrapv1.ReplicateDocumentRequest{
		Part: &scrapv1.ReplicateDocumentRequest_Init{
			Init: &scrapv1.ReplicateDocumentInit{
				TransactionId: "tx-rep-001",
				DocumentName:  "invoice.xml",
				ContentType:   "application/xml",
				BlockId:       42,
				StartOffset:   40,
				FrameCount:    3,
				TotalBytes:    16384,
				Sha256:        make([]byte, 32),
				EncryptionEnvelope: []byte(
					`{"version":1,"wrapped_data_key":"vault:v1:test"}`,
				),
				ShardId: 7,
			},
		},
	}

	init := req.GetInit()
	if init == nil {
		t.Fatal("Init should not be nil")
	}
	if init.GetTransactionId() != "tx-rep-001" {
		t.Fatalf("TransactionId: got %q", init.GetTransactionId())
	}
	if init.GetBlockId() != 42 {
		t.Fatalf("BlockId: got %d", init.GetBlockId())
	}
	if init.GetShardId() != 7 {
		t.Fatalf("ShardId: got %d", init.GetShardId())
	}
	if string(init.GetEncryptionEnvelope()) != `{"version":1,"wrapped_data_key":"vault:v1:test"}` {
		t.Fatalf("EncryptionEnvelope: got %q", init.GetEncryptionEnvelope())
	}
}

func TestReplicateDocumentRequestOneofChunk(t *testing.T) {
	req := &scrapv1.ReplicateDocumentRequest{
		Part: &scrapv1.ReplicateDocumentRequest_ChunkData{
			ChunkData: []byte("frame payload"),
		},
	}
	if req.GetInit() != nil {
		t.Fatal("Init should be nil for chunk message")
	}
	if len(req.GetChunkData()) == 0 {
		t.Fatal("ChunkData should not be empty")
	}
}

func TestReplicateDocumentResponseSHA256(t *testing.T) {
	sha := make([]byte, 32)
	sha[0] = 0xAB
	resp := &scrapv1.ReplicateDocumentResponse{Sha256: sha}

	data, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &scrapv1.ReplicateDocumentResponse{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.GetSha256()) != 32 {
		t.Fatalf("SHA256 should be 32 bytes, got %d", len(decoded.GetSha256()))
	}
	if decoded.GetSha256()[0] != 0xAB {
		t.Fatalf("SHA256[0]: got %x", decoded.GetSha256()[0])
	}
}

func TestTransferBlockRequestFields(t *testing.T) {
	req := &scrapv1.TransferBlockRequest{
		ShardId: 7,
		BlockId: 42,
	}

	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &scrapv1.TransferBlockRequest{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.GetShardId() != 7 {
		t.Fatalf("ShardId: got %d", decoded.GetShardId())
	}
	if decoded.GetBlockId() != 42 {
		t.Fatalf("BlockId: got %d", decoded.GetBlockId())
	}
}

func TestTransferBlockResponseOneofMeta(t *testing.T) {
	resp := &scrapv1.TransferBlockResponse{
		Part: &scrapv1.TransferBlockResponse_Meta{
			Meta: &scrapv1.TransferBlockMeta{
				BlockId:   42,
				BlockSize: 67108864,
				IdxSize:   32768,
			},
		},
	}

	meta := resp.GetMeta()
	if meta == nil {
		t.Fatal("Meta should not be nil")
	}
	if meta.GetBlockId() != 42 {
		t.Fatalf("BlockId: got %d", meta.GetBlockId())
	}
	if meta.GetBlockSize() != 67108864 {
		t.Fatalf("BlockSize: got %d", meta.GetBlockSize())
	}
}

func TestTransferBlockResponseOneofChunk(t *testing.T) {
	resp := &scrapv1.TransferBlockResponse{
		Part: &scrapv1.TransferBlockResponse_ChunkData{
			ChunkData: []byte("block bytes"),
		},
	}
	if resp.GetMeta() != nil {
		t.Fatal("Meta should be nil for chunk message")
	}
	if len(resp.GetChunkData()) == 0 {
		t.Fatal("ChunkData should not be empty")
	}
}

func TestLeaderHintRoundTrip(t *testing.T) {
	hint := &scrapv1.LeaderHint{
		LeaderAddr: "scrapd-2.scrap-headless.ns.svc:9090",
	}

	data, err := proto.Marshal(hint)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &scrapv1.LeaderHint{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.GetLeaderAddr() != "scrapd-2.scrap-headless.ns.svc:9090" {
		t.Fatalf("LeaderAddr: got %q", decoded.GetLeaderAddr())
	}
}
