package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AdminServer struct {
	inspect    InspectApplication
	repair     RepairApplication
	member     MemberApplication
	dr         DisasterRecoveryApplication
	operations *operations.Store
	now        func() time.Time
}

type InspectApplication interface {
	GetAdminClusterSummary(context.Context) (*adminv1.ClusterSummary, error)
	GetAdminShard(context.Context, string) (*adminv1.Shard, error)
	GetAdminDocument(context.Context, identity.Document) (*adminv1.AdminDocument, error)
	GetAdminBlock(context.Context, BlockTarget) (*adminv1.Block, error)
	GetAdminMember(context.Context, string) (*adminv1.StorageMember, error)
	GetAdminCapacityRunway(context.Context, string) (*adminv1.CapacityRunway, error)
}

type RepairApplication interface {
	GetRepairQueue(context.Context, string) ([]*adminv1.RepairQueueItem, error)
}

type MemberApplication interface {
	CordonMember(context.Context, MemberMutationRequest) (*adminv1.StorageMember, error)
	UncordonMember(context.Context, MemberMutationRequest) (*adminv1.StorageMember, error)
	GetEvictionSafety(context.Context, string) (*adminv1.EvictionSafety, error)
}

type DisasterRecoveryApplication interface {
	GetRecoveryReadiness(context.Context) (*adminv1.RecoveryReadiness, error)
}

const (
	adminOperationPlanTTL                  = 15 * time.Minute
	adminAuditActorIdentity                = "pre-production-admin-api"
	adminOperationTypeMetadata             = "scrap.operation_type"
	adminOperationPlanIDMetadata           = "scrap.operation_plan_id"
	adminPlanHashMetadata                  = "scrap.plan_hash"
	adminDryRunMetadata                    = "scrap.dry_run"
	adminOperationLaneMetadata             = "scrap.operation_lane"
	adminBackendLaneMetadata               = "scrap.backend_lane"
	adminDestinationKeyIDMetadata          = "scrap.destination_key_id"
	adminCapacityProfileIDMetadata         = "scrap.capacity_profile_id"
	adminCapacityOverrideExpiresAtMetadata = "scrap.capacity_override_expires_at"
	adminCapacityOverrideReasonMetadata    = "scrap.capacity_override_reason"

	adminAuditEventOperationStarted  = "operation_started"
	adminAuditEventOperationCanceled = "operation_canceled"
	adminAuditEventMemberCordoned    = "member_cordoned"
	adminAuditEventMemberUncordoned  = "member_uncordoned"
)

var deterministicProtoMarshal = proto.MarshalOptions{Deterministic: true}

type AdminOption func(*AdminServer)

func NewAdminServer(options ...AdminOption) *AdminServer {
	server := &AdminServer{now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(server)
	}
	return server
}

func WithOperationStore(store *operations.Store) AdminOption {
	return func(server *AdminServer) {
		server.operations = store
	}
}

func WithInspectApplication(inspect InspectApplication) AdminOption {
	return func(server *AdminServer) {
		server.inspect = inspect
	}
}

func WithRepairApplication(repair RepairApplication) AdminOption {
	return func(server *AdminServer) {
		server.repair = repair
	}
}

func WithMemberApplication(member MemberApplication) AdminOption {
	return func(server *AdminServer) {
		server.member = member
	}
}

func WithDisasterRecoveryApplication(dr DisasterRecoveryApplication) AdminOption {
	return func(server *AdminServer) {
		server.dr = dr
	}
}

func RegisterAdminServer(registrar grpc.ServiceRegistrar, server *AdminServer) {
	adminv1.RegisterInspectServiceServer(registrar, server)
	adminv1.RegisterOperationServiceServer(registrar, server)
	adminv1.RegisterRestoreServiceServer(registrar, server)
	adminv1.RegisterRepairServiceServer(registrar, server)
	adminv1.RegisterMemberServiceServer(registrar, server)
	adminv1.RegisterLifecycleServiceServer(registrar, server)
	adminv1.RegisterKeyServiceServer(registrar, server)
	adminv1.RegisterCapacityServiceServer(registrar, server)
	adminv1.RegisterDisasterRecoveryServiceServer(registrar, server)
}

