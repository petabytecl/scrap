package shard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
)

type projectionRebuildCore interface {
	// The *Locked methods must only be called from within the finalize
	// closure passed to finalizeAndSwapRebuiltProjection: they assume the
	// shard apply lock is held.
	currentOpenBlockIDLocked() uint64
	confirmedUploadForRebuildLocked(blockID uint64) (index.ConfirmedUpload, error)
	pendingUploadForRebuildLocked(blockID uint64) (index.PendingUpload, error)
	copyContentSafetyIntoLocked(dst *index.Index) error
	projectionAppliedIndex() (uint64, bool)
	// finalizeAndSwapRebuiltProjection holds the shard apply lock across
	// finalize and the projection swap, so no raft apply can mutate the
	// outgoing projection between the rebuild catch-up and the switch (#464).
	finalizeAndSwapRebuiltProjection(finalize func() error, pebbleDir, tempDir, oldDir string) (idxNil bool, err error)
}

type projectionRebuilder struct {
	core      projectionRebuildCore
	dataDir   string
	blocksDir string
	shardID   uint64
	upload    UploadConfig
	logger    *slog.Logger

	// stateMu orders rebuilding/rebuildDone updates so Wait never observes the
	// stale (closed) done channel of a previous rebuild after Trigger has
	// already marked a new rebuild in progress.
	stateMu     sync.Mutex
	rebuilding  atomic.Bool
	rebuildDone atomic.Pointer[chan struct{}]

	// betweenPhasesForTest, when set, runs after the unlocked bulk scan and
	// before the locked finalize+swap — the rebuild-vs-apply window (#464).
	betweenPhasesForTest func()
}

func newProjectionRebuilder(core projectionRebuildCore, dataDir, blocksDir string, shardID uint64, upload UploadConfig, logger *slog.Logger) *projectionRebuilder {
	if logger == nil {
		logger = slog.Default()
	}
	r := &projectionRebuilder{
		core:      core,
		dataDir:   dataDir,
		blocksDir: blocksDir,
		shardID:   shardID,
		upload:    upload,
		logger:    logger,
	}
	done := make(chan struct{})
	close(done)
	r.rebuildDone.Store(&done)
	return r
}

func (r *projectionRebuilder) InProgress() bool {
	return r.rebuilding.Load()
}

func (r *projectionRebuilder) Trigger(ctx context.Context) (alreadyInProgress bool, err error) {
	r.stateMu.Lock()
	if !r.rebuilding.CompareAndSwap(false, true) {
		r.stateMu.Unlock()
		return true, nil
	}
	done := make(chan struct{})
	r.rebuildDone.Store(&done)
	r.stateMu.Unlock()
	// The rebuild is detached: it outlives the triggering RPC. WithoutCancel keeps
	// the caller's trace/values for log correlation while dropping cancellation and
	// any deadline, so returning from this RPC does not abort the rebuild.
	go r.doRebuild(context.WithoutCancel(ctx), done)
	return false, nil
}

func (r *projectionRebuilder) Wait() {
	r.stateMu.Lock()
	p := r.rebuildDone.Load()
	r.stateMu.Unlock()
	if p != nil {
		<-*p
	}
}

func (r *projectionRebuilder) setInProgressForTest(v bool) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.rebuilding.Store(v)
	if v {
		done := make(chan struct{})
		r.rebuildDone.Store(&done)
		return
	}
	done := make(chan struct{})
	close(done)
	r.rebuildDone.Store(&done)
}

// recoverProjectionSwapDirs cleans up after a crash during a projection swap.
// If the live projection directory vanished mid-swap, the newest
// pebble.previous-* directory is the pre-rebuild projection and is restored;
// all remaining timestamped rebuild/previous directories are stale and removed
// so they do not accumulate forever.
func recoverProjectionSwapDirs(dataDir, pebbleDir string) error {
	previous, err := filepath.Glob(filepath.Join(dataDir, "pebble.previous-*"))
	if err != nil {
		return fmt.Errorf("shard: glob previous projections: %w", err)
	}
	sort.Strings(previous)

	missing, err := projectionDirMissingOrEmpty(pebbleDir)
	if err != nil {
		return err
	}
	if len(previous) > 0 && missing {
		newest := previous[len(previous)-1]
		_ = os.RemoveAll(pebbleDir)
		if err := os.Rename(newest, pebbleDir); err != nil {
			return fmt.Errorf("shard: restore projection from %s: %w", newest, err)
		}
		previous = previous[:len(previous)-1]
	}

	rebuilds, err := filepath.Glob(filepath.Join(dataDir, "pebble.rebuild-*"))
	if err != nil {
		return fmt.Errorf("shard: glob rebuild projections: %w", err)
	}
	for _, dir := range append(previous, rebuilds...) {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("shard: remove stale projection dir %s: %w", dir, err)
		}
	}
	return nil
}

