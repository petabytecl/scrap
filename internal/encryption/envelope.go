package encryption

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	DefaultTransitMountPath = "transit"
	DefaultTransitKeyName   = "scrap-documents"

	EnvelopeVersion = 1

	PayloadAlgorithmAES256GCM = "AES-256-GCM"

	defaultCiphertextFramePayload = 64 * 1024
	noncePrefixSize               = 8
	aesGCMNonceSize               = 12
	aes256KeySize                 = 32
)

var (
	ErrInvalidEnvelope = errors.New("encryption invalid envelope")
	ErrIntegrity       = errors.New("encryption integrity failed")
)

type DocumentConfig struct {
	Transit      Transit
	TransitMount string
	TransitKey   string
}

type DocumentIdentity struct {
	TransactionID string
	DocumentName  string
}

type EncryptedDocument struct {
	Envelope        []byte
	Frames          [][]byte
	PlaintextSHA256 [sha256.Size]byte
	PlaintextSize   int64
	CiphertextSize  int64
}

type Envelope struct {
	Version          int    `json:"version"`
	TransitMount     string `json:"transit_mount"`
	TransitKey       string `json:"transit_key"`
	KeyVersion       int    `json:"key_version"`
	WrappedDataKey   string `json:"wrapped_data_key"`
	PayloadAlgorithm string `json:"payload_algorithm"`
	NoncePrefix      []byte `json:"nonce_prefix"`
	PlaintextSHA256  []byte `json:"plaintext_sha256"`
	PlaintextLength  int64  `json:"plaintext_length"`
	CiphertextLength int64  `json:"ciphertext_length"`
}

func EncryptDocument(ctx context.Context, cfg DocumentConfig, identity DocumentIdentity, body io.Reader) (EncryptedDocument, error) {
	if cfg.Transit == nil {
		return EncryptedDocument{}, fmt.Errorf("encryption transit is required: %w", ErrInvalidEnvelope)
	}
	cfg = normalizeDocumentConfig(cfg)
	keyContext := DocumentKeyContext(identity)
	dataKey, err := cfg.Transit.GenerateDataKey(ctx, GenerateDataKeyRequest{
		Context: keyContext,
		Bits:    dataKeyBits256,
	})
	if err != nil {
		return EncryptedDocument{}, err
	}
	defer zeroBytes(dataKey.Plaintext)

	aead, err := newPayloadAEAD(dataKey.Plaintext)
	if err != nil {
		return EncryptedDocument{}, err
	}

	noncePrefix := make([]byte, noncePrefixSize)
	if _, err := rand.Read(noncePrefix); err != nil {
		return EncryptedDocument{}, fmt.Errorf("encryption nonce generation failed: %w", err)
	}

	frames, plaintextSHA, plaintextSize, ciphertextSize, err := encryptFrames(aead, noncePrefix, identity, body)
	if err != nil {
		return EncryptedDocument{}, err
	}
	envelope, err := MarshalEnvelope(Envelope{
		Version:          EnvelopeVersion,
		TransitMount:     cfg.TransitMount,
		TransitKey:       cfg.TransitKey,
		KeyVersion:       dataKey.Version,
		WrappedDataKey:   dataKey.WrappedKey,
		PayloadAlgorithm: PayloadAlgorithmAES256GCM,
		NoncePrefix:      noncePrefix,
		PlaintextSHA256:  plaintextSHA[:],
		PlaintextLength:  plaintextSize,
		CiphertextLength: ciphertextSize,
	})
	if err != nil {
		return EncryptedDocument{}, err
	}

	return EncryptedDocument{
		Envelope:        envelope,
		Frames:          cloneFrames(frames),
		PlaintextSHA256: plaintextSHA,
		PlaintextSize:   plaintextSize,
		CiphertextSize:  ciphertextSize,
	}, nil
}

func DecryptDocument(
	ctx context.Context,
	transit Transit,
	identity DocumentIdentity,
	envelopeBytes []byte,
	frames [][]byte,
	expectedSHA [sha256.Size]byte,
	expectedSize int64,
) ([]byte, error) {
	if transit == nil {
		return nil, fmt.Errorf("encryption transit is required: %w", ErrUnavailable)
	}
	envelope, err := ParseEnvelope(envelopeBytes)
	if err != nil {
		return nil, err
	}
	if err := validateDecryptMetadata(envelope, frames, expectedSHA, expectedSize); err != nil {
		return nil, err
	}

	unwrapped, err := transit.UnwrapDataKey(ctx, UnwrapDataKeyRequest{
		WrappedKey: envelope.WrappedDataKey,
		Context:    DocumentKeyContext(identity),
	})
	if err != nil {
		return nil, err
	}
	defer zeroBytes(unwrapped.Plaintext)

	aead, err := newPayloadAEAD(unwrapped.Plaintext)
	if err != nil {
		return nil, err
	}

	plaintext, err := decryptFrames(aead, envelope.NoncePrefix, identity, frames)
	if err != nil {
		return nil, err
	}
	if err := verifyPlaintextIntegrity(plaintext, envelope, expectedSHA); err != nil {
		return nil, err
	}
	return plaintext, nil
}

