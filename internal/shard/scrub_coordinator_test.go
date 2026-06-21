package shard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

type scrubCoordinatorCoreStub struct {
	leader           bool
	requireLeaderErr error
	proposeErr       error
	proposed         chan []byte
	result           scrub.Result
	openBlockID      uint64
}

func (s *scrubCoordinatorCoreStub) Propose(ctx context.Context, data []byte) error {
	if s.proposeErr != nil {
		return s.proposeErr
	}
	if s.proposed == nil {
		return nil
	}
	select {
	case s.proposed <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *scrubCoordinatorCoreStub) IsLeader() bool {
	return s.leader
}

func (s *scrubCoordinatorCoreStub) requireLeader() error {
	if s.requireLeaderErr != nil {
		return s.requireLeaderErr
	}
	if !s.leader {
		return errors.New("not leader")
	}
	return nil
}

func (s *scrubCoordinatorCoreStub) scrubProjectionResult(scrubID string, entryIndex uint64) scrub.Result {
	result := s.result
	result.ScrubID = scrubID
	result.AppliedIndex = entryIndex
	return result
}

func (s *scrubCoordinatorCoreStub) currentOpenBlockID() uint64 {
	return s.openBlockID
}

func (s *scrubCoordinatorCoreStub) RestoreBlockForRepair(context.Context, uint64) error {
	return nil
}

func (s *scrubCoordinatorCoreStub) recordEvictionHealthBlockBestEffort(uint64) {}

func TestScrubCoordinatorProposeApplyAndCache(t *testing.T) {
	core := &scrubCoordinatorCoreStub{
		leader:   true,
		proposed: make(chan []byte, 1),
		result:   scrub.Result{SHA256: [32]byte{1, 2, 3}},
	}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resultCh := make(chan scrubCoordinatorResult, 1)
	go func() {
		result, err := c.ProposeConsistencyCheck(ctx, "scrub-001")
		resultCh <- scrubCoordinatorResult{result: result, err: err}
	}()

	cmd := readProposedScrubCommand(t, core.proposed)
	c.applyConsistencyCheck(cmd.GetConsistencyCheck(), 42)

	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatalf("ProposeConsistencyCheck: %v", outcome.err)
	}
	if outcome.result.ScrubID != "scrub-001" {
		t.Fatalf("ScrubID = %q, want scrub-001", outcome.result.ScrubID)
	}
	if outcome.result.AppliedIndex != 42 {
		t.Fatalf("AppliedIndex = %d, want 42", outcome.result.AppliedIndex)
	}
	if outcome.result.SHA256 != core.result.SHA256 {
		t.Fatal("SHA256 should come from the core projection result")
	}

	cached, ok := c.GetScrubResult("scrub-001")
	if !ok {
		t.Fatal("expected cached scrub result")
	}
	if cached != outcome.result {
		t.Fatalf("cached result = %+v, want %+v", cached, outcome.result)
	}
	if _, ok := c.GetScrubResult("missing"); ok {
		t.Fatal("expected no result for unknown scrub ID")
	}
}

func TestScrubCoordinatorContextCancelRemovesProposal(t *testing.T) {
	core := &scrubCoordinatorCoreStub{
		leader:   true,
		proposed: make(chan []byte, 1),
	}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan scrubCoordinatorResult, 1)
	go func() {
		result, err := c.ProposeConsistencyCheck(ctx, "scrub-cancel")
		resultCh <- scrubCoordinatorResult{result: result, err: err}
	}()

	_ = readProposedScrubCommand(t, core.proposed)
	cancel()

	outcome := <-resultCh
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", outcome.err)
	}

	c.mu.RLock()
	_, ok := c.proposals["scrub-cancel"]
	c.mu.RUnlock()
	if ok {
		t.Fatal("expected canceled proposal to be removed")
	}
}

func TestScrubCoordinatorApplyAfterStopIsSafe(t *testing.T) {
	core := &scrubCoordinatorCoreStub{
		leader: true,
		result: scrub.Result{SHA256: [32]byte{4, 5, 6}},
	}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	c.Stop()
	c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: "post-stop"}, 7)

	cached, ok := c.GetScrubResult("post-stop")
	if !ok {
		t.Fatal("expected post-stop apply to cache result")
	}
	if cached.AppliedIndex != 7 {
		t.Fatalf("AppliedIndex = %d, want 7", cached.AppliedIndex)
	}
}

