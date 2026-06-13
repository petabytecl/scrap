package server_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestDocumentServiceMapsRebuildingStoreErrorsToUnavailable(t *testing.T) {
	client := startServerWith(t, rebuildingStore{})

	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "WriteDocument",
			call: func(ctx context.Context) error {
				return callWriteDocument(ctx, client, "tx-rebuilding", "doc.xml", []byte("payload"))
			},
		},
		{
			name: "HeadDocument",
			call: func(ctx context.Context) error {
				_, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
					TransactionId: "tx-rebuilding",
					DocumentName:  "doc.xml",
				})
				if err != nil {
					return fmt.Errorf("HeadDocument: %w", err)
				}
				return nil
			},
		},
		{
			name: "ReadDocument",
			call: func(ctx context.Context) error {
				stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{
					TransactionId: "tx-rebuilding",
					DocumentName:  "doc.xml",
				})
				if err != nil {
					return fmt.Errorf("ReadDocument: %w", err)
				}
				_, err = stream.Recv()
				if err != nil {
					return fmt.Errorf("recv ReadDocument: %w", err)
				}
				return nil
			},
		},
		{
			name: "FindDocuments",
			call: func(ctx context.Context) error {
				_, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
					TransactionId: "tx-rebuilding",
				})
				if err != nil {
					return fmt.Errorf("FindDocuments: %w", err)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRebuildingUnavailable(t, tt.call(context.Background()))
		})
	}
}

func TestGRPCShardRestartReplaysCommittedDocuments(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	firstShard := openRestartEvidenceShard(t, dataDir)
	firstShardClosed := false
	defer func() {
		if !firstShardClosed {
			_ = firstShard.Close()
		}
	}()
	firstServer := startRestartEvidenceServer(t, firstShard)
	defer firstServer.stop()
	payload := []byte("restart evidence payload")
	writeResp := writeDocument(ctx, t, firstServer.client, "tx-grpc-restart", "doc.xml", "text/xml", payload)
	assertGRPCRestartEvidence(ctx, t, firstServer.client, writeResp, payload)

	firstServer.stop()
	if err := firstShard.Close(); err != nil {
		t.Fatalf("Close first shard: %v", err)
	}
	firstShardClosed = true

	reopenedShard := openRestartEvidenceShard(t, dataDir)
	defer func() { _ = reopenedShard.Close() }()
	reopenedServer := startRestartEvidenceServer(t, reopenedShard)
	defer reopenedServer.stop()

	assertGRPCRestartEvidence(ctx, t, reopenedServer.client, writeResp, payload)
	replayResp := writeDocument(ctx, t, reopenedServer.client, "tx-grpc-restart", "doc.xml", "text/xml", payload)
	if replayResp.GetSha256Checksum() != writeResp.GetSha256Checksum() {
		t.Fatalf("exact replay checksum = %q, want %q", replayResp.GetSha256Checksum(), writeResp.GetSha256Checksum())
	}
	if !replayResp.GetCreatedAt().AsTime().Equal(writeResp.GetCreatedAt().AsTime()) {
		t.Fatalf("exact replay CreatedAt = %s, want %s", replayResp.GetCreatedAt().AsTime(), writeResp.GetCreatedAt().AsTime())
	}
}

type rebuildingStore struct{}

func (rebuildingStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, rebuildingError()
}

func (rebuildingStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, rebuildingError()
}

func (rebuildingStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, rebuildingError()
}

func (rebuildingStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, rebuildingError()
}

func rebuildingError() error {
	return fmt.Errorf("%w: shard unavailable", storeapi.ErrRebuilding)
}

func assertRebuildingUnavailable(t *testing.T, err error) {
	t.Helper()

	assertStatusCode(t, err, codes.Unavailable)
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if !strings.Contains(st.Message(), "projection rebuild in progress") {
		t.Fatalf("message = %q, want stable rebuild unavailable message", st.Message())
	}
	if strings.Contains(st.Message(), "shard unavailable") {
		t.Fatalf("message = %q, should not expose wrapped shard detail", st.Message())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.UnavailableReasonProjectionRebuild {
		t.Fatalf("reason = %q, want %q", info.GetReason(), storeapi.UnavailableReasonProjectionRebuild)
	}
}

func callWriteDocument(ctx context.Context, client scrapv1.DocumentServiceClient, txID, docName string, payload []byte) error {
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return fmt.Errorf("WriteDocument: %w", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId: txID,
				DocumentName:  docName,
				ContentType:   "text/xml",
			},
		},
	}); err != nil {
		return fmt.Errorf("send init: %w", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: payload},
	}); err != nil {
		return fmt.Errorf("send chunk: %w", err)
	}
	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("CloseAndRecv: %w", err)
	}
	return nil
}

func assertGRPCRestartEvidence(
	ctx context.Context,
	t *testing.T,
	client scrapv1.DocumentServiceClient,
	writeResp *scrapv1.WriteDocumentResponse,
	payload []byte,
) {
	t.Helper()

	head, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-grpc-restart",
		DocumentName:  "doc.xml",
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	assertHeadMatchesWrite(t, head, "doc.xml", "text/xml", int64(len(payload)), writeResp)

	got := readDocument(ctx, t, client, "tx-grpc-restart", "doc.xml")
	if string(got) != string(payload) {
		t.Fatalf("ReadDocument payload = %q, want %q", got, payload)
	}

	find, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{TransactionId: "tx-grpc-restart"})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(find.GetDocuments()) != 1 {
		t.Fatalf("FindDocuments count = %d, want 1", len(find.GetDocuments()))
	}
	assertGRPCDocumentMetaMatchesWrite(t, find.GetDocuments()[0], "doc.xml", "text/xml", int64(len(payload)), writeResp)
}

type restartEvidenceServer struct {
	client scrapv1.DocumentServiceClient
	stop   func()
}

func startRestartEvidenceServer(t *testing.T, store storeapi.Store) restartEvidenceServer {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful.
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, store)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		gs.GracefulStop()
		t.Fatalf("Dial: %v", err)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = conn.Close()
			gs.GracefulStop()
		})
	}

	return restartEvidenceServer{
		client: scrapv1.NewDocumentServiceClient(conn),
		stop:   stop,
	}
}

func openRestartEvidenceShard(t *testing.T, dataDir string) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:      dataDir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	waitForReadVerificationShardLeader(t, s)
	return s
}
