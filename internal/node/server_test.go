package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/config"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestServerServesPublicAndAdminAPIs(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialAuthorizedTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	_, err := publicClient.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	requireCode(t, err, codes.Unimplemented)

	adminConn := dialAuthorizedTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	adminClient := adminv1.NewRestoreServiceClient(adminConn)
	_, err = adminClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abc",
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	requireCode(t, err, codes.Unimplemented)

	_ = publicConn.Close()
	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerServesHealthChecksWithoutWorkloadIdentity(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Health: fakeHealthApplication{},
	}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	adminConn := dialTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	healthClient := healthv1.NewHealthClient(adminConn)
	for _, service := range []string{"", adminReadinessHealthService} {
		resp, err := healthClient.Check(context.Background(), &healthv1.HealthCheckRequest{Service: service})
		testutil.RequireNoErrorf(t, err, "health check %q", service)
		testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_SERVING, "health status for %q", service)
	}

	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerRejectsOversizedReceivedMessages(t *testing.T) {
	cfg := config.Default()
	cfg.GRPCServerLimits.MaxRecvMsgSizeBytes = 1024
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{
		Documents: readAllDocuments{},
	}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialAuthorizedTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	client := scrapv1.NewDocumentServiceClient(publicConn)
	write, err := client.WriteDocument(context.Background())
	testutil.RequireNoErrorf(t, err, "WriteDocument")
	testutil.RequireNoErrorf(t, write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: validWriteInit("oversized.xml")},
	}), "send init")

	err = write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: bytes.Repeat([]byte("x"), 2*1024)}},
	})
	if err == nil {
		_, err = write.CloseAndRecv()
	}
	requireCode(t, err, codes.ResourceExhausted)

	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerRejectsOversizedAdminMessages(t *testing.T) {
	cfg := config.Default()
	cfg.GRPCServerLimits.MaxRecvMsgSizeBytes = 1024
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	adminConn := dialAuthorizedTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	client := adminv1.NewRestoreServiceClient(adminConn)
	_, err := client.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abc",
		OperationPlanId: strings.Repeat("x", 2*1024),
		PlanHash:        "hash-1",
	})
	requireCode(t, err, codes.ResourceExhausted)

	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerRejectsOversizedSentMessages(t *testing.T) {
	cfg := config.Default()
	cfg.GRPCServerLimits.MaxSendMsgSizeBytes = 1024
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{
		Documents: largeReadDocuments{data: bytes.Repeat([]byte("x"), 2*1024)},
	}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialAuthorizedTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	client := scrapv1.NewDocumentServiceClient(publicConn)
	read, err := client.ReadDocument(context.Background(), validReadRequest("large-read.xml"))
	testutil.RequireNoErrorf(t, err, "open read stream")
	_, err = read.Recv()
	requireCode(t, err, codes.ResourceExhausted)

	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerConcurrentStreamLimitAppliesToPublicServer(t *testing.T) {
	cfg := config.Default()
	cfg.GRPCServerLimits.MaxConcurrentStreams = 1
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	documents := newBlockingReadDocuments()
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{
		Documents: documents,
	}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialAuthorizedTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	client := scrapv1.NewDocumentServiceClient(publicConn)
	first, err := client.ReadDocument(context.Background(), validReadRequest("first.xml"))
	testutil.RequireNoErrorf(t, err, "open first read stream")
	<-documents.started

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer secondCancel()
	_, err = client.ReadDocument(secondCtx, validReadRequest("second.xml"))
	requireStreamLimitRejection(t, err)
	testutil.RequireEqualf(t, documents.callCount(), 1, "read handler call count")

	close(documents.release)
	_, err = first.Recv()
	testutil.RequireErrorIsf(t, err, io.EOF, "first stream completion")
	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerRejectsInvalidGRPCLimitConfig(t *testing.T) {
	cfg := config.Default()
	cfg.GRPCServerLimits.MaxConcurrentStreams = 1 << 32
	publicListener := bufconn.Listen(1024 * 1024)
	defer func() { testutil.RequireNoErrorf(t, publicListener.Close(), "close public listener") }()
	adminListener := bufconn.Listen(1024 * 1024)
	defer func() { testutil.RequireNoErrorf(t, adminListener.Close(), "close admin listener") }()

	_, err := newServerWithConfig(cfg, publicListener, adminListener, Applications{}, testAuthorizationManager(t), "")

	if err == nil {
		t.Fatal("invalid grpc max concurrent streams error = nil, want error")
	}
}

func TestServerHealthReadinessReflectsReadFreshFailure(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Health: fakeHealthApplication{readinessErr: errors.New("read index unavailable")},
	}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	adminConn := dialTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	healthClient := healthv1.NewHealthClient(adminConn)
	resp, err := healthClient.Check(context.Background(), &healthv1.HealthCheckRequest{Service: adminReadinessHealthService})
	testutil.RequireNoErrorf(t, err, "readiness health check")
	testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "readiness status")
	resp, err = healthClient.Check(context.Background(), &healthv1.HealthCheckRequest{})
	testutil.RequireNoErrorf(t, err, "liveness health check")
	testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_SERVING, "liveness status")

	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerHealthLivenessReflectsLocalStorageFailure(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Health: fakeHealthApplication{livenessErr: errors.New("blockstore closed")},
	}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	adminConn := dialTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	healthClient := healthv1.NewHealthClient(adminConn)
	resp, err := healthClient.Check(context.Background(), &healthv1.HealthCheckRequest{})
	testutil.RequireNoErrorf(t, err, "liveness health check")
	testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "liveness status")

	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerHealthRejectsUnknownService(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Health: fakeHealthApplication{},
	}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	adminConn := dialTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	healthClient := healthv1.NewHealthClient(adminConn)
	_, err := healthClient.Check(context.Background(), &healthv1.HealthCheckRequest{Service: "scrap.unknown.v1.Service"})
	requireCode(t, err, codes.NotFound)

	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerServesLocalStorageApplications(t *testing.T) {
	dir := t.TempDir()
	app, err := localstorage.Open(dir)
	testutil.RequireNoErrorf(t, err, "open local storage")
	defer func() { testutil.RequireNoErrorf(t, app.Close(), "close app") }()
	operationStore, err := operations.Open(dir)
	testutil.RequireNoErrorf(t, err, "open operation store")
	defer func() { testutil.RequireNoErrorf(t, operationStore.Close(), "close operation store") }()

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Documents:    app,
		Transactions: app,
		Inspect:      app,
		Repair:       app,
		Member:       app,
		DR:           app,
		Operations:   operationStore,
	}, testAuthorizationManager(t), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialAuthorizedTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	documents := scrapv1.NewDocumentServiceClient(publicConn)
	transactions := scrapv1.NewTransactionServiceClient(publicConn)

	data := []byte("invoice bytes")
	sum := sha256.Sum256(data)
	expectedLength := uint64(len(data))
	writeResp := writeLocalStorageDocument(t, documents, data, sum[:], expectedLength)
	requireLocalStorageHead(t, documents, writeResp.GetMetadata().GetIdentity(), expectedLength)
	requireLocalStorageRead(t, documents, writeResp.GetMetadata().GetIdentity(), data)
	requireLocalStorageTransaction(t, transactions)

	adminConn := dialAuthorizedTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	inspectClient := adminv1.NewInspectServiceClient(adminConn)
	inspected := requireLocalStorageInspectDocument(t, inspectClient, expectedLength, sum[:])
	blockID := inspected.GetBlockIds()[0]
	requireLocalStorageInspectBlock(t, inspectClient, blockID, expectedLength)
	requireLocalStorageClusterSummary(t, inspectClient)
	requireLocalStorageShard(t, inspectClient)
	requireLocalStorageMember(t, inspectClient)
	requireLocalStorageRunway(t, inspectClient)

	restoreClient := adminv1.NewRestoreServiceClient(adminConn)
	repairClient := adminv1.NewRepairServiceClient(adminConn)
	requireLocalStorageRepairQueueEmpty(t, repairClient)
	memberClient := adminv1.NewMemberServiceClient(adminConn)
	requireLocalStorageCordon(t, memberClient)
	requireLocalStorageEvictionSafetyWarning(t, memberClient)
	drClient := adminv1.NewDisasterRecoveryServiceClient(adminConn)
	requireLocalStorageRecoveryReadinessWarning(t, drClient)

	requireLocalStorageRestorePlanStored(t, restoreClient, operationStore)

	_ = publicConn.Close()
	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func writeLocalStorageDocument(
	t *testing.T,
	documents scrapv1.DocumentServiceClient,
	data []byte,
	sum []byte,
	expectedLength uint64,
) *scrapv1.WriteDocumentResponse {
	t.Helper()
	write, err := documents.WriteDocument(context.Background())
	testutil.RequireNoErrorf(t, err, "WriteDocument")
	testutil.RequireNoErrorf(t, write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			Identity: &scrapv1.DocumentIdentity{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  "invoice.xml",
			},
			DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
			ExpectedLength:       &expectedLength,
			ExpectedSha256:       sum,
			ClientIdempotencyKey: stringPtr("write-1"),
			CreatedByService:     "billing-etl",
		}},
	}), "send init")
	testutil.RequireNoErrorf(t, write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: data}},
	}), "send chunk")
	writeResp, err := write.CloseAndRecv()
	testutil.RequireNoErrorf(t, err, "close write")
	testutil.RequireEqualf(t, uint32(1), writeResp.GetAchievedReplicaCount(), "write achieved replica count")
	testutil.RequireEqualf(t, expectedLength, writeResp.GetMetadata().GetLength(), "write metadata length")
	return writeResp
}

