package api

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/testutil"
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

func TestAdminServerGetClusterSummaryUsesInspectApplication(t *testing.T) {
	expected := &adminv1.ClusterSummary{
		ShardCount:         1,
		StorageMemberCount: 1,
		LocalBytesUsed:     100,
		LocalBytesCapacity: 1000,
	}
	inspect := &fakeInspectApplication{summary: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetClusterSummary(context.Background(), &adminv1.GetClusterSummaryRequest{})
	testutil.RequireNoErrorf(t, err, "get cluster summary")
	if !proto.Equal(resp.GetSummary(), expected) {
		t.Fatalf("summary = %#v, want %#v", resp.GetSummary(), expected)
	}
}

func TestAdminServerGetShardUsesInspectApplication(t *testing.T) {
	expected := &adminv1.Shard{
		ShardId:        "local",
		LeaderMemberId: "local",
		VoterMemberIds: []string{"local"},
		CommittedIndex: 7,
		AppliedIndex:   7,
	}
	inspect := &fakeInspectApplication{shard: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetShard(context.Background(), &adminv1.GetShardRequest{ShardId: "local"})
	testutil.RequireNoErrorf(t, err, "get shard")
	if inspect.gotShard != "local" {
		t.Fatalf("inspect got shard = %q, want local", inspect.gotShard)
	}
	if !proto.Equal(resp.GetShard(), expected) {
		t.Fatalf("shard = %#v, want %#v", resp.GetShard(), expected)
	}
}

func TestAdminServerGetDocumentUsesInspectApplication(t *testing.T) {
	expected := &adminv1.AdminDocument{
		Document: &adminv1.DocumentTarget{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
		ShardId:        "shard-a",
		BlockIds:       []string{"block-a"},
		Length:         123,
		LogicalSha256:  []byte{1, 2, 3},
		RepairRequired: true,
	}
	inspect := &fakeInspectApplication{document: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetDocument(context.Background(), &adminv1.GetDocumentRequest{
		Document: &adminv1.DocumentTarget{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	testutil.RequireNoErrorf(t, err, "get document")
	if inspect.got != (identity.Document{TenantID: "tenant", TransactionID: "txn", DocumentName: "invoice.xml"}) {
		t.Fatalf("inspect got identity = %#v, want requested document", inspect.got)
	}
	if !proto.Equal(resp.GetDocument(), expected) {
		t.Fatalf("document = %#v, want %#v", resp.GetDocument(), expected)
	}
}

func TestAdminServerGetBlockUsesInspectApplication(t *testing.T) {
	expected := &adminv1.Block{
		ShardId:          "local",
		BlockId:          "block-a",
		Length:           456,
		Checksum:         []byte{4, 5, 6},
		ReplicaMemberIds: []string{"member-a"},
		BackendObjectKey: "blocks/block-a.blk",
	}
	inspect := &fakeInspectApplication{block: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetBlock(context.Background(), &adminv1.GetBlockRequest{
		Block: &adminv1.BlockTarget{
			ShardId: "local",
			BlockId: "block-a",
		},
	})
	testutil.RequireNoErrorf(t, err, "get block")
	if inspect.gotBlock != (BlockTarget{ShardID: "local", BlockID: "block-a"}) {
		t.Fatalf("inspect got block = %#v, want requested block", inspect.gotBlock)
	}
	if !proto.Equal(resp.GetBlock(), expected) {
		t.Fatalf("block = %#v, want %#v", resp.GetBlock(), expected)
	}
}

func TestAdminServerGetMemberUsesInspectApplication(t *testing.T) {
	expected := &adminv1.StorageMember{
		StorageMemberId: "local",
		CellId:          "local",
		State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
		BytesUsed:       100,
		BytesCapacity:   1000,
		LastSeenAt:      timestamppb.New(time.Unix(100, 0).UTC()),
	}
	inspect := &fakeInspectApplication{member: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetMember(context.Background(), &adminv1.GetMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	testutil.RequireNoErrorf(t, err, "get member")
	if inspect.gotMember != "local" {
		t.Fatalf("inspect got member = %q, want local", inspect.gotMember)
	}
	if !proto.Equal(resp.GetStorageMember(), expected) {
		t.Fatalf("member = %#v, want %#v", resp.GetStorageMember(), expected)
	}
}

func TestAdminServerGetCapacityRunwayUsesInspectApplication(t *testing.T) {
	expected := &adminv1.CapacityRunway{
		CapacityProfileId:    "local-non-production",
		UsableBytesRemaining: 900,
		Warnings:             []*adminv1.OperationWarning{{Code: "SCRAP_NON_PRODUCTION_CAPACITY_PROFILE"}},
	}
	inspect := &fakeInspectApplication{runway: expected}
	client, cleanup := newAdminInspectClient(t, inspect)
	defer cleanup()

	resp, err := client.GetCapacityRunway(context.Background(), &adminv1.GetCapacityRunwayRequest{
		CapacityProfileId: ptr("local-non-production"),
	})
	testutil.RequireNoErrorf(t, err, "get capacity runway")
	if inspect.gotCapacityProfileID != "local-non-production" {
		t.Fatalf("inspect got profile = %q, want local-non-production", inspect.gotCapacityProfileID)
	}
	if !proto.Equal(resp.GetRunway(), expected) {
		t.Fatalf("runway = %#v, want %#v", resp.GetRunway(), expected)
	}
}

func TestAdminServerMemberLifecycleUsesMemberApplication(t *testing.T) {
	expected := &adminv1.StorageMember{
		StorageMemberId: "local",
		CellId:          "local",
		State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
		Cordoned:        true,
	}
	safety := &adminv1.EvictionSafety{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		SafeToEvict:   false,
	}
	member := &fakeMemberApplication{cordoned: expected, uncordoned: proto.Clone(expected).(*adminv1.StorageMember), safety: safety}
	member.uncordoned.Cordoned = false
	client, cleanup := newAdminMemberClient(t, member)
	defer cleanup()

	cordonResp, err := client.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		Reason:        "maintenance",
		OperationId:   validUUIDv7(),
	})
	testutil.RequireNoErrorf(t, err, "cordon member")
	if member.cordonReq.StorageMember != "local" || !proto.Equal(cordonResp.GetStorageMember(), expected) {
		t.Fatalf("cordon = %#v req=%#v, want cordoned local", cordonResp.GetStorageMember(), member.cordonReq)
	}

	uncordonResp, err := client.UncordonMember(context.Background(), &adminv1.UncordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		OperationId:   validUUIDv7(),
	})
	testutil.RequireNoErrorf(t, err, "uncordon member")
	if member.uncordonReq.StorageMember != "local" || uncordonResp.GetStorageMember().GetCordoned() {
		t.Fatalf("uncordon = %#v req=%#v, want uncordoned local", uncordonResp.GetStorageMember(), member.uncordonReq)
	}

	safetyResp, err := client.GetEvictionSafety(context.Background(), &adminv1.GetEvictionSafetyRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
	})
	testutil.RequireNoErrorf(t, err, "get eviction safety")
	if member.safetyMemberID != "local" || !proto.Equal(safetyResp.GetSafety(), safety) {
		t.Fatalf("safety = %#v member=%q, want local safety", safetyResp.GetSafety(), member.safetyMemberID)
	}
}

func TestAdminServerGetRecoveryReadinessUsesDisasterRecoveryApplication(t *testing.T) {
	expected := &adminv1.RecoveryReadiness{
		Ready:    false,
		Warnings: []*adminv1.OperationWarning{{Code: "SCRAP_DR_METADATA_EXPORT_MISSING"}},
	}
	dr := &fakeDisasterRecoveryApplication{readiness: expected}
	client, cleanup := newAdminDisasterRecoveryClient(t, dr, nil)
	defer cleanup()

	resp, err := client.GetRecoveryReadiness(context.Background(), &adminv1.GetRecoveryReadinessRequest{})
	testutil.RequireNoErrorf(t, err, "get recovery readiness")
	if dr.calls != 1 || !proto.Equal(resp.GetReadiness(), expected) {
		t.Fatalf("readiness = %#v calls=%d, want fake readiness", resp.GetReadiness(), dr.calls)
	}
}

func TestAdminServerGetOperationReadsDurableStore(t *testing.T) {
	store := openTestOperationStore(t)
	operation := testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	resp, err := client.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: validUUIDv7()})
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, err, "list operations")
	if len(resp.GetOperations()) != 1 || resp.GetOperations()[0].GetOperationId() != validUUIDv7() {
		t.Fatalf("operations = %#v, want only running repair operation", resp.GetOperations())
	}
}

func TestAdminServerCancelOperationUpdatesDurableStore(t *testing.T) {
	store := openTestOperationStore(t)
	operation := testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	resp, err := client.CancelOperation(context.Background(), &adminv1.CancelOperationRequest{OperationId: validUUIDv7()})
	testutil.RequireNoErrorf(t, err, "cancel operation")
	if resp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_CANCELED || resp.GetOperation().GetFinishedAt() == nil {
		t.Fatalf("operation = %#v, want canceled with finished_at", resp.GetOperation())
	}
	stored, err := store.Get(validUUIDv7())
	testutil.RequireNoErrorf(t, err, "get stored operation")
	if stored.GetState() != adminv1.OperationState_OPERATION_STATE_CANCELED {
		t.Fatalf("stored state = %s, want canceled", stored.GetState())
	}
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	if len(events) != 1 ||
		events[0].GetEventType() != adminAuditEventOperationCanceled ||
		events[0].GetOperationId() != validUUIDv7() ||
		events[0].GetOperationType() != "repair" ||
		events[0].GetActorIdentity() != adminAuditActorIdentity ||
		events[0].GetOccurredAt() == nil {
		t.Fatalf("audit events = %#v, want operation_canceled event", events)
	}
}

func TestAdminServerWatchOperationStreamsDurableSnapshot(t *testing.T) {
	store := openTestOperationStore(t)
	operation := testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)
	operation.Progress = &adminv1.OperationProgress{WorkUnitsTotal: 10, WorkUnitsCompleted: 3, Message: "repairing"}
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	stream, err := client.WatchOperation(context.Background(), &adminv1.WatchOperationRequest{OperationId: validUUIDv7()})
	testutil.RequireNoErrorf(t, err, "watch operation")
	resp, err := stream.Recv()
	testutil.RequireNoErrorf(t, err, "recv watch snapshot")
	if resp.GetSequence() != 1 ||
		resp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_RUNNING ||
		resp.GetDelta().GetProgress().GetWorkUnitsCompleted() != 3 {
		t.Fatalf("watch response = %#v, want current operation snapshot", resp)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("second recv error = %v, want EOF", err)
	}
}

func TestAdminServerWatchOperationHonorsAfterSequence(t *testing.T) {
	store := openTestOperationStore(t)
	testutil.RequireNoErrorf(t, store.Put(testOperation(validUUIDv7(), "repair", adminv1.OperationState_OPERATION_STATE_RUNNING)), "put operation")
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	stream, err := client.WatchOperation(context.Background(), &adminv1.WatchOperationRequest{
		OperationId:   validUUIDv7(),
		AfterSequence: ptr(uint64(1)),
	})
	testutil.RequireNoErrorf(t, err, "watch operation")
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("recv error = %v, want EOF after already-seen sequence", err)
	}
}

func TestAdminServerWatchOperationReturnsNotFound(t *testing.T) {
	store := openTestOperationStore(t)
	client, cleanup := newAdminOperationClient(t, store)
	defer cleanup()

	stream, err := client.WatchOperation(context.Background(), &adminv1.WatchOperationRequest{OperationId: validUUIDv7()})
	testutil.RequireNoErrorf(t, err, "watch operation")
	_, err = stream.Recv()
	if code := status.Code(err); code != codes.NotFound {
		t.Fatalf("code = %s, want %s; err = %v", code, codes.NotFound, err)
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
	testutil.RequireNoErrorf(t, err, "plan restore")
	plan := planResp.GetPlan()
	requireRestorePlan(t, plan)
	_, err = store.GetPlan(plan.GetOperationPlanId())
	testutil.RequireNoErrorf(t, err, "get stored plan")

	startReq := &adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
		Metadata: map[string]string{
			"ticket": "INC-1",
		},
	}
	startResp, err := restoreClient.StartRestore(context.Background(), startReq)
	testutil.RequireNoErrorf(t, err, "start restore")
	operation := startResp.GetOperation()
	requireQueuedRestoreOperation(t, operation, plan)

	getResp, err := operationClient.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: validUUIDv7()})
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, getResp.GetOperation().GetOperationType(), "restore", "stored operation type")

	retryResp, err := restoreClient.StartRestore(context.Background(), startReq)
	testutil.RequireNoErrorf(t, err, "retry start restore")
	testutil.RequireEqualf(t, retryResp.GetOperation().GetRequestedAt().AsTime(), operation.GetRequestedAt().AsTime(), "idempotent retry requested_at")
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireRestoreStartedAuditEvent(t, events, startReq.GetOperationId())
}

