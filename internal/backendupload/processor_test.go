package backendupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestProcessorUploadsPendingIntentAndRecordsUploaded(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block bytes")))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: store, Source: LocalBlockSource{Blocks: blocks}, Index: staticBlockIndexSource{body: []byte("index")}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
		Now:      fixedTime(time.Unix(500, 0).UTC()),
	}.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("result = %#v, want one upload", result)
	}
	if len(updater.calls) != 1 ||
		updater.calls[0].blockID != intent.BlockID ||
		updater.calls[0].state != metastore.UploadStateUploaded ||
		updater.calls[0].lastError != "" ||
		updater.calls[0].commandID == "" ||
		!updater.calls[0].proposedAt.Equal(time.Unix(500, 0).UTC()) {
		t.Fatalf("state calls = %#v, want uploaded call", updater.calls)
	}
	if _, err := store.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		t.Fatalf("head uploaded object: %v", err)
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
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
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
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("result = %#v, want failed intent retried and uploaded", result)
	}
	if len(updater.calls) != 1 ||
		updater.calls[0].blockID != intent.BlockID ||
		updater.calls[0].state != metastore.UploadStateUploaded ||
		updater.calls[0].lastError != "" ||
		!updater.calls[0].proposedAt.Equal(time.Unix(501, 0).UTC()) {
		t.Fatalf("state calls = %#v, want uploaded retry call", updater.calls)
	}
	if _, err := store.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		t.Fatalf("head uploaded object: %v", err)
	}
}

func TestProcessorRetriesPartialObjectSetAndRecordsUploaded(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("partial object set")))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
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
	if err != nil {
		t.Fatalf("run first processor: %v", err)
	}
	if result.Scanned != 1 || result.Failed != 1 || result.Uploaded != 0 {
		t.Fatalf("first result = %#v, want failed partial object set", result)
	}
	if result.Errors[backend.ErrorClassNotFound] != 1 {
		t.Fatalf("first errors = %#v, want one not_found verification error", result.Errors)
	}
	if len(updater.calls) != 1 ||
		updater.calls[0].state != metastore.UploadStateFailed ||
		updater.calls[0].lastError == "" {
		t.Fatalf("first state calls = %#v, want failed verification", updater.calls)
	}

	retry := intent
	retry.State = metastore.UploadStateFailed
	retry.LastError = updater.calls[0].lastError
	retry.HasLastError = true
	processor.Intents = staticIntentLister{intents: []metastore.UploadIntent{retry}}
	result, err = processor.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run retry processor: %v", err)
	}
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 {
		t.Fatalf("retry result = %#v, want uploaded object set", result)
	}
	if len(updater.calls) != 2 ||
		updater.calls[1].state != metastore.UploadStateUploaded ||
		updater.calls[1].lastError != "" {
		t.Fatalf("retry state calls = %#v, want uploaded retry", updater.calls)
	}
}

func TestProcessorRecoversDurableFailedIntentAfterRestart(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("durable partial object set")))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
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
	if err := metadata.RecordUploadIntent(intent); err != nil {
		t.Fatalf("record upload intent: %v", err)
	}
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
	if err != nil {
		t.Fatalf("run first processor: %v", err)
	}
	if result.Scanned != 1 || result.Failed != 1 || result.Uploaded != 0 {
		t.Fatalf("first result = %#v, want failed partial object set", result)
	}
	if err := metadata.Close(); err != nil {
		t.Fatalf("close metastore before restart: %v", err)
	}
	metadata = openMetastoreAt(t, metadataDir)
	processor.Intents = metadata
	processor.Updater = metastoreIntentStateUpdater{Store: metadata}
	processor.Now = fixedTime(time.Unix(504, 0).UTC())

	result, err = processor.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run restarted processor: %v", err)
	}
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 {
		t.Fatalf("restart result = %#v, want uploaded object set", result)
	}
	got, err := metadata.GetUploadIntent(intent.BlockID)
	if err != nil {
		t.Fatalf("get upload intent after restart: %v", err)
	}
	if got.State != metastore.UploadStateUploaded || got.HasLastError {
		t.Fatalf("upload intent after restart = %#v, want uploaded without error", got)
	}
}

func TestProcessorDefersOpenLocalBlockWithoutRecordingFailure(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("open block bytes")))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	updater := &recordingIntentStateUpdater{}

	result, err := Processor{
		Uploader: Uploader{Backend: openTestBackendStore(t), Source: LocalBlockSource{Blocks: blocks}},
		Intents:  staticIntentLister{intents: []metastore.UploadIntent{intent}},
		Updater:  updater,
	}.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
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
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
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
	if err != nil {
		t.Fatalf("run processor: %v", err)
	}
	if result.Scanned != 2 || result.Failed != 2 || result.Uploaded != 0 {
		t.Fatalf("result = %#v, want two failed uploads", result)
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
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
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

func (l staticIntentLister) ListUploadIntents() ([]metastore.UploadIntent, error) {
	if l.err != nil {
		return nil, l.err
	}
	return append([]metastore.UploadIntent(nil), l.intents...), nil
}

type metastoreIntentStateUpdater struct {
	Store *metastore.Store
}

func (u metastoreIntentStateUpdater) UpdateUploadIntentState(_ context.Context, blockID string, state metastore.UploadState, lastError string, _ string, proposedAt time.Time) error {
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

func (u *recordingIntentStateUpdater) UpdateUploadIntentState(_ context.Context, blockID string, state metastore.UploadState, lastError string, commandID string, proposedAt time.Time) error {
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
	if err != nil {
		t.Fatalf("open metastore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