func (s *AdminServer) GetClusterSummary(ctx context.Context, req *adminv1.GetClusterSummaryRequest) (*adminv1.GetClusterSummaryResponse, error) {
	if req == nil {
		var problems violations
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetClusterSummary")
	}
	summary, err := s.inspect.GetAdminClusterSummary(ctx)
	if err != nil {
		return nil, err
	}
	if s.operations != nil {
		pending, err := s.operations.List(operations.ListFilter{
			States: []adminv1.OperationState{
				adminv1.OperationState_OPERATION_STATE_QUEUED,
				adminv1.OperationState_OPERATION_STATE_RUNNING,
			},
		})
		if err != nil {
			return nil, err
		}
		summary.PendingOperationCount = uint32(len(pending))
	}
	return &adminv1.GetClusterSummaryResponse{Summary: summary}, nil
}

func (s *AdminServer) GetShard(ctx context.Context, req *adminv1.GetShardRequest) (*adminv1.GetShardResponse, error) {
	if err := validateRequiredRequestText(req, "shard_id", func(req *adminv1.GetShardRequest) string { return req.ShardId }); err != nil {
		return nil, err
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetShard")
	}
	shard, err := s.inspect.GetAdminShard(ctx, req.ShardId)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetShardResponse{Shard: shard}, nil
}

func (s *AdminServer) GetDocument(ctx context.Context, req *adminv1.GetDocumentRequest) (*adminv1.GetDocumentResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateAdminDocumentTarget("document", req.Document, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetDocument")
	}
	document, err := s.inspect.GetAdminDocument(ctx, identity.Document{
		TenantID:      req.Document.GetTenantId(),
		TransactionID: req.Document.GetTransactionId(),
		DocumentName:  req.Document.GetDocumentName(),
	})
	if err != nil {
		return nil, err
	}
	return &adminv1.GetDocumentResponse{Document: document}, nil
}

func (s *AdminServer) GetBlock(ctx context.Context, req *adminv1.GetBlockRequest) (*adminv1.GetBlockResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	target := validateBlockTarget("block", req.Block, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetBlock")
	}
	block, err := s.inspect.GetAdminBlock(ctx, target)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetBlockResponse{Block: block}, nil
}

func (s *AdminServer) GetMember(ctx context.Context, req *adminv1.GetMemberRequest) (*adminv1.GetMemberResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	memberID := validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetMember")
	}
	member, err := s.inspect.GetAdminMember(ctx, memberID)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetMemberResponse{StorageMember: member}, nil
}

func (s *AdminServer) GetCapacityRunway(ctx context.Context, req *adminv1.GetCapacityRunwayRequest) (*adminv1.GetCapacityRunwayResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateOptionalText("capacity_profile_id", req.CapacityProfileId, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.inspect == nil {
		return nil, unimplementedAdmin("GetCapacityRunway")
	}
	runway, err := s.inspect.GetAdminCapacityRunway(ctx, req.GetCapacityProfileId())
	if err != nil {
		return nil, err
	}
	return &adminv1.GetCapacityRunwayResponse{Runway: runway}, nil
}

func (s *AdminServer) GetOperation(_ context.Context, req *adminv1.GetOperationRequest) (*adminv1.GetOperationResponse, error) {
	if err := validateOperationIDRequest(req, "operation_id", func(req *adminv1.GetOperationRequest) string { return req.OperationId }); err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("GetOperation")
	}
	operation, err := s.operations.Get(req.GetOperationId())
	if errors.Is(err, operations.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "operation not found")
	}
	if err != nil {
		return nil, err
	}
	return &adminv1.GetOperationResponse{Operation: operation}, nil
}

