package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/config"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestServerServesPublicAndAdminAPIs(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer publicConn.Close()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	_, err := publicClient.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	requireCode(t, err, codes.Unimplemented)

	adminConn := dialTestServer(t, adminListener)
	defer adminConn.Close()
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
	if err := <-done; err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
}

func TestServerServesLocalStorageApplications(t *testing.T) {
	dir := t.TempDir()
	app, err := localstorage.Open(dir)
	if err != nil {
		t.Fatalf("open local storage: %v", err)
	}
	defer app.Close()
	operationStore, err := operations.Open(dir)
	if err != nil {
		t.Fatalf("open operation store: %v", err)
	}
	defer operationStore.Close()

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
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer publicConn.Close()
	documents := scrapv1.NewDocumentServiceClient(publicConn)
	transactions := scrapv1.NewTransactionServiceClient(publicConn)

	data := []byte("invoice bytes")
	sum := sha256.Sum256(data)
	expectedLength := uint64(len(data))
	write, err := documents.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			Identity: &scrapv1.DocumentIdentity{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  "invoice.xml",
			},
			DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
			ExpectedLength:       &expectedLength,
			ExpectedSha256:       sum[:],
			ClientIdempotencyKey: stringPtr("write-1"),
			CreatedByService:     "billing-etl",
		}},
	}); err != nil {
		t.Fatalf("send init: %v", err)
	}
	if err := write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: data}},
	}); err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	writeResp, err := write.CloseAndRecv()
	if err != nil {
		t.Fatalf("close write: %v", err)
	}
	if writeResp.GetAchievedReplicaCount() != 1 || writeResp.GetMetadata().GetLength() != expectedLength {
		t.Fatalf("write response = %#v, want local 1/1 write metadata", writeResp)
	}

	headResp, err := documents.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: writeResp.GetMetadata().GetIdentity(),
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if headResp.GetMetadata().GetLength() != expectedLength {
		t.Fatalf("head length = %d, want %d", headResp.GetMetadata().GetLength(), expectedLength)
	}

	read, err := documents.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		Identity: writeResp.GetMetadata().GetIdentity(),
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	first, err := read.Recv()
	if err != nil {
		t.Fatalf("recv metadata: %v", err)
	}
	if first.GetMetadata() == nil || first.GetMetadata().GetSource() != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("first read response = %#v, want local metadata", first)
	}
	var got bytes.Buffer
	for {
		msg, err := read.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv chunk: %v", err)
		}
		got.Write(msg.GetChunk().GetData())
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("read bytes = %q, want %q", got.Bytes(), data)
	}

	transactionResp, err := transactions.GetTransaction(context.Background(), &scrapv1.GetTransactionRequest{
		Transaction: &scrapv1.TransactionIdentity{TenantId: "tenant", TransactionId: "txn"},
	})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if transactionResp.GetTransaction().GetDocumentCount() != 1 {
		t.Fatalf("transaction = %#v, want one document", transactionResp.GetTransaction())
	}

	adminConn := dialTestServer(t, adminListener)
	defer adminConn.Close()
	inspectClient := adminv1.NewInspectServiceClient(adminConn)
	inspectResp, err := inspectClient.GetDocument(context.Background(), &adminv1.GetDocumentRequest{
		Document: &adminv1.DocumentTarget{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	inspected := inspectResp.GetDocument()
	if inspected.GetShardId() != "local" ||
		inspected.GetLength() != expectedLength ||
		!bytes.Equal(inspected.GetLogicalSha256(), sum[:]) ||
		len(inspected.GetBlockIds()) != 1 ||
		inspected.GetBlockIds()[0] == "" ||
		inspected.GetRepairRequired() {
		t.Fatalf("inspect document = %#v, want local physical metadata", inspected)
	}
	blockID := inspected.GetBlockIds()[0]
	blockResp, err := inspectClient.GetBlock(context.Background(), &adminv1.GetBlockRequest{
		Block: &adminv1.BlockTarget{ShardId: "local", BlockId: blockID},
	})
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	block := blockResp.GetBlock()
	if block.GetShardId() != "local" ||
		block.GetBlockId() != blockID ||
		block.GetLength() <= expectedLength ||
		len(block.GetChecksum()) != sha256.Size ||
		len(block.GetReplicaMemberIds()) != 1 ||
		block.GetReplicaMemberIds()[0] != "local" ||
		block.GetBackendObjectKey() != "blocks/"+blockID+".blk" {
		t.Fatalf("inspect block = %#v, want local block metadata", block)
	}
	summaryResp, err := inspectClient.GetClusterSummary(context.Background(), &adminv1.GetClusterSummaryRequest{})
	if err != nil {
		t.Fatalf("GetClusterSummary: %v", err)
	}
	if summaryResp.GetSummary().GetShardCount() != 1 ||
		summaryResp.GetSummary().GetStorageMemberCount() != 1 ||
		summaryResp.GetSummary().GetLocalBytesUsed() == 0 ||
		summaryResp.GetSummary().GetLocalBytesCapacity() == 0 {
		t.Fatalf("cluster summary = %#v, want local single-member summary", summaryResp.GetSummary())
	}
	shardResp, err := inspectClient.GetShard(context.Background(), &adminv1.GetShardRequest{ShardId: "local"})
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if shardResp.GetShard().GetLeaderMemberId() != "local" ||
		len(shardResp.GetShard().GetVoterMemberIds()) != 1 ||
		shardResp.GetShard().GetAppliedIndex() == 0 {
		t.Fatalf("shard = %#v, want local shard metadata", shardResp.GetShard())
	}
	memberResp, err := inspectClient.GetMember(context.Background(), &adminv1.GetMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if memberResp.GetStorageMember().GetState() != adminv1.MemberState_MEMBER_STATE_ONLINE ||
		memberResp.GetStorageMember().GetBytesCapacity() == 0 {
		t.Fatalf("member = %#v, want online local member", memberResp.GetStorageMember())
	}
	runwayResp, err := inspectClient.GetCapacityRunway(context.Background(), &adminv1.GetCapacityRunwayRequest{})
	if err != nil {
		t.Fatalf("GetCapacityRunway: %v", err)
	}
	if runwayResp.GetRunway().GetCapacityProfileId() != "local-non-production" ||
		runwayResp.GetRunway().GetUsableBytesRemaining() == 0 ||
		len(runwayResp.GetRunway().GetWarnings()) == 0 {
		t.Fatalf("runway = %#v, want local non-production runway", runwayResp.GetRunway())
	}

	restoreClient := adminv1.NewRestoreServiceClient(adminConn)
	repairClient := adminv1.NewRepairServiceClient(adminConn)
	repairQueue, err := repairClient.GetRepairQueue(context.Background(), &adminv1.GetRepairQueueRequest{ShardId: stringPtr("local")})
	if err != nil {
		t.Fatalf("GetRepairQueue: %v", err)
	}
	if len(repairQueue.GetItems()) != 0 {
		t.Fatalf("repair queue = %#v, want empty queue", repairQueue.GetItems())
	}
	memberClient := adminv1.NewMemberServiceClient(adminConn)
	cordonResp, err := memberClient.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		Reason:        "maintenance",
		OperationId:   "018f6d86-7a22-7abc-8def-123456789abc",
	})
	if err != nil {
		t.Fatalf("CordonMember: %v", err)
	}
	if !cordonResp.GetStorageMember().GetCordoned() {
		t.Fatalf("cordon response = %#v, want cordoned", cordonResp.GetStorageMember())
	}
	safetyResp, err := memberClient.GetEvictionSafety(context.Background(), &adminv1.GetEvictionSafetyRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	if err != nil {
		t.Fatalf("GetEvictionSafety: %v", err)
	}
	if safetyResp.GetSafety().GetSafeToEvict() || len(safetyResp.GetSafety().GetWarnings()) == 0 {
		t.Fatalf("eviction safety = %#v, want unsafe warning", safetyResp.GetSafety())
	}
	drClient := adminv1.NewDisasterRecoveryServiceClient(adminConn)
	readinessResp, err := drClient.GetRecoveryReadiness(context.Background(), &adminv1.GetRecoveryReadinessRequest{})
	if err != nil {
		t.Fatalf("GetRecoveryReadiness: %v", err)
	}
	if readinessResp.GetReadiness().GetReady() || len(readinessResp.GetReadiness().GetWarnings()) == 0 {
		t.Fatalf("recovery readiness = %#v, want not ready warnings", readinessResp.GetReadiness())
	}

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
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if _, err := operationStore.GetPlan(planResp.GetPlan().GetOperationPlanId()); err != nil {
		t.Fatalf("get stored operation plan: %v", err)
	}

	_ = publicConn.Close()
	_ = adminConn.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
}

func TestListenRejectsProductionWriteACKWithoutReadinessGate(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "127.0.0.1:65535"
	cfg.EnableProductionWriteACK = true

	server, err := Listen(cfg, Applications{})
	if server != nil {
		server.Close()
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

func dialTestServer(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}

func stringPtr(value string) *string {
	return &value
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
