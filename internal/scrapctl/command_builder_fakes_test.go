package scrapctl

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func registerCommandBuilderFake(server *grpc.Server, fake *commandBuilderAdminServer) {
	adminv1.RegisterInspectServiceServer(server, fake)
	adminv1.RegisterOperationServiceServer(server, fake)
	adminv1.RegisterRestoreServiceServer(server, fake)
	adminv1.RegisterRepairServiceServer(server, fake)
	adminv1.RegisterMemberServiceServer(server, fake)
	adminv1.RegisterLifecycleServiceServer(server, fake)
	adminv1.RegisterKeyServiceServer(server, fake)
	adminv1.RegisterCapacityServiceServer(server, fake)
	adminv1.RegisterDisasterRecoveryServiceServer(server, fake)
}

type commandBuilderAdminServer struct {
	adminv1.UnimplementedInspectServiceServer
	adminv1.UnimplementedOperationServiceServer
	adminv1.UnimplementedRestoreServiceServer
	adminv1.UnimplementedRepairServiceServer
	adminv1.UnimplementedMemberServiceServer
	adminv1.UnimplementedLifecycleServiceServer
	adminv1.UnimplementedKeyServiceServer
	adminv1.UnimplementedCapacityServiceServer
	adminv1.UnimplementedDisasterRecoveryServiceServer

	summaryCalls           int
	recoveryReadinessCalls int
	getShardReq            *adminv1.GetShardRequest
	getDocumentReq         *adminv1.GetDocumentRequest
	getBlockReq            *adminv1.GetBlockRequest
	getMemberReq           *adminv1.GetMemberRequest
	getCapacityRunwayReq   *adminv1.GetCapacityRunwayRequest
	getRepairQueueReq      *adminv1.GetRepairQueueRequest
	listOperationsReq      *adminv1.ListOperationsRequest
	cancelOperationReq     *adminv1.CancelOperationRequest
	cordonMemberReq        *adminv1.CordonMemberRequest
	uncordonMemberReq      *adminv1.UncordonMemberRequest
	evictionSafetyReq      *adminv1.GetEvictionSafetyRequest
	lastPlan               commandBuilderPlanRecord
	lastStart              commandBuilderStartRecord
}

type commandBuilderPlanRecord struct {
	kind              string
	targets           []*adminv1.Target
	dryRun            bool
	pinUntil          *timestamppb.Timestamp
	metadata          map[string]string
	memberID          string
	destinationKeyID  string
	capacityProfileID string
	reason            string
	expiresAt         *timestamppb.Timestamp
}

type commandBuilderStartRecord struct {
	kind        string
	planID      string
	planHash    string
	operationID string
	metadata    map[string]string
}

func (s *commandBuilderAdminServer) GetClusterSummary(context.Context, *adminv1.GetClusterSummaryRequest) (*adminv1.GetClusterSummaryResponse, error) {
	s.summaryCalls++
	return &adminv1.GetClusterSummaryResponse{Summary: &adminv1.ClusterSummary{ShardCount: 2}}, nil
}

func (s *commandBuilderAdminServer) GetShard(_ context.Context, req *adminv1.GetShardRequest) (*adminv1.GetShardResponse, error) {
	s.getShardReq = req
	return &adminv1.GetShardResponse{Shard: &adminv1.Shard{ShardId: req.GetShardId(), LeaderMemberId: "member-a"}}, nil
}

func (s *commandBuilderAdminServer) GetDocument(_ context.Context, req *adminv1.GetDocumentRequest) (*adminv1.GetDocumentResponse, error) {
	s.getDocumentReq = req
	return &adminv1.GetDocumentResponse{Document: &adminv1.AdminDocument{Document: req.GetDocument(), ShardId: "shard-a", Length: 3}}, nil
}

func (s *commandBuilderAdminServer) GetBlock(_ context.Context, req *adminv1.GetBlockRequest) (*adminv1.GetBlockResponse, error) {
	s.getBlockReq = req
	return &adminv1.GetBlockResponse{Block: &adminv1.Block{ShardId: req.GetBlock().GetShardId(), BlockId: req.GetBlock().GetBlockId(), Length: 3}}, nil
}