func (s *AdminServer) WatchOperation(req *adminv1.WatchOperationRequest, stream grpc.ServerStreamingServer[adminv1.WatchOperationResponse]) error {
	if err := validateOperationIDRequest(req, "operation_id", func(req *adminv1.WatchOperationRequest) string { return req.OperationId }); err != nil {
		return err
	}
	if s.operations == nil {
		return unimplementedAdmin("WatchOperation")
	}
	operation, err := s.operations.Get(req.GetOperationId())
	if errors.Is(err, operations.ErrNotFound) {
		return status.Error(codes.NotFound, "operation not found")
	}
	if err != nil {
		return err
	}
	const snapshotSequence uint64 = 1
	if req.GetAfterSequence() >= snapshotSequence {
		return nil
	}
	return stream.Send(&adminv1.WatchOperationResponse{
		Sequence:  snapshotSequence,
		Operation: operation,
		Delta: &adminv1.OperationDelta{
			State:     operation.GetState(),
			Progress:  operation.GetProgress(),
			Warnings:  operation.GetWarnings(),
			LastError: operation.GetLastError(),
		},
	})
}

func (s *AdminServer) ListOperations(_ context.Context, req *adminv1.ListOperationsRequest) (*adminv1.ListOperationsResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	for i, state := range req.States {
		if state == adminv1.OperationState_OPERATION_STATE_UNSPECIFIED {
			problems.add("states", reasonEnumUnspecified, "operation state filters must not be unspecified")
			continue
		}
		if _, ok := adminv1.OperationState_name[int32(state)]; !ok {
			problems.add("states", reasonEnumUnknown, "operation state filter is not recognized")
		}
		_ = i
	}
	validateOptionalText("operation_type", req.OperationType, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("ListOperations")
	}
	result, err := s.operations.List(operations.ListFilter{
		States:        req.States,
		OperationType: req.GetOperationType(),
	})
	if err != nil {
		return nil, err
	}
	return &adminv1.ListOperationsResponse{Operations: result}, nil
}

func (s *AdminServer) CancelOperation(_ context.Context, req *adminv1.CancelOperationRequest) (*adminv1.CancelOperationResponse, error) {
	if err := validateOperationIDRequest(req, "operation_id", func(req *adminv1.CancelOperationRequest) string { return req.OperationId }); err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("CancelOperation")
	}
	operation, err := s.operations.Cancel(req.GetOperationId(), s.now())
	if errors.Is(err, operations.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "operation not found")
	}
	if err != nil {
		return nil, err
	}
	if operation.GetState() == adminv1.OperationState_OPERATION_STATE_CANCELED {
		if err := s.auditOperationCanceled(operation); err != nil {
			return nil, err
		}
	}
	return &adminv1.CancelOperationResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanRestore(_ context.Context, req *adminv1.PlanRestoreRequest) (*adminv1.PlanRestoreResponse, error) {
	planReq, err := ValidatePlanRestoreRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanRestore")
	}
	plan, err := s.createOperationPlan("restore", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanRestoreResponse{Plan: plan}, nil
}

