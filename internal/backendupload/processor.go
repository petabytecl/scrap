package backendupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
)

const (
	defaultRunOnceBatchSize = 100
	defaultUploadTimeout    = 10 * time.Minute
)

type IntentLister interface {
	ListPendingUploadIntents(limit int) ([]metastore.UploadIntent, error)
}

type IntentStateUpdater interface {
	UpdateUploadIntentState(ctx context.Context, blockID string, state metastore.UploadState, lastError, commandID string, proposedAt time.Time) error
}

type Processor struct {
	Uploader  Uploader
	Intents   IntentLister
	Updater   IntentStateUpdater
	Now       func() time.Time
	BatchSize int

	// UploadTimeout bounds each block upload attempt so a stalled backend
	// cannot monopolize the processor loop indefinitely.
	UploadTimeout time.Duration
}

type RunResult struct {
	Scanned  int
	Skipped  int
	Deferred int
	Uploaded int
	Failed   int
	Errors   map[backend.ErrorClass]int
}

func (p Processor) RunOnce(ctx context.Context) (RunResult, error) {
	var result RunResult
	if err := p.validate(); err != nil {
		return result, err
	}
	intents, err := p.Intents.ListPendingUploadIntents(p.batchSize())
	if err != nil {
		return result, err
	}
	for _, intent := range intents {
		if err := p.processIntent(ctx, intent, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (p Processor) validate() error {
	if p.Intents == nil {
		return errNotConfigured("upload intent lister")
	}
	if p.Updater == nil {
		return errNotConfigured("upload intent state updater")
	}
	return nil
}

func (p Processor) batchSize() int {
	if p.BatchSize > 0 {
		return p.BatchSize
	}
	return defaultRunOnceBatchSize
}

func (p Processor) processIntent(ctx context.Context, intent metastore.UploadIntent, result *RunResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result.Scanned++
	if !shouldUpload(intent.State) {
		result.Skipped++
		return nil
	}
	if _, err := p.uploadBlock(ctx, intent); err != nil {
		return p.recordUploadFailure(ctx, intent, err, result)
	}
	if err := p.recordState(ctx, intent.BlockID, metastore.UploadStateUploaded, ""); err != nil {
		return err
	}
	result.Uploaded++
	return nil
}

func (p Processor) uploadBlock(ctx context.Context, intent metastore.UploadIntent) (UploadResult, error) {
	uploadCtx, cancel := context.WithTimeout(ctx, p.uploadTimeout())
	defer cancel()
	return p.Uploader.UploadBlock(uploadCtx, intent)
}

func (p Processor) uploadTimeout() time.Duration {
	if p.UploadTimeout > 0 {
		return p.UploadTimeout
	}
	return defaultUploadTimeout
}

func (p Processor) recordUploadFailure(ctx context.Context, intent metastore.UploadIntent, uploadErr error, result *RunResult) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(uploadErr, blockstore.ErrBlockOpen) {
		result.Deferred++
		return nil
	}
	result.recordError(uploadErr)
	if err := p.recordState(ctx, intent.BlockID, metastore.UploadStateFailed, uploadErr.Error()); err != nil {
		return err
	}
	result.Failed++
	return nil
}

func (r *RunResult) recordError(err error) {
	class, ok := classifyBackendUploadError(err)
	if !ok {
		return
	}
	if r.Errors == nil {
		r.Errors = make(map[backend.ErrorClass]int)
	}
	r.Errors[class]++
}

func classifyBackendUploadError(err error) (backend.ErrorClass, bool) {
	var classified *backend.Error
	if errors.As(err, &classified) && classified.Class.Valid() {
		return classified.Class, true
	}
	switch {
	case errors.Is(err, backend.ErrThrottled),
		errors.Is(err, backend.ErrTransient),
		errors.Is(err, backend.ErrAuth),
		errors.Is(err, backend.ErrNotFound),
		errors.Is(err, backend.ErrConflict),
		errors.Is(err, backend.ErrChecksumMismatch),
		errors.Is(err, backend.ErrInvalidRange),
		errors.Is(err, backend.ErrPermanent):
		return backend.ClassifyError(err), true
	default:
		return "", false
	}
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