func MarshalEnvelope(envelope Envelope) ([]byte, error) {
	envelope = normalizeEnvelope(envelope)
	if err := validateEnvelope(envelope); err != nil {
		return nil, err
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %w", ErrInvalidEnvelope, err)
	}
	return data, nil
}

func ParseEnvelope(data []byte) (Envelope, error) {
	if len(data) == 0 {
		return Envelope{}, fmt.Errorf("%w: missing envelope", ErrInvalidEnvelope)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("%w: decode: %w", ErrInvalidEnvelope, err)
	}
	envelope = normalizeEnvelope(envelope)
	if err := validateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	envelope.NoncePrefix = cloneBytes(envelope.NoncePrefix)
	envelope.PlaintextSHA256 = cloneBytes(envelope.PlaintextSHA256)
	return envelope, nil
}

func DocumentKeyContext(identity DocumentIdentity) []byte {
	return appendIdentity(nil, "scrap:v1:document-key", identity, 0)
}

func normalizeDocumentConfig(cfg DocumentConfig) DocumentConfig {
	cfg.TransitMount = normalizeTransitPath(cfg.TransitMount, DefaultTransitMountPath)
	cfg.TransitKey = normalizeTransitPath(cfg.TransitKey, DefaultTransitKeyName)
	return cfg
}

func normalizeEnvelope(envelope Envelope) Envelope {
	envelope.TransitMount = strings.Trim(envelope.TransitMount, "/ \t\r\n")
	envelope.TransitKey = strings.Trim(envelope.TransitKey, "/ \t\r\n")
	envelope.PayloadAlgorithm = strings.TrimSpace(envelope.PayloadAlgorithm)
	return envelope
}

func validateEnvelope(envelope Envelope) error {
	if err := validateEnvelopeHeader(envelope); err != nil {
		return err
	}
	return validateEnvelopePayload(envelope)
}

func validateEnvelopeHeader(envelope Envelope) error {
	switch {
	case envelope.Version != EnvelopeVersion:
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidEnvelope, envelope.Version)
	case envelope.TransitMount == "":
		return fmt.Errorf("%w: transit mount is required", ErrInvalidEnvelope)
	case envelope.TransitKey == "":
		return fmt.Errorf("%w: transit key is required", ErrInvalidEnvelope)
	case envelope.KeyVersion <= 0:
		return fmt.Errorf("%w: key version is required", ErrInvalidEnvelope)
	case strings.TrimSpace(envelope.WrappedDataKey) == "":
		return fmt.Errorf("%w: wrapped data key is required", ErrInvalidEnvelope)
	case envelope.PayloadAlgorithm != PayloadAlgorithmAES256GCM:
		return fmt.Errorf("%w: unsupported payload algorithm %q", ErrInvalidEnvelope, envelope.PayloadAlgorithm)
	default:
		return nil
	}
}

func validateEnvelopePayload(envelope Envelope) error {
	switch {
	case len(envelope.NoncePrefix) != noncePrefixSize:
		return fmt.Errorf("%w: nonce prefix length %d", ErrInvalidEnvelope, len(envelope.NoncePrefix))
	case len(envelope.PlaintextSHA256) != sha256.Size:
		return fmt.Errorf("%w: plaintext SHA-256 length %d", ErrInvalidEnvelope, len(envelope.PlaintextSHA256))
	case envelope.PlaintextLength < 0:
		return fmt.Errorf("%w: negative plaintext length", ErrInvalidEnvelope)
	case envelope.CiphertextLength < 0:
		return fmt.Errorf("%w: negative ciphertext length", ErrInvalidEnvelope)
	default:
		return nil
	}
}

func validateDecryptMetadata(envelope Envelope, frames [][]byte, expectedSHA [sha256.Size]byte, expectedSize int64) error {
	switch {
	case expectedSize != envelope.PlaintextLength:
		return fmt.Errorf("%w: plaintext length metadata mismatch", ErrIntegrity)
	case !bytes.Equal(expectedSHA[:], envelope.PlaintextSHA256):
		return fmt.Errorf("%w: plaintext SHA-256 metadata mismatch", ErrIntegrity)
	case framePayloadBytes(frames) != envelope.CiphertextLength:
		return fmt.Errorf("%w: ciphertext length mismatch", ErrIntegrity)
	default:
		return nil
	}
}