func (s *AdminServer) StartRestore(_ context.Context, req *adminv1.StartRestoreRequest) (*adminv1.StartRestoreResponse, error) {
	startReq, err := ValidateStartRestoreRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartRestore")
	}
	operation, err := s.startPlannedOperation("restore", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartRestoreResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanPrewarm(_ context.Context, req *adminv1.PlanPrewarmRequest) (*adminv1.PlanPrewarmResponse, error) {
	planReq, err := ValidatePlanPrewarmRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanPrewarm")
	}
	plan, err := s.createOperationPlan("prewarm", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanPrewarmResponse{Plan: plan}, nil
}

func (s *AdminServer) StartPrewarm(_ context.Context, req *adminv1.StartPrewarmRequest) (*adminv1.StartPrewarmResponse, error) {
	startReq, err := ValidateStartPrewarmRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartPrewarm")
	}
	operation, err := s.startPlannedOperation("prewarm", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartPrewarmResponse{Operation: operation}, nil
}

func (s *AdminServer) GetRepairQueue(ctx context.Context, req *adminv1.GetRepairQueueRequest) (*adminv1.GetRepairQueueResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateOptionalText("shard_id", req.ShardId, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	if s.repair == nil {
		return nil, unimplementedAdmin("GetRepairQueue")
	}
	items, err := s.repair.GetRepairQueue(ctx, req.GetShardId())
	if err != nil {
		return nil, err
	}
	return &adminv1.GetRepairQueueResponse{Items: items}, nil
}

func (s *AdminServer) PlanRepair(_ context.Context, req *adminv1.PlanRepairRequest) (*adminv1.PlanRepairResponse, error) {
	planReq, err := ValidatePlanRepairRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanRepair")
	}
	plan, err := s.createOperationPlan("repair", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanRepairResponse{Plan: plan}, nil
}

func (s *AdminServer) StartRepair(_ context.Context, req *adminv1.StartRepairRequest) (*adminv1.StartRepairResponse, error) {
	startReq, err := ValidateStartRepairRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartRepair")
	}
	operation, err := s.startPlannedOperation("repair", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartRepairResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanScrub(_ context.Context, req *adminv1.PlanScrubRequest) (*adminv1.PlanScrubResponse, error) {
	planReq, err := ValidatePlanScrubRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanScrub")
	}
	plan, err := s.createOperationPlan("scrub", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanScrubResponse{Plan: plan}, nil
}

func (s *AdminServer) StartScrub(_ context.Context, req *adminv1.StartScrubRequest) (*adminv1.StartScrubResponse, error) {
	startReq, err := ValidateStartScrubRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartScrub")
	}
	operation, err := s.startPlannedOperation("scrub", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartScrubResponse{Operation: operation}, nil
}

func (s *AdminServer) CordonMember(ctx context.Context, req *adminv1.CordonMemberRequest) (*adminv1.CordonMemberResponse, error) {
	validated, err := ValidateCordonMemberRequest(req)
	if err != nil {
		return nil, err
	}
	if s.member == nil {
		return nil, unimplementedAdmin("CordonMember")
	}
	member, err := s.member.CordonMember(ctx, validated)
	if err != nil {
		return nil, err
	}
	if err := s.auditMemberMutation(adminAuditEventMemberCordoned, "cordon-member", validated); err != nil {
		return nil, err
	}
	return &adminv1.CordonMemberResponse{StorageMember: member}, nil
}

func (s *AdminServer) UncordonMember(ctx context.Context, req *adminv1.UncordonMemberRequest) (*adminv1.UncordonMemberResponse, error) {
	validated, err := ValidateUncordonMemberRequest(req)
	if err != nil {
		return nil, err
	}
	if s.member == nil {
		return nil, unimplementedAdmin("UncordonMember")
	}
	member, err := s.member.UncordonMember(ctx, validated)
	if err != nil {
		return nil, err
	}
	if err := s.auditMemberMutation(adminAuditEventMemberUncordoned, "uncordon-member", validated); err != nil {
		return nil, err
	}
	return &adminv1.UncordonMemberResponse{StorageMember: member}, nil
}

func (s *AdminServer) GetEvictionSafety(ctx context.Context, req *adminv1.GetEvictionSafetyRequest) (*adminv1.GetEvictionSafetyResponse, error) {
	validated, err := ValidateGetEvictionSafetyRequest(req)
	if err != nil {
		return nil, err
	}
	if s.member == nil {
		return nil, unimplementedAdmin("GetEvictionSafety")
	}
	safety, err := s.member.GetEvictionSafety(ctx, validated.StorageMember)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetEvictionSafetyResponse{Safety: safety}, nil
}

func (s *AdminServer) PlanDrain(_ context.Context, req *adminv1.PlanDrainRequest) (*adminv1.PlanDrainResponse, error) {
	planReq, err := ValidatePlanDrainRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanDrain")
	}
	plan, err := s.createOperationPlan("drain", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanDrainResponse{Plan: plan}, nil
}

func (s *AdminServer) StartDrain(_ context.Context, req *adminv1.StartDrainRequest) (*adminv1.StartDrainResponse, error) {
	startReq, err := ValidateStartDrainRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartDrain")
	}
	operation, err := s.startPlannedOperation("drain", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartDrainResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanTombstone(_ context.Context, req *adminv1.PlanTombstoneRequest) (*adminv1.PlanTombstoneResponse, error) {
	planReq, err := ValidatePlanTombstoneRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanTombstone")
	}
	plan, err := s.createOperationPlan("tombstone", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanTombstoneResponse{Plan: plan}, nil
}

func (s *AdminServer) StartTombstone(_ context.Context, req *adminv1.StartTombstoneRequest) (*adminv1.StartTombstoneResponse, error) {
	startReq, err := ValidateStartTombstoneRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartTombstone")
	}
	operation, err := s.startPlannedOperation("tombstone", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartTombstoneResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanKeyRotation(_ context.Context, req *adminv1.PlanKeyRotationRequest) (*adminv1.PlanKeyRotationResponse, error) {
	planReq, err := ValidatePlanKeyRotationRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanKeyRotation")
	}
	plan, err := s.createOperationPlan("rewrap", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanKeyRotationResponse{Plan: plan}, nil
}

func (s *AdminServer) StartKeyRotation(_ context.Context, req *adminv1.StartKeyRotationRequest) (*adminv1.StartKeyRotationResponse, error) {
	startReq, err := ValidateStartKeyRotationRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartKeyRotation")
	}
	operation, err := s.startPlannedOperation("rewrap", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartKeyRotationResponse{Operation: operation}, nil
}

func (s *AdminServer) PlanCapacityOverride(_ context.Context, req *adminv1.PlanCapacityOverrideRequest) (*adminv1.PlanCapacityOverrideResponse, error) {
	planReq, err := ValidatePlanCapacityOverrideRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanCapacityOverride")
	}
	plan, err := s.createOperationPlan("capacity-override", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanCapacityOverrideResponse{Plan: plan}, nil
}

func (s *AdminServer) StartCapacityOverride(_ context.Context, req *adminv1.StartCapacityOverrideRequest) (*adminv1.StartCapacityOverrideResponse, error) {
	startReq, err := ValidateStartCapacityOverrideRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartCapacityOverride")
	}
	operation, err := s.startPlannedOperation("capacity-override", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartCapacityOverrideResponse{Operation: operation}, nil
}

func (s *AdminServer) GetRecoveryReadiness(ctx context.Context, req *adminv1.GetRecoveryReadinessRequest) (*adminv1.GetRecoveryReadinessResponse, error) {
	if req == nil {
		var problems violations
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	if s.dr == nil {
		return nil, unimplementedAdmin("GetRecoveryReadiness")
	}
	readiness, err := s.dr.GetRecoveryReadiness(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetRecoveryReadinessResponse{Readiness: readiness}, nil
}

func (s *AdminServer) PlanRecovery(_ context.Context, req *adminv1.PlanRecoveryRequest) (*adminv1.PlanRecoveryResponse, error) {
	planReq, err := ValidatePlanRecoveryRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("PlanRecovery")
	}
	plan, err := s.createOperationPlan("recovery", planReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.PlanRecoveryResponse{Plan: plan}, nil
}

func (s *AdminServer) StartMetadataRestore(_ context.Context, req *adminv1.StartMetadataRestoreRequest) (*adminv1.StartMetadataRestoreResponse, error) {
	startReq, err := ValidateStartMetadataRestoreRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartMetadataRestore")
	}
	operation, err := s.startPlannedOperationFromPlan("recovery", "metadata-restore", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartMetadataRestoreResponse{Operation: operation}, nil
}

func (s *AdminServer) StartCopyVerify(_ context.Context, req *adminv1.StartCopyVerifyRequest) (*adminv1.StartCopyVerifyResponse, error) {
	startReq, err := ValidateStartCopyVerifyRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartCopyVerify")
	}
	operation, err := s.startPlannedOperationFromPlan("recovery", "copy-verify", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartCopyVerifyResponse{Operation: operation}, nil
}

func (s *AdminServer) StartDRDrill(_ context.Context, req *adminv1.StartDRDrillRequest) (*adminv1.StartDRDrillResponse, error) {
	startReq, err := ValidateStartDRDrillRequest(req)
	if err != nil {
		return nil, err
	}
	if s.operations == nil {
		return nil, unimplementedAdmin("StartDRDrill")
	}
	operation, err := s.startPlannedOperationFromPlan("recovery", "dr-drill", startReq)
	if err != nil {
		return nil, err
	}
	return &adminv1.StartDRDrillResponse{Operation: operation}, nil
}

func (s *AdminServer) createOperationPlan(operationType string, req OperationPlanRequest) (*adminv1.OperationPlan, error) {
	metadata := cloneTags(req.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[adminOperationTypeMetadata] = operationType
	if req.DryRun {
		metadata[adminDryRunMetadata] = "true"
	} else {
		metadata[adminDryRunMetadata] = "false"
	}
	if req.PinUntil != nil {
		metadata["scrap.pin_until"] = req.PinUntil.Format(time.RFC3339Nano)
	}
	if metadata[adminOperationLaneMetadata] == "" {
		metadata[adminOperationLaneMetadata] = defaultAdminOperationLane(operationType)
	}
	if metadata[adminBackendLaneMetadata] == "" && isBackendLaneOperation(operationType) {
		metadata[adminBackendLaneMetadata] = defaultAdminBackendLane(operationType)
	}

	planID, err := identity.NewUUIDv7()
	if err != nil {
		return nil, err
	}
	plan := &adminv1.OperationPlan{
		OperationPlanId: planID,
		ExpiresAt:       timestamppb.New(s.now().Add(adminOperationPlanTTL)),
		Targets:         adminTargetsToProto(req.Targets),
		EstimatedImpact: estimateOperationImpact(req.Targets),
		Warnings:        cloneWarnings(req.Warnings),
		Metadata:        metadata,
	}
	plan.PlanHash, err = hashPlan(plan)
	if err != nil {
		return nil, err
	}
	if err := s.operations.PutPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func defaultAdminOperationLane(operationType string) string {
	switch operationType {
	case "restore":
		return "interactive-restore"
	case "prewarm":
		return "planned-prewarm"
	case "metadata-restore", "copy-verify", "dr-drill":
		return "bulk-audit"
	default:
		return operationType
	}
}

func isBackendLaneOperation(operationType string) bool {
	switch operationType {
	case "restore", "prewarm", "repair", "rewrap", "scrub", "metadata-restore", "copy-verify", "dr-drill":
		return true
	default:
		return false
	}
}

func defaultAdminBackendLane(operationType string) string {
	switch operationType {
	case "repair":
		return "repair"
	case "rewrap":
		return "rewrap"
	case "scrub":
		return "scrub"
	case "metadata-restore", "copy-verify", "dr-drill":
		return "dr_copy"
	default:
		return "restore"
	}
}

func (s *AdminServer) startPlannedOperation(operationType string, req OperationStartRequest) (*adminv1.Operation, error) {
	return s.startPlannedOperationFromPlan(operationType, operationType, req)
}

func (s *AdminServer) startPlannedOperationFromPlan(planType string, operationType string, req OperationStartRequest) (*adminv1.Operation, error) {
	plan, err := s.operations.GetPlan(req.OperationPlanID)
	if errors.Is(err, operations.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "operation plan not found")
	}
	if err != nil {
		return nil, err
	}
	if plan.GetPlanHash() != req.PlanHash {
		return nil, status.Error(codes.FailedPrecondition, "plan hash does not match operation plan")
	}
	if plan.GetMetadata()[adminOperationTypeMetadata] != planType {
		return nil, status.Error(codes.FailedPrecondition, "operation plan type does not match start request")
	}
	if plan.GetExpiresAt().AsTime().Before(s.now()) {
		return nil, status.Error(codes.FailedPrecondition, "operation plan has expired")
	}
	if existing, err := s.operations.Get(req.OperationID); err == nil {
		if isSameStartedOperation(existing, operationType, req) {
			if err := s.auditOperationStarted(existing); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, status.Error(codes.AlreadyExists, "operation id already exists with different metadata")
	} else if !errors.Is(err, operations.ErrNotFound) {
		return nil, err
	}

	metadata := cloneTags(plan.GetMetadata())
	if metadata == nil {
		metadata = make(map[string]string)
	}
	for key, value := range req.Metadata {
		metadata[key] = value
	}
	metadata[adminOperationPlanIDMetadata] = req.OperationPlanID
	metadata[adminPlanHashMetadata] = req.PlanHash

	operation := &adminv1.Operation{
		OperationId:         req.OperationID,
		OperationType:       operationType,
		State:               adminv1.OperationState_OPERATION_STATE_QUEUED,
		RequestedByIdentity: "pre-production-admin-api",
		RequestedAt:         timestamppb.New(s.now()),
		DryRun:              plan.GetMetadata()[adminDryRunMetadata] == "true",
		Targets:             cloneTargets(plan.GetTargets()),
		Progress:            &adminv1.OperationProgress{Message: "queued"},
		Metadata:            metadata,
	}
	created, err := s.operations.Create(operation)
	if errors.Is(err, operations.ErrConflict) {
		return nil, status.Error(codes.AlreadyExists, "operation id already exists with different metadata")
	}
	if err != nil {
		return nil, err
	}
	if err := s.auditOperationStarted(created); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *AdminServer) auditOperationStarted(operation *adminv1.Operation) error {
	if s.operations == nil {
		return nil
	}
	return s.operations.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       auditEventID(adminAuditEventOperationStarted, operation.GetOperationId()),
		EventType:     adminAuditEventOperationStarted,
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: auditActorIdentity(operation.GetRequestedByIdentity()),
		OccurredAt:    auditOperationRequestedAt(operation, s.now()),
		Targets:       cloneTargets(operation.GetTargets()),
		Metadata:      cloneTags(operation.GetMetadata()),
	})
}

func (s *AdminServer) auditOperationCanceled(operation *adminv1.Operation) error {
	if s.operations == nil {
		return nil
	}
	return s.operations.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       auditEventID(adminAuditEventOperationCanceled, operation.GetOperationId()),
		EventType:     adminAuditEventOperationCanceled,
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: adminAuditActorIdentity,
		OccurredAt:    auditOperationFinishedAt(operation, s.now()),
		Targets:       cloneTargets(operation.GetTargets()),
		Metadata:      cloneTags(operation.GetMetadata()),
	})
}

func (s *AdminServer) auditMemberMutation(eventType string, operationType string, req MemberMutationRequest) error {
	if s.operations == nil {
		return nil
	}
	metadata := map[string]string{"storage_member": req.StorageMember}
	if req.Reason != "" {
		metadata["reason"] = req.Reason
	}
	return s.operations.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       auditEventID(eventType, req.OperationID),
		EventType:     eventType,
		OperationId:   req.OperationID,
		OperationType: operationType,
		ActorIdentity: adminAuditActorIdentity,
		OccurredAt:    timestamppb.New(s.now()),
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_StorageMember{
					StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: req.StorageMember},
				},
			},
		},
		Metadata: metadata,
	})
}

