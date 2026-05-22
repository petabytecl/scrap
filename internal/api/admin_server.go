package api

import (
	"context"
	"errors"
	"time"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AdminServer struct {
	operations *operations.Store
	now        func() time.Time
}

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

func (s *AdminServer) WatchOperation(req *adminv1.WatchOperationRequest, _ grpc.ServerStreamingServer[adminv1.WatchOperationResponse]) error {
	if err := validateOperationIDRequest(req, "operation_id", func(req *adminv1.WatchOperationRequest) string { return req.OperationId }); err != nil {
		return err
	}
	return unimplementedAdmin("WatchOperation")
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
	if _, err := ValidatePlanRestoreRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanRestore")
}

func (s *AdminServer) StartRestore(_ context.Context, req *adminv1.StartRestoreRequest) (*adminv1.StartRestoreResponse, error) {
	if _, err := ValidateStartRestoreRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartRestore")
}

func (s *AdminServer) PlanPrewarm(_ context.Context, req *adminv1.PlanPrewarmRequest) (*adminv1.PlanPrewarmResponse, error) {
	if _, err := ValidatePlanPrewarmRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanPrewarm")
}

func (s *AdminServer) StartPrewarm(_ context.Context, req *adminv1.StartPrewarmRequest) (*adminv1.StartPrewarmResponse, error) {
	if _, err := ValidateStartPrewarmRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartPrewarm")
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
	if _, err := ValidatePlanRepairRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanRepair")
}

func (s *AdminServer) StartRepair(_ context.Context, req *adminv1.StartRepairRequest) (*adminv1.StartRepairResponse, error) {
	if _, err := ValidateStartRepairRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartRepair")
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
	if _, err := ValidatePlanDrainRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanDrain")
}

func (s *AdminServer) StartDrain(_ context.Context, req *adminv1.StartDrainRequest) (*adminv1.StartDrainResponse, error) {
	if _, err := ValidateStartDrainRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartDrain")
}

func (s *AdminServer) PlanTombstone(_ context.Context, req *adminv1.PlanTombstoneRequest) (*adminv1.PlanTombstoneResponse, error) {
	if _, err := ValidatePlanTombstoneRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("PlanTombstone")
}

func (s *AdminServer) StartTombstone(_ context.Context, req *adminv1.StartTombstoneRequest) (*adminv1.StartTombstoneResponse, error) {
	if _, err := ValidateStartTombstoneRequest(req); err != nil {
		return nil, err
	}
	return nil, unimplementedAdmin("StartTombstone")
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