func requireLocalStorageHead(t *testing.T, documents scrapv1.DocumentServiceClient, identity *scrapv1.DocumentIdentity, expectedLength uint64) {
	t.Helper()
	headResp, err := documents.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{Identity: identity})
	testutil.RequireNoErrorf(t, err, "HeadDocument")
	testutil.RequireEqualf(t, expectedLength, headResp.GetMetadata().GetLength(), "head document length")
}

func requireLocalStorageRead(t *testing.T, documents scrapv1.DocumentServiceClient, identity *scrapv1.DocumentIdentity, data []byte) {
	t.Helper()
	read, err := documents.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{Identity: identity})
	testutil.RequireNoErrorf(t, err, "ReadDocument")
	first, err := read.Recv()
	testutil.RequireNoErrorf(t, err, "recv metadata")
	testutil.RequireNotNilf(t, first.GetMetadata(), "first read response metadata")
	testutil.RequireEqualf(t, scrapv1.StorageSource_STORAGE_SOURCE_LOCAL, first.GetMetadata().GetSource(), "first read source")
	got := readLocalStorageChunks(t, read)
	testutil.RequireTruef(t, bytes.Equal(got, data), "read bytes = %q, want %q", got, data)
}

func readLocalStorageChunks(t *testing.T, read grpc.ServerStreamingClient[scrapv1.ReadDocumentResponse]) []byte {
	t.Helper()
	var got bytes.Buffer
	for {
		msg, err := read.Recv()
		if errors.Is(err, io.EOF) {
			return got.Bytes()
		}
		testutil.RequireNoErrorf(t, err, "recv chunk")
		got.Write(msg.GetChunk().GetData())
	}
}

