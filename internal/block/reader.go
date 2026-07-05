package block

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
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

func ReadDocumentTwoPass(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	if len(entry.EncryptionEnvelope) > 0 {
		return nil, fmt.Errorf("%w: use ReadDocumentFrames and decrypt", ErrEncryptedEntry)
	}
	if err := verifyPass(blkPath, entry); err != nil {
		return nil, err
	}
	return streamPass(blkPath, entry)
}

func ReadDocumentFramesFromBlock(blkPath string, shardID, blockID uint64, entry IndexEntry) ([][]byte, error) {
	if err := VerifyHeader(blkPath, shardID, blockID); err != nil {
		return nil, err
	}
	return ReadDocumentFrames(blkPath, entry)
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

func verifyPass(blkPath string, entry IndexEntry) error {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return fmt.Errorf("block: open %s: %w", blkPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		return fmt.Errorf("block: seek: %w", err)
	}

	hasher := sha256.New()
	for i := range int(entry.FrameCount) {
		hdr, payload, err := ReadFrame(f)
		if err != nil {
			return fmt.Errorf("block: verify frame %d: %w", i, err)
		}
		if err := validateReadFrameSequence(hdr, uint32(i)); err != nil {
			return fmt.Errorf("block: verify frame %d: %w", i, err)
		}
		hasher.Write(payload)
	}

	if isZeroSHA256(entry.SHA256) {
		return fmt.Errorf("%w: document integrity check missing SHA-256", ErrSHA256Mismatch)
	}
	var gotDigest [32]byte
	copy(gotDigest[:], hasher.Sum(nil))
	if gotDigest != entry.SHA256 {
		return fmt.Errorf("%w: document integrity check failed", ErrSHA256Mismatch)
	}

	return nil
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

func streamPass(blkPath string, entry IndexEntry) (io.ReadCloser, error) {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open %s for frame read: %w", blkPath, err)
	}
	if _, err := f.Seek(entry.FirstFrameOff, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: seek for frame read: %w", err)
	}
	return &frameStreamReader{f: f, entry: entry, hasher: sha256.New()}, nil
}

// frameStreamReader streams one Frame payload at a time so the read path
// never buffers a whole Document, and re-hashes what it serves: the stream
// pass re-reads the file after the verify pass, so the bytes handed to the
// client must independently match the index SHA-256.
type frameStreamReader struct {
	f        *os.File
	entry    IndexEntry
	hasher   hash.Hash
	buf      []byte
	frameIdx uint32
	err      error
}

func (r *frameStreamReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	for len(r.buf) == 0 {
		if r.frameIdx == r.entry.FrameCount {
			r.err = r.finish()
			return 0, r.err
		}
		if err := r.nextFrame(); err != nil {
			r.err = err
			return 0, r.err
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *frameStreamReader) nextFrame() error {
	hdr, payload, err := ReadFrame(r.f)
	if err != nil {
		return fmt.Errorf("block: read stored frame %d: %w", r.frameIdx, err)
	}
	if err := validateReadFrameSequence(hdr, r.frameIdx); err != nil {
		return fmt.Errorf("block: read stored frame %d: %w", r.frameIdx, err)
	}
	r.hasher.Write(payload)
	r.buf = payload
	r.frameIdx++
	return nil
}

func (r *frameStreamReader) finish() error {
	var gotDigest [32]byte
	copy(gotDigest[:], r.hasher.Sum(nil))
	if gotDigest != r.entry.SHA256 {
		return fmt.Errorf("%w: document changed between verify and stream passes", ErrSHA256Mismatch)
	}
	return io.EOF
}

func (r *frameStreamReader) Close() error {
	return r.f.Close()
}