func verifyPlaintextIntegrity(plaintext []byte, envelope Envelope, expectedSHA [sha256.Size]byte) error {
	if int64(len(plaintext)) != envelope.PlaintextLength {
		return fmt.Errorf("%w: plaintext length mismatch", ErrIntegrity)
	}
	gotSHA := sha256.Sum256(plaintext)
	if !bytes.Equal(gotSHA[:], expectedSHA[:]) {
		return fmt.Errorf("%w: plaintext SHA-256 mismatch", ErrIntegrity)
	}
	return nil
}

func encryptFrames(aead cipher.AEAD, noncePrefix []byte, identity DocumentIdentity, body io.Reader) ([][]byte, [sha256.Size]byte, int64, int64, error) {
	plainFrameSize := defaultCiphertextFramePayload - aead.Overhead()
	if plainFrameSize <= 0 {
		return nil, [sha256.Size]byte{}, 0, 0, fmt.Errorf("%w: invalid AEAD overhead", ErrInvalidEnvelope)
	}

	hasher := sha256.New()
	buf := make([]byte, plainFrameSize)
	var frames [][]byte
	var plaintextSize int64
	var ciphertextSize int64
	var frameSeq uint32

	for {
		n, readErr := io.ReadFull(body, buf)
		if n > 0 {
			plaintext := buf[:n]
			hasher.Write(plaintext)
			plaintextSize += int64(n)

			ciphertext := aead.Seal(nil, frameNonce(noncePrefix, frameSeq), plaintext, frameAAD(identity, frameSeq))
			frames = append(frames, ciphertext)
			ciphertextSize += int64(len(ciphertext))
			frameSeq++
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			return nil, [sha256.Size]byte{}, 0, 0, fmt.Errorf("encryption read document: %w", readErr)
		}
	}

	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return frames, digest, plaintextSize, ciphertextSize, nil
}

func decryptFrames(aead cipher.AEAD, noncePrefix []byte, identity DocumentIdentity, frames [][]byte) ([]byte, error) {
	plaintext := make([]byte, 0, max(0, int(framePayloadBytes(frames))-aead.Overhead()*len(frames)))
	for i, frame := range frames {
		if len(frame) < aead.Overhead() {
			return nil, fmt.Errorf("%w: ciphertext frame %d too short", ErrIntegrity, i)
		}
		opened, err := aead.Open(nil, frameNonce(noncePrefix, uint32(i)), frame, frameAAD(identity, uint32(i)))
		if err != nil {
			return nil, fmt.Errorf("%w: decrypt frame %d", ErrIntegrity, i)
		}
		plaintext = append(plaintext, opened...)
	}
	return plaintext, nil
}

func newPayloadAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != aes256KeySize {
		return nil, fmt.Errorf("%w: data key length %d", ErrInvalidEnvelope, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid data key: %w", ErrInvalidEnvelope, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid data key mode", ErrInvalidEnvelope)
	}
	if aead.NonceSize() != aesGCMNonceSize {
		return nil, fmt.Errorf("%w: unsupported nonce size %d", ErrInvalidEnvelope, aead.NonceSize())
	}
	return aead, nil
}

func frameNonce(prefix []byte, frameSeq uint32) []byte {
	var nonce [aesGCMNonceSize]byte
	copy(nonce[:noncePrefixSize], prefix)
	binary.BigEndian.PutUint32(nonce[noncePrefixSize:], frameSeq)
	return nonce[:]
}

func frameAAD(identity DocumentIdentity, frameSeq uint32) []byte {
	return appendIdentity(nil, "scrap:v1:document-frame", identity, frameSeq)
}

func appendIdentity(dst []byte, domain string, identity DocumentIdentity, frameSeq uint32) []byte {
	dst = appendLen(dst, []byte(domain))
	dst = appendLen(dst, []byte(identity.TransactionID))
	dst = appendLen(dst, []byte(identity.DocumentName))
	var seq [4]byte
	binary.BigEndian.PutUint32(seq[:], frameSeq)
	return append(dst, seq[:]...)
}

func appendLen(dst, value []byte) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(value))) //nolint:gosec // identity strings are bounded by request validation.
	dst = append(dst, lenBuf[:]...)
	return append(dst, value...)
}

func framePayloadBytes(frames [][]byte) int64 {
	var total int64
	for _, frame := range frames {
		total += int64(len(frame))
	}
	return total
}

func cloneFrames(frames [][]byte) [][]byte {
	if len(frames) == 0 {
		return nil
	}
	out := make([][]byte, len(frames))
	for i, frame := range frames {
		out[i] = cloneBytes(frame)
	}
	return out
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func normalizeTransitPath(value, fallback string) string {
	value = strings.Trim(value, "/ \t\r\n")
	if value == "" {
		return fallback
	}
	return value
}
