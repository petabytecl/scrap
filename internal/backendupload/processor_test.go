package backendupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestProcessorUploadsPendingIntentAndRecordsUploaded(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block bytes")))
	testutil.RequireNoErrorf(t, err, "append block")
	sealTestCurrentBlock(ctx, t, blocks)
	intent := testUploadIntent(record.BlockID)
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: store, Source: LocalBlockSource{Blocks: blocks}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
		Now:      fixedTime(time.Unix(500, 0).UTC()),
	}.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, uploaded: 1})
	requireUploadedIntentCall(t, updater.calls, 0, intent.BlockID, time.Unix(500, 0).UTC())
	_, err = store.HeadObject(ctx, intent.BackendObjectKey)
	testutil.RequireNoErrorf(t, err, "head uploaded object")
}

func TestProcessorRequiresConfiguredCollaborators(t *testing.T) {
	_, err := Processor{}.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upload intent lister") {
		t.Fatalf("missing lister error = %v, want not configured", err)
	}
	_, err = Processor{Intents: staticIntentLister{}}.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upload intent state updater") {
		t.Fatalf("missing updater error = %v, want not configured", err)
	}
}

func TestProcessorSkipsNonPendingIntents(t *testing.T) {
	intent := testUploadIntent("block-1")
	intent.State = metastore.UploadStateUploaded
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: openTestBackendStore(t), Source: staticBlockSource{body: []byte("block")}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
	}.RunOnce(context.Background())
	testutil.RequireNoErrorf(t, err, "run processor")
	if result.Scanned != 1 || result.Skipped != 1 || result.Uploaded != 0 || result.Failed != 0 {
		t.Fatalf("result = %#v, want one skipped intent", result)
	}
	if len(updater.calls) != 0 {
		t.Fatalf("state calls = %#v, want none", updater.calls)
	}
}

func TestProcessorRetriesFailedIntentAndRecordsUploaded(t *testing.T) {
	ctx := context.Background()
	intent := testUploadIntent("block-1")
	intent.State = metastore.UploadStateFailed
	intent.LastError = "backend throttled"
	intent.HasLastError = true
	store := openTestBackendStore(t)
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: store, Source: staticBlockSource{body: []byte("recovered block")}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
		Now:      fixedTime(time.Unix(501, 0).UTC()),
	}.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, uploaded: 1})
	requireUploadedIntentCall(t, updater.calls, 0, intent.BlockID, time.Unix(501, 0).UTC())
	_, err = store.HeadObject(ctx, intent.BackendObjectKey)
	testutil.RequireNoErrorf(t, err, "head uploaded object")
}

func TestProcessorRetriesPartialObjectSetAndRecordsUploaded(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("partial object set")))
	testutil.RequireNoErrorf(t, err, "append block")
	sealTestCurrentBlock(ctx, t, blocks)
	intent := testUploadIntent(record.BlockID)
	backendStore := &flakyHeadBackendStore{
		Store: store,
		failures: map[string]int{
			intent.IndexObjectKey: 1,
		},
	}
	updater := &recordingIntentStateUpdater{}
	processor := Processor{
		Uploader: Uploader{
			Backend: backendStore,
			Source:  LocalBlockSource{Blocks: blocks},
			Index:   staticBlockIndexSource{body: []byte("index bytes")},
		},
		Intents: staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater: updater,
		Now:     fixedTime(time.Unix(502, 0).UTC()),
	}

	result, err := processor.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run first processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, failed: 1})
	testutil.RequireEqualf(t, result.Errors[backend.ErrorClassNotFound], 1, "first not_found error count")
	requireFailedIntentCall(t, updater.calls, 0)

	retry := intent
	retry.State = metastore.UploadStateFailed
	retry.LastError = updater.calls[0].lastError
	retry.HasLastError = true
	processor.Intents = staticIntentLister{intents: []metastore.UploadIntent{retry}}
	result, err = processor.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run retry processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, uploaded: 1})
	requireUploadedIntentCall(t, updater.calls, 1, intent.BlockID, time.Unix(502, 0).UTC())
}

func TestProcessorRequestsBoundedPendingIntentBatch(t *testing.T) {
	first := testUploadIntent("block-1")
	second := testUploadIntent("block-2")
	lister := &recordingIntentLister{intents: []metastore.UploadIntent{first, second}}
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader:  Uploader{Backend: openTestBackendStore(t), Source: staticBlockSource{body: []byte("block")}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:   lister,
		Updater:   updater,
		BatchSize: 1,
	}.RunOnce(context.Background())
	testutil.RequireNoErrorf(t, err, "run processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, uploaded: 1})
	testutil.RequireEqualf(t, lister.limit, 1, "requested upload intent batch size")
	testutil.RequireEqualf(t, updater.calls[0].blockID, first.BlockID, "updated block id")
}

func TestProcessorTimesOutStalledUploadAndRecordsFailure(t *testing.T) {
	intent := testUploadIntent("block-1")
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{
			Backend: blockingPutBackendStore{Store: openTestBackendStore(t)},
			Source:  staticBlockSource{body: []byte("block")},
			Index:   staticBlockIndexSource{body: []byte("index")},
		},
		Intents:       staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:       updater,
		UploadTimeout: 10 * time.Millisecond,
	}.RunOnce(context.Background())
	testutil.RequireNoErrorf(t, err, "run processor")
	requireProcessorCounts(t, result, processorCounts{scanned: 1, failed: 1})
	requireFailedIntentCall(t, updater.calls, 0)
	if !strings.Contains(updater.calls[0].lastError, context.DeadlineExceeded.Error()) {
		t.Fatalf("last error = %q, want deadline exceeded", updater.calls[0].lastError)
	}
}

