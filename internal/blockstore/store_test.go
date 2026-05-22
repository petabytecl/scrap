package blockstore

import (
	"bytes"
	"context"
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
