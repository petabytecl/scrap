package replication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPrepareNormalWriteSucceedsWithQuorumAndMarksRepairRequired(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	source := testByteSource(testPreparedBytes)
	peerErr := errors.New("peer prepare failed")
	result, err := PrepareDocument(context.Background(), document, source, []Target{
		successTarget("member-1", document),
		failingTarget("member-2", peerErr),
		successTarget("member-3", document),
		failingTarget("member-4", peerErr),
	}, DefaultPolicy())
	testutil.RequireNoErrorf(t, err, "prepare document")
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
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
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
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
		successTarget("member-1", document),
		failingTarget("member-2", errors.New("peer unavailable")),
		successTarget("member-3", document),
		failingTarget("member-4", errors.New("peer unavailable")),
	}, Policy{TargetReplicaCount: 5, QuorumReplicaCount: 3, AllowCriticalQuorumDegrade: true})
	testutil.RequireNoErrorf(t, err, "prepare document")
	if result.RequiredReplicaCount != 3 || result.AchievedReplicaCount != 3 {
		t.Fatalf("replica counts = required %d achieved %d, want 3/3", result.RequiredReplicaCount, result.AchievedReplicaCount)
	}
	if !result.RepairRequired || !result.Degraded {
		t.Fatalf("repair/degraded = %v/%v, want true/true", result.RepairRequired, result.Degraded)
	}
}

func TestPreparePeerPreparesUseConfiguredConcurrency(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan prepareResult, 1)
	go func() {
		result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
			blockingSuccessTarget("member-1", document, started, release),
			blockingSuccessTarget("member-2", document, started, release),
		}, Policy{
			TargetReplicaCount:    3,
			QuorumReplicaCount:    3,
			PrepareTimeout:        time.Second,
			MaxConcurrentPrepares: 2,
		})
		done <- prepareResult{result: result, err: err}
	}()

	requireStartedMembers(t, started, "member-1", "member-2")
	close(release)
	outcome := requirePrepareResult(t, done)
	testutil.RequireNoErrorf(t, outcome.err, "prepare document")
	if outcome.result.AchievedReplicaCount != 3 || outcome.result.RepairRequired {
		t.Fatalf("result achieved/repair = %d/%v, want 3/false", outcome.result.AchievedReplicaCount, outcome.result.RepairRequired)
	}
}

func TestPreparePeerTimeoutDoesNotStallQuorum(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
		hangingTarget("member-1"),
		successTarget("member-2", document),
	}, Policy{
		TargetReplicaCount:    3,
		QuorumReplicaCount:    2,
		PrepareTimeout:        50 * time.Millisecond,
		MaxConcurrentPrepares: 2,
	})
	testutil.RequireNoErrorf(t, err, "prepare document")
	if result.AchievedReplicaCount != 2 || !result.RepairRequired {
		t.Fatalf("result = %#v, want quorum with repair required", result)
	}
	if len(result.PeerErrors) != 1 || !errors.Is(result.PeerErrors[0].Err, context.DeadlineExceeded) {
		t.Fatalf("peer errors = %#v, want one deadline exceeded error", result.PeerErrors)
	}
}

func TestPrepareFailsWhenPeerTargetsCannotReachQuorum(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
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
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
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

func TestPrepareTransfersRangeAndChecksumEvidence(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	peer := newMemoryPeer("member-1")
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
		{MemberID: "member-1", Preparer: peer},
	}, Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2})
	testutil.RequireNoErrorf(t, err, "prepare document")
	if result.DesiredReplicaCount != 2 || result.RequiredReplicaCount != 2 || result.AchievedReplicaCount != 2 {
		t.Fatalf("replica counts = desired %d required %d achieved %d, want 2/2/2", result.DesiredReplicaCount, result.RequiredReplicaCount, result.AchievedReplicaCount)
	}
	if peer.prepareCount != 1 {
		t.Fatalf("prepare count = %d, want 1", peer.prepareCount)
	}
	if len(peer.bytesByDocument) != 1 {
		t.Fatalf("prepared documents = %d, want 1", len(peer.bytesByDocument))
	}
}

