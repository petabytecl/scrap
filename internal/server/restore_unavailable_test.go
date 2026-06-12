package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type restoreUnavailableStore struct {
	reason  string
	message string
}

func (restoreUnavailableStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, storeapi.ErrInvalidArgument
}

func (restoreUnavailableStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s restoreUnavailableStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	reason := s.reason
	if reason == "" {
		reason = storeapi.UnavailableReasonBackendRestoreUnavailable
	}
	message := s.message
	if message == "" {
		message = "Backend restore unavailable"
	}
	return nil, storeapi.DocumentMeta{}, storeapi.NewUnavailable(reason, message)
}

func (restoreUnavailableStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

type restoreDataLossStore struct {
	reason  string
	message string
}

func (restoreDataLossStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, storeapi.ErrInvalidArgument
}

func (restoreDataLossStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s restoreDataLossStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, storeapi.NewDataLoss(s.reason, s.message)
}

func (restoreDataLossStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

func TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail(t *testing.T) {
	client := startRestoreServer(t, restoreUnavailableStore{})
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected restore unavailable error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("code = %s, want UNAVAILABLE", st.Code())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("reason = %q, want backend_restore_unavailable", info.GetReason())
	}
}

func TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail(t *testing.T) {
	client := startRestoreServer(t, restoreUnavailableStore{
		reason:  storeapi.UnavailableReasonCryptoUnavailable,
		message: "leaky plaintext fake-transit:v1 /tmp/internal tx-restore doc.bin",
	})
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected crypto unavailable error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("code = %s, want UNAVAILABLE", st.Code())
	}
	if st.Message() != storeapi.ErrUnavailable.Error() {
		t.Fatalf("message = %q, want sanitized unavailable message", st.Message())
	}
	if forbidden := []string{"plaintext", "fake-transit", "/tmp", "tx-restore", "doc.bin"}; statusMessageContainsAny(st.Message(), forbidden) {
		t.Fatalf("message = %q, contains crypto internals", st.Message())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.UnavailableReasonCryptoUnavailable {
		t.Fatalf("reason = %q, want crypto_unavailable", info.GetReason())
	}
}

func TestReadDocumentUnavailableWithUnknownReasonOmitsErrorInfoDetail(t *testing.T) {
	client := startRestoreServer(t, restoreUnavailableStore{
		reason:  "backend_key_/tmp/validation-token",
		message: "leaky Backend key /tmp/internal validation-token",
	})
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected unavailable error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("code = %s, want UNAVAILABLE", st.Code())
	}
	if st.Message() != storeapi.ErrUnavailable.Error() {
		t.Fatalf("message = %q, want sanitized unavailable message", st.Message())
	}
	if forbidden := []string{"Backend key", "validation-token", "/tmp", "tx-restore", "doc.bin"}; statusMessageContainsAny(st.Message(), forbidden) {
		t.Fatalf("message = %q, contains unavailable internals", st.Message())
	}
	if info := errorInfoDetail(st); info != nil {
		t.Fatalf("unexpected ErrorInfo detail for unknown reason: %+v", info)
	}
}

func TestReadDocumentRestoreDataLossReturnsErrorInfoDetail(t *testing.T) {
	client := startRestoreServer(t, restoreDataLossStore{
		reason:  storeapi.DataLossReasonBackendRestoreCorrupt,
		message: "Backend restore corrupt with /tmp/internal detail",
	})
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected restore data-loss error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.DataLoss {
		t.Fatalf("code = %s, want DATA_LOSS", st.Code())
	}
	if st.Message() != storeapi.ErrDataLoss.Error() {
		t.Fatalf("message = %q, want sanitized data-loss message", st.Message())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.DataLossReasonBackendRestoreCorrupt {
		t.Fatalf("reason = %q, want backend_restore_corrupt", info.GetReason())
	}
}

func TestReadDocumentDataLossWithUnknownReasonOmitsErrorInfoDetail(t *testing.T) {
	client := startRestoreServer(t, restoreDataLossStore{
		reason:  "backend_key_/tmp/validation-token",
		message: "leaky Backend key /tmp/internal validation-token",
	})
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected restore data-loss error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.DataLoss {
		t.Fatalf("code = %s, want DATA_LOSS", st.Code())
	}
	if st.Message() != storeapi.ErrDataLoss.Error() {
		t.Fatalf("message = %q, want sanitized data-loss message", st.Message())
	}
	if info := errorInfoDetail(st); info != nil {
		t.Fatalf("unexpected ErrorInfo detail for unknown reason: %+v", info)
	}
}

func TestReadDocumentRestoreMissingBackendObjectReturnsSanitizedDataLossDetail(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	client, s := startRestoreShardServer(t, backendStore)
	confirmed := stageEvictedRestoreBlockThroughGRPC(ctx, t, client, s)
	if err := backendStore.DeleteObject(ctx, confirmed.BlockObject.Key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc-1.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected missing Backend object data-loss error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.DataLoss {
		t.Fatalf("code = %s, want DATA_LOSS", st.Code())
	}
	if st.Message() != storeapi.ErrDataLoss.Error() {
		t.Fatalf("message = %q, want sanitized data-loss message", st.Message())
	}
	if forbidden := []string{confirmed.BlockObject.Key, "Backend restore", "Block ", "validation", "/tmp"}; statusMessageContainsAny(st.Message(), forbidden) {
		t.Fatalf("message = %q, contains restore internals", st.Message())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.DataLossReasonBackendRestoreMissing {
		t.Fatalf("reason = %q, want backend_restore_missing", info.GetReason())
	}
}

func startRestoreServer(t *testing.T, store storeapi.Store) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, store)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func startRestoreShardServer(t *testing.T, backendStore backend.Backend) (scrapv1.DocumentServiceClient, *shard.Shard) {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:        t.TempDir(),
		ShardID:        0,
		RaftID:         1,
		Peers:          map[uint64]string{1: "localhost:9091"},
		BlockSealSize:  41,
		TickInterval:   10 * time.Millisecond,
		BootstrapGrace: time.Second,
		Upload: shard.UploadConfig{
			Enabled:               true,
			Backend:               backendStore,
			CellID:                "cell-test",
			Concurrency:           1,
			RestoreRetryBaseDelay: time.Nanosecond,
		},
	})
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	waitForReadVerificationShardLeader(t, s)

	return startServerWith(t, s), s
}

func stageEvictedRestoreBlockThroughGRPC(
	ctx context.Context,
	t *testing.T,
	client scrapv1.DocumentServiceClient,
	s *shard.Shard,
) index.ConfirmedUpload {
	t.Helper()

	content := bytes.Repeat([]byte("restore through grpc "), 4)
	writeDocument(ctx, t, client, "tx-restore", "doc-1.bin", "application/octet-stream", content)
	writeDocument(ctx, t, client, "tx-restore-next", "doc-2.bin", "application/octet-stream", []byte("seal previous"))

	confirmed := waitRestoreServerConfirmedUpload(t, s, 1)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, shard.EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, confirmed.BlockID)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}
	return confirmed
}

func waitRestoreServerConfirmedUpload(t *testing.T, s *shard.Shard, blockID uint64) index.ConfirmedUpload {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		confirmed, err := s.ConfirmedUploadForTest(blockID)
		if err == nil {
			return confirmed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for confirmed upload block %d", blockID)
	return index.ConfirmedUpload{}
}

func statusMessageContainsAny(message string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(message, needle) {
			return true
		}
	}
	return false
}