func requireRestorePlan(t *testing.T, plan *adminv1.OperationPlan) {
	t.Helper()
	testutil.RequireTruef(t, plan.GetOperationPlanId() != "", "plan = %#v, want id", plan)
	testutil.RequireTruef(t, plan.GetPlanHash() != "", "plan = %#v, want hash", plan)
	testutil.RequireNotNilf(t, plan.GetExpiresAt(), "plan = %#v, want expires_at", plan)
	testutil.RequireEqualf(t, plan.GetMetadata()[adminOperationTypeMetadata], "restore", "plan operation type")
	testutil.RequireEqualf(t, len(plan.GetTargets()), 1, "plan target count")
}

func requireQueuedRestoreOperation(t *testing.T, operation *adminv1.Operation, plan *adminv1.OperationPlan) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetOperationType(), "restore", "operation type")
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "operation state")
	testutil.RequireEqualf(t, operation.GetMetadata()[adminOperationPlanIDMetadata], plan.GetOperationPlanId(), "operation plan id metadata")
	testutil.RequireEqualf(t, operation.GetMetadata()["ticket"], "INC-1", "operation ticket metadata")
	testutil.RequireEqualf(t, len(operation.GetTargets()), 1, "operation target count")
}

func requireRestoreStartedAuditEvent(t *testing.T, events []*adminv1.AuditEvent, operationID string) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 1, "audit event count")
	event := events[0]
	testutil.RequireEqualf(t, event.GetEventType(), adminAuditEventOperationStarted, "audit event type")
	testutil.RequireEqualf(t, event.GetOperationId(), operationID, "audit operation id")
	testutil.RequireEqualf(t, event.GetOperationType(), "restore", "audit operation type")
	testutil.RequireEqualf(t, event.GetActorIdentity(), adminAuditActorIdentity, "audit actor identity")
	testutil.RequireEqualf(t, event.GetMetadata()["ticket"], "INC-1", "audit ticket metadata")
	testutil.RequireEqualf(t, len(event.GetTargets()), 1, "audit target count")
}

func TestAdminServerPlanAndStartPrewarmUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	restoreClient, _, _, operationClient, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	pinUntil := timestamppb.New(time.Unix(900, 0).UTC())
	planResp, err := restoreClient.PlanPrewarm(context.Background(), &adminv1.PlanPrewarmRequest{
		Targets:  []*adminv1.Target{documentAdminTarget()},
		PinUntil: pinUntil,
		Metadata: map[string]string{
			"requested_by": "test",
		},
	})
	testutil.RequireNoErrorf(t, err, "plan prewarm")
	plan := planResp.GetPlan()
	requirePrewarmPlanMetadata(t, plan.GetMetadata())

	startReq := &adminv1.StartPrewarmRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
		Metadata: map[string]string{
			"ticket": "INC-2",
		},
	}
	startResp, err := restoreClient.StartPrewarm(context.Background(), startReq)
	testutil.RequireNoErrorf(t, err, "start prewarm")
	operation := startResp.GetOperation()
	requireQueuedPrewarmOperation(t, operation)
	getResp, err := operationClient.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: operation.GetOperationId()})
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, getResp.GetOperation().GetOperationType(), "prewarm", "stored operation type")
}

func requirePrewarmPlanMetadata(t *testing.T, metadata map[string]string) {
	t.Helper()
	testutil.RequireEqualf(t, metadata[adminOperationTypeMetadata], "prewarm", "prewarm plan operation type")
	testutil.RequireEqualf(t, metadata[adminOperationLaneMetadata], "planned-prewarm", "prewarm plan operation lane")
	testutil.RequireEqualf(t, metadata[adminBackendLaneMetadata], "restore", "prewarm plan backend lane")
	testutil.RequireTruef(t, metadata["scrap.pin_until"] != "", "prewarm plan pin metadata = %#v", metadata)
}

