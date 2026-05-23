package replication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

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
	ErrMissingByteSource    = errors.New("replication: prepared byte source is required")
	ErrTransferMismatch     = errors.New("replication: prepare transfer mismatch")
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
	PrepareDocument(context.Context, PrepareRequest) (Receipt, error)
}

type PreparerFunc func(context.Context, PrepareRequest) (Receipt, error)

func (f PreparerFunc) PrepareDocument(ctx context.Context, request PrepareRequest) (Receipt, error) {
	return f(ctx, request)
}

type ByteSource interface {
	WritePreparedDocument(context.Context, PreparedDocument, io.Writer) error
}

type ByteSourceFunc func(context.Context, PreparedDocument, io.Writer) error

func (f ByteSourceFunc) WritePreparedDocument(ctx context.Context, document PreparedDocument, writer io.Writer) error {
	return f(ctx, document, writer)
}

type PrepareRequest struct {
	Document PreparedDocument
	Source   ByteSource
}

func (r PrepareRequest) WriteBytes(ctx context.Context, writer io.Writer) error {
	if r.Source == nil {
		return ErrMissingByteSource
	}
	return r.Source.WritePreparedDocument(ctx, r.Document, writer)
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

func PrepareDocument(ctx context.Context, document PreparedDocument, source ByteSource, targets []Target, policy Policy) (Result, error) {
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
	if len(targets) > 0 && source == nil {
		result.RepairRequired = true
		return result, ErrMissingByteSource
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
		receipt, err := target.Preparer.PrepareDocument(ctx, PrepareRequest{
			Document: document,
			Source:   source,
		})
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

func ValidatePreparedBytes(document PreparedDocument, data []byte) error {
	if uint64(len(data)) != document.StoredLength {
		return fmt.Errorf("%w: transferred length = %d, stored length = %d", ErrTransferMismatch, len(data), document.StoredLength)
	}
	got := sha256.Sum256(data)
	if got != document.StoredSHA256 {
		return fmt.Errorf("%w: stored checksum does not match prepared document", ErrTransferMismatch)
	}
	for i, frame := range document.Frames {
		if frame.SegmentOffset < document.StoredOffset {
			return fmt.Errorf("%w: frame %d starts before prepared range", ErrTransferMismatch, i)
		}
		relativeStart := frame.SegmentOffset - document.StoredOffset
		relativeEnd := relativeStart + frame.SegmentLength
		if relativeEnd < relativeStart || relativeEnd > uint64(len(data)) {
			return fmt.Errorf("%w: frame %d is outside prepared range", ErrTransferMismatch, i)
		}
		frameSHA := sha256.Sum256(data[relativeStart:relativeEnd])
		if frameSHA != frame.SHA256 {
			return fmt.Errorf("%w: frame %d checksum does not match prepared document", ErrTransferMismatch, i)
		}
	}
	return nil
}

func ReceiptFromPreparedDocument(memberID string, document PreparedDocument) Receipt {
	return Receipt{
		MemberID:      memberID,
		BlockID:       document.BlockID,
		StoredOffset:  document.StoredOffset,
		StoredLength:  document.StoredLength,
		LogicalSHA256: document.LogicalSHA256,
		StoredSHA256:  document.StoredSHA256,
	}
}

func ReplicaRefs(receipts []Receipt) []blockstore.ReplicaRef {
	replicas := make([]blockstore.ReplicaRef, 0, len(receipts))
	for _, receipt := range receipts {
		replicas = append(replicas, blockstore.ReplicaRef{
			MemberID:     receipt.MemberID,
			BlockID:      receipt.BlockID,
			StoredOffset: receipt.StoredOffset,
			StoredLength: receipt.StoredLength,
			StoredSHA256: receipt.StoredSHA256,
		})
	}
	return replicas
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