func auditEventID(eventType string, operationID string) string {
	return eventType + ":" + operationID
}

func auditActorIdentity(actor string) string {
	if actor == "" {
		return adminAuditActorIdentity
	}
	return actor
}

func auditOperationRequestedAt(operation *adminv1.Operation, fallback time.Time) *timestamppb.Timestamp {
	if operation.GetRequestedAt() != nil {
		return operation.GetRequestedAt()
	}
	return timestamppb.New(fallback)
}

func auditOperationFinishedAt(operation *adminv1.Operation, fallback time.Time) *timestamppb.Timestamp {
	if operation.GetFinishedAt() != nil {
		return operation.GetFinishedAt()
	}
	return timestamppb.New(fallback)
}

func isSameStartedOperation(operation *adminv1.Operation, operationType string, req OperationStartRequest) bool {
	return operation.GetOperationType() == operationType &&
		operation.GetMetadata()[adminOperationPlanIDMetadata] == req.OperationPlanID &&
		operation.GetMetadata()[adminPlanHashMetadata] == req.PlanHash
}

func adminTargetsToProto(targets []AdminTarget) []*adminv1.Target {
	out := make([]*adminv1.Target, 0, len(targets))
	for _, target := range targets {
		switch target.Kind {
		case AdminTargetDocument:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_Document{Document: &adminv1.DocumentTarget{
				TenantId:      target.Document.TenantID,
				TransactionId: target.Document.TransactionID,
				DocumentName:  target.Document.DocumentName,
			}}})
		case AdminTargetTransaction:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_Transaction{Transaction: &adminv1.TransactionTarget{
				TenantId:      target.Transaction.TenantID,
				TransactionId: target.Transaction.TransactionID,
			}}})
		case AdminTargetBlock:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_Block{Block: &adminv1.BlockTarget{
				ShardId: target.Block.ShardID,
				BlockId: target.Block.BlockID,
			}}})
		case AdminTargetShard:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_Shard{Shard: &adminv1.ShardTarget{ShardId: target.ShardID}}})
		case AdminTargetStorageMember:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_StorageMember{StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: target.StorageMember}}})
		case AdminTargetSnapshot:
			snapshot := &adminv1.SnapshotTarget{SnapshotId: target.Snapshot.SnapshotID}
			if target.Snapshot.ShardID != "" {
				shardID := target.Snapshot.ShardID
				snapshot.ShardId = &shardID
			}
			if target.Snapshot.CheckpointID != "" {
				checkpointID := target.Snapshot.CheckpointID
				snapshot.CheckpointId = &checkpointID
			}
			out = append(out, &adminv1.Target{Target: &adminv1.Target_Snapshot{Snapshot: snapshot}})
		case AdminTargetCapacityProfile:
			out = append(out, &adminv1.Target{Target: &adminv1.Target_CapacityProfile{CapacityProfile: &adminv1.CapacityProfileTarget{
				CapacityProfileId: target.CapacityProfile,
			}}})
		}
	}
	return out
}

