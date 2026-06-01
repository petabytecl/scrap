package scrub_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

type transferPayload struct {
	blk []byte
	idx []byte
}

type recordingBlockTransferer struct {
	calls    []transferCall
	payloads map[uint64]transferPayload
	err      error
}

type transferCall struct {
	addr    string
	blockID uint64
}

func (t *recordingBlockTransferer) TransferBlock(_ context.Context, addr string, blockID uint64) ([]byte, []byte, error) {
	t.calls = append(t.calls, transferCall{addr: addr, blockID: blockID})
	if t.err != nil {
		return nil, nil, t.err
	}
	payload, ok := t.payloads[blockID]
	if !ok {
		return nil, nil, errors.New("missing replacement")
	}
	return bytes.Clone(payload.blk), bytes.Clone(payload.idx), nil
}

type recordingBackendRestorer struct {
	calls     []uint64
	err       error
	onRestore func(uint64) error
}

func (r *recordingBackendRestorer) RestoreBlockForRepair(_ context.Context, blockID uint64) error {
	r.calls = append(r.calls, blockID)
	if r.err != nil {
		return r.err
	}
	if r.onRestore != nil {
		return r.onRestore(blockID)
	}
	return nil
}

func TestBlockRepair_FailedPeerTransferLeavesQuarantineIntact(t *testing.T) {
	dir := t.TempDir()
	quarantineBlock(t, dir, 7)
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{err: errors.New("peer unavailable")}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireQuarantined(t, dir, 7)
	requireNoPromotedBlock(t, dir, 7)
	if len(transferer.calls) != 1 {
		t.Fatalf("transfer calls = %d, want 1", len(transferer.calls))
	}
	if metrics.repairsFailed != 1 || metrics.repairsOK != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=0 failed=1", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_FallsBackToBackendWhenPeersAreEvicted(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(14)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("backend replacement"))
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{err: scrub.ErrPeerBlockEvicted}
	restorer := &recordingBackendRestorer{
		onRestore: func(blockID uint64) error {
			promoteBackendRepairReplacement(t, dir, blockID, replacement)
			return nil
		},
	}
	repair := scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:       dir,
		Transferer:      transferer,
		BackendRestorer: restorer,
		Metrics:         metrics,
		PeerAddrs:       []string{"member-a:9091", "member-b:9091"},
	})

	repair.RepairQuarantined(context.Background())

	if len(transferer.calls) != 2 {
		t.Fatalf("transfer calls = %d, want both peers tried", len(transferer.calls))
	}
	if len(restorer.calls) != 1 || restorer.calls[0] != blockID {
		t.Fatalf("backend restore calls = %v, want [%d]", restorer.calls, blockID)
	}
	requireNotQuarantined(t, dir, blockID)
	if metrics.repairsOK != 1 || metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=1 failed=0", metrics.repairsOK, metrics.repairsFailed)
	}
	if metrics.decremented != 1 {
		t.Fatalf("quarantine gauge decremented %d times, want 1", metrics.decremented)
	}
}

func TestBlockRepair_FallsBackToBackendWithoutPeers(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(15)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("backend only replacement"))
	metrics := &deepScrubMetrics{}
	restorer := &recordingBackendRestorer{
		onRestore: func(blockID uint64) error {
			promoteBackendRepairReplacement(t, dir, blockID, replacement)
			return nil
		},
	}
	repair := scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:       dir,
		BackendRestorer: restorer,
		Metrics:         metrics,
	})

	repair.RepairQuarantined(context.Background())

	if len(restorer.calls) != 1 || restorer.calls[0] != blockID {
		t.Fatalf("backend restore calls = %v, want [%d]", restorer.calls, blockID)
	}
	requireNotQuarantined(t, dir, blockID)
	if metrics.repairsOK != 1 || metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=1 failed=0", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_BackendFallbackFailureLeavesQuarantineIntact(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(16)
	quarantineBlock(t, dir, blockID)
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{err: scrub.ErrPeerBlockEvicted}
	restorer := &recordingBackendRestorer{err: errors.New("backend unavailable")}
	repair := scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:       dir,
		Transferer:      transferer,
		BackendRestorer: restorer,
		Metrics:         metrics,
		PeerAddrs:       []string{"member-a:9091"},
	})

	repair.RepairQuarantined(context.Background())

	requireQuarantined(t, dir, blockID)
	if metrics.repairsOK != 0 || metrics.repairsFailed != 1 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=0 failed=1", metrics.repairsOK, metrics.repairsFailed)
	}
	if metrics.decremented != 0 {
		t.Fatalf("quarantine gauge decremented %d times, want 0", metrics.decremented)
	}
}

