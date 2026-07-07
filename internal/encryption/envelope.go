package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
	aesGCMTagSize                 = 16
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
