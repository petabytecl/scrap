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
	"sync/atomic"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

type projectionRebuildCore interface {
	currentOpenBlockID() uint64
	confirmedUploadForRebuild(blockID uint64) (index.ConfirmedUpload, error)
	swapRebuiltProjection(pebbleDir, tempDir, oldDir string) (idxNil bool, err error)
}

type projectionRebuilder struct {
	core      projectionRebuildCore
	dataDir   string
	blocksDir string
	shardID   uint64
	upload    UploadConfig
	logger    *slog.Logger

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
	if !r.rebuilding.CompareAndSwap(false, true) {
		return true, nil
	}
	done := make(chan struct{})
	r.rebuildDone.Store(&done)
	// The rebuild is detached: it outlives the triggering RPC. WithoutCancel keeps
	// the caller's trace/values for log correlation while dropping cancellation and
	// any deadline, so returning from this RPC does not abort the rebuild.
	go r.doRebuild(context.WithoutCancel(ctx), done)
	return false, nil
}

func (r *projectionRebuilder) Wait() {
	if p := r.rebuildDone.Load(); p != nil {
		<-*p
	}
}

func (r *projectionRebuilder) setInProgressForTest(v bool) {
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
		return r.rebuildEvictedUploadAuthorities(projection, blockIDs)
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

func (r *projectionRebuilder) rebuildEvictedUploadAuthorities(projection *index.Index, blockIDs []uint64) error {
	openBlockID := r.core.currentOpenBlockID()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if _, err := r.rebuildEvictedUploadAuthority(projection, blockID); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildPendingUpload(ctx context.Context, projection *index.Index, blockID uint64) error {
	info, statErr := os.Stat(r.blockPath(blockID))
	if statErr == nil {
		return r.putRebuiltPendingUpload(projection, blockID, info)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		r.logger.ErrorContext(ctx, "shard: rebuild upload cannot stat sealed Block metadata", "block_id", blockID, "err", statErr)
		return fmt.Errorf("shard: rebuild pending upload %d stat sealed Block metadata: %w", blockID, statErr)
	}
	evicted, err := r.rebuildEvictedUploadAuthority(projection, blockID)
	if err != nil {
		return err
	}
	if evicted {
		return nil
	}
	r.logger.ErrorContext(ctx, "shard: rebuild upload missing sealed Block metadata", "block_id", blockID, "err", statErr)
	return fmt.Errorf("shard: rebuild pending upload %d missing sealed Block metadata: %w", blockID, statErr)
}

func (r *projectionRebuilder) rebuildEvictedUploadAuthority(projection *index.Index, blockID uint64) (bool, error) {
	lifecycle, err := ClassifyLocalBlock(r.blocksDir, blockID)
	if err != nil {
		return false, fmt.Errorf("shard: rebuild pending upload %d classify local lifecycle: %w", blockID, err)
	}
	if lifecycle.State != LocalBlockStateEvicted {
		return false, nil
	}
	confirmed, err := r.core.confirmedUploadForRebuild(blockID)
	if err != nil {
		return false, fmt.Errorf("shard: rebuild pending upload %d evicted Block missing committed ConfirmUpload: %w", blockID, err)
	}
	if err := validateRestoreAuthority(confirmed, lifecycle); err != nil {
		return false, err
	}
	if err := projection.PutConfirmedUpload(confirmed); err != nil {
		return false, fmt.Errorf("shard: rebuild confirmed upload %d: %w", blockID, err)
	}
	return true, nil
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