func estimateOperationImpact(targets []AdminTarget) *adminv1.OperationImpact {
	impact := &adminv1.OperationImpact{}
	shards := make(map[string]bool)
	for _, target := range targets {
		switch target.Kind {
		case AdminTargetDocument:
			impact.AffectedDocumentCount++
		case AdminTargetTransaction:
			impact.AffectedShardCount++
		case AdminTargetBlock:
			shards[target.Block.ShardID] = true
		case AdminTargetShard:
			shards[target.ShardID] = true
		}
	}
	impact.AffectedShardCount += uint32(len(shards))
	return impact
}

func hashPlan(plan *adminv1.OperationPlan) (string, error) {
	clone := proto.Clone(plan).(*adminv1.OperationPlan)
	clone.PlanHash = ""
	data, err := deterministicProtoMarshal.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneTargets(targets []*adminv1.Target) []*adminv1.Target {
	if len(targets) == 0 {
		return nil
	}
	out := make([]*adminv1.Target, 0, len(targets))
	for _, target := range targets {
		out = append(out, proto.Clone(target).(*adminv1.Target))
	}
	return out
}

func cloneWarnings(warnings []*adminv1.OperationWarning) []*adminv1.OperationWarning {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]*adminv1.OperationWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, proto.Clone(warning).(*adminv1.OperationWarning))
	}
	return out
}

func validateRequiredRequestText[T any](req *T, field string, get func(*T) string) error {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return problems.err()
	}
	validateRequiredText(field, get(req), &problems)
	return problems.err()
}

func validateOperationIDRequest[T any](req *T, field string, get func(*T) string) error {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return problems.err()
	}
	validateUUIDv7(field, get(req), &problems)
	return problems.err()
}

func unimplementedAdmin(method string) error {
	return status.Errorf(codes.Unimplemented, "%s is not implemented", method)
}
