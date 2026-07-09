package block

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrSHA256Mismatch = errors.New("block: SHA-256 mismatch")
	ErrFrameSequence  = errors.New("block: frame sequence corrupt")
	ErrEncryptedEntry = errors.New("block: entry is encrypted")
)

// ReadDocument reads a plaintext Document via the two-pass path. Encrypted
// entries are rejected: their index SHA-256 covers the plaintext while the
// stored Frames hold ciphertext, so callers must use ReadDocumentFrames and
// decrypt.
func ReadDocument(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	return ReadDocumentTwoPass(blkPath, entry)
}

func ReadDocumentFromBlock(blkPath string, shardID, blockID uint64, entry IndexEntry) (io.ReadCloser, error) {
	if err := VerifyHeader(blkPath, shardID, blockID); err != nil {
		return nil, err
	}
	return ReadDocumentTwoPass(blkPath, entry)
}

// ReadDocumentTwoPass verifies Frame CRC and Document SHA-256 into a private
// spool, then returns a reader over that verified snapshot (M-01 / ADR 0002).
// No payload bytes are exposed until verification completes.
func ReadDocumentTwoPass(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	if len(entry.EncryptionEnvelope) > 0 {
		return nil, fmt.Errorf("%w: use ReadDocumentFrames and decrypt", ErrEncryptedEntry)
	}
	payload, err := verifyAndSpool(blkPath, entry)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func ReadDocumentFramesFromBlock(blkPath string, shardID, blockID uint64, entry IndexEntry) ([][]byte, error) {
	if err := VerifyHeader(blkPath, shardID, blockID); err != nil {
		return nil, err
	}
	return ReadDocumentFrames(blkPath, entry)
}

// OpenDocumentFrameSource streams one Document's stored Frame payloads so
// encrypted reads never buffer a whole Document. The caller must Close it.
func OpenDocumentFrameSource(blkPath string, shardID, blockID uint64, entry IndexEntry) (*StoredFrameSource, error) {
	if err := VerifyHeader(blkPath, shardID, blockID); err != nil {
		return nil, err
	}
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open %s for frame read: %w", blkPath, err)
	}
	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: seek for frame read: %w", err)
	}
	return &StoredFrameSource{f: f, entry: entry}, nil
}

// StoredFrameSource yields one stored Frame payload at a time and returns
// io.EOF after the last Frame of the Document.
type StoredFrameSource struct {
	f        *os.File
	entry    IndexEntry
	frameIdx uint32
}

// NextFrame returns the next stored Frame payload after validating its
// header sequence, or io.EOF past the Document's last Frame.
func (s *StoredFrameSource) NextFrame() ([]byte, error) {
	if s.frameIdx == s.entry.FrameCount {
		return nil, io.EOF
	}
	hdr, payload, err := ReadFrame(s.f)
	if err != nil {
		return nil, fmt.Errorf("block: read stored frame %d: %w", s.frameIdx, err)
	}
	if err := validateReadFrameSequence(hdr, s.frameIdx); err != nil {
		return nil, fmt.Errorf("block: read stored frame %d: %w", s.frameIdx, err)
	}
	s.frameIdx++
	return payload, nil
}

// Close releases the underlying Block file handle.
func (s *StoredFrameSource) Close() error {
	return s.f.Close()
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
		hdr, payload, err := ReadFrame(f)
		if err != nil {
			return nil, fmt.Errorf("block: read stored frame %d: %w", i, err)
		}
		if err := validateReadFrameSequence(hdr, uint32(i)); err != nil {
			return nil, fmt.Errorf("block: read stored frame %d: %w", i, err)
		}
		frames = append(frames, payload)
	}
	return frames, nil
}

// verifyAndSpool reads every Frame for the Document, verifies sequence and
// SHA-256, and returns an immutable in-memory snapshot. Callers must not
// expose any bytes until this returns successfully (M-01).
func verifyAndSpool(blkPath string, entry IndexEntry) ([]byte, error) {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open %s: %w", blkPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		return nil, fmt.Errorf("block: seek: %w", err)
	}

	if isZeroSHA256(entry.SHA256) {
		return nil, fmt.Errorf("%w: document integrity check missing SHA-256", ErrSHA256Mismatch)
	}

	hasher := sha256.New()
	spool := make([]byte, 0, entry.TotalBytes)
	for i := range int(entry.FrameCount) {
		hdr, payload, err := ReadFrame(f)
		if err != nil {
			return nil, fmt.Errorf("block: verify frame %d: %w", i, err)
		}
		if err := validateReadFrameSequence(hdr, uint32(i)); err != nil {
			return nil, fmt.Errorf("block: verify frame %d: %w", i, err)
		}
		hasher.Write(payload)
		spool = append(spool, payload...)
	}

	var gotDigest [32]byte
	copy(gotDigest[:], hasher.Sum(nil))
	if gotDigest != entry.SHA256 {
		return nil, fmt.Errorf("%w: document integrity check failed", ErrSHA256Mismatch)
	}
	return spool, nil
}

func isZeroSHA256(digest [32]byte) bool {
	return digest == [32]byte{}
}

func validateReadFrameSequence(hdr FrameHeader, frameSeq uint32) error {
	if hdr.FrameSeq != frameSeq {
		return fmt.Errorf("%w: frame_seq %d want %d", ErrFrameSequence, hdr.FrameSeq, frameSeq)
	}
	return nil
}