func TestPrepareCanRetryAfterPeerFailure(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	peer := newMemoryPeer("member-1")
	flaky := &flakyPreparer{nextErr: errors.New("peer unavailable"), next: peer}
	result, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
		{MemberID: "member-1", Preparer: flaky},
	}, Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2})
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("first prepare error = %v, want %v", err, ErrInsufficientReplicas)
	}
	if result.AchievedReplicaCount != 1 || len(result.PeerErrors) != 1 {
		t.Fatalf("first result = %#v, want only local replica and one peer error", result)
	}

	result, err = PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), []Target{
		{MemberID: "member-1", Preparer: flaky},
	}, Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2})
	testutil.RequireNoErrorf(t, err, "retry prepare document")
	if result.AchievedReplicaCount != 2 {
		t.Fatalf("retry achieved replicas = %d, want 2", result.AchievedReplicaCount)
	}
}

func TestPrepareDuplicateIsIdempotent(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	peer := newMemoryPeer("member-1")
	targets := []Target{{MemberID: "member-1", Preparer: peer}}
	policy := Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	first, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), targets, policy)
	testutil.RequireNoErrorf(t, err, "first prepare document")
	second, err := PrepareDocument(context.Background(), document, testByteSource(testPreparedBytes), targets, policy)
	testutil.RequireNoErrorf(t, err, "duplicate prepare document")
	if len(first.Receipts) != 1 || len(second.Receipts) != 1 || first.Receipts[0] != second.Receipts[0] {
		t.Fatalf("duplicate receipts = %#v then %#v, want same receipt", first.Receipts, second.Receipts)
	}
	if peer.prepareCount != 2 {
		t.Fatalf("prepare count = %d, want duplicate call to be accepted", peer.prepareCount)
	}
}

func TestPrepareRejectsCorruptTransfer(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	result, err := PrepareDocument(context.Background(), document, testByteSource([]byte("corrupt bytes")), []Target{
		successTarget("member-1", document),
	}, Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2})
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("prepare error = %v, want %v", err, ErrInsufficientReplicas)
	}
	if len(result.PeerErrors) != 1 || !errors.Is(result.PeerErrors[0].Err, ErrTransferMismatch) {
		t.Fatalf("peer errors = %#v, want transfer mismatch", result.PeerErrors)
	}
}

func TestValidatePreparedBytesAllowsStoredHashDifferentFromLogicalHash(t *testing.T) {
	document := testPreparedDocument(metastore.PriorityClassNormal)
	document.LogicalSHA256 = [32]byte{9, 8, 7}
	testutil.RequireNoErrorf(t, ValidatePreparedBytes(document, testPreparedBytes), "validate prepared bytes with different logical hash")
}

var testPreparedBytes = []byte("replicated document bytes")

func testPreparedDocument(priority metastore.PriorityClass) PreparedDocument {
	logical := sha256Bytes(testPreparedBytes)
	stored := logical
	frame := logical
	return PreparedDocument{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  "invoice.xml",
		},
		PriorityClass: priority,
		BlockID:       "block-1",
		StoredOffset:  64,
		StoredLength:  uint64(len(testPreparedBytes)),
		LogicalSHA256: logical,
		StoredSHA256:  stored,
		Frames: []blockstore.FrameRecord{
			{
				FrameOffset:   64,
				SegmentOffset: 64,
				SegmentLength: uint64(len(testPreparedBytes)),
				SHA256:        frame,
			},
		},
	}
}

func successTarget(memberID string, document PreparedDocument) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(ctx context.Context, request PrepareRequest) (Receipt, error) {
			var buf bytes.Buffer
			if err := request.WriteBytes(ctx, &buf); err != nil {
				return Receipt{}, err
			}
			if err := ValidatePreparedBytes(request.Document, buf.Bytes()); err != nil {
				return Receipt{}, err
			}
			return ReceiptFromPreparedDocument(memberID, document), nil
		}),
	}
}

