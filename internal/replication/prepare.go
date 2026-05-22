package replication

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

const (
	defaultTargetReplicaCount = 5
	defaultQuorumReplicaCount = 3
)

var (
	ErrInsufficientReplicas = errors.New("replication: insufficient prepared replicas")
	ErrReceiptMismatch      = errors.New("replication: prepare receipt mismatch")
)

type Policy struct {
	TargetReplicaCount         int
	QuorumReplicaCount         int
	AllowCriticalQuorumDegrade bool
}

type PreparedDocument struct {
	Identity      identity.Document
	PriorityClass metastore.PriorityClass
	BlockID       string
	StoredOffset  uint64
	StoredLength  uint64
	LogicalSHA256 [32]byte
	StoredSHA256  [32]byte
	Frames        []blockstore.FrameRecord
}

type Target struct {
	MemberID string
	Preparer Preparer
}

type Preparer interface {
	PrepareDocument(context.Context, PreparedDocument) (Receipt, error)
}

type PreparerFunc func(context.Context, PreparedDocument) (Receipt, error)

func (f PreparerFunc) PrepareDocument(ctx context.Context, document PreparedDocument) (Receipt, error) {
	return f(ctx, document)
}

type Receipt struct {
	MemberID      string
	BlockID       string
	StoredOffset  uint64
	StoredLength  uint64
	LogicalSHA256 [32]byte
	StoredSHA256  [32]byte
}

type Result struct {
	DesiredReplicaCount  int
	RequiredReplicaCount int
	AchievedReplicaCount int
	RepairRequired       bool
	Degraded             bool
	Receipts             []Receipt
	PeerErrors           []PeerError
}

type PeerError struct {
	MemberID string
	Err      error
}

func DefaultPolicy() Policy {
	return Policy{
		TargetReplicaCount: defaultTargetReplicaCount,
		QuorumReplicaCount: defaultQuorumReplicaCount,
	}
}

func PreparedDocumentFromMetadata(document metastore.Document) PreparedDocument {
	return PreparedDocument{
		Identity:      document.Identity,
		PriorityClass: document.PriorityClass,
		BlockID:       document.Location.BlockID,
		StoredOffset:  document.Location.StoredOffset,
		StoredLength:  document.Location.StoredLength,
		LogicalSHA256: document.LogicalSHA256,
		StoredSHA256:  document.StoredSHA256,
		Frames:        append([]blockstore.FrameRecord(nil), document.Location.Frames...),
	}
}

func PrepareDocument(ctx context.Context, document PreparedDocument, targets []Target, policy Policy) (Result, error) {
	policy = normalizePolicy(policy)
	required := requiredReplicaCount(document.PriorityClass, policy)
	result := Result{
		DesiredReplicaCount:  policy.TargetReplicaCount,
		RequiredReplicaCount: required,
		AchievedReplicaCount: 1,
	}
	if len(targets)+1 < required {
		result.RepairRequired = true
		return result, ErrInsufficientReplicas
	}

	for _, target := range targets {
		if result.AchievedReplicaCount >= policy.TargetReplicaCount {
			break
		}
		if target.MemberID == "" || target.Preparer == nil {
			result.PeerErrors = append(result.PeerErrors, PeerError{MemberID: target.MemberID, Err: ErrReceiptMismatch})
			continue
		}
		if err := ctx.Err(); err != nil {
			result.PeerErrors = append(result.PeerErrors, PeerError{MemberID: target.MemberID, Err: err})
			break
		}
		receipt, err := target.Preparer.PrepareDocument(ctx, document)
		if err != nil {
			result.PeerErrors = append(result.PeerErrors, PeerError{MemberID: target.MemberID, Err: err})
			continue
		}
		if err := validateReceipt(document, target.MemberID, receipt); err != nil {
			result.PeerErrors = append(result.PeerErrors, PeerError{MemberID: target.MemberID, Err: err})
			continue
		}
		result.Receipts = append(result.Receipts, receipt)
		result.AchievedReplicaCount++
	}

	if result.AchievedReplicaCount < policy.TargetReplicaCount {
		result.RepairRequired = true
	}
	if result.AchievedReplicaCount < required {
		return result, ErrInsufficientReplicas
	}
	result.Degraded = result.RepairRequired && document.PriorityClass == metastore.PriorityClassCriticalIngest && policy.AllowCriticalQuorumDegrade
	return result, nil
}

func normalizePolicy(policy Policy) Policy {
	if policy.TargetReplicaCount == 0 {
		policy.TargetReplicaCount = defaultTargetReplicaCount
	}
	if policy.QuorumReplicaCount == 0 {
		policy.QuorumReplicaCount = defaultQuorumReplicaCount
	}
	return policy
}

func requiredReplicaCount(priority metastore.PriorityClass, policy Policy) int {
	if priority == metastore.PriorityClassCriticalIngest && !policy.AllowCriticalQuorumDegrade {
		return policy.TargetReplicaCount
	}
	return policy.QuorumReplicaCount
}

func validateReceipt(document PreparedDocument, memberID string, receipt Receipt) error {
	if receipt.MemberID != memberID {
		return fmt.Errorf("%w: receipt member %q, target member %q", ErrReceiptMismatch, receipt.MemberID, memberID)
	}
	if receipt.BlockID != document.BlockID || receipt.StoredOffset != document.StoredOffset || receipt.StoredLength != document.StoredLength {
		return fmt.Errorf("%w: receipt location does not match prepared document", ErrReceiptMismatch)
	}
	if !bytes.Equal(receipt.LogicalSHA256[:], document.LogicalSHA256[:]) || !bytes.Equal(receipt.StoredSHA256[:], document.StoredSHA256[:]) {
		return fmt.Errorf("%w: receipt checksum does not match prepared document", ErrReceiptMismatch)
	}
	return nil
}
