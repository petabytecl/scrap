package api

import (
	"fmt"
	"time"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	reasonInvalidTargetKind = "SCRAP_INVALID_TARGET_KIND"
	reasonInvalidUUIDv7     = "SCRAP_INVALID_UUIDV7"
)

type AdminTargetKind string

const (
	AdminTargetDocument      AdminTargetKind = "document"
	AdminTargetTransaction   AdminTargetKind = "transaction"
	AdminTargetBlock         AdminTargetKind = "block"
	AdminTargetShard         AdminTargetKind = "shard"
	AdminTargetStorageMember AdminTargetKind = "storage_member"
	AdminTargetSnapshot      AdminTargetKind = "snapshot"
)

type AdminTarget struct {
	Kind          AdminTargetKind
	Document      identity.Document
	Transaction   identity.Transaction
	Block         BlockTarget
	ShardID       string
	StorageMember string
	Snapshot      SnapshotTarget
}

type BlockTarget struct {
	ShardID string
	BlockID string
}

type SnapshotTarget struct {
	ShardID      string
	SnapshotID   string
	CheckpointID string
}

type OperationPlanRequest struct {
	Targets  []AdminTarget
	DryRun   bool
	PinUntil *time.Time
	Metadata map[string]string
}

type OperationStartRequest struct {
	OperationID     string
	OperationPlanID string
	PlanHash        string
	Metadata        map[string]string
}

type MemberMutationRequest struct {
	OperationID   string
	StorageMember string
	Reason        string
}

type MemberTargetRequest struct {
	StorageMember string
}

var restoreTargetKinds = map[AdminTargetKind]bool{
	AdminTargetDocument:    true,
	AdminTargetTransaction: true,
	AdminTargetBlock:       true,
}

var repairTargetKinds = map[AdminTargetKind]bool{
	AdminTargetDocument:      true,
	AdminTargetBlock:         true,
	AdminTargetShard:         true,
	AdminTargetStorageMember: true,
}

var tombstoneTargetKinds = map[AdminTargetKind]bool{
	AdminTargetDocument:    true,
	AdminTargetTransaction: true,
}

func ValidateAdminTarget(target *adminv1.Target) (AdminTarget, error) {
	var problems violations
	validated := validateAdminTarget("target", target, nil, &problems)
	if err := problems.err(); err != nil {
		return AdminTarget{}, err
	}
	return validated, nil
}

func ValidatePlanRestoreRequest(req *adminv1.PlanRestoreRequest) (OperationPlanRequest, error) {
	if req == nil {
		return missingPlanRequest()
	}
	return validateTargetPlanRequest("targets", req.Targets, req.DryRun, nil, req.Metadata, restoreTargetKinds)
}

func ValidatePlanPrewarmRequest(req *adminv1.PlanPrewarmRequest) (OperationPlanRequest, error) {
	if req == nil {
		return missingPlanRequest()
	}
	return validateTargetPlanRequest("targets", req.Targets, req.DryRun, req.PinUntil, req.Metadata, restoreTargetKinds)
}

func ValidatePlanRepairRequest(req *adminv1.PlanRepairRequest) (OperationPlanRequest, error) {
	if req == nil {
		return missingPlanRequest()
	}
	return validateTargetPlanRequest("targets", req.Targets, req.DryRun, nil, req.Metadata, repairTargetKinds)
}

func ValidatePlanTombstoneRequest(req *adminv1.PlanTombstoneRequest) (OperationPlanRequest, error) {
	if req == nil {
		return missingPlanRequest()
	}
	return validateTargetPlanRequest("targets", req.Targets, req.DryRun, nil, req.Metadata, tombstoneTargetKinds)
}

func ValidatePlanRecoveryRequest(req *adminv1.PlanRecoveryRequest) (OperationPlanRequest, error) {
	if req == nil {
		return missingPlanRequest()
	}
	return validateTargetPlanRequest("targets", req.Targets, req.DryRun, nil, req.Metadata, nil)
}