func TestScrubCoordinatorRetainsOverlappingResultsByID(t *testing.T) {
	core := &scrubCoordinatorCoreStub{leader: true, result: scrub.Result{SHA256: [32]byte{9}}}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: "scrub-a"}, 1)
	c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: "scrub-b"}, 2)

	a, ok := c.GetScrubResult("scrub-a")
	if !ok || a.AppliedIndex != 1 {
		t.Fatalf("scrub-a result = %+v ok=%v, want AppliedIndex 1", a, ok)
	}
	b, ok := c.GetScrubResult("scrub-b")
	if !ok || b.AppliedIndex != 2 {
		t.Fatalf("scrub-b result = %+v ok=%v, want AppliedIndex 2", b, ok)
	}
}

func TestScrubCoordinatorRetentionEvictsOldestBeyondCap(t *testing.T) {
	core := &scrubCoordinatorCoreStub{leader: true}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	total := maxRetainedScrubResults + 5
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("scrub-%03d", i)
		c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: id}, uint64(i))
	}

	if _, ok := c.GetScrubResult("scrub-000"); ok {
		t.Fatal("expected oldest scrub result to be evicted")
	}
	newest := fmt.Sprintf("scrub-%03d", total-1)
	if _, ok := c.GetScrubResult(newest); !ok {
		t.Fatalf("expected newest scrub result %q to be retained", newest)
	}
	c.mu.RLock()
	n := len(c.results)
	c.mu.RUnlock()
	if n != maxRetainedScrubResults {
		t.Fatalf("retained results = %d, want %d", n, maxRetainedScrubResults)
	}
}

func TestScrubCoordinatorDuplicateScrubIDDoesNotHangFirstWaiter(t *testing.T) {
	core := &scrubCoordinatorCoreStub{
		leader:   true,
		proposed: make(chan []byte, 1),
		result:   scrub.Result{SHA256: [32]byte{7}},
	}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	firstCh := make(chan scrubCoordinatorResult, 1)
	go func() {
		result, err := c.ProposeConsistencyCheck(ctx, "dup")
		firstCh <- scrubCoordinatorResult{result: result, err: err}
	}()

	// The first proposal has registered once its command reaches the proposer.
	_ = readProposedScrubCommand(t, core.proposed)

	// A duplicate in-flight scrub ID is rejected deterministically and does not
	// displace the first waiter.
	if _, err := c.ProposeConsistencyCheck(ctx, "dup"); !errors.Is(err, ErrScrubInProgress) {
		t.Fatalf("duplicate proposal err = %v, want ErrScrubInProgress", err)
	}

	// The first waiter still receives its result on apply (did not hang).
	c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: "dup"}, 11)

	outcome := <-firstCh
	if outcome.err != nil {
		t.Fatalf("first waiter err = %v, want nil", outcome.err)
	}
	if outcome.result.AppliedIndex != 11 {
		t.Fatalf("first waiter AppliedIndex = %d, want 11", outcome.result.AppliedIndex)
	}
}

func TestScrubCoordinatorApplyDoesNotHoldLockDuringSend(t *testing.T) {
	core := &scrubCoordinatorCoreStub{leader: true}
	c := newScrubCoordinator(core, t.TempDir(), nil, nil)

	// Register an UNBUFFERED waiter so the apply send blocks until a receiver is
	// ready. If the coordinator held c.mu across the send, no other goroutine
	// could acquire the lock while apply is blocked.
	unbuffered := make(chan scrub.Result)
	c.mu.Lock()
	c.proposals["blocking"] = unbuffered
	c.mu.Unlock()

	applyDone := make(chan struct{})
	go func() {
		c.applyConsistencyCheck(&scrapv1.RequestConsistencyCheck{ScrubId: "blocking"}, 5)
		close(applyDone)
	}()

	// Prove the lock is free while apply is blocked on the send.
	lockFree := make(chan struct{})
	go func() {
		c.GetScrubResult("anything")
		close(lockFree)
	}()
	select {
	case <-lockFree:
	case <-time.After(2 * time.Second):
		t.Fatal("GetScrubResult blocked: coordinator held mutex across the send")
	}

	// Drain the blocking send so apply can finish.
	select {
	case <-unbuffered:
	case <-time.After(2 * time.Second):
		t.Fatal("apply did not reach the send")
	}
	<-applyDone
}

func TestScrubCoordinatorDefaultBlockRepairerSupportsBackendOnly(t *testing.T) {
	c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, t.TempDir(), nil, nil)
	repairer := c.defaultBlockRepairer(Config{
		Upload: UploadConfig{
			Enabled: true,
			Backend: backend.NewFS(t.TempDir()),
		},
	})
	if repairer == nil {
		t.Fatal("expected default Backend-only block repairer")
	}
}

