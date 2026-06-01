package shard

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestCopyRestoreObjectPreservesLocalWriteError(t *testing.T) {
	localErr := errors.New("disk full")
	tmp := &failingRestoreStagingFile{writeErr: localErr}
	confirmed := index.ConfirmedUpload{
		BlockID: 7,
		BlockObject: index.BackendObjectMetadata{
			SizeBytes: int64(len("payload")),
		},
	}

	err := copyRestoreObject(tmp, strings.NewReader("payload"), confirmed)
	if !errors.Is(err, localErr) {
		t.Fatalf("copyRestoreObject error = %v, want local write error", err)
	}
	if errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("copyRestoreObject error = %v, should not map local write errors to ErrUnavailable", err)
	}
	if !tmp.closed {
		t.Fatal("copyRestoreObject should close staging file on write failure")
	}
}

func TestCopyRestoreObjectPreservesLocalShortWrite(t *testing.T) {
	tmp := &failingRestoreStagingFile{shortWrite: true}
	confirmed := index.ConfirmedUpload{
		BlockID: 7,
		BlockObject: index.BackendObjectMetadata{
			SizeBytes: int64(len("payload")),
		},
	}

	err := copyRestoreObject(tmp, strings.NewReader("payload"), confirmed)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("copyRestoreObject error = %v, want ErrShortWrite", err)
	}
	if errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("copyRestoreObject error = %v, should not map short writes to ErrUnavailable", err)
	}
	if !tmp.closed {
		t.Fatal("copyRestoreObject should close staging file on short write")
	}
}

type failingRestoreStagingFile struct {
	writeErr   error
	shortWrite bool
	closed     bool
}

func (f *failingRestoreStagingFile) Write(p []byte) (int, error) {
	if f.shortWrite {
		return len(p) - 1, nil
	}
	return 0, f.writeErr
}

func (f *failingRestoreStagingFile) Sync() error {
	return nil
}

func (f *failingRestoreStagingFile) Close() error {
	f.closed = true
	return nil
}