func requireQueuedPrewarmOperation(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetOperationType(), "prewarm", "operation type")
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "operation state")
	testutil.RequireEqualf(t, operation.GetMetadata()[adminOperationLaneMetadata], "planned-prewarm", "operation lane")
	testutil.RequireEqualf(t, operation.GetMetadata()[adminBackendLaneMetadata], "restore", "operation backend lane")
	testutil.RequireEqualf(t, operation.GetMetadata()["ticket"], "INC-2", "operation ticket")
}

func TestAdminServerMemberLifecycleWritesAuditEvents(t *testing.T) {
	store := openTestOperationStore(t)
	expected := &adminv1.StorageMember{
		StorageMemberId: "local",
		CellId:          "local",
		State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
		Cordoned:        true,
	}
	member := &fakeMemberApplication{cordoned: expected, uncordoned: proto.Clone(expected).(*adminv1.StorageMember)}
	member.uncordoned.Cordoned = false
	client, cleanup := newAdminMemberClientWithStore(t, member, store)
	defer cleanup()

	_, err := client.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		Reason:        "maintenance",
		OperationId:   "018f6d86-7a22-7abc-8def-123456789abc",
	})
	testutil.RequireNoErrorf(t, err, "cordon member")
	_, err = client.UncordonMember(context.Background(), &adminv1.UncordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		OperationId:   "018f6d86-7a22-7abc-8def-123456789abd",
	})
	testutil.RequireNoErrorf(t, err, "uncordon member")

	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireMemberLifecycleAuditEvents(t, events)
}

func requireMemberLifecycleAuditEvents(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 2, "member audit event count")
	requireMemberAuditEvent(t, events[0], adminAuditEventMemberCordoned, "cordon-member", "maintenance")
	requireMemberAuditEvent(t, events[1], adminAuditEventMemberUncordoned, "uncordon-member", "")
}

func requireMemberAuditEvent(t *testing.T, event *adminv1.AuditEvent, eventType, operationType, reason string) {
	t.Helper()
	testutil.RequireEqualf(t, event.GetEventType(), eventType, "member audit event type")
	testutil.RequireEqualf(t, event.GetOperationType(), operationType, "member audit operation type")
	testutil.RequireEqualf(t, event.GetMetadata()["reason"], reason, "member audit reason")
	testutil.RequireEqualf(t, len(event.GetTargets()), 1, "member audit target count")
	testutil.RequireEqualf(t, event.GetTargets()[0].GetStorageMember().GetStorageMemberId(), "local", "member audit target")
}