func TestBlockRepair_CorruptReplacementDeletedAndQuarantineRemains(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(8)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("clean replacement"))
	replacement.blk[block.HeaderSize+block.FrameHeaderSize+1] ^= 0xff
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireQuarantined(t, dir, blockID)
	requireNoPromotedBlock(t, dir, blockID)
	matches, err := filepath.Glob(filepath.Join(dir, "*.repair"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair staging files remain: %v", matches)
	}
	if metrics.repairsFailed != 1 || metrics.repairsOK != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=0 failed=1", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_InvalidReplacementIndexDeletedAndQuarantineRemains(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(11)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("replacement with invalid index"))
	replacement.idx = []byte("not an index")
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireQuarantined(t, dir, blockID)
	requireNoPromotedBlock(t, dir, blockID)
	requireNoRepairStaging(t, dir)
	if metrics.repairsFailed != 1 || metrics.repairsOK != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=0 failed=1", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_VerifiedReplacementRemovesQuarantineFiles(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(9)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("verified replacement"))
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireNotQuarantined(t, dir, blockID)
	result, err := block.VerifyBlock(block.FilePath(dir, blockID), block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) != 0 {
		t.Fatalf("promoted replacement is corrupt: %v", result.CorruptFrames)
	}
	if metrics.repairsOK != 1 || metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=1 failed=0", metrics.repairsOK, metrics.repairsFailed)
	}
	if metrics.decremented != 1 {
		t.Fatalf("quarantine gauge decremented %d times, want 1", metrics.decremented)
	}
}

func TestBlockRepair_CleansStaleStagingBeforeSuccessfulRepair(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(12)
	quarantineBlock(t, dir, blockID)
	if err := os.WriteFile(block.FilePath(dir, blockID)+".repair", []byte("stale blk"), 0o600); err != nil {
		t.Fatalf("WriteFile stale blk: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(dir, blockID)+".repair", []byte("stale idx"), 0o600); err != nil {
		t.Fatalf("WriteFile stale idx: %v", err)
	}
	replacement := replacementPayload(t, blockID, []byte("replacement after stale staging"))
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireNotQuarantined(t, dir, blockID)
	requireNoRepairStaging(t, dir)
	if metrics.repairsOK != 1 || metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=1 failed=0", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_RotatesPeersAcrossConfiguredMembers(t *testing.T) {
	dir := t.TempDir()
	payloads := make(map[uint64]transferPayload)
	for _, blockID := range []uint64{1, 2, 3} {
		quarantineBlock(t, dir, blockID)
		payloads[blockID] = replacementPayload(t, blockID, []byte("rotated replacement"))
	}
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: payloads}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091", "member-b:9091"})

	repair.RepairQuarantined(context.Background())

	if len(transferer.calls) != 3 {
		t.Fatalf("transfer calls = %d, want 3", len(transferer.calls))
	}
	got := []string{transferer.calls[0].addr, transferer.calls[1].addr, transferer.calls[2].addr}
	want := []string{"member-a:9091", "member-b:9091", "member-a:9091"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("peer rotation = %v, want %v", got, want)
		}
	}
	if metrics.repairsOK != 3 || metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=3 failed=0", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_ListFailureSkipsTransferAndMetrics(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "missing")
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{}
	repair := newTestBlockRepair(missingDir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	if len(transferer.calls) != 0 {
		t.Fatalf("transfer calls = %d, want 0", len(transferer.calls))
	}
	if metrics.repairsOK+metrics.repairsFailed != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want no repair metrics", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_PromoteFailureLeavesQuarantineIntact(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(13)
	quarantineBlock(t, dir, blockID)
	idxDest := block.IdxFilePath(dir, blockID)
	if err := os.Mkdir(idxDest, 0o700); err != nil {
		t.Fatalf("Mkdir idx dest blocker: %v", err)
	}
	replacement := replacementPayload(t, blockID, []byte("replacement blocked at promote"))
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	repair := newTestBlockRepair(dir, transferer, metrics, []string{"member-a:9091"})

	repair.RepairQuarantined(context.Background())

	requireQuarantined(t, dir, blockID)
	if _, err := os.Stat(block.FilePath(dir, blockID)); !os.IsNotExist(err) {
		t.Fatalf("expected no promoted block, stat err=%v", err)
	}
	requireNoRepairStaging(t, dir)
	if metrics.repairsFailed != 1 || metrics.repairsOK != 0 {
		t.Fatalf("repair metrics ok=%d failed=%d, want ok=0 failed=1", metrics.repairsOK, metrics.repairsFailed)
	}
}