func ValidateStartRestoreRequest(req *adminv1.StartRestoreRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartPrewarmRequest(req *adminv1.StartPrewarmRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartRepairRequest(req *adminv1.StartRepairRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartDrainRequest(req *adminv1.StartDrainRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartTombstoneRequest(req *adminv1.StartTombstoneRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartMetadataRestoreRequest(req *adminv1.StartMetadataRestoreRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartCopyVerifyRequest(req *adminv1.StartCopyVerifyRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateStartDRDrillRequest(req *adminv1.StartDRDrillRequest) (OperationStartRequest, error) {
	if req == nil {
		return missingStartRequest()
	}
	return validateStartRequest(req.OperationId, req.OperationPlanId, req.PlanHash, req.Metadata)
}

func ValidateCordonMemberRequest(req *adminv1.CordonMemberRequest) (MemberMutationRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return MemberMutationRequest{}, problems.err()
	}
	validateUUIDv7("operation_id", req.OperationId, &problems)
	member := validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	validateRequiredText("reason", req.Reason, &problems)
	if err := problems.err(); err != nil {
		return MemberMutationRequest{}, err
	}
	return MemberMutationRequest{
		OperationID:   req.OperationId,
		StorageMember: member,
		Reason:        req.Reason,
	}, nil
}

func ValidateUncordonMemberRequest(req *adminv1.UncordonMemberRequest) (MemberMutationRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return MemberMutationRequest{}, problems.err()
	}
	validateUUIDv7("operation_id", req.OperationId, &problems)
	member := validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	if err := problems.err(); err != nil {
		return MemberMutationRequest{}, err
	}
	return MemberMutationRequest{OperationID: req.OperationId, StorageMember: member}, nil
}

func ValidateGetEvictionSafetyRequest(req *adminv1.GetEvictionSafetyRequest) (MemberTargetRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return MemberTargetRequest{}, problems.err()
	}
	member := validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	if err := problems.err(); err != nil {
		return MemberTargetRequest{}, err
	}
	return MemberTargetRequest{StorageMember: member}, nil
}

func ValidatePlanDrainRequest(req *adminv1.PlanDrainRequest) (OperationPlanRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return OperationPlanRequest{}, problems.err()
	}
	member := validateStorageMemberTarget("storage_member", req.StorageMember, &problems)
	validateMetadata("metadata", req.Metadata, &problems)
	if err := problems.err(); err != nil {
		return OperationPlanRequest{}, err
	}
	return OperationPlanRequest{
		Targets: []AdminTarget{{
			Kind:          AdminTargetStorageMember,
			StorageMember: member,
		}},
		DryRun:   req.DryRun,
		Metadata: cloneTags(req.Metadata),
	}, nil
}

func validateTargetPlanRequest(
	field string,
	targets []*adminv1.Target,
	dryRun bool,
	pinUntil *timestamppb.Timestamp,
	metadata map[string]string,
	allowed map[AdminTargetKind]bool,
) (OperationPlanRequest, error) {
	var problems violations
	if len(targets) == 0 {
		problems.add(field, identity.ReasonRequired, "at least one target is required")
	}
	out := OperationPlanRequest{
		Targets:  make([]AdminTarget, 0, len(targets)),
		DryRun:   dryRun,
		Metadata: cloneTags(metadata),
	}
	for i, target := range targets {
		out.Targets = append(out.Targets, validateAdminTarget(fmt.Sprintf("%s[%d]", field, i), target, allowed, &problems))
	}
	out.PinUntil = validateOptionalAdminTime("pin_until", pinUntil, &problems)
	validateMetadata("metadata", metadata, &problems)
	if err := problems.err(); err != nil {
		return OperationPlanRequest{}, err
	}
	return out, nil
}

func validateStartRequest(operationID string, operationPlanID string, planHash string, metadata map[string]string) (OperationStartRequest, error) {
	var problems violations
	validateUUIDv7("operation_id", operationID, &problems)
	validateRequiredText("operation_plan_id", operationPlanID, &problems)
	validateRequiredText("plan_hash", planHash, &problems)
	validateMetadata("metadata", metadata, &problems)
	if err := problems.err(); err != nil {
		return OperationStartRequest{}, err
	}
	return OperationStartRequest{
		OperationID:     operationID,
		OperationPlanID: operationPlanID,
		PlanHash:        planHash,
		Metadata:        cloneTags(metadata),
	}, nil
}

func validateAdminTarget(field string, target *adminv1.Target, allowed map[AdminTargetKind]bool, problems *violations) AdminTarget {
	if target == nil || target.GetTarget() == nil {
		problems.add(field, identity.ReasonRequired, "target is required")
		return AdminTarget{}
	}

	switch typed := target.GetTarget().(type) {
	case *adminv1.Target_Document:
		doc := validateAdminDocumentTarget(field+".document", typed.Document, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetDocument, Document: doc}, allowed, problems)
	case *adminv1.Target_Transaction:
		transaction := validateAdminTransactionTarget(field+".transaction", typed.Transaction, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetTransaction, Transaction: transaction}, allowed, problems)
	case *adminv1.Target_Block:
		block := validateBlockTarget(field+".block", typed.Block, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetBlock, Block: block}, allowed, problems)
	case *adminv1.Target_Shard:
		shardID := validateShardTarget(field+".shard", typed.Shard, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetShard, ShardID: shardID}, allowed, problems)
	case *adminv1.Target_StorageMember:
		memberID := validateStorageMemberTarget(field+".storage_member", typed.StorageMember, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetStorageMember, StorageMember: memberID}, allowed, problems)
	case *adminv1.Target_Snapshot:
		snapshot := validateSnapshotTarget(field+".snapshot", typed.Snapshot, problems)
		return validateAllowedTarget(field, AdminTarget{Kind: AdminTargetSnapshot, Snapshot: snapshot}, allowed, problems)
	default:
		problems.add(field, reasonInvalidTargetKind, "target kind is not recognized")
		return AdminTarget{}
	}
}

