package replication

import (
	"context"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestPrepareNormalWriteSucceedsWithQuorumAndMarksRepairRequired(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	peerErr := errors.New("peer prepare failed")
	result, err := PrepareDocument(context.Background(), document, []Target{
		successTarget("member-1", document),
		failingTarget("member-2", peerErr),
		successTarget("member-3", document),
		failingTarget("member-4", peerErr),
	}, DefaultPolicy())
	if err != nil {
		t.Fatalf("prepare document: %v", err)
	}
	if result.DesiredReplicaCount != 5 || result.RequiredReplicaCount != 3 || result.AchievedReplicaCount != 3 {
		t.Fatalf("replica counts = desired %d required %d achieved %d, want 5/3/3", result.DesiredReplicaCount, result.RequiredReplicaCount, result.AchievedReplicaCount)
	}
	if !result.RepairRequired {
		t.Fatal("repair required = false, want true after quorum-only prepare")
	}
	if result.Degraded {
		t.Fatal("normal priority prepare was marked degraded")
	}
	if len(result.Receipts) != 2 || len(result.PeerErrors) != 2 {
		t.Fatalf("receipts/errors = %d/%d, want 2/2", len(result.Receipts), len(result.PeerErrors))
	}
}

func TestPrepareCriticalWriteRequiresAllReplicasByDefault(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassCriticalIngest)
	result, err := PrepareDocument(context.Background(), document, []Target{
		successTarget("member-1", document),
		successTarget("member-2", document),
		successTarget("member-3", document),
		failingTarget("member-4", errors.New("peer unavailable")),
	}, DefaultPolicy())
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("prepare error = %v, want %v", err, ErrInsufficientReplicas)
	}
	if result.RequiredReplicaCount != 5 || result.AchievedReplicaCount != 4 {
		t.Fatalf("replica counts = required %d achieved %d, want 5/4", result.RequiredReplicaCount, result.AchievedReplicaCount)
	}
	if result.Degraded {
		t.Fatal("critical prepare degraded without explicit critical quorum policy")
	}
}

func TestPrepareCriticalWriteCanDegradeToQuorumWhenPolicyAllows(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassCriticalIngest)
	result, err := PrepareDocument(context.Background(), document, []Target{
		successTarget("member-1", document),
		failingTarget("member-2", errors.New("peer unavailable")),
		successTarget("member-3", document),
		failingTarget("member-4", errors.New("peer unavailable")),
	}, Policy{TargetReplicaCount: 5, QuorumReplicaCount: 3, AllowCriticalQuorumDegrade: true})
	if err != nil {
		t.Fatalf("prepare document: %v", err)
	}
	if result.RequiredReplicaCount != 3 || result.AchievedReplicaCount != 3 {
		t.Fatalf("replica counts = required %d achieved %d, want 3/3", result.RequiredReplicaCount, result.AchievedReplicaCount)
	}
	if !result.RepairRequired || !result.Degraded {
		t.Fatalf("repair/degraded = %v/%v, want true/true", result.RepairRequired, result.Degraded)
	}
}

func TestPrepareFailsWhenPeerTargetsCannotReachQuorum(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	result, err := PrepareDocument(context.Background(), document, []Target{
		successTarget("member-1", document),
	}, DefaultPolicy())
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("prepare error = %v, want %v", err, ErrInsufficientReplicas)
	}
	if result.AchievedReplicaCount != 1 {
		t.Fatalf("achieved replicas = %d, want local replica only before prepare attempts", result.AchievedReplicaCount)
	}
}

func TestPrepareRejectsMismatchedReceipt(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	mismatched := document
	mismatched.BlockID = "wrong-block"
	result, err := PrepareDocument(context.Background(), document, []Target{
		successTarget("member-1", mismatched),
		successTarget("member-2", document),
	}, DefaultPolicy())
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("prepare error = %v, want %v", err, ErrInsufficientReplicas)
	}
	if result.AchievedReplicaCount != 2 {
		t.Fatalf("achieved replicas = %d, want local + one valid peer", result.AchievedReplicaCount)
	}
	if len(result.PeerErrors) != 1 || !errors.Is(result.PeerErrors[0].Err, ErrReceiptMismatch) {
		t.Fatalf("peer errors = %#v, want one receipt mismatch", result.PeerErrors)
	}
}

func testPreparedDocument(priority metastore.PriorityClass) PreparedDocument {
	logical := [32]byte{1, 2, 3}
	stored := [32]byte{4, 5, 6}
	frame := [32]byte{7, 8, 9}
	return PreparedDocument{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  "invoice.xml",
		},
		PriorityClass: priority,
		BlockID:       "block-1",
		StoredOffset:  64,
		StoredLength:  42,
		LogicalSHA256: logical,
		StoredSHA256:  stored,
		Frames: []blockstore.FrameRecord{
			{
				FrameOffset:   64,
				SegmentOffset: 64,
				SegmentLength: 42,
				SHA256:        frame,
			},
		},
	}
}

func successTarget(memberID string, document PreparedDocument) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(context.Context, PreparedDocument) (Receipt, error) {
			return Receipt{
				MemberID:      memberID,
				BlockID:       document.BlockID,
				StoredOffset:  document.StoredOffset,
				StoredLength:  document.StoredLength,
				LogicalSHA256: document.LogicalSHA256,
				StoredSHA256:  document.StoredSHA256,
			}, nil
		}),
	}
}

func failingTarget(memberID string, err error) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(context.Context, PreparedDocument) (Receipt, error) {
			return Receipt{}, err
		}),
	}
}
