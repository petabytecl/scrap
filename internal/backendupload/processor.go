package backendupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
)

type IntentLister interface {
	ListUploadIntents() ([]metastore.UploadIntent, error)
}

type IntentStateUpdater interface {
	UpdateUploadIntentState(ctx context.Context, blockID string, state metastore.UploadState, lastError string, commandID string, proposedAt time.Time) error
}

type Processor struct {
	Uploader Uploader
	Intents  IntentLister
	Updater  IntentStateUpdater
	Now      func() time.Time
}

type RunResult struct {
	Scanned  int
	Skipped  int
	Deferred int
	Uploaded int
	Failed   int
}

func (p Processor) RunOnce(ctx context.Context) (RunResult, error) {
	var result RunResult
	if p.Intents == nil {
		return result, errNotConfigured("upload intent lister")
	}
	if p.Updater == nil {
		return result, errNotConfigured("upload intent state updater")
	}
	intents, err := p.Intents.ListUploadIntents()
	if err != nil {
		return result, err
	}
	for _, intent := range intents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		if !shouldUpload(intent.State) {
			result.Skipped++
			continue
		}
		if _, err := p.Uploader.UploadBlock(ctx, intent); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			if errors.Is(err, blockstore.ErrBlockOpen) {
				result.Deferred++
				continue
			}
			if err := p.recordState(ctx, intent.BlockID, metastore.UploadStateFailed, err.Error()); err != nil {
				return result, err
			}
			result.Failed++
			continue
		}
		if err := p.recordState(ctx, intent.BlockID, metastore.UploadStateUploaded, ""); err != nil {
			return result, err
		}
		result.Uploaded++
	}
	return result, nil
}

func shouldUpload(state metastore.UploadState) bool {
	return state == metastore.UploadStatePending || state == metastore.UploadStateFailed
}

func (p Processor) recordState(ctx context.Context, blockID string, state metastore.UploadState, lastError string) error {
	proposedAt := time.Now().UTC()
	if p.Now != nil {
		proposedAt = p.Now()
	}
	return p.Updater.UpdateUploadIntentState(
		ctx,
		blockID,
		state,
		lastError,
		uploadStateCommandID(blockID, state, lastError),
		proposedAt,
	)
}

func uploadStateCommandID(blockID string, state metastore.UploadState, lastError string) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("upload-intent-state"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(blockID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(strconv.FormatUint(uint64(state), 10)))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(lastError))
	sum := hasher.Sum(nil)
	return "upload-intent-state:" + hex.EncodeToString(sum[:16])
}

func errNotConfigured(component string) error {
	return &notConfiguredError{component: component}
}

type notConfiguredError struct {
	component string
}

func (e *notConfiguredError) Error() string {
	return "backendupload: " + e.component + " is not configured"
}