// projectionDirMissingOrEmpty reports whether the live projection directory is
// absent or empty. A non-ENOENT read error (EACCES/EIO on a failing disk) is
// returned rather than treated as "present", so recoverProjectionSwapDirs
// aborts instead of deleting the pebble.previous-* backups without restoring.
func projectionDirMissingOrEmpty(pebbleDir string) (bool, error) {
	entries, err := os.ReadDir(pebbleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("shard: read projection dir %s: %w", pebbleDir, err)
	}
	return len(entries) == 0, nil
}

func (r *projectionRebuilder) doRebuild(ctx context.Context, done chan struct{}) {
	defer close(done)

	pebbleDir := filepath.Join(r.dataDir, "pebble")
	tempDir := filepath.Join(r.dataDir, fmt.Sprintf("pebble.rebuild-%d", time.Now().UnixNano()))
	oldDir := filepath.Join(r.dataDir, fmt.Sprintf("pebble.previous-%d", time.Now().UnixNano()))

	newIdx, bulk, err := r.prepareRebuildProjection(tempDir)
	if err != nil {
		r.logger.ErrorContext(ctx, "rebuild: prepare projection failed", "err", err)
		_ = os.RemoveAll(tempDir)
		r.rebuilding.Store(false)
		return
	}

	if hook := r.betweenPhasesForTest; hook != nil {
		hook()
	}

	// The finalize closure runs under the shard apply lock, together with the
	// swap: it catches up everything applied since the bulk scan (#464), so a
	// commit landing during the long unlocked prepare cannot be lost with the
	// outgoing projection.
	idxNil, err := r.core.finalizeAndSwapRebuiltProjection(func() error {
		return r.finalizeRebuildLocked(newIdx, bulk)
	}, pebbleDir, tempDir, oldDir)
	_ = os.RemoveAll(tempDir)

	if err != nil {
		r.logger.ErrorContext(ctx, "rebuild: swap projection failed", "err", err, "shard_id", r.shardID)
		if idxNil {
			r.logger.ErrorContext(ctx, "rebuild: index is nil after failed swap; shard degraded", "err", err, "shard_id", r.shardID)
			return
		}
	}

	_ = os.RemoveAll(oldDir)
	r.rebuilding.Store(false)
}

// prepareRebuildProjection is the long, unlocked bulk phase: it rebuilds
// Document records from every Block .idx and returns the open rebuild
// projection plus the per-Block entry counts it consumed, so the locked
// finalize phase can re-apply exactly the .idx tail written by applies that
// landed during this scan. State without a Block-file source (content
// safety, upload outbox) is deliberately NOT built here — it is copied under
// the apply lock in finalizeRebuildLocked, where it cannot race an apply.
// rebuildBulkState is everything the unlocked bulk phase captured for the
// locked finalize: how far each Block's .idx was consumed (for the delta
// re-scan) and the per-Block filesystem evidence (so finalize does no fs I/O
// under the apply lock).
type rebuildBulkState struct {
	seenEntries map[uint64]int
	fsState     map[uint64]rebuildBlockFS
}

func (r *projectionRebuilder) prepareRebuildProjection(tempDir string) (*index.Index, rebuildBulkState, error) {
	if err := os.RemoveAll(tempDir); err != nil {
		return nil, rebuildBulkState{}, fmt.Errorf("shard: remove stale rebuild dir: %w", err)
	}

	newIdx, err := index.Open(tempDir)
	if err != nil {
		return nil, rebuildBulkState{}, fmt.Errorf("shard: open rebuild index: %w", err)
	}
	seenEntries, err := r.rebuildProjectionInto(newIdx)
	if err != nil {
		_ = newIdx.Close()
		return nil, rebuildBulkState{}, err
	}
	fsState := make(map[uint64]rebuildBlockFS, len(seenEntries))
	for blockID := range seenEntries {
		fsState[blockID] = r.snapshotBlockFS(blockID)
	}
	// Carry the durable applied-index watermark into the rebuilt projection.
	// The rebuild's sources (Block .idx files) cover at least the state the
	// old watermark stands for, and without the key a restart after the swap
	// fails the raft snapshot restore check as a partial DataDir restore.
	// (The swap re-persists a newer watermark after the switch if one was
	// recorded meanwhile.)
	if watermark, ok := r.core.projectionAppliedIndex(); ok && watermark > 0 {
		if err := newIdx.PersistAppliedIndex(watermark); err != nil {
			_ = newIdx.Close()
			return nil, rebuildBulkState{}, fmt.Errorf("shard: carry applied index into rebuild: %w", err)
		}
	}
	return newIdx, rebuildBulkState{seenEntries: seenEntries, fsState: fsState}, nil
}