func validateAllowedTarget(field string, target AdminTarget, allowed map[AdminTargetKind]bool, problems *violations) AdminTarget {
	if allowed != nil && !allowed[target.Kind] {
		problems.add(field, reasonInvalidTargetKind, fmt.Sprintf("target kind %q is not accepted by this request", target.Kind))
	}
	return target
}

func validateAdminDocumentTarget(field string, target *adminv1.DocumentTarget, problems *violations) identity.Document {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "document target is required")
		return identity.Document{}
	}
	doc, identityProblems := identity.NewDocument(target.TenantId, target.TransactionId, target.DocumentName)
	addIdentityProblems(field, identityProblems, problems)
	return doc
}

func validateAdminTransactionTarget(field string, target *adminv1.TransactionTarget, problems *violations) identity.Transaction {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "transaction target is required")
		return identity.Transaction{}
	}
	transaction, identityProblems := identity.NewTransaction(target.TenantId, target.TransactionId)
	addIdentityProblems(field, identityProblems, problems)
	return transaction
}

func validateBlockTarget(field string, target *adminv1.BlockTarget, problems *violations) BlockTarget {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "block target is required")
		return BlockTarget{}
	}
	validateRequiredText(field+".shard_id", target.ShardId, problems)
	validateRequiredText(field+".block_id", target.BlockId, problems)
	return BlockTarget{ShardID: target.ShardId, BlockID: target.BlockId}
}

func validateShardTarget(field string, target *adminv1.ShardTarget, problems *violations) string {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "shard target is required")
		return ""
	}
	validateRequiredText(field+".shard_id", target.ShardId, problems)
	return target.ShardId
}

func validateStorageMemberTarget(field string, target *adminv1.StorageMemberTarget, problems *violations) string {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "storage member target is required")
		return ""
	}
	validateRequiredText(field+".storage_member_id", target.StorageMemberId, problems)
	return target.StorageMemberId
}

func validateSnapshotTarget(field string, target *adminv1.SnapshotTarget, problems *violations) SnapshotTarget {
	if target == nil {
		problems.add(field, identity.ReasonRequired, "snapshot target is required")
		return SnapshotTarget{}
	}
	validateOptionalText(field+".shard_id", target.ShardId, problems)
	validateOptionalText(field+".checkpoint_id", target.CheckpointId, problems)
	if target.SnapshotId == "" && (target.CheckpointId == nil || *target.CheckpointId == "") {
		problems.add(field, identity.ReasonRequired, "snapshot_id or checkpoint_id is required")
	}
	if target.SnapshotId != "" {
		validateText(field+".snapshot_id", target.SnapshotId, problems)
	}
	return SnapshotTarget{
		ShardID:      optionalString(target.ShardId),
		SnapshotID:   target.SnapshotId,
		CheckpointID: optionalString(target.CheckpointId),
	}
}

func validateUUIDv7(field string, value string, problems *violations) {
	if value == "" {
		problems.add(field, identity.ReasonRequired, field+" is required")
		return
	}
	if !isUUIDv7(value) {
		problems.add(field, reasonInvalidUUIDv7, field+" must be a UUIDv7")
	}
}

func isUUIDv7(value string) bool {
	return identity.IsUUIDv7(value)
}

func validateOptionalAdminTime(field string, value *timestamppb.Timestamp, problems *violations) *time.Time {
	if value == nil {
		return nil
	}
	if err := value.CheckValid(); err != nil {
		problems.add(field, reasonInvalidTimestamp, "timestamp is outside the valid protobuf range")
		return nil
	}
	parsed := value.AsTime()
	return &parsed
}

func validateMetadata(field string, metadata map[string]string, problems *violations) {
	validateTags(field, metadata, problems)
	if size := tagsSize(metadata); size > MaxMetadataBytes {
		problems.add(field, reasonMetadataTooLarge, fmt.Sprintf("metadata is %d bytes; maximum is %d bytes", size, MaxMetadataBytes))
	}
}

func missingPlanRequest() (OperationPlanRequest, error) {
	var problems violations
	problems.add("request", identity.ReasonRequired, "request is required")
	return OperationPlanRequest{}, problems.err()
}

func missingStartRequest() (OperationStartRequest, error) {
	var problems violations
	problems.add("request", identity.ReasonRequired, "request is required")
	return OperationStartRequest{}, problems.err()
}
