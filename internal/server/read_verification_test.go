package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestGRPCReadDocumentStreamsVerifiedMetadataThenChunks(t *testing.T) {
	ctx := context.Background()
	client, _ := startReadVerificationShardServer(t)
	const contentType = "application/pdf"
	payload := bytes.Repeat([]byte("verified read success "), 16)
	writeResp := writeDocument(ctx, t, client, "tx-read-success", "doc.pdf", contentType, payload)

	head, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-read-success",
		DocumentName:  "doc.pdf",
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	assertHeadMatchesWrite(t, head, "doc.pdf", contentType, int64(len(payload)), writeResp)

	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-read-success",
		DocumentName:  "doc.pdf",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv metadata: %v", err)
	}
	meta := first.GetMeta()
	if meta == nil {
		t.Fatalf("first ReadDocument response = %T, want metadata", first.GetPart())
	}
	assertReadMetaMatchesWrite(t, meta, contentType, int64(len(payload)), writeResp)

	got := recvReadChunks(t, stream)
	if !bytes.Equal(got, payload) {
		t.Fatalf("ReadDocument payload = %q, want %q", got, payload)
	}
}

func TestGRPCReadDocumentCorruptBlockReturnsDataLossBeforeAnyMessage(t *testing.T) {
	ctx := context.Background()
	client, s := startReadVerificationShardServer(t)
	payload := bytes.Repeat([]byte("verified read payload "), 16)
	writeDocument(ctx, t, client, "tx-read-corrupt", "doc.xml", "text/xml", payload)

	corruptRegisteredShardBlock(t, s, 1)

	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-read-corrupt",
		DocumentName:  "doc.xml",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	msg, err := stream.Recv()
	if err == nil {
		t.Fatalf("first Recv returned response before integrity failure: %+v", msg)
	}
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("first Recv status = %s, want %s (err=%v)", status.Code(err), codes.DataLoss, err)
	}
}

func TestGRPCQuarantinedReadDeniedAndMetadataExposesScanStatus(t *testing.T) {
	ctx := context.Background()
	client, s := startReadVerificationShardServer(t)
	payload := bytes.Repeat([]byte("quarantined read payload "), 8)
	writeDocument(ctx, t, client, "tx-read-quarantined", "unsafe.xml", "text/xml", payload)
	writeDocument(ctx, t, client, "tx-read-quarantined", "safe.xml", "text/xml", []byte("safe payload"))

	reportReadVerificationDetection(ctx, t, s, "tx-read-quarantined", "unsafe.xml")

	head, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-read-quarantined",
		DocumentName:  "unsafe.xml",
	})
	if err != nil {
		t.Fatalf("HeadDocument quarantined: %v", err)
	}
	assertGRPCScanStatus(t, "HeadDocument", head.GetScanStatus(), scrapv1.ScanStatus_SCAN_STATUS_QUARANTINED)

	find, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-read-quarantined",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	statuses := grpcScanStatusesByDocument(find.GetDocuments())
	assertGRPCScanStatus(t, "FindDocuments unsafe", statuses["unsafe.xml"], scrapv1.ScanStatus_SCAN_STATUS_QUARANTINED)
	assertGRPCScanStatus(t, "FindDocuments safe", statuses["safe.xml"], scrapv1.ScanStatus_SCAN_STATUS_UNSCANNED)

	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-read-quarantined",
		DocumentName:  "unsafe.xml",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	msg, err := stream.Recv()
	if err == nil {
		t.Fatalf("ReadDocument returned response before quarantine denial: %+v", msg)
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReadDocument status = %s, want %s (err=%v)", status.Code(err), codes.FailedPrecondition, err)
	}
	assertGRPCErrorInfoReason(t, err, storeapi.PreconditionReasonContentQuarantined)
}

func reportReadVerificationDetection(ctx context.Context, t *testing.T, s *shard.Shard, txID, docName string) {
	t.Helper()

	err := s.ReportDetections(ctx, avscan.Block{BlockID: 1}, []avscan.Detection{{
		TransactionID: txID,
		DocumentName:  docName,
		DetectedAtUs:  1716700001000000,
		ScanType:      avscan.DetectionScanTypeInitial,
		Reason:        avscan.DetectionReasonScannerDetection,
	}})
	if err != nil {
		t.Fatalf("ReportDetections: %v", err)
	}
}