func failingTarget(memberID string, err error) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(context.Context, PrepareRequest) (Receipt, error) {
			return Receipt{}, err
		}),
	}
}

func blockingSuccessTarget(memberID string, document PreparedDocument, started chan<- string, release <-chan struct{}) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(ctx context.Context, request PrepareRequest) (Receipt, error) {
			select {
			case started <- memberID:
			case <-ctx.Done():
				return Receipt{}, ctx.Err()
			}
			select {
			case <-release:
			case <-ctx.Done():
				return Receipt{}, ctx.Err()
			}
			var buf bytes.Buffer
			if err := request.WriteBytes(ctx, &buf); err != nil {
				return Receipt{}, err
			}
			if err := ValidatePreparedBytes(request.Document, buf.Bytes()); err != nil {
				return Receipt{}, err
			}
			return ReceiptFromPreparedDocument(memberID, document), nil
		}),
	}
}

func hangingTarget(memberID string) Target {
	return Target{
		MemberID: memberID,
		Preparer: PreparerFunc(func(ctx context.Context, _ PrepareRequest) (Receipt, error) {
			<-ctx.Done()
			return Receipt{}, ctx.Err()
		}),
	}
}

type prepareResult struct {
	result Result
	err    error
}

func requireStartedMembers(t *testing.T, started <-chan string, members ...string) {
	t.Helper()
	seen := make(map[string]bool, len(members))
	for range members {
		select {
		case memberID := <-started:
			seen[memberID] = true
		case <-time.After(time.Second):
			t.Fatalf("started members = %#v, want %v", seen, members)
		}
	}
	for _, memberID := range members {
		if !seen[memberID] {
			t.Fatalf("started members = %#v, missing %s", seen, memberID)
		}
	}
}

func requirePrepareResult(t *testing.T, done <-chan prepareResult) prepareResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("prepare document did not finish")
		return prepareResult{}
	}
}

func testByteSource(data []byte) ByteSource {
	return ByteSourceFunc(func(ctx context.Context, _ PreparedDocument, writer io.Writer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := writer.Write(data)
		return err
	})
}

func sha256Bytes(data []byte) [32]byte {
	return sha256.Sum256(data)
}

type memoryPeer struct {
	memberID        string
	prepareCount    int
	receipts        map[string]Receipt
	bytesByDocument map[string][]byte
}

func newMemoryPeer(memberID string) *memoryPeer {
	return &memoryPeer{
		memberID:        memberID,
		receipts:        make(map[string]Receipt),
		bytesByDocument: make(map[string][]byte),
	}
}

func (p *memoryPeer) PrepareDocument(ctx context.Context, request PrepareRequest) (Receipt, error) {
	p.prepareCount++
	var buf bytes.Buffer
	if err := request.WriteBytes(ctx, &buf); err != nil {
		return Receipt{}, err
	}
	data := append([]byte(nil), buf.Bytes()...)
	if err := ValidatePreparedBytes(request.Document, data); err != nil {
		return Receipt{}, err
	}
	key := documentKey(request.Document)
	receipt := ReceiptFromPreparedDocument(p.memberID, request.Document)
	if existing, ok := p.receipts[key]; ok {
		if existing != receipt || !bytes.Equal(p.bytesByDocument[key], data) {
			return Receipt{}, ErrReceiptMismatch
		}
		return existing, nil
	}
	p.receipts[key] = receipt
	p.bytesByDocument[key] = data
	return receipt, nil
}

type flakyPreparer struct {
	nextErr error
	next    Preparer
}

func (p *flakyPreparer) PrepareDocument(ctx context.Context, request PrepareRequest) (Receipt, error) {
	if p.nextErr != nil {
		err := p.nextErr
		p.nextErr = nil
		return Receipt{}, err
	}
	return p.next.PrepareDocument(ctx, request)
}

func documentKey(document PreparedDocument) string {
	return document.Identity.TenantID + "/" + document.Identity.TransactionID + "/" + document.Identity.DocumentName
}
