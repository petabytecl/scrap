package blockstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestAppendReadRoundTrip(t *testing.T) {
	store := openTestStore(t)
	data := append(bytes.Repeat([]byte("a"), 128*1024), bytes.Repeat([]byte("b"), 64*1024)...)

	record, err := store.Append(context.Background(), bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append")
	if record.StoredOffset != HeaderLength {
		t.Fatalf("stored offset = %d, want %d", record.StoredOffset, HeaderLength)
	}
	if record.StoredLength != uint64(len(data)) {
		t.Fatalf("stored length = %d, want %d", record.StoredLength, len(data))
	}
	if len(record.Frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(record.Frames))
	}

	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadRange(context.Background(), record, 0, nil, &got), "read range")
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("read bytes differ from appended bytes")
	}
}

func TestAppendRecordsStoredSHA256FromWrittenBytes(t *testing.T) {
	store := openTestStore(t)
	data := []byte("stored checksum bytes")
	want := sha256.Sum256(data)

	record, err := store.Append(context.Background(), bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append")

	testutil.RequireEqualf(t, record.StoredSHA256, want, "stored sha256")
	testutil.RequireEqualf(t, record.LogicalSHA256, want, "logical sha256")
}

func TestReadRangeReadsSubset(t *testing.T) {
	store := openTestStore(t)
	data := []byte("0123456789")
	record, err := store.Append(context.Background(), bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append")

	length := uint64(4)
	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadRange(context.Background(), record, 3, &length, &got), "read range")
	if got.String() != "3456" {
		t.Fatalf("range = %q, want 3456", got.String())
	}
}

func TestReadRangeDetectsChecksumMismatch(t *testing.T) {
	store := openTestStore(t)
	record, err := store.Append(context.Background(), bytes.NewReader([]byte("valid bytes")))
	testutil.RequireNoErrorf(t, err, "append")

	file, err := os.OpenFile(store.BlockPath(record.BlockID), os.O_WRONLY, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	if _, err := file.WriteAt([]byte("X"), testStoredOffset(t, record.StoredOffset)); err != nil {
		t.Fatalf("tamper block: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close block")

	err = store.ReadRange(context.Background(), record, 0, nil, io.Discard)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("read error = %v, want %v", err, ErrChecksumMismatch)
	}
}

func testStoredOffset(t *testing.T, offset uint64) int64 {
	t.Helper()
	got, err := safeconv.Uint64ToInt64("stored offset", offset)
	testutil.RequireNoErrorf(t, err, "convert stored offset")
	return got
}

func TestFailedAppendIsTruncated(t *testing.T) {
	store := openTestStore(t)
	streamErr := errors.New("stream failed")
	_, err := store.Append(context.Background(), &failingReader{
		data: []byte("partial"),
		err:  streamErr,
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("append error = %v, want %v", err, streamErr)
	}

	record, err := store.Append(context.Background(), bytes.NewReader([]byte("next")))
	testutil.RequireNoErrorf(t, err, "second append")
	if record.StoredOffset != HeaderLength {
		t.Fatalf("stored offset after failed append = %d, want %d", record.StoredOffset, HeaderLength)
	}
}

func TestValidatedAppendErrorIsTruncated(t *testing.T) {
	store := openTestStore(t)
	validationErr := errors.New("validation failed")
	_, err := store.AppendValidated(context.Background(), bytes.NewReader([]byte("rejected")), func(Record) error {
		return validationErr
	})
	if !errors.Is(err, validationErr) {
		t.Fatalf("append error = %v, want %v", err, validationErr)
	}

	record, err := store.Append(context.Background(), bytes.NewReader([]byte("accepted")))
	testutil.RequireNoErrorf(t, err, "second append")
	if record.StoredOffset != HeaderLength {
		t.Fatalf("stored offset after rejected append = %d, want %d", record.StoredOffset, HeaderLength)
	}
}

func TestSealCurrentMarksBlockSealedAndRotates(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	first, err := store.Append(ctx, bytes.NewReader([]byte("first")))
	testutil.RequireNoErrorf(t, err, "append first")
	if got, want := store.CurrentBlockLength(), HeaderLength+first.StoredLength; got != want {
		t.Fatalf("current block length = %d, want %d", got, want)
	}

	sealedBlockID, err := store.SealCurrent(ctx)
	testutil.RequireNoErrorf(t, err, "seal current")
	if sealedBlockID != first.BlockID {
		t.Fatalf("sealed block id = %q, want %q", sealedBlockID, first.BlockID)
	}
	sealed, err := store.IsSealed(first.BlockID)
	testutil.RequireNoErrorf(t, err, "is sealed")
	if !sealed {
		t.Fatalf("block %q was not marked sealed", first.BlockID)
	}

	second, err := store.Append(ctx, bytes.NewReader([]byte("second")))
	testutil.RequireNoErrorf(t, err, "append second")
	if second.BlockID == first.BlockID {
		t.Fatalf("second append used sealed block %q", second.BlockID)
	}
	if second.StoredOffset != HeaderLength {
		t.Fatalf("second stored offset = %d, want %d", second.StoredOffset, HeaderLength)
	}
	if got, want := store.CurrentBlockLength(), HeaderLength+second.StoredLength; got != want {
		t.Fatalf("rotated block length = %d, want %d", got, want)
	}
}

func TestSealBlockMarksRecoveredNonCurrentBlockSealed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
	first, err := store.Append(ctx, bytes.NewReader([]byte("first block")))
	testutil.RequireNoErrorf(t, err, "append first block")
	firstID := first.BlockID
	testutil.RequireNoErrorf(t, store.Close(), "close first store")
	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	if reopened.CurrentBlockID() == firstID {
		t.Fatalf("current block id = %s, want a new current block after reopen", firstID)
	}

	sealed, err := reopened.SealBlock(ctx, firstID)
	testutil.RequireNoErrorf(t, err, "seal recovered block")
	if !sealed {
		t.Fatal("seal recovered block returned false, want true")
	}
	isSealed, err := reopened.IsSealed(firstID)
	testutil.RequireNoErrorf(t, err, "is sealed")
	if !isSealed {
		t.Fatal("recovered block is not sealed")
	}
}

func TestSealCurrentRejectsEmptyBlock(t *testing.T) {
	store := openTestStore(t)
	_, err := store.SealCurrent(context.Background())
	if !errors.Is(err, ErrEmptyBlock) {
		t.Fatalf("seal error = %v, want %v", err, ErrEmptyBlock)
	}
}

func TestInstallSealedBlockRestoresVerifiedBlockFile(t *testing.T) {
	ctx := context.Background()
	source := openTestStore(t)
	record, err := source.Append(ctx, bytes.NewReader([]byte("restored block bytes")))
	testutil.RequireNoErrorf(t, err, "append source")
	if _, err := source.SealCurrent(ctx); err != nil {
		t.Fatalf("seal source: %v", err)
	}
	data, err := os.ReadFile(source.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read source block")
	sum := sha256.Sum256(data)

	target := openTestStore(t)
	testutil.RequireNoErrorf(t, target.InstallSealedBlock(ctx, record.BlockID, uint64(len(data)), sum, bytes.NewReader(data)), "install sealed block")
	sealed, err := target.IsSealed(record.BlockID)
	testutil.RequireNoErrorf(t, err, "is sealed")
	if !sealed {
		t.Fatalf("installed block %q is not sealed", record.BlockID)
	}
	installed, err := target.EnsureSealedBlock(ctx, record.BlockID, uint64(len(data)), sum)
	testutil.RequireNoErrorf(t, err, "ensure sealed block")
	if !installed {
		t.Fatalf("ensure sealed block returned false for installed block")
	}
	got, err := os.ReadFile(target.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read restored block")
	if !bytes.Equal(got, data) {
		t.Fatal("restored block bytes differ from source")
	}
}

func TestReplaceSealedBlockInstallsVerifiedReplacement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	record, err := store.Append(ctx, bytes.NewReader([]byte("stale block bytes")))
	testutil.RequireNoErrorf(t, err, "append stale block")
	_, err = store.SealCurrent(ctx)
	testutil.RequireNoErrorf(t, err, "seal stale block")
	replacement := []byte("replacement block bytes")
	replacementSHA := sha256.Sum256(replacement)

	err = store.ReplaceSealedBlock(ctx, record.BlockID, uint64(len(replacement)), replacementSHA, bytes.NewReader(replacement))
	testutil.RequireNoErrorf(t, err, "replace sealed block")
	sealed, err := store.IsSealed(record.BlockID)
	testutil.RequireNoErrorf(t, err, "is sealed")
	if !sealed {
		t.Fatalf("replacement block %q is not sealed", record.BlockID)
	}
	got, err := os.ReadFile(store.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read replaced block")
	if !bytes.Equal(got, replacement) {
		t.Fatalf("replaced block = %q, want %q", got, replacement)
	}
}

func TestReplaceSealedBlockPreservesOriginalOnReaderFailure(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	record, err := store.Append(ctx, bytes.NewReader([]byte("original block bytes")))
	testutil.RequireNoErrorf(t, err, "append original block")
	_, err = store.SealCurrent(ctx)
	testutil.RequireNoErrorf(t, err, "seal original block")
	original, err := os.ReadFile(store.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read original block")
	replacement := []byte("replacement block bytes")
	replacementSHA := sha256.Sum256(replacement)

	err = store.ReplaceSealedBlock(ctx, record.BlockID, uint64(len(replacement)), replacementSHA, iotest.ErrReader(io.ErrUnexpectedEOF))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("replace error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	after, err := os.ReadFile(store.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read original block after failed replace")
	if !bytes.Equal(after, original) {
		t.Fatalf("original block changed after failed replace")
	}
	sealed, err := store.IsSealed(record.BlockID)
	testutil.RequireNoErrorf(t, err, "is sealed")
	if !sealed {
		t.Fatalf("original block %q is not sealed after failed replace", record.BlockID)
	}
}

func TestReplaceSealedBlockValidatesContextAndReader(t *testing.T) {
	store := openTestStore(t)
	replacement := []byte("replacement block bytes")
	replacementSHA := sha256.Sum256(replacement)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	err := store.ReplaceSealedBlock(cancelled, "block-id", uint64(len(replacement)), replacementSHA, bytes.NewReader(replacement))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replace error = %v, want %v", err, context.Canceled)
	}
	err = store.ReplaceSealedBlock(context.Background(), "block-id", uint64(len(replacement)), replacementSHA, nil)
	if err == nil || !strings.Contains(err.Error(), "replacement block reader is required") {
		t.Fatalf("nil reader error = %v, want replacement reader validation", err)
	}
}

func TestInstallVerifiedRangeRepairsPreparedDocumentRange(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	record, err := store.Append(ctx, bytes.NewReader([]byte("healthy repaired range")))
	testutil.RequireNoErrorf(t, err, "append source")
	corrupt := []byte("corrupt repaired range")
	if err := store.InstallVerifiedRange(ctx, record, record.LogicalSHA256, bytes.NewReader(corrupt)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("corrupt range install error = %v, want %v", err, ErrChecksumMismatch)
	}
	testutil.RequireNoErrorf(t, os.Remove(store.BlockPath(record.BlockID)), "remove local block before repair")
	testutil.RequireNoErrorf(t, store.InstallVerifiedRange(ctx, record, record.LogicalSHA256, bytes.NewReader([]byte("healthy repaired range"))), "install verified range")
	length := record.StoredLength
	testutil.RequireNoErrorf(t, store.VerifyRange(record, 0, &length), "verify repaired range")
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close store")
	})
	return store
}

type failingReader struct {
	data []byte
	err  error
	sent bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, r.err
	}
	copy(p, r.data)
	r.sent = true
	return len(r.data), nil
}
