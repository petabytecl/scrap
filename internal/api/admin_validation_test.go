package api

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestValidateAdminTargetDocumentNormalizesIdentity(t *testing.T) {
	target, err := ValidateAdminTarget(&adminv1.Target{
		Target: &adminv1.Target_Document{
			Document: &adminv1.DocumentTarget{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  `folder\invoice.xml`,
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "unexpected error")
	if target.Kind != AdminTargetDocument {
		t.Fatalf("kind = %s, want document", target.Kind)
	}
	if target.Document.DocumentName != "folder/invoice.xml" {
		t.Fatalf("document name = %q, want normalized", target.Document.DocumentName)
	}
}

func TestValidatePlanRestoreRequestRejectsUnsupportedTargets(t *testing.T) {
	_, err := ValidatePlanRestoreRequest(&adminv1.PlanRestoreRequest{
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

func TestValidatePlanRestoreRequestRequiresTargetsAndBoundedMetadata(t *testing.T) {
	_, err := ValidatePlanRestoreRequest(&adminv1.PlanRestoreRequest{
		Metadata: map[string]string{
			"large": strings.Repeat("x", MaxMetadataBytes),
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "targets", identity.ReasonRequired)
	requireViolation(t, violations, "metadata[large]", identity.ReasonTooLong)
	requireViolation(t, violations, "metadata", reasonMetadataTooLarge)
}

func TestValidatePlanPrewarmRequestValidatesPinUntil(t *testing.T) {
	_, err := ValidatePlanPrewarmRequest(&adminv1.PlanPrewarmRequest{
		Targets: []*adminv1.Target{blockAdminTarget()},
		PinUntil: &timestamppb.Timestamp{
			Seconds: 253402300800,
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "pin_until", reasonInvalidTimestamp)
}

func TestValidatePlanKeyRotationRequestRequiresDestinationKey(t *testing.T) {
	_, err := ValidatePlanKeyRotationRequest(&adminv1.PlanKeyRotationRequest{
		Targets: []*adminv1.Target{blockAdminTarget()},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "destination_key_id", identity.ReasonRequired)

	valid, err := ValidatePlanKeyRotationRequest(&adminv1.PlanKeyRotationRequest{
		Targets:          []*adminv1.Target{blockAdminTarget()},
		DestinationKeyId: "transit/backend-v2",
	})
	testutil.RequireNoErrorf(t, err, "unexpected error")
	if valid.Metadata[adminDestinationKeyIDMetadata] != "transit/backend-v2" {
		t.Fatalf("metadata = %#v, want destination key id", valid.Metadata)
	}
}

func TestValidatePlanCapacityOverrideRequestRequiresBoundedReason(t *testing.T) {
	_, err := ValidatePlanCapacityOverrideRequest(&adminv1.PlanCapacityOverrideRequest{
		CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "expires_at", identity.ReasonRequired)
	requireViolation(t, violations, "reason", identity.ReasonRequired)

	expiresAt := timestamppb.New(time.Unix(900, 0).UTC())
	valid, err := ValidatePlanCapacityOverrideRequest(&adminv1.PlanCapacityOverrideRequest{
		CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
		ExpiresAt:       expiresAt,
		Reason:          "incident INC-42",
	})
	testutil.RequireNoErrorf(t, err, "unexpected error")
	if valid.Metadata[adminCapacityProfileIDMetadata] != "production-a" ||
		valid.Metadata[adminCapacityOverrideReasonMetadata] != "incident INC-42" ||
		valid.Metadata[adminCapacityOverrideExpiresAtMetadata] != expiresAt.AsTime().Format(time.RFC3339Nano) ||
		len(valid.Warnings) != 1 {
		t.Fatalf("valid request = %#v, want capacity override metadata and warning", valid)
	}
}

func TestValidateStartRestoreRequestRequiresUUIDv7AndPlanToken(t *testing.T) {
	_, err := ValidateStartRestoreRequest(&adminv1.StartRestoreRequest{
		OperationId: "not-a-uuid",
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "operation_id", reasonInvalidUUIDv7)
	requireViolation(t, violations, "operation_plan_id", identity.ReasonRequired)
	requireViolation(t, violations, "plan_hash", identity.ReasonRequired)

	valid, err := ValidateStartRestoreRequest(&adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	testutil.RequireNoErrorf(t, err, "unexpected error")
	if valid.OperationID != validUUIDv7() {
		t.Fatalf("operation id = %q, want %q", valid.OperationID, validUUIDv7())
	}
}

func TestValidateCordonMemberRequestRequiresOperationAndReason(t *testing.T) {
	_, err := ValidateCordonMemberRequest(&adminv1.CordonMemberRequest{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "member-a"},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "operation_id", identity.ReasonRequired)
	requireViolation(t, violations, "reason", identity.ReasonRequired)

	valid, err := ValidateCordonMemberRequest(&adminv1.CordonMemberRequest{
		OperationId:   validUUIDv7(),
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "member-a"},
		Reason:        "node maintenance",
	})
	testutil.RequireNoErrorf(t, err, "unexpected error")
	if valid.StorageMember != "member-a" {
		t.Fatalf("storage member = %q, want member-a", valid.StorageMember)
	}
}

func blockAdminTarget() *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Block{
			Block: &adminv1.BlockTarget{
				ShardId: "shard-a",
				BlockId: "block-a",
			},
		},
	}
}

func validUUIDv7() string {
	return "018f6d86-7a22-7abc-8def-123456789abc"
}
