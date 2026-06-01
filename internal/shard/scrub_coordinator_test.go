package shard

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
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
