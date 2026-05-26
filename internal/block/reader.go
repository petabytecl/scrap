package block

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrSHA256Mismatch = errors.New("block: SHA-256 mismatch")
)

func ReadDocument(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	f, err := os.Open(blkPath)
	if err != nil {
		return nil, fmt.Errorf("block: open %s: %w", blkPath, err)
	}

	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		f.Close()
		return nil, fmt.Errorf("block: seek to frame: %w", err)
	}

	hasher := sha256.New()
	var allPayloads [][]byte

	for i := range int(entry.FrameCount) {
		_, payload, err := ReadFrame(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("block: read frame %d: %w", i, err)
		}
		hasher.Write(payload)
		allPayloads = append(allPayloads, payload)
	}

	f.Close()

	gotChecksum := hex.EncodeToString(hasher.Sum(nil))
	if entry.SHA256Checksum != "" && gotChecksum != entry.SHA256Checksum {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrSHA256Mismatch, gotChecksum, entry.SHA256Checksum)
	}

	var combined []byte
	for _, p := range allPayloads {
		combined = append(combined, p...)
	}

	return io.NopCloser(bytes.NewReader(combined)), nil
}
