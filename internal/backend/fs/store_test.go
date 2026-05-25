package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPutHeadReadRangeRoundTrip(t *testing.T) {
	store := openTestStore(t)
	data := []byte("backend object bytes")
	object, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "put object")
	if object.Length != uint64(len(data)) {
		t.Fatalf("object length = %d, want %d", object.Length, len(data))
	}

	head, err := store.HeadObject(context.Background(), "blocks/block-1")
	testutil.RequireNoErrorf(t, err, "head object")
	if head != object {
		t.Fatalf("head = %#v, want %#v", head, object)
	}

	length := uint64(6)
	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadObjectRange(context.Background(), "blocks/block-1", backend.Range{Offset: 8, Length: &length}, &got), "read object range")
	if got.String() != "object" {
		t.Fatalf("range = %q, want object", got.String())
	}
}

func TestPutObjectIsIdempotentForSameBytes(t *testing.T) {
	store := openTestStore(t)
	first, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("same bytes")))
	testutil.RequireNoErrorf(t, err, "put first object")
	second, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("same bytes")))
	testutil.RequireNoErrorf(t, err, "put second object")
	if second != first {
		t.Fatalf("second = %#v, want first %#v", second, first)
	}
}

func TestPutObjectConflictsForDifferentBytes(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("first bytes"))); err != nil {
		t.Fatalf("put first object: %v", err)
	}
	_, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("different bytes")))
	if !errors.Is(err, backend.ErrConflict) {
		t.Fatalf("put conflict error = %v, want %v", err, backend.ErrConflict)
	}
}

func TestPutMutableObjectReplacesExistingBytes(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "cells/local/metadata/v1/current.pointer", bytes.NewReader([]byte("pointer v1"))); err != nil {
		t.Fatalf("put first pointer: %v", err)
	}
	object, err := store.PutMutableObject(context.Background(), "cells/local/metadata/v1/current.pointer", bytes.NewReader([]byte("pointer v2")))
	testutil.RequireNoErrorf(t, err, "put mutable pointer")
	if object.Length != uint64(len("pointer v2")) {
		t.Fatalf("mutable object length = %d, want %d", object.Length, len("pointer v2"))
	}

	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadObjectRange(context.Background(), "cells/local/metadata/v1/current.pointer", backend.Range{}, &got), "read mutable pointer")
	if got.String() != "pointer v2" {
		t.Fatalf("mutable pointer = %q, want pointer v2", got.String())
	}
}

func TestPutObjectVerifiesExistingBytesBeforeIdempotentSuccess(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("verified bytes"))); err != nil {
		t.Fatalf("put object: %v", err)
	}
	testutil.RequireNoErrorf(t, os.WriteFile(store.objectPath("blocks/block-1", dataSuffix), []byte("corrupt bytes"), 0o600), "corrupt object")
	_, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("verified bytes")))
	if !errors.Is(err, backend.ErrChecksumMismatch) {
		t.Fatalf("put error = %v, want %v", err, backend.ErrChecksumMismatch)
	}
}

func TestReadObjectRangeDetectsCorruptionBeforeWritingBytes(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("verified bytes"))); err != nil {
		t.Fatalf("put object: %v", err)
	}
	testutil.RequireNoErrorf(t, os.WriteFile(store.objectPath("blocks/block-1", dataSuffix), []byte("corrupt bytes"), 0o600), "corrupt object")
	var got bytes.Buffer
	err := store.ReadObjectRange(context.Background(), "blocks/block-1", backend.Range{}, &got)
	if !errors.Is(err, backend.ErrChecksumMismatch) {
		t.Fatalf("read error = %v, want %v", err, backend.ErrChecksumMismatch)
	}
	if got.Len() != 0 {
		t.Fatalf("wrote %d bytes before corruption error", got.Len())
	}
}

func TestReadObjectRangeRejectsInvalidRange(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("short"))); err != nil {
		t.Fatalf("put object: %v", err)
	}
	length := uint64(10)
	err := store.ReadObjectRange(context.Background(), "blocks/block-1", backend.Range{Offset: 1, Length: &length}, &bytes.Buffer{})
	if !errors.Is(err, backend.ErrInvalidRange) {
		t.Fatalf("read error = %v, want %v", err, backend.ErrInvalidRange)
	}
}

