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
	currentOpenBlockID() uint64
	confirmedUploadForRebuild(blockID uint64) (index.ConfirmedUpload, error)
	pendingUploadForRebuild(blockID uint64) (index.PendingUpload, error)
	swapRebuiltProjection(pebbleDir, tempDir, oldDir string) (idxNil bool, err error)
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

	if err := r.prepareRebuildProjection(tempDir); err != nil {
		r.logger.ErrorContext(ctx, "rebuild: prepare projection failed", "err", err)
		_ = os.RemoveAll(tempDir)
		r.rebuilding.Store(false)
		return
	}

	idxNil, err := r.core.swapRebuiltProjection(pebbleDir, tempDir, oldDir)
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

func (r *projectionRebuilder) prepareRebuildProjection(tempDir string) error {
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("shard: remove stale rebuild dir: %w", err)
	}

	newIdx, err := index.Open(tempDir)
	if err != nil {
		return fmt.Errorf("shard: open rebuild index: %w", err)
	}
	if err := r.rebuildProjectionInto(newIdx); err != nil {
		_ = newIdx.Close()
		return err
	}
	if err := newIdx.Close(); err != nil {
		return fmt.Errorf("shard: close rebuild index: %w", err)
	}
	return nil
}

func (r *projectionRebuilder) rebuildProjectionInto(projection *index.Index) error {
	blockIDs, err := r.listBlockIndexIDs()
	if err != nil {
		return err
	}

	for _, blockID := range blockIDs {
		ir, err := block.OpenIndexReader(r.idxPath(blockID))
		if err != nil {
			return fmt.Errorf("shard: open block index %d: %w", blockID, err)
		}
		entries := ir.Entries()
		if err := ir.Close(); err != nil {
			return fmt.Errorf("shard: close block index %d: %w", blockID, err)
		}

		for _, entry := range entries {
			if err := addProjectionDocument(projection, entry.TransactionID, blockID); err != nil {
				return fmt.Errorf("shard: rebuild projection %s/%s: %w", entry.TransactionID, entry.DocName, err)
			}
		}
	}

	if err := r.rebuildUploadOutbox(projection, blockIDs); err != nil {
		return err
	}
	return nil
}

func (r *projectionRebuilder) rebuildUploadOutbox(projection *index.Index, blockIDs []uint64) error {
	be := r.upload.Backend
	if !r.upload.Enabled || be == nil {
		return r.rebuildCommittedUploadAuthorities(projection, blockIDs)
	}

	openBlockID := r.core.currentOpenBlockID()

	ctx := context.Background()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if err := r.rebuildPendingUpload(ctx, projection, blockID); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildCommittedUploadAuthorities(projection *index.Index, blockIDs []uint64) error {
	openBlockID := r.core.currentOpenBlockID()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if _, err := r.rebuildLocalConfirmedUploadAuthority(projection, blockID); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildPendingUpload(ctx context.Context, projection *index.Index, blockID uint64) error {
	info, statErr := os.Stat(r.blockPath(blockID))
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
	evicted, err := r.rebuildLocalConfirmedUploadAuthority(projection, blockID)
	if err != nil {
		return err
	}
	if evicted {
		return nil
	}
	r.logger.ErrorContext(ctx, "shard: rebuild upload missing sealed Block metadata", "block_id", blockID, "err", statErr)
	return fmt.Errorf("shard: rebuild pending upload %d missing sealed Block metadata: %w", blockID, statErr)
}

func (r *projectionRebuilder) rebuildLocalConfirmedUploadAuthority(projection *index.Index, blockID uint64) (bool, error) {
	lifecycle, err := localblock.Classify(r.blocksDir, blockID)
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
	confirmed, err := r.core.confirmedUploadForRebuild(blockID)
	if err != nil {
		if errors.Is(err, index.ErrConfirmedUploadNotFound) {
			return index.ConfirmedUpload{}, false, nil
		}
		return index.ConfirmedUpload{}, false, fmt.Errorf("shard: rebuild pending upload %d committed ConfirmUpload: %w", blockID, err)
	}
	return confirmed, true, nil
}

func (r *projectionRebuilder) pendingUploadForRebuild(blockID uint64) (index.PendingUpload, bool, error) {
	pending, err := r.core.pendingUploadForRebuild(blockID)
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
