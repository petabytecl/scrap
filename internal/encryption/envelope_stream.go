package encryption

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
)

// FrameSource yields ciphertext frame payloads in order and returns io.EOF
// after the last frame. Sources that also implement io.Closer are closed by
// the readers built on top of them.
type FrameSource interface {
	NextFrame() ([]byte, error)
}

// EncryptedDocumentInfo describes a fully streamed encrypted Document. It is
// only available after the encryptor has produced its last frame.
type EncryptedDocumentInfo struct {
	Envelope        []byte
	PlaintextSHA256 [sha256.Size]byte
	PlaintextSize   int64
	CiphertextSize  int64
	FrameCount      uint32
}

// DocumentEncryptor seals a Document body into ciphertext frames one at a
// time so callers never hold more than one frame of plaintext or ciphertext.
// It reads one frame ahead of what it returns so the final frame is known
// when it is produced.
type DocumentEncryptor struct {
	aead        cipher.AEAD
	dataKey     []byte
	body        io.Reader
	identity    DocumentIdentity
	noncePrefix []byte
	hasher      hash.Hash

	transitMount string
	transitKey   string
	keyVersion   int
	wrappedKey   string

	cur            []byte
	next           []byte
	curN           int
	curErr         error
	aheadErr       error
	primed         bool
	done           bool
	err            error
	frameSeq       uint32
	plaintextSize  int64
	ciphertextSize int64
}

func NewDocumentEncryptor(ctx context.Context, cfg DocumentConfig, identity DocumentIdentity, body io.Reader) (*DocumentEncryptor, error) {
	if cfg.Transit == nil {
		return nil, fmt.Errorf("encryption transit is required: %w", ErrInvalidEnvelope)
	}
	cfg = normalizeDocumentConfig(cfg)
	dataKey, err := cfg.Transit.GenerateDataKey(ctx, GenerateDataKeyRequest{
		Context: DocumentKeyContext(identity),
		Bits:    dataKeyBits256,
	})
	if err != nil {
		return nil, err
	}
	aead, err := newPayloadAEAD(dataKey.Plaintext)
	if err != nil {
		zeroBytes(dataKey.Plaintext)
		return nil, err
	}
	noncePrefix := make([]byte, noncePrefixSize)
	if _, err := rand.Read(noncePrefix); err != nil {
		zeroBytes(dataKey.Plaintext)
		return nil, fmt.Errorf("encryption nonce generation failed: %w", err)
	}
	plainFrameSize := defaultCiphertextFramePayload - aead.Overhead()
	if plainFrameSize <= 0 {
		zeroBytes(dataKey.Plaintext)
		return nil, fmt.Errorf("%w: invalid AEAD overhead", ErrInvalidEnvelope)
	}

	return &DocumentEncryptor{
		aead:         aead,
		dataKey:      dataKey.Plaintext,
		body:         body,
		identity:     identity,
		noncePrefix:  noncePrefix,
		hasher:       sha256.New(),
		transitMount: cfg.TransitMount,
		transitKey:   cfg.TransitKey,
		keyVersion:   dataKey.Version,
		wrappedKey:   dataKey.WrappedKey,
		cur:          make([]byte, plainFrameSize),
		next:         make([]byte, plainFrameSize),
	}, nil
}

// NextFrame returns the next sealed ciphertext frame and whether it is the
// last one. It returns io.EOF once the body is exhausted; a body with no
// bytes at all yields io.EOF before any frame.
func (e *DocumentEncryptor) NextFrame() ([]byte, bool, error) {
	if e.err != nil {
		return nil, false, e.err
	}
	if e.done {
		e.err = io.EOF
		return nil, false, io.EOF
	}
	if !e.primed {
		e.curN, e.curErr = io.ReadFull(e.body, e.cur)
		e.primed = true
	}
	if e.curErr != nil && !isStreamBodyEOF(e.curErr) {
		e.err = fmt.Errorf("encryption read document: %w", e.curErr)
		return nil, false, e.err
	}
	if e.curN == 0 {
		e.done = true
		e.err = io.EOF
		return nil, false, io.EOF
	}

	nextN, err := e.readAhead()
	if err != nil {
		e.err = err
		return nil, false, e.err
	}

	plaintext := e.cur[:e.curN]
	e.hasher.Write(plaintext)
	e.plaintextSize += int64(len(plaintext))
	frame := e.aead.Seal(nil, frameNonce(e.noncePrefix, e.frameSeq), plaintext, frameAAD(e.identity, e.frameSeq))
	e.ciphertextSize += int64(len(frame))
	e.frameSeq++

	e.cur, e.next = e.next, e.cur
	e.curN, e.curErr = nextN, e.aheadErr
	last := nextN == 0
	if last {
		e.done = true
	}
	return frame, last, nil
}