// finalizeRebuildLocked is the short catch-up phase, run under the shard
// apply lock immediately before the swap. It closes newIdx in every path.
func (r *projectionRebuilder) finalizeRebuildLocked(newIdx *index.Index, bulk rebuildBulkState) (err error) {
	defer func() {
		closeErr := newIdx.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("shard: close rebuild index: %w", closeErr)
		}
	}()

	blockIDs, err := r.applyBlockIndexDelta(newIdx, bulk.seenEntries)
	if err != nil {
		return err
	}
	// Content-quarantine records and the scanner watermark live only in the
	// projection (no Block-file source), so the .idx scan cannot reconstruct
	// them. Copied under the apply lock, the live projection reflects every
	// quarantine/scanner apply, including ones during the bulk scan (#464).
	if err := r.core.copyContentSafetyIntoLocked(newIdx); err != nil {
		return fmt.Errorf("shard: carry content safety state into rebuild: %w", err)
	}
	// The outbox pass under the lock reads only the live projection (fresh
	// pending/confirmed records, including window applies) plus the fs
	// evidence snapshotted in the unlocked phase — no per-Block stat/classify
	// I/O while the raft apply loop is stalled, or a large Cell's finalize
	// could hold up heartbeats long enough to cost leadership.
	return r.rebuildUploadOutbox(newIdx, blockIDs, bulk.fsState)
}

// applyBlockIndexDelta re-applies Document records appended to any Block
// .idx after the bulk scan counted its entries. Block .idx files are
// append-only for Document records and only mutate under the apply lock, so
// the tail beyond the bulk count is exactly the rebuild-window delta.
func (r *projectionRebuilder) applyBlockIndexDelta(projection *index.Index, seenEntries map[uint64]int) ([]uint64, error) {
	blockIDs, err := r.listBlockIndexIDs()
	if err != nil {
		return nil, err
	}
	for _, blockID := range blockIDs {
		entries, err := r.readBlockIndexEntries(blockID)
		if err != nil {
			return nil, err
		}
		seen := seenEntries[blockID]
		if seen > len(entries) {
			seen = len(entries)
		}
		for _, entry := range entries[seen:] {
			if err := addProjectionDocument(projection, entry.TransactionID, blockID); err != nil {
				return nil, fmt.Errorf("shard: rebuild projection %s/%s: %w", entry.TransactionID, entry.DocName, err)
			}
		}
	}
	return blockIDs, nil
}

func (r *projectionRebuilder) rebuildProjectionInto(projection *index.Index) (map[uint64]int, error) {
	blockIDs, err := r.listBlockIndexIDs()
	if err != nil {
		return nil, err
	}

	seenEntries := make(map[uint64]int, len(blockIDs))
	for _, blockID := range blockIDs {
		entries, err := r.readBlockIndexEntries(blockID)
		if err != nil {
			return nil, err
		}
		seenEntries[blockID] = len(entries)

		for _, entry := range entries {
			if err := addProjectionDocument(projection, entry.TransactionID, blockID); err != nil {
				return nil, fmt.Errorf("shard: rebuild projection %s/%s: %w", entry.TransactionID, entry.DocName, err)
			}
		}
	}
	return seenEntries, nil
}

func (r *projectionRebuilder) readBlockIndexEntries(blockID uint64) ([]block.IndexEntry, error) {
	ir, err := block.OpenIndexReader(r.idxPath(blockID))
	if err != nil {
		return nil, fmt.Errorf("shard: open block index %d: %w", blockID, err)
	}
	entries := ir.Entries()
	if err := ir.Close(); err != nil {
		return nil, fmt.Errorf("shard: close block index %d: %w", blockID, err)
	}
	return entries, nil
}