func TestAdminServerStartRestoreRejectsMismatchedPlanHash(t *testing.T) {
	store := openTestOperationStore(t)
	restoreClient, _, _, _, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
	})
	testutil.RequireNoErrorf(t, err, "plan restore")
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
	testutil.RequireNoErrorf(t, err, "plan repair")
	startResp, err := repairClient.StartRepair(context.Background(), &adminv1.StartRepairRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: planResp.GetPlan().GetOperationPlanId(),
		PlanHash:        planResp.GetPlan().GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start repair")
	if startResp.GetOperation().GetOperationType() != "repair" ||
		startResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued repair", startResp.GetOperation())
	}
}

func TestAdminServerPlanAndStartScrubUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	_, repairClient, _, _, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := repairClient.PlanScrub(context.Background(), &adminv1.PlanScrubRequest{
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Transaction{
					Transaction: &adminv1.TransactionTarget{
						TenantId:      "tenant",
						TransactionId: "txn",
					},
				},
			},
		},
		DryRun: true,
	})
	testutil.RequireNoErrorf(t, err, "plan scrub")
	startResp, err := repairClient.StartScrub(context.Background(), &adminv1.StartScrubRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: planResp.GetPlan().GetOperationPlanId(),
		PlanHash:        planResp.GetPlan().GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start scrub")
	if startResp.GetOperation().GetOperationType() != "scrub" ||
		!startResp.GetOperation().GetDryRun() ||
		startResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued scrub dry-run", startResp.GetOperation())
	}
}

func TestAdminServerPlanAndStartDrainUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	memberClient, cleanup := newAdminMemberClientWithStore(t, nil, store)
	defer cleanup()

	planResp, err := memberClient.PlanDrain(context.Background(), &adminv1.PlanDrainRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		DryRun:        true,
	})
	testutil.RequireNoErrorf(t, err, "plan drain")
	plan := planResp.GetPlan()
	if plan.GetMetadata()[adminOperationTypeMetadata] != "drain" ||
		len(plan.GetTargets()) != 1 ||
		plan.GetTargets()[0].GetStorageMember().GetStorageMemberId() != "local" {
		t.Fatalf("plan = %#v, want drain plan for local member", plan)
	}

	startResp, err := memberClient.StartDrain(context.Background(), &adminv1.StartDrainRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ac1",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start drain")
	if startResp.GetOperation().GetOperationType() != "drain" ||
		!startResp.GetOperation().GetDryRun() ||
		startResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued drain dry-run", startResp.GetOperation())
	}
}

func TestAdminServerPlanAndStartTombstoneUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	_, _, lifecycleClient, _, cleanup := newAdminWorkflowClients(t, store)
	defer cleanup()

	planResp, err := lifecycleClient.PlanTombstone(context.Background(), &adminv1.PlanTombstoneRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
	})
	testutil.RequireNoErrorf(t, err, "plan tombstone")
	plan := planResp.GetPlan()
	if plan.GetMetadata()[adminOperationTypeMetadata] != "tombstone" ||
		len(plan.GetTargets()) != 1 {
		t.Fatalf("plan = %#v, want tombstone plan", plan)
	}

	startResp, err := lifecycleClient.StartTombstone(context.Background(), &adminv1.StartTombstoneRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ac2",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start tombstone")
	if startResp.GetOperation().GetOperationType() != "tombstone" ||
		startResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued tombstone", startResp.GetOperation())
	}
}

func TestAdminServerPlanAndStartKeyRotationQueuesRewrapOperation(t *testing.T) {
	store := openTestOperationStore(t)
	keyClient, cleanup := newAdminKeyClient(t, store)
	defer cleanup()

	planResp, err := keyClient.PlanKeyRotation(context.Background(), &adminv1.PlanKeyRotationRequest{
		Targets:          []*adminv1.Target{blockAdminTarget()},
		DestinationKeyId: "transit/backend-v2",
		DryRun:           true,
	})
	testutil.RequireNoErrorf(t, err, "plan key rotation")
	plan := planResp.GetPlan()
	if plan.GetMetadata()[adminOperationTypeMetadata] != "rewrap" ||
		plan.GetMetadata()[adminDestinationKeyIDMetadata] != "transit/backend-v2" ||
		plan.GetMetadata()[adminBackendLaneMetadata] != "rewrap" {
		t.Fatalf("plan metadata = %#v, want rewrap key rotation plan", plan.GetMetadata())
	}

	startResp, err := keyClient.StartKeyRotation(context.Background(), &adminv1.StartKeyRotationRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ac3",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start key rotation")
	if startResp.GetOperation().GetOperationType() != "rewrap" ||
		!startResp.GetOperation().GetDryRun() ||
		startResp.GetOperation().GetMetadata()[adminDestinationKeyIDMetadata] != "transit/backend-v2" {
		t.Fatalf("operation = %#v, want queued rewrap operation", startResp.GetOperation())
	}
}