type processorCounts struct {
	scanned  int
	uploaded int
	failed   int
	skipped  int
}

func requireProcessorCounts(t *testing.T, result RunResult, want processorCounts) {
	t.Helper()
	testutil.RequireEqualf(t, result.Scanned, want.scanned, "processor scanned count")
	testutil.RequireEqualf(t, result.Uploaded, want.uploaded, "processor uploaded count")
	testutil.RequireEqualf(t, result.Failed, want.failed, "processor failed count")
	testutil.RequireEqualf(t, result.Skipped, want.skipped, "processor skipped count")
}

func requireUploadedIntentCall(t *testing.T, calls []stateCall, index int, blockID string, proposedAt time.Time) {
	t.Helper()
	testutil.RequireTruef(t, len(calls) > index, "state calls = %#v, want index %d", calls, index)
	call := calls[index]
	testutil.RequireEqualf(t, call.blockID, blockID, "state call block id")
	testutil.RequireEqualf(t, call.state, metastore.UploadStateUploaded, "state call state")
	testutil.RequireEqualf(t, call.lastError, "", "state call last error")
	testutil.RequireTruef(t, call.commandID != "", "state call command id is empty")
	testutil.RequireTruef(t, call.proposedAt.Equal(proposedAt), "state call proposed_at = %s, want %s", call.proposedAt, proposedAt)
}

func requireFailedIntentCall(t *testing.T, calls []stateCall, index int) {
	t.Helper()
	testutil.RequireTruef(t, len(calls) > index, "state calls = %#v, want index %d", calls, index)
	call := calls[index]
	testutil.RequireEqualf(t, call.state, metastore.UploadStateFailed, "state call state")
	testutil.RequireTruef(t, call.lastError != "", "state call last error is empty")
}

func TestProcessorRecoversDurableFailedIntentAfterRestart(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("durable partial object set")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	backendStore := &flakyHeadBackendStore{
		Store: store,
		failures: map[string]int{
			intent.IndexObjectKey: 1,
		},
	}
	metadataDir := t.TempDir()
	metadata := openMetastoreAt(t, metadataDir)
	testutil.RequireNoErrorf(t, metadata.RecordUploadIntent(intent), "record upload intent")
	processor := Processor{
		Uploader: Uploader{
			Backend: backendStore,
			Source:  LocalBlockSource{Blocks: blocks},
			Index:   staticBlockIndexSource{body: []byte("index bytes")},
		},
		Intents: metadata,
		Updater: metastoreIntentStateUpdater{Store: metadata},
		Now:     fixedTime(time.Unix(503, 0).UTC()),
	}

	result, err := processor.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run first processor")
	if result.Scanned != 1 || result.Failed != 1 || result.Uploaded != 0 {
		t.Fatalf("first result = %#v, want failed partial object set", result)
	}
	testutil.RequireNoErrorf(t, metadata.Close(), "close metastore before restart")
	metadata = openMetastoreAt(t, metadataDir)
	processor.Intents = metadata
	processor.Updater = metastoreIntentStateUpdater{Store: metadata}
	processor.Now = fixedTime(time.Unix(504, 0).UTC())

	result, err = processor.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run restarted processor")
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 {
		t.Fatalf("restart result = %#v, want uploaded object set", result)
	}
	got, err := metadata.GetUploadIntent(intent.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent after restart")
	if got.State != metastore.UploadStateUploaded || got.HasLastError {
		t.Fatalf("upload intent after restart = %#v, want uploaded without error", got)
	}
}

func TestProcessorDefersOpenLocalBlockWithoutRecordingFailure(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("open block bytes")))
	testutil.RequireNoErrorf(t, err, "append block")
	intent := testUploadIntent(record.BlockID)
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: openTestBackendStore(t), Source: LocalBlockSource{Blocks: blocks}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
	}.RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run processor")
	if result.Scanned != 1 || result.Deferred != 1 || result.Failed != 0 || result.Uploaded != 0 {
		t.Fatalf("result = %#v, want one deferred open block", result)
	}
	if len(updater.calls) != 0 {
		t.Fatalf("state calls = %#v, want none for deferred open block", updater.calls)
	}
}

