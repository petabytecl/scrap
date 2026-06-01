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

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

type projectionRebuildCore interface {
	currentOpenBlockID() uint64
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
		return nil
	}

	openBlockID := r.core.currentOpenBlockID()

	ctx := context.Background()
	cellID := cellIDOrLocal(r.upload.CellID)
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		if err := r.rebuildBlockUploadState(ctx, projection, be, cellID, blockID); err != nil {
			return err
		}
	}
	return nil
}

func (r *projectionRebuilder) rebuildBlockUploadState(ctx context.Context, projection *index.Index, be backend.Backend, cellID string, blockID uint64) error {
	prefix := backendKeyPrefix(cellID, r.shardID, blockID)
	blkObject, idxObject, uploaded, err := uploadedBlockObjects(ctx, be, prefix)
	if err != nil {
		return r.handleRebuildUploadCheckError(ctx, blockID, err)
	}

	info, statErr := os.Stat(r.blockPath(blockID))
	if statErr != nil {
		if uploaded {
			return fmt.Errorf("shard: rebuild confirmed upload %d missing sealed Block metadata: %w", blockID, statErr)
		}
		return nil
	}

	if uploaded {
		return r.putRebuiltConfirmedUpload(projection, blockID, info.Size(), blkObject, idxObject)
	}
	return r.putRebuiltPendingUpload(projection, blockID, info)
}

func (r *projectionRebuilder) handleRebuildUploadCheckError(ctx context.Context, blockID uint64, err error) error {
	class := backend.ErrorClass(err)
	if class != backend.ClassTransient && class != backend.ClassAuth && class != backend.ClassThrottled {
		return fmt.Errorf("shard: rebuild upload metadata %d: %w", blockID, err)
	}
	r.logger.WarnContext(ctx, "shard: rebuild upload check skipped (transient)", "block_id", blockID, "err", err)
	return nil
}

func (r *projectionRebuilder) putRebuiltConfirmedUpload(
	projection *index.Index,
	blockID uint64,
	sealedSize int64,
	blkObject index.BackendObjectMetadata,
	idxObject index.BackendObjectMetadata,
) error {
	if err := projection.PutConfirmedUpload(index.ConfirmedUpload{
		BlockID:         blockID,
		ShardID:         r.shardID,
		ConfirmedAtUs:   time.Now().UnixMicro(),
		SealedSizeBytes: sealedSize,
		BlockObject:     blkObject,
		IndexObject:     idxObject,
	}); err != nil {
		return fmt.Errorf("shard: rebuild confirmed upload %d: %w", blockID, err)
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

// uploadedBlockObjects returns both object metadata records when .blk and .idx
// exist in the Backend. A missing object means the Block still needs upload.
// Other Backend errors are returned so rebuild does not incorrectly requeue a
// Block when the dependency is unavailable.
func uploadedBlockObjects(ctx context.Context, be backend.Backend, prefix string) (index.BackendObjectMetadata, index.BackendObjectMetadata, bool, error) {
	blk, err := backendObjectMetadata(ctx, be, prefix+".blk")
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return index.BackendObjectMetadata{}, index.BackendObjectMetadata{}, false, nil
		}
		return index.BackendObjectMetadata{}, index.BackendObjectMetadata{}, false, err
	}
	idx, err := backendObjectMetadata(ctx, be, prefix+".idx")
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return index.BackendObjectMetadata{}, index.BackendObjectMetadata{}, false, nil
		}
		return index.BackendObjectMetadata{}, index.BackendObjectMetadata{}, false, err
	}
	return blk, idx, true, nil
}

func backendObjectMetadata(ctx context.Context, be backend.Backend, key string) (index.BackendObjectMetadata, error) {
	meta, err := be.HeadObject(ctx, key)
	if err != nil {
		return index.BackendObjectMetadata{}, err
	}
	if meta.ETag == "" {
		return index.BackendObjectMetadata{}, fmt.Errorf("%w: backend object %s missing validation token", backend.ErrCorrupt, key)
	}
	return index.BackendObjectMetadata{
		Key:             key,
		SizeBytes:       meta.Size,
		ValidationToken: meta.ETag,
	}, nil
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