// rebuildBlockFS is one Block's filesystem evidence (data-file stat and local
// lifecycle), captured in the UNLOCKED bulk phase so the locked finalize does
// no per-Block fs I/O. Block files barely change during a rebuild (client
// traffic and eviction apply are rejected), matching the staleness the
// pre-#464 prepare-time outbox rebuild already had.
type rebuildBlockFS struct {
	statInfo     os.FileInfo
	statErr      error
	lifecycle    localblock.Lifecycle
	lifecycleErr error
}

func (r *projectionRebuilder) snapshotBlockFS(blockID uint64) rebuildBlockFS {
	var fs rebuildBlockFS
	fs.statInfo, fs.statErr = os.Stat(r.blockPath(blockID))
	fs.lifecycle, fs.lifecycleErr = localblock.Classify(r.blocksDir, blockID)
	return fs
}

// blockFS returns the phase-1 snapshot for the Block, or captures it fresh
// for a Block that appeared during the rebuild window (bounded by the window).
func (r *projectionRebuilder) blockFS(fsState map[uint64]rebuildBlockFS, blockID uint64) rebuildBlockFS {
	if fs, ok := fsState[blockID]; ok {
		return fs
	}
	return r.snapshotBlockFS(blockID)
}

func (r *projectionRebuilder) rebuildUploadOutbox(projection *index.Index, blockIDs []uint64, fsState map[uint64]rebuildBlockFS) error {
	be := r.upload.Backend
	if !r.upload.Enabled || be == nil {
		return r.rebuildCommittedUploadAuthorities(projection, blockIDs, fsState)
	}

	openBlockID := r.core.currentOpenBlockIDLocked()

	ctx := context.Background()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if err := r.rebuildPendingUpload(ctx, projection, blockID, r.blockFS(fsState, blockID)); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildCommittedUploadAuthorities(projection *index.Index, blockIDs []uint64, fsState map[uint64]rebuildBlockFS) error {
	openBlockID := r.core.currentOpenBlockIDLocked()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if _, err := r.rebuildLocalConfirmedUploadAuthority(projection, blockID, r.blockFS(fsState, blockID)); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildPendingUpload(ctx context.Context, projection *index.Index, blockID uint64, fs rebuildBlockFS) error {
	info, statErr := fs.statInfo, fs.statErr
	if statErr == nil {
		pending, hasPending, err := r.pendingUploadForRebuild(blockID)
		if err != nil {
			return err
		}
		confirmed, hasConfirmed, err := r.committedUploadAuthorityForRebuild(blockID)
		if err != nil {
			return err
		}
		if shouldPreservePendingUploadForRebuild(pending, hasPending, confirmed, hasConfirmed) {
			return r.putPendingUploadForRebuild(projection, pending)
		}
		if hasConfirmed {
			if err := projection.PutConfirmedUpload(confirmed); err != nil {
				return fmt.Errorf("shard: rebuild confirmed upload %d: %w", blockID, err)
			}
			return nil
		}
		return r.putRebuiltPendingUpload(projection, blockID, info)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		r.logger.ErrorContext(ctx, "shard: rebuild upload cannot stat sealed Block metadata", "block_id", blockID, "err", statErr)
		return fmt.Errorf("shard: rebuild pending upload %d stat sealed Block metadata: %w", blockID, statErr)
	}
	evicted, err := r.rebuildLocalConfirmedUploadAuthority(projection, blockID, fs)
	if err != nil {
		return err
	}
	if evicted {
		return nil
	}
	r.logger.ErrorContext(ctx, "shard: rebuild upload missing sealed Block metadata", "block_id", blockID, "err", statErr)
	return fmt.Errorf("shard: rebuild pending upload %d missing sealed Block metadata: %w", blockID, statErr)
}

func (r *projectionRebuilder) rebuildLocalConfirmedUploadAuthority(projection *index.Index, blockID uint64, fs rebuildBlockFS) (bool, error) {
	lifecycle, err := fs.lifecycle, fs.lifecycleErr
	if err != nil {
		return false, fmt.Errorf("shard: rebuild pending upload %d classify local lifecycle: %w", blockID, err)
	}
	confirmed, ok, err := r.committedUploadAuthorityForRebuild(blockID)
	if err != nil {
		return false, err
	}

	shouldCopy, err := shouldRebuildConfirmedUploadAuthority(blockID, lifecycle, confirmed, ok)
	if err != nil {
		return false, err
	}
	if !shouldCopy {
		return false, nil
	}
	if err := projection.PutConfirmedUpload(confirmed); err != nil {
		return false, fmt.Errorf("shard: rebuild confirmed upload %d: %w", blockID, err)
	}
	return true, nil
}

func shouldRebuildConfirmedUploadAuthority(
	blockID uint64,
	lifecycle localblock.Lifecycle,
	confirmed index.ConfirmedUpload,
	hasAuthority bool,
) (bool, error) {
	if !hasAuthority {
		if lifecycle.State == localblock.StateEvicted {
			return false, fmt.Errorf("shard: rebuild pending upload %d evicted Block missing committed ConfirmUpload: %w", blockID, index.ErrConfirmedUploadNotFound)
		}
		return false, nil
	}

	switch lifecycle.State {
	case localblock.StateEvicted:
		if err := validateRestoreAuthority(confirmed, lifecycle); err != nil {
			return false, err
		}
		return true, nil
	case localblock.StateHot, localblock.StateHotCleanupNeeded:
		return true, nil
	default:
		return false, nil
	}
}

func (r *projectionRebuilder) committedUploadAuthorityForRebuild(blockID uint64) (index.ConfirmedUpload, bool, error) {
	confirmed, err := r.core.confirmedUploadForRebuildLocked(blockID)
	if err != nil {
		if errors.Is(err, index.ErrConfirmedUploadNotFound) {
			return index.ConfirmedUpload{}, false, nil
		}
		return index.ConfirmedUpload{}, false, fmt.Errorf("shard: rebuild pending upload %d committed ConfirmUpload: %w", blockID, err)
	}
	return confirmed, true, nil
}

func (r *projectionRebuilder) pendingUploadForRebuild(blockID uint64) (index.PendingUpload, bool, error) {
	pending, err := r.core.pendingUploadForRebuildLocked(blockID)
	if err != nil {
		if errors.Is(err, index.ErrPendingUploadNotFound) {
			return index.PendingUpload{}, false, nil
		}
		return index.PendingUpload{}, false, fmt.Errorf("shard: rebuild pending upload %d current outbox: %w", blockID, err)
	}
	return pending, true, nil
}

func shouldPreservePendingUploadForRebuild(
	pending index.PendingUpload,
	hasPending bool,
	confirmed index.ConfirmedUpload,
	hasConfirmed bool,
) bool {
	if !hasPending {
		return false
	}
	if !hasConfirmed {
		return true
	}
	return pending.UploadGeneration > confirmed.UploadGeneration
}

func (r *projectionRebuilder) putPendingUploadForRebuild(projection *index.Index, upload index.PendingUpload) error {
	if err := projection.PutPendingUpload(upload); err != nil {
		return fmt.Errorf("shard: rebuild pending upload %d: %w", upload.BlockID, err)
	}
	return nil
}

func (r *projectionRebuilder) putRebuiltPendingUpload(projection *index.Index, blockID uint64, info os.FileInfo) error {
	if err := projection.PutPendingUpload(index.PendingUpload{
		BlockID:         blockID,
		ShardID:         r.shardID,
		SealedSizeBytes: info.Size(),
		SealedAtUs:      info.ModTime().UnixMicro(),
	}); err != nil {
		return fmt.Errorf("shard: rebuild pending upload %d: %w", blockID, err)
	}
	return nil
}

func (r *projectionRebuilder) listBlockIndexIDs() ([]uint64, error) {
	entries, err := os.ReadDir(r.blocksDir)
	if err != nil {
		return nil, fmt.Errorf("shard: read blocks dir: %w", err)
	}

	blockIDs := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".idx") {
			continue
		}
		hexPart := strings.TrimSuffix(name, ".idx")
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			continue
		}
		blockIDs = append(blockIDs, id)
	}
	sort.Slice(blockIDs, func(i, j int) bool {
		return blockIDs[i] < blockIDs[j]
	})
	return blockIDs, nil
}

func (r *projectionRebuilder) blockPath(id uint64) string {
	return filepath.Join(r.blocksDir, fmt.Sprintf("%016x.blk", id))
}

func (r *projectionRebuilder) idxPath(id uint64) string {
	return filepath.Join(r.blocksDir, fmt.Sprintf("%016x.idx", id))
}