func grpcScanStatusesByDocument(docs []*scrapv1.DocumentMeta) map[string]scrapv1.ScanStatus {
	statuses := make(map[string]scrapv1.ScanStatus, len(docs))
	for _, doc := range docs {
		statuses[doc.GetName()] = doc.GetScanStatus()
	}
	return statuses
}

func assertGRPCScanStatus(t *testing.T, label string, got, want scrapv1.ScanStatus) {
	t.Helper()

	if got != want {
		t.Fatalf("%s scan_status = %v, want %v", label, got, want)
	}
}

func assertGRPCErrorInfoReason(t *testing.T, err error, want string) {
	t.Helper()

	st := status.Convert(err)
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != want {
		t.Fatalf("reason = %q, want %q", info.GetReason(), want)
	}
}

func assertHeadMatchesWrite(
	t *testing.T,
	head *scrapv1.HeadDocumentResponse,
	docName string,
	contentType string,
	size int64,
	writeResp *scrapv1.WriteDocumentResponse,
) {
	t.Helper()

	if head.GetName() != docName {
		t.Fatalf("HeadDocument Name = %q, want %q", head.GetName(), docName)
	}
	if head.GetContentType() != contentType {
		t.Fatalf("HeadDocument ContentType = %q, want %q", head.GetContentType(), contentType)
	}
	if head.GetSize() != size {
		t.Fatalf("HeadDocument Size = %d, want %d", head.GetSize(), size)
	}
	if head.GetSha256Checksum() != writeResp.GetSha256Checksum() {
		t.Fatalf("HeadDocument SHA = %q, want %q", head.GetSha256Checksum(), writeResp.GetSha256Checksum())
	}
	if !head.GetCreatedAt().AsTime().Equal(writeResp.GetCreatedAt().AsTime()) {
		t.Fatalf("HeadDocument CreatedAt = %v, want %v", head.GetCreatedAt().AsTime(), writeResp.GetCreatedAt().AsTime())
	}
}

func assertReadMetaMatchesWrite(
	t *testing.T,
	meta *scrapv1.ReadDocumentMeta,
	contentType string,
	size int64,
	writeResp *scrapv1.WriteDocumentResponse,
) {
	t.Helper()

	if meta.GetContentType() != contentType {
		t.Fatalf("ReadDocument ContentType = %q, want %q", meta.GetContentType(), contentType)
	}
	if meta.GetSize() != size {
		t.Fatalf("ReadDocument Size = %d, want %d", meta.GetSize(), size)
	}
	if meta.GetSha256Checksum() != writeResp.GetSha256Checksum() {
		t.Fatalf("ReadDocument SHA = %q, want %q", meta.GetSha256Checksum(), writeResp.GetSha256Checksum())
	}
	if !meta.GetCreatedAt().AsTime().Equal(writeResp.GetCreatedAt().AsTime()) {
		t.Fatalf("ReadDocument CreatedAt = %v, want %v", meta.GetCreatedAt().AsTime(), writeResp.GetCreatedAt().AsTime())
	}
}

func recvReadChunks(t *testing.T, stream scrapv1.DocumentService_ReadDocumentClient) []byte {
	t.Helper()

	var got []byte
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("Recv chunk: %v", err)
		}
		if msg.GetMeta() != nil {
			t.Fatalf("unexpected metadata response after first message: %+v", msg.GetMeta())
		}
		got = append(got, msg.GetChunkData()...)
	}
}

func startReadVerificationShardServer(t *testing.T) (scrapv1.DocumentServiceClient, *shard.Shard) {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:      t.TempDir(),
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	waitForReadVerificationShardLeader(t, s)

	return startServerWith(t, s), s
}

func waitForReadVerificationShardLeader(t *testing.T, s *shard.Shard) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
}

func corruptRegisteredShardBlock(t *testing.T, s *shard.Shard, blockID uint64) {
	t.Helper()

	blkPath := block.FilePath(filepath.Join(s.DataDirForTest(), "blocks"), blockID)
	data, err := os.ReadFile(blkPath) //nolint:gosec // path is from test Shard temp dir.
	if err != nil {
		t.Fatalf("ReadFile block: %v", err)
	}
	if len(data) <= block.HeaderSize+block.FrameHeaderSize {
		t.Fatalf("Block length = %d, want payload after first Frame header", len(data))
	}
	data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // path is from test Shard temp dir.
		t.Fatalf("WriteFile corrupt block: %v", err)
	}
}
