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
	operations *operations.Store
	now        func() time.Time
}

const (
	adminOperationPlanTTL        = 15 * time.Minute
	adminOperationTypeMetadata   = "scrap.operation_type"
	adminOperationPlanIDMetadata = "scrap.operation_plan_id"
	adminPlanHashMetadata        = "scrap.plan_hash"
	adminDryRunMetadata          = "scrap.dry_run"
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

func RegisterAdminServer(registrar grpc.ServiceRegistrar, server *AdminServer) {
	adminv1.RegisterInspectServiceServer(registrar, server)
	adminv1.RegisterOperationServiceServer(registrar, server)
	adminv1.RegisterRestoreServiceServer(registrar, server)
	adminv1.RegisterRepairServiceServer(registrar, server)
	adminv1.RegisterMemberServiceServer(registrar, server)
	adminv1.RegisterLifecycleServiceServer(registrar, server)
	adminv1.RegisterDisasterRecoveryServiceServer(registrar, server)
}

func (s *AdminServer) GetClusterSummary(context.Context, *adminv1.GetClusterSummaryRequest) (*adminv1.GetClusterSummaryResponse, error) {
	return nil, unimplementedAdmin("GetClusterSummary")
}

func (s *AdminServer) GetShard(_ context.Context, req *adminv1.GetShardRequest) (*adminv1.GetShardResponse, error) {
	if err := validateRequiredRequestText(req, "shard_id", func(req *adminv1.GetShardRequest) string { return req.ShardId }); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetShard")
}

func (s *AdminServer) GetDocument(_ context.Context, req *adminv1.GetDocumentRequest) (*adminv1.GetDocumentResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateAdminDocumentTarget("document", req.Document, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetDocument")
}

func (s *AdminServer) GetBlock(_ context.Context, req *adminv1.GetBlockRequest) (*adminv1.GetBlockResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateBlockTarget("block", req.Block, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetBlock")
}

func (s *AdminServer) GetMember(_ context.Context, req *adminv1.GetMemberRequest) (*adminv1.GetMemberResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetMember")
}

func (s *AdminServer) GetCapacityRunway(_ context.Context, req *adminv1.GetCapacityRunwayRequest) (*adminv1.GetCapacityRunwayResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateOptionalText("capacity_profile_id", req.CapacityProfileId, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetCapacityRunway")
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

func (s *AdminServer) GetRepairQueue(_ context.Context, req *adminv1.GetRepairQueueRequest) (*adminv1.GetRepairQueueResponse, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return nil, problems.err()
	}
	validateOptionalText("shard_id", req.ShardId, &problems)
	if err := problems.err(); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetRepairQueue")
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

func (s *AdminServer) CordonMember(_ context.Context, req *adminv1.CordonMemberRequest) (*adminv1.CordonMemberResponse, error) {
	if _, err := ValidateCordonMemberRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("CordonMember")
}

func (s *AdminServer) UncordonMember(_ context.Context, req *adminv1.UncordonMemberRequest) (*adminv1.UncordonMemberResponse, error) {
	if _, err := ValidateUncordonMemberRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("UncordonMember")
}

func (s *AdminServer) GetEvictionSafety(_ context.Context, req *adminv1.GetEvictionSafetyRequest) (*adminv1.GetEvictionSafetyResponse, error) {
	if _, err := ValidateGetEvictionSafetyRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("GetEvictionSafety")
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

func (s *AdminServer) GetRecoveryReadiness(context.Context, *adminv1.GetRecoveryReadinessRequest) (*adminv1.GetRecoveryReadinessResponse, error) {
	return nil, unimplementedAdmin("GetRecoveryReadiness")
}

func (s *AdminServer) PlanRecovery(_ context.Context, req *adminv1.PlanRecoveryRequest) (*adminv1.PlanRecoveryResponse, error) {
	if _, err := ValidatePlanRecoveryRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanRecovery")
}

func (s *AdminServer) StartMetadataRestore(_ context.Context, req *adminv1.StartMetadataRestoreRequest) (*adminv1.StartMetadataRestoreResponse, error) {
	if _, err := ValidateStartMetadataRestoreRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartMetadataRestore")
}

func (s *AdminServer) StartCopyVerify(_ context.Context, req *adminv1.StartCopyVerifyRequest) (*adminv1.StartCopyVerifyResponse, error) {
	if _, err := ValidateStartCopyVerifyRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartCopyVerify")
}

func (s *AdminServer) StartDRDrill(_ context.Context, req *adminv1.StartDRDrillRequest) (*adminv1.StartDRDrillResponse, error) {
	if _, err := ValidateStartDRDrillRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartDRDrill")
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

	planID, err := identity.NewUUIDv7()
	if err != nil {
		return nil, err
	}
	plan := &adminv1.OperationPlan{
		OperationPlanId: planID,
		ExpiresAt:       timestamppb.New(s.now().Add(adminOperationPlanTTL)),
		Targets:         adminTargetsToProto(req.Targets),
		EstimatedImpact: estimateOperationImpact(req.Targets),
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

func (s *AdminServer) startPlannedOperation(operationType string, req OperationStartRequest) (*adminv1.Operation, error) {
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
	if plan.GetMetadata()[adminOperationTypeMetadata] != operationType {
		return nil, status.Error(codes.FailedPrecondition, "operation plan type does not match start request")
	}
	if plan.GetExpiresAt().AsTime().Before(s.now()) {
		return nil, status.Error(codes.FailedPrecondition, "operation plan has expired")
	}
	if existing, err := s.operations.Get(req.OperationID); err == nil {
		if isSameStartedOperation(existing, operationType, req) {
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
	return created, nil
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