func TestAdminServerPlanAndStartCapacityOverrideUseDurableOperationStore(t *testing.T) {
	store := openTestOperationStore(t)
	capacityClient, cleanup := newAdminCapacityClient(t, store)
	defer cleanup()

	expiresAt := timestamppb.New(time.Unix(1200, 0).UTC())
	planResp, err := capacityClient.PlanCapacityOverride(context.Background(), &adminv1.PlanCapacityOverrideRequest{
		CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
		ExpiresAt:       expiresAt,
		Reason:          "incident INC-42",
		DryRun:          true,
	})
	testutil.RequireNoErrorf(t, err, "plan capacity override")
	plan := planResp.GetPlan()
	if plan.GetMetadata()[adminOperationTypeMetadata] != "capacity-override" ||
		plan.GetMetadata()[adminCapacityProfileIDMetadata] != "production-a" ||
		plan.GetMetadata()[adminCapacityOverrideReasonMetadata] != "incident INC-42" ||
		len(plan.GetWarnings()) != 1 ||
		len(plan.GetTargets()) != 1 ||
		plan.GetTargets()[0].GetCapacityProfile().GetCapacityProfileId() != "production-a" {
		t.Fatalf("plan = %#v, want bounded capacity override plan", plan)
	}

	startResp, err := capacityClient.StartCapacityOverride(context.Background(), &adminv1.StartCapacityOverrideRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ac4",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start capacity override")
	if startResp.GetOperation().GetOperationType() != "capacity-override" ||
		!startResp.GetOperation().GetDryRun() ||
		startResp.GetOperation().GetMetadata()[adminCapacityProfileIDMetadata] != "production-a" {
		t.Fatalf("operation = %#v, want queued capacity override operation", startResp.GetOperation())
	}
}

func TestAdminServerCriticalWorkflowStartsWriteSanitizedAuditEvents(t *testing.T) {
	store := openTestOperationStore(t)
	_, _, lifecycleClient, _, lifecycleCleanup := newAdminWorkflowClients(t, store)
	defer lifecycleCleanup()
	keyClient, keyCleanup := newAdminKeyClient(t, store)
	defer keyCleanup()
	capacityClient, capacityCleanup := newAdminCapacityClient(t, store)
	defer capacityCleanup()

	tombstonePlan, err := lifecycleClient.PlanTombstone(context.Background(), &adminv1.PlanTombstoneRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
		Metadata: map[string]string{
			"ticket":           "INC-1",
			"plaintext_secret": "do-not-audit",
		},
	})
	testutil.RequireNoErrorf(t, err, "plan tombstone")
	_, err = lifecycleClient.StartTombstone(context.Background(), &adminv1.StartTombstoneRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ad1",
		OperationPlanId: tombstonePlan.GetPlan().GetOperationPlanId(),
		PlanHash:        tombstonePlan.GetPlan().GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start tombstone")

	keyPlan, err := keyClient.PlanKeyRotation(context.Background(), &adminv1.PlanKeyRotationRequest{
		Targets:          []*adminv1.Target{blockAdminTarget()},
		DestinationKeyId: "transit/backend-v2",
	})
	testutil.RequireNoErrorf(t, err, "plan key rotation")
	_, err = keyClient.StartKeyRotation(context.Background(), &adminv1.StartKeyRotationRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ad2",
		OperationPlanId: keyPlan.GetPlan().GetOperationPlanId(),
		PlanHash:        keyPlan.GetPlan().GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start key rotation")

	capacityPlan, err := capacityClient.PlanCapacityOverride(context.Background(), &adminv1.PlanCapacityOverrideRequest{
		CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
		ExpiresAt:       timestamppb.New(time.Unix(1200, 0).UTC()),
		Reason:          "incident INC-42",
		DryRun:          true,
	})
	testutil.RequireNoErrorf(t, err, "plan capacity override")
	_, err = capacityClient.StartCapacityOverride(context.Background(), &adminv1.StartCapacityOverrideRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789ad3",
		OperationPlanId: capacityPlan.GetPlan().GetOperationPlanId(),
		PlanHash:        capacityPlan.GetPlan().GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start capacity override")

	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireCriticalWorkflowAuditEvents(t, events)
}

func requireCriticalWorkflowAuditEvents(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 3, "critical workflow audit event count")
	byType := make(map[string]*adminv1.AuditEvent, len(events))
	for _, event := range events {
		testutil.RequireEqualf(t, event.GetEventType(), adminAuditEventOperationStarted, "audit event type")
		byType[event.GetOperationType()] = event
	}
	testutil.RequireEqualf(t, byType["tombstone"].GetMetadata()["ticket"], "INC-1", "tombstone audit ticket")
	testutil.RequireEqualf(t, byType["tombstone"].GetMetadata()["plaintext_secret"], "", "tombstone audit sanitized secret")
	testutil.RequireEqualf(t, byType["rewrap"].GetMetadata()[adminDestinationKeyIDMetadata], "transit/backend-v2", "rewrap audit destination key")
	testutil.RequireEqualf(t, byType["capacity-override"].GetMetadata()[adminCapacityOverrideReasonMetadata], "incident INC-42", "capacity audit reason")
}

func TestAdminServerStartedOperationStatusSurvivesStoreReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := operations.Open(dir)
	testutil.RequireNoErrorf(t, err, "open operation store")
	restoreClient, _, _, _, cleanup := newAdminWorkflowClients(t, store)

	planResp, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{documentAdminTarget()},
	})
	testutil.RequireNoErrorf(t, err, "plan restore")
	operationID := "018f6d86-7a22-7abc-8def-123456789ac5"
	if _, err := restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     operationID,
		OperationPlanId: planResp.GetPlan().GetOperationPlanId(),
		PlanHash:        planResp.GetPlan().GetPlanHash(),
	}); err != nil {
		t.Fatalf("start restore: %v", err)
	}
	cleanup()
	testutil.RequireNoErrorf(t, store.Close(), "close operation store")

	reopened, err := operations.Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen operation store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	operationClient, operationCleanup := newAdminOperationClient(t, reopened)
	defer operationCleanup()

	getResp, err := operationClient.GetOperation(context.Background(), &adminv1.GetOperationRequest{OperationId: operationID})
	testutil.RequireNoErrorf(t, err, "get operation after reopen")
	if getResp.GetOperation().GetOperationType() != "restore" ||
		getResp.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("operation = %#v, want queued restore after reopen", getResp.GetOperation())
	}
	listResp, err := operationClient.ListOperations(context.Background(), &adminv1.ListOperationsRequest{
		OperationType: ptr("restore"),
		States:        []adminv1.OperationState{adminv1.OperationState_OPERATION_STATE_QUEUED},
	})
	testutil.RequireNoErrorf(t, err, "list operations after reopen")
	if len(listResp.GetOperations()) != 1 || listResp.GetOperations()[0].GetOperationId() != operationID {
		t.Fatalf("operations = %#v, want restored operation visible after reopen", listResp.GetOperations())
	}
}

