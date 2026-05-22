package api

import (
	"context"
	"net"
	"testing"
	"time"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAdminServerPlanRestoreRejectsInvalidTargetOverGRPC(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Shard{
					Shard: &adminv1.ShardTarget{ShardId: "shard-a"},
				},
			},
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "targets[0]", reasonInvalidTargetKind)
}

func TestAdminServerStartRestoreRejectsMissingOperationIDOverGRPC(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "operation_id", identity.ReasonRequired)
}

func TestAdminServerStartRestoreReturnsUnimplementedAfterValidation(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	if st.Code() != codes.Unimplemented {
		t.Fatalf("code = %s, want %s", st.Code(), codes.Unimplemented)
	}
}

func TestAdminServerCordonMemberValidatesOverGRPC(t *testing.T) {
	_, memberClient, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := memberClient.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		OperationId:   validUUIDv7(),
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "member-a"},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "reason", identity.ReasonRequired)
}

func TestAdminServerGetOperationReadsDurableStore(t *testing.T) {
	store := openTestOperationStore(t)
	operation := testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	resp, err := client.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: validUUIDv7()})
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if resp.GetOperation().GetOperationType() != "repair" || resp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_RUNNING {
		t.Fatalf("operation = %#v, want running repair", resp.GetOperation())
	}
}

func TestAdminServerListOperationsFiltersDurableStore(t *testing.T) {
	store := openTestOperationStore(t)
	for _, operation := range []*adminv1.Operation{
		testOperation("018f6d86-7a22-7abc-8def-123456789abc", "repair", adminv1.OperationState_OPERATION_STATE_RUNNING),
		testOperation("018f6d86-7a22-7abc-8def-123456789abd", "repair", adminv1.OperationState_OPERATION_STATE_SUCCEEDED),
		testOperation("018f6d86-7a22-7abc-8def-123456789abe", "restore", adminv1.OperationState_OPERATION_STATE_RUNNING),
	} {
		if err := store.Put(operation); err != nil {
			t.Fatalf("put operation %s: %v", operation.GetOperationId(), err)
		}
	}
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	resp, err := client.ListOperations(context.Background(), &adminv1.ListOperationsRequest{
		States:        []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_RUNNING},
		OperationType: ptr("repair"),
	})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	if len(resp.GetOperations()) != 1 || resp.GetOperations()[0].GetOperationId() != validUUIDv7() {
		t.Fatalf("operations = %#v, want only running repair operation", resp.GetOperations())
	}
}

func TestAdminServerCancelOperationUpdatesDurableStore(t *testing.T) {
	store := openTestOperationStore(t)
	operation := testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	resp, err := client.CancelOperation(context.Background(), &adminv1.CancelOperationRequest{OperationId: validUUIDv7()})
	if err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	if resp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_CANCELED || resp.GetOperation().GetFinishedAt() == nil {
		t.Fatalf("operation = %#v, want canceled with finished_at", resp.GetOperation())
	}
	stored, err := store.Get(validUUIDv7())
	if err != nil {
		t.Fatalf("get stored operation: %v", err)
	}
	if stored.GetState() != adminv1.OperationState_OPERATION_STATE_CANCELED {
		t.Fatalf("stored state = %s, want canceled", stored.GetState())
	}
}

func TestAdminServerPlanAndStartRestoreUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	restoreClient, _, _, operationClient, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
		Metadata: map[string]string{
			"requested_by": "test",
		},
	})
	if err != nil {
		t.Fatalf("plan restore: %v", err)
	}
	plan := planResp.GetPlan()
	if plan.GetOperationPlanId() == "" || plan.GetPlanHash() == "" || plan.GetExpiresAt() == nil {
		t.Fatalf("plan = %#v, want id/hash/expires_at", plan)
	}
	if plan.GetMetadata()[adminOperationTypeMetadata] != "restore" || len(plan.GetTargets()) != 1 {
		t.Fatalf("plan metadata/targets = %#v/%#v, want restore plan", plan.GetMetadata(), plan.GetTargets())
	}
	if _, err := store.GetPlan(plan.GetOperationPlanId()); err != nil {
		t.Fatalf("get stored plan: %v", err)
	}

	startReq := &adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
		Metadata: map[string]string{
			"ticket": "INC-1",
		},
	}
	startResp, err := restoreClient.StartRestore(context.Background(), startReq)
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}
	operation := startResp.GetOperation()
	if operation.GetOperationType() != "restore" ||
		operation.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		operation.GetMetadata()[adminOperationPlanIDMetadata] != plan.GetOperationPlanId() ||
		operation.GetMetadata()["ticket"] != "INC-1" ||
		len(operation.GetTargets()) != 1 {
		t.Fatalf("operation = %#v, want queued restore from plan", operation)
	}

	getResp, err := operationClient.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: validUUIDv7()})
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if getResp.GetOperation().GetOperationType() != "restore" {
		t.Fatalf("stored operation = %#v, want restore", getResp.GetOperation())
	}

	retryResp, err := restoreClient.StartRestore(context.Background(), startReq)
	if err != nil {
		t.Fatalf("retry start restore: %v", err)
	}
	if retryResp.GetOperation().GetRequestedAt().AsTime() != operation.GetRequestedAt().AsTime() {
		t.Fatal("idempotent retry did not return the existing operation")
	}
}

func TestAdminServerStartRestoreRejectsMismatchedPlanHash(t *testing.T) {
	store := openTestOperationStore(t)
	restoreClient, _, _, _, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
	})
	if err != nil {
		t.Fatalf("plan restore: %v", err)
	}
	_, err = restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: planResp.GetPlan().GetOperationPlanId(),
		PlanHash:        "wrong-hash",
	})
	if code := status.Code(err); code != codes.FailedPrecondition {
		t.Fatalf("code = %s, want %s; err = %v", code, codes.FailedPrecondition, err)
	}
}

func TestAdminServerPlanAndStartRepairUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	_, repairClient, _, _, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := repairClient.PlanRepair(context.Background(), &adminv1.PlanRepairRequest{
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Shard{
					Shard: &adminv1.ShardTarget{ShardId: "shard-a"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("plan repair: %v", err)
	}
	startResp, err := repairClient.StartRepair(context.Background(), &adminv1.StartRepairRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: planResp.GetPlan().GetOperationPlanId(),
		PlanHash:        planResp.GetPlan().GetPlanHash(),
	})
	if err != nil {
		t.Fatalf("start repair: %v", err)
	}
	if startResp.GetOperation().GetOperationType() != "repair" ||
		startResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued repair", startResp.GetOperation())
	}
}

func newAdminTestClients(
	t *testing.T,
) (adminv1.RestoreServiceClient, adminv1.MemberServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer())
	go func() {
		_ = server.Serve(listener)
	}()

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
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewRestoreServiceClient(conn), adminv1.NewMemberServiceClient(conn), cleanup
}

func newAdminOperationClient(t *testing.T, store *operations.Store) (adminv1.OperationServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithOperationStore(store)))
	go func() {
		_ = server.Serve(listener)
	}()

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
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewOperationServiceClient(conn), cleanup
}

func newAdminWorkflowClients(
	t *testing.T,
	store *operations.Store,
) (adminv1.RestoreServiceClient, adminv1.RepairServiceClient, adminv1.LifecycleServiceClient, adminv1.OperationServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithOperationStore(store)))
	go func() {
		_ = server.Serve(listener)
	}()

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
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewRestoreServiceClient(conn),
		adminv1.NewRepairServiceClient(conn),
		adminv1.NewLifecycleServiceClient(conn),
		adminv1.NewOperationServiceClient(conn),
		cleanup
}

func openTestOperationStore(t *testing.T) *operations.Store {
	t.Helper()
	store, err := operations.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open operation store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close operation store: %v", err)
		}
	})
	return store
}

func testOperation(operationID string, operationType string, state adminv1.OperationState) *adminv1.Operation {
	return &adminv1.Operation{
		OperationId:         operationID,
		OperationType:       operationType,
		State:               state,
		RequestedByIdentity: "test-operator",
		RequestedAt:         timestamppb.New(time.Unix(100, 0).UTC()),
	}
}

func documentAdminTarget() *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Document{
			Document: &adminv1.DocumentTarget{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  "invoice.xml",
			},
		},
	}
}

func ptr[T any](value T) *T {
	return &value
}