// readAhead fills the spare buffer with the next chunk so the frame being
// produced knows whether it is the last one.
func (e *DocumentEncryptor) readAhead() (int, error) {
	// NextFrame already rejected non-EOF read errors, so a pending curErr can
	// only mean the body ended while filling the current chunk.
	if isStreamBodyEOF(e.curErr) {
		e.aheadErr = e.curErr
		return 0, nil
	}
	n, err := io.ReadFull(e.body, e.next)
	if err != nil && !isStreamBodyEOF(err) {
		return 0, fmt.Errorf("encryption read document: %w", err)
	}
	e.aheadErr = err
	return n, nil
}

// Finalize returns the envelope and digests once every frame was produced.
func (e *DocumentEncryptor) Finalize() (EncryptedDocumentInfo, error) {
	if !e.done {
		return EncryptedDocumentInfo{}, fmt.Errorf("%w: encryptor finalized before last frame", ErrInvalidEnvelope)
	}
	var digest [sha256.Size]byte
	copy(digest[:], e.hasher.Sum(nil))
	envelope, err := MarshalEnvelope(Envelope{
		Version:          EnvelopeVersion,
		TransitMount:     e.transitMount,
		TransitKey:       e.transitKey,
		KeyVersion:       e.keyVersion,
		WrappedDataKey:   e.wrappedKey,
		PayloadAlgorithm: PayloadAlgorithmAES256GCM,
		NoncePrefix:      e.noncePrefix,
		PlaintextSHA256:  digest[:],
		PlaintextLength:  e.plaintextSize,
		CiphertextLength: e.ciphertextSize,
	})
	if err != nil {
		return EncryptedDocumentInfo{}, err
	}
	return EncryptedDocumentInfo{
		Envelope:        envelope,
		PlaintextSHA256: digest,
		PlaintextSize:   e.plaintextSize,
		CiphertextSize:  e.ciphertextSize,
		FrameCount:      e.frameSeq,
	}, nil
}

// Close zeroes the data key. It is safe to call multiple times.
func (e *DocumentEncryptor) Close() {
	zeroBytes(e.dataKey)
}

func isStreamBodyEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// DocumentDecryptor holds one unwrapped data key for streaming decryption so
// a verify pass and a serve pass share a single Transit round trip.
type DocumentDecryptor struct {
	aead        cipher.AEAD
	dataKey     []byte
	identity    DocumentIdentity
	envelope    Envelope
	expectedSHA [sha256.Size]byte
}

func NewDocumentDecryptor(
	ctx context.Context,
	transit Transit,
	identity DocumentIdentity,
	envelopeBytes []byte,
	expectedSHA [sha256.Size]byte,
	expectedSize int64,
) (*DocumentDecryptor, error) {
	if transit == nil {
		return nil, fmt.Errorf("encryption transit is required: %w", ErrUnavailable)
	}
	envelope, err := ParseEnvelope(envelopeBytes)
	if err != nil {
		return nil, err
	}
	if expectedSize != envelope.PlaintextLength {
		return nil, fmt.Errorf("%w: plaintext length metadata mismatch", ErrIntegrity)
	}
	if !bytes.Equal(expectedSHA[:], envelope.PlaintextSHA256) {
		return nil, fmt.Errorf("%w: plaintext SHA-256 metadata mismatch", ErrIntegrity)
	}

	unwrapped, err := transit.UnwrapDataKey(ctx, UnwrapDataKeyRequest{
		WrappedKey: envelope.WrappedDataKey,
		Context:    DocumentKeyContext(identity),
	})
	if err != nil {
		return nil, err
	}
	aead, err := newPayloadAEAD(unwrapped.Plaintext)
	if err != nil {
		zeroBytes(unwrapped.Plaintext)
		return nil, err
	}
	return &DocumentDecryptor{
		aead:        aead,
		dataKey:     unwrapped.Plaintext,
		identity:    identity,
		envelope:    envelope,
		expectedSHA: expectedSHA,
	}, nil
}