func TestBlockRepair_WaitsForPressurePause(t *testing.T) {
	dir := t.TempDir()
	blockID := uint64(10)
	quarantineBlock(t, dir, blockID)
	replacement := replacementPayload(t, blockID, []byte("paused replacement"))
	metrics := &deepScrubMetrics{}
	transferer := &recordingBlockTransferer{payloads: map[uint64]transferPayload{blockID: replacement}}
	pause := newControlledPause()
	repair := scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:       dir,
		Transferer:      transferer,
		Metrics:         metrics,
		PauseController: pause,
		PeerAddrs:       []string{"member-a:9091"},
	})

	done := make(chan struct{})
	go func() {
		repair.RepairQuarantined(context.Background())
		close(done)
	}()

	select {
	case <-pause.waiting:
	case <-time.After(time.Second):
		t.Fatal("repair did not wait on pressure pause")
	}
	if len(transferer.calls) != 0 {
		t.Fatalf("transfer calls while paused = %d, want 0", len(transferer.calls))
	}

	pause.Resume()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("repair did not resume after pressure pause cleared")
	}
	if len(transferer.calls) != 1 {
		t.Fatalf("transfer calls after resume = %d, want 1", len(transferer.calls))
	}
	if metrics.pauses != 1 {
		t.Fatalf("pause metrics = %d, want 1", metrics.pauses)
	}
}

func newTestBlockRepair(dir string, transferer *recordingBlockTransferer, metrics *deepScrubMetrics, peers []string) *scrub.BlockRepair {
	return scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:  dir,
		Transferer: transferer,
		Metrics:    metrics,
		PeerAddrs:  peers,
	})
}

func quarantineBlock(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeRepairBlock(t, dir, blockID, []byte("old quarantined block"))
	if err := block.Quarantine(block.FilePath(dir, blockID)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
}

func replacementPayload(t *testing.T, blockID uint64, body []byte) transferPayload {
	t.Helper()
	dir := t.TempDir()
	writeRepairBlock(t, dir, blockID, body)

	blk, err := os.ReadFile(block.FilePath(dir, blockID))
	if err != nil {
		t.Fatalf("ReadFile blk: %v", err)
	}
	idx, err := os.ReadFile(block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("ReadFile idx: %v", err)
	}
	return transferPayload{blk: blk, idx: idx}
}

func writeRepairBlock(t *testing.T, dir string, blockID uint64, body []byte) {
	t.Helper()

	bw, err := block.NewWriter(block.FilePath(dir, blockID), 1, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	result, err := bw.AppendDocument("tx-repair", "doc.bin", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-repair",
		DocName:       "doc.bin",
		ContentType:   "application/octet-stream",
		CreatedAt:     time.Unix(0, 0),
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}); err != nil {
		t.Fatalf("Append index: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}
}

func requireQuarantined(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	if _, err := os.Stat(block.FilePath(dir, blockID) + block.QuarantineSuffix); err != nil {
		t.Fatalf("expected quarantined block: %v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(dir, blockID) + block.QuarantineSuffix); err != nil {
		t.Fatalf("expected quarantined index: %v", err)
	}
}

func requireNotQuarantined(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	if _, err := os.Stat(block.FilePath(dir, blockID) + block.QuarantineSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected block quarantine removed, stat err=%v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(dir, blockID) + block.QuarantineSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected index quarantine removed, stat err=%v", err)
	}
	if _, err := os.Stat(block.FilePath(dir, blockID)); err != nil {
		t.Fatalf("expected promoted block: %v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(dir, blockID)); err != nil {
		t.Fatalf("expected promoted index: %v", err)
	}
}

func requireNoPromotedBlock(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	if _, err := os.Stat(block.FilePath(dir, blockID)); !os.IsNotExist(err) {
		t.Fatalf("expected no promoted block, stat err=%v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(dir, blockID)); !os.IsNotExist(err) {
		t.Fatalf("expected no promoted index, stat err=%v", err)
	}
}

func requireNoRepairStaging(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.repair"))
	if err != nil {
		t.Fatalf("Glob repair staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair staging files remain: %v", matches)
	}
}

func promoteBackendRepairReplacement(t *testing.T, dir string, blockID uint64, replacement transferPayload) {
	t.Helper()
	if err := os.WriteFile(block.FilePath(dir, blockID), replacement.blk, 0o600); err != nil {
		t.Fatalf("WriteFile backend repair blk: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(dir, blockID), replacement.idx, 0o600); err != nil {
		t.Fatalf("WriteFile backend repair idx: %v", err)
	}
	if err := os.Remove(block.FilePath(dir, blockID) + block.QuarantineSuffix); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove blk quarantine: %v", err)
	}
	if err := os.Remove(block.IdxFilePath(dir, blockID) + block.QuarantineSuffix); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove idx quarantine: %v", err)
	}
}
