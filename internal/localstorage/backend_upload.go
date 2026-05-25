package localstorage

import (
	"context"
	"errors"
	"sort"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/published"
)

type BackendUploadOnceResult struct {
	SealedBlockID       string
	SealedBlockIDs      []string
	Sealed              bool
	Upload              backendupload.RunResult
	MetadataPublished   bool
	MetadataPublication *published.SnapshotPublication
}

func (u *BackendUploadApplication) Processor(store backend.Store) backendupload.Processor {
	app := u.app
	return backendupload.Processor{
		Uploader: backendupload.Uploader{
			Backend:  store,
			Source:   backendupload.LocalBlockSource{Blocks: app.blocks},
			Envelope: app.backendEnvelopeSource(),
			Index: backendupload.LocalBlockIndexSource{
				Documents: app.metadata,
				ShardID:   "local",
			},
		},
		Intents: app.metadata,
		Updater: app.authority,
		Now:     app.now,
	}
}

func (u *BackendUploadApplication) RunOnce(ctx context.Context, store backend.Store) (BackendUploadOnceResult, error) {
	var result BackendUploadOnceResult
	sealedBlockID, sealed, err := u.SealCurrentBlockIfDue(ctx)
	if err != nil {
		return result, err
	}
	result.SealedBlockID = sealedBlockID
	result.Sealed = sealed
	if sealed {
		result.SealedBlockIDs = append(result.SealedBlockIDs, sealedBlockID)
	}
	recovered, err := u.SealPendingBlocks(ctx)
	if err != nil {
		return result, err
	}
	result.SealedBlockIDs = append(result.SealedBlockIDs, recovered...)
	result.Upload, err = u.Processor(store).RunOnce(ctx)
	if err != nil {
		return result, err
	}
	if u.shouldPublishMetadataCheckpoint(ctx, store, result.Upload) {
		mutable, ok := store.(backend.MutableStore)
		if !ok {
			return result, errors.New("localstorage: backend store does not support mutable metadata pointers")
		}
		publication, err := u.app.publishMetadataSnapshot(ctx, mutable)
		if err != nil {
			return result, err
		}
		result.MetadataPublished = true
		result.MetadataPublication = &publication
	}
	return result, err
}

func (u *BackendUploadApplication) shouldPublishMetadataCheckpoint(ctx context.Context, store backend.Store, upload backendupload.RunResult) bool {
	if upload.Uploaded > 0 {
		return true
	}
	if upload.Deferred > 0 || upload.Failed > 0 {
		return false
	}
	if _, err := published.VerifyCurrentCheckpoint(ctx, store, localPublishedCellID); errors.Is(err, published.ErrCurrentPointerNotFound) {
		return true
	}
	return false
}

func (u *BackendUploadApplication) SealPendingBlocks(ctx context.Context) ([]string, error) {
	intents, err := u.app.metadata.ListUploadIntents()
	if err != nil {
		return nil, err
	}
	currentBlockID := u.app.blocks.CurrentBlockID()
	var sealed []string
	for _, intent := range intents {
		if intent.BlockID == "" || intent.BlockID == currentBlockID || !isUploadPending(intent.State) {
			continue
		}
		didSeal, err := u.app.blocks.SealBlock(ctx, intent.BlockID)
		if errors.Is(err, blockstore.ErrEmptyBlock) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if didSeal {
			sealed = append(sealed, intent.BlockID)
		}
	}
	sort.Strings(sealed)
	return sealed, nil
}

func isUploadPending(state metastore.UploadState) bool {
	return state == metastore.UploadStatePending || state == metastore.UploadStateFailed
}

func (u *BackendUploadApplication) SealCurrentBlockIfDue(ctx context.Context) (string, bool, error) {
	if u.app.sealBlockAtBytes == 0 || u.app.blocks.CurrentBlockLength() < u.app.sealBlockAtBytes {
		return "", false, nil
	}
	blockID, err := u.app.blocks.SealCurrent(ctx)
	if errors.Is(err, blockstore.ErrEmptyBlock) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return blockID, true, nil
}
