package blockstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"testing"
)

func TestAppendReadRoundTrip(t *testing.T) {
	store := openTestStore(t)
	data := append(bytes.Repeat([]byte("a"), 128*1024), bytes.Repeat([]byte("b"), 64*1024)...)

	record, err := store.Append(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
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
	if err := store.ReadRange(context.Background(), record, 0, nil, &got); err != nil {
		t.Fatalf("read range: %v", err)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("read bytes differ from appended bytes")
	}
}

func TestReadRangeReadsSubset(t *testing.T) {
	store := openTestStore(t)
	data := []byte("0123456789")
	record, err := store.Append(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	length := uint64(4)
	var got bytes.Buffer
	if err := store.ReadRange(context.Background(), record, 3, &length, &got); err != nil {
		t.Fatalf("read range: %v", err)
	}
	if got.String() != "3456" {
		t.Fatalf("range = %q, want 3456", got.String())
	}
}

func TestReadRangeDetectsChecksumMismatch(t *testing.T) {
	store := openTestStore(t)
	record, err := store.Append(context.Background(), bytes.NewReader([]byte("valid bytes")))
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	file, err := os.OpenFile(store.BlockPath(record.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(record.StoredOffset)); err != nil {
		t.Fatalf("tamper block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}

	err = store.ReadRange(context.Background(), record, 0, nil, io.Discard)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("read error = %v, want %v", err, ErrChecksumMismatch)
	}
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
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
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
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if record.StoredOffset != HeaderLength {
		t.Fatalf("stored offset after rejected append = %d, want %d", record.StoredOffset, HeaderLength)
	}
}

func TestSealCurrentMarksBlockSealedAndRotates(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	first, err := store.Append(ctx, bytes.NewReader([]byte("first")))
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	if got, want := store.CurrentBlockLength(), HeaderLength+first.StoredLength; got != want {
		t.Fatalf("current block length = %d, want %d", got, want)
	}

	sealedBlockID, err := store.SealCurrent(ctx)
	if err != nil {
		t.Fatalf("seal current: %v", err)
	}
	if sealedBlockID != first.BlockID {
		t.Fatalf("sealed block id = %q, want %q", sealedBlockID, first.BlockID)
	}
	sealed, err := store.IsSealed(first.BlockID)
	if err != nil {
		t.Fatalf("is sealed: %v", err)
	}
	if !sealed {
		t.Fatalf("block %q was not marked sealed", first.BlockID)
	}

	second, err := store.Append(ctx, bytes.NewReader([]byte("second")))
	if err != nil {
		t.Fatalf("append second: %v", err)
	}
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
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	first, err := store.Append(ctx, bytes.NewReader([]byte("first block")))
	if err != nil {
		t.Fatalf("append first block: %v", err)
	}
	firstID := first.BlockID
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	if reopened.CurrentBlockID() == firstID {
		t.Fatalf("current block id = %s, want a new current block after reopen", firstID)
	}

	sealed, err := reopened.SealBlock(ctx, firstID)
	if err != nil {
		t.Fatalf("seal recovered block: %v", err)
	}
	if !sealed {
		t.Fatal("seal recovered block returned false, want true")
	}
	isSealed, err := reopened.IsSealed(firstID)
	if err != nil {
		t.Fatalf("is sealed: %v", err)
	}
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
	if err != nil {
		t.Fatalf("append source: %v", err)
	}
	if _, err := source.SealCurrent(ctx); err != nil {
		t.Fatalf("seal source: %v", err)
	}
	data, err := os.ReadFile(source.BlockPath(record.BlockID))
	if err != nil {
		t.Fatalf("read source block: %v", err)
	}
	sum := sha256.Sum256(data)

	target := openTestStore(t)
	if err := target.InstallSealedBlock(ctx, record.BlockID, uint64(len(data)), sum, bytes.NewReader(data)); err != nil {
		t.Fatalf("install sealed block: %v", err)
	}
	sealed, err := target.IsSealed(record.BlockID)
	if err != nil {
		t.Fatalf("is sealed: %v", err)
	}
	if !sealed {
		t.Fatalf("installed block %q is not sealed", record.BlockID)
	}
	installed, err := target.EnsureSealedBlock(ctx, record.BlockID, uint64(len(data)), sum)
	if err != nil {
		t.Fatalf("ensure sealed block: %v", err)
	}
	if !installed {
		t.Fatalf("ensure sealed block returned false for installed block")
	}
	got, err := os.ReadFile(target.BlockPath(record.BlockID))
	if err != nil {
		t.Fatalf("read restored block: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("restored block bytes differ from source")
	}
}

func TestInstallVerifiedRangeRepairsPreparedDocumentRange(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	record, err := store.Append(ctx, bytes.NewReader([]byte("healthy repaired range")))
	if err != nil {
		t.Fatalf("append source: %v", err)
	}
	corrupt := []byte("corrupt repaired range")
	if err := store.InstallVerifiedRange(ctx, record, record.LogicalSHA256, bytes.NewReader(corrupt)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("corrupt range install error = %v, want %v", err, ErrChecksumMismatch)
	}
	if err := os.Remove(store.BlockPath(record.BlockID)); err != nil {
		t.Fatalf("remove local block before repair: %v", err)
	}
	if err := store.InstallVerifiedRange(ctx, record, record.LogicalSHA256, bytes.NewReader([]byte("healthy repaired range"))); err != nil {
		t.Fatalf("install verified range: %v", err)
	}
	length := record.StoredLength
	if err := store.VerifyRange(record, 0, &length); err != nil {
		t.Fatalf("verify repaired range: %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
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
