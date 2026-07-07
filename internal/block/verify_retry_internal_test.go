package block

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// faultingReadSeeker injects I/O-class read failures at chosen call numbers.
type faultingReadSeeker struct {
	inner     io.ReadSeeker
	readCalls int
	failCalls map[int]bool
	failAll   bool
	failFrom  int
	seeks     int
}

var errInjectedIO = errors.New("injected I/O fault")

func (f *faultingReadSeeker) Read(p []byte) (int, error) {
	f.readCalls++
	if f.failAll && f.readCalls >= f.failFrom {
		return 0, errInjectedIO
	}
	if f.failCalls[f.readCalls] {
		return 0, errInjectedIO
	}
	return f.inner.Read(p)
}

func (f *faultingReadSeeker) Seek(offset int64, whence int) (int64, error) {
	f.seeks++
	return f.inner.Seek(offset, whence)
}

func writeRetryTestBlock(t *testing.T, dir string) (blkPath string, idxEntries []IndexEntry) {
	t.Helper()

	blkPath = filepath.Join(dir, "0000000000000064.blk")
	idxPath := filepath.Join(dir, "0000000000000064.idx")

	bw, err := NewWriter(blkPath, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		result, err := bw.AppendDocument("tx-retry", name, "text/plain", bytes.NewReader(bytes.Repeat([]byte(name[:1]), 512)))
		if err != nil {
			t.Fatalf("AppendDocument %s: %v", name, err)
		}
		if err := iw.Append(IndexEntry{
			TransactionID: "tx-retry",
			DocName:       name,
			ContentType:   "text/plain",
			CreatedAt:     time.Now(),
			FirstFrameOff: result.FirstFrameOffset,
			FrameCount:    result.FrameCount,
			TotalBytes:    result.Size,
			SHA256:        result.SHA256,
		}); err != nil {
			t.Fatalf("Append index %s: %v", name, err)
		}
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}

	entries, err := loadIdxEntries(idxPath)
	if err != nil {
		t.Fatalf("loadIdxEntries: %v", err)
	}
	return blkPath, entries
}

func openPastHeader(t *testing.T, blkPath string) *os.File {
	t.Helper()
	f, err := os.Open(blkPath) //nolint:gosec // test path under t.TempDir
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Seek(HeaderSize, io.SeekStart); err != nil {
		t.Fatalf("seek past header: %v", err)
	}
	return f
}

// Regression for #470: a single transient I/O read fault must not record the
// frame as corrupt — verification re-seeks and re-reads once, reports the
// retry, and finishes clean.
func TestVerifyFramesRetriesTransientReadFaultOnce(t *testing.T) {
	blkPath, entries := writeRetryTestBlock(t, t.TempDir())
	f := openPastHeader(t, blkPath)

	faulty := &faultingReadSeeker{inner: f, failCalls: map[int]bool{2: true}}
	result := verifyFrames(faulty, entries)

	if len(result.CorruptFrames) != 0 {
		t.Fatalf("corrupt frames = %+v, want none after clean re-read", result.CorruptFrames)
	}
	if result.TransientReadRetries != 1 {
		t.Fatalf("transient retries = %d, want 1", result.TransientReadRetries)
	}
	if result.FramesVerified == 0 {
		t.Fatal("no frames verified")
	}
}

// A persistent read fault still counts as corruption after the bounded
// re-read: more retries would mask a dying disk.
func TestVerifyFramesPersistentReadFaultIsCorruption(t *testing.T) {
	blkPath, entries := writeRetryTestBlock(t, t.TempDir())
	f := openPastHeader(t, blkPath)

	faulty := &faultingReadSeeker{inner: f, failAll: true, failFrom: 2}
	result := verifyFrames(faulty, entries)

	if len(result.CorruptFrames) == 0 {
		t.Fatal("persistent read fault not recorded as corruption")
	}
	if result.TransientReadRetries != 0 {
		t.Fatalf("transient retries = %d, want 0 for persistent fault", result.TransientReadRetries)
	}
	if faulty.seeks != 1 {
		t.Fatalf("seeks = %d, want exactly one bounded retry", faulty.seeks)
	}
}

// A checksum mismatch is proven corruption on successfully read bytes and
// must never trigger a re-read.
func TestVerifyFramesDoesNotRetryChecksumCorruption(t *testing.T) {
	dir := t.TempDir()
	blkPath, entries := writeRetryTestBlock(t, dir)

	data, err := os.ReadFile(blkPath) //nolint:gosec // test path under t.TempDir
	if err != nil {
		t.Fatalf("read block: %v", err)
	}
	data[HeaderSize+FrameHeaderSize] ^= 0xFF                   // flip a payload byte in frame 1
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test path under t.TempDir
		t.Fatalf("write corrupted block: %v", err)
	}

	f := openPastHeader(t, blkPath)
	faulty := &faultingReadSeeker{inner: f}
	result := verifyFrames(faulty, entries)

	if len(result.CorruptFrames) == 0 {
		t.Fatal("checksum corruption not recorded")
	}
	if faulty.seeks != 0 {
		t.Fatalf("seeks = %d, want no retry for checksum corruption", faulty.seeks)
	}
	if result.TransientReadRetries != 0 {
		t.Fatalf("transient retries = %d, want 0", result.TransientReadRetries)
	}
}

// The injected fault must classify as I/O (ErrVerifyRead), not as EOF or a
// structural error, or the retry paths above test nothing.
func TestReadFrameRawClassifiesIOFailure(t *testing.T) {
	blkPath, _ := writeRetryTestBlock(t, t.TempDir())
	f := openPastHeader(t, blkPath)

	faulty := &faultingReadSeeker{inner: f, failAll: true, failFrom: 1}
	_, _, err := readFrameRaw(faulty)
	if !errors.Is(err, ErrVerifyRead) {
		t.Fatalf("readFrameRaw error = %v, want ErrVerifyRead", err)
	}
	if !errors.Is(err, errInjectedIO) {
		t.Fatalf("readFrameRaw error = %v, want wrapped cause", err)
	}
	_ = fmt.Sprintf("%v", err)
}