func (s *commandBuilderAdminServer) GetMember(_ context.Context, req *adminv1.GetMemberRequest) (*adminv1.GetMemberResponse, error) {
	s.getMemberReq = req
	return &adminv1.GetMemberResponse{StorageMember: storageMember(req.GetStorageMember().GetStorageMemberId())}, nil
}

func (s *commandBuilderAdminServer) GetCapacityRunway(_ context.Context, req *adminv1.GetCapacityRunwayRequest) (*adminv1.GetCapacityRunwayResponse, error) {
	s.getCapacityRunwayReq = req
	return &adminv1.GetCapacityRunwayResponse{Runway: &adminv1.CapacityRunway{CapacityProfileId: req.GetCapacityProfileId(), RunwayDays: 7}}, nil
}

func (s *commandBuilderAdminServer) GetRepairQueue(_ context.Context, req *adminv1.GetRepairQueueRequest) (*adminv1.GetRepairQueueResponse, error) {
	s.getRepairQueueReq = req
	return &adminv1.GetRepairQueueResponse{
		Items: []*adminv1.RepairQueueItem{
			{Target: shardTarget("shard-a"), Reason: "checksum"},
		},
		NextPageToken: "next-page",
	}, nil
}

func (s *commandBuilderAdminServer) GetRecoveryReadiness(context.Context, *adminv1.GetRecoveryReadinessRequest) (*adminv1.GetRecoveryReadinessResponse, error) {
	s.recoveryReadinessCalls++
	return &adminv1.GetRecoveryReadinessResponse{Readiness: &adminv1.RecoveryReadiness{
		Ready:                        true,
		LatestRestorableCheckpointAt: testTimestamp(),
	}}, nil
}