// Verify streams every frame through authenticated decryption and checks the
// whole-document digest and lengths without materializing the plaintext.
func (d *DocumentDecryptor) Verify(source FrameSource) error {
	reader := d.Reader(source)
	_, err := io.Copy(io.Discard, reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Reader returns a streaming plaintext reader over the frames in source.
// Every frame is authenticated before any of its bytes are served, and the
// whole-document digest and lengths are re-checked before EOF is returned.
// Closing the reader closes the source when it implements io.Closer; it does
// not zero the data key — call Close on the decryptor for that.
func (d *DocumentDecryptor) Reader(source FrameSource) io.ReadCloser {
	return &documentDecryptReader{decryptor: d, source: source, hasher: sha256.New()}
}

// Close zeroes the data key. It is safe to call multiple times.
func (d *DocumentDecryptor) Close() {
	zeroBytes(d.dataKey)
}

type documentDecryptReader struct {
	decryptor      *DocumentDecryptor
	source         FrameSource
	hasher         hash.Hash
	buf            []byte
	frameSeq       uint32
	plaintextSize  int64
	ciphertextSize int64
	err            error
}

func (r *documentDecryptReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	for len(r.buf) == 0 {
		frame, err := r.source.NextFrame()
		if errors.Is(err, io.EOF) {
			r.err = r.finish()
			return 0, r.err
		}
		if err != nil {
			r.err = err
			return 0, r.err
		}
		if err := r.openFrame(frame); err != nil {
			r.err = err
			return 0, r.err
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *documentDecryptReader) openFrame(frame []byte) error {
	d := r.decryptor
	if len(frame) < d.aead.Overhead() {
		return fmt.Errorf("%w: ciphertext frame %d too short", ErrIntegrity, r.frameSeq)
	}
	opened, err := d.aead.Open(nil, frameNonce(d.envelope.NoncePrefix, r.frameSeq), frame, frameAAD(d.identity, r.frameSeq))
	if err != nil {
		return fmt.Errorf("%w: decrypt frame %d", ErrIntegrity, r.frameSeq)
	}
	r.ciphertextSize += int64(len(frame))
	r.plaintextSize += int64(len(opened))
	r.hasher.Write(opened)
	r.frameSeq++
	r.buf = opened
	return nil
}

func (r *documentDecryptReader) finish() error {
	envelope := r.decryptor.envelope
	if r.ciphertextSize != envelope.CiphertextLength {
		return fmt.Errorf("%w: ciphertext length mismatch", ErrIntegrity)
	}
	if r.plaintextSize != envelope.PlaintextLength {
		return fmt.Errorf("%w: plaintext length mismatch", ErrIntegrity)
	}
	var digest [sha256.Size]byte
	copy(digest[:], r.hasher.Sum(nil))
	if !bytes.Equal(digest[:], r.decryptor.expectedSHA[:]) {
		return fmt.Errorf("%w: plaintext SHA-256 mismatch", ErrIntegrity)
	}
	return io.EOF
}

func (r *documentDecryptReader) Close() error {
	if closer, ok := r.source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// sliceFrameSource adapts in-memory frames to FrameSource.
type sliceFrameSource struct {
	frames [][]byte
	next   int
}

func NewSliceFrameSource(frames [][]byte) FrameSource {
	return &sliceFrameSource{frames: frames}
}

func (s *sliceFrameSource) NextFrame() ([]byte, error) {
	if s.next >= len(s.frames) {
		return nil, io.EOF
	}
	frame := s.frames[s.next]
	s.next++
	return frame, nil
}
