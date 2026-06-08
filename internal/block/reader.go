package block

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

var ErrSHA256Mismatch = errors.New("block: SHA-256 mismatch")

func ReadDocument(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	return ReadDocumentTwoPass(blkPath, entry)
}

func ReadDocumentTwoPass(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	if err := verifyPass(blkPath, entry); err != nil {
		return nil, err
	}
	return streamPass(blkPath, entry)
}

func ReadDocumentFrames(blkPath string, entry IndexEntry) ([][]byte, error) {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open %s for frame read: %w", blkPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		return nil, fmt.Errorf("block: seek for frame read: %w", err)
	}

	frames := make([][]byte, 0, entry.FrameCount)
	for i := range int(entry.FrameCount) {
		_, payload, err := ReadFrame(f)
		if err != nil {
			return nil, fmt.Errorf("block: read stored frame %d: %w", i, err)
		}
		frames = append(frames, payload)
	}
	return frames, nil
}

func verifyPass(blkPath string, entry IndexEntry) error {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return fmt.Errorf("block: open %s: %w", blkPath, err)
	}
	defer func() { _ = f.Close() }() // read-only verify pass; close error is non-critical

	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		return fmt.Errorf("block: seek: %w", err)
	}

	hasher := sha256.New()
	for i := range int(entry.FrameCount) {
		_, payload, err := ReadFrame(f)
		if err != nil {
			return fmt.Errorf("block: verify frame %d: %w", i, err)
		}
		hasher.Write(payload)
	}

	var emptyDigest [32]byte
	if entry.SHA256 != emptyDigest {
		var gotDigest [32]byte
		copy(gotDigest[:], hasher.Sum(nil))
		if gotDigest != entry.SHA256 {
			return fmt.Errorf("%w: document integrity check failed", ErrSHA256Mismatch)
		}
	}

	return nil
}

func streamPass(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	frames, err := ReadDocumentFrames(blkPath, entry)
	if err != nil {
		return nil, err
	}
	var combined []byte
	for _, payload := range frames {
		combined = append(combined, payload...)
	}
	return io.NopCloser(bytes.NewReader(combined)), nil
}