func TestScrubCoordinatorDefaultBlockRepairerUsesConfiguredBackendWhenUploadsDisabled(t *testing.T) {
	c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, t.TempDir(), nil, nil)
	repairer := c.defaultBlockRepairer(Config{
		Upload: UploadConfig{
			Enabled: false,
			Backend: backend.NewFS(t.TempDir()),
		},
	})
	if repairer == nil {
		t.Fatal("expected Backend repairer when Backend is configured")
	}
}

func TestScrubCoordinatorDefaultBlockRepairerNilWithoutRepairSource(t *testing.T) {
	c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, t.TempDir(), nil, nil)
	if repairer := c.defaultBlockRepairer(Config{}); repairer != nil {
		t.Fatalf("defaultBlockRepairer = %T, want nil", repairer)
	}
}

func TestScrubCoordinatorListsEvictedBlocksForDeepScrubSkip(t *testing.T) {
	dir := t.TempDir()
	core := &scrubCoordinatorCoreStub{openBlockID: 99}
	c := newScrubCoordinator(core, dir, nil, nil)

	if err := os.WriteFile(block.IdxFilePath(dir, 7), []byte("idx retained"), 0o600); err != nil {
		t.Fatalf("write retained idx: %v", err)
	}
	if err := WriteEvictionMarker(dir, EvictionMarker{
		BlockID:         7,
		BackendKey:      "cell-a/shards/0/7.blk",
		SizeBytes:       123,
		ValidationToken: "etag",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	blocks, err := c.ListSealedBlocks(0)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].BlockID != 7 {
		t.Fatalf("blocks = %+v, want evicted Block 7 listed for scrub skip", blocks)
	}

	state, err := c.ClassifyScrubBlock(7)
	if err != nil {
		t.Fatalf("ClassifyScrubBlock: %v", err)
	}
	if state != scrub.BlockLocalStateEvicted {
		t.Fatalf("state = %s, want evicted", state)
	}
}

func TestScrubCoordinatorClassifyScrubBlockMapsLifecycleStates(t *testing.T) {
	tests := []struct {
		name    string
		blockID uint64
		stage   func(*testing.T, string, uint64)
		want    scrub.BlockLocalState
	}{
		{
			name:    "hot",
			blockID: 1,
			stage: func(t *testing.T, dir string, blockID uint64) {
				t.Helper()
				writeScrubLifecycleFile(t, block.FilePath(dir, blockID))
				writeScrubLifecycleFile(t, block.IdxFilePath(dir, blockID))
			},
			want: scrub.BlockLocalStateHot,
		},
		{
			name:    "hot cleanup needed",
			blockID: 2,
			stage: func(t *testing.T, dir string, blockID uint64) {
				t.Helper()
				writeScrubLifecycleFile(t, block.FilePath(dir, blockID))
				writeScrubLifecycleFile(t, block.IdxFilePath(dir, blockID))
				writeScrubEvictionMarker(t, dir, blockID)
			},
			want: scrub.BlockLocalStateHotCleanupNeeded,
		},
		{
			name:    "metadata loss",
			blockID: 3,
			stage: func(t *testing.T, dir string, blockID uint64) {
				t.Helper()
				writeScrubLifecycleFile(t, block.FilePath(dir, blockID))
			},
			want: scrub.BlockLocalStateMetadataLoss,
		},
		{
			name:    "unexpected loss",
			blockID: 4,
			stage: func(t *testing.T, dir string, blockID uint64) {
				t.Helper()
				writeScrubLifecycleFile(t, block.IdxFilePath(dir, blockID))
			},
			want: scrub.BlockLocalStateUnexpectedLoss,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, dir, nil, nil)
			tt.stage(t, dir, tt.blockID)

			got, err := c.ClassifyScrubBlock(tt.blockID)
			if err != nil {
				t.Fatalf("ClassifyScrubBlock: %v", err)
			}
			if got != tt.want {
				t.Fatalf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

func writeScrubLifecycleFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("scrub lifecycle"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeScrubEvictionMarker(t *testing.T, dir string, blockID uint64) {
	t.Helper()

	if err := WriteEvictionMarker(dir, EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0/7.blk",
		SizeBytes:       123,
		ValidationToken: "etag",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}

type scrubCoordinatorResult struct {
	result scrub.Result
	err    error
}

func readProposedScrubCommand(t *testing.T, proposed <-chan []byte) *scrapv1.RaftCommand {
	t.Helper()

	select {
	case data := <-proposed:
		cmd := &scrapv1.RaftCommand{}
		if err := proto.Unmarshal(data, cmd); err != nil {
			t.Fatalf("unmarshal proposed command: %v", err)
		}
		if cmd.GetConsistencyCheck() == nil {
			t.Fatal("expected consistency-check command")
		}
		return cmd
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proposed command")
		return nil
	}
}