func requireLocalStorageTransaction(t *testing.T, transactions scrapv1.TransactionServiceClient) {
	t.Helper()
	transactionResp, err := transactions.GetTransaction(context.Background(), &scrapv1.GetTransactionRequest{
		Transaction: &scrapv1.TransactionIdentity{TenantId: "tenant", TransactionId: "txn"},
	})
	testutil.RequireNoErrorf(t, err, "GetTransaction")
	testutil.RequireEqualf(t, uint32(1), transactionResp.GetTransaction().GetDocumentCount(), "transaction document count")
}

func requireLocalStorageInspectDocument(
	t *testing.T,
	inspectClient adminv1.InspectServiceClient,
	expectedLength uint64,
	sum []byte,
) *adminv1.AdminDocument {
	t.Helper()
	inspectResp, err := inspectClient.GetDocument(context.Background(), &adminv1.GetDocumentRequest{
		Document: &adminv1.DocumentTarget{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	testutil.RequireNoErrorf(t, err, "GetDocument")
	inspected := inspectResp.GetDocument()
	testutil.RequireEqualf(t, "local", inspected.GetShardId(), "inspect document shard")
	testutil.RequireEqualf(t, expectedLength, inspected.GetLength(), "inspect document length")
	testutil.RequireTruef(t, bytes.Equal(inspected.GetLogicalSha256(), sum), "inspect logical sha")
	testutil.RequireEqualf(t, 1, len(inspected.GetBlockIds()), "inspect document block count")
	testutil.RequireTruef(t, inspected.GetBlockIds()[0] != "", "inspect document block id")
	testutil.RequireFalsef(t, inspected.GetRepairRequired(), "inspect document repair required")
	return inspected
}

func requireLocalStorageInspectBlock(
	t *testing.T,
	inspectClient adminv1.InspectServiceClient,
	blockID string,
	expectedLength uint64,
) {
	t.Helper()
	blockResp, err := inspectClient.GetBlock(context.Background(), &adminv1.GetBlockRequest{
		Block: &adminv1.BlockTarget{ShardId: "local", BlockId: blockID},
	})
	testutil.RequireNoErrorf(t, err, "GetBlock")
	block := blockResp.GetBlock()
	testutil.RequireEqualf(t, "local", block.GetShardId(), "inspect block shard")
	testutil.RequireEqualf(t, blockID, block.GetBlockId(), "inspect block id")
	testutil.RequireTruef(t, block.GetLength() > expectedLength, "inspect block length = %d, want > %d", block.GetLength(), expectedLength)
	testutil.RequireEqualf(t, sha256.Size, len(block.GetChecksum()), "inspect block checksum size")
	testutil.RequireEqualf(t, 1, len(block.GetReplicaMemberIds()), "inspect block replica count")
	testutil.RequireEqualf(t, "local", block.GetReplicaMemberIds()[0], "inspect block replica member")
	testutil.RequireEqualf(t, "blocks/"+blockID+".blk", block.GetBackendObjectKey(), "inspect block backend object key")
}

func requireLocalStorageClusterSummary(t *testing.T, inspectClient adminv1.InspectServiceClient) {
	t.Helper()
	summaryResp, err := inspectClient.GetClusterSummary(context.Background(), &adminv1.GetClusterSummaryRequest{})
	testutil.RequireNoErrorf(t, err, "GetClusterSummary")
	summary := summaryResp.GetSummary()
	testutil.RequireEqualf(t, uint32(1), summary.GetShardCount(), "cluster summary shard count")
	testutil.RequireEqualf(t, uint32(1), summary.GetStorageMemberCount(), "cluster summary member count")
	testutil.RequireTruef(t, summary.GetLocalBytesUsed() > 0, "cluster summary local bytes used")
	testutil.RequireTruef(t, summary.GetLocalBytesCapacity() > 0, "cluster summary local bytes capacity")
}

func requireLocalStorageShard(t *testing.T, inspectClient adminv1.InspectServiceClient) {
	t.Helper()
	shardResp, err := inspectClient.GetShard(context.Background(), &adminv1.GetShardRequest{ShardId: "local"})
	testutil.RequireNoErrorf(t, err, "GetShard")
	shard := shardResp.GetShard()
	testutil.RequireEqualf(t, "local", shard.GetLeaderMemberId(), "shard leader")
	testutil.RequireEqualf(t, 1, len(shard.GetVoterMemberIds()), "shard voter count")
	testutil.RequireTruef(t, shard.GetAppliedIndex() > 0, "shard applied index")
}

func requireLocalStorageMember(t *testing.T, inspectClient adminv1.InspectServiceClient) {
	t.Helper()
	memberResp, err := inspectClient.GetMember(context.Background(), &adminv1.GetMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	testutil.RequireNoErrorf(t, err, "GetMember")
	member := memberResp.GetStorageMember()
	testutil.RequireEqualf(t, adminv1.MemberState_MEMBER_STATE_ONLINE, member.GetState(), "member state")
	testutil.RequireTruef(t, member.GetBytesCapacity() > 0, "member bytes capacity")
}

func requireLocalStorageRunway(t *testing.T, inspectClient adminv1.InspectServiceClient) {
	t.Helper()
	runwayResp, err := inspectClient.GetCapacityRunway(context.Background(), &adminv1.GetCapacityRunwayRequest{})
	testutil.RequireNoErrorf(t, err, "GetCapacityRunway")
	runway := runwayResp.GetRunway()
	testutil.RequireEqualf(t, "local-non-production", runway.GetCapacityProfileId(), "runway capacity profile")
	testutil.RequireTruef(t, runway.GetUsableBytesRemaining() > 0, "runway usable bytes")
	testutil.RequireTruef(t, len(runway.GetWarnings()) > 0, "runway warnings")
}

func requireLocalStorageRepairQueueEmpty(t *testing.T, repairClient adminv1.RepairServiceClient) {
	t.Helper()
	repairQueue, err := repairClient.GetRepairQueue(context.Background(), &adminv1.GetRepairQueueRequest{ShardId: stringPtr("local")})
	testutil.RequireNoErrorf(t, err, "GetRepairQueue")
	testutil.RequireEqualf(t, 0, len(repairQueue.GetItems()), "repair queue item count")
}

func requireLocalStorageCordon(t *testing.T, memberClient adminv1.MemberServiceClient) {
	t.Helper()
	cordonResp, err := memberClient.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		Reason:        "maintenance",
		OperationId:   "018f6d86-7a22-7abc-8def-123456789abc",
	})
	testutil.RequireNoErrorf(t, err, "CordonMember")
	testutil.RequireTruef(t, cordonResp.GetStorageMember().GetCordoned(), "cordon response should be cordoned")
}

func requireLocalStorageEvictionSafetyWarning(t *testing.T, memberClient adminv1.MemberServiceClient) {
	t.Helper()
	safetyResp, err := memberClient.GetEvictionSafety(context.Background(), &adminv1.GetEvictionSafetyRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	testutil.RequireNoErrorf(t, err, "GetEvictionSafety")
	testutil.RequireFalsef(t, safetyResp.GetSafety().GetSafeToEvict(), "eviction should not be safe")
	testutil.RequireTruef(t, len(safetyResp.GetSafety().GetWarnings()) > 0, "eviction warning")
}

func requireLocalStorageRecoveryReadinessWarning(t *testing.T, drClient adminv1.DisasterRecoveryServiceClient) {
	t.Helper()
	readinessResp, err := drClient.GetRecoveryReadiness(context.Background(), &adminv1.GetRecoveryReadinessRequest{})
	testutil.RequireNoErrorf(t, err, "GetRecoveryReadiness")
	testutil.RequireFalsef(t, readinessResp.GetReadiness().GetReady(), "recovery readiness should not be ready")
	testutil.RequireTruef(t, len(readinessResp.GetReadiness().GetWarnings()) > 0, "recovery readiness warnings")
}

func requireLocalStorageRestorePlanStored(
	t *testing.T,
	restoreClient adminv1.RestoreServiceClient,
	operationStore *operations.Store,
) {
	t.Helper()
	planResp, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Document{
					Document: &adminv1.DocumentTarget{
						TenantId:      "tenant",
						TransactionId: "txn",
						DocumentName:  "invoice.xml",
					},
				},
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "PlanRestore")
	_, err = operationStore.GetPlan(planResp.GetPlan().GetOperationPlanId())
	testutil.RequireNoErrorf(t, err, "get stored operation plan")
}

func TestListenRejectsProductionWriteACKWithoutReadinessGate(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "127.0.0.1:65535"
	cfg.EnableProductionWriteACK = true

	server, err := Listen(cfg, Applications{})
	if server != nil {
		testutil.RequireNoErrorf(t, server.Close(), "close server")
	}
	if !errors.Is(err, config.ErrProductionWriteACKReadinessGate) {
		t.Fatalf("Listen error = %v, want %v", err, config.ErrProductionWriteACKReadinessGate)
	}
	for _, gate := range []string{
		config.ProductionWriteACKReadinessGate,
		config.ReadinessGateMetadataCompatibility,
		config.ReadinessGateRaftMetadataDurability,
		config.ReadinessGatePeerByteDurability,
		config.ReadinessGateBackendRestoreWorkflow,
		config.ReadinessGateOpenBaoEnvelopeWorkflow,
		config.ReadinessGateCapacityAdmission,
		config.ReadinessGateOperatorReadiness,
	} {
		if !strings.Contains(err.Error(), gate) {
			t.Fatalf("Listen error %q does not name missing gate %s", err, gate)
		}
	}
}

func TestListenFailsClosedWithoutAuthorizationPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "127.0.0.1:1"
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()

	server, err := Listen(cfg, Applications{})
	if server != nil {
		testutil.RequireNoErrorf(t, server.Close(), "close server")
	}
	if err == nil || !strings.Contains(err.Error(), "authorization policy path is required") {
		t.Fatalf("Listen error = %v, want missing authorization policy", err)
	}
}

func TestServerAuthorizesPublicAndAdminCapabilities(t *testing.T) {
	policyPath := writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] },
    "operator": { "capabilities": ["admin.inspect.cluster_summary"] }
  }
}`)
	manager, err := authz.LoadManagerFromFile(policyPath, authorizationCapabilities())
	testutil.RequireNoErrorf(t, err, "load authorization policy")

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{}, manager, policyPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	headReq := &scrapv1.HeadDocumentRequest{Identity: &scrapv1.DocumentIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}}
	_, err = publicClient.HeadDocument(context.Background(), headReq)
	requireCode(t, err, codes.Unauthenticated)
	_, err = publicClient.HeadDocument(workloadContext("operator"), headReq)
	requireCode(t, err, codes.PermissionDenied)
	_, err = publicClient.HeadDocument(workloadContext("billing-etl"), headReq)
	requireCode(t, err, codes.Unimplemented)

	adminConn := dialTestServer(t, adminListener)
	defer func() { _ = adminConn.Close() }()
	adminClient := adminv1.NewInspectServiceClient(adminConn)
	_, err = adminClient.GetClusterSummary(workloadContext("billing-etl"), &adminv1.GetClusterSummaryRequest{})
	requireCode(t, err, codes.PermissionDenied)
	_, err = adminClient.GetClusterSummary(workloadContext("operator"), &adminv1.GetClusterSummaryRequest{})
	requireCode(t, err, codes.Unimplemented)

	_ = publicConn.Close()
	_ = adminConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerDeniesPublicRequestOutsideAllowedTenant(t *testing.T) {
	policyPath := writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": {
      "capabilities": ["public.document.head"],
      "allowed_tenants": ["tenant-a"]
    }
  }
}`)
	manager, err := authz.LoadManagerFromFile(policyPath, authorizationCapabilities())
	testutil.RequireNoErrorf(t, err, "load authorization policy")

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{Documents: readAllDocuments{}}, manager, policyPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	_, err = publicClient.HeadDocument(workloadContext("billing-etl"), &scrapv1.HeadDocumentRequest{Identity: &scrapv1.DocumentIdentity{
		TenantId:      "tenant-b",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}})
	requireCode(t, err, codes.PermissionDenied)

	_, err = publicClient.HeadDocument(workloadContext("billing-etl"), &scrapv1.HeadDocumentRequest{Identity: &scrapv1.DocumentIdentity{
		TenantId:      "tenant-a",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}})
	testutil.RequireNoErrorf(t, err, "allowed tenant head")

	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerAuditsDeniedRequests(t *testing.T) {
	policyPath := writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.inspect.cluster_summary"] }
  }
}`)
	manager, err := authz.LoadManagerFromFile(policyPath, authorizationCapabilities())
	testutil.RequireNoErrorf(t, err, "load authorization policy")
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operation store")
	defer func() { testutil.RequireNoErrorf(t, store.Close(), "close store") }()

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{Operations: store}, manager, policyPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	headReq := &scrapv1.HeadDocumentRequest{Identity: &scrapv1.DocumentIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}}
	deniedCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		authz.WorkloadIdentityMetadataKey, "operator",
		"x-request-id", "req-denied",
		"x-correlation-id", "corr-denied",
	))
	_, err = publicClient.HeadDocument(deniedCtx, headReq)
	requireCode(t, err, codes.PermissionDenied)

	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	if len(events) != 1 {
		t.Fatalf("audit events = %#v, want one denied request event", events)
	}
	event := events[0]
	testutil.RequireEqualf(t, event.GetEventType(), "authorization_denied", "audit event type")
	testutil.RequireEqualf(t, event.GetActorIdentity(), "operator", "audit actor")
	testutil.RequireEqualf(t, event.GetOperationType(), scrapv1.DocumentService_HeadDocument_FullMethodName, "audit operation type")
	testutil.RequireEqualf(t, event.GetOperationId(), "corr-denied", "audit operation id")
	testutil.RequireEqualf(t, event.GetMetadata()["capability"], "public.document.head", "audit capability")
	testutil.RequireEqualf(t, event.GetMetadata()["reason"], authz.ReasonCapabilityDenied, "audit reason")
	testutil.RequireEqualf(t, event.GetMetadata()["tenant_id"], "tenant", "audit tenant")
	testutil.RequireEqualf(t, event.GetMetadata()["correlation_id"], "corr-denied", "audit correlation id")
	testutil.RequireEqualf(t, event.GetMetadata()["request_id"], "req-denied", "audit request id")

	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestAuthorizationPolicyReloadRejectsBadPolicyAndKeepsLastValidPolicy(t *testing.T) {
	policyPath := writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	manager, err := authz.LoadManagerFromFile(policyPath, authorizationCapabilities())
	testutil.RequireNoErrorf(t, err, "load authorization policy")

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{}, manager, policyPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer func() { _ = publicConn.Close() }()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	headReq := &scrapv1.HeadDocumentRequest{Identity: &scrapv1.DocumentIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}}
	_, err = publicClient.HeadDocument(workloadContext("billing-etl"), headReq)
	requireCode(t, err, codes.Unimplemented)

	testutil.RequireNoErrorf(t, os.WriteFile(policyPath, []byte(`{"version":"policy-v2","workloads":{"billing-etl":{"capabilities":["public"]}}}`), 0o600), "write invalid policy")
	if err := server.ReloadAuthorizationPolicy(); err == nil {
		t.Fatal("reload invalid policy succeeded")
	}
	alerts := manager.ReloadAlerts()
	if len(alerts) != 1 || alerts[0].Code != authz.ReasonPolicyReloadRejected {
		t.Fatalf("alerts = %#v, want rejected reload alert", alerts)
	}
	_, err = publicClient.HeadDocument(workloadContext("billing-etl"), headReq)
	requireCode(t, err, codes.Unimplemented)

	_ = publicConn.Close()
	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestAuthorizationPolicyReloadReturnsReloadAndAuditErrors(t *testing.T) {
	policyPath := writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	manager, err := authz.LoadManagerFromFile(policyPath, authorizationCapabilities())
	testutil.RequireNoErrorf(t, err, "load authorization policy")
	testutil.RequireNoErrorf(t, os.WriteFile(policyPath, []byte(`{"version":"policy-v2","workloads":{"billing-etl":{"capabilities":["public"]}}}`), 0o600), "write invalid policy")
	auditErr := errors.New("audit append failed")
	server := &Server{
		authorization: manager,
		policyPath:    policyPath,
		auditEvents:   failingAuditEventAppender{err: auditErr},
	}

	err = server.ReloadAuthorizationPolicy()
	if !errors.Is(err, authz.ErrInvalidPolicy) {
		t.Fatalf("reload error = %v, want invalid policy", err)
	}
	if !errors.Is(err, auditErr) {
		t.Fatalf("reload error = %v, want joined audit error", err)
	}
}

func dialTestServer(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	return dialTestServerWithWorkload(t, listener, "")
}

func dialAuthorizedTestServer(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	return dialTestServerWithWorkload(t, listener, "test-workload")
}

func dialTestServerWithWorkload(t *testing.T, listener *bufconn.Listener, workload string) *grpc.ClientConn {
	t.Helper()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	options := []grpc.DialOption{
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if workload != "" {
		options = append(options, grpc.WithUnaryInterceptor(workloadUnaryClientInterceptor(workload)))
		options = append(options, grpc.WithStreamInterceptor(workloadStreamClientInterceptor(workload)))
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		options...,
	)
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	return conn
}

func workloadUnaryClientInterceptor(workload string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(workloadContextWithBase(ctx, workload), method, req, reply, cc, opts...)
	}
}

func workloadStreamClientInterceptor(workload string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(workloadContextWithBase(ctx, workload), desc, cc, method, opts...)
	}
}

func stringPtr(value string) *string {
	return &value
}

func workloadContext(workload string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(authz.WorkloadIdentityMetadataKey, workload))
}

func workloadContextWithBase(ctx context.Context, workload string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authz.WorkloadIdentityMetadataKey, workload)
}

func writeAuthorizationPolicy(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/authz-policy.json"
	testutil.RequireNoErrorf(t, os.WriteFile(path, []byte(body), 0o600), "write authorization policy")
	return path
}

func testAuthorizationManager(t *testing.T) *authz.Manager {
	t.Helper()
	capabilities := authorizationCapabilities()
	manager, err := authz.NewManager(authz.Policy{
		Version: "test-policy",
		Workloads: map[string]authz.WorkloadPolicy{
			"test-workload": {Capabilities: capabilities},
		},
	}, capabilities)
	testutil.RequireNoErrorf(t, err, "new test authorization manager")
	return manager
}

func requireNewServerWithConfig(
	t *testing.T,
	cfg config.Config,
	publicListener,
	adminListener net.Listener,
	apps Applications,
	authorization *authz.Manager,
) *Server {
	t.Helper()
	server, err := newServerWithConfig(cfg, publicListener, adminListener, apps, authorization, "")
	testutil.RequireNoErrorf(t, err, "new server with config")
	return server
}

func requireStreamLimitRejection(t *testing.T, err error) {
	t.Helper()
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.ResourceExhausted, codes.Unavailable:
		return
	default:
		t.Fatalf("stream limit error code = %s, want DeadlineExceeded, ResourceExhausted, or Unavailable; err = %v", status.Code(err), err)
	}
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	if st.Code() != code {
		t.Fatalf("code = %s, want %s", st.Code(), code)
	}
}

type failingAuditEventAppender struct {
	err error
}

func (s failingAuditEventAppender) AppendAuditEvent(*adminv1.AuditEvent) error {
	return s.err
}

type fakeHealthApplication struct {
	readinessErr error
	livenessErr  error
}

func (a fakeHealthApplication) CheckReadiness(context.Context) error {
	return a.readinessErr
}

func (a fakeHealthApplication) CheckLiveness(context.Context) error {
	return a.livenessErr
}

type readAllDocuments struct{}

func (d readAllDocuments) WriteDocument(
	_ context.Context,
	init storageapp.WriteDocumentInit,
	chunks storageapp.ChunkReader,
) (storageapp.WriteDocumentResult, error) {
	for {
		_, err := chunks.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return storageapp.WriteDocumentResult{}, err
		}
	}
	return storageapp.WriteDocumentResult{
		Metadata: storageapp.DocumentMetadata{Identity: init.Identity},
	}, nil
}

func (d readAllDocuments) HeadDocument(
	_ context.Context,
	req storageapp.HeadDocumentRequest,
) (storageapp.DocumentMetadata, error) {
	return storageapp.DocumentMetadata{Identity: req.Identity}, nil
}

func (d readAllDocuments) ReadDocument(
	context.Context,
	storageapp.ReadDocumentRequest,
	storageapp.ReadDocumentSender,
) error {
	return nil
}

func (d readAllDocuments) FindDocuments(
	context.Context,
	storageapp.FindDocumentsRequest,
) (storageapp.FindDocumentsResult, error) {
	return storageapp.FindDocumentsResult{}, nil
}

type blockingReadDocuments struct {
	readAllDocuments
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
}

type largeReadDocuments struct {
	readAllDocuments
	data []byte
}

func (d largeReadDocuments) ReadDocument(
	_ context.Context,
	_ storageapp.ReadDocumentRequest,
	sender storageapp.ReadDocumentSender,
) error {
	return sender.SendChunk(d.data)
}

func newBlockingReadDocuments() *blockingReadDocuments {
	return &blockingReadDocuments{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (d *blockingReadDocuments) ReadDocument(
	ctx context.Context,
	_ storageapp.ReadDocumentRequest,
	_ storageapp.ReadDocumentSender,
) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.once.Do(func() {
		close(d.started)
	})
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *blockingReadDocuments) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func validWriteInit(documentName string) *scrapv1.WriteDocumentInit {
	return &scrapv1.WriteDocumentInit{
		Identity:         documentIdentity(documentName),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}
}

func validReadRequest(documentName string) *scrapv1.ReadDocumentRequest {
	return &scrapv1.ReadDocumentRequest{Identity: documentIdentity(documentName)}
}

func documentIdentity(documentName string) *scrapv1.DocumentIdentity {
	return &scrapv1.DocumentIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
		DocumentName:  documentName,
	}
}
