package block_test

// Coverage for the streaming encrypted write/read paths (#471b): every
// encrypted Document flows through AppendDocumentFrameSource on write and
// StoredFrameSource / ReadDocumentFramesFromBlock on read, yet these were at 0%
// in-package coverage. Their behavior on the empty/mid-error/corruption paths
// is what keeps a ciphertext read from silently diverging.

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

const (
	frameSourceShardID = 3
	frameSourceBlockID = 77
)

// framePayloads returns a next() that yields the given payloads, flagging the
// last one, then io.EOF.
func framePayloads(payloads [][]byte) func() ([]byte, bool, error) {
	i := 0
	return func() ([]byte, bool, error) {
		if i >= len(payloads) {
			return nil, false, io.EOF
		}
		p := payloads[i]
		last := i == len(payloads)-1
		i++
		return p, last, nil
	}
}

func newFrameSourceWriter(t *testing.T) (*block.Writer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fs.blk")
	bw, err := block.NewWriter(path, frameSourceShardID, frameSourceBlockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = bw.Close() })
	return bw, path
}

func TestAppendDocumentFrameSourceRoundTrip(t *testing.T) {
	bw, path := newFrameSourceWriter(t)
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), block.MaxFramePayload),
		bytes.Repeat([]byte("b"), block.MaxFramePayload),
		[]byte("tail ciphertext"),
	}

	firstOffset, frameCount, err := bw.AppendDocumentFrameSource(framePayloads(payloads))
	if err != nil {
		t.Fatalf("AppendDocumentFrameSource: %v", err)
	}
	if int(frameCount) != len(payloads) {
		t.Fatalf("frameCount = %d, want %d", frameCount, len(payloads))
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entry := block.IndexEntry{FirstFrameOff: firstOffset, FrameCount: frameCount}
	assertStoredFrameSourceYields(t, path, entry, payloads)

	// Buffered read via ReadDocumentFramesFromBlock returns the same payloads.
	frames, err := block.ReadDocumentFramesFromBlock(path, frameSourceShardID, frameSourceBlockID, entry)
	if err != nil {
		t.Fatalf("ReadDocumentFramesFromBlock: %v", err)
	}
	if len(frames) != len(payloads) {
		t.Fatalf("read %d frames, want %d", len(frames), len(payloads))
	}
	for i := range payloads {
		if !bytes.Equal(frames[i], payloads[i]) {
			t.Fatalf("read frame %d mismatch", i)
		}
	}
}

func assertStoredFrameSourceYields(t *testing.T, path string, entry block.IndexEntry, payloads [][]byte) {
	t.Helper()
	src, err := block.OpenDocumentFrameSource(path, frameSourceShardID, frameSourceBlockID, entry)
	if err != nil {
		t.Fatalf("OpenDocumentFrameSource: %v", err)
	}
	defer func() { _ = src.Close() }()
	for i, want := range payloads {
		got, err := src.NextFrame()
		if err != nil {
			t.Fatalf("NextFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}
	if _, err := src.NextFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("NextFrame after last = %v, want io.EOF", err)
	}
}

func TestAppendDocumentFrameSourceRejectsEmptySource(t *testing.T) {
	bw, _ := newFrameSourceWriter(t)
	_, _, err := bw.AppendDocumentFrameSource(framePayloads(nil))
	if err == nil {
		t.Fatal("AppendDocumentFrameSource accepted an empty source, want error")
	}
}

func TestAppendDocumentFrameSourcePropagatesSourceError(t *testing.T) {
	bw, _ := newFrameSourceWriter(t)
	sentinel := errors.New("source failed mid-stream")
	calls := 0
	next := func() ([]byte, bool, error) {
		calls++
		if calls == 1 {
			return []byte("first"), false, nil
		}
		return nil, false, sentinel
	}
	if _, _, err := bw.AppendDocumentFrameSource(next); !errors.Is(err, sentinel) {
		t.Fatalf("AppendDocumentFrameSource error = %v, want sentinel", err)
	}
}

func TestAppendDocumentFrameSourceRejectsClosedWriter(t *testing.T) {
	bw, _ := newFrameSourceWriter(t)
	if err := bw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, err := bw.AppendDocumentFrameSource(framePayloads([][]byte{[]byte("x")})); err == nil {
		t.Fatal("AppendDocumentFrameSource on a closed writer succeeded, want error")
	}
}

func TestOpenDocumentFrameSourceRejectsWrongHeader(t *testing.T) {
	bw, path := newFrameSourceWriter(t)
	firstOffset, frameCount, err := bw.AppendDocumentFrameSource(framePayloads([][]byte{[]byte("x")}))
	if err != nil {
		t.Fatalf("AppendDocumentFrameSource: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entry := block.IndexEntry{FirstFrameOff: firstOffset, FrameCount: frameCount}

	// Wrong shard/block identity must be rejected before any frame is read.
	if _, err := block.OpenDocumentFrameSource(path, frameSourceShardID+1, frameSourceBlockID, entry); err == nil {
		t.Fatal("OpenDocumentFrameSource accepted a wrong shard id, want error")
	}
	if _, err := block.ReadDocumentFramesFromBlock(path, frameSourceShardID, frameSourceBlockID+1, entry); err == nil {
		t.Fatal("ReadDocumentFramesFromBlock accepted a wrong block id, want error")
	}
}

func TestStoredFrameSourceRejectsFrameCountPastEOF(t *testing.T) {
	bw, path := newFrameSourceWriter(t)
	firstOffset, frameCount, err := bw.AppendDocumentFrameSource(framePayloads([][]byte{[]byte("only")}))
	if err != nil {
		t.Fatalf("AppendDocumentFrameSource: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// An index entry claiming more frames than were written must fail rather than
	// silently return a short Document.
	entry := block.IndexEntry{FirstFrameOff: firstOffset, FrameCount: frameCount + 1}

	src, err := block.OpenDocumentFrameSource(path, frameSourceShardID, frameSourceBlockID, entry)
	if err != nil {
		t.Fatalf("OpenDocumentFrameSource: %v", err)
	}
	defer func() { _ = src.Close() }()
	if _, err := src.NextFrame(); err == nil {
		// first frame reads fine
		if _, err := src.NextFrame(); err == nil {
			t.Fatal("NextFrame past EOF succeeded, want read error")
		}
	}

	if _, err := block.ReadDocumentFramesFromBlock(path, frameSourceShardID, frameSourceBlockID, entry); err == nil {
		t.Fatal("ReadDocumentFramesFromBlock with an over-count entry succeeded, want error")
	}
}