func TestAdminServerPlanRecoveryStartsDRSuboperations(t *testing.T) {
	store := openTestOperationStore(t)
	drClient, cleanup := newAdminDisasterRecoveryClient(t, nil, store)
	defer cleanup()

	planResp, err := drClient.PlanRecovery(context.Background(), &adminv1.PlanRecoveryRequest{
		Targets: []*adminv1.Target{snapshotAdminTarget()},
		Metadata: map[string]string{
			"reason": "quarterly-drill",
		},
	})
	testutil.RequireNoErrorf(t, err, "plan recovery")
	plan := planResp.GetPlan()
	if plan.GetMetadata()[adminOperationTypeMetadata] != "recovery" ||
		plan.GetMetadata()["reason"] != "quarterly-drill" ||
		len(plan.GetTargets()) != 1 {
		t.Fatalf("plan = %#v, want recovery plan", plan)
	}

	metadataRestore, err := drClient.StartMetadataRestore(context.Background(), &adminv1.StartMetadataRestoreRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abc",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start metadata restore")
	if metadataRestore.GetOperation().GetOperationType() != "metadata-restore" ||
		metadataRestore.GetOperation().GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED {
		t.Fatalf("metadata restore operation = %#v, want queued metadata restore", metadataRestore.GetOperation())
	}

	copyVerify, err := drClient.StartCopyVerify(context.Background(), &adminv1.StartCopyVerifyRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abd",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start copy verify")
	if copyVerify.GetOperation().GetOperationType() != "copy-verify" {
		t.Fatalf("copy verify operation = %#v, want copy-verify", copyVerify.GetOperation())
	}

	drill, err := drClient.StartDRDrill(context.Background(), &adminv1.StartDRDrillRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abe",
		OperationPlanId: plan.GetOperationPlanId(),
		PlanHash:        plan.GetPlanHash(),
	})
	testutil.RequireNoErrorf(t, err, "start dr drill")
	if drill.GetOperation().GetOperationType() != "dr-drill" {
		t.Fatalf("drill operation = %#v, want dr-drill", drill.GetOperation())
	}
}