func TestStoreRejectsEmptyKeysAndCanceledContexts(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "", strings.NewReader("data")); err == nil {
		t.Fatal("PutObject with empty key succeeded")
	}
	if _, err := store.PutMutableObject(context.Background(), "", strings.NewReader("data")); err == nil {
		t.Fatal("PutMutableObject with empty key succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.HeadObject(ctx, "blocks/block-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("HeadObject canceled error = %v, want %v", err, context.Canceled)
	}
	if _, err := store.PutObject(ctx, "blocks/block-1", strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutObject canceled error = %v, want %v", err, context.Canceled)
	}
}

func TestReadObjectRangeAllowsZeroLengthSelection(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("short"))); err != nil {
		t.Fatalf("put object: %v", err)
	}
	length := uint64(0)
	writer := failWriter{err: errors.New("writer should not be called")}
	err := store.ReadObjectRange(context.Background(), "blocks/block-1", backend.Range{Offset: 2, Length: &length}, writer)
	testutil.RequireNoErrorf(t, err, "read zero-length range")
}

func TestObjectFromMetadataValidatesChecksumShape(t *testing.T) {
	if _, err := objectFromMetadata(metadata{Key: "blocks/block-1", SHA256Hex: "not-hex"}); err == nil {
		t.Fatal("metadata with invalid hex checksum succeeded")
	}
	if _, err := objectFromMetadata(metadata{Key: "blocks/block-1", SHA256Hex: "01"}); err == nil {
		t.Fatal("metadata with short checksum succeeded")
	}
}

func TestCopyWithContextPropagatesFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyWithContext(ctx, io.Discard, strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy error = %v, want %v", err, context.Canceled)
	}

	writeErr := errors.New("write failed")
	if err := copyWithContext(context.Background(), failWriter{err: writeErr}, strings.NewReader("data")); !errors.Is(err, writeErr) {
		t.Fatalf("writer error = %v, want %v", err, writeErr)
	}
	if err := copyWithContext(context.Background(), shortWriter{}, strings.NewReader("data")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want %v", err, io.ErrShortWrite)
	}
	if err := copyWithContext(context.Background(), io.Discard, failReader{err: io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestObjectSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
	if _, err := store.PutObject(context.Background(), "blocks/block-1", bytes.NewReader([]byte("persisted"))); err != nil {
		t.Fatalf("put object: %v", err)
	}

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	var got bytes.Buffer
	testutil.RequireNoErrorf(t, reopened.ReadObjectRange(context.Background(), "blocks/block-1", backend.Range{}, &got), "read reopened object")
	if got.String() != "persisted" {
		t.Fatalf("reopened bytes = %q, want persisted", got.String())
	}
}

func TestStoreExposesExplicitFilesystemCapacityProfile(t *testing.T) {
	profile := backend.NonProductionFilesystemProfile("test-fs", 4*1024*1024*1024)
	store, err := OpenWithCapacityProfile(t.TempDir(), profile)
	testutil.RequireNoErrorf(t, err, "open store with profile")
	if store.Provider() != backend.ProviderFilesystem {
		t.Fatalf("provider = %s, want filesystem", store.Provider())
	}
	got := store.CapacityProfile()
	if got.ID != "test-fs" || got.Mode != backend.ProfileModeNonProduction || got.Provider != backend.ProviderFilesystem {
		t.Fatalf("capacity profile = %#v, want explicit non-production filesystem profile", got)
	}
	got.LaneBudgets = nil
	if len(store.CapacityProfile().LaneBudgets) == 0 {
		t.Fatal("capacity profile was not defensively copied")
	}
}

func TestStoreRejectsNonFilesystemCapacityProfile(t *testing.T) {
	profile := backend.NonProductionFilesystemProfile("test-s3", 4*1024*1024*1024)
	profile.Provider = backend.ProviderS3Like
	_, err := OpenWithCapacityProfile(t.TempDir(), profile)
	if err == nil || !errors.Is(err, backend.ErrPermanent) && !strings.Contains(err.Error(), "not filesystem") {
		t.Fatalf("open error = %v, want provider rejection", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open store")
	return store
}

type failWriter struct {
	err error
}

func (w failWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) {
	return 0, nil
}

type failReader struct {
	err error
}

func (r failReader) Read([]byte) (int, error) {
	return 0, r.err
}