func (s *commandBuilderAdminServer) PlanRestore(_ context.Context, req *adminv1.PlanRestoreRequest) (*adminv1.PlanRestoreResponse, error) {
	s.recordTargetPlan("restore", req.GetTargets(), req.GetDryRun(), nil, req.GetMetadata())
	return &adminv1.PlanRestoreResponse{Plan: operationPlan("restore", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanPrewarm(_ context.Context, req *adminv1.PlanPrewarmRequest) (*adminv1.PlanPrewarmResponse, error) {
	s.recordTargetPlan("prewarm", req.GetTargets(), req.GetDryRun(), req.GetPinUntil(), req.GetMetadata())
	return &adminv1.PlanPrewarmResponse{Plan: operationPlan("prewarm", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanRepair(_ context.Context, req *adminv1.PlanRepairRequest) (*adminv1.PlanRepairResponse, error) {
	s.recordTargetPlan("repair", req.GetTargets(), req.GetDryRun(), nil, req.GetMetadata())
	return &adminv1.PlanRepairResponse{Plan: operationPlan("repair", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanScrub(_ context.Context, req *adminv1.PlanScrubRequest) (*adminv1.PlanScrubResponse, error) {
	s.recordTargetPlan("scrub", req.GetTargets(), req.GetDryRun(), nil, req.GetMetadata())
	return &adminv1.PlanScrubResponse{Plan: operationPlan("scrub", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanTombstone(_ context.Context, req *adminv1.PlanTombstoneRequest) (*adminv1.PlanTombstoneResponse, error) {
	s.recordTargetPlan("tombstone", req.GetTargets(), req.GetDryRun(), nil, req.GetMetadata())
	return &adminv1.PlanTombstoneResponse{Plan: operationPlan("tombstone", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanRecovery(_ context.Context, req *adminv1.PlanRecoveryRequest) (*adminv1.PlanRecoveryResponse, error) {
	s.recordTargetPlan("recovery", req.GetTargets(), req.GetDryRun(), nil, req.GetMetadata())
	return &adminv1.PlanRecoveryResponse{Plan: operationPlan("recovery", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanDrain(_ context.Context, req *adminv1.PlanDrainRequest) (*adminv1.PlanDrainResponse, error) {
	s.lastPlan = commandBuilderPlanRecord{
		kind:     "drain",
		memberID: req.GetStorageMember().GetStorageMemberId(),
		dryRun:   req.GetDryRun(),
		metadata: req.GetMetadata(),
	}
	return &adminv1.PlanDrainResponse{Plan: operationPlan("drain", nil, req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanKeyRotation(_ context.Context, req *adminv1.PlanKeyRotationRequest) (*adminv1.PlanKeyRotationResponse, error) {
	s.lastPlan = commandBuilderPlanRecord{
		kind:             "key-rotation",
		targets:          req.GetTargets(),
		destinationKeyID: req.GetDestinationKeyId(),
		dryRun:           req.GetDryRun(),
		metadata:         req.GetMetadata(),
	}
	return &adminv1.PlanKeyRotationResponse{Plan: operationPlan("key-rotation", req.GetTargets(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) PlanCapacityOverride(_ context.Context, req *adminv1.PlanCapacityOverrideRequest) (*adminv1.PlanCapacityOverrideResponse, error) {
	s.lastPlan = commandBuilderPlanRecord{
		kind:              "capacity-override",
		capacityProfileID: req.GetCapacityProfile().GetCapacityProfileId(),
		expiresAt:         req.GetExpiresAt(),
		reason:            req.GetReason(),
		dryRun:            req.GetDryRun(),
		metadata:          req.GetMetadata(),
	}
	return &adminv1.PlanCapacityOverrideResponse{Plan: operationPlan("capacity-override", nil, req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartRestore(_ context.Context, req *adminv1.StartRestoreRequest) (*adminv1.StartRestoreResponse, error) {
	s.recordStart("restore", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartRestoreResponse{Operation: operation("restore", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartPrewarm(_ context.Context, req *adminv1.StartPrewarmRequest) (*adminv1.StartPrewarmResponse, error) {
	s.recordStart("prewarm", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartPrewarmResponse{Operation: operation("prewarm", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartRepair(_ context.Context, req *adminv1.StartRepairRequest) (*adminv1.StartRepairResponse, error) {
	s.recordStart("repair", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartRepairResponse{Operation: operation("repair", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartScrub(_ context.Context, req *adminv1.StartScrubRequest) (*adminv1.StartScrubResponse, error) {
	s.recordStart("scrub", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartScrubResponse{Operation: operation("scrub", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartDrain(_ context.Context, req *adminv1.StartDrainRequest) (*adminv1.StartDrainResponse, error) {
	s.recordStart("drain", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartDrainResponse{Operation: operation("drain", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartTombstone(_ context.Context, req *adminv1.StartTombstoneRequest) (*adminv1.StartTombstoneResponse, error) {
	s.recordStart("tombstone", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartTombstoneResponse{Operation: operation("tombstone", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartKeyRotation(_ context.Context, req *adminv1.StartKeyRotationRequest) (*adminv1.StartKeyRotationResponse, error) {
	s.recordStart("key-rotation", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartKeyRotationResponse{Operation: operation("key-rotation", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartCapacityOverride(_ context.Context, req *adminv1.StartCapacityOverrideRequest) (*adminv1.StartCapacityOverrideResponse, error) {
	s.recordStart("capacity-override", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartCapacityOverrideResponse{Operation: operation("capacity-override", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartMetadataRestore(_ context.Context, req *adminv1.StartMetadataRestoreRequest) (*adminv1.StartMetadataRestoreResponse, error) {
	s.recordStart("metadata-restore", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartMetadataRestoreResponse{Operation: operation("metadata-restore", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartCopyVerify(_ context.Context, req *adminv1.StartCopyVerifyRequest) (*adminv1.StartCopyVerifyResponse, error) {
	s.recordStart("copy-verify", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartCopyVerifyResponse{Operation: operation("copy-verify", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) StartDRDrill(_ context.Context, req *adminv1.StartDRDrillRequest) (*adminv1.StartDRDrillResponse, error) {
	s.recordStart("dr-drill", req.GetOperationPlanId(), req.GetPlanHash(), req.GetOperationId(), req.GetMetadata())
	return &adminv1.StartDRDrillResponse{Operation: operation("dr-drill", req.GetOperationId(), req.GetMetadata())}, nil
}

func (s *commandBuilderAdminServer) ListOperations(_ context.Context, req *adminv1.ListOperationsRequest) (*adminv1.ListOperationsResponse, error) {
	s.listOperationsReq = req
	return &adminv1.ListOperationsResponse{
		Operations:    []*adminv1.Operation{operation("repair", "op-list", nil)},
		NextPageToken: "next-page",
	}, nil
}

func (s *commandBuilderAdminServer) CancelOperation(_ context.Context, req *adminv1.CancelOperationRequest) (*adminv1.CancelOperationResponse, error) {
	s.cancelOperationReq = req
	return &adminv1.CancelOperationResponse{Operation: operation("cancel", req.GetOperationId(), nil)}, nil
}

func (s *commandBuilderAdminServer) CordonMember(_ context.Context, req *adminv1.CordonMemberRequest) (*adminv1.CordonMemberResponse, error) {
	s.cordonMemberReq = req
	return &adminv1.CordonMemberResponse{StorageMember: storageMember(req.GetStorageMember().GetStorageMemberId())}, nil
}

func (s *commandBuilderAdminServer) UncordonMember(_ context.Context, req *adminv1.UncordonMemberRequest) (*adminv1.UncordonMemberResponse, error) {
	s.uncordonMemberReq = req
	return &adminv1.UncordonMemberResponse{StorageMember: storageMember(req.GetStorageMember().GetStorageMemberId())}, nil
}

func (s *commandBuilderAdminServer) GetEvictionSafety(_ context.Context, req *adminv1.GetEvictionSafetyRequest) (*adminv1.GetEvictionSafetyResponse, error) {
	s.evictionSafetyReq = req
	return &adminv1.GetEvictionSafetyResponse{Safety: &adminv1.EvictionSafety{
		StorageMember: req.GetStorageMember(),
		SafeToEvict:   true,
	}}, nil
}

func (s *commandBuilderAdminServer) recordTargetPlan(kind string, targets []*adminv1.Target, dryRun bool, pinUntil *timestamppb.Timestamp, metadata map[string]string) {
	s.lastPlan = commandBuilderPlanRecord{
		kind:     kind,
		targets:  targets,
		dryRun:   dryRun,
		pinUntil: pinUntil,
		metadata: metadata,
	}
}

func (s *commandBuilderAdminServer) recordStart(kind, planID, planHash, operationID string, metadata map[string]string) {
	s.lastStart = commandBuilderStartRecord{
		kind:        kind,
		planID:      planID,
		planHash:    planHash,
		operationID: operationID,
		metadata:    metadata,
	}
}

func requirePlanRecord(t *testing.T, record commandBuilderPlanRecord, kind, metadataKey, metadataValue string) {
	t.Helper()

	testutil.RequireEqualf(t, record.kind, kind, "plan kind")
	if metadataKey != "" {
		testutil.RequireEqualf(t, record.metadata[metadataKey], metadataValue, "plan metadata")
	}
}

func requireTargetKind(t *testing.T, target *adminv1.Target, want string) {
	t.Helper()

	var got string
	switch target.GetTarget().(type) {
	case *adminv1.Target_Document:
		got = "document"
	case *adminv1.Target_Transaction:
		got = "transaction"
	case *adminv1.Target_Block:
		got = "block"
	case *adminv1.Target_Shard:
		got = "shard"
	case *adminv1.Target_StorageMember:
		got = "storage_member"
	case *adminv1.Target_Snapshot:
		got = "snapshot"
	case *adminv1.Target_CapacityProfile:
		got = "capacity_profile"
	default:
		got = "<nil>"
	}
	testutil.RequireEqualf(t, got, want, "target kind")
}

func operationPlan(kind string, targets []*adminv1.Target, metadata map[string]string) *adminv1.OperationPlan {
	return &adminv1.OperationPlan{
		OperationPlanId: "plan-" + kind,
		PlanHash:        "hash-" + kind,
		ExpiresAt:       testTimestamp(),
		Targets:         targets,
		Metadata:        metadata,
	}
}

func operation(kind, id string, metadata map[string]string) *adminv1.Operation {
	return &adminv1.Operation{
		OperationId:   id,
		OperationType: kind,
		State:         adminv1.OperationState_OPERATION_STATE_QUEUED,
		Metadata:      metadata,
	}
}

func storageMember(id string) *adminv1.StorageMember {
	return &adminv1.StorageMember{
		StorageMemberId: id,
		State:           adminv1.MemberState_MEMBER_STATE_ONLINE,
		LastSeenAt:      testTimestamp(),
	}
}

func shardTarget(id string) *adminv1.Target {
	return &adminv1.Target{Target: &adminv1.Target_Shard{Shard: &adminv1.ShardTarget{ShardId: id}}}
}

func testTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Unix(100, 0).UTC())
}