func TestAdminServerGetRepairQueueUsesRepairApplication(t *testing.T) {
	expected := []*adminv1.RepairQueueItem{
		{
			Target:     documentAdminTarget(),
			Reason:     "quarantined local reference",
			DetectedAt: timestamppb.New(time.Unix(400, 0).UTC()),
		},
	}
	repair := &fakeRepairApplication{items: expected}
	client, cleanup := newAdminRepairClient(t, repair)
	defer cleanup()

	resp, err := client.GetRepairQueue(context.Background(), &adminv1.GetRepairQueueRequest{ShardId: ptr("local")})
	testutil.RequireNoErrorf(t, err, "get repair queue")
	if repair.gotShardID != "local" {
		t.Fatalf("repair got shard = %q, want local", repair.gotShardID)
	}
	if len(resp.GetItems()) != 1 || !proto.Equal(resp.GetItems()[0], expected[0]) {
		t.Fatalf("repair queue = %#v, want %#v", resp.GetItems(), expected)
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewRestoreServiceClient(conn), adminv1.NewMemberServiceClient(conn), cleanup
}

func newAdminInspectClient(t *testing.T, inspect InspectApplication) (adminv1.InspectServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithInspectApplication(inspect)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewInspectServiceClient(conn), cleanup
}

func newAdminRepairClient(t *testing.T, repair RepairApplication) (adminv1.RepairServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithRepairApplication(repair)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewRepairServiceClient(conn), cleanup
}

func newAdminMemberClient(t *testing.T, member MemberApplication) (adminv1.MemberServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithMemberApplication(member)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewMemberServiceClient(conn), cleanup
}

func newAdminMemberClientWithStore(t *testing.T, member MemberApplication, store *operations.Store) (adminv1.MemberServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithMemberApplication(member), WithOperationStore(store)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewMemberServiceClient(conn), cleanup
}

func newAdminDisasterRecoveryClient(t *testing.T, dr DisasterRecoveryApplication, store *operations.Store) (adminv1.DisasterRecoveryServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer(WithDisasterRecoveryApplication(dr), WithOperationStore(store)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewDisasterRecoveryServiceClient(conn), cleanup
}

func newAdminKeyClient(t *testing.T, store *operations.Store) (adminv1.KeyServiceClient, func()) {
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewKeyServiceClient(conn), cleanup
}

func newAdminCapacityClient(t *testing.T, store *operations.Store) (adminv1.CapacityServiceClient, func()) {
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewCapacityServiceClient(conn), cleanup
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
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
	testutil.RequireNoErrorf(t, err, "open operation store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close operation store")
	})
	return store
}

func testOperation(operationID, operationType string, state adminv1.OperationState) *adminv1.Operation {
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

func snapshotAdminTarget() *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Snapshot{
			Snapshot: &adminv1.SnapshotTarget{
				ShardId:    ptr("local"),
				SnapshotId: "snapshot-1",
			},
		},
	}
}

func ptr[T any](value T) *T {
	return &value
}

type fakeInspectApplication struct {
	got                  identity.Document
	gotBlock             BlockTarget
	gotShard             string
	gotMember            string
	gotCapacityProfileID string
	summary              *adminv1.ClusterSummary
	shard                *adminv1.Shard
	document             *adminv1.AdminDocument
	block                *adminv1.Block
	member               *adminv1.StorageMember
	runway               *adminv1.CapacityRunway
}

func (f *fakeInspectApplication) GetAdminClusterSummary(context.Context) (*adminv1.ClusterSummary, error) {
	return f.summary, nil
}

func (f *fakeInspectApplication) GetAdminShard(_ context.Context, shardID string) (*adminv1.Shard, error) {
	f.gotShard = shardID
	return f.shard, nil
}

func (f *fakeInspectApplication) GetAdminDocument(_ context.Context, doc identity.Document) (*adminv1.AdminDocument, error) {
	f.got = doc
	return f.document, nil
}

func (f *fakeInspectApplication) GetAdminBlock(_ context.Context, target BlockTarget) (*adminv1.Block, error) {
	f.gotBlock = target
	return f.block, nil
}

func (f *fakeInspectApplication) GetAdminMember(_ context.Context, memberID string) (*adminv1.StorageMember, error) {
	f.gotMember = memberID
	return f.member, nil
}

func (f *fakeInspectApplication) GetAdminCapacityRunway(_ context.Context, capacityProfileID string) (*adminv1.CapacityRunway, error) {
	f.gotCapacityProfileID = capacityProfileID
	return f.runway, nil
}

type fakeRepairApplication struct {
	gotShardID string
	items      []*adminv1.RepairQueueItem
}

func (f *fakeRepairApplication) GetRepairQueue(_ context.Context, shardID string) ([]*adminv1.RepairQueueItem, error) {
	f.gotShardID = shardID
	return f.items, nil
}

type fakeMemberApplication struct {
	cordonReq      MemberMutationRequest
	uncordonReq    MemberMutationRequest
	safetyMemberID string
	cordoned       *adminv1.StorageMember
	uncordoned     *adminv1.StorageMember
	safety         *adminv1.EvictionSafety
}

func (f *fakeMemberApplication) CordonMember(_ context.Context, req MemberMutationRequest) (*adminv1.StorageMember, error) {
	f.cordonReq = req
	return f.cordoned, nil
}

func (f *fakeMemberApplication) UncordonMember(_ context.Context, req MemberMutationRequest) (*adminv1.StorageMember, error) {
	f.uncordonReq = req
	return f.uncordoned, nil
}

func (f *fakeMemberApplication) GetEvictionSafety(_ context.Context, memberID string) (*adminv1.EvictionSafety, error) {
	f.safetyMemberID = memberID
	return f.safety, nil
}

type fakeDisasterRecoveryApplication struct {
	calls     int
	readiness *adminv1.RecoveryReadiness
}

func (f *fakeDisasterRecoveryApplication) GetRecoveryReadiness(context.Context) (*adminv1.RecoveryReadiness, error) {
	f.calls++
	return f.readiness, nil
}