func TestProcessorRecordsBackendErrorClass(t *testing.T) {
	providerErr := backend.NewError(backend.ErrorClassThrottled, "put", "objects/block-1.blk", backend.ErrThrottled)
	intent := testUploadIntent("block-1")
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{
			Backend: failingPutBackendStore{
				Store: openTestBackendStore(t),
				err:   providerErr,
			},
			Source: staticBlockSource{body: []byte("block")},
			Index:  staticBlockIndexSource{body: []byte("index")},
		},
		Intents: staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater: updater,
	}.RunOnce(context.Background())
	testutil.RequireNoErrorf(t, err, "run processor")
	if result.Scanned != 1 || result.Failed != 1 || result.Uploaded != 0 {
		t.Fatalf("result = %#v, want failed provider upload", result)
	}
	if result.Errors[backend.ErrorClassThrottled] != 1 {
		t.Fatalf("errors = %#v, want one throttled provider error", result.Errors)
	}
	if len(updater.calls) != 1 || updater.calls[0].state != metastore.UploadStateFailed {
		t.Fatalf("state calls = %#v, want one failed call", updater.calls)
	}
}

func TestProcessorRecordsFailedUploadAndContinues(t *testing.T) {
	sourceErr := errors.New("source unavailable")
	first := testUploadIntent("block-1")
	second := testUploadIntent("block-2")
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: openTestBackendStore(t), Source: staticBlockSource{err: sourceErr}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{first, second}},
		Updater:  updater,
	}.RunOnce(context.Background())
	testutil.RequireNoErrorf(t, err, "run processor")
	if result.Scanned != 2 || result.Failed != 2 || result.Uploaded != 0 {
		t.Fatalf("result = %#v, want two failed uploads", result)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v, want no backend/provider error classes for source errors", result.Errors)
	}
	if len(updater.calls) != 2 {
		t.Fatalf("state calls = %#v, want two failed calls", updater.calls)
	}
	for _, call := range updater.calls {
		if call.state != metastore.UploadStateFailed || call.lastError != sourceErr.Error() {
			t.Fatalf("state call = %#v, want failed source error", call)
		}
	}
}

func TestProcessorReturnsUpdaterErrorAfterUpload(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block bytes")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	updateErr := errors.New("raft unavailable")

	result, err := Processor{
		Uploader: Uploader{Backend: store, Source: LocalBlockSource{Blocks: blocks}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  &recordingIntentStateUpdater{err: updateErr},
	}.RunOnce(ctx)
	if !errors.Is(err, updateErr) {
		t.Fatalf("error = %v, want %v", err, updateErr)
	}
	if result.Uploaded != 0 || result.Failed != 0 {
		t.Fatalf("result = %#v, want no recorded outcome after updater failure", result)
	}
	if _, err := store.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		t.Fatalf("object should have uploaded before updater error: %v", err)
	}
}

type staticIntentLister struct {
	intents []metastore.UploadIntent
	err     error
}

func (l staticIntentLister) ListPendingUploadIntents(limit int) ([]metastore.UploadIntent, error) {
	if l.err != nil {
		return nil, l.err
	}
	if limit > 0 && len(l.intents) > limit {
		return append([]metastore.UploadIntent(nil), l.intents[:limit]...), nil
	}
	return append([]metastore.UploadIntent(nil), l.intents...), nil
}

type recordingIntentLister struct {
	intents []metastore.UploadIntent
	limit   int
}

func (l *recordingIntentLister) ListPendingUploadIntents(limit int) ([]metastore.UploadIntent, error) {
	l.limit = limit
	if limit > 0 && len(l.intents) > limit {
		return append([]metastore.UploadIntent(nil), l.intents[:limit]...), nil
	}
	return append([]metastore.UploadIntent(nil), l.intents...), nil
}

type metastoreIntentStateUpdater struct {
	Store *metastore.Store
}

func (u metastoreIntentStateUpdater) UpdateUploadIntentState(_ context.Context, blockID string, state metastore.UploadState, lastError, _ string, proposedAt time.Time) error {
	_, err := u.Store.UpdateUploadIntentState(blockID, state, lastError, proposedAt)
	return err
}

type failingPutBackendStore struct {
	backend.Store
	err error
}

func (s failingPutBackendStore) PutObject(context.Context, string, io.Reader) (backend.Object, error) {
	return backend.Object{}, s.err
}

type blockingPutBackendStore struct {
	backend.Store
}

func (s blockingPutBackendStore) PutObject(ctx context.Context, _ string, _ io.Reader) (backend.Object, error) {
	<-ctx.Done()
	return backend.Object{}, ctx.Err()
}

type stateCall struct {
	blockID    string
	state      metastore.UploadState
	lastError  string
	commandID  string
	proposedAt time.Time
}

type recordingIntentStateUpdater struct {
	calls []stateCall
	err   error
}

func (u *recordingIntentStateUpdater) UpdateUploadIntentState(_ context.Context, blockID string, state metastore.UploadState, lastError, commandID string, proposedAt time.Time) error {
	if u.err != nil {
		return u.err
	}
	u.calls = append(u.calls, stateCall{
		blockID:    blockID,
		state:      state,
		lastError:  lastError,
		commandID:  commandID,
		proposedAt: proposedAt,
	})
	return nil
}

func fixedTime(value time.Time) func() time.Time {
	return func() time.Time { return value }
}

func openMetastoreAt(t *testing.T, dir string) *metastore.Store {
	t.Helper()
	store, err := metastore.Open(dir)
	testutil.RequireNoErrorf(t, err, "open metastore")
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
